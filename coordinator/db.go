package main

import (
	"database/sql"
	"log"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

var (
	db *sql.DB
	mu sync.Mutex
)

func initDB() {
	var err error
	db, err = sql.Open("sqlite", "./wecloud.db")
	if err != nil {
		log.Fatal(err)
	}
	db.SetMaxOpenConns(1) // SQLite: serialize writers to avoid SQLITE_BUSY

	schema := `
	CREATE TABLE IF NOT EXISTS workers (
		id          TEXT PRIMARY KEY,
		ip          TEXT NOT NULL,
		host_ip     TEXT DEFAULT '',
		cpu         INTEGER NOT NULL,
		ram         INTEGER NOT NULL,
		disk_gb     INTEGER DEFAULT 0,
		active_apps INTEGER DEFAULT 0,
		owner_id    TEXT,
		last_seen   DATETIME
	);
	CREATE TABLE IF NOT EXISTS apps (
		id          TEXT PRIMARY KEY,
		name        TEXT,
		image       TEXT,
		worker_id   TEXT,
		owner_id    TEXT,
		port        INTEGER,
		status      TEXT,
		created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY(worker_id) REFERENCES workers(id)
	);
	CREATE TABLE IF NOT EXISTS users (
		id          TEXT PRIMARY KEY,
		email       TEXT UNIQUE NOT NULL,
		created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS magic_links (
		token       TEXT PRIMARY KEY,
		email       TEXT NOT NULL,
		expires_at  DATETIME NOT NULL,
		used        INTEGER NOT NULL DEFAULT 0,
		created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS sessions (
		token       TEXT PRIMARY KEY,
		user_id     TEXT NOT NULL,
		expires_at  DATETIME NOT NULL,
		created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY(user_id) REFERENCES users(id)
	);
	CREATE TABLE IF NOT EXISTS ledger (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id     TEXT NOT NULL,
		delta       INTEGER NOT NULL,
		reason      TEXT NOT NULL,
		app_id      TEXT,
		created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY(user_id) REFERENCES users(id)
	);
	CREATE INDEX IF NOT EXISTS idx_ledger_user ON ledger(user_id);
	CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);`
	if _, err = db.Exec(schema); err != nil {
		log.Fatalf("Database setup error: %v", err)
	}

	// Additive migrations for databases created before these columns existed.
	migrate("ALTER TABLE apps ADD COLUMN owner_id TEXT")
	migrate("ALTER TABLE workers ADD COLUMN owner_id TEXT")
}

// migrate runs a best-effort additive change, ignoring "duplicate column" errors.
func migrate(stmt string) {
	if _, err := db.Exec(stmt); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") {
			log.Printf("migration failed: %v", err)
		}
	}
}
