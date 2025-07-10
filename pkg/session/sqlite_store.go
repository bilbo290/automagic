package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// SQLiteSessionStore manages storage of completed sessions using SQLite
type SQLiteSessionStore struct {
	db *sql.DB
}

// Ensure SQLiteSessionStore implements the Store interface
var _ Store = (*SQLiteSessionStore)(nil)

// NewSQLiteSessionStore creates a new SQLite-based session store
func NewSQLiteSessionStore(dataDir string) (*SQLiteSessionStore, error) {
	if dataDir == "" {
		dataDir = filepath.Join(os.Getenv("HOME"), ".peter")
	}

	// Ensure directory exists
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %v", err)
	}

	dbPath := filepath.Join(dataDir, "sessions.db")
	db, err := sql.Open("sqlite3", dbPath+"?_timeout=5000&_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %v", err)
	}

	store := &SQLiteSessionStore{db: db}
	if err := store.createTables(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create tables: %v", err)
	}

	return store, nil
}

// createTables creates the necessary tables
func (s *SQLiteSessionStore) createTables() error {
	// First create the table with original schema if it doesn't exist
	createTableQuery := `
	CREATE TABLE IF NOT EXISTS completed_sessions (
		issue_iid INTEGER PRIMARY KEY,
		session_id TEXT NOT NULL,
		project_path TEXT NOT NULL,
		completion_time INTEGER NOT NULL,
		last_comment_time INTEGER
	);
	`
	
	if _, err := s.db.Exec(createTableQuery); err != nil {
		return err
	}
	
	// Now add the new columns if they don't exist (migration)
	migrationQueries := []string{
		`ALTER TABLE completed_sessions ADD COLUMN working_dir TEXT`,
		`ALTER TABLE completed_sessions ADD COLUMN claude_command TEXT`,
		`ALTER TABLE completed_sessions ADD COLUMN claude_flags TEXT`,
		`ALTER TABLE completed_sessions ADD COLUMN env_vars TEXT`,
	}
	
	for _, query := range migrationQueries {
		// These will fail if columns already exist, which is expected
		s.db.Exec(query)
	}
	
	// Create index
	indexQuery := `CREATE INDEX IF NOT EXISTS idx_completion_time ON completed_sessions(completion_time);`
	_, err := s.db.Exec(indexQuery)
	return err
}

