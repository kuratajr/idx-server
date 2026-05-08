package database

import (
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/nezhahq/nezha/internal/xtpro/models"

	_ "modernc.org/sqlite"
)

type Database struct {
	db *sql.DB
}

func NewDatabase(dsn string) (*Database, error) {
	if dsn == "" {
		dsn = "./xtpro.db"
	}
	dsn = buildSQLiteDSN(dsn)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)

	database := &Database{db: db}
	if err := database.migrate(); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	go database.cleanupOldConnections()
	log.Println("[database] SQLite3 initialized with optimizations")
	return database, nil
}

func buildSQLiteDSN(input string) string {
	if strings.HasPrefix(input, "file:") {
		return ensureSQLiteQuery(input)
	}
	return ensureSQLiteQuery("file:" + input)
}

func ensureSQLiteQuery(fileURI string) string {
	u, err := url.Parse(fileURI)
	if err != nil {
		return fileURI
	}

	q := u.Query()
	q.Set("cache", "shared")
	q.Set("mode", "rwc")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "cache_size(-64000)")
	q.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	return u.String()
}

func (d *Database) migrate() error {
	queries := []string{
		`PRAGMA foreign_keys = ON;`,
		`
		CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
			username TEXT UNIQUE NOT NULL,
			email TEXT UNIQUE NOT NULL,
			password TEXT NOT NULL,
			role TEXT DEFAULT 'user',
			api_key TEXT UNIQUE,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		`,
		`
		CREATE TABLE IF NOT EXISTS tunnels (
			id TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			protocol TEXT NOT NULL CHECK (protocol IN ('tcp', 'udp', 'http')),
			local_host TEXT NOT NULL,
			local_port INTEGER NOT NULL CHECK (local_port > 0 AND local_port < 65536),
			public_port INTEGER UNIQUE,
			status TEXT DEFAULT 'inactive',
			client_id TEXT,
			auth_token TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			last_seen DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		`,
		`
		CREATE TABLE IF NOT EXISTS connections (
			id TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
			tunnel_id TEXT NOT NULL REFERENCES tunnels(id) ON DELETE CASCADE,
			remote_addr TEXT NOT NULL,
			connected_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			disconnected_at DATETIME,
			bytes_up INTEGER DEFAULT 0,
			bytes_down INTEGER DEFAULT 0,
			duration INTEGER DEFAULT 0
		);
		`,
		`
		CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);
		CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);
		CREATE INDEX IF NOT EXISTS idx_users_api_key ON users(api_key);
		CREATE INDEX IF NOT EXISTS idx_tunnels_user_id ON tunnels(user_id);
		CREATE INDEX IF NOT EXISTS idx_tunnels_public_port ON tunnels(public_port);
		CREATE INDEX IF NOT EXISTS idx_tunnels_status ON tunnels(status);
		CREATE INDEX IF NOT EXISTS idx_connections_tunnel_id ON connections(tunnel_id);
		CREATE INDEX IF NOT EXISTS idx_connections_connected_at ON connections(connected_at);
		`,
		`
		CREATE TRIGGER IF NOT EXISTS update_users_updated_at
		AFTER UPDATE ON users
		FOR EACH ROW
		BEGIN
			UPDATE users SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
		END;
		`,
		`
		CREATE TRIGGER IF NOT EXISTS update_tunnels_updated_at
		AFTER UPDATE ON tunnels
		FOR EACH ROW
		BEGIN
			UPDATE tunnels SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
		END;
		`,
	}

	for _, query := range queries {
		if _, err := d.db.Exec(query); err != nil {
			return fmt.Errorf("failed to execute migration query: %w", err)
		}
	}

	log.Println("[database] SQLite3 migration completed successfully")
	return nil
}

func (d *Database) Close() error { return d.db.Close() }

func (d *Database) CreateUser(user *models.User) error {
	query := `
		INSERT INTO users (username, email, password, role, api_key)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at, updated_at
	`
	return d.db.QueryRow(query, user.Username, user.Email, user.Password, user.Role, user.APIKey).
		Scan(&user.ID, &user.CreatedAt, &user.UpdatedAt)
}

