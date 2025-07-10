package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"peter/pkg/claude"
	"peter/pkg/config"
	"peter/pkg/daemon"
	"peter/pkg/gitlab"
)

func generateConfigTemplate() error {
	template := `# Peter GitLab Automation Configuration
# Edit these values with your GitLab credentials and preferences

# GitLab Configuration (REQUIRED)
GITLAB_URL=https://gitlab.com
GITLAB_TOKEN=glpat-your-token-here
GITLAB_USERNAME=your-gitlab-username

# Claude Configuration
CLAUDE_COMMAND=claude
CLAUDE_FLAGS="--dangerously-skip-permissions --output-format stream-json --verbose"

# Project Configuration (Optional - will be set via interactive mode)
DEFAULT_PROJECT_PATH=

# Daemon Configuration (Optional)
DAEMON_INTERVAL=10
CLAUDE_LABEL=claude
PROCESS_LABEL=picked_up_by_claude
REVIEW_LABEL=waiting_human_review
`

	return os.WriteFile(".env", []byte(template), 0644)
}

func selectProject(projects []gitlab.Project) (*gitlab.Project, error) {
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

func selectIssue(issues []gitlab.Issue) (*gitlab.Issue, error) {
	if len(issues) == 0 {
		return nil, fmt.Errorf("no issues available")
	}

	if len(issues) == 1 {
		fmt.Printf("Only one issue available: #%d %s\n", issues[0].IID, issues[0].Title)
		return &issues[0], nil
	}

	fmt.Printf("\nSelect an issue:\n")
	for i, issue := range issues {
		fmt.Printf("%d. #%d: %s\n", i+1, issue.IID, issue.Title)
		fmt.Printf("   State: %s\n", issue.State)
		if len(issue.Labels) > 0 {
			fmt.Printf("   Labels: %s\n", strings.Join(issue.Labels, ", "))
		}
		fmt.Printf("   Author: %s\n", issue.Author.Name)
		if issue.Assignee.Name != "" {
			fmt.Printf("   Assignee: %s\n", issue.Assignee.Name)
		}
		fmt.Printf("   Created: %s\n", issue.CreatedAt)
		fmt.Printf("   URL: %s\n\n", issue.WebURL)
	}

	fmt.Printf("Enter issue number (1-%d): ", len(issues))

	var choice int
	for {
		_, err := fmt.Scanf("%d", &choice)
		if err != nil {
			fmt.Printf("Invalid input. Please enter a number: ")
			continue
		}

		if choice < 1 || choice > len(issues) {
			fmt.Printf("Invalid choice. Please enter a number between 1 and %d: ", len(issues))
			continue
		}

		break
	}

	selected := &issues[choice-1]
	fmt.Printf("\nSelected issue: #%d %s\n", selected.IID, selected.Title)
	return selected, nil
}

func selectLabelFilter() string {
	fmt.Printf("\nFilter issues by label:\n")
	fmt.Printf("1. All issues (no filter)\n")
	fmt.Printf("2. open\n")
	fmt.Printf("3. solved\n")
	fmt.Printf("4. picked_up_by_claude\n")
	fmt.Printf("Enter your choice (1-4): ")

	var choice int
	for {
		_, err := fmt.Scanf("%d", &choice)
		if err != nil {
			fmt.Printf("Invalid input. Please enter a number: ")
			continue
		}

		if choice < 1 || choice > 4 {
			fmt.Printf("Invalid choice. Please enter a number between 1 and 4: ")
			continue
		}

		break
	}

	switch choice {
	case 1:
		return ""
	case 2:
		return "open"
	case 3:
		return "solved"
	case 4:
		return "picked_up_by_claude"
	default:
		return ""
	}
}

func runInteractiveWorkflow(gitlabClient *gitlab.Client, cfg *config.Config) error {
	// Step 1: Select project
	fmt.Printf("=== Project Selection ===\n")
	projects, err := gitlabClient.GetAccessibleProjects()
	if err != nil {
		return fmt.Errorf("error fetching projects: %v", err)
	}

	selectedProject, err := selectProject(projects)
	if err != nil {
		return fmt.Errorf("error selecting project: %v", err)
	}

	// Save the selection
	if err := config.SaveProjectSelection(selectedProject.PathWithNamespace); err != nil {
		fmt.Printf("Warning: Could not save project selection: %v\n", err)
	} else {
		fmt.Printf("Project selection saved to peter.yaml\n")
	}

	// Step 2: Select label filter
	fmt.Printf("\n=== Issue Filtering ===\n")
	labelFilter := selectLabelFilter()

	// Step 3: Fetch and display issues
	fmt.Printf("\n=== Issue Selection ===\n")
	var labels []string
	if labelFilter != "" {
		labels = append(labels, labelFilter)
		fmt.Printf("Fetching issues with label '%s' from project %s...\n", labelFilter, selectedProject.PathWithNamespace)
	} else {
		fmt.Printf("Fetching all open issues from project %s...\n", selectedProject.PathWithNamespace)
	}

	issues, err := gitlabClient.GetProjectIssues(selectedProject.PathWithNamespace, labels, "opened")
	if err != nil {
		return fmt.Errorf("error fetching issues: %v", err)
	}

	if len(issues) == 0 {
		fmt.Printf("No issues found with the selected criteria.\n")
		return nil
	}

	fmt.Printf("Found %d issues:\n", len(issues))

	// Step 4: Select issue
	selectedIssue, err := selectIssue(issues)
	if err != nil {
		return fmt.Errorf("error selecting issue: %v", err)
	}

	// Step 5: Show final result
	fmt.Printf("\n=== Summary ===\n")
	fmt.Printf("Selected Project: %s\n", selectedProject.PathWithNamespace)
	fmt.Printf("Selected Issue: #%d %s\n", selectedIssue.IID, selectedIssue.Title)
	fmt.Printf("Issue URL: %s\n", selectedIssue.WebURL)

	// Ask if user wants to process the issue now
	fmt.Printf("\nWhat would you like to do?\n")
	fmt.Printf("1. Process this issue now\n")
	fmt.Printf("2. Debug MCP integration for this issue\n")
	fmt.Printf("3. Exit\n")
	fmt.Printf("Enter your choice (1-3): ")
	
	var choice int
	fmt.Scanf("%d", &choice)
	
	switch choice {
	case 1:
		return processIssue(selectedIssue.IID, cfg)
	case 2:
		return debugMCPForIssue(selectedIssue.IID, cfg)
	case 3:
		fmt.Printf("You can process this issue later with: go run main.go -issue %d\n", selectedIssue.IID)
		return nil
	default:
		fmt.Printf("Invalid choice. You can process this issue later with: go run main.go -issue %d\n", selectedIssue.IID)
		return nil
	}
}

func processIssue(issueNumber int, cfg *config.Config) error {
	return processIssueWithOptions(issueNumber, cfg, false, false)
}

func processIssueWithOptions(issueNumber int, cfg *config.Config, dryRun bool, semiDryRun bool) error {
	processManager := claude.NewProcessManager()

	fmt.Printf("Processing issue #%d...\n", issueNumber)

	processID := fmt.Sprintf("issue-%d-%d", issueNumber, time.Now().Unix())

	// For semi-dry-run, we want to clone but not execute
	actualDryRun := dryRun || semiDryRun
	
	process, err := claude.CreateProcessWithCallbackAndGitlabDryRun(
		issueNumber,
		processID,
		cfg.Claude.Command,
		cfg.Claude.Flags,
		cfg.Projects.DefaultPath,
		cfg.GitLab.Username,
		cfg.GitLab.URL,
		dryRun, // Only pass true dry-run for repository cloning
		nil,
		nil,
	)
	if err != nil {
		return fmt.Errorf("error creating claude process: %v", err)
	}

	if actualDryRun {
		if dryRun {
			fmt.Println("\n=== DRY RUN MODE ===")
		} else {
			fmt.Println("\n=== SEMI-DRY RUN MODE ===")
			fmt.Println("Repository has been cloned/verified.")
		}
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
		if dryRun {
			fmt.Println("=== END DRY RUN ===")
		} else {
			fmt.Println("=== END SEMI-DRY RUN ===")
			
			// Additional checks in semi-dry-run mode
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
			
			// In semi-dry-run mode, show what cleanup would do
			fmt.Printf("\n=== REPOSITORY CLEANUP ===\n")
			fmt.Printf("After Claude finishes, the following cleanup would occur:\n")
			fmt.Printf("- Reset any uncommitted changes (git reset --hard HEAD)\n")
			fmt.Printf("- Remove untracked files (git clean -fd)\n")
			fmt.Printf("- Switch back to main branch\n")
			fmt.Printf("- Delete any issue-* branches\n")
			fmt.Printf("- Pull latest changes\n")
			fmt.Printf("Repository will be ready for the next parallel session\n")
		}
		return nil
	}

	processManager.AddProcess(process)

	if err := claude.RunProcess(process); err != nil {
		return fmt.Errorf("error executing claude command: %v", err)
	}

	return nil
}

func debugMCPForIssue(issueNumber int, cfg *config.Config) error {
	fmt.Printf("Starting MCP debug session for issue #%d...\n", issueNumber)

	// Get project path from config or interactive selection
	projectPath := cfg.Projects.DefaultPath
	if projectPath == "" {
		fmt.Println("No project configured. Please select a project:")
		// Note: This is a simplified fallback - in a real implementation, 
		// you'd call project selection logic here
		return fmt.Errorf("no project configured for MCP debug")
	}

	processManager := claude.NewProcessManager()
	
	process, err := claude.CreateMCPDebugProcess(issueNumber, cfg, projectPath)
	if err != nil {
		return fmt.Errorf("error creating MCP debug process: %v", err)
	}

	processManager.AddProcess(process)

	if err := claude.RunProcess(process); err != nil {
		return fmt.Errorf("error running MCP debug process: %v", err)
	}

	return nil
}

func testLabelFiltering(gitlabClient *gitlab.Client, cfg *config.Config) error {
	if cfg.Projects.DefaultPath == "" {
		return fmt.Errorf("no project configured. Please run with -interactive first")
	}

	fmt.Printf("Testing label filtering for project: %s\n\n", cfg.Projects.DefaultPath)

	// Test 1: Get all open issues
	fmt.Printf("=== Test 1: All Open Issues ===\n")
	allIssues, err := gitlabClient.GetProjectIssues(cfg.Projects.DefaultPath, []string{}, "opened")
	if err != nil {
		return fmt.Errorf("failed to fetch all issues: %v", err)
	}

	fmt.Printf("Found %d total open issues:\n", len(allIssues))
	for _, issue := range allIssues {
		fmt.Printf("  #%d: %s\n", issue.IID, issue.Title)
		fmt.Printf("    Labels: [%s]\n", strings.Join(issue.Labels, ", "))
		fmt.Printf("    State: %s\n\n", issue.State)
	}

	// Test 2: Filter by claude label
	fmt.Printf("=== Test 2: Issues with '%s' label ===\n", cfg.Daemon.ClaudeLabel)
	claudeIssues, err := gitlabClient.GetProjectIssues(cfg.Projects.DefaultPath, []string{cfg.Daemon.ClaudeLabel}, "opened")
	if err != nil {
		return fmt.Errorf("failed to fetch claude issues: %v", err)
	}

	fmt.Printf("Found %d issues with '%s' label:\n", len(claudeIssues), cfg.Daemon.ClaudeLabel)
	for _, issue := range claudeIssues {
		fmt.Printf("  #%d: %s\n", issue.IID, issue.Title)
		fmt.Printf("    Labels: [%s]\n", strings.Join(issue.Labels, ", "))
	}

	// Test 3: Manual filtering to see if API is working correctly
	fmt.Printf("\n=== Test 3: Manual Filter Check ===\n")
	fmt.Printf("Manually filtering all issues for label '%s':\n", cfg.Daemon.ClaudeLabel)

	manualCount := 0
	for _, issue := range allIssues {
		for _, label := range issue.Labels {
			if label == cfg.Daemon.ClaudeLabel {
				manualCount++
				fmt.Printf("  Manual match #%d: %s\n", issue.IID, issue.Title)
				break
			}
		}
	}

	fmt.Printf("\nSummary:\n")
	fmt.Printf("- API filtered results: %d issues\n", len(claudeIssues))
	fmt.Printf("- Manual filtering: %d issues\n", manualCount)

	if len(claudeIssues) != manualCount {
		fmt.Printf("⚠️  MISMATCH! API filtering may not be working correctly.\n")
	} else {
		fmt.Printf("✅ API filtering matches manual filtering.\n")
	}

	return nil
}

func main() {
	var issueNumber int
	var listProjects bool
	var searchQuery string
	var selectProjectFlag bool
	var listIssues bool
	var filterLabel string
	var selectIssueFlag bool
	var daemonMode bool
	var testLabels bool
	var debugMCP bool
	var processStatus bool
	var dryRun bool
	var semiDryRun bool
	var memoryMode bool
	var generateConfig bool
	flag.IntVar(&issueNumber, "issue", 0, "GitLab issue number to process")
	flag.BoolVar(&listProjects, "list-projects", false, "List accessible GitLab projects")
	flag.StringVar(&searchQuery, "search", "", "Search for projects by name")
	flag.BoolVar(&selectProjectFlag, "interactive", false, "Interactive project and issue selection")
	flag.BoolVar(&listIssues, "list-issues", false, "List issues in the selected project")
	flag.StringVar(&filterLabel, "label", "", "Filter issues by label (solved, open, picked_up_by_claude)")
	flag.BoolVar(&selectIssueFlag, "select-issue", false, "Interactive issue selection")
	flag.BoolVar(&daemonMode, "daemon", false, "Run in daemon mode to monitor for issues with 'claude' label")
	flag.BoolVar(&testLabels, "test-labels", false, "Test label filtering functionality")
	flag.BoolVar(&debugMCP, "debug-mcp", false, "Debug MCP (Model Context Protocol) integration")
	flag.BoolVar(&processStatus, "status", false, "Show process status (requires daemon mode)")
	flag.BoolVar(&dryRun, "dry-run", false, "Show the prompt that would be sent to Claude without executing")
	flag.BoolVar(&semiDryRun, "semi-dry-run", false, "Clone repository and show prompt without executing Claude")
	flag.BoolVar(&memoryMode, "memory", false, "Enable SQLite session storage and resume functionality")
	flag.BoolVar(&generateConfig, "generate-config", false, "Generate a template .env configuration file")
	flag.Parse()

	// Handle generate-config flag first
	if generateConfig {
		if err := generateConfigTemplate(); err != nil {
			fmt.Printf("Error generating config template: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Generated .env template file. Please edit it with your GitLab credentials.")
		return
	}

	// Check for conflicting flags
	if dryRun && semiDryRun {
		fmt.Println("Error: Cannot use both -dry-run and -semi-dry-run flags together")
		fmt.Println("  -dry-run: Shows what would happen without any changes")
		fmt.Println("  -semi-dry-run: Clones repository but doesn't run Claude")
		os.Exit(1)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Printf("Error loading configuration: %v\n", err)
		os.Exit(1)
	}

	if err := config.Validate(cfg); err != nil {
		fmt.Printf("Configuration error: %v\n", err)
		os.Exit(1)
	}

	gitlabClient := gitlab.NewClient(cfg.GitLab.URL, cfg.GitLab.Token)

	// Test connection first
	fmt.Printf("Testing GitLab connection...\n")
	if err := gitlabClient.TestConnection(); err != nil {
		fmt.Printf("GitLab connection test failed: %v\n", err)
		fmt.Printf("Please check your GitLab URL and token configuration\n")
		os.Exit(1)
	}
	fmt.Printf("GitLab connection successful!\n")

	if listProjects {
		projects, err := gitlabClient.GetAccessibleProjects()
		if err != nil {
			fmt.Printf("Error fetching projects: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Found %d accessible projects:\n\n", len(projects))
		for _, project := range projects {
			fmt.Printf("ID: %d\nName: %s\nPath: %s\nDescription: %s\nWebURL: %s\nVisibility: %s\n\n",
				project.ID, project.Name, project.PathWithNamespace, project.Description, project.WebURL, project.Visibility)
		}
		return
	}

	if searchQuery != "" {
		projects, err := gitlabClient.SearchProjects(searchQuery)
		if err != nil {
			fmt.Printf("Error searching projects: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Found %d projects matching '%s':\n\n", len(projects), searchQuery)
		for _, project := range projects {
			fmt.Printf("ID: %d\nName: %s\nPath: %s\nDescription: %s\nWebURL: %s\n\n",
				project.ID, project.Name, project.PathWithNamespace, project.Description, project.WebURL)
		}
		return
	}

	if selectProjectFlag {
		if err := runInteractiveWorkflow(gitlabClient, cfg); err != nil {
			fmt.Printf("Error in interactive workflow: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if daemonMode {
		var d *daemon.Daemon
		if dryRun {
			d = daemon.NewWithDryRun(gitlabClient, cfg, true)
		} else if semiDryRun {
			d = daemon.NewWithSemiDryRun(gitlabClient, cfg)
		} else {
			d = daemon.New(gitlabClient, cfg)
		}
		if err := d.RunWithMemoryMode(memoryMode); err != nil {
			fmt.Printf("Error in daemon mode: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if processStatus {
		// Note: This would require a running daemon instance
		// For now, just show a message about usage
		fmt.Printf("Process status requires a running daemon instance.\n")
		fmt.Printf("To use this feature, you would need to implement IPC communication\n")
		fmt.Printf("between the daemon and the status command.\n")
		return
	}

	if testLabels {
		if err := testLabelFiltering(gitlabClient, cfg); err != nil {
			fmt.Printf("Error testing labels: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if debugMCP {
		// Get project path from config or interactive selection
		projectPath := cfg.Projects.DefaultPath
		if projectPath == "" {
			fmt.Println("No project configured. Please run: go run main.go -interactive")
			os.Exit(1)
		}
		
		if err := claude.TestGitLabMCPIntegration(cfg, gitlabClient, projectPath); err != nil {
			fmt.Printf("Error testing MCP: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Handle issue-related operations
	if listIssues || selectIssueFlag {
		if cfg.Projects.DefaultPath == "" {
			fmt.Println("Error: No project selected. Please run: go run main.go -interactive")
			os.Exit(1)
		}

		var labels []string
		if filterLabel != "" {
			labels = append(labels, filterLabel)
		}

		issues, err := gitlabClient.GetProjectIssues(cfg.Projects.DefaultPath, labels, "opened")
		if err != nil {
			fmt.Printf("Error fetching issues: %v\n", err)
			os.Exit(1)
		}

		if listIssues {
			fmt.Printf("Found %d issues in project %s:\n\n", len(issues), cfg.Projects.DefaultPath)
			for _, issue := range issues {
				fmt.Printf("Issue #%d: %s\n", issue.IID, issue.Title)
				fmt.Printf("  State: %s\n", issue.State)
				if len(issue.Labels) > 0 {
					fmt.Printf("  Labels: %s\n", strings.Join(issue.Labels, ", "))
				}
				fmt.Printf("  Author: %s\n", issue.Author.Name)
				if issue.Assignee.Name != "" {
					fmt.Printf("  Assignee: %s\n", issue.Assignee.Name)
				}
				fmt.Printf("  Created: %s\n", issue.CreatedAt)
				fmt.Printf("  URL: %s\n\n", issue.WebURL)
			}
			return
		}

		if selectIssueFlag {
			selectedIssue, err := selectIssue(issues)
			if err != nil {
				fmt.Printf("Error selecting issue: %v\n", err)
				os.Exit(1)
			}

			fmt.Printf("Selected issue: #%d %s\n", selectedIssue.IID, selectedIssue.Title)
			fmt.Printf("You can now run: go run main.go -issue %d\n", selectedIssue.IID)
			return
		}
	}

	if issueNumber == 0 {
		fmt.Println("Error: Please provide an issue number using -issue flag")
		fmt.Println("Usage: peter -issue 123")
		fmt.Println("       peter -issue 123 -dry-run")
		fmt.Println("       peter -issue 123 -semi-dry-run")
		fmt.Println("       peter -generate-config")
		fmt.Println("       peter -list-projects")
		fmt.Println("       peter -search backend")
		fmt.Println("       peter -interactive")
		fmt.Println("       peter -list-issues")
		fmt.Println("       peter -list-issues -label open")
		fmt.Println("       peter -select-issue")
		fmt.Println("       peter -select-issue -label solved")
		fmt.Println("       peter -daemon")
		fmt.Println("       peter -daemon -memory")
		fmt.Println("       peter -daemon -dry-run")
		fmt.Println("       peter -daemon -semi-dry-run")
		fmt.Println("       peter -test-labels")
		fmt.Println("       peter -debug-mcp")
		fmt.Println("       peter -status")
		os.Exit(1)
	}

	if err := processIssueWithOptions(issueNumber, cfg, dryRun, semiDryRun); err != nil {
		fmt.Printf("Error processing issue: %v\n", err)
		os.Exit(1)
	}
}