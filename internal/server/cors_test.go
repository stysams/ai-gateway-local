package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDesktopCORSAllowlist(t *testing.T) {
	handler := desktopCORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	for _, origin := range []string{"http://wails.localhost", "wails://wails", "http://127.0.0.1:9245"} {
		req := httptest.NewRequest(http.MethodOptions, "/api/v1/status", nil)
		req.Header.Set("Origin", origin)
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		if resp.Code != http.StatusNoContent || resp.Header().Get("Access-Control-Allow-Origin") != origin {
			t.Fatalf("origin %q: status=%d headers=%v", origin, resp.Code, resp.Header())
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("Origin", "https://untrusted.example")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if got := resp.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("untrusted origin allowed as %q", got)
	}
}
