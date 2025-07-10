package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/bilbo290/automagic/pkg/claude"
	"github.com/bilbo290/automagic/pkg/config"
	"github.com/bilbo290/automagic/pkg/gitlab"
	"github.com/bilbo290/automagic/pkg/session"
)

type Daemon struct {
	gitlabClient    *gitlab.Client
	config          *config.Config
	selectedProject string
	processManager  *claude.ProcessManager
	sessionStore    session.Store
	resumeProcesses map[int]*exec.Cmd // Track resume processes by issue ID
	dryRun          bool
	semiDryRun      bool
	lastCommentTime map[int]string // Track last processed comment timestamp by issue ID
}

func New(gitlabClient *gitlab.Client, config *config.Config) *Daemon {
	var sessionStore session.Store

	sqliteStore, err := session.NewSQLiteSessionStore("")
	if err != nil {
		fmt.Printf("Warning: Failed to create SQLite session store, falling back to JSON store: %v\n", err)
		// Fallback to JSON store
		jsonStore := session.NewSessionStore("")
		jsonStore.Load() // Load existing sessions, ignore errors
		sessionStore = jsonStore
	} else {
		sessionStore = sqliteStore
		// Try to migrate from old JSON store if it exists
		jsonStore := session.NewSessionStore("")
		if jsonStore.Load() == nil {
			fmt.Printf("Migrating sessions from JSON to SQLite...\n")
			if err := sqliteStore.MigrateFromJSONStore(jsonStore); err != nil {
				fmt.Printf("Warning: Failed to migrate sessions: %v\n", err)
			} else {
				fmt.Printf("Successfully migrated sessions to SQLite\n")
			}
		}

		// Clean up invalid and old sessions
		fmt.Printf("Cleaning up invalid session IDs...\n")
		if err := sqliteStore.CleanupInvalidSessions(); err != nil {
			fmt.Printf("Warning: Failed to cleanup invalid sessions: %v\n", err)
		}

		// Clean up sessions older than 7 days (Claude sessions likely expire)
		fmt.Printf("Cleaning up old sessions...\n")
		if err := sqliteStore.CleanupOldSessions(7 * 24 * time.Hour); err != nil {
			fmt.Printf("Warning: Failed to cleanup old sessions: %v\n", err)
		}
	}

	return &Daemon{
		gitlabClient:    gitlabClient,
		config:          config,
		processManager:  claude.NewProcessManager(),
		sessionStore:    sessionStore,
		resumeProcesses: make(map[int]*exec.Cmd),
		dryRun:          false,
		lastCommentTime: make(map[int]string),
	}
}

func NewWithDryRun(gitlabClient *gitlab.Client, config *config.Config, dryRun bool) *Daemon {
	var sessionStore session.Store

	sqliteStore, err := session.NewSQLiteSessionStore("")
	if err != nil {
		fmt.Printf("Warning: Failed to create SQLite session store, falling back to JSON store: %v\n", err)
		// Fallback to JSON store
		jsonStore := session.NewSessionStore("")
		jsonStore.Load() // Load existing sessions, ignore errors
		sessionStore = jsonStore
	} else {
		sessionStore = sqliteStore
	}

	return &Daemon{
		gitlabClient:    gitlabClient,
		config:          config,
		processManager:  claude.NewProcessManager(),
		sessionStore:    sessionStore,
		resumeProcesses: make(map[int]*exec.Cmd),
		dryRun:          dryRun,
		lastCommentTime: make(map[int]string),
	}
}

func NewWithSemiDryRun(gitlabClient *gitlab.Client, config *config.Config) *Daemon {
	var sessionStore session.Store

	sqliteStore, err := session.NewSQLiteSessionStore("")
	if err != nil {
		fmt.Printf("Warning: Failed to create SQLite session store, falling back to JSON store: %v\n", err)
		// Fallback to JSON store
		jsonStore := session.NewSessionStore("")
		jsonStore.Load() // Load existing sessions, ignore errors
		sessionStore = jsonStore
	} else {
		sessionStore = sqliteStore
	}

	return &Daemon{
		gitlabClient:    gitlabClient,
		config:          config,
		processManager:  claude.NewProcessManager(),
		sessionStore:    sessionStore,
		resumeProcesses: make(map[int]*exec.Cmd),
		dryRun:          false, // For semi-dry-run, we clone repos but don't execute
		semiDryRun:      true,
		lastCommentTime: make(map[int]string),
	}
}

// isValidUUID checks if a string is a valid UUID format
func isValidUUID(sessionID string) bool {
	uuidPattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	return uuidPattern.MatchString(sessionID)
}

func (d *Daemon) selectProject(projects []gitlab.Project) (*gitlab.Project, error) {
	if len(projects) == 0 {
		return nil, fmt.Errorf("no projects available")
	}

	if len(projects) == 1 {
		fmt.Printf("Only one project available: %s\n", projects[0].PathWithNamespace)
		return &projects[0], nil
	}

	fmt.Printf("\nSelect a project:\n")
	for i, project := range projects {
		fmt.Printf("%d. %s\n", i+1, project.PathWithNamespace)
		if project.Description != "" {
			fmt.Printf("   Description: %s\n", project.Description)
		}
		fmt.Printf("   URL: %s\n", project.WebURL)
		fmt.Printf("   Visibility: %s\n\n", project.Visibility)
	}

	fmt.Printf("Enter project number (1-%d): ", len(projects))

	var choice int
	for {
		_, err := fmt.Scanf("%d", &choice)
		if err != nil {
			fmt.Printf("Invalid input. Please enter a number: ")
			continue
		}

		if choice < 1 || choice > len(projects) {
			fmt.Printf("Invalid choice. Please enter a number between 1 and %d: ", len(projects))
			continue
		}

		break
	}

	selected := &projects[choice-1]
	fmt.Printf("\nSelected project: %s\n", selected.PathWithNamespace)
	return selected, nil
}

