// Package alerting provides Alertmanager API integration and incident management.
package alerting

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Alert represents a single alert from the Alertmanager /api/v2/alerts endpoint.
type Alert struct {
	Fingerprint string            `json:"fingerprint"`
	Status      struct {
		State string `json:"state"` // "active" | "suppressed" | "unprocessed"
	} `json:"status"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    time.Time         `json:"startsAt"`
	EndsAt      time.Time         `json:"endsAt"`
	GeneratorURL string           `json:"generatorURL"`
}

// Client is a minimal Alertmanager API client.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates an Alertmanager client for the given base URL
// (e.g. "http://alertmanager.monitoring.svc.cluster.local:9093").
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// GetAlerts fetches all currently active alerts from Alertmanager.
func (c *Client) GetAlerts(ctx context.Context) ([]Alert, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/api/v2/alerts?active=true&silenced=false&inhibited=false", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("alertmanager request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("alertmanager returned %d", resp.StatusCode)
	}

	var alerts []Alert
	if err := json.NewDecoder(resp.Body).Decode(&alerts); err != nil {
		return nil, fmt.Errorf("decode alertmanager response: %w", err)
	}
	return alerts, nil
}

// AlertSeverity extracts the severity label, defaulting to "info".
func (a *Alert) AlertSeverity() string {
	if s, ok := a.Labels["severity"]; ok && s != "" {
		return s
	}
	return "info"
}

// Summary returns the alert summary annotation.
func (a *Alert) Summary() string { return a.Annotations["summary"] }

// Description returns the alert description annotation.
func (a *Alert) Description() string { return a.Annotations["description"] }

// RunbookURL returns the alert runbook URL annotation.
func (a *Alert) RunbookURL() string { return a.Annotations["runbook_url"] }

// LabelsJSON marshals the alert's labels map to a JSON string.
func (a *Alert) LabelsJSON() string {
	b, _ := json.Marshal(a.Labels)
	return string(b)
}
