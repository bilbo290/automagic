package daemon

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"peter/pkg/claude"
	"peter/pkg/config"
	"peter/pkg/gitlab"
)

type Daemon struct {
	gitlabClient    *gitlab.Client
	config          *config.Config
	selectedProject string
	processManager  *claude.ProcessManager
	dryRun          bool
}

func New(gitlabClient *gitlab.Client, config *config.Config) *Daemon {
	return &Daemon{
		gitlabClient:   gitlabClient,
		config:         config,
		processManager: claude.NewProcessManager(),
		dryRun:         false,
	}
}

func NewWithDryRun(gitlabClient *gitlab.Client, config *config.Config, dryRun bool) *Daemon {
	return &Daemon{
		gitlabClient:   gitlabClient,
		config:         config,
		processManager: claude.NewProcessManager(),
		dryRun:         dryRun,
	}
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
	} else {
		fmt.Printf("[%s] Processing issue #%d: %s\n", timestamp, issue.IID, issue.Title)
	}

	// Update labels to mark as being processed
	newLabels := make([]string, 0)
	for _, label := range issue.Labels {
		if label != d.config.Daemon.ClaudeLabel {
			newLabels = append(newLabels, label)
		}
	}
	newLabels = append(newLabels, d.config.Daemon.ProcessLabel)

	if d.dryRun {
		fmt.Printf("[%s] [DRY RUN] Would update labels: remove '%s', add '%s'\n", timestamp, d.config.Daemon.ClaudeLabel, d.config.Daemon.ProcessLabel)
	} else {
		if err := d.gitlabClient.UpdateIssueLabels(d.selectedProject, issue.IID, newLabels); err != nil {
			return fmt.Errorf("failed to update issue labels: %v", err)
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
	} else {
		fmt.Printf("Starting async process for issue #%d...\n", issueNumber)
	}

	processID := fmt.Sprintf("issue-%d-%d", issueNumber, time.Now().Unix())

	// Define completion labels - remove process label and add solved label
	completionLabels := []string{"solved"}

	// Create completion callback
	onCompletion := func(process *claude.Process, success bool) error {
		timestamp := time.Now().Format("2006-01-02 15:04:05")
		
		if success {
			fmt.Printf("[%s] Successfully completed issue #%d\n", timestamp, process.IssueNum)
			
			// Update labels to mark as solved
			newLabels := make([]string, 0)
			
			// Get current issue to get current labels
			issue, err := d.gitlabClient.GetIssue(d.selectedProject, process.IssueNum)
			if err != nil {
				fmt.Printf("[%s] Warning: failed to get issue #%d for label update: %v\n", timestamp, process.IssueNum, err)
				return nil
			}
			
			// Remove process label and add solved label
			for _, label := range issue.Labels {
				if label != d.config.Daemon.ProcessLabel {
					newLabels = append(newLabels, label)
				}
			}
			newLabels = append(newLabels, "solved")
			
			// Update labels
			if err := d.gitlabClient.UpdateIssueLabels(d.selectedProject, process.IssueNum, newLabels); err != nil {
				fmt.Printf("[%s] Warning: failed to update completion labels for issue #%d: %v\n", timestamp, process.IssueNum, err)
			} else {
				fmt.Printf("[%s] Updated labels for issue #%d to 'solved'\n", timestamp, process.IssueNum)
			}
		} else {
			fmt.Printf("[%s] Failed to complete issue #%d\n", timestamp, process.IssueNum)
			
			// Get current issue to get current labels
			issue, err := d.gitlabClient.GetIssue(d.selectedProject, process.IssueNum)
			if err != nil {
				fmt.Printf("[%s] Warning: failed to get issue #%d for label update: %v\n", timestamp, process.IssueNum, err)
				return nil
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
		
		return nil
	}

	process, err := claude.CreateProcessWithCallback(
		issueNumber,
		processID,
		d.config.Claude.Command,
		d.config.Claude.Flags,
		d.selectedProject,
		d.config.GitLab.Username,
		completionLabels,
		onCompletion,
	)
	if err != nil {
		return fmt.Errorf("error creating claude process: %v", err)
	}

	if d.dryRun {
		fmt.Println("\n=== DRY RUN MODE (Daemon) ===")
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
		fmt.Println("=== END DRY RUN ===\n")
		
		// In dry-run mode, still update labels to show what would happen
		fmt.Printf("[DRY RUN] Would update labels: remove '%s', add 'solved' on completion\n", d.config.Daemon.ProcessLabel)
	} else {
		d.processManager.AddProcess(process)

		// Run the process asynchronously
		claude.RunProcessAsync(process, d.processManager)
	}

	return nil
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
	}
	fmt.Printf("Monitoring project: %s\n", d.selectedProject)
	fmt.Printf("Monitoring for issues with label: %s\n", d.config.Daemon.ClaudeLabel)
	fmt.Printf("Processing interval: %d seconds\n", d.config.Daemon.Interval)
	fmt.Printf("Press Ctrl+C to stop...\n\n")

	// Set up signal handling for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Keep track of processed issues to avoid duplicates
	processedIssues := make(map[int]bool)

	ticker := time.NewTicker(time.Duration(d.config.Daemon.Interval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-sigCh:
			fmt.Printf("\nReceived shutdown signal. Stopping daemon...\n")
			return nil

		case <-ticker.C:
			fmt.Printf("[%s] Checking for new issues...\n", time.Now().Format("2006-01-02 15:04:05"))

			// Fetch issues with the claude label
			issues, err := d.gitlabClient.GetProjectIssues(d.selectedProject, []string{d.config.Daemon.ClaudeLabel}, "opened")
			if err != nil {
				fmt.Printf("[%s] Error fetching issues: %v\n", time.Now().Format("2006-01-02 15:04:05"), err)
				continue
			}

			newIssues := 0
			for _, issue := range issues {
				if !processedIssues[issue.IID] {
					processedIssues[issue.IID] = true
					newIssues++

					// Process issue asynchronously with automatic label updates
					if err := d.processIssueWithLabelUpdate(&issue); err != nil {
						fmt.Printf("[%s] Failed to start processing issue #%d: %v\n", time.Now().Format("2006-01-02 15:04:05"), issue.IID, err)
					}
				}
			}

			if newIssues > 0 {
				fmt.Printf("[%s] Found %d new issues to process\n", time.Now().Format("2006-01-02 15:04:05"), newIssues)
			} else {
				fmt.Printf("[%s] No new issues found\n", time.Now().Format("2006-01-02 15:04:05"))
			}
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