func (d *Daemon) processIssueWithLabelUpdate(issue *gitlab.Issue) error {
	timestamp := time.Now().Format("2006-01-02 15:04:05")

	if d.dryRun {
		fmt.Printf("[%s] [DRY RUN] Would process issue #%d: %s\n", timestamp, issue.IID, issue.Title)
	} else if d.semiDryRun {
		fmt.Printf("[%s] [SEMI-DRY RUN] Processing issue #%d: %s\n", timestamp, issue.IID, issue.Title)
	} else {
		fmt.Printf("[%s] Processing issue #%d: %s\n", timestamp, issue.IID, issue.Title)
	}

	// Update labels to mark as being processed
	// Check if this is a human review response (has waiting_human_review label)
	isHumanReviewResponse := false
	for _, label := range issue.Labels {
		if label == d.config.Daemon.ReviewLabel {
			isHumanReviewResponse = true
			break
		}
	}

	newLabels := make([]string, 0)
	for _, label := range issue.Labels {
		// Remove both claude and waiting_human_review labels
		if label != d.config.Daemon.ClaudeLabel && label != d.config.Daemon.ReviewLabel {
			newLabels = append(newLabels, label)
		}
	}
	// Always add picked_up_by_claude label
	newLabels = append(newLabels, d.config.Daemon.ProcessLabel)

	if d.dryRun {
		if isHumanReviewResponse {
			fmt.Printf("[%s] [DRY RUN] Would update labels: remove '%s', add '%s' (human review response)\n",
				timestamp, d.config.Daemon.ReviewLabel, d.config.Daemon.ProcessLabel)
		} else {
			fmt.Printf("[%s] [DRY RUN] Would update labels: remove '%s', add '%s'\n",
				timestamp, d.config.Daemon.ClaudeLabel, d.config.Daemon.ProcessLabel)
		}
	} else if d.semiDryRun {
		if isHumanReviewResponse {
			fmt.Printf("[%s] [SEMI-DRY RUN] Would update labels: remove '%s', add '%s' (human review response)\n",
				timestamp, d.config.Daemon.ReviewLabel, d.config.Daemon.ProcessLabel)
		} else {
			fmt.Printf("[%s] [SEMI-DRY RUN] Would update labels: remove '%s', add '%s'\n",
				timestamp, d.config.Daemon.ClaudeLabel, d.config.Daemon.ProcessLabel)
		}
	} else {
		if err := d.gitlabClient.UpdateIssueLabels(d.selectedProject, issue.IID, newLabels); err != nil {
			return fmt.Errorf("failed to update issue labels: %v", err)
		}
		if isHumanReviewResponse {
			fmt.Printf("[%s] Updated labels: removed '%s', added '%s' (continuing human review loop)\n",
				timestamp, d.config.Daemon.ReviewLabel, d.config.Daemon.ProcessLabel)
		} else {
			fmt.Printf("[%s] Updated labels: removed '%s', added '%s'\n",
				timestamp, d.config.Daemon.ClaudeLabel, d.config.Daemon.ProcessLabel)
		}
	}

	// Process the issue asynchronously with completion callback
	if err := d.processIssueAsync(issue.IID); err != nil {
		fmt.Printf("[%s] Error starting process for issue #%d: %v\n", time.Now().Format("2006-01-02 15:04:05"), issue.IID, err)
		return err
	}

	fmt.Printf("[%s] Started processing issue #%d\n", time.Now().Format("2006-01-02 15:04:05"), issue.IID)
	return nil
}

func (d *Daemon) processIssue(issueNumber int) error {
	processManager := claude.NewProcessManager()

	fmt.Printf("Processing issue #%d...\n", issueNumber)

	processID := fmt.Sprintf("issue-%d-%d", issueNumber, time.Now().Unix())

	process, err := claude.CreateProcess(
		issueNumber,
		processID,
		d.config.Claude.Command,
		d.config.Claude.Flags,
		d.selectedProject,
		d.config.GitLab.Username,
	)
	if err != nil {
		return fmt.Errorf("error creating claude process: %v", err)
	}

	processManager.AddProcess(process)

	if err := claude.RunProcess(process); err != nil {
		return fmt.Errorf("error executing claude command: %v", err)
	}

	return nil
}

func (d *Daemon) processIssueAsync(issueNumber int) error {
	if d.dryRun {
		fmt.Printf("[DRY RUN] Would start async process for issue #%d...\n", issueNumber)
	} else if d.semiDryRun {
		fmt.Printf("[SEMI-DRY RUN] Starting repository check for issue #%d...\n", issueNumber)
	} else {
		fmt.Printf("Starting async process for issue #%d...\n", issueNumber)
	}

	processID := fmt.Sprintf("issue-%d-%d", issueNumber, time.Now().Unix())

	// Define completion labels - remove process label and add review label
	completionLabels := []string{d.config.Daemon.ReviewLabel}

	// Create completion callback (runs asynchronously to avoid blocking)
	onCompletion := func(process *claude.Process, success bool) error {
		// Run completion tasks asynchronously to avoid blocking the main process
		go func() {
			timestamp := time.Now().Format("2006-01-02 15:04:05")

			if success {
				fmt.Printf("[%s] Successfully completed issue #%d\n", timestamp, process.IssueNum)

				// First: Post a completion comment to the issue
				completionComment := "✅ **Task completed successfully**\n\nClaude has finished processing this issue. The implementation has been completed and is ready for human review."
				note, err := d.gitlabClient.CreateIssueNote(d.selectedProject, process.IssueNum, completionComment)
				if err != nil {
					fmt.Printf("[%s] Warning: failed to post completion comment for issue #%d: %v\n", timestamp, process.IssueNum, err)
					// Still continue with label updates even if comment fails
				} else {
					fmt.Printf("[%s] Posted completion comment for issue #%d\n", timestamp, process.IssueNum)
					// Update the last comment time to the actual comment timestamp
					// This prevents the daemon from immediately triggering again
					d.lastCommentTime[process.IssueNum] = note.CreatedAt
					fmt.Printf("[%s] Updated last comment time for issue #%d to comment timestamp: %s\n", timestamp, process.IssueNum, note.CreatedAt)
				}

				// Add a small delay to ensure the comment is processed
				time.Sleep(2 * time.Second)

				// Second: Update labels to mark as waiting for human review
				newLabels := make([]string, 0)

				// Get current issue to get current labels
				issue, err := d.gitlabClient.GetIssue(d.selectedProject, process.IssueNum)
				if err != nil {
					fmt.Printf("[%s] Warning: failed to get issue #%d for label update: %v\n", timestamp, process.IssueNum, err)
					return
				}

				// Remove process label and add review label
				for _, label := range issue.Labels {
					if label != d.config.Daemon.ProcessLabel {
						newLabels = append(newLabels, label)
					}
				}
				newLabels = append(newLabels, d.config.Daemon.ReviewLabel)

				// Update labels
				if err := d.gitlabClient.UpdateIssueLabels(d.selectedProject, process.IssueNum, newLabels); err != nil {
					fmt.Printf("[%s] Warning: failed to update completion labels for issue #%d: %v\n", timestamp, process.IssueNum, err)
				} else {
					fmt.Printf("[%s] Updated labels for issue #%d to '%s'\n", timestamp, process.IssueNum, d.config.Daemon.ReviewLabel)
				}

				// Store session information for comment monitoring
				sessionID := process.ClaudeSessionID
				if sessionID == "" {
					fmt.Printf("[%s] Warning: Claude session ID not captured for issue #%d, using fallback ID %s\n", timestamp, process.IssueNum, process.ID)
					sessionID = process.ID // Fallback to internal ID
				}

				// Prepare environment context for storage
				envVars := make(map[string]string)
				if process.Cmd != nil && process.Cmd.Env != nil {
					for _, env := range process.Cmd.Env {
						if strings.Contains(env, "=") {
							parts := strings.SplitN(env, "=", 2)
							envVars[parts[0]] = parts[1]
						}
					}
				}

				if err := d.sessionStore.AddCompletedSession(
					process.IssueNum,
					sessionID,
					d.selectedProject,
					time.Now(),
					process.WorkingDir,
					d.config.Claude.Command,
					d.config.Claude.Flags,
					envVars,
				); err != nil {
					fmt.Printf("[%s] Warning: failed to store session info for issue #%d: %v\n", timestamp, process.IssueNum, err)
				} else {
					fmt.Printf("[%s] Stored session %s for issue #%d (monitoring for new comments)\n", timestamp, sessionID, process.IssueNum)
				}
			} else {
				fmt.Printf("[%s] Failed to complete issue #%d\n", timestamp, process.IssueNum)

				// Get current issue to get current labels
				issue, err := d.gitlabClient.GetIssue(d.selectedProject, process.IssueNum)
				if err != nil {
					fmt.Printf("[%s] Warning: failed to get issue #%d for label update: %v\n", timestamp, process.IssueNum, err)
					return
				}

				// Remove process label and add error label
				newLabels := make([]string, 0)
				for _, label := range issue.Labels {
					if label != d.config.Daemon.ProcessLabel {
						newLabels = append(newLabels, label)
					}
				}
				newLabels = append(newLabels, "error")

				// Update labels
				if err := d.gitlabClient.UpdateIssueLabels(d.selectedProject, process.IssueNum, newLabels); err != nil {
					fmt.Printf("[%s] Warning: failed to update error labels for issue #%d: %v\n", timestamp, process.IssueNum, err)
				} else {
					fmt.Printf("[%s] Updated labels for issue #%d to 'error'\n", timestamp, process.IssueNum)
				}
			}
		}() // End of async goroutine

		return nil // Return immediately from callback
	}

	process, err := claude.CreateProcessWithCallbackAndGitlabDryRun(
		issueNumber,
		processID,
		d.config.Claude.Command,
		d.config.Claude.Flags,
		d.selectedProject,
		d.config.GitLab.Username,
		d.config.GitLab.URL,
		d.dryRun,
		completionLabels,
		onCompletion,
	)
	if err != nil {
		return fmt.Errorf("error creating claude process: %v", err)
	}

	if d.dryRun || d.semiDryRun {
		if d.dryRun {
			fmt.Println("\n=== DRY RUN MODE (Daemon) ===")
		} else {
			fmt.Println("\n=== SEMI-DRY RUN MODE (Daemon) ===")
			fmt.Println("Repository has been cloned/verified.")
		}
		fmt.Printf("Issue #%d\n", issueNumber)
		fmt.Printf("Process ID: %s\n", processID)
		fmt.Printf("Working Directory: %s\n", process.Cmd.Dir)
		fmt.Printf("Command: %s\n", process.Cmd.Path)
		fmt.Printf("Arguments: %v\n", process.Cmd.Args)
		fmt.Println("\n=== PROMPT ===")
		// Extract the prompt from the command args
		for i, arg := range process.Cmd.Args {
			if arg == "-p" && i+1 < len(process.Cmd.Args) {
				fmt.Println(process.Cmd.Args[i+1])
				break
			}
		}

		if d.semiDryRun {
			fmt.Println("=== END SEMI-DRY RUN ===")

			// Additional repository checks in semi-dry-run mode
			fmt.Println("\n=== REPOSITORY STATUS ===")
			// Run git status in the repository directory
			statusCmd := exec.Command("git", "status", "--short")
			statusCmd.Dir = process.Cmd.Dir
			if output, err := statusCmd.Output(); err == nil {
				if len(output) == 0 {
					fmt.Println("Git status: Clean working directory")
				} else {
					fmt.Printf("Git status:\n%s", output)
				}
			}

			// Show current branch
			branchCmd := exec.Command("git", "branch", "--show-current")
			branchCmd.Dir = process.Cmd.Dir
			if output, err := branchCmd.Output(); err == nil {
				fmt.Printf("Current branch: %s", output)
			}

			fmt.Printf("\n[SEMI-DRY RUN] Would update labels: remove '%s', add '%s' on completion\n", d.config.Daemon.ProcessLabel, d.config.Daemon.ReviewLabel)

			// In semi-dry-run mode, show what cleanup would do
			fmt.Printf("\n=== REPOSITORY CLEANUP ===\n")
			fmt.Printf("After Claude finishes, the following cleanup would occur:\n")
			fmt.Printf("- Reset any uncommitted changes (git reset --hard HEAD)\n")
			fmt.Printf("- Remove untracked files (git clean -fd)\n")
			fmt.Printf("- Switch back to main branch\n")
			fmt.Printf("- Delete any issue-* branches\n")
			fmt.Printf("- Pull latest changes\n")
			fmt.Printf("Repository will be ready for the next parallel session\n")
		} else {
			fmt.Println("=== END DRY RUN ===\n")
			fmt.Printf("[DRY RUN] Would update labels: remove '%s', add '%s' on completion\n", d.config.Daemon.ProcessLabel, d.config.Daemon.ReviewLabel)
		}
	} else {
		d.processManager.AddProcess(process)

		// Run the process asynchronously
		claude.RunProcessAsync(process, d.processManager)
	}

	return nil
}

