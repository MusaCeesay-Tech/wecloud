package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"
)

// Public routing configuration. The mesh serves apps as
//
//	<app-id>.<base-domain>[:proxy-port]
//
// which works with sslip.io: set WECLOUD_BASE_DOMAIN to "<leader-ip>.sslip.io"
// and every <app>.<leader-ip>.sslip.io resolves straight to the leader.
func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func publicScheme() string { return envOr("WECLOUD_PUBLIC_SCHEME", "http") }
func baseDomain() string   { return envOr("WECLOUD_BASE_DOMAIN", "wecloud.local") }
func proxyPort() string    { return strings.TrimSpace(os.Getenv("WECLOUD_PROXY_PORT")) } // empty => default port

// envInt reads an integer env var, falling back when unset or invalid.
func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n := 0
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n <= 0 {
		return fallback
	}
	return n
}

// appURL builds the user-facing URL for an app id.
func appURL(appID string) string {
	u := fmt.Sprintf("%s://%s.%s", publicScheme(), appID, baseDomain())
	if p := proxyPort(); p != "" {
		u += ":" + p
	}
	return u
}

// handleConfig exposes the routing configuration to the web dashboard.
func handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"base_domain": baseDomain(),
		"scheme":      publicScheme(),
		"proxy_port":  proxyPort(),
		"app_pattern": fmt.Sprintf("%s://<app-id>.%s", publicScheme(), baseDomain()),
	})
}
