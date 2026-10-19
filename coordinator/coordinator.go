package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// Worker is the state the coordinator keeps about a volunteer host node.
type Worker struct {
	ID         string `json:"id"`
	IP         string `json:"ip"`      // callback address for /spawn (includes worker port)
	HostIP     string `json:"host_ip"` // bare host IP used to reach mapped container ports
	CPUCores   int    `json:"cpu_cores"`
	RAM        int    `json:"ram_mb"` // worker sends MB
	DiskGB     int    `json:"disk_gb"`
	ActiveApps int    `json:"active_apps"`
	Secret     string `json:"secret,omitempty"`     // authenticates tunnel connections
	OwnerCode  string `json:"owner_code,omitempty"` // one-time code linking node to a user
}

// Application is a developer workload scheduled onto a worker.
type Application struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DockerImage string `json:"docker_image"`
	WorkerID    string `json:"worker_id"`
	Port        int    `json:"port"`
	Status      string `json:"status"`
}

// spawnRequest is the payload the coordinator pushes to a worker's /spawn endpoint.
type spawnRequest struct {
	AppID        string `json:"app_id"`
	Image        string `json:"image"`
	InternalPort int    `json:"internal_port"`
}

// spawnResponse is what a worker replies with after instantiating a container.
type spawnResponse struct {
	Status  string `json:"status"`
	Port    int    `json:"port"`
	Error   string `json:"error,omitempty"`
	Message string `json:"message,omitempty"`
}

type deployRequest struct {
	Name         string `json:"name"`
	Image        string `json:"image"`
	InternalPort int    `json:"internal_port"`
}

// httpClient is used for outbound calls to worker daemons. Generous timeout
// because a worker may need to pull a large image before replying.
var httpClient = &http.Client{Timeout: 300 * time.Second}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// handleHeartbeat upserts a worker's live telemetry.
func handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var worker Worker
	if err := json.NewDecoder(r.Body).Decode(&worker); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if worker.ID == "" {
		http.Error(w, "worker id is required", http.StatusBadRequest)
		return
	}

	if err := upsertWorker(&worker); err != nil {
		http.Error(w, "DB error", http.StatusInternalServerError)
		return
	}
	log.Printf("heartbeat registered: %s (%d cores / %d MB / %d GB disk, %d apps)",
		worker.ID, worker.CPUCores, worker.RAM, worker.DiskGB, worker.ActiveApps)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// pickWorker selects the healthiest least-loaded node (bin-packing by lowest density).
func pickWorker() (id, ip string, err error) {
	// Only consider nodes seen in the last 30 seconds, then prefer the least loaded.
	err = db.QueryRow(`SELECT id, ip FROM workers
		WHERE last_seen > datetime('now', '-30 seconds')
		ORDER BY active_apps ASC, cpu DESC
		LIMIT 1`).Scan(&id, &ip)
	return
}

