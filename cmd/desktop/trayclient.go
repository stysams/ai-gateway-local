package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const trayHTTPTimeout = 15 * time.Second

type trayRoute struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type trayStatus struct {
	LoggingEnabled   bool                 `json:"logging_enabled"`
	AutostartEnabled bool                 `json:"autostart_enabled"`
	Routes           map[string]trayRoute `json:"routes"`
}

type trayProvider struct {
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	DefaultModel string      `json:"default_model"`
	Enabled      *bool       `json:"enabled"`
	Models       []trayModel `json:"models"`
}

type trayModel struct {
	ID      string `json:"id"`
	Enabled *bool  `json:"enabled"`
}

type trayConfig struct {
	UI struct {
		Language string `json:"language"`
	} `json:"ui"`
}

type trayAPI struct {
	base string
	http *http.Client
}

func newTrayAPI(base string) *trayAPI {
	return &trayAPI{base: base, http: &http.Client{Timeout: trayHTTPTimeout}}
}

func (c *trayAPI) status(ctx context.Context) (trayStatus, error) {
	var result trayStatus
	err := c.request(ctx, http.MethodGet, "/api/v1/status", nil, &result)
	return result, err
}

func (c *trayAPI) providers(ctx context.Context) ([]trayProvider, error) {
	var result []trayProvider
	err := c.request(ctx, http.MethodGet, "/api/v1/providers", nil, &result)
	return result, err
}

func (c *trayAPI) config(ctx context.Context) (trayConfig, error) {
	var result trayConfig
	err := c.request(ctx, http.MethodGet, "/api/v1/config", nil, &result)
	return result, err
}

func (c *trayAPI) setRoute(ctx context.Context, client string, route trayRoute) error {
	return c.request(ctx, http.MethodPut, "/api/v1/routes/"+client, route, nil)
}

func (c *trayAPI) setLogging(ctx context.Context, enabled bool) error {
	return c.request(ctx, http.MethodPut, "/api/v1/logging", map[string]bool{"enabled": enabled}, nil)
}

func (c *trayAPI) setAutostart(ctx context.Context, enabled bool) error {
	return c.request(ctx, http.MethodPut, "/api/v1/autostart", map[string]bool{"enabled": enabled}, nil)
}

func (c *trayAPI) shutdown(ctx context.Context) error {
	return c.request(ctx, http.MethodPost, "/api/v1/shutdown", nil, nil)
}

func (c *trayAPI) request(ctx context.Context, method, path string, body, result any) error {
	var payload *bytes.Reader
	if body == nil {
		payload = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, payload)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s returned HTTP %d", method, path, resp.StatusCode)
	}
	if result == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}
