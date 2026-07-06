package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/inception42/cortex/reconciler/internal/tokens"
	"github.com/inception42/cortex/shared"
)

// Client talks to the Cortex control plane's reconciler-facing API, presenting
// the reconciler's own Entra token on every call. The tenant it acts on is the
// token's tid — never sent as a parameter.
type Client struct {
	baseURL string
	tokens  tokens.Source
	http    *http.Client
}

func New(baseURL string, src tokens.Source) *Client {
	return &Client{baseURL: baseURL, tokens: src, http: &http.Client{Timeout: 15 * time.Second}}
}

func (c *Client) authorize(ctx context.Context, req *http.Request) error {
	tok, err := c.tokens.Token(ctx)
	if err != nil {
		return fmt.Errorf("acquire token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	return nil
}

// Sync pulls the desired state (entitled + enabled agents) for this tenant.
func (c *Client) Sync(ctx context.Context) (shared.DesiredState, error) {
	var ds shared.DesiredState
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/recon/sync", nil)
	if err != nil {
		return ds, err
	}
	if err := c.authorize(ctx, req); err != nil {
		return ds, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return ds, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return ds, fmt.Errorf("sync: %d %s", resp.StatusCode, b)
	}
	return ds, json.NewDecoder(resp.Body).Decode(&ds)
}

// Heartbeat reports install identity + actual agent state to the control plane.
func (c *Client) Heartbeat(ctx context.Context, hb shared.Heartbeat) error {
	body, err := json.Marshal(hb)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/recon/heartbeat", bytes.NewReader(body))
	if err != nil {
		return err
	}
	if err := c.authorize(ctx, req); err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("heartbeat: %d %s", resp.StatusCode, b)
	}
	return nil
}
