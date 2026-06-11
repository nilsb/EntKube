package services

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"

	"github.com/entkube/entkube/internal/gitops"
	"github.com/entkube/entkube/internal/vault"
	gogittransport "github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// GitSyncService polls Git repositories and reconciles DeploymentManifest rows.
// It also supports on-demand sync via EnqueueSync (called from webhooks or UI).
// Runs every 3 minutes with 300ms inter-deployment delay.
type GitSyncService struct {
	pool      *pgxpool.Pool
	vaultSvc  *vault.Service
	gitSvc    *gitops.Service
	selfNode  uuid.UUID
	mu        sync.Mutex
	syncQueue []uuid.UUID // on-demand deployment IDs
}

// NewGitSyncService creates a GitSyncService.
func NewGitSyncService(pool *pgxpool.Pool, vaultSvc *vault.Service, selfNode uuid.UUID) *GitSyncService {
	return &GitSyncService{
		pool:     pool,
		vaultSvc: vaultSvc,
		gitSvc:   gitops.New(),
		selfNode: selfNode,
	}
}

// EnqueueSync adds a deployment ID to the on-demand sync queue.
// These are processed before the regular periodic scan.
func (s *GitSyncService) EnqueueSync(deploymentID uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.syncQueue = append(s.syncQueue, deploymentID)
}

// drainQueue returns and clears the on-demand queue.
func (s *GitSyncService) drainQueue() []uuid.UUID {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.syncQueue) == 0 {
		return nil
	}
	ids := s.syncQueue
	s.syncQueue = nil
	return ids
}

