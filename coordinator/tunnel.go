package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Reverse-tunnel hub. A worker behind NAT keeps outbound connections open to the
// coordinator: one *control* connection (spawn commands + telemetry) and a pool
// of *reverse* streams (raw byte pipes used to serve app HTTP traffic). This is
// what lets a home/office node with no port-forwarding host real apps.

var (
	controlsMu sync.RWMutex
	controls   = map[string]*controlConn{}

	reverseMu sync.Mutex
	reverse   = map[string]chan net.Conn{}
)

var errNoControl = errors.New("worker has no control tunnel")

type controlConn struct {
	workerID string
	conn     net.Conn
	wmu      sync.Mutex
	pmu      sync.Mutex
	pend     map[int64]chan spawnResponse
	nextID   int64
}

func hasControl(workerID string) bool {
	controlsMu.RLock()
	defer controlsMu.RUnlock()
	_, ok := controls[workerID]
	return ok
}

// authenticateWorker validates a tunnel connection's worker id + secret. On
// first contact (secret not yet recorded) it trusts and stores the secret, since
// the worker registration heartbeat carries it too.
func authenticateWorker(id, secret string) bool {
	if id == "" || secret == "" {
		return false
	}
	var dbSecret sql.NullString
	err := db.QueryRow(`SELECT secret FROM workers WHERE id = ?`, id).Scan(&dbSecret)
	if err == sql.ErrNoRows {
		return false // worker must heartbeat (or have registered) at least once
	}
	if err != nil {
		return false
	}
	if !dbSecret.Valid || dbSecret.String == "" {
		mu.Lock()
		_, _ = db.Exec(`UPDATE workers SET secret = ? WHERE id = ?`, secret, id)
		mu.Unlock()
		return true
	}
	return dbSecret.String == secret
}

// handleWorkerControl upgrades an outbound worker connection into the control
// channel used to push spawn commands and receive results.
func handleWorkerControl(w http.ResponseWriter, r *http.Request) {
	id := r.Header.Get("X-Worker-Id")
	if !authenticateWorker(id, r.Header.Get("X-Worker-Secret")) {
		w.Header().Set("Connection", "close")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		return
	}
	cc := &controlConn{workerID: id, conn: conn, pend: map[int64]chan spawnResponse{}}
	controlsMu.Lock()
	if old := controls[id]; old != nil {
		_ = old.conn.Close()
	}
	controls[id] = cc
	controlsMu.Unlock()
	log.Printf("tunnel control connected: %s", id)
	go cc.readLoop()
}

func (cc *controlConn) readLoop() {
	defer func() {
		controlsMu.Lock()
		if controls[cc.workerID] == cc {
			delete(controls, cc.workerID)
		}
		controlsMu.Unlock()
		_ = cc.conn.Close()
		log.Printf("tunnel control disconnected: %s", cc.workerID)
	}()

	sc := bufio.NewScanner(cc.conn)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := append([]byte(nil), sc.Bytes()...)
		var env struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(line, &env) != nil {
			continue
		}
		switch env.Type {
		case "heartbeat":
			var wk Worker
			if json.Unmarshal(line, &wk) == nil && wk.ID != "" {
				_ = upsertWorker(&wk)
			}
		case "spawn_result":
			var sr struct {
				ID     int64  `json:"id"`
				Status string `json:"status"`
				Port   int    `json:"port"`
				Error  string `json:"error"`
			}
			if json.Unmarshal(line, &sr) != nil {
				continue
			}
			cc.pmu.Lock()
			ch := cc.pend[sr.ID]
			delete(cc.pend, sr.ID)
			cc.pmu.Unlock()
			if ch != nil {
				ch <- spawnResponse{Status: sr.Status, Port: sr.Port, Error: sr.Error}
			}
		}
	}
}

func (cc *controlConn) writeLine(v interface{}) error {
	b, _ := json.Marshal(v)
	b = append(b, '\n')
	cc.wmu.Lock()
	defer cc.wmu.Unlock()
	_, err := cc.conn.Write(b)
	return err
}

