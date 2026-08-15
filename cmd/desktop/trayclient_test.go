package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestTrayClientUsesOnlyManagementEndpoints(t *testing.T) {
	var mu sync.Mutex
	seen := make(map[string]string)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.Method+" "+r.URL.Path] = r.Header.Get("Content-Type")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/status":
			json.NewEncoder(w).Encode(trayStatus{Routes: map[string]trayRoute{"codex": {Provider: "p", Model: "m"}}})
		case "/api/v1/providers":
			json.NewEncoder(w).Encode([]trayProvider{{ID: "p", Name: "Provider", DefaultModel: "m"}})
		case "/api/v1/config":
			json.NewEncoder(w).Encode(map[string]any{"ui": map[string]string{"language": "en-US"}})
		default:
			json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		}
	}))
	defer server.Close()
	client := newTrayAPI(server.URL)
	ctx := context.Background()
	if _, err := client.status(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.providers(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.config(ctx); err != nil {
		t.Fatal(err)
	}
	if err := client.setRoute(ctx, "codex", trayRoute{Provider: "p", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := client.setLogging(ctx, false); err != nil {
		t.Fatal(err)
	}
	if err := client.setAutostart(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := client.shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	for _, endpoint := range []string{
		"GET /api/v1/status", "GET /api/v1/providers", "GET /api/v1/config",
		"PUT /api/v1/routes/codex", "PUT /api/v1/logging", "PUT /api/v1/autostart", "POST /api/v1/shutdown",
	} {
		if _, ok := seen[endpoint]; !ok {
			t.Errorf("missing request %s; seen=%v", endpoint, seen)
		}
	}
	for endpoint, contentType := range seen {
		if endpoint[:3] == "PUT" && contentType != "application/json" {
			t.Errorf("%s Content-Type = %q", endpoint, contentType)
		}
	}
}