// Run processes the on-demand queue then performs a full periodic scan.
func (s *GitSyncService) Run(ctx context.Context) error {
	// On-demand first.
	for _, id := range s.drainQueue() {
		if err := s.syncByID(ctx, id); err != nil {
			slog.Error("on-demand git sync failed", "deployment", id, "err", err)
		}
	}

	// Periodic scan: all git-backed deployments with auto-sync enabled.
	type deploymentRow struct {
		ID               uuid.UUID
		TenantID         uuid.UUID
		GitURL           *string
		GitPath          string
		GitRevision      string
		GitAutoSync      bool
		LastSyncedCommit *string
		DeploymentType   string
	}

	rows, err := s.pool.Query(ctx, `
		SELECT d.id, c.tenant_id, d.git_url, d.git_path, d.git_revision,
		       d.git_auto_sync, d.git_last_synced_commit,
		       d.deployment_type::text
		FROM   app_deployments d
		JOIN   kubernetes_clusters cl ON cl.id = d.cluster_id
		JOIN   customers cu ON cu.id = (SELECT customer_id FROM apps WHERE id = d.app_id)
		JOIN   tenants c ON c.id = cu.tenant_id
		WHERE  d.deployment_type IN ('git_yaml','git_helm','git_app_of_apps')
		  AND  d.git_auto_sync = TRUE
		  AND  d.git_url IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("query git deployments: %w", err)
	}
	defer rows.Close()

	var deployments []deploymentRow
	for rows.Next() {
		var r deploymentRow
		if err := rows.Scan(&r.ID, &r.TenantID, &r.GitURL, &r.GitPath,
			&r.GitRevision, &r.GitAutoSync, &r.LastSyncedCommit, &r.DeploymentType); err != nil {
			return err
		}
		deployments = append(deployments, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	synced, skipped, failed := 0, 0, 0
	for _, d := range deployments {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		changed, err := s.syncDeploymentGit(ctx, d.ID, d.TenantID,
			*d.GitURL, d.GitPath, d.GitRevision,
			d.LastSyncedCommit, d.DeploymentType)
		if err != nil {
			slog.Error("git sync failed", "deployment", d.ID, "err", err)
			failed++
		} else if changed {
			synced++
		} else {
			skipped++
		}
	}

	slog.Info("git sync pass complete",
		"synced", synced, "skipped_no_change", skipped, "failed", failed)
	return nil
}

func (s *GitSyncService) syncByID(ctx context.Context, deploymentID uuid.UUID) error {
	var tenantID uuid.UUID
	var gitURL, gitPath, gitRevision, deploymentType string
	var lastCommit *string
	err := s.pool.QueryRow(ctx, `
		SELECT c.tenant_id, d.git_url, d.git_path, d.git_revision,
		       d.git_last_synced_commit, d.deployment_type::text
		FROM   app_deployments d
		JOIN   kubernetes_clusters cl ON cl.id = d.cluster_id
		JOIN   customers cu ON cu.id = (SELECT customer_id FROM apps WHERE id = d.app_id)
		JOIN   tenants c ON c.id = cu.tenant_id
		WHERE  d.id = $1 AND d.git_url IS NOT NULL`, deploymentID).
		Scan(&tenantID, &gitURL, &gitPath, &gitRevision, &lastCommit, &deploymentType)
	if err != nil {
		return fmt.Errorf("get deployment: %w", err)
	}
	_, err = s.syncDeploymentGit(ctx, deploymentID, tenantID,
		gitURL, gitPath, gitRevision, lastCommit, deploymentType)
	return err
}

func (s *GitSyncService) syncDeploymentGit(
	ctx context.Context,
	deploymentID, tenantID uuid.UUID,
	gitURL, gitPath, gitRevision string,
	lastCommit *string,
	deploymentType string,
) (changed bool, err error) {
	// Fetch credentials for this repo URL.
	authMethod, err := s.loadGitAuth(ctx, tenantID, gitURL)
	if err != nil {
		return false, fmt.Errorf("load git auth: %w", err)
	}

	// Check HEAD commit — skip if unchanged.
	head, err := s.gitSvc.HeadCommit(ctx, gitURL, gitRevision, authMethod)
	if err != nil {
		return false, fmt.Errorf("head commit: %w", err)
	}
	if lastCommit != nil && *lastCommit == head {
		return false, nil // nothing changed
	}

	// Full checkout.
	result, err := s.gitSvc.CheckoutPath(ctx, gitURL, gitRevision, gitPath, authMethod)
	if err != nil {
		return false, fmt.Errorf("checkout: %w", err)
	}

	if err := s.applyManifests(ctx, deploymentID, result.Files, deploymentType); err != nil {
		return false, fmt.Errorf("apply manifests: %w", err)
	}

	// Update sync cursor.
	_, err = s.pool.Exec(ctx, `
		UPDATE app_deployments
		SET git_last_synced_commit = $2,
		    git_last_synced_at     = now(),
		    updated_at             = now()
		WHERE id = $1`, deploymentID, result.CommitSHA)
	return true, err
}

// applyManifests reconciles deployment_manifests for a GitYaml deployment.
func (s *GitSyncService) applyManifests(
	ctx context.Context,
	deploymentID uuid.UUID,
	files map[string][]byte,
	deploymentType string,
) error {
	if deploymentType != "git_yaml" {
		// Helm and app-of-apps handled separately; stub here.
		return nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Remove existing manifests to replace them.
	_, err = tx.Exec(ctx, `DELETE FROM deployment_manifests WHERE deployment_id = $1`, deploymentID)
	if err != nil {
		return err
	}

	sortOrder := 0
	for relPath, content := range files {
		if !isYAMLFile(relPath) {
			continue
		}
		for _, doc := range gitops.SplitYAMLDocuments(string(content)) {
			kind, name := gitops.ExtractKindName(doc)
			if kind == "" {
				kind = "Unknown"
			}
			if name == "" {
				name = strings.TrimSuffix(filepath.Base(relPath), ".yaml")
			}
			_, err = tx.Exec(ctx, `
				INSERT INTO deployment_manifests
					(deployment_id, kind, name, sort_order, yaml_content, source_file)
				VALUES ($1, $2, $3, $4, $5, $6)`,
				deploymentID, kind, name, sortOrder, doc, relPath)
			if err != nil {
				return err
			}
			sortOrder++
		}
	}

	return tx.Commit(ctx)
}

func (s *GitSyncService) loadGitAuth(ctx context.Context, tenantID uuid.UUID, repoURL string) (gogittransport.AuthMethod, error) {
	// Find a matching git_repositories credential for this URL + tenant.
	var username string
	var credVaultKey *string
	err := s.pool.QueryRow(ctx, `
		SELECT username, credential_vault_key
		FROM   git_repositories
		WHERE  tenant_id = $1 AND url = $2
		LIMIT  1`, tenantID, repoURL).Scan(&username, &credVaultKey)
	if err != nil || credVaultKey == nil {
		return nil, nil // public repo or no credential configured
	}

	token, err := s.loadVaultSecret(ctx, tenantID, *credVaultKey)
	if err != nil {
		return nil, err
	}
	return gitops.HTTPAuth(username, token), nil
}

func (s *GitSyncService) loadVaultSecret(ctx context.Context, tenantID uuid.UUID, keyName string) (string, error) {
	var encDEK, nonce []byte
	if err := s.pool.QueryRow(ctx, `
		SELECT vnk.encrypted_dek, vnk.nonce
		FROM   vault_node_keys vnk
		JOIN   secret_vaults sv ON sv.id = vnk.vault_id
		WHERE  sv.tenant_id = $1 AND vnk.node_id = $2`,
		tenantID, s.selfNode).Scan(&encDEK, &nonce); err != nil {
		return "", err
	}
	dek, err := s.vaultSvc.UnsealDEK(encDEK, nonce)
	if err != nil {
		return "", err
	}
	var encVal, valNonce []byte
	if err := s.pool.QueryRow(ctx, `
		SELECT vs.encrypted_value, vs.value_nonce
		FROM   vault_secrets vs
		JOIN   secret_vaults sv ON sv.id = vs.vault_id
		WHERE  sv.tenant_id = $1 AND vs.key_name = $2 AND vs.deleted_at IS NULL`,
		tenantID, keyName).Scan(&encVal, &valNonce); err != nil {
		return "", err
	}
	plain, err := s.vaultSvc.DecryptSecret(dek, valNonce, encVal)
	return string(plain), err
}

func isYAMLFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".yaml" || ext == ".yml"
}
