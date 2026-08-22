package server

import (
	"net"
	"net/http"
)

// loopbackManagementOnly keeps the management plane local even when the
// shared listener is explicitly bound to 0.0.0.0 for LAN data-plane access.
// RemoteAddr comes from net/http's accepted TCP connection; forwarded headers
// are deliberately ignored because ai-gateway does not configure trusted
// proxies.
func loopbackManagementOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackRemote(r.RemoteAddr) {
			writeAPIError(w, http.StatusForbidden, "management_loopback_required", "management API is only available from loopback", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopbackRemote(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
