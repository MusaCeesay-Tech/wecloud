package main

import (
	"database/sql"
	"net/http"
)

const (
	signupBonus         = 100 // granted once when an account is created
	deployFee           = 1   // charged when an app is successfully deployed
	referralSignupBonus = 10  // default referral credit for referred signup
	referralActivity    = 5   // default referral credit for referred activity
)

const (
	reasonSignupBonus     = "signup_bonus"
	reasonDeployFee       = "deploy_fee"
	reasonHostReward      = "host_reward"      // earned for hosting (uptime)
	reasonReferralSignup  = "referral_signup"  // credited to referrer on eligible signup
	reasonReferralActivity = "referral_activity" // credited to referrer on eligible activity
)

// balance returns the sum of a user's ledger deltas.
func balance(userID string) int {
	var total sql.NullInt64
	_ = db.QueryRow(`SELECT SUM(delta) FROM ledger WHERE user_id = ?`, userID).Scan(&total)
	return int(total.Int64)
}

// recordCreditRef appends a ledger entry with optional referral attribution.
func recordCreditRef(userID string, delta int, reason, appID string, refUserID sql.NullString) error {
	_, err := db.Exec(`INSERT INTO ledger (user_id, delta, reason, app_id, referral_user_id) VALUES (?, ?, ?, ?, ?)`,
		userID, delta, reason, appID, refUserID)
	return err
}

func handleBalance(w http.ResponseWriter, r *http.Request) {
	userID := userFromRequest(r)
	if userID == "" {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"user_id": userID,
		"credits": balance(userID),
	})
}

func handleHistory(w http.ResponseWriter, r *http.Request) {
	userID := userFromRequest(r)
	if userID == "" {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}
	rows, err := db.Query(`SELECT delta, reason, app_id, created_at FROM ledger
		WHERE user_id = ? ORDER BY id DESC LIMIT 100`, userID)
	if err != nil {
		http.Error(w, "DB error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	out := []map[string]interface{}{}
	for rows.Next() {
		var delta int
		var reason, appID, createdAt sql.NullString
		if err := rows.Scan(&delta, &reason, &appID, &createdAt); err != nil {
			continue
		}
		out = append(out, map[string]interface{}{
			"delta":      delta,
			"reason":     reason.String,
			"app_id":     appID.String,
			"created_at": createdAt.String,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"user_id": userID,
		"credits": balance(userID),
		"entries": out,
	})
}
