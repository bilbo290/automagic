package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	GitLab struct {
		URL      string
		Token    string
		Username string
	}

	Claude struct {
		Command string
		Flags   string
	}

	Projects struct {
		DefaultPath string
	}

	Daemon struct {
		Interval     int
		ClaudeLabel  string
		ProcessLabel string
		ReviewLabel  string
	}
}

func loadEnvFile(filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		// Remove quotes if present
		if (strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`)) ||
			(strings.HasPrefix(value, `'`) && strings.HasSuffix(value, `'`)) {
			value = value[1 : len(value)-1]
		}

		os.Setenv(key, value)
	}

	return scanner.Err()
}

func Load() (*Config, error) {
	var config Config

	// Try to load .env file first
	envFiles := []string{".env", ".env.local"}
	for _, envFile := range envFiles {
		if _, err := os.Stat(envFile); err == nil {
			if err := loadEnvFile(envFile); err != nil {
				fmt.Printf("Warning: failed to load %s: %v\n", envFile, err)
			}
			break
		}
	}

	// Load configuration from environment variables
	config.GitLab.URL = getEnvWithDefault("GITLAB_URL", "https://gitlab.com")
	config.GitLab.Token = os.Getenv("GITLAB_TOKEN")
	config.GitLab.Username = os.Getenv("GITLAB_USERNAME")

	config.Claude.Command = getEnvWithDefault("CLAUDE_COMMAND", "claude")
	config.Claude.Flags = getEnvWithDefault("CLAUDE_FLAGS", "--dangerously-skip-permissions --output-format stream-json --verbose")

	config.Projects.DefaultPath = os.Getenv("DEFAULT_PROJECT_PATH")

	intervalStr := getEnvWithDefault("DAEMON_INTERVAL", "10")
	interval, err := strconv.Atoi(intervalStr)
	if err != nil {
		fmt.Printf("Warning: invalid DAEMON_INTERVAL value '%s', using default 10\n", intervalStr)
		interval = 10
	}
	config.Daemon.Interval = interval

	config.Daemon.ClaudeLabel = getEnvWithDefault("CLAUDE_LABEL", "claude")
	config.Daemon.ProcessLabel = getEnvWithDefault("PROCESS_LABEL", "picked_up_by_claude")
	config.Daemon.ReviewLabel = getEnvWithDefault("REVIEW_LABEL", "waiting_human_review")

	return &config, nil
}

func getEnvWithDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func Validate(config *Config) error {
	if config.GitLab.Token == "" {
		return fmt.Errorf("GitLab token is required. Set GITLAB_TOKEN environment variable")
	}

	if config.GitLab.URL == "" {
		return fmt.Errorf("GitLab URL is required. Set GITLAB_URL environment variable")
	}

	if config.GitLab.Username == "" {
		return fmt.Errorf("GitLab username is required. Set GITLAB_USERNAME environment variable")
	}

	return nil
}

func SaveProjectSelection(projectPath string) error {
	// Create or update .env file with the selected project
	envFile := ".env"
	
	// Read existing .env file if it exists
	existingVars := make(map[string]string)
	if file, err := os.Open(envFile); err == nil {
		defer file.Close()
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				existingVars[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
			}
		}
	}

	// Update the project path
	existingVars["DEFAULT_PROJECT_PATH"] = projectPath

	// Write back to .env file
	file, err := os.Create(envFile)
	if err != nil {
		return fmt.Errorf("failed to create .env file: %v", err)
	}
	defer file.Close()

	// Write comment header
	fmt.Fprintln(file, "# Peter GitLab Automation Configuration")
	fmt.Fprintln(file, "# Generated automatically - you can edit these values")
	fmt.Fprintln(file, "")

	// Write all variables in a logical order
	writeEnvVar(file, "GITLAB_URL", existingVars)
	writeEnvVar(file, "GITLAB_TOKEN", existingVars)
	writeEnvVar(file, "GITLAB_USERNAME", existingVars)
	fmt.Fprintln(file, "")
	writeEnvVar(file, "CLAUDE_COMMAND", existingVars)
	writeEnvVar(file, "CLAUDE_FLAGS", existingVars)
	fmt.Fprintln(file, "")
	writeEnvVar(file, "DEFAULT_PROJECT_PATH", existingVars)
	fmt.Fprintln(file, "")
	writeEnvVar(file, "DAEMON_INTERVAL", existingVars)
	writeEnvVar(file, "CLAUDE_LABEL", existingVars)
	writeEnvVar(file, "PROCESS_LABEL", existingVars)
	writeEnvVar(file, "REVIEW_LABEL", existingVars)

	return nil
}

func writeEnvVar(file *os.File, key string, vars map[string]string) {
	if value, exists := vars[key]; exists && value != "" {
		// Quote the value if it contains spaces
		if strings.Contains(value, " ") {
			fmt.Fprintf(file, "%s=\"%s\"\n", key, value)
		} else {
			fmt.Fprintf(file, "%s=%s\n", key, value)
		}
	}
}

// PrintConfig prints the current configuration for debugging
func PrintConfig(config *Config) {
	fmt.Println("Current Configuration:")
	fmt.Printf("  GitLab URL: %s\n", config.GitLab.URL)
	fmt.Printf("  GitLab Username: %s\n", config.GitLab.Username)
	fmt.Printf("  GitLab Token: %s\n", maskToken(config.GitLab.Token))
	fmt.Printf("  Claude Command: %s\n", config.Claude.Command)
	fmt.Printf("  Claude Flags: %s\n", config.Claude.Flags)
	fmt.Printf("  Default Project: %s\n", config.Projects.DefaultPath)
	fmt.Printf("  Daemon Interval: %d seconds\n", config.Daemon.Interval)
	fmt.Printf("  Labels: %s → %s → %s\n", 
		config.Daemon.ClaudeLabel, 
		config.Daemon.ProcessLabel, 
		config.Daemon.ReviewLabel)
}

func maskToken(token string) string {
	if len(token) <= 8 {
		return "***"
	}
	return token[:4] + "***" + token[len(token)-4:]
}