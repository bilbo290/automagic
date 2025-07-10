package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Process struct {
	ID               string
	ClaudeSessionID  string // Claude's actual UUID session ID for resume
	Cmd              *exec.Cmd
	IssueNum         int
	Status           string
	StartTime        time.Time
	CompletionLabels []string
	ProjectPath      string
	WorkingDir       string
	ClonedRepo       bool
	OnCompletion     func(process *Process, success bool) error
}

type ProcessManager struct {
	processes map[string]*Process
	mu        sync.RWMutex
}

func NewProcessManager() *ProcessManager {
	return &ProcessManager{
		processes: make(map[string]*Process),
	}
}

func (pm *ProcessManager) AddProcess(process *Process) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.processes[process.ID] = process
}

func (pm *ProcessManager) GetProcess(id string) (*Process, bool) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	process, exists := pm.processes[id]
	return process, exists
}

func (pm *ProcessManager) ListProcesses() []*Process {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	processes := make([]*Process, 0, len(pm.processes))
	for _, process := range pm.processes {
		processes = append(processes, process)
	}
	return processes
}

func (pm *ProcessManager) RemoveProcess(id string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	delete(pm.processes, id)
}

func (pm *ProcessManager) GetRunningProcesses() []*Process {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	var running []*Process
	for _, process := range pm.processes {
		if process.Status == "running" {
			running = append(running, process)
		}
	}
	return running
}

func (pm *ProcessManager) GetProcessesByStatus(status string) []*Process {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	var filtered []*Process
	for _, process := range pm.processes {
		if process.Status == status {
			filtered = append(filtered, process)
		}
	}
	return filtered
}

