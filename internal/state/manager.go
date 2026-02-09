package state

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Manager handles local state persistence
type Manager struct {
	db *sql.DB
}

// NewManager creates a new state manager
func NewManager(dbPath string) (*Manager, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	return &Manager{db: db}, nil
}

// Close closes the database connection
func (m *Manager) Close() error {
	return m.db.Close()
}

// migrate creates the database schema
func migrate(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS applied_state (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		stack_version INTEGER NOT NULL,
		state_hash TEXT NOT NULL,
		applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS service_processes (
		service_id TEXT PRIMARY KEY,
		service_name TEXT NOT NULL,
		git_commit TEXT NOT NULL,
		runtime TEXT NOT NULL DEFAULT 'process',
		container_id TEXT,
		container_name TEXT,
		image_tag TEXT,
		pid INTEGER,
		status TEXT NOT NULL DEFAULT 'stopped',
		restart_count INTEGER DEFAULT 0,
		last_error TEXT,
		started_at DATETIME,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS service_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		service_id TEXT NOT NULL,
		level TEXT NOT NULL,
		message TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_service_logs_service_id ON service_logs(service_id);
	CREATE INDEX IF NOT EXISTS idx_service_logs_created_at ON service_logs(created_at);
	`

	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}

	if err := ensureServiceProcessColumns(db); err != nil {
		return err
	}

	return nil
}

func ensureServiceProcessColumns(db *sql.DB) error {
	columns := map[string]string{
		"runtime":        "TEXT NOT NULL DEFAULT 'process'",
		"container_id":   "TEXT",
		"container_name": "TEXT",
		"image_tag":      "TEXT",
	}

	rows, err := db.Query("PRAGMA table_info(service_processes)")
	if err != nil {
		return fmt.Errorf("failed to inspect service_processes: %w", err)
	}
	defer rows.Close()

	existing := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			return fmt.Errorf("failed to read service_processes columns: %w", err)
		}
		existing[name] = true
	}

	for name, definition := range columns {
		if existing[name] {
			continue
		}
		stmt := fmt.Sprintf("ALTER TABLE service_processes ADD COLUMN %s %s", name, definition)
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("failed to add column %s: %w", name, err)
		}
	}

	return nil
}

// AppliedState represents the last successfully applied state
type AppliedState struct {
	StackVersion int       `json:"stack_version"`
	StateHash    string    `json:"state_hash"`
	AppliedAt    time.Time `json:"applied_at"`
}

// GetAppliedState returns the last applied state
func (m *Manager) GetAppliedState() (*AppliedState, error) {
	row := m.db.QueryRow(`
		SELECT stack_version, state_hash, applied_at 
		FROM applied_state 
		WHERE id = 1
	`)

	var state AppliedState
	var appliedAt string
	err := row.Scan(&state.StackVersion, &state.StateHash, &appliedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get applied state: %w", err)
	}

	state.AppliedAt, _ = time.Parse(time.RFC3339, appliedAt)
	return &state, nil
}

// SetAppliedState records that a state was successfully applied
func (m *Manager) SetAppliedState(version int, hash string) error {
	_, err := m.db.Exec(`
		INSERT INTO applied_state (id, stack_version, state_hash, applied_at)
		VALUES (1, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			stack_version = excluded.stack_version,
			state_hash = excluded.state_hash,
			applied_at = excluded.applied_at
	`, version, hash)

	if err != nil {
		return fmt.Errorf("failed to set applied state: %w", err)
	}

	return nil
}

// ServiceProcess represents a running service process
type ServiceProcess struct {
	ServiceID     string    `json:"service_id"`
	ServiceName   string    `json:"service_name"`
	GitCommit     string    `json:"git_commit"`
	Runtime       string    `json:"runtime"`
	ContainerID   string    `json:"container_id"`
	ContainerName string    `json:"container_name"`
	ImageTag      string    `json:"image_tag"`
	PID           int       `json:"pid"`
	Status        string    `json:"status"`
	RestartCount  int       `json:"restart_count"`
	LastError     string    `json:"last_error"`
	StartedAt     time.Time `json:"started_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// GetServiceProcess retrieves a service process record
func (m *Manager) GetServiceProcess(serviceID string) (*ServiceProcess, error) {
	row := m.db.QueryRow(`
		SELECT service_id, service_name, git_commit, runtime, container_id, container_name, image_tag, pid, status, restart_count, last_error, started_at, updated_at
		FROM service_processes
		WHERE service_id = ?
	`, serviceID)

	var p ServiceProcess
	var startedAt, updatedAt sql.NullString
	err := row.Scan(&p.ServiceID, &p.ServiceName, &p.GitCommit, &p.Runtime, &p.ContainerID, &p.ContainerName, &p.ImageTag, &p.PID, &p.Status, &p.RestartCount, &p.LastError, &startedAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get service process: %w", err)
	}

	if startedAt.Valid {
		p.StartedAt, _ = time.Parse(time.RFC3339, startedAt.String)
	}
	if updatedAt.Valid {
		p.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt.String)
	}

	return &p, nil
}

// ListServiceProcesses returns all service processes
func (m *Manager) ListServiceProcesses() ([]ServiceProcess, error) {
	rows, err := m.db.Query(`
		SELECT service_id, service_name, git_commit, runtime, container_id, container_name, image_tag, pid, status, restart_count, last_error, started_at, updated_at
		FROM service_processes
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list service processes: %w", err)
	}
	defer rows.Close()

	var processes []ServiceProcess
	for rows.Next() {
		var p ServiceProcess
		var startedAt, updatedAt sql.NullString
		if err := rows.Scan(&p.ServiceID, &p.ServiceName, &p.GitCommit, &p.Runtime, &p.ContainerID, &p.ContainerName, &p.ImageTag, &p.PID, &p.Status, &p.RestartCount, &p.LastError, &startedAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan service process: %w", err)
		}
		if startedAt.Valid {
			p.StartedAt, _ = time.Parse(time.RFC3339, startedAt.String)
		}
		if updatedAt.Valid {
			p.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt.String)
		}
		processes = append(processes, p)
	}

	return processes, nil
}

// SaveServiceProcess saves or updates a service process record
func (m *Manager) SaveServiceProcess(p *ServiceProcess) error {
	if p.Runtime == "" {
		p.Runtime = "process"
	}
	_, err := m.db.Exec(`
		INSERT INTO service_processes (service_id, service_name, git_commit, runtime, container_id, container_name, image_tag, pid, status, restart_count, last_error, started_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(service_id) DO UPDATE SET
			service_name = excluded.service_name,
			git_commit = excluded.git_commit,
			runtime = excluded.runtime,
			container_id = excluded.container_id,
			container_name = excluded.container_name,
			image_tag = excluded.image_tag,
			pid = excluded.pid,
			status = excluded.status,
			restart_count = excluded.restart_count,
			last_error = excluded.last_error,
			started_at = excluded.started_at,
			updated_at = excluded.updated_at
	`, p.ServiceID, p.ServiceName, p.GitCommit, p.Runtime, p.ContainerID, p.ContainerName, p.ImageTag, p.PID, p.Status, p.RestartCount, p.LastError, p.StartedAt)

	if err != nil {
		return fmt.Errorf("failed to save service process: %w", err)
	}

	return nil
}

// DeleteServiceProcess removes a service process record
func (m *Manager) DeleteServiceProcess(serviceID string) error {
	_, err := m.db.Exec("DELETE FROM service_processes WHERE service_id = ?", serviceID)
	if err != nil {
		return fmt.Errorf("failed to delete service process: %w", err)
	}
	return nil
}

// LogServiceMessage logs a message from a service
func (m *Manager) LogServiceMessage(serviceID, level, message string) error {
	_, err := m.db.Exec(`
		INSERT INTO service_logs (service_id, level, message)
		VALUES (?, ?, ?)
	`, serviceID, level, message)

	if err != nil {
		return fmt.Errorf("failed to log message: %w", err)
	}

	return nil
}

// GetAllServiceProcesses retrieves all service processes
func (m *Manager) GetAllServiceProcesses() []*ServiceProcess {
	rows, err := m.db.Query(`
		SELECT service_id, service_name, git_commit, runtime, container_id, container_name, image_tag, pid, status, restart_count, last_error, started_at, updated_at
		FROM service_processes
		ORDER BY service_id
	`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var processes []*ServiceProcess
	for rows.Next() {
		var p ServiceProcess
		var pid sql.NullInt64
		var restartCount sql.NullInt64
		var lastError sql.NullString
		var startedAt, updatedAt sql.NullString

		err := rows.Scan(&p.ServiceID, &p.ServiceName, &p.GitCommit, &p.Runtime, &p.ContainerID, &p.ContainerName, &p.ImageTag, &pid, &p.Status, &restartCount, &lastError, &startedAt, &updatedAt)
		if err != nil {
			continue
		}

		if pid.Valid {
			p.PID = int(pid.Int64)
		}
		if restartCount.Valid {
			p.RestartCount = int(restartCount.Int64)
		}
		if lastError.Valid {
			p.LastError = lastError.String
		}
		if startedAt.Valid {
			p.StartedAt, _ = time.Parse(time.RFC3339, startedAt.String)
		}
		if updatedAt.Valid {
			p.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt.String)
		}

		processes = append(processes, &p)
	}

	return processes
}

// GetServiceLogs retrieves logs for a service
func (m *Manager) GetServiceLogs(serviceID string, limit int) ([]struct {
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}, error) {
	rows, err := m.db.Query(`
		SELECT level, message, created_at
		FROM service_logs
		WHERE service_id = ?
		ORDER BY created_at DESC
		LIMIT ?
	`, serviceID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get service logs: %w", err)
	}
	defer rows.Close()

	var logs []struct {
		Level     string    `json:"level"`
		Message   string    `json:"message"`
		CreatedAt time.Time `json:"created_at"`
	}

	for rows.Next() {
		var log struct {
			Level     string    `json:"level"`
			Message   string    `json:"message"`
			CreatedAt time.Time `json:"created_at"`
		}
		var createdAt string
		if err := rows.Scan(&log.Level, &log.Message, &createdAt); err != nil {
			return nil, fmt.Errorf("failed to scan log: %w", err)
		}
		log.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		logs = append(logs, log)
	}

	return logs, nil
}