func (d *Daemon) resumeSessionWithComments(session *session.CompletedSession, newComments []gitlab.Note) error {
	return d.resumeSessionWithCommentsWithContext(context.Background(), session, newComments)
}

func (d *Daemon) resumeSessionWithCommentsWithContext(ctx context.Context, session *session.CompletedSession, newComments []gitlab.Note) error {
	// Check if context is already cancelled
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	timestamp := time.Now().Format("2006-01-02 15:04:05")

	// Build comment context
	commentContext := fmt.Sprintf("# New Comments on Issue #%d\n\n", session.IssueIID)
	commentContext += "The following comments were added after you completed this issue:\n\n"

	for i, comment := range newComments {
		// Check for cancellation during comment processing
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		commentContext += fmt.Sprintf("## Comment %d by @%s\n", i+1, comment.Author.Username)
		commentContext += fmt.Sprintf("**Posted:** %s\n\n", comment.CreatedAt)
		commentContext += fmt.Sprintf("%s\n\n", comment.Body)
		commentContext += "---\n\n"
	}

	commentContext += "Please review these comments and take any necessary follow-up actions. "
	commentContext += "You can update your previous work, answer questions, or make additional changes as needed."

	// Validate session ID format
	if !isValidUUID(session.SessionID) {
		fmt.Printf("[%s] Skipping resume for issue #%d: session ID '%s' is not a valid UUID (likely from old format)\n",
			timestamp, session.IssueIID, session.SessionID)
		return nil
	}

	if d.dryRun {
		fmt.Printf("[%s] [DRY RUN] Would resume session %s with comment context:\n%s\n", timestamp, session.SessionID, commentContext)
		return nil
	} else if d.semiDryRun {
		fmt.Printf("[%s] [SEMI-DRY RUN] Would resume session %s with comment context:\n%s\n", timestamp, session.SessionID, commentContext)
		return nil
	}

	// Check again before starting the process
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Use stored environment context to recreate the exact same execution environment
	args := []string{}
	claudeCommand := session.ClaudeCommand
	claudeFlags := session.ClaudeFlags
	workingDir := session.WorkingDir

	// Fallback to config values if not stored in session (for backward compatibility)
	if claudeCommand == "" {
		claudeCommand = d.config.Claude.Command
	}
	if claudeFlags == "" {
		claudeFlags = d.config.Claude.Flags
	}
	if workingDir == "" {
		// Fallback to detection logic for old sessions
		detectedDir, _, err := claude.DetectProjectDirectory(session.ProjectPath)
		if err != nil {
			return fmt.Errorf("failed to detect working directory: %v", err)
		}
		workingDir = detectedDir
	}

	// Build command arguments using the stored flags
	if claudeFlags != "" {
		args = strings.Fields(claudeFlags)
	}
	args = append(args, "-r", session.SessionID, "-p", commentContext)

	fmt.Printf("[%s] Resuming Claude session %s for issue #%d with new comments\n", timestamp, session.SessionID, session.IssueIID)
	fmt.Printf("[%s] Using stored environment: command=%s, working_dir=%s\n", timestamp, claudeCommand, workingDir)

	// Create a context-aware command execution using the stored command and environment
	cmd := exec.CommandContext(ctx, claudeCommand, args...)
	cmd.Dir = workingDir

	// Use stored environment variables if available, otherwise fall back to current environment
	if session.EnvVars != nil && len(session.EnvVars) > 0 {
		// Convert stored environment map back to slice format
		envSlice := make([]string, 0, len(session.EnvVars))
		for key, value := range session.EnvVars {
			envSlice = append(envSlice, fmt.Sprintf("%s=%s", key, value))
		}
		cmd.Env = envSlice
		fmt.Printf("[%s] Using %d stored environment variables\n", timestamp, len(session.EnvVars))
	} else {
		// Fallback for backward compatibility
		cmd.Env = os.Environ()
		fmt.Printf("[%s] Using current environment (no stored env vars)\n", timestamp)
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Start the resume command asynchronously
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start resume session: %v", err)
	}

	// Track this process for graceful shutdown
	d.resumeProcesses[session.IssueIID] = cmd

	fmt.Printf("[%s] Started resume session for issue #%d (PID: %d)\n", timestamp, session.IssueIID, cmd.Process.Pid)

	// Don't wait for completion - let it run in background
	// The process will complete on its own and respect context cancellation
	go func() {
		err := cmd.Wait()

		// Remove from tracking when completed
		delete(d.resumeProcesses, session.IssueIID)

		if err != nil {
			// Check if it was cancelled due to context
			if ctx.Err() != nil {
				fmt.Printf("[%s] Resume session for issue #%d cancelled\n",
					time.Now().Format("2006-01-02 15:04:05"), session.IssueIID)
			} else {
				errorMsg := err.Error()
				fmt.Printf("[%s] Resume session for issue #%d completed with error: %v\n",
					time.Now().Format("2006-01-02 15:04:05"), session.IssueIID, err)

				// Check if the error indicates the session is no longer valid
				if strings.Contains(errorMsg, "No conversation found") ||
					strings.Contains(errorMsg, "session ID") ||
					strings.Contains(errorMsg, "not found") {
					fmt.Printf("[%s] Session %s appears to be invalid/expired, removing from database\n",
						time.Now().Format("2006-01-02 15:04:05"), session.SessionID)

					// Remove the invalid session from the store
					if removeErr := d.sessionStore.RemoveSession(session.IssueIID); removeErr != nil {
						fmt.Printf("[%s] Warning: Failed to remove invalid session for issue #%d: %v\n",
							time.Now().Format("2006-01-02 15:04:05"), session.IssueIID, removeErr)
					} else {
						fmt.Printf("[%s] Removed invalid session for issue #%d from database\n",
							time.Now().Format("2006-01-02 15:04:05"), session.IssueIID)
					}
				}
			}
		} else {
			fmt.Printf("[%s] Resume session for issue #%d completed successfully\n",
				time.Now().Format("2006-01-02 15:04:05"), session.IssueIID)
		}
	}()

	return nil
}

