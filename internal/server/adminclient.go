package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// adminTimeout bounds each management API call made by the CLI (stop/status).
const adminTimeout = 5 * time.Second

// AdminClient talks to the management API of a running gateway. It is used by
// the stop and status CLI commands and always targets the loopback address.
type AdminClient struct {
	base string
	http *http.Client
}

// SetAutostart updates and verifies the current-user login registration.
func (c *AdminClient) SetAutostart(ctx context.Context, enabled bool) (*AutostartResponse, error) {
	body, err := json.Marshal(AutostartRequest{Enabled: enabled})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.base+"/api/v1/autostart", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("autostart: unexpected HTTP %d", resp.StatusCode)
	}
	var result AutostartResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("autostart: decode response: %w", err)
	}
	return &result, nil
}

// NewAdminClient returns a client for the gateway listening on port.
func NewAdminClient(port int) *AdminClient {
	return &AdminClient{
		base: fmt.Sprintf("http://127.0.0.1:%d", port),
		http: &http.Client{Timeout: adminTimeout},
	}
}

// Base returns the base URL of the management API.
func (c *AdminClient) Base() string { return c.base }

// Status fetches GET /api/v1/status.
func (c *AdminClient) Status(ctx context.Context) (*StatusResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/v1/status", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status: unexpected HTTP %d", resp.StatusCode)
	}
	var st StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return nil, fmt.Errorf("status: decode response: %w", err)
	}
	return &st, nil
}

// Shutdown posts POST /api/v1/shutdown and succeeds on 202 Accepted.
func (c *AdminClient) Shutdown(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/v1/shutdown", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("shutdown: unexpected HTTP %d", resp.StatusCode)
	}
	return nil
}

// Healthz checks GET /healthz; nil means the gateway is reachable and alive.
func (c *AdminClient) Healthz(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz: unexpected HTTP %d", resp.StatusCode)
	}
	return nil
}

// Doctor fetches the complete live diagnostic report.
func (c *AdminClient) Doctor(ctx context.Context) (*DoctorReport, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/v1/doctor", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("doctor: unexpected HTTP %d", resp.StatusCode)
	}
	var report DoctorReport
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		return nil, fmt.Errorf("doctor: decode response: %w", err)
	}
	return &report, nil
}
