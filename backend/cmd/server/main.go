package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/entkube/entkube/internal/api"
	authpkg "github.com/entkube/entkube/internal/auth"
	"github.com/entkube/entkube/internal/config"
	"github.com/entkube/entkube/internal/db"
	"github.com/entkube/entkube/internal/ha"
	"github.com/entkube/entkube/internal/services"
	vaultpkg "github.com/entkube/entkube/internal/vault"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ── Database ────────────────────────────────────────────────
	slog.Info("running migrations")
	if err := db.Migrate(ctx, cfg.DatabaseURL); err != nil {
		slog.Error("migrate", "err", err)
		os.Exit(1)
	}

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("connect db", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	// ── Vault ───────────────────────────────────────────────────
	vaultSvc, err := vaultpkg.New(cfg.VaultRootKey)
	if err != nil {
		slog.Error("init vault", "err", err)
		os.Exit(1)
	}

	// ── HA node identity ────────────────────────────────────────
	keyPair, err := ha.LoadOrGenerateKeyPair(cfg.NodeKeyFile)
	if err != nil {
		slog.Error("load node key", "err", err)
		os.Exit(1)
	}

	pubKeyBytes, err := keyPair.PublicKeyBytes()
	if err != nil {
		slog.Error("get public key", "err", err)
		os.Exit(1)
	}
	selfID := deterministicUUID(pubKeyBytes)
	slog.Info("node identity", "id", selfID, "address", cfg.NodeAddress)

	nodeMgr := ha.NewNodeManager(pool, selfID, keyPair, cfg.NodeAddress)
	if err := nodeMgr.Register(ctx); err != nil {
		slog.Error("register node", "err", err)
		os.Exit(1)
	}

	// ── Vault sync service ──────────────────────────────────────
	vaultSyncer := vaultpkg.NewSyncService(pool, vaultSvc, selfID, nodeMgr.SharedSecret)

	// ── HA sync + heartbeat ─────────────────────────────────────
	syncMgr := ha.NewSyncManager(pool, nodeMgr, vaultSyncer, 30*time.Second)
	go syncMgr.Run(ctx)
	go runHeartbeat(ctx, nodeMgr)

	// ── Bootstrap: join configured peers ───────────────────────
	for _, peerAddr := range cfg.HAPeers {
		if err := joinPeer(ctx, nodeMgr, selfID, pubKeyBytes, peerAddr, cfg.HAJoinToken); err != nil {
			slog.Warn("join peer", "addr", peerAddr, "err", err)
		}
	}

	// ── Lease manager + background services ────────────────────
	leaseMgr := ha.NewLeaseManager(pool, selfID)

	// Background services. Each runs on exactly one node at a time.
	deploySvc := services.NewDeploymentSyncService(pool, vaultSvc, selfID)
	uptimeSvc := services.NewUptimeTrackingService(pool)
	alertSyncSvc := services.NewAlertSyncService(pool)
	routeHealthSvc := services.NewExternalRouteHealthService(pool)
	notifSvc := services.NewNotificationService(pool)
	escalationSvc := services.NewAlertEscalationService(pool, notifSvc, 15)
	gitSyncSvc := services.NewGitSyncService(pool, vaultSvc, selfID)

	go leaseMgr.RunServiceLoop(ctx, "deployment-sync",   2*time.Minute, deploySvc.Run)
	go leaseMgr.RunServiceLoop(ctx, "uptime-tracking",   5*time.Minute, uptimeSvc.Run)
	go leaseMgr.RunServiceLoop(ctx, "alert-sync",        2*time.Minute, alertSyncSvc.Run)
	go leaseMgr.RunServiceLoop(ctx, "route-health",      5*time.Minute, routeHealthSvc.Run)
	go leaseMgr.RunServiceLoop(ctx, "alert-escalation",  5*time.Minute, escalationSvc.Run)
	go leaseMgr.RunServiceLoop(ctx, "git-sync",          3*time.Minute, gitSyncSvc.Run)

	// ── Auth service ────────────────────────────────────────────
	authSvc := authpkg.New(pool, cfg.JWTSecret, selfID)

	// ── Bootstrap admin user (only if ADMIN_EMAIL/PASSWORD are set and no users exist) ──
	if cfg.AdminEmail != "" && cfg.AdminPassword != "" {
		if err := bootstrapAdmin(ctx, pool, authSvc, selfID, cfg.AdminEmail, cfg.AdminPassword); err != nil {
			slog.Warn("bootstrap admin skipped or failed", "err", err)
		}
	}

	// ── HTTP server ─────────────────────────────────────────────
	srv := api.New(
		pool, authSvc, nodeMgr, vaultSvc, selfID,
		cfg.HAJoinToken, gitSyncSvc.EnqueueSync, cfg.StaticDir, cfg.Port,
	)
	go func() {
		if err := srv.ListenAndServe(); err != nil {
			slog.Error("server error", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")
}

// ────────────────────────────────────────────────────────────────
// Background helpers
// ────────────────────────────────────────────────────────────────

func runHeartbeat(ctx context.Context, nodeMgr *ha.NodeManager) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := nodeMgr.Heartbeat(ctx); err != nil {
				slog.Warn("heartbeat failed", "err", err)
			}
		}
	}
}

func joinPeer(
	ctx context.Context,
	nodeMgr *ha.NodeManager,
	selfID uuid.UUID,
	selfPubKey []byte,
	peerAddr, joinToken string,
) error {
	req := &ha.JoinRequest{
		NodeID:    selfID,
		Address:   nodeMgr.SelfAddress(),
		PublicKey: selfPubKey,
		JoinToken: joinToken,
	}
	resp, err := ha.CallJoin(ctx, peerAddr, req)
	if err != nil {
		return fmt.Errorf("call join on %s: %w", peerAddr, err)
	}
	return nodeMgr.AddPeer(ctx, ha.Node{
		ID:        resp.NodeID,
		Address:   resp.Address,
		PublicKey: resp.PublicKey,
	})
}

func deterministicUUID(pubKey []byte) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, pubKey)
}

// bootstrapAdmin creates the first admin user if no users exist yet.
// Idempotent: does nothing if users already exist or the email is taken.
func bootstrapAdmin(ctx context.Context, pool *pgxpool.Pool, authSvc *authpkg.Service, selfID uuid.UUID, email, password string) error {
	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE deleted_at IS NULL`).Scan(&count); err != nil {
		return fmt.Errorf("count users: %w", err)
	}
	if count > 0 {
		return nil // users already exist, skip
	}
	hash, err := authpkg.HashPassword(password)
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO users (email, password_hash, is_admin, origin_node_id)
		VALUES ($1, $2, TRUE, $3)
		ON CONFLICT (email) DO NOTHING`,
		email, hash, selfID)
	if err != nil {
		return fmt.Errorf("create admin: %w", err)
	}
	slog.Info("bootstrapped admin user", "email", email)
	return nil
}