// AddCompletedSession stores information about a completed session
func (s *SQLiteSessionStore) AddCompletedSession(issueIID int, sessionID, projectPath string, completionTime time.Time, workingDir, claudeCommand, claudeFlags string, envVars map[string]string) error {
	// Convert envVars map to JSON string for storage
	envVarsJSON := ""
	if envVars != nil {
		if jsonBytes, err := json.Marshal(envVars); err == nil {
			envVarsJSON = string(jsonBytes)
		}
	}

	query := `
	INSERT OR REPLACE INTO completed_sessions 
	(issue_iid, session_id, project_path, completion_time, last_comment_time, working_dir, claude_command, claude_flags, env_vars)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := s.db.Exec(query, issueIID, sessionID, projectPath, completionTime.Unix(), nil, workingDir, claudeCommand, claudeFlags, envVarsJSON)
	return err
}

// UpdateLastCommentTime updates the last seen comment time for an issue
func (s *SQLiteSessionStore) UpdateLastCommentTime(issueIID int, commentTime time.Time) error {
	query := `UPDATE completed_sessions SET last_comment_time = ? WHERE issue_iid = ?`

	result, err := s.db.Exec(query, commentTime.Unix(), issueIID)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return fmt.Errorf("session not found for issue %d", issueIID)
	}

	return nil
}

// GetCompletedSession retrieves session information for an issue
func (s *SQLiteSessionStore) GetCompletedSession(issueIID int) (*CompletedSession, bool) {
	query := `
	SELECT issue_iid, session_id, project_path, completion_time, last_comment_time, 
	       working_dir, claude_command, claude_flags, env_vars
	FROM completed_sessions 
	WHERE issue_iid = ?
	`

	row := s.db.QueryRow(query, issueIID)

	var session CompletedSession
	var completionTimeUnix int64
	var lastCommentTimeUnix sql.NullInt64
	var workingDir, claudeCommand, claudeFlags, envVarsJSON sql.NullString

	err := row.Scan(
		&session.IssueIID,
		&session.SessionID,
		&session.ProjectPath,
		&completionTimeUnix,
		&lastCommentTimeUnix,
		&workingDir,
		&claudeCommand,
		&claudeFlags,
		&envVarsJSON,
	)

	if err == sql.ErrNoRows {
		return nil, false
	}
	if err != nil {
		fmt.Printf("Error querying session for issue %d: %v\n", issueIID, err)
		return nil, false
	}

	session.CompletionTime = time.Unix(completionTimeUnix, 0)
	if lastCommentTimeUnix.Valid {
		t := time.Unix(lastCommentTimeUnix.Int64, 0)
		session.LastCommentTime = &t
	}

	// Set environment context fields
	if workingDir.Valid {
		session.WorkingDir = workingDir.String
	}
	if claudeCommand.Valid {
		session.ClaudeCommand = claudeCommand.String
	}
	if claudeFlags.Valid {
		session.ClaudeFlags = claudeFlags.String
	}
	if envVarsJSON.Valid && envVarsJSON.String != "" {
		var envVars map[string]string
		if err := json.Unmarshal([]byte(envVarsJSON.String), &envVars); err == nil {
			session.EnvVars = envVars
		}
	}

	return &session, true
}

// GetCompletedSessions returns all completed sessions
func (s *SQLiteSessionStore) GetCompletedSessions() []*CompletedSession {
	query := `
	SELECT issue_iid, session_id, project_path, completion_time, last_comment_time,
	       working_dir, claude_command, claude_flags, env_vars
	FROM completed_sessions 
	ORDER BY completion_time DESC
	`

	rows, err := s.db.Query(query)
	if err != nil {
		fmt.Printf("Error querying all sessions: %v\n", err)
		return nil
	}
	defer rows.Close()

	var sessions []*CompletedSession
	for rows.Next() {
		var session CompletedSession
		var completionTimeUnix int64
		var lastCommentTimeUnix sql.NullInt64
		var workingDir, claudeCommand, claudeFlags, envVarsJSON sql.NullString

		err := rows.Scan(
			&session.IssueIID,
			&session.SessionID,
			&session.ProjectPath,
			&completionTimeUnix,
			&lastCommentTimeUnix,
			&workingDir,
			&claudeCommand,
			&claudeFlags,
			&envVarsJSON,
		)
		if err != nil {
			fmt.Printf("Error scanning session row: %v\n", err)
			continue
		}

		session.CompletionTime = time.Unix(completionTimeUnix, 0)
		if lastCommentTimeUnix.Valid {
			t := time.Unix(lastCommentTimeUnix.Int64, 0)
			session.LastCommentTime = &t
		}

		// Set environment context fields
		if workingDir.Valid {
			session.WorkingDir = workingDir.String
		}
		if claudeCommand.Valid {
			session.ClaudeCommand = claudeCommand.String
		}
		if claudeFlags.Valid {
			session.ClaudeFlags = claudeFlags.String
		}
		if envVarsJSON.Valid && envVarsJSON.String != "" {
			var envVars map[string]string
			if err := json.Unmarshal([]byte(envVarsJSON.String), &envVars); err == nil {
				session.EnvVars = envVars
			}
		}

		sessions = append(sessions, &session)
	}

	return sessions
}

// GetRecentlyCompletedSessions returns sessions completed within the specified duration
func (s *SQLiteSessionStore) GetRecentlyCompletedSessions(since time.Duration) []*CompletedSession {
	cutoff := time.Now().Add(-since).Unix()

	query := `
	SELECT issue_iid, session_id, project_path, completion_time, last_comment_time,
	       working_dir, claude_command, claude_flags, env_vars
	FROM completed_sessions 
	WHERE completion_time > ?
	ORDER BY completion_time DESC
	`

	rows, err := s.db.Query(query, cutoff)
	if err != nil {
		fmt.Printf("Error querying recent sessions: %v\n", err)
		return nil
	}
	defer rows.Close()

	var sessions []*CompletedSession
	for rows.Next() {
		var session CompletedSession
		var completionTimeUnix int64
		var lastCommentTimeUnix sql.NullInt64
		var workingDir, claudeCommand, claudeFlags, envVarsJSON sql.NullString

		err := rows.Scan(
			&session.IssueIID,
			&session.SessionID,
			&session.ProjectPath,
			&completionTimeUnix,
			&lastCommentTimeUnix,
			&workingDir,
			&claudeCommand,
			&claudeFlags,
			&envVarsJSON,
		)
		if err != nil {
			fmt.Printf("Error scanning recent session row: %v\n", err)
			continue
		}

		session.CompletionTime = time.Unix(completionTimeUnix, 0)
		if lastCommentTimeUnix.Valid {
			t := time.Unix(lastCommentTimeUnix.Int64, 0)
			session.LastCommentTime = &t
		}

		// Set environment context fields
		if workingDir.Valid {
			session.WorkingDir = workingDir.String
		}
		if claudeCommand.Valid {
			session.ClaudeCommand = claudeCommand.String
		}
		if claudeFlags.Valid {
			session.ClaudeFlags = claudeFlags.String
		}
		if envVarsJSON.Valid && envVarsJSON.String != "" {
			var envVars map[string]string
			if err := json.Unmarshal([]byte(envVarsJSON.String), &envVars); err == nil {
				session.EnvVars = envVars
			}
		}

		sessions = append(sessions, &session)
	}

	return sessions
}

// RemoveSession removes a session from the store
func (s *SQLiteSessionStore) RemoveSession(issueIID int) error {
	query := `DELETE FROM completed_sessions WHERE issue_iid = ?`
	_, err := s.db.Exec(query, issueIID)
	return err
}

// CleanupInvalidSessions removes sessions with invalid (non-UUID) session IDs
func (s *SQLiteSessionStore) CleanupInvalidSessions() error {
	// Get all sessions and check them in Go code since SQLite REGEXP might not be available
	query := `SELECT issue_iid, session_id FROM completed_sessions`

	rows, err := s.db.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()

	var invalidIssueIDs []int
	uuidPattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

	for rows.Next() {
		var issueIID int
		var sessionID string

		if err := rows.Scan(&issueIID, &sessionID); err != nil {
			continue
		}

		if !uuidPattern.MatchString(sessionID) {
			invalidIssueIDs = append(invalidIssueIDs, issueIID)
		}
	}

	// Delete invalid sessions
	for _, issueIID := range invalidIssueIDs {
		deleteQuery := `DELETE FROM completed_sessions WHERE issue_iid = ?`
		if _, err := s.db.Exec(deleteQuery, issueIID); err != nil {
			fmt.Printf("Warning: Failed to delete invalid session for issue %d: %v\n", issueIID, err)
		}
	}

	if len(invalidIssueIDs) > 0 {
		fmt.Printf("Cleaned up %d sessions with invalid session IDs\n", len(invalidIssueIDs))
	}

	return nil
}

// CleanupOldSessions removes sessions older than the specified duration
func (s *SQLiteSessionStore) CleanupOldSessions(maxAge time.Duration) error {
	cutoff := time.Now().Add(-maxAge).Unix()

	query := `DELETE FROM completed_sessions WHERE completion_time < ?`

	result, err := s.db.Exec(query, cutoff)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected > 0 {
		fmt.Printf("Cleaned up %d old sessions (older than %v)\n", rowsAffected, maxAge)
	}

	return nil
}

// Close closes the database connection
func (s *SQLiteSessionStore) Close() error {
	return s.db.Close()
}

// MigrateFromJSONStore migrates data from the old JSON-based store
func (s *SQLiteSessionStore) MigrateFromJSONStore(jsonStore *SessionStore) error {
	sessions := jsonStore.GetCompletedSessions()

	for _, session := range sessions {
		// For migrated sessions, use empty values for new fields since they weren't stored in JSON
		err := s.AddCompletedSession(
			session.IssueIID,
			session.SessionID,
			session.ProjectPath,
			session.CompletionTime,
			session.WorkingDir,     // Will be empty string for old sessions
			session.ClaudeCommand,  // Will be empty string for old sessions
			session.ClaudeFlags,    // Will be empty string for old sessions
			session.EnvVars,        // Will be nil for old sessions
		)
		if err != nil {
			return fmt.Errorf("failed to migrate session %d: %v", session.IssueIID, err)
		}

		if session.LastCommentTime != nil {
			err = s.UpdateLastCommentTime(session.IssueIID, *session.LastCommentTime)
			if err != nil {
				return fmt.Errorf("failed to migrate last comment time for session %d: %v", session.IssueIID, err)
			}
		}
	}

	return nil
}