func (d *Database) GetUserByUsername(username string) (*models.User, error) {
	user := &models.User{}
	query := `
		SELECT id, username, email, password, role, api_key, created_at, updated_at
		FROM users WHERE username = $1
	`
	err := d.db.QueryRow(query, username).Scan(
		&user.ID, &user.Username, &user.Email, &user.Password,
		&user.Role, &user.APIKey, &user.CreatedAt, &user.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (d *Database) GetUserByAPIKey(apiKey string) (*models.User, error) {
	user := &models.User{}
	query := `
		SELECT id, username, email, password, role, api_key, created_at, updated_at
		FROM users WHERE api_key = $1
	`
	err := d.db.QueryRow(query, apiKey).Scan(
		&user.ID, &user.Username, &user.Email, &user.Password,
		&user.Role, &user.APIKey, &user.CreatedAt, &user.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (d *Database) GetAllUsers() ([]*models.User, error) {
	query := `
		SELECT id, username, email, password, role, api_key, created_at, updated_at
		FROM users ORDER BY created_at DESC
	`
	rows, err := d.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []*models.User
	for rows.Next() {
		user := &models.User{}
		if err := rows.Scan(
			&user.ID, &user.Username, &user.Email, &user.Password,
			&user.Role, &user.APIKey, &user.CreatedAt, &user.UpdatedAt,
		); err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (d *Database) GetAllTunnels() ([]*models.Tunnel, error) {
	query := `
		SELECT id, user_id, name, protocol, local_host, local_port, public_port,
			   status, client_id, auth_token, created_at, updated_at, last_seen
		FROM tunnels ORDER BY created_at DESC
	`
	rows, err := d.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tunnels []*models.Tunnel
	for rows.Next() {
		tunnel := &models.Tunnel{}
		if err := rows.Scan(
			&tunnel.ID, &tunnel.UserID, &tunnel.Name, &tunnel.Protocol,
			&tunnel.LocalHost, &tunnel.LocalPort, &tunnel.PublicPort,
			&tunnel.Status, &tunnel.ClientID, &tunnel.AuthToken,
			&tunnel.CreatedAt, &tunnel.UpdatedAt, &tunnel.LastSeen,
		); err != nil {
			return nil, err
		}
		tunnels = append(tunnels, tunnel)
	}
	return tunnels, rows.Err()
}

func (d *Database) GetTunnelsByUserID(userID string) ([]*models.Tunnel, error) {
	query := `
		SELECT id, user_id, name, protocol, local_host, local_port, public_port,
			   status, client_id, auth_token, created_at, updated_at, last_seen
		FROM tunnels WHERE user_id = $1 ORDER BY created_at DESC
	`
	rows, err := d.db.Query(query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tunnels []*models.Tunnel
	for rows.Next() {
		tunnel := &models.Tunnel{}
		if err := rows.Scan(
			&tunnel.ID, &tunnel.UserID, &tunnel.Name, &tunnel.Protocol,
			&tunnel.LocalHost, &tunnel.LocalPort, &tunnel.PublicPort,
			&tunnel.Status, &tunnel.ClientID, &tunnel.AuthToken,
			&tunnel.CreatedAt, &tunnel.UpdatedAt, &tunnel.LastSeen,
		); err != nil {
			return nil, err
		}
		tunnels = append(tunnels, tunnel)
	}
	return tunnels, rows.Err()
}

func (d *Database) GetTunnelByID(tunnelID string) (*models.Tunnel, error) {
	tunnel := &models.Tunnel{}
	query := `
		SELECT id, user_id, name, protocol, local_host, local_port, public_port,
			   status, client_id, auth_token, created_at, updated_at, last_seen
		FROM tunnels WHERE id = $1
	`
	err := d.db.QueryRow(query, tunnelID).Scan(
		&tunnel.ID, &tunnel.UserID, &tunnel.Name, &tunnel.Protocol,
		&tunnel.LocalHost, &tunnel.LocalPort, &tunnel.PublicPort,
		&tunnel.Status, &tunnel.ClientID, &tunnel.AuthToken,
		&tunnel.CreatedAt, &tunnel.UpdatedAt, &tunnel.LastSeen,
	)
	if err != nil {
		return nil, err
	}
	return tunnel, nil
}

func (d *Database) GetMetrics() (*models.Metrics, error) {
	metrics := &models.Metrics{}

	if err := d.db.QueryRow(`SELECT COUNT(*) FROM tunnels WHERE status = 'active'`).Scan(&metrics.ActiveTunnels); err != nil {
		return nil, err
	}
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM connections`).Scan(&metrics.TotalConnections); err != nil {
		return nil, err
	}
	if err := d.db.QueryRow(`SELECT COALESCE(SUM(bytes_up), 0) FROM connections`).Scan(&metrics.TotalBytesUp); err != nil {
		return nil, err
	}
	if err := d.db.QueryRow(`SELECT COALESCE(SUM(bytes_down), 0) FROM connections`).Scan(&metrics.TotalBytesDown); err != nil {
		return nil, err
	}
	if err := d.db.QueryRow(`
		SELECT COUNT(DISTINCT t.user_id)
		FROM tunnels t
		WHERE t.status = 'active' AND t.last_seen > datetime('now', '-1 hour')
	`).Scan(&metrics.ActiveUsers); err != nil {
		return nil, err
	}

	return metrics, nil
}

