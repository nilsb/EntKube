package handlers

import (
	"log/slog"
	"net/http"
	"os"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WebhookHandler handles inbound webhook calls from external services.
type WebhookHandler struct {
	pool         *pgxpool.Pool
	gitSyncQueue func(uuid.UUID)
}

// NewWebhookHandler creates a WebhookHandler.
func NewWebhookHandler(pool *pgxpool.Pool, gitSyncQueue func(uuid.UUID)) *WebhookHandler {
	return &WebhookHandler{pool: pool, gitSyncQueue: gitSyncQueue}
}

// Git handles POST /api/webhooks/git/{secret}.
// The secret in the URL is compared to WEBHOOK_GIT_SECRET env var. On match
// all git-backed deployments whose git_url matches the pushed repository are
// enqueued for immediate sync.
func (h *WebhookHandler) Git(w http.ResponseWriter, r *http.Request) {
	secret := r.PathValue("secret")
	expected := os.Getenv("WEBHOOK_GIT_SECRET")
	if expected == "" || secret != expected {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	// GitHub / GitLab / Gitea all send the repository URL in different headers
	// or body fields. We look at a few common ones, and fall back to a query
	// parameter so the URL can encode the repo.
	repoURL := r.URL.Query().Get("repo")
	if repoURL == "" {
		// GitHub sends X-GitHub-Event and the clone URL in the body as JSON, but
		// we keep this handler lightweight. Operators can pass ?repo= instead.
		repoURL = r.Header.Get("X-Repository-URL")
	}

	var ids []uuid.UUID
	if repoURL != "" {
		rows, err := h.pool.Query(r.Context(),
			`SELECT id FROM app_deployments WHERE git_url = $1 AND git_auto_sync = TRUE`,
			repoURL)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var id uuid.UUID
				if rows.Scan(&id) == nil {
					ids = append(ids, id)
				}
			}
		}
	} else {
		// No repo URL — enqueue all auto-sync git deployments.
		rows, err := h.pool.Query(r.Context(),
			`SELECT id FROM app_deployments
			 WHERE git_url IS NOT NULL AND git_auto_sync = TRUE`)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var id uuid.UUID
				if rows.Scan(&id) == nil {
					ids = append(ids, id)
				}
			}
		}
	}

	for _, id := range ids {
		h.gitSyncQueue(id)
	}

	slog.Info("git webhook received", "repo", repoURL, "enqueued", len(ids))
	writeJSON(w, http.StatusOK, map[string]int{"enqueued": len(ids)})
}