func (d *Daemon) checkForNewClaudeIssues(processedIssues map[int]bool, timestamp string) (int, error) {
	// Fetch issues with the claude label (new work)
	fmt.Printf("[%s] DEBUG: Fetching issues with label '%s' from project '%s'...\n", timestamp, d.config.Daemon.ClaudeLabel, d.selectedProject)
	issues, err := d.gitlabClient.GetProjectIssues(d.selectedProject, []string{d.config.Daemon.ClaudeLabel}, "opened")
	if err != nil {
		fmt.Printf("[%s] DEBUG: Failed to fetch claude issues: %v\n", timestamp, err)
		return 0, fmt.Errorf("failed to fetch claude issues: %v", err)
	}
	fmt.Printf("[%s] DEBUG: Successfully fetched %d issues with claude label\n", timestamp, len(issues))

	newIssues := 0
	for _, issue := range issues {
		if !processedIssues[issue.IID] {
			processedIssues[issue.IID] = true
			newIssues++

			fmt.Printf("[%s] Found new issue #%d: %s\n", timestamp, issue.IID, issue.Title)

			// Process issue asynchronously with automagic label updates
			if err := d.processIssueWithLabelUpdate(&issue); err != nil {
				fmt.Printf("[%s] Failed to start processing issue #%d: %v\n", timestamp, issue.IID, err)
			} else {
				fmt.Printf("[%s] Started new Claude session for issue #%d\n", timestamp, issue.IID)
			}
		}
	}

	return newIssues, nil
}

func (d *Daemon) checkForNewClaudeIssuesWithContext(ctx context.Context, processedIssues map[int]bool, timestamp string) (int, error) {
	// Check if context is already cancelled
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	// Fetch issues with the claude label (new work) with timeout
	fmt.Printf("[%s] DEBUG: Fetching issues with label '%s' from project '%s'...\n", timestamp, d.config.Daemon.ClaudeLabel, d.selectedProject)

	// Create a timeout context for the API call
	apiCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Use a channel to make the API call cancellable
	type result struct {
		issues []gitlab.Issue
		err    error
	}

	resultCh := make(chan result, 1)
	go func() {
		issues, err := d.gitlabClient.GetProjectIssues(d.selectedProject, []string{d.config.Daemon.ClaudeLabel}, "opened")
		resultCh <- result{issues: issues, err: err}
	}()

	// Wait for either the result or context cancellation
	var issues []gitlab.Issue
	var err error
	select {
	case <-apiCtx.Done():
		fmt.Printf("[%s] DEBUG: API call timed out or was cancelled\n", timestamp)
		return 0, apiCtx.Err()
	case res := <-resultCh:
		issues = res.issues
		err = res.err
	}

	if err != nil {
		fmt.Printf("[%s] DEBUG: Failed to fetch claude issues: %v\n", timestamp, err)
		return 0, fmt.Errorf("failed to fetch claude issues: %v", err)
	}
	fmt.Printf("[%s] DEBUG: Successfully fetched %d issues with claude label\n", timestamp, len(issues))

	newIssues := 0
	for _, issue := range issues {
		// Check for cancellation between issues
		select {
		case <-ctx.Done():
			return newIssues, ctx.Err()
		default:
		}

		if !processedIssues[issue.IID] {
			processedIssues[issue.IID] = true
			newIssues++

			fmt.Printf("[%s] Found new issue #%d: %s\n", timestamp, issue.IID, issue.Title)

			// Process issue asynchronously with automagic label updates
			if err := d.processIssueWithLabelUpdate(&issue); err != nil {
				fmt.Printf("[%s] Failed to start processing issue #%d: %v\n", timestamp, issue.IID, err)
			} else {
				fmt.Printf("[%s] Started new Claude session for issue #%d\n", timestamp, issue.IID)
			}
		}
	}

	return newIssues, nil
}

