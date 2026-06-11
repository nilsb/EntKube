// Package services contains the background services ported from the .NET app.
// Each service implements a Run(ctx) error method and is started via
// ha.LeaseManager.RunServiceLoop so exactly one node in the cluster runs it.
package services

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/entkube/entkube/internal/k8s"
	"github.com/entkube/entkube/internal/vault"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DeploymentSyncService polls every active app deployment, fetches its live
// Kubernetes state, and writes back sync/health status. Mirrors the .NET
// DeploymentSyncService (2-minute interval, 200ms inter-deployment delay).
type DeploymentSyncService struct {
	pool     *pgxpool.Pool
	vaultSvc *vault.Service
	selfNode uuid.UUID
}

// NewDeploymentSyncService creates a DeploymentSyncService.
func NewDeploymentSyncService(pool *pgxpool.Pool, vaultSvc *vault.Service, selfNode uuid.UUID) *DeploymentSyncService {
	return &DeploymentSyncService{pool: pool, vaultSvc: vaultSvc, selfNode: selfNode}
}

// Run executes one sync pass over all deployments.
func (s *DeploymentSyncService) Run(ctx context.Context) error {
	type deploymentRow struct {
		ID                  uuid.UUID
		Namespace           string
		Name                string
		ClusterID           uuid.UUID
		KubeconfigVaultKey  *string
		TenantID            uuid.UUID
	}

	rows, err := s.pool.Query(ctx, `
		SELECT
			d.id, d.namespace, d.name,
			d.cluster_id,
			c.kubeconfig_vault_key,
			c.tenant_id
		FROM app_deployments d
		JOIN kubernetes_clusters c ON c.id = d.cluster_id
		WHERE c.kubeconfig_vault_key IS NOT NULL
		ORDER BY d.id`)
	if err != nil {
		return fmt.Errorf("query deployments: %w", err)
	}
	defer rows.Close()

	var deployments []deploymentRow
	for rows.Next() {
		var r deploymentRow
		if err := rows.Scan(&r.ID, &r.Namespace, &r.Name, &r.ClusterID,
			&r.KubeconfigVaultKey, &r.TenantID); err != nil {
			return err
		}
		deployments = append(deployments, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	synced, failed := 0, 0
	for _, d := range deployments {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := s.syncDeployment(ctx, d.ID, d.Namespace, d.Name, d.TenantID, d.ClusterID, d.KubeconfigVaultKey); err != nil {
			slog.Warn("deployment sync failed", "deployment", d.ID, "err", err)
			failed++
		} else {
			synced++
		}
		time.Sleep(200 * time.Millisecond)
	}

	slog.Info("deployment sync pass complete", "synced", synced, "failed", failed)
	return nil
}

func (s *DeploymentSyncService) syncDeployment(
	ctx context.Context,
	deploymentID uuid.UUID,
	namespace, deploymentName string,
	tenantID, clusterID uuid.UUID,
	kubeconfigVaultKey *string,
) error {
	kubeconfig, err := s.loadKubeconfig(ctx, tenantID, kubeconfigVaultKey)
	if err != nil {
		return s.writeStatus(ctx, deploymentID, "failed", "unknown",
			fmt.Sprintf("load kubeconfig: %v", err))
	}

	client, err := k8s.New(kubeconfig)
	if err != nil {
		return s.writeStatus(ctx, deploymentID, "failed", "unknown",
			fmt.Sprintf("create k8s client: %v", err))
	}

	status, err := client.GetDeploymentStatus(ctx, namespace, deploymentName)
	if err != nil {
		return s.writeStatus(ctx, deploymentID, "failed", "unknown", err.Error())
	}

	return s.writeStatus(ctx, deploymentID, status.SyncStatus, status.HealthStatus, status.StatusMessage)
}

func (s *DeploymentSyncService) writeStatus(
	ctx context.Context, id uuid.UUID,
	syncStatus, healthStatus, msg string,
) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE app_deployments
		SET sync_status    = $2::sync_status,
		    health_status  = $3::health_status,
		    status_message = $4,
		    last_synced_at = now(),
		    updated_at     = now()
		WHERE id = $1`,
		id, syncStatus, healthStatus, msg)
	return err
}

// loadKubeconfig retrieves the raw kubeconfig YAML from the tenant vault.
func (s *DeploymentSyncService) loadKubeconfig(
	ctx context.Context,
	tenantID uuid.UUID,
	vaultKeyPtr *string,
) (string, error) {
	if vaultKeyPtr == nil || *vaultKeyPtr == "" {
		return "", fmt.Errorf("no kubeconfig vault key configured")
	}
	vaultKey := *vaultKeyPtr

	var encDEK, nonce []byte
	err := s.pool.QueryRow(ctx, `
		SELECT vnk.encrypted_dek, vnk.nonce
		FROM vault_node_keys vnk
		JOIN secret_vaults sv ON sv.id = vnk.vault_id
		WHERE sv.tenant_id = $1 AND vnk.node_id = $2`,
		tenantID, s.selfNode).Scan(&encDEK, &nonce)
	if err != nil {
		return "", fmt.Errorf("get vault DEK: %w", err)
	}

	dek, err := s.vaultSvc.UnsealDEK(encDEK, nonce)
	if err != nil {
		return "", fmt.Errorf("unseal DEK: %w", err)
	}

	var encValue, valueNonce []byte
	err = s.pool.QueryRow(ctx, `
		SELECT vs.encrypted_value, vs.value_nonce
		FROM vault_secrets vs
		JOIN secret_vaults sv ON sv.id = vs.vault_id
		WHERE sv.tenant_id = $1 AND vs.key_name = $2 AND vs.deleted_at IS NULL`,
		tenantID, vaultKey).Scan(&encValue, &valueNonce)
	if err != nil {
		return "", fmt.Errorf("get vault secret %q: %w", vaultKey, err)
	}

	plaintext, err := s.vaultSvc.DecryptSecret(dek, valueNonce, encValue)
	if err != nil {
		return "", fmt.Errorf("decrypt kubeconfig: %w", err)
	}

	return string(plaintext), nil
}