// handleDeploy implements the verified deployment lifecycle:
//  1. schedule a target worker
//  2. push /spawn to that worker and wait for confirmation
//  3. only persist a "running" record once the worker confirms the container is up
func handleDeploy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	userID := userFromRequest(r)
	if userID == "" {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if bal := balance(userID); bal < deployFee {
		writeJSON(w, http.StatusPaymentRequired, map[string]interface{}{
			"error":    "insufficient credits",
			"credits":  bal,
			"required": deployFee,
		})
		return
	}
	var req deployRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Image == "" {
		http.Error(w, "image is required", http.StatusBadRequest)
		return
	}
	if req.InternalPort == 0 {
		req.InternalPort = 80
	}

	mu.Lock()
	workerID, workerIP, err := pickWorker()
	mu.Unlock()
	if err != nil {
		http.Error(w, "No healthy peer nodes available right now.", http.StatusServiceUnavailable)
		return
	}

	appID := fmt.Sprintf("app-%d", time.Now().UnixNano())

	// Persist an interim "provisioning" record so the app is traceable even if spawn fails.
	mu.Lock()
	_, err = db.Exec(`INSERT INTO apps (id, name, image, worker_id, owner_id, port, status)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, appID, req.Name, req.Image, workerID, userID, 0, "provisioning")
	mu.Unlock()
	if err != nil {
		http.Error(w, "Failed to persist application state", http.StatusInternalServerError)
		return
	}

	// Push the spawn command to the worker: over the reverse tunnel when the
	// node is behind NAT, otherwise by direct HTTP to its daemon.
	spawnReq := spawnRequest{AppID: appID, Image: req.Image, InternalPort: req.InternalPort}
	var hostPort int
	var spawnErr error
	if hasControl(workerID) {
		hostPort, spawnErr = spawnViaControl(workerID, spawnReq)
	} else {
		hostPort, spawnErr = spawnOnWorker(workerIP, appID, req.Image, req.InternalPort)
	}
	if spawnErr != nil {
		mu.Lock()
		_, _ = db.Exec(`UPDATE apps SET status = ? WHERE id = ?`, "failed", appID)
		mu.Unlock()
		log.Printf("app %s FAILED to spawn on %s: %v", appID, workerID, spawnErr)
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{
			"app_id": appID,
			"status": "failed",
			"error":  spawnErr.Error(),
		})
		return
	}

	// Worker confirmed the container is running: commit the verified record.
	mu.Lock()
	_, err = db.Exec(`UPDATE apps SET port = ?, status = ? WHERE id = ?`,
		hostPort, "running", appID)
	if err == nil {
		_, _ = db.Exec(`UPDATE workers SET active_apps = active_apps + 1 WHERE id = ?`, workerID)
		_ = recordCreditRef(userID, -deployFee, reasonDeployFee, appID, sql.NullString{})
	}
	mu.Unlock()
	if err != nil {
		http.Error(w, "Failed to commit verified deployment", http.StatusInternalServerError)
		return
	}

	log.Printf("app %s verified running on node %s at %s:%d (owner %s, charged %d)",
		appID, workerID, workerIP, hostPort, userID, deployFee)
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"app_id":        appID,
		"worker_id":     workerID,
		"owner_id":      userID,
		"target_ip":     workerIP,
		"host_port":     hostPort,
		"internal_port": req.InternalPort,
		"status":        "running",
		"charged":       deployFee,
		"credits":       balance(userID),
		"url":           appURL(appID),
	})
}

// spawnOnWorker performs the outbound HTTP call to a worker daemon's /spawn endpoint.
func spawnOnWorker(workerIP, appID, image string, internalPort int) (int, error) {
	body, _ := json.Marshal(spawnRequest{AppID: appID, Image: image, InternalPort: internalPort})
	endpoint := strings.TrimRight(workerIP, "/") + "/spawn"

	resp, err := httpClient.Post(endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("worker unreachable at %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	var sr spawnResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return 0, fmt.Errorf("invalid worker response: %w", err)
	}
	if resp.StatusCode != http.StatusOK || sr.Status != "running" || sr.Port == 0 {
		msg := sr.Error
		if msg == "" {
			msg = sr.Message
		}
		if msg == "" {
			msg = fmt.Sprintf("worker returned status %d", resp.StatusCode)
		}
		return 0, fmt.Errorf("spawn rejected: %s", msg)
	}
	return sr.Port, nil
}

// handleLookup resolves an app id to the reachable worker address for the mesh proxy.
func handleLookup(w http.ResponseWriter, r *http.Request) {
	appID := strings.TrimPrefix(r.URL.Path, "/api/lookup/")
	if appID == "" {
		http.Error(w, "app id required", http.StatusBadRequest)
		return
	}
	var targetHostIP, status, workerID string
	var port int
	err := db.QueryRow(`SELECT workers.host_ip, apps.port, apps.status, apps.worker_id
		FROM apps JOIN workers ON apps.worker_id = workers.id
		WHERE apps.id = ?`, appID).Scan(&targetHostIP, &port, &status, &workerID)
	if err != nil {
		http.Error(w, "App mapping not found", http.StatusNotFound)
		return
	}
	if status != "running" || port == 0 {
		http.Error(w, fmt.Sprintf("App not running (status=%s)", status), http.StatusServiceUnavailable)
		return
	}
	tunneled := hasControl(workerID)
	if !tunneled && targetHostIP == "" {
		http.Error(w, "Worker did not report host_ip", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"app_id":    appID,
		"worker_id": workerID,
		"host_ip":   targetHostIP,
		"port":      port,
		"tunneled":  tunneled,
	})
}

// handleWorkers lists ledger state (handy for verification).
func handleWorkers(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(`SELECT id, ip, host_ip, cpu, ram, disk_gb, active_apps, last_seen FROM workers ORDER BY id`)
	if err != nil {
		http.Error(w, "DB error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	out := []map[string]interface{}{}
	for rows.Next() {
		var id, ip, hostIP, lastSeen sql.NullString
		var cpu, ram, disk, apps int
		if err := rows.Scan(&id, &ip, &hostIP, &cpu, &ram, &disk, &apps, &lastSeen); err != nil {
			continue
		}
		out = append(out, map[string]interface{}{
			"id": id.String, "ip": ip.String, "host_ip": hostIP.String, "cpu_cores": cpu,
			"ram_mb": ram, "disk_gb": disk, "active_apps": apps, "last_seen": lastSeen.String,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleApps lists deployed applications (handy for verification). Pass
// `?mine=1` with a session to list only the caller's apps.
func handleApps(w http.ResponseWriter, r *http.Request) {
	query := `SELECT id, name, image, worker_id, owner_id, port, status FROM apps`
	args := []interface{}{}
	if r.URL.Query().Get("mine") == "1" {
		userID := userFromRequest(r)
		if userID == "" {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		query += ` WHERE owner_id = ?`
		args = append(args, userID)
	}
	query += ` ORDER BY created_at DESC`

	rows, err := db.Query(query, args...)
	if err != nil {
		http.Error(w, "DB error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	out := []map[string]interface{}{}
	for rows.Next() {
		var id, name, image, wid, owner, status sql.NullString
		var port int
		if err := rows.Scan(&id, &name, &image, &wid, &owner, &port, &status); err != nil {
			continue
		}
		out = append(out, map[string]interface{}{
			"id": id.String, "name": name.String, "image": image.String,
			"worker_id": wid.String, "owner_id": owner.String,
			"port": port, "status": status.String, "url": appURL(id.String),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func main() {
	initDB()
	defer db.Close()

	http.HandleFunc("/api/worker/heartbeat", handleHeartbeat)
	http.HandleFunc("/api/worker/control", handleWorkerControl)
	http.HandleFunc("/api/worker/reverse", handleWorkerReverse)
	http.HandleFunc("/api/tunnel/acquire", handleTunnelAcquire)
	http.HandleFunc("/api/developer/deploy", handleDeploy)
	http.HandleFunc("/api/lookup/", handleLookup)
	http.HandleFunc("/api/workers", handleWorkers)
	http.HandleFunc("/api/apps", handleApps)

	http.HandleFunc("/api/auth/request", handleAuthRequest)
	http.HandleFunc("/api/auth/verify", handleAuthVerify)
	http.HandleFunc("/api/auth/me", handleAuthMe)
	http.HandleFunc("/api/auth/logout", handleAuthLogout)
	http.HandleFunc("/api/credits/balance", handleBalance)
	http.HandleFunc("/api/credits/history", handleHistory)

	http.HandleFunc("/api/config", handleConfig)
	http.HandleFunc("/api/nodes/claim", handleClaimCode)
	http.HandleFunc("/api/nodes", handleMyNodes)

	startRewardLoop()

	log.Printf("WeCloud Core Orchestrator running on port :8080 (apps served at %s://<app-id>.%s)...", publicScheme(), baseDomain())
	log.Fatal(http.ListenAndServe(":8080", nil))
}
