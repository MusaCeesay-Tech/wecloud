package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
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
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// appTarget is what the coordinator resolves an app id to.
type appTarget struct {
	AppID    string `json:"app_id"`
	WorkerID string `json:"worker_id"`
	HostIP   string `json:"host_ip"`
	Port     int    `json:"port"`
	Tunneled bool   `json:"tunneled"`
}

var (
	lookupCache = struct {
		sync.RWMutex
		m map[string]cachedTarget
	}{m: make(map[string]cachedTarget)}
)

type cachedTarget struct {
	target    appTarget
	expiresAt time.Time
}

func resolveTarget(appID string) (appTarget, error) {
	lookupCache.RLock()
	if c, ok := lookupCache.m[appID]; ok && time.Now().Before(c.expiresAt) {
		lookupCache.RUnlock()
		return c.target, nil
	}
	lookupCache.RUnlock()

	resp, err := http.Get(fmt.Sprintf("%s/api/lookup/%s", coordinatorURL, appID))
	if err != nil {
		return appTarget{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return appTarget{}, fmt.Errorf("coordinator lookup failed: HTTP %d", resp.StatusCode)
	}
	var t appTarget
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return appTarget{}, err
	}
	lookupCache.Lock()
	lookupCache.m[appID] = cachedTarget{target: t, expiresAt: time.Now().Add(5 * time.Second)}
	lookupCache.Unlock()
	return t, nil
}

// acquireTunnel asks the coordinator for a raw byte pipe to a NAT'd worker. The
// coordinator hijacks the connection and never writes HTTP response headers, so
// the returned connection carries the worker's HTTP response directly.
func acquireTunnel(workerID string, port int) (net.Conn, error) {
	u, err := url.Parse(coordinatorURL)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialTimeout("tcp", u.Host, 10*time.Second)
	if err != nil {
		return nil, err
	}
	req := fmt.Sprintf("GET /api/tunnel/acquire?worker=%s&port=%d HTTP/1.1\r\nHost: %s\r\n\r\n",
		url.QueryEscape(workerID), port, u.Host)
	if _, err := conn.Write([]byte(req)); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

// tunnelDialer returns a DialContext that routes the connection through the
// coordinator to the resolved worker, falling back to a direct dial when possible.
func tunnelDialer(tgt appTarget) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := acquireTunnel(tgt.WorkerID, tgt.Port)
		if err == nil {
			return conn, nil
		}
		if tgt.HostIP != "" {
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network,
				fmt.Sprintf("%s:%d", tgt.HostIP, tgt.Port))
		}
		return nil, err
	}
}

func handleRoutingProxy(res http.ResponseWriter, req *http.Request) {
	host := req.Host
	if i := strings.Index(host, ":"); i != -1 {
		host = host[:i]
	}
	hostParts := strings.Split(host, ".")
	if len(hostParts) < 2 || hostParts[0] == "" {
		http.Error(res, "Bad Gateway: Invalid application routing structure", http.StatusBadGateway)
		return
	}
	appID := hostParts[0]

	tgt, err := resolveTarget(appID)
	if err != nil {
		http.Error(res, "Bad Gateway: "+err.Error(), http.StatusBadGateway)
		return
	}

	targetHost := fmt.Sprintf("%s:%d", tgt.HostIP, tgt.Port)
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	if tgt.Tunneled {
		targetHost = fmt.Sprintf("%s.tunnel:%d", tgt.WorkerID, tgt.Port)
		transport.DialContext = tunnelDialer(tgt)
	}

	target, _ := url.Parse("http://" + targetHost)
	proxy := &httputil.ReverseProxy{
		Transport: transport,
		Director: func(r *http.Request) {
			r.URL.Scheme = "http"
			r.URL.Host = target.Host
			r.Host = host
			r.Header.Set("X-Forwarded-App", appID)
			r.Header.Set("X-Forwarded-Host", host)
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("proxy error for %s: %v", appID, err)
			http.Error(w, "Bad Gateway: "+err.Error(), http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(res, req)
}

func main() {
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	http.HandleFunc("/", handleRoutingProxy)
	log.Printf("WeCloud Global Entry Mesh Router listening on %s...", listenAddr)
	log.Fatal(http.ListenAndServe(listenAddr, nil))
}
