package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

var coordinatorURL = envOr("WECLOUD_COORDINATOR", "http://localhost:8080")
var listenAddr = envOr("WECLOUD_PROXY_ADDR", ":8081")

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// lookupCache reduces load on the coordinator for a burst of requests.
var (
	lookupCache = struct {
		sync.RWMutex
		m map[string]cachedOrigin
	}{m: make(map[string]cachedOrigin)}
)

type cachedOrigin struct {
	origin    string
	expiresAt time.Time
}

func resolveOrigin(appID string) (string, error) {
	lookupCache.RLock()
	if c, ok := lookupCache.m[appID]; ok && time.Now().Before(c.expiresAt) {
		lookupCache.RUnlock()
		return c.origin, nil
	}
	lookupCache.RUnlock()

	resp, err := http.Get(fmt.Sprintf("%s/api/lookup/%s", coordinatorURL, appID))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("coordinator lookup failed: HTTP %d", resp.StatusCode)
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	origin := string(bodyBytes)

	lookupCache.Lock()
	lookupCache.m[appID] = cachedOrigin{origin: origin, expiresAt: time.Now().Add(5 * time.Second)}
	lookupCache.Unlock()
	return origin, nil
}

func handleRoutingProxy(res http.ResponseWriter, req *http.Request) {
	// Expects virtual host domains like: app-1721....wecloud.local
	host := req.Host
	if i := strings.Index(host, ":"); i != -1 {
		host = host[:i]
	}
	hostParts := strings.Split(host, ".")
	if len(hostParts) < 2 || hostParts[0] == "" {
		http.Error(res, "Bad Gateway: Invalid application routing structure", http.StatusBadRequest)
		return
	}
	appID := hostParts[0]

	origin, err := resolveOrigin(appID)
	if err != nil {
		log.Printf("routing error for %s: %v", appID, err)
		http.Error(res, "Cloud Routing Error: Dynamic application target unresolved", http.StatusBadGateway)
		return
	}

	targetURL, err := url.Parse("http://" + origin)
	if err != nil {
		http.Error(res, "Configuration Malformed", http.StatusInternalServerError)
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	req.URL.Host = targetURL.Host
	req.URL.Scheme = targetURL.Scheme
	req.Host = targetURL.Host
	req.Header.Set("X-Forwarded-Host", host)
	req.Header.Set("X-Forwarded-App", appID)
	proxy.ServeHTTP(res, req)
}

func main() {
	http.HandleFunc("/", handleRoutingProxy)
	log.Printf("WeCloud Global Entry Mesh Router listening on %s...", listenAddr)
	log.Fatal(http.ListenAndServe(listenAddr, nil))
}