func (d *Daemon) checkForHumanReviewIssuesWithContext(ctx context.Context, processedIssues map[int]bool, timestamp string) (int, error) {
	// Check if context is already cancelled
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	// Fetch issues with the waiting_human_review label
	fmt.Printf("[%s] DEBUG: Fetching issues with label '%s' from project '%s'...\n", timestamp, d.config.Daemon.ReviewLabel, d.selectedProject)

	// Create a timeout context for the API call
	apiCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Use a channel to make the API call cancellable
	type result struct {
		issues []gitlab.Issue
		err    error
	}

	resultCh := make(chan result, 1)
	go func() {
		issues, err := d.gitlabClient.GetProjectIssues(d.selectedProject, []string{d.config.Daemon.ReviewLabel}, "opened")
		resultCh <- result{issues: issues, err: err}
	}()

	// Wait for either the result or context cancellation
	var issues []gitlab.Issue
	var err error
	select {
	case <-apiCtx.Done():
		fmt.Printf("[%s] DEBUG: API call timed out or was cancelled\n", timestamp)
		return 0, apiCtx.Err()
	case res := <-resultCh:
		issues = res.issues
		err = res.err
	}

	if err != nil {
		fmt.Printf("[%s] DEBUG: Failed to fetch review issues: %v\n", timestamp, err)
		return 0, fmt.Errorf("failed to fetch review issues: %v", err)
	}
	fmt.Printf("[%s] DEBUG: Successfully fetched %d issues with review label\n", timestamp, len(issues))

	// List all review issues for debugging
	for i, issue := range issues {
		fmt.Printf("[%s] DEBUG: Review issue %d: #%d - %s (labels: %v)\n", timestamp, i+1, issue.IID, issue.Title, issue.Labels)
	}

	newSessions := 0
	for _, issue := range issues {
		// Check for cancellation between issues
		select {
		case <-ctx.Done():
			return newSessions, ctx.Err()
		default:
		}

		// Skip if already processed in this cycle
		if processedIssues[issue.IID] {
			fmt.Printf("[%s] DEBUG: Issue #%d already processed in this cycle, skipping\n", timestamp, issue.IID)
			continue
		}

		// Get the latest comments to check if last comment is from human
		fmt.Printf("[%s] DEBUG: Checking latest comments for issue #%d\n", timestamp, issue.IID)

		// Add a longer delay to handle potential API caching/replication delays
		time.Sleep(3 * time.Second)

		// Create a timeout context for comment checking
		commentCtx, commentCancel := context.WithTimeout(ctx, 8*time.Second)
		defer commentCancel()

		type commentResult struct {
			comments []gitlab.Note
			err      error
		}

		commentCh := make(chan commentResult, 1)
		go func() {
			fmt.Printf("[%s] DEBUG: Fetching discussions for issue #%d\n", timestamp, issue.IID)

			// Get all discussions/comments for this issue
			discussions, err := d.gitlabClient.GetIssueDiscussionsWithContext(commentCtx, d.selectedProject, issue.IID)
			if err != nil {
				fmt.Printf("[%s] DEBUG: Error fetching discussions for issue #%d: %v\n", timestamp, issue.IID, err)
				commentCh <- commentResult{comments: nil, err: err}
				return
			}

			fmt.Printf("[%s] DEBUG: Issue #%d has %d discussions (fetched at %s)\n", timestamp, issue.IID, len(discussions), time.Now().Format("15:04:05"))

			// Flatten all notes from all discussions and filter out system notes
			var allNotes []gitlab.Note
			for i, discussion := range discussions {
				fmt.Printf("[%s] DEBUG: Discussion %d has %d notes\n", timestamp, i+1, len(discussion.Notes))
				for j, note := range discussion.Notes {
					fmt.Printf("[%s] DEBUG:   Note %d: @%s (system: %v) at %s: %.50s...\n",
						timestamp, j+1, note.Author.Username, note.System, note.CreatedAt, note.Body)

					// Skip system-generated notes (like label changes, etc.)
					if !note.System {
						allNotes = append(allNotes, note)
					}
				}
			}

			fmt.Printf("[%s] DEBUG: Issue #%d has %d non-system notes total\n", timestamp, issue.IID, len(allNotes))

			// Sort notes by creation time to ensure we get the actual latest comment
			sort.Slice(allNotes, func(i, j int) bool {
				timeI, errI := time.Parse(time.RFC3339, allNotes[i].CreatedAt)
				timeJ, errJ := time.Parse(time.RFC3339, allNotes[j].CreatedAt)
				if errI != nil || errJ != nil {
					// Fallback to string comparison if parsing fails
					return allNotes[i].CreatedAt < allNotes[j].CreatedAt
				}
				return timeI.Before(timeJ)
			})

			commentCh <- commentResult{comments: allNotes, err: nil}
		}()

		var comments []gitlab.Note
		select {
		case <-commentCtx.Done():
			fmt.Printf("[%s] DEBUG: Comment checking timed out for issue #%d\n", timestamp, issue.IID)
			commentCancel()
			continue
		case res := <-commentCh:
			comments = res.comments
			err = res.err
		}
		commentCancel()

		if err != nil {
			fmt.Printf("[%s] Error getting comments for issue #%d: %v\n", timestamp, issue.IID, err)
			continue
		}

		fmt.Printf("[%s] DEBUG: Issue #%d has %d total comments (non-system)\n", timestamp, issue.IID, len(comments))
		
		// If we expected more comments, try a direct API call to double-check
		if len(comments) < 14 { // You mentioned you added a comment, so should be > 13
			fmt.Printf("[%s] DEBUG: Expected more comments, trying direct API call...\n", timestamp)
			directDiscussions, directErr := d.gitlabClient.GetIssueDiscussions(d.selectedProject, issue.IID)
			if directErr == nil {
				var directNotes []gitlab.Note
				for _, discussion := range directDiscussions {
					for _, note := range discussion.Notes {
						if !note.System {
							directNotes = append(directNotes, note)
						}
					}
				}
				fmt.Printf("[%s] DEBUG: Direct API call found %d comments (was %d)\n", timestamp, len(directNotes), len(comments))
				if len(directNotes) > len(comments) {
					comments = directNotes
					fmt.Printf("[%s] DEBUG: Using direct API results\n", timestamp)
				}
			}
		}

		// Show the last few comments for debugging
		if len(comments) > 0 {
			numToShow := 3
			if len(comments) < numToShow {
				numToShow = len(comments)
			}

			fmt.Printf("[%s] DEBUG: Last %d comments for issue #%d:\n", timestamp, numToShow, issue.IID)
			for i := len(comments) - numToShow; i < len(comments); i++ {
				comment := comments[i]
				fmt.Printf("[%s] DEBUG:   %d. @%s at %s: %.50s...\n",
					timestamp, i+1, comment.Author.Username, comment.CreatedAt, comment.Body)
			}
		}

		// Check if the last comment is from a human (not a bot)
		if len(comments) > 0 {
			lastComment := comments[len(comments)-1]
			
			// Check multiple criteria to identify bot comments
			isBotComment := false
			
			// Check if display name contains common bot indicators
			authorName := strings.ToLower(lastComment.Author.Name)
			if strings.Contains(authorName, "claude") || strings.Contains(authorName, "bot") {
				isBotComment = true
			}
			
			// Check if username contains "bot" (for auto-generated bot usernames)
			authorUsername := strings.ToLower(lastComment.Author.Username)
			if strings.Contains(authorUsername, "bot") {
				isBotComment = true
			}
			
			// Only check against configured username if it contains "bot" 
			// (to avoid matching human usernames that happen to be in config)
			botUsername := strings.ToLower(d.config.GitLab.Username)
			if strings.Contains(botUsername, "bot") && lastComment.Author.Username == d.config.GitLab.Username {
				isBotComment = true
			}
			
			isHumanComment := !isBotComment
			
			fmt.Printf("[%s] DEBUG: Issue #%d last comment by @%s (%s) at %s\n", 
				timestamp, issue.IID, lastComment.Author.Username, lastComment.Author.Name, lastComment.CreatedAt)
			fmt.Printf("[%s] DEBUG: Bot detection - Name: '%s', Username: '%s', Config: '%s'\n", 
				timestamp, lastComment.Author.Name, lastComment.Author.Username, botUsername)
			fmt.Printf("[%s] DEBUG: Is bot comment: %v, Is human comment: %v\n", 
				timestamp, isBotComment, isHumanComment)

			// Check if this comment is newer than the last one we processed
			lastProcessedTime, hasProcessedBefore := d.lastCommentTime[issue.IID]
			
			// Parse timestamps for proper comparison
			var isNewerComment bool
			if !hasProcessedBefore {
				isNewerComment = true
				fmt.Printf("[%s] DEBUG: Issue #%d - never processed before, treating as new\n", timestamp, issue.IID)
			} else {
				// Parse both timestamps for proper comparison
				lastTime, err1 := time.Parse(time.RFC3339, lastProcessedTime)
				currentTime, err2 := time.Parse(time.RFC3339, lastComment.CreatedAt)
				
				if err1 != nil || err2 != nil {
					// Fallback to string comparison if parsing fails
					isNewerComment = lastComment.CreatedAt > lastProcessedTime
					fmt.Printf("[%s] DEBUG: Issue #%d - timestamp parse failed, using string comparison\n", timestamp, issue.IID)
				} else {
					isNewerComment = currentTime.After(lastTime)
					fmt.Printf("[%s] DEBUG: Issue #%d - parsed timestamp comparison\n", timestamp, issue.IID)
				}
			}

			fmt.Printf("[%s] DEBUG: Issue #%d - last processed: '%s', current: '%s', newer: %v\n",
				timestamp, issue.IID, lastProcessedTime, lastComment.CreatedAt, isNewerComment)

			if isHumanComment && isNewerComment {
				// Mark as processed in this cycle and update last comment time
				processedIssues[issue.IID] = true
				d.lastCommentTime[issue.IID] = lastComment.CreatedAt
				newSessions++

				fmt.Printf("[%s] Found issue #%d with NEW human comment from @%s: %s\n",
					timestamp, issue.IID, lastComment.Author.Username, issue.Title)

				// Process issue asynchronously with automagic label updates
				if err := d.processIssueWithLabelUpdate(&issue); err != nil {
					fmt.Printf("[%s] Failed to start processing issue #%d: %v\n", timestamp, issue.IID, err)
				} else {
					fmt.Printf("[%s] Started new Claude session for issue #%d (human review response)\n", timestamp, issue.IID)
				}
			} else {
				if !isHumanComment {
					fmt.Printf("[%s] DEBUG: Skipping issue #%d - last comment is from bot\n", timestamp, issue.IID)
				} else {
					fmt.Printf("[%s] DEBUG: Skipping issue #%d - no new human comments since last check\n", timestamp, issue.IID)
				}
			}
		} else {
			fmt.Printf("[%s] DEBUG: Issue #%d has no comments, skipping\n", timestamp, issue.IID)
		}
	}

	return newSessions, nil
}

