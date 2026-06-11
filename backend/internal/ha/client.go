package ha

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

var defaultClient = &http.Client{Timeout: 15 * time.Second}

// CallJoin sends a JoinRequest to peerAddr and returns the peer's JoinResponse.
// This is called once by a new node on startup to bootstrap into the cluster.
func CallJoin(ctx context.Context, peerAddr string, req *JoinRequest) (*JoinResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal join request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		peerAddr+"/api/ha/join", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := defaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("join request to %s: %w", peerAddr, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("join returned status %d from %s", resp.StatusCode, peerAddr)
	}

	var jr JoinResponse
	if err := json.NewDecoder(resp.Body).Decode(&jr); err != nil {
		return nil, fmt.Errorf("decode join response: %w", err)
	}
	return &jr, nil
}
