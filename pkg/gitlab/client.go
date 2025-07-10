package gitlab

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Issue struct {
	ID          int      `json:"id"`
	IID         int      `json:"iid"`
	ProjectID   int      `json:"project_id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	State       string   `json:"state"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
	Labels      []string `json:"labels"`
	WebURL      string   `json:"web_url"`
	Author      struct {
		ID       int    `json:"id"`
		Name     string `json:"name"`
		Username string `json:"username"`
	} `json:"author"`
	Assignee struct {
		ID       int    `json:"id"`
		Name     string `json:"name"`
		Username string `json:"username"`
	} `json:"assignee"`
}

type Project struct {
	ID                int    `json:"id"`
	Name              string `json:"name"`
	Path              string `json:"path"`
	PathWithNamespace string `json:"path_with_namespace"`
	Description       string `json:"description"`
	WebURL            string `json:"web_url"`
	DefaultBranch     string `json:"default_branch"`
	Visibility        string `json:"visibility"`
	LastActivityAt    string `json:"last_activity_at"`
}

type Client struct {
	BaseURL string
	Token   string
	client  *http.Client
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		BaseURL: baseURL,
		Token:   token,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) makeRequest(endpoint string) ([]byte, error) {
	url := fmt.Sprintf("%s/api/v4%s", c.BaseURL, endpoint)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

func (c *Client) TestConnection() error {
	_, err := c.makeRequest("/user")
	return err
}

func (c *Client) GetAccessibleProjects() ([]Project, error) {
	body, err := c.makeRequest("/projects?membership=true&per_page=100")
	if err != nil {
		return nil, err
	}

	var projects []Project
	if err := json.Unmarshal(body, &projects); err != nil {
		return nil, fmt.Errorf("failed to parse projects: %v", err)
	}

	return projects, nil
}

func (c *Client) GetProject(projectID string) (*Project, error) {
	endpoint := fmt.Sprintf("/projects/%s", projectID)
	body, err := c.makeRequest(endpoint)
	if err != nil {
		return nil, err
	}

	var project Project
	if err := json.Unmarshal(body, &project); err != nil {
		return nil, fmt.Errorf("failed to parse project: %v", err)
	}

	return &project, nil
}

func (c *Client) SearchProjects(query string) ([]Project, error) {
	endpoint := fmt.Sprintf("/projects?search=%s&membership=true&per_page=50", query)
	body, err := c.makeRequest(endpoint)
	if err != nil {
		return nil, err
	}

	var projects []Project
	if err := json.Unmarshal(body, &projects); err != nil {
		return nil, fmt.Errorf("failed to parse projects: %v", err)
	}

	return projects, nil
}

func (c *Client) GetProjectIssues(projectPath string, labels []string, state string) ([]Issue, error) {
	// URL encode the project path
	encodedPath := strings.ReplaceAll(projectPath, "/", "%2F")

	endpoint := fmt.Sprintf("/projects/%s/issues?per_page=100", encodedPath)

	if len(labels) > 0 {
		labelStr := strings.Join(labels, ",")
		endpoint += "&labels=" + labelStr
	}

	if state != "" {
		endpoint += "&state=" + state
	}

	body, err := c.makeRequest(endpoint)
	if err != nil {
		return nil, err
	}

	var issues []Issue
	if err := json.Unmarshal(body, &issues); err != nil {
		return nil, fmt.Errorf("failed to parse issues: %v", err)
	}

	return issues, nil
}

func (c *Client) GetIssue(projectPath string, issueIID int) (*Issue, error) {
	encodedPath := strings.ReplaceAll(projectPath, "/", "%2F")
	endpoint := fmt.Sprintf("/projects/%s/issues/%d", encodedPath, issueIID)

	body, err := c.makeRequest(endpoint)
	if err != nil {
		return nil, err
	}

	var issue Issue
	if err := json.Unmarshal(body, &issue); err != nil {
		return nil, fmt.Errorf("failed to parse issue: %v", err)
	}

	return &issue, nil
}

func (c *Client) UpdateIssueLabels(projectPath string, issueIID int, labels []string) error {
	encodedPath := strings.ReplaceAll(projectPath, "/", "%2F")
	endpoint := fmt.Sprintf("/projects/%s/issues/%d", encodedPath, issueIID)

	labelStr := strings.Join(labels, ",")
	url := fmt.Sprintf("%s/api/v4%s?labels=%s", c.BaseURL, endpoint, labelStr)

	req, err := http.NewRequest("PUT", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to update labels: status %d, body: %s", resp.StatusCode, string(body))
	}

	return nil
}