// extractSessionIDFromText extracts a UUID session ID from text output
func extractSessionIDFromText(text string) string {
	// UUID pattern: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
	uuidPattern := regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	
	// Look for common session ID patterns in Claude output
	sessionPatterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)session\s+id[:\s]+([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`),
		regexp.MustCompile(`(?i)session[:\s]+([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`),
		regexp.MustCompile(`(?i)id[:\s]+([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`),
	}
	
	// First try specific session ID patterns
	for _, pattern := range sessionPatterns {
		if matches := pattern.FindStringSubmatch(text); len(matches) > 1 {
			return matches[1]
		}
	}
	
	// Fallback: look for any UUID in the text (but be conservative)
	if strings.Contains(strings.ToLower(text), "session") {
		if match := uuidPattern.FindString(text); match != "" {
			return match
		}
	}
	
	return ""
}

// ensureRepositoryExists checks if the repository exists locally and clones it if needed
// Returns: (repoPath, wasCloned, error)
func ensureRepositoryExists(projectPath string, gitlabURL string, dryRun bool) (string, bool, error) {
	// Extract project name from path (e.g., "vbi/backend/vb_integration" -> "vb_integration")
	pathParts := strings.Split(projectPath, "/")
	if len(pathParts) == 0 {
		return "", false, fmt.Errorf("invalid project path: %s", projectPath)
	}
	projectName := pathParts[len(pathParts)-1]
	
	// Check current directory first
	cwd, err := os.Getwd()
	if err != nil {
		return "", false, fmt.Errorf("failed to get current working directory: %v", err)
	}
	
	// Check if we're already in the project directory
	if filepath.Base(cwd) == projectName {
		// Verify it's a git repository
		gitDir := filepath.Join(cwd, ".git")
		if _, err := os.Stat(gitDir); err == nil {
			fmt.Printf("Already in project directory: %s\n", cwd)
			return cwd, false, nil // Not cloned, already existed
		}
	}
	
	// Check if project exists as a subdirectory
	projectDir := filepath.Join(cwd, projectName)
	gitDir := filepath.Join(projectDir, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		fmt.Printf("Found existing repository at: %s\n", projectDir)
		return projectDir, false, nil // Not cloned, already existed
	}
	
	// Repository doesn't exist, need to clone
	if dryRun {
		fmt.Printf("[DRY RUN] Repository not found locally. Would clone %s\n", projectPath)
		cloneURL := fmt.Sprintf("%s/%s.git", strings.TrimSuffix(gitlabURL, "/"), projectPath)
		fmt.Printf("[DRY RUN] Clone command: git clone %s %s\n", cloneURL, projectName)
		fmt.Printf("[DRY RUN] Would clone to: %s\n", projectDir)
		return projectDir, true, nil // Would be cloned in real mode
	} else {
		fmt.Printf("Repository not found locally. Cloning %s...\n", projectPath)
		
		// Construct clone URL
		cloneURL := fmt.Sprintf("%s/%s.git", strings.TrimSuffix(gitlabURL, "/"), projectPath)
		
		// Clone the repository
		cmd := exec.Command("git", "clone", cloneURL, projectName)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Dir = cwd
		
		if err := cmd.Run(); err != nil {
			return "", false, fmt.Errorf("failed to clone repository: %v", err)
		}
		
		fmt.Printf("Successfully cloned repository to: %s\n", projectDir)
		return projectDir, true, nil // Was actually cloned
	}
}

// DetectProjectDirectory finds the best working directory based on current location and project context
func DetectProjectDirectory(projectPath string) (string, string, error) {
	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("failed to get current working directory: %v", err)
	}

	// Check if we're in a Go project by looking for go.mod
	goModPath := filepath.Join(cwd, "go.mod")
	if _, err := os.Stat(goModPath); err == nil {
		// We're in a Go project directory, read module name
		content, err := os.ReadFile(goModPath)
		if err == nil {
			lines := strings.Split(string(content), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "module ") {
					moduleName := strings.TrimPrefix(line, "module ")
					moduleName = strings.TrimSpace(moduleName)
					return cwd, moduleName, nil
				}
			}
		}
		return cwd, "", nil
	}

	// Check if we're in a subdirectory of a Go project
	dir := cwd
	for {
		parentDir := filepath.Dir(dir)
		if parentDir == dir {
			// Reached root directory
			break
		}
		
		goModPath := filepath.Join(parentDir, "go.mod")
		if _, err := os.Stat(goModPath); err == nil {
			// Found go.mod in parent directory
			content, err := os.ReadFile(goModPath)
			if err == nil {
				lines := strings.Split(string(content), "\n")
				for _, line := range lines {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "module ") {
						moduleName := strings.TrimPrefix(line, "module ")
						moduleName = strings.TrimSpace(moduleName)
						return parentDir, moduleName, nil
					}
				}
			}
			return parentDir, "", nil
		}
		dir = parentDir
	}

	// Check if directory name matches project name from GitLab path
	if projectPath != "" {
		pathParts := strings.Split(projectPath, "/")
		if len(pathParts) > 0 {
			expectedProjectName := pathParts[len(pathParts)-1]
			if strings.Contains(filepath.Base(cwd), expectedProjectName) {
				return cwd, "", nil
			}
		}
	}

	// Default to current working directory
	return cwd, "", nil
}

func CreateProcess(issueNumber int, processID string, claudeCommand, claudeFlags, projectPath, username string, customPrompt ...string) (*Process, error) {
	return CreateProcessWithCallback(issueNumber, processID, claudeCommand, claudeFlags, projectPath, username, nil, nil, customPrompt...)
}

func CreateProcessWithCallback(issueNumber int, processID string, claudeCommand, claudeFlags, projectPath, username string, completionLabels []string, onCompletion func(*Process, bool) error, customPrompt ...string) (*Process, error) {
	// For backward compatibility, use default GitLab URL
	return CreateProcessWithCallbackAndGitlab(issueNumber, processID, claudeCommand, claudeFlags, projectPath, username, "https://gitlab.com", completionLabels, onCompletion, customPrompt...)
}

func CreateProcessWithCallbackAndGitlab(issueNumber int, processID string, claudeCommand, claudeFlags, projectPath, username, gitlabURL string, completionLabels []string, onCompletion func(*Process, bool) error, customPrompt ...string) (*Process, error) {
	return CreateProcessWithCallbackAndGitlabDryRun(issueNumber, processID, claudeCommand, claudeFlags, projectPath, username, gitlabURL, false, completionLabels, onCompletion, customPrompt...)
}

func CreateProcessWithCallbackAndGitlabDryRun(issueNumber int, processID string, claudeCommand, claudeFlags, projectPath, username, gitlabURL string, dryRun bool, completionLabels []string, onCompletion func(*Process, bool) error, customPrompt ...string) (*Process, error) {
	if username == "" {
		username = "user"
	}

	// Ensure repository exists locally (clone if needed)
	repoDir, wasCloned, err := ensureRepositoryExists(projectPath, gitlabURL, dryRun)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure repository exists: %v", err)
	}

	// Now detect project information from the repository directory
	workingDir := repoDir
	moduleName := ""
	
	// Check for go.mod in the repository
	goModPath := filepath.Join(repoDir, "go.mod")
	if content, err := os.ReadFile(goModPath); err == nil {
		lines := strings.Split(string(content), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "module ") {
				moduleName = strings.TrimPrefix(line, "module ")
				moduleName = strings.TrimSpace(moduleName)
				break
			}
		}
	}

	var prompt string
	if len(customPrompt) > 0 && customPrompt[0] != "" {
		prompt = customPrompt[0]
	} else {
		// Build project context information
		projectInfo := fmt.Sprintf("- **GitLab Project Path**: `%s`\n- **Your Username**: @%s\n- **Current Working Directory**: `%s`", projectPath, username, workingDir)
		
		if moduleName != "" {
			projectInfo += fmt.Sprintf("\n- **Go Module**: `%s`", moduleName)
		}

		prompt = fmt.Sprintf(`# Look at issue %d and fix it
## Project Information
%s

Always use Gitlab MCP for Gitlab related tasks.
Use git for commit and push.

**Important**: The repository has been verified/cloned and you are now in the project directory.

Complexity Index:
T4: 1-3 minutes
T3: 4-6 minutes  
T2: 7-15 minutes
T1: 15+ minutes

## MANDATORY Workflow - Follow these steps in order:

### 1. **Retrieve & Analyze Issue** 
   - Get issue details using GitLab MCP
   - Read the issue description thoroughly
   - Read ALL existing comments on the issue to understand context and any previous attempts
   - Analyze the requirements and acceptance criteria

### 2. **Create and Post Implementation Plan** (REQUIRED)
   - Search the codebase to understand the current implementation
   - Identify files that need to be modified
   - Create a detailed plan with:
     - Summary of the issue
     - List of files to be modified
     - Step-by-step implementation approach
     - Testing strategy
   - **POST THIS PLAN AS A COMMENT ON THE GITLAB ISSUE using GitLab MCP**
   - Format the plan clearly with markdown

### 3. **Verify Current State**
   - Run 'git status' to check current branch and changes
   - Run 'git pull' to ensure you have the latest changes

### 4. **Create Branch**
   - Create a new branch for the issue: `+"`git checkout -b issue-{issue_number}`"+`

### 5. **Implement Changes**
   - Follow your posted plan
   - Make the necessary code changes
   - Test changes locally
   - Commit changes with clear commit messages

### 6. **Push & Create MR**
   - Push branch: `+"`git push -u origin issue-{issue_number}`"+`
   - Create merge request using GitLab MCP
   - Reference the issue in the MR description

### 7. **Final Update & Human Review**
   - Comment on the issue with the MR link and completion status
   - The issue will be automatically marked as "waiting_human_review"
   - Humans can now review your work and provide feedback
   - If they add comments with feedback, I will automatically resume this session to iterate

**IMPORTANT**: You MUST post your implementation plan to the GitLab issue before making any code changes. This ensures transparency and allows for feedback before implementation begins.

**Human Review Process**: After completion, the issue enters a review phase where:
- The issue label changes from "picked_up_by_claude" to "waiting_human_review"
- Humans can review the code, test the changes, and provide feedback
- Any new comments will automatically trigger a session resume with the feedback context
- Only when humans are satisfied should they manually change the label to "solved"
`, issueNumber, projectInfo)
	}

	// Set up environment first - this is crucial for MCP server initialization
	homeDir := os.Getenv("HOME")
	if homeDir == "" {
		return nil, fmt.Errorf("HOME environment variable not set")
	}

	// Use the user's shell to preserve environment
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}

	// Build command arguments
	args := []string{}
	if claudeFlags != "" {
		// Split flags by spaces (simple parsing - might need improvement for quoted args)
		args = strings.Fields(claudeFlags)
	}
	args = append(args, "-p", prompt)
	
	// Run claude directly without shell
	cmd := exec.Command(claudeCommand, args...)
	cmd.Stderr = os.Stderr

	// Set environment and working directory BEFORE anything else
	cmd.Env = os.Environ()
	// Use the detected working directory instead of home directory
	cmd.Dir = workingDir

	process := &Process{
		ID:               processID,
		Cmd:              cmd,
		IssueNum:         issueNumber,
		Status:           "starting",
		StartTime:        time.Now(),
		CompletionLabels: completionLabels,
		ProjectPath:      projectPath,
		WorkingDir:       workingDir,
		ClonedRepo:       wasCloned,
		OnCompletion:     onCompletion,
	}

	return process, nil
}

// cleanupRepositoryState cleans up the repository to prepare it for the next session
// DISABLED: Repository cleanup is now disabled to allow reuse of worktree issues
func cleanupRepositoryState(process *Process) {
	fmt.Printf("Repository cleanup disabled - leaving repository state unchanged at: %s\n", process.WorkingDir)
	fmt.Printf("Note: This allows reuse of existing worktree issues and preserves work in progress\n")
	
	// All cleanup operations are now disabled:
	// - No git reset --hard HEAD
	// - No git clean -fd
	// - No branch switching
	// - No branch deletion
	// - No git pull
	
	// This allows Claude to continue working on existing branches and preserves any work in progress
}

// runGitCommand executes a git command with the given arguments
func runGitCommand(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Stdout = nil // Suppress output for cleanup commands
	cmd.Stderr = nil
	return cmd.Run()
}

// detectMainBranch tries to detect the main branch name (main, master, develop, etc.)
func detectMainBranch() string {
	// Try to get the default branch from remote
	cmd := exec.Command("git", "symbolic-ref", "refs/remotes/origin/HEAD")
	if output, err := cmd.Output(); err == nil {
		// Output format: "refs/remotes/origin/main"
		parts := strings.Split(strings.TrimSpace(string(output)), "/")
		if len(parts) > 0 {
			return parts[len(parts)-1]
		}
	}
	
	// Fallback: try common branch names
	commonBranches := []string{"main", "master", "develop"}
	for _, branch := range commonBranches {
		cmd := exec.Command("git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
		if cmd.Run() == nil {
			return branch
		}
	}
	
	// If nothing found, return empty (will skip checkout)
	return ""
}

// cleanupIssueBranches removes local branches that look like issue branches
func cleanupIssueBranches() error {
	// Get list of local branches
	cmd := exec.Command("git", "branch", "--format=%(refname:short)")
	output, err := cmd.Output()
	if err != nil {
		return err
	}
	
	branches := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, branch := range branches {
		branch = strings.TrimSpace(branch)
		// Delete branches that start with "issue-"
		if strings.HasPrefix(branch, "issue-") {
			if err := runGitCommand("branch", "-D", branch); err != nil {
				fmt.Printf("Warning: failed to delete branch %s: %v\n", branch, err)
			} else {
				fmt.Printf("Deleted issue branch: %s\n", branch)
			}
		}
	}
	
	return nil
}

func RunProcess(process *Process) error {
	// Ensure cleanup happens even on early failures
	defer func() {
		if process.Status == "failed" {
			cleanupRepositoryState(process)
		}
	}()

	stdout, err := process.Cmd.StdoutPipe()
	if err != nil {
		process.Status = "failed"
		if process.OnCompletion != nil {
			process.OnCompletion(process, false)
		}
		return fmt.Errorf("error creating stdout pipe: %v", err)
	}

	if err := process.Cmd.Start(); err != nil {
		process.Status = "failed"
		if process.OnCompletion != nil {
			process.OnCompletion(process, false)
		}
		return fmt.Errorf("error starting claude command: %v", err)
	}

	process.Status = "running"

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()

		var jsonData map[string]interface{}
		if err := json.Unmarshal([]byte(line), &jsonData); err != nil {
			// Check for session ID in plain text output
			if process.ClaudeSessionID == "" {
				if sessionID := extractSessionIDFromText(line); sessionID != "" {
					process.ClaudeSessionID = sessionID
					fmt.Printf("DEBUG: Captured Claude session ID: %s\n", sessionID)
				}
			}
			fmt.Println(line)
			continue
		}

		// Check for session ID in JSON
		if process.ClaudeSessionID == "" {
			if sessionID, ok := jsonData["session_id"].(string); ok && sessionID != "" {
				process.ClaudeSessionID = sessionID
				fmt.Printf("DEBUG: Captured Claude session ID from JSON: %s\n", sessionID)
			}
		}

		if content, ok := jsonData["content"].(string); ok {
			// Check for session ID in content
			if process.ClaudeSessionID == "" {
				if sessionID := extractSessionIDFromText(content); sessionID != "" {
					process.ClaudeSessionID = sessionID
					fmt.Printf("DEBUG: Captured Claude session ID from content: %s\n", sessionID)
				}
			}
			fmt.Print(content)
		} else if delta, ok := jsonData["delta"].(string); ok {
			// Check for session ID in delta
			if process.ClaudeSessionID == "" {
				if sessionID := extractSessionIDFromText(delta); sessionID != "" {
					process.ClaudeSessionID = sessionID
					fmt.Printf("DEBUG: Captured Claude session ID from delta: %s\n", sessionID)
				}
			}
			fmt.Print(delta)
		} else if result, ok := jsonData["result"].(string); ok {
			// Check for session ID in result
			if process.ClaudeSessionID == "" {
				if sessionID := extractSessionIDFromText(result); sessionID != "" {
					process.ClaudeSessionID = sessionID
					fmt.Printf("DEBUG: Captured Claude session ID from result: %s\n", sessionID)
				}
			}
			fmt.Print(result)
		} else {
			fmt.Println(line)
		}
	}

	success := true
	if err := process.Cmd.Wait(); err != nil {
		process.Status = "failed"
		success = false
	} else {
		process.Status = "completed"
	}

	// Call completion callback if provided
	if process.OnCompletion != nil {
		if callbackErr := process.OnCompletion(process, success); callbackErr != nil {
			fmt.Printf("Warning: completion callback failed: %v\n", callbackErr)
		}
	}

	// Cleanup repository state after process completion (asynchronously to avoid blocking)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Printf("Warning: repository cleanup panicked for issue #%d: %v\n", process.IssueNum, r)
			}
		}()
		cleanupRepositoryState(process)
	}()

	if !success {
		return fmt.Errorf("error executing claude command")
	}

	return nil
}

func RunProcessAsync(process *Process, processManager *ProcessManager) {
	go func() {
		defer processManager.RemoveProcess(process.ID)
		if err := RunProcess(process); err != nil {
			fmt.Printf("Process %s failed: %v\n", process.ID, err)
		}
	}()
}