// spawnViaControl pushes a spawn command over the tunnel and waits for the
// worker's result. Used for nodes that are not directly reachable.
func spawnViaControl(workerID string, req spawnRequest) (int, error) {
	controlsMu.RLock()
	cc := controls[workerID]
	controlsMu.RUnlock()
	if cc == nil {
		return 0, errNoControl
	}
	id := atomic.AddInt64(&cc.nextID, 1)
	ch := make(chan spawnResponse, 1)
	cc.pmu.Lock()
	cc.pend[id] = ch
	cc.pmu.Unlock()

	if err := cc.writeLine(map[string]interface{}{
		"type": "spawn", "id": id, "app_id": req.AppID,
		"image": req.Image, "internal_port": req.InternalPort,
	}); err != nil {
		cc.pmu.Lock()
		delete(cc.pend, id)
		cc.pmu.Unlock()
		return 0, err
	}

	select {
	case resp := <-ch:
		if resp.Status != "running" || resp.Port == 0 {
			msg := resp.Error
			if msg == "" {
				msg = "worker returned " + resp.Status
			}
			return 0, fmt.Errorf("spawn rejected: %s", msg)
		}
		return resp.Port, nil
	case <-time.After(300 * time.Second):
		cc.pmu.Lock()
		delete(cc.pend, id)
		cc.pmu.Unlock()
		return 0, fmt.Errorf("spawn timed out over tunnel")
	}
}

// handleWorkerReverse parks an idle outbound worker connection in the pool.
func handleWorkerReverse(w http.ResponseWriter, r *http.Request) {
	id := r.Header.Get("X-Worker-Id")
	if !authenticateWorker(id, r.Header.Get("X-Worker-Secret")) {
		w.Header().Set("Connection", "close")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		return
	}
	reverseMu.Lock()
	ch := reverse[id]
	if ch == nil {
		ch = make(chan net.Conn, 16)
		reverse[id] = ch
	}
	reverseMu.Unlock()
	select {
	case ch <- conn:
	default:
		_ = conn.Close() // pool is full; drop the extra stream
	}
}

func acquireReverse(workerID string, timeout time.Duration) (net.Conn, error) {
	reverseMu.Lock()
	ch := reverse[workerID]
	if ch == nil {
		ch = make(chan net.Conn, 16)
		reverse[workerID] = ch
	}
	reverseMu.Unlock()
	select {
	case c := <-ch:
		return c, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("no reverse stream available for %s", workerID)
	}
}

// handleTunnelAcquire bridges a proxy connection to a worker reverse stream.
// The proxy speaks plain HTTP over the returned raw connection.
func handleTunnelAcquire(w http.ResponseWriter, r *http.Request) {
	workerID := r.URL.Query().Get("worker")
	port := r.URL.Query().Get("port")
	if workerID == "" || port == "" {
		http.Error(w, "worker and port are required", http.StatusBadRequest)
		return
	}
	stream, err := acquireReverse(workerID, 30*time.Second)
	if err != nil {
		log.Printf("tunnel acquire failed for %s:%s: %v", workerID, port, err)
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		_ = stream.Close()
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	pconn, brw, err := hj.Hijack()
	if err != nil {
		_ = stream.Close()
		return
	}
	// Tell the worker which local port to relay to, then pipe bytes both ways.
	// Read via brw so any request bytes already buffered by net/http are kept.
	if _, err := stream.Write([]byte("PORT " + port + "\n")); err != nil {
		_ = stream.Close()
		_ = pconn.Close()
		return
	}
	go func() {
		_, _ = io.Copy(stream, brw)
		_ = stream.Close()
	}()
	_, _ = io.Copy(pconn, stream)
	_ = pconn.Close()
}

// upsertWorker is the single place that persists worker telemetry, shared by the
// HTTP heartbeat and tunnel control heartbeats.
func upsertWorker(worker *Worker) error {
	mu.Lock()
	_, err := db.Exec(`INSERT INTO workers (id, ip, host_ip, cpu, ram, disk_gb, active_apps, secret, last_seen)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(id) DO UPDATE SET
			ip=excluded.ip,
			host_ip=excluded.host_ip,
			cpu=excluded.cpu,
			ram=excluded.ram,
			disk_gb=excluded.disk_gb,
			active_apps=excluded.active_apps,
			secret=COALESCE(NULLIF(excluded.secret, ''), workers.secret),
			last_seen=excluded.last_seen`,
		worker.ID, worker.IP, worker.HostIP, worker.CPUCores, worker.RAM, worker.DiskGB, worker.ActiveApps, worker.Secret)
	mu.Unlock()
	if err == nil && worker.OwnerCode != "" {
		claimNode(worker.ID, worker.OwnerCode)
	}
	return err
}
