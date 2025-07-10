package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	GitLab struct {
		URL      string `yaml:"url" env:"GITLAB_URL"`
		Token    string `yaml:"token" env:"GITLAB_TOKEN"`
		Username string `yaml:"username" env:"GITLAB_USERNAME"`
	} `yaml:"gitlab"`

	Claude struct {
		Command string `yaml:"command" env:"CLAUDE_COMMAND"`
		Flags   string `yaml:"flags" env:"CLAUDE_FLAGS"`
	} `yaml:"claude"`

	Projects struct {
		DefaultPath string `yaml:"default_path" env:"DEFAULT_PROJECT_PATH"`
	} `yaml:"projects"`

	Daemon struct {
		Interval     int    `yaml:"interval" env:"DAEMON_INTERVAL"`
		ClaudeLabel  string `yaml:"claude_label" env:"CLAUDE_LABEL"`
		ProcessLabel string `yaml:"process_label" env:"PROCESS_LABEL"`
		ReviewLabel  string `yaml:"review_label" env:"REVIEW_LABEL"`
	} `yaml:"daemon"`
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

func loadYAMLConfig(filename string) (*Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return &config, nil
}

func Load() (*Config, error) {
	var config Config

	// Try to load .env file first
	envFiles := []string{".env", ".env.local", filepath.Join(os.Getenv("HOME"), ".peter.env")}
	for _, envFile := range envFiles {
		if _, err := os.Stat(envFile); err == nil {
			if err := loadEnvFile(envFile); err != nil {
				fmt.Printf("Warning: failed to load %s: %v\n", envFile, err)
			}
			break
		}
	}

	// Try to load YAML config
	configFiles := []string{"peter.yaml", "peter.yml", "config.yaml", "config.yml", filepath.Join(os.Getenv("HOME"), ".peter.yaml")}
	for _, configFile := range configFiles {
		if yamlConfig, err := loadYAMLConfig(configFile); err == nil {
			config = *yamlConfig
			break
		}
	}

	// Override with environment variables
	if val := os.Getenv("GITLAB_URL"); val != "" {
		config.GitLab.URL = val
	}
	if val := os.Getenv("GITLAB_TOKEN"); val != "" {
		config.GitLab.Token = val
	}
	if val := os.Getenv("GITLAB_USERNAME"); val != "" {
		config.GitLab.Username = val
	}
	if val := os.Getenv("CLAUDE_COMMAND"); val != "" {
		config.Claude.Command = val
	}
	if val := os.Getenv("CLAUDE_FLAGS"); val != "" {
		config.Claude.Flags = val
	}
	if val := os.Getenv("DEFAULT_PROJECT_PATH"); val != "" {
		config.Projects.DefaultPath = val
	}

	// Set defaults
	if config.GitLab.URL == "" {
		config.GitLab.URL = "https://gitlab.com"
	}
	if config.Claude.Command == "" {
		config.Claude.Command = "claude"
	}
	if config.Claude.Flags == "" {
		config.Claude.Flags = "--dangerously-skip-permissions --output-format stream-json --verbose"
	}
	// DefaultPath is now optional - projects will be selected interactively
	// if config.Projects.DefaultPath == "" {
	// 	config.Projects.DefaultPath = "vbi/backend/vb_integration"
	// }
	if config.Daemon.Interval == 0 {
		config.Daemon.Interval = 10 // 10 seconds default
	}
	if config.Daemon.ClaudeLabel == "" {
		config.Daemon.ClaudeLabel = "claude"
	}
	if config.Daemon.ProcessLabel == "" {
		config.Daemon.ProcessLabel = "picked_up_by_claude"
	}
	if config.Daemon.ReviewLabel == "" {
		config.Daemon.ReviewLabel = "waiting_human_review"
	}

	return &config, nil
}

func Validate(config *Config) error {
	if config.GitLab.Token == "" {
		return fmt.Errorf("GitLab token is required. Set GITLAB_TOKEN environment variable or add it to your config file")
	}

	if config.GitLab.URL == "" {
		return fmt.Errorf("GitLab URL is required")
	}

	return nil
}

func SaveProjectSelection(projectPath string) error {
	configContent := fmt.Sprintf(`projects:
  default_path: %s
`, projectPath)

	return os.WriteFile("peter.yaml", []byte(configContent), 0644)
}