func (d *Daemon) checkForReviewIssuesWithComments(timestamp string) (int, error) {
	// Fetch issues with the review label (waiting for human review)
	fmt.Printf("[%s] DEBUG: Fetching issues with label '%s' from project '%s'...\n", timestamp, d.config.Daemon.ReviewLabel, d.selectedProject)
	reviewIssues, err := d.gitlabClient.GetProjectIssues(d.selectedProject, []string{d.config.Daemon.ReviewLabel}, "opened")
	if err != nil {
		fmt.Printf("[%s] DEBUG: Failed to fetch review issues: %v\n", timestamp, err)
		return 0, fmt.Errorf("failed to fetch review issues: %v", err)
	}
	fmt.Printf("[%s] DEBUG: Successfully fetched %d issues with review label\n", timestamp, len(reviewIssues))

	resumedSessions := 0
	for _, issue := range reviewIssues {
		// Check if we have a completed session for this issue
		session, exists := d.sessionStore.GetCompletedSession(issue.IID)
		if !exists {
			// No session record, skip this issue
			continue
		}

		// Determine the cutoff time for new comments
		cutoffTime := session.CompletionTime
		if session.LastCommentTime != nil {
			cutoffTime = *session.LastCommentTime
		}

		// Check for new comments since the cutoff time
		newComments, err := d.gitlabClient.GetIssueCommentsAfter(session.ProjectPath, session.IssueIID, cutoffTime)
		if err != nil {
			fmt.Printf("[%s] Error checking comments for issue #%d: %v\n", timestamp, session.IssueIID, err)
			continue
		}

		if len(newComments) > 0 {
			fmt.Printf("[%s] Found %d new comments on issue #%d\n", timestamp, len(newComments), session.IssueIID)

			// Resume Claude session with new comments
			if err := d.resumeSessionWithComments(session, newComments); err != nil {
				fmt.Printf("[%s] Error resuming session for issue #%d: %v\n", timestamp, session.IssueIID, err)
				continue
			}

			resumedSessions++
			fmt.Printf("[%s] Resumed Claude session for issue #%d\n", timestamp, session.IssueIID)

			// Update last comment time to latest comment
			latestCommentTime := newComments[len(newComments)-1].CreatedAt
			if parsedTime, err := time.Parse(time.RFC3339, latestCommentTime); err == nil {
				d.sessionStore.UpdateLastCommentTime(session.IssueIID, parsedTime)
			}
		}
	}

	return resumedSessions, nil
}

