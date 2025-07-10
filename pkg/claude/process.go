package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Process struct {
	ID               string
	Cmd              *exec.Cmd
	IssueNum         int
	Status           string
	StartTime        time.Time
	CompletionLabels []string
	ProjectPath      string
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

// detectProjectDirectory finds the best working directory based on current location and project context
func detectProjectDirectory(projectPath string) (string, string, error) {
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
	if username == "" {
		username = "user"
	}

	// Detect the appropriate working directory and project information
	workingDir, moduleName, err := detectProjectDirectory(projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to detect project directory: %v", err)
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

**Important**: You are currently in the project directory. Use the current working directory for all operations.

Complexity Index:
T4: 1-3 minutes
T3: 4-6 minutes  
T2: 7-15 minutes
T1: 15+ minutes

Workflow:
1. **Retrieve Issue**: Get issue details using GitLab MCP
2. **Analyze Current State**: Check current branch, git status, and project structure
3. **Create Branch**: Create a new branch for the issue
   - `+"`git checkout -b issue-{issue_number}`"+` (or use worktree if needed)
4. **Implement Changes**: 
   - Read issue description thoroughly
   - Search codebase to understand the problem
   - Implement the required changes
   - Test changes locally
   - Commit changes with clear commit messages
5. **Push & Create MR**: Push branch and create merge request using GitLab MCP
6. **Report Status**: Comment on issue with progress

**Note**: The repository should already be available in the current directory. If not, you may need to clone it first.
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
		OnCompletion:     onCompletion,
	}

	return process, nil
}

func RunProcess(process *Process) error {
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
			fmt.Println(line)
			continue
		}

		if content, ok := jsonData["content"].(string); ok {
			fmt.Print(content)
		} else if delta, ok := jsonData["delta"].(string); ok {
			fmt.Print(delta)
		} else if result, ok := jsonData["result"].(string); ok {
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
