// Package k8s wraps client-go to provide per-cluster Kubernetes clients
// loaded from kubeconfig YAML strings retrieved from the vault.
package k8s

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// Client wraps a kubernetes.Clientset with helper methods matching the
// operations the .NET KubernetesOperationsService performed.
type Client struct {
	cs        *kubernetes.Clientset
	clusterID string
}

// New parses a raw kubeconfig YAML string and returns a Client.
// A new Client is created per cluster operation (no persistent connection).
func New(kubeconfigYAML string) (*Client, error) {
	if kubeconfigYAML == "" {
		return nil, fmt.Errorf("kubeconfig is empty")
	}
	cfg, err := clientcmd.RESTConfigFromKubeConfig([]byte(kubeconfigYAML))
	if err != nil {
		return nil, fmt.Errorf("parse kubeconfig: %w", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("create k8s clientset: %w", err)
	}
	return &Client{cs: cs}, nil
}

// ────────────────────────────────────────────────────────────────
// Pod operations
// ────────────────────────────────────────────────────────────────

// ListPods returns all pods in a namespace.
func (c *Client) ListPods(ctx context.Context, namespace string) (*corev1.PodList, error) {
	return c.cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
}

// DeletePod deletes a pod by name (triggers a restart for pods managed by a controller).
func (c *Client) DeletePod(ctx context.Context, namespace, name string) error {
	return c.cs.CoreV1().Pods(namespace).Delete(ctx, name, metav1.DeleteOptions{})
}

// ────────────────────────────────────────────────────────────────
// Deployment operations
// ────────────────────────────────────────────────────────────────

// ListDeployments returns all Deployments in a namespace.
func (c *Client) ListDeployments(ctx context.Context, namespace string) (*appsv1.DeploymentList, error) {
	return c.cs.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
}

// GetDeployment fetches a single Deployment.
func (c *Client) GetDeployment(ctx context.Context, namespace, name string) (*appsv1.Deployment, error) {
	return c.cs.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
}

// ScaleDeployment patches the replica count of a Deployment.
func (c *Client) ScaleDeployment(ctx context.Context, namespace, name string, replicas int32) error {
	patch := fmt.Sprintf(`{"spec":{"replicas":%d}}`, replicas)
	_, err := c.cs.AppsV1().Deployments(namespace).Patch(
		ctx, name, types.MergePatchType, []byte(patch), metav1.PatchOptions{})
	return err
}

// RestartDeployment triggers a rolling restart by annotating the pod template.
func (c *Client) RestartDeployment(ctx context.Context, namespace, name string) error {
	patch := `{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":"` +
		metav1.Now().UTC().Format("2006-01-02T15:04:05Z") + `"}}}}}`
	_, err := c.cs.AppsV1().Deployments(namespace).Patch(
		ctx, name, types.MergePatchType, []byte(patch), metav1.PatchOptions{})
	return err
}

// ────────────────────────────────────────────────────────────────
// Sync / health helpers
// ────────────────────────────────────────────────────────────────

// DeploymentStatus summarises the sync and health status of a Deployment.
type DeploymentStatus struct {
	SyncStatus    string
	HealthStatus  string
	StatusMessage string
	Ready         int32
	Total         int32
}

// GetDeploymentStatus fetches a Deployment and derives its sync/health status.
// This mirrors .NET KubernetesOperationsService.ComputeStatusFromResources.
func (c *Client) GetDeploymentStatus(ctx context.Context, namespace, name string) (*DeploymentStatus, error) {
	d, err := c.GetDeployment(ctx, namespace, name)
	if err != nil {
		return &DeploymentStatus{
			SyncStatus:    "failed",
			HealthStatus:  "missing",
			StatusMessage: err.Error(),
		}, nil
	}

	total := *d.Spec.Replicas
	ready := d.Status.ReadyReplicas
	updated := d.Status.UpdatedReplicas

	status := &DeploymentStatus{Total: total, Ready: ready}

	switch {
	case d.Status.ObservedGeneration < d.Generation:
		status.SyncStatus = "out_of_sync"
		status.HealthStatus = "progressing"
		status.StatusMessage = "rollout in progress"
	case updated < total:
		status.SyncStatus = "syncing"
		status.HealthStatus = "progressing"
		status.StatusMessage = fmt.Sprintf("%d/%d replicas updated", updated, total)
	case ready == total && total > 0:
		status.SyncStatus = "synced"
		status.HealthStatus = "healthy"
	case ready < total:
		status.SyncStatus = "synced"
		status.HealthStatus = "degraded"
		status.StatusMessage = fmt.Sprintf("%d/%d replicas ready", ready, total)
	case total == 0:
		status.SyncStatus = "synced"
		status.HealthStatus = "suspended"
		status.StatusMessage = "scaled to zero"
	default:
		status.SyncStatus = "unknown"
		status.HealthStatus = "unknown"
	}

	return status, nil
}

// ListNamespaces returns all namespaces visible to the cluster credentials.
func (c *Client) ListNamespaces(ctx context.Context) ([]string, error) {
	nsList, err := c.cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	names := make([]string, len(nsList.Items))
	for i, ns := range nsList.Items {
		names[i] = ns.Name
	}
	return names, nil
}

// EnsureNamespace creates the namespace if it does not already exist.
func (c *Client) EnsureNamespace(ctx context.Context, namespace string) error {
	_, err := c.cs.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err == nil {
		return nil // already exists
	}
	if !strings.Contains(err.Error(), "not found") {
		return fmt.Errorf("check namespace: %w", err)
	}
	_, err = c.cs.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: namespace},
	}, metav1.CreateOptions{})
	return err
}
