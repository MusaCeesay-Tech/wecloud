package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// Reward economy for volunteer hosts: a node earns 1 whole credit per
// `secondsPerCredit` seconds (default one hour) that it stays online AND claimed
// by an owner. Accrual is tracked in milliseconds so partial hours carry over
// instead of being lost or rounded up.
func rewardIntervalSeconds() int { return envInt("WECLOUD_REWARD_INTERVAL", 60) }
func secondsPerCredit() int      { return envInt("WECLOUD_REWARD_SECONDS_PER_CREDIT", 3600) }

const timeLayout = "2006-01-02 15:04:05"

// handleClaimCreate mints a short-lived node claim code bound to the caller.
// The operator starts a worker with WECLOUD_OWNER_CODE=<code> to bind it to
// their account so it starts earning host rewards.
func handleClaimCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	userID := userFromRequest(r)
	if userID == "" {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	code := strings.ToUpper(randomToken()[:8])
	mu.Lock()
	_, err := db.Exec(`INSERT INTO claim_codes (code, user_id) VALUES (?, ?)`, code, userID)
	mu.Unlock()
	if err != nil {
		http.Error(w, "could not create claim code", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"code": code,
		"how":  fmt.Sprintf("Start the worker with WECLOUD_OWNER_CODE=%s to earn host rewards from this node.", code),
	})
}

// handleMyNodes lists the authenticated user's claimed worker nodes.
func handleMyNodes(w http.ResponseWriter, r *http.Request) {
	userID := userFromRequest(r)
	if userID == "" {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}
	rows, err := db.Query(`SELECT id, host_ip, cpu, ram, disk_gb, active_apps, COALESCE(reward_ms,0), last_seen
		FROM workers WHERE owner_id = ? ORDER BY id`, userID)
	if err != nil {
		http.Error(w, "DB error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	nodes := []map[string]interface{}{}
	for rows.Next() {
		var id, hostIP, lastSeen sql.NullString
		var cpu, ram, disk, apps, rewardMs int
		if err := rows.Scan(&id, &hostIP, &cpu, &ram, &disk, &apps, &rewardMs, &lastSeen); err != nil {
			continue
		}
		nodes = append(nodes, map[string]interface{}{
			"id": id.String, "host_ip": hostIP.String, "cpu_cores": cpu, "ram_mb": ram,
			"disk_gb": disk, "active_apps": apps, "reward_progress_ms": rewardMs,
			"last_seen": lastSeen.String,
		})
	}
	rows.Close()

	var rewarded sql.NullInt64
	_ = db.QueryRow(`SELECT SUM(delta) FROM ledger WHERE user_id = ? AND reason = ?`,
		userID, reasonHostReward).Scan(&rewarded)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"user_id":          userID,
		"nodes":            nodes,
		"rewarded_credits": int(rewarded.Int64),
	})
}

// startRewardLoop periodically credits node owners for uptime.
func startRewardLoop() {
	interval := envInt("WECLOUD_REWARD_INTERVAL", 60)
	spc := envInt("WECLOUD_REWARD_SECONDS_PER_CREDIT", 3600)
	go func() {
		ticker := time.NewTicker(time.Duration(interval) * time.Second)
		defer ticker.Stop()
		log.Printf("host reward loop started: 1 credit per %ds online per node (tick %ds)", spc, interval)
		for range ticker.C {
			creditOnlineNodes(spc)
		}
	}()
}

// creditOnlineNodes advances the uptime accumulator for every owned node and
// emits whole-credit payouts when the accumulator crosses the rate.
func creditOnlineNodes(secPerCredit int) {
	type nodeRow struct {
		id        string
		owner     string
		lastSeen  time.Time
		rewardMs  int
		checkedAt time.Time
		hasCheck  bool
	}

	mu.Lock()
	var snapshot []nodeRow
	rows, err := db.Query(`SELECT id, owner_id, last_seen, COALESCE(reward_ms,0), reward_checked_at
		FROM workers WHERE owner_id IS NOT NULL AND owner_id != ''`)
	if err != nil {
		mu.Unlock()
		log.Printf("reward loop query failed: %v", err)
		return
	}
	for rows.Next() {
		var id, owner, lastSeen, checkedAt sql.NullString
		var rewardMs int
		if err := rows.Scan(&id, &owner, &lastSeen, &rewardMs, &checkedAt); err != nil {
			continue
		}
		n := nodeRow{id: id.String, owner: owner.String, rewardMs: rewardMs}
		n.lastSeen = parseDBTime(lastSeen.String)
		if t, ok := parseDBTimeOK(checkedAt.String); ok {
			n.checkedAt = t
			n.hasCheck = true
		}
		snapshot = append(snapshot, n)
	}
	rows.Close()

	// Apply updates only after the read cursor is closed (avoids SQLite writer lock).
	now := time.Now().UTC()
	threshold := int64(secPerCredit) * 1000
	for _, n := range snapshot {
		if !n.hasCheck {
			_, _ = db.Exec(`UPDATE workers SET reward_checked_at = ? WHERE id = ?`,
				now.Format(time.RFC3339), n.id)
			continue
		}
		deltaMs := now.Sub(n.checkedAt).Milliseconds()
		if deltaMs < 0 {
			deltaMs = 0
		}
		online := now.Sub(n.lastSeen) <= 60*time.Second
		acc := n.rewardMs
		if online {
			acc += int(deltaMs)
		}
		credits := 0
		for int64(acc) >= threshold {
			credits++
			acc -= int(threshold)
		}
		if credits > 0 {
			if err := recordCreditRef(n.owner, credits, reasonHostReward, "", sql.NullString{}); err == nil {
				log.Printf("host reward: %s earned %d credit(s) for node %s", n.owner, credits, n.id)
			}
		}
		_, _ = db.Exec(`UPDATE workers SET reward_ms = ?, reward_checked_at = ? WHERE id = ?`,
			acc, now.Format(time.RFC3339), n.id)
	}
	mu.Unlock()
}

// parseDBTimeOK accepts the timestamp shapes SQLite and the driver may return.
func parseDBTimeOK(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func parseDBTime(s string) time.Time {
	t, _ := parseDBTimeOK(s)
	return t
}

// claimNode links a worker to the user that owns a one-time claim code.
func claimNode(workerID, code string) {
	mu.Lock()
	defer mu.Unlock()
	var owner string
	err := db.QueryRow(`SELECT user_id FROM claim_codes WHERE code = ? AND used = 0`, code).Scan(&owner)
	if err != nil {
		return
	}
	_, _ = db.Exec(`UPDATE workers SET owner_id = ? WHERE id = ?`, owner, workerID)
	_, _ = db.Exec(`UPDATE claim_codes SET used = 1, used_at = datetime('now') WHERE code = ?`, code)
	log.Printf("node %s claimed by %s via code %s", workerID, owner, code)
}
