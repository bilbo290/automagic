package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CompletedSession tracks information about a completed Claude session
type CompletedSession struct {
	IssueIID        int        `json:"issue_iid"`
	SessionID       string     `json:"session_id"`
	ProjectPath     string     `json:"project_path"`
	CompletionTime  time.Time  `json:"completion_time"`
	LastCommentTime *time.Time `json:"last_comment_time,omitempty"`
	// Environment context for session resumption
	WorkingDir    string            `json:"working_dir"`
	ClaudeCommand string            `json:"claude_command"`
	ClaudeFlags   string            `json:"claude_flags"`
	EnvVars       map[string]string `json:"env_vars"`
}

// SessionStore manages storage of completed sessions (JSON-based, legacy)
type SessionStore struct {
	sessions map[int]*CompletedSession // Map of issue IID to session info
	mu       sync.RWMutex
	filePath string
}

// Ensure SessionStore implements the Store interface
var _ Store = (*SessionStore)(nil)

// NewSessionStore creates a new session store
func NewSessionStore(dataDir string) *SessionStore {
	if dataDir == "" {
		dataDir = filepath.Join(os.Getenv("HOME"), ".automagic")
	}

	// Ensure directory exists
	os.MkdirAll(dataDir, 0755)

	return &SessionStore{
		sessions: make(map[int]*CompletedSession),
		filePath: filepath.Join(dataDir, "sessions.json"),
	}
}

// Load reads session data from disk
func (s *SessionStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist yet, that's okay
			return nil
		}
		return fmt.Errorf("failed to read session file: %v", err)
	}

	var sessions []CompletedSession
	if err := json.Unmarshal(data, &sessions); err != nil {
		return fmt.Errorf("failed to parse session file: %v", err)
	}

	// Rebuild the map
	s.sessions = make(map[int]*CompletedSession)
	for i := range sessions {
		session := &sessions[i]
		s.sessions[session.IssueIID] = session
	}

	return nil
}

// Save writes session data to disk
func (s *SessionStore) Save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Convert map to slice for JSON marshaling
	sessions := make([]CompletedSession, 0, len(s.sessions))
	for _, session := range s.sessions {
		sessions = append(sessions, *session)
	}

	data, err := json.MarshalIndent(sessions, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal sessions: %v", err)
	}

	if err := os.WriteFile(s.filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write session file: %v", err)
	}

	return nil
}

// AddCompletedSession stores information about a completed session
func (s *SessionStore) AddCompletedSession(issueIID int, sessionID, projectPath string, completionTime time.Time, workingDir, claudeCommand, claudeFlags string, envVars map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sessions[issueIID] = &CompletedSession{
		IssueIID:       issueIID,
		SessionID:      sessionID,
		ProjectPath:    projectPath,
		CompletionTime: completionTime,
		WorkingDir:     workingDir,
		ClaudeCommand:  claudeCommand,
		ClaudeFlags:    claudeFlags,
		EnvVars:        envVars,
	}

	return s.Save()
}

// UpdateLastCommentTime updates the last seen comment time for an issue
func (s *SessionStore) UpdateLastCommentTime(issueIID int, commentTime time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if session, exists := s.sessions[issueIID]; exists {
		session.LastCommentTime = &commentTime
		return s.Save()
	}

	return fmt.Errorf("session not found for issue %d", issueIID)
}

// GetCompletedSession retrieves session information for an issue
func (s *SessionStore) GetCompletedSession(issueIID int) (*CompletedSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	session, exists := s.sessions[issueIID]
	if !exists {
		return nil, false
	}

	// Return a copy to avoid concurrent access issues
	sessionCopy := *session
	return &sessionCopy, true
}

// GetCompletedSessions returns all completed sessions
func (s *SessionStore) GetCompletedSessions() []*CompletedSession {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sessions := make([]*CompletedSession, 0, len(s.sessions))
	for _, session := range s.sessions {
		sessionCopy := *session
		sessions = append(sessions, &sessionCopy)
	}

	return sessions
}

// GetRecentlyCompletedSessions returns sessions completed within the specified duration
func (s *SessionStore) GetRecentlyCompletedSessions(since time.Duration) []*CompletedSession {
	cutoff := time.Now().Add(-since)

	s.mu.RLock()
	defer s.mu.RUnlock()

	var recent []*CompletedSession
	for _, session := range s.sessions {
		if session.CompletionTime.After(cutoff) {
			sessionCopy := *session
			recent = append(recent, &sessionCopy)
		}
	}

	return recent
}

// RemoveSession removes a session from the store
func (s *SessionStore) RemoveSession(issueIID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.sessions, issueIID)
	return s.Save()
}