func (d *Daemon) checkForReviewIssuesWithCommentsWithContext(ctx context.Context, timestamp string) (int, error) {
	// Check if context is already cancelled
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	// Fetch issues with the review label (waiting for human review) with timeout
	fmt.Printf("[%s] DEBUG: Fetching issues with label '%s' from project '%s'...\n", timestamp, d.config.Daemon.ReviewLabel, d.selectedProject)

	// Create a timeout context for the API call
	apiCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Use a channel to make the API call cancellable
	type result struct {
		issues []gitlab.Issue
		err    error
	}

	resultCh := make(chan result, 1)
	go func() {
		reviewIssues, err := d.gitlabClient.GetProjectIssues(d.selectedProject, []string{d.config.Daemon.ReviewLabel}, "opened")
		resultCh <- result{issues: reviewIssues, err: err}
	}()

	// Wait for either the result or context cancellation
	var reviewIssues []gitlab.Issue
	var err error
	select {
	case <-apiCtx.Done():
		fmt.Printf("[%s] DEBUG: Review issues API call timed out or was cancelled\n", timestamp)
		return 0, apiCtx.Err()
	case res := <-resultCh:
		reviewIssues = res.issues
		err = res.err
	}

	if err != nil {
		fmt.Printf("[%s] DEBUG: Failed to fetch review issues: %v\n", timestamp, err)
		return 0, fmt.Errorf("failed to fetch review issues: %v", err)
	}
	fmt.Printf("[%s] DEBUG: Successfully fetched %d issues with review label\n", timestamp, len(reviewIssues))

	resumedSessions := 0
	for i, issue := range reviewIssues {
		fmt.Printf("[%s] DEBUG: Processing review issue %d/%d (#%d)\n", timestamp, i+1, len(reviewIssues), issue.IID)

		// Check for cancellation between issues
		select {
		case <-ctx.Done():
			fmt.Printf("[%s] DEBUG: Context cancelled while processing issue %d\n", timestamp, issue.IID)
			return resumedSessions, ctx.Err()
		default:
		}

		// Check if we have a completed session for this issue
		fmt.Printf("[%s] DEBUG: Looking up session for issue #%d\n", timestamp, issue.IID)
		session, exists := d.sessionStore.GetCompletedSession(issue.IID)
		if !exists {
			fmt.Printf("[%s] DEBUG: No session record for issue #%d, skipping\n", timestamp, issue.IID)
			continue
		}
		fmt.Printf("[%s] DEBUG: Found session for issue #%d\n", timestamp, issue.IID)

		// Determine the cutoff time for new comments
		cutoffTime := session.CompletionTime
		if session.LastCommentTime != nil {
			cutoffTime = *session.LastCommentTime
		}

		// Check for new comments since the cutoff time (with context timeout)
		fmt.Printf("[%s] DEBUG: Checking comments for issue #%d since %v\n", timestamp, session.IssueIID, cutoffTime)

		// Make comment checking cancellable with shorter timeout
		commentCtx, commentCancel := context.WithTimeout(ctx, 8*time.Second)
		defer commentCancel()

		type commentResult struct {
			comments []gitlab.Note
			err      error
		}

		commentCh := make(chan commentResult, 1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Printf("[%s] DEBUG: Panic in comment checking for issue #%d: %v\n", timestamp, session.IssueIID, r)
					commentCh <- commentResult{comments: nil, err: fmt.Errorf("panic in comment checking: %v", r)}
				}
			}()
			fmt.Printf("[%s] DEBUG: Starting API call for comments on issue #%d\n", timestamp, session.IssueIID)
			comments, err := d.gitlabClient.GetIssueCommentsAfterWithContext(commentCtx, session.ProjectPath, session.IssueIID, cutoffTime)
			fmt.Printf("[%s] DEBUG: Finished API call for comments on issue #%d, found %d comments, err: %v\n", timestamp, session.IssueIID, len(comments), err)
			commentCh <- commentResult{comments: comments, err: err}
		}()

		var newComments []gitlab.Note
		select {
		case <-commentCtx.Done():
			fmt.Printf("[%s] DEBUG: Comment checking timed out or was cancelled for issue #%d (context error: %v)\n", timestamp, session.IssueIID, commentCtx.Err())
			commentCancel()
			continue
		case res := <-commentCh:
			newComments = res.comments
			err = res.err
		}
		commentCancel()

		if err != nil {
			fmt.Printf("[%s] Error checking comments for issue #%d: %v\n", timestamp, session.IssueIID, err)
			continue
		}

		fmt.Printf("[%s] DEBUG: Found %d new comments for issue #%d\n", timestamp, len(newComments), session.IssueIID)

		if len(newComments) > 0 {
			fmt.Printf("[%s] Found %d new comments on issue #%d\n", timestamp, len(newComments), session.IssueIID)

			// Check for cancellation before resuming session
			select {
			case <-ctx.Done():
				fmt.Printf("[%s] DEBUG: Cancelled before resuming session for issue #%d\n", timestamp, session.IssueIID)
				return resumedSessions, ctx.Err()
			default:
			}

			// Resume Claude session with new comments (this is now async and won't block)
			fmt.Printf("[%s] DEBUG: Starting session resume for issue #%d\n", timestamp, session.IssueIID)
			if err := d.resumeSessionWithCommentsWithContext(ctx, session, newComments); err != nil {
				if ctx.Err() != nil {
					fmt.Printf("[%s] Session resume cancelled for issue #%d\n", timestamp, session.IssueIID)
					return resumedSessions, ctx.Err()
				}
				fmt.Printf("[%s] Error resuming session for issue #%d: %v\n", timestamp, session.IssueIID, err)
				continue
			}
			fmt.Printf("[%s] DEBUG: Finished session resume for issue #%d\n", timestamp, session.IssueIID)

			resumedSessions++
			fmt.Printf("[%s] Resumed Claude session for issue #%d\n", timestamp, session.IssueIID)

			// Update last comment time to latest comment
			latestCommentTime := newComments[len(newComments)-1].CreatedAt
			if parsedTime, err := time.Parse(time.RFC3339, latestCommentTime); err == nil {
				d.sessionStore.UpdateLastCommentTime(session.IssueIID, parsedTime)
			}
		}
	}

	return resumedSessions, nil
}

func (d *Daemon) Run() error {
	// Step 1: Select project interactively
	fmt.Printf("=== Project Selection for Daemon Mode ===\n")
	projects, err := d.gitlabClient.GetAccessibleProjects()
	if err != nil {
		return fmt.Errorf("error fetching projects: %v", err)
	}

	selectedProject, err := d.selectProject(projects)
	if err != nil {
		return fmt.Errorf("error selecting project: %v", err)
	}

	d.selectedProject = selectedProject.PathWithNamespace
	fmt.Printf("Project selected: %s\n", d.selectedProject)

	// Step 2: Start daemon monitoring
	fmt.Printf("\n=== Starting Daemon Mode ===\n")
	if d.dryRun {
		fmt.Printf("*** DRY RUN MODE - No actual processing will occur ***\n")
	} else if d.semiDryRun {
		fmt.Printf("*** SEMI-DRY RUN MODE - Will clone repositories but not execute Claude ***\n")
	}
	fmt.Printf("Monitoring project: %s\n", d.selectedProject)
	fmt.Printf("Monitoring for issues with label: %s\n", d.config.Daemon.ClaudeLabel)
	fmt.Printf("Processing interval: %d seconds\n", d.config.Daemon.Interval)
	fmt.Printf("Press Ctrl+C to stop...\n\n")

	// Set up signal handling for graceful shutdown with context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Cancel context when signal received
	go func() {
		<-sigCh
		fmt.Printf("\nReceived shutdown signal. Cancelling operations...\n")
		cancel()
	}()

	// Keep track of processed issues to avoid duplicates
	processedIssues := make(map[int]bool)

	ticker := time.NewTicker(time.Duration(d.config.Daemon.Interval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Printf("\nReceived shutdown signal. Stopping daemon...\n")

			// Gracefully stop any running processes
			runningProcesses := d.processManager.GetRunningProcesses()
			totalProcesses := len(runningProcesses) + len(d.resumeProcesses)

			if totalProcesses > 0 {
				fmt.Printf("Terminating %d running Claude processes...\n", totalProcesses)

				// Terminate regular Claude processes
				for _, process := range runningProcesses {
					if process.Cmd != nil && process.Cmd.Process != nil {
						fmt.Printf("  Terminating process for issue #%d (PID: %d)\n", process.IssueNum, process.Cmd.Process.Pid)
						process.Cmd.Process.Signal(syscall.SIGTERM)
					}
				}

				// Terminate resume processes
				for issueID, cmd := range d.resumeProcesses {
					if cmd != nil && cmd.Process != nil {
						fmt.Printf("  Terminating resume process for issue #%d (PID: %d)\n", issueID, cmd.Process.Pid)
						cmd.Process.Signal(syscall.SIGTERM)
					}
				}

				// Give processes a moment to terminate gracefully
				fmt.Printf("Waiting 3 seconds for processes to terminate...\n")
				time.Sleep(3 * time.Second)

				// Force kill any remaining processes
				for _, process := range runningProcesses {
					if process.Cmd != nil && process.Cmd.Process != nil {
						process.Cmd.Process.Kill()
					}
				}

				for _, cmd := range d.resumeProcesses {
					if cmd != nil && cmd.Process != nil {
						cmd.Process.Kill()
					}
				}
			}

			fmt.Printf("Daemon stopped.\n")
			return nil

		case <-ticker.C:
			// Check if context was cancelled before starting work
			select {
			case <-ctx.Done():
				fmt.Printf("\nOperation cancelled before processing\n")
				return nil
			default:
			}

			timestamp := time.Now().Format("2006-01-02 15:04:05")
			fmt.Printf("[%s] Checking for issues to process...\n", timestamp)

			// 1. Check for new issues with 'claude' label (spawn new sessions)
			fmt.Printf("[%s] DEBUG: Starting checkForNewClaudeIssues...\n", timestamp)
			newIssues, err := d.checkForNewClaudeIssuesWithContext(ctx, processedIssues, timestamp)
			if err != nil {
				if ctx.Err() != nil {
					fmt.Printf("[%s] Operation cancelled by user\n", timestamp)
					continue
				}
				fmt.Printf("[%s] Error checking for new claude issues: %v\n", timestamp, err)
			}
			fmt.Printf("[%s] DEBUG: Finished checkForNewClaudeIssues, found %d new issues\n", timestamp, newIssues)

			// 2. Check for issues under review with new comments (resume sessions)
			fmt.Printf("[%s] DEBUG: Starting checkForReviewIssuesWithComments...\n", timestamp)
			resumedIssues, err := d.checkForReviewIssuesWithCommentsWithContext(ctx, timestamp)
			if err != nil {
				if ctx.Err() != nil {
					fmt.Printf("[%s] Operation cancelled by user\n", timestamp)
					continue
				}
				fmt.Printf("[%s] Error checking for review issues with comments: %v\n", timestamp, err)
			}
			fmt.Printf("[%s] DEBUG: Finished checkForReviewIssuesWithComments, resumed %d sessions\n", timestamp, resumedIssues)

			// Summary
			if newIssues > 0 || resumedIssues > 0 {
				fmt.Printf("[%s] Activity: %d new sessions started, %d sessions resumed\n", timestamp, newIssues, resumedIssues)
			} else {
				fmt.Printf("[%s] No new activity found\n", timestamp)
			}
			fmt.Printf("[%s] DEBUG: Finished polling cycle, waiting for next tick...\n", timestamp)
		}
	}
}

