// Package gitops provides Git repository operations using go-git (pure Go, no CLI).
package gitops

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
)

// CloneResult contains the commit SHA and checked-out files.
type CloneResult struct {
	CommitSHA string
	Files     map[string][]byte // relative path → content
}

// Service performs Git operations against remote repositories.
type Service struct{}

// New creates a GitService.
func New() *Service { return &Service{} }

// HeadCommit returns the current HEAD commit SHA for the given branch/tag,
// without a full clone (uses git ls-remote).
func (s *Service) HeadCommit(ctx context.Context, repoURL, ref string, auth transport.AuthMethod) (string, error) {
	// go-git does not have a direct ls-remote without a clone, so we do a
	// shallow clone into a temp directory and read HEAD.
	dir, err := os.MkdirTemp("", "entkube-git-*")
	if err != nil {
		return "", fmt.Errorf("mktemp: %w", err)
	}
	defer os.RemoveAll(dir)

	repo, err := gogit.PlainCloneContext(ctx, dir, false, &gogit.CloneOptions{
		URL:           repoURL,
		Auth:          auth,
		ReferenceName: branchRef(ref),
		SingleBranch:  true,
		Depth:         1,
		NoCheckout:    true,
	})
	if err != nil {
		return "", fmt.Errorf("shallow clone for HEAD: %w", err)
	}

	head, err := repo.Head()
	if err != nil {
		return "", fmt.Errorf("get HEAD: %w", err)
	}
	return head.Hash().String(), nil
}

// CheckoutPath clones the repository at the given ref and returns the content
// of all files under path (relative to repo root). path="" returns all files.
func (s *Service) CheckoutPath(ctx context.Context, repoURL, ref, path string, auth transport.AuthMethod) (*CloneResult, error) {
	dir, err := os.MkdirTemp("", "entkube-git-*")
	if err != nil {
		return nil, fmt.Errorf("mktemp: %w", err)
	}
	defer os.RemoveAll(dir)

	repo, err := gogit.PlainCloneContext(ctx, dir, false, &gogit.CloneOptions{
		URL:           repoURL,
		Auth:          auth,
		ReferenceName: branchRef(ref),
		SingleBranch:  true,
		Depth:         1,
	})
	if err != nil {
		return nil, fmt.Errorf("clone %s@%s: %w", repoURL, ref, err)
	}

	head, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("get HEAD: %w", err)
	}

	root := filepath.Join(dir, filepath.FromSlash(path))
	files := make(map[string][]byte)
	if err := collectFiles(root, dir, files); err != nil {
		return nil, fmt.Errorf("collect files: %w", err)
	}

	return &CloneResult{CommitSHA: head.Hash().String(), Files: files}, nil
}

// collectFiles walks root and reads all files, keying them by path relative to base.
func collectFiles(root, base string, out map[string][]byte) error {
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("stat %s: %w", root, err)
	}
	if !info.IsDir() {
		// root is a single file
		data, err := os.ReadFile(root)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(base, root)
		out[filepath.ToSlash(rel)] = data
		return nil
	}
	return filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(base, p)
		rel = filepath.ToSlash(rel)
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		data, err := io.ReadAll(f)
		if err != nil {
			return err
		}
		out[rel] = data
		return nil
	})
}

// ────────────────────────────────────────────────────────────────
// Auth helpers
// ────────────────────────────────────────────────────────────────

// HTTPAuth returns a go-git HTTP auth method from a username and PAT/password.
func HTTPAuth(username, token string) transport.AuthMethod {
	if token == "" {
		return nil
	}
	return &http.BasicAuth{Username: username, Password: token}
}

// SplitYAMLDocuments splits a multi-document YAML file on "---" separators,
// returning non-empty documents.
func SplitYAMLDocuments(yaml string) []string {
	var docs []string
	for _, doc := range strings.Split(yaml, "\n---") {
		if trimmed := strings.TrimSpace(doc); trimmed != "" {
			docs = append(docs, trimmed)
		}
	}
	return docs
}

// ExtractKindName extracts "kind" and "metadata.name" from a simple YAML doc.
// Returns ("", "") on parse failure — callers should handle gracefully.
func ExtractKindName(yaml string) (kind, name string) {
	for _, line := range strings.Split(yaml, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "kind:") {
			kind = strings.TrimSpace(strings.TrimPrefix(line, "kind:"))
		}
		if strings.HasPrefix(line, "  name:") || (strings.HasPrefix(line, "name:") && !strings.Contains(line, ".")) {
			name = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "  "), "name:"))
		}
	}
	return
}

func branchRef(ref string) plumbing.ReferenceName {
	if ref == "" || ref == "HEAD" {
		return plumbing.HEAD
	}
	return plumbing.NewBranchReferenceName(ref)
}
