package session

import "time"

// Store defines the interface for session storage
type Store interface {
	AddCompletedSession(issueIID int, sessionID, projectPath string, completionTime time.Time, workingDir, claudeCommand, claudeFlags string, envVars map[string]string) error
	UpdateLastCommentTime(issueIID int, commentTime time.Time) error
	GetCompletedSession(issueIID int) (*CompletedSession, bool)
	GetCompletedSessions() []*CompletedSession
	GetRecentlyCompletedSessions(since time.Duration) []*CompletedSession
	RemoveSession(issueIID int) error
}

// Load method for backward compatibility with JSON store
type Loadable interface {
	Load() error
}

// Save method for backward compatibility with JSON store  
type Saveable interface {
	Save() error
}