func (d *Daemon) RunWithMemoryMode(memoryMode bool) error {
	if memoryMode {
		// Use existing Run() method with SQLite session storage
		return d.Run()
	} else {
		// Run without memory - create new sessions without resuming
		return d.RunWithoutMemory()
	}
}

func (d *Daemon) RunWithoutMemory() error {
	// Step 1: Select project interactively
	fmt.Printf("=== Project Selection for Daemon Mode ===\n")
	projects, err := d.gitlabClient.GetAccessibleProjects()
	if err != nil {
		return fmt.Errorf("error fetching projects: %v", err)
	}

	selectedProject, err := d.selectProject(projects)
	if err != nil {
		return fmt.Errorf("error selecting project: %v", err)
	}

	d.selectedProject = selectedProject.PathWithNamespace
	fmt.Printf("Project selected: %s\n", d.selectedProject)

	// Step 2: Start daemon monitoring
	fmt.Printf("\n=== Starting Daemon Mode (No Memory) ===\n")
	if d.dryRun {
		fmt.Printf("*** DRY RUN MODE - No actual processing will occur ***\n")
	} else if d.semiDryRun {
		fmt.Printf("*** SEMI-DRY RUN MODE - Will clone repositories but not execute Claude ***\n")
	}
	fmt.Printf("Monitoring project: %s\n", d.selectedProject)
	fmt.Printf("Monitoring for issues with label: %s\n", d.config.Daemon.ClaudeLabel)
	fmt.Printf("Processing interval: %d seconds\n", d.config.Daemon.Interval)
	fmt.Printf("Memory mode: DISABLED (no session resumption)\n")
	fmt.Printf("Press Ctrl+C to stop...\n\n")

	// Set up signal handling for graceful shutdown with context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Cancel context when signal received
	go func() {
		<-sigCh
		fmt.Printf("\nReceived shutdown signal. Cancelling operations...\n")
		cancel()
	}()

	ticker := time.NewTicker(time.Duration(d.config.Daemon.Interval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Printf("\nReceived shutdown signal. Stopping daemon...\n")

			// Gracefully stop any running processes
			runningProcesses := d.processManager.GetRunningProcesses()
			totalProcesses := len(runningProcesses)

			if totalProcesses > 0 {
				fmt.Printf("Terminating %d running Claude processes...\n", totalProcesses)

				// Terminate regular Claude processes
				for _, process := range runningProcesses {
					if process.Cmd != nil && process.Cmd.Process != nil {
						fmt.Printf("  Terminating process for issue #%d (PID: %d)\n", process.IssueNum, process.Cmd.Process.Pid)
						process.Cmd.Process.Signal(syscall.SIGTERM)
					}
				}

				// Give processes a moment to terminate gracefully
				fmt.Printf("Waiting 3 seconds for processes to terminate...\n")
				time.Sleep(3 * time.Second)

				// Force kill any remaining processes
				for _, process := range runningProcesses {
					if process.Cmd != nil && process.Cmd.Process != nil {
						process.Cmd.Process.Kill()
					}
				}
			}

			fmt.Printf("Daemon stopped.\n")
			return nil

		case <-ticker.C:
			// Check if context was cancelled before starting work
			select {
			case <-ctx.Done():
				fmt.Printf("\nOperation cancelled before processing\n")
				return nil
			default:
			}

			// Create fresh processed issues map for this polling cycle
			processedIssues := make(map[int]bool)

			timestamp := time.Now().Format("2006-01-02 15:04:05")
			fmt.Printf("[%s] Checking for issues to process...\n", timestamp)

			// Check for new issues with 'claude' label
			fmt.Printf("[%s] DEBUG: Starting checkForNewClaudeIssues...\n", timestamp)
			newIssues, err := d.checkForNewClaudeIssuesWithContext(ctx, processedIssues, timestamp)
			if err != nil {
				if ctx.Err() != nil {
					fmt.Printf("[%s] Operation cancelled by user\n", timestamp)
					continue
				}
				fmt.Printf("[%s] Error checking for new claude issues: %v\n", timestamp, err)
			}
			fmt.Printf("[%s] DEBUG: Finished checkForNewClaudeIssues, found %d new issues\n", timestamp, newIssues)

			// Check for issues with 'waiting_human_review' label that have human comments
			fmt.Printf("[%s] DEBUG: Starting checkForHumanReviewIssues...\n", timestamp)
			reviewIssues, err := d.checkForHumanReviewIssuesWithContext(ctx, processedIssues, timestamp)
			if err != nil {
				if ctx.Err() != nil {
					fmt.Printf("[%s] Operation cancelled by user\n", timestamp)
					continue
				}
				fmt.Printf("[%s] Error checking for human review issues: %v\n", timestamp, err)
			}
			fmt.Printf("[%s] DEBUG: Finished checkForHumanReviewIssues, found %d issues with human comments\n", timestamp, reviewIssues)

			// Summary
			totalNewSessions := newIssues + reviewIssues
			if totalNewSessions > 0 {
				fmt.Printf("[%s] Activity: %d new sessions started (%d claude label, %d human review)\n", timestamp, totalNewSessions, newIssues, reviewIssues)
			} else {
				fmt.Printf("[%s] No new activity found\n", timestamp)
			}
			fmt.Printf("[%s] DEBUG: Finished polling cycle, waiting for next tick...\n", timestamp)
		}
	}
}

func (d *Daemon) GetProcessStatus() {
	fmt.Printf("=== Process Status ===\n")

	running := d.processManager.GetRunningProcesses()
	completed := d.processManager.GetProcessesByStatus("completed")
	failed := d.processManager.GetProcessesByStatus("failed")

	fmt.Printf("Running processes: %d\n", len(running))
	for _, process := range running {
		fmt.Printf("  - Issue #%d (ID: %s) - Running for %v\n",
			process.IssueNum, process.ID, time.Since(process.StartTime))
	}

	fmt.Printf("Completed processes: %d\n", len(completed))
	for _, process := range completed {
		fmt.Printf("  - Issue #%d (ID: %s) - Completed in %v\n",
			process.IssueNum, process.ID, time.Since(process.StartTime))
	}

	fmt.Printf("Failed processes: %d\n", len(failed))
	for _, process := range failed {
		fmt.Printf("  - Issue #%d (ID: %s) - Failed after %v\n",
			process.IssueNum, process.ID, time.Since(process.StartTime))
	}
}
