package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestManagementAPIRejectsNonLoopbackSources(t *testing.T) {
	s := newTestServer(t)
	handler := s.routes()
	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/config"},
		{http.MethodGet, "/api/v1/logs"},
		{http.MethodGet, "/api/v1/providers"},
		{http.MethodPost, "/api/v1/shutdown"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.RemoteAddr = "192.0.2.10:43123"
		req.Header.Set("X-Forwarded-For", "127.0.0.1")
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		if resp.Code != http.StatusForbidden {
			t.Fatalf("%s %s status = %d, body = %s", tc.method, tc.path, resp.Code, resp.Body.String())
		}
		var body errorBody
		if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s %s decode error: %v", tc.method, tc.path, err)
		}
		if body.Error.Code != "management_loopback_required" {
			t.Fatalf("%s %s error code = %q", tc.method, tc.path, body.Error.Code)
		}
	}
	select {
	case <-s.ShutdownRequested():
		t.Fatal("rejected remote shutdown request triggered shutdown")
	default:
	}
}

func TestManagementAPIAcceptsLoopbackAndLeavesDataPlaneReachable(t *testing.T) {
	s := newTestServer(t)
	handler := s.routes()
	for _, remoteAddr := range []string{"127.0.0.1:43123", "[::1]:43123"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
		req.RemoteAddr = remoteAddr
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("loopback %s status = %d, body = %s", remoteAddr, resp.Code, resp.Body.String())
		}
	}

	for _, path := range []string{"/healthz", "/readyz", "/v1/models"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "192.0.2.10:43123"
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		if resp.Code == http.StatusForbidden && strings.Contains(resp.Body.String(), "management_loopback_required") {
			t.Fatalf("non-management path %s was blocked by management boundary", path)
		}
	}
}

func TestLoopbackRemoteParsing(t *testing.T) {
	for _, tc := range []struct {
		remote string
		want   bool
	}{
		{"127.0.0.1:1", true},
		{"[::1]:1", true},
		{"127.0.0.2:1", true},
		{"192.0.2.1:1", false},
		{"localhost:1", false},
		{"127.0.0.1", false},
		{"", false},
	} {
		if got := isLoopbackRemote(tc.remote); got != tc.want {
			t.Errorf("isLoopbackRemote(%q) = %v, want %v", tc.remote, got, tc.want)
		}
	}
}
