package gitlab

import (
	"context"
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

type MergeRequest struct {
	ID           int    `json:"id"`
	IID          int    `json:"iid"`
	ProjectID    int    `json:"project_id"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	State        string `json:"state"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	SourceBranch string `json:"source_branch"`
	TargetBranch string `json:"target_branch"`
	WebURL       string `json:"web_url"`
	Author       struct {
		ID       int    `json:"id"`
		Name     string `json:"name"`
		Username string `json:"username"`
	} `json:"author"`
	Assignee struct {
		ID       int    `json:"id"`
		Name     string `json:"name"`
		Username string `json:"username"`
	} `json:"assignee"`
	Assignees []struct {
		ID       int    `json:"id"`
		Name     string `json:"name"`
		Username string `json:"username"`
	} `json:"assignees"`
	Reviewers []struct {
		ID       int    `json:"id"`
		Name     string `json:"name"`
		Username string `json:"username"`
	} `json:"reviewers"`
	Labels []string `json:"labels"`
}

type Discussion struct {
	ID    string `json:"id"`
	Notes []Note `json:"notes"`
}

type Note struct {
	ID        int    `json:"id"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	System    bool   `json:"system"`
	Author    struct {
		ID       int    `json:"id"`
		Name     string `json:"name"`
		Username string `json:"username"`
	} `json:"author"`
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
		client:  &http.Client{Timeout: 10 * time.Second}, // Reduced from 30s to 10s
	}
}

func (c *Client) makeRequest(endpoint string) ([]byte, error) {
	return c.makeRequestWithContext(context.Background(), endpoint)
}

func (c *Client) makeRequestWithContext(ctx context.Context, endpoint string) ([]byte, error) {
	url := fmt.Sprintf("%s/api/v4%s", c.BaseURL, endpoint)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
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

func (c *Client) GetIssueDiscussions(projectPath string, issueIID int) ([]Discussion, error) {
	return c.GetIssueDiscussionsWithContext(context.Background(), projectPath, issueIID)
}

func (c *Client) GetIssueDiscussionsWithContext(ctx context.Context, projectPath string, issueIID int) ([]Discussion, error) {
	encodedPath := strings.ReplaceAll(projectPath, "/", "%2F")

	var allDiscussions []Discussion
	page := 1
	perPage := 100 // Get more items per page

	for {
		endpoint := fmt.Sprintf("/projects/%s/issues/%d/discussions?per_page=%d&page=%d", encodedPath, issueIID, perPage, page)

		body, err := c.makeRequestWithContext(ctx, endpoint)
		if err != nil {
			return nil, err
		}

		var discussions []Discussion
		if err := json.Unmarshal(body, &discussions); err != nil {
			return nil, fmt.Errorf("failed to parse discussions: %v", err)
		}

		allDiscussions = append(allDiscussions, discussions...)

		// If we got fewer items than per_page, we've reached the end
		if len(discussions) < perPage {
			break
		}

		page++

		// Safety check to prevent infinite loops (max 50 pages = 5000 discussions)
		if page > 50 {
			break
		}
	}

	return allDiscussions, nil
}

func (c *Client) GetIssueCommentsAfter(projectPath string, issueIID int, afterTime time.Time) ([]Note, error) {
	return c.GetIssueCommentsAfterWithContext(context.Background(), projectPath, issueIID, afterTime)
}

func (c *Client) GetIssueCommentsAfterWithContext(ctx context.Context, projectPath string, issueIID int, afterTime time.Time) ([]Note, error) {
	discussions, err := c.GetIssueDiscussionsWithContext(ctx, projectPath, issueIID)
	if err != nil {
		return nil, err
	}

	var newComments []Note
	for _, discussion := range discussions {
		for _, note := range discussion.Notes {
			// Check for cancellation during processing
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}

			// Skip system notes (like label changes)
			if note.System {
				continue
			}

			// Parse created time
			createdAt, err := time.Parse(time.RFC3339, note.CreatedAt)
			if err != nil {
				continue // Skip if we can't parse the time
			}

			// Only include comments after the specified time
			if createdAt.After(afterTime) {
				newComments = append(newComments, note)
			}
		}
	}

	return newComments, nil
}

func (c *Client) GetLatestCommentTime(projectPath string, issueIID int) (*time.Time, error) {
	discussions, err := c.GetIssueDiscussions(projectPath, issueIID)
	if err != nil {
		return nil, err
	}

	var latestTime *time.Time
	for _, discussion := range discussions {
		for _, note := range discussion.Notes {
			// Skip system notes
			if note.System {
				continue
			}

			// Parse created time
			createdAt, err := time.Parse(time.RFC3339, note.CreatedAt)
			if err != nil {
				continue
			}

			if latestTime == nil || createdAt.After(*latestTime) {
				latestTime = &createdAt
			}
		}
	}

	return latestTime, nil
}

func (c *Client) CreateIssueNote(projectPath string, issueIID int, body string) (*Note, error) {
	encodedPath := strings.ReplaceAll(projectPath, "/", "%2F")
	url := fmt.Sprintf("%s/api/v4/projects/%s/issues/%d/notes", c.BaseURL, encodedPath, issueIID)

	payload := map[string]string{
		"body": body,
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %v", err)
	}

	req, err := http.NewRequest("POST", url, strings.NewReader(string(jsonPayload)))
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

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("failed to create note: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	var note Note
	if err := json.Unmarshal(respBody, &note); err != nil {
		return nil, fmt.Errorf("failed to parse note response: %v", err)
	}

	return &note, nil
}

func (c *Client) GetAssignedMergeRequests(username string, state string) ([]MergeRequest, error) {
	// Try with scope=all to get MRs from all accessible projects
	endpoint := fmt.Sprintf("/merge_requests?assignee_username=%s&scope=all&per_page=100", username)

	if state != "" {
		endpoint += "&state=" + state
	}

	body, err := c.makeRequest(endpoint)
	if err != nil {
		return nil, err
	}

	var mergeRequests []MergeRequest
	if err := json.Unmarshal(body, &mergeRequests); err != nil {
		return nil, fmt.Errorf("failed to parse merge requests: %v", err)
	}

	return mergeRequests, nil
}

func (c *Client) GetMergeRequestsForReview(username string, state string) ([]MergeRequest, error) {
	// Try with scope=all to get MRs from all accessible projects
	endpoint := fmt.Sprintf("/merge_requests?reviewer_username=%s&scope=all&per_page=100", username)

	if state != "" {
		endpoint += "&state=" + state
	}

	body, err := c.makeRequest(endpoint)
	if err != nil {
		return nil, err
	}

	var mergeRequests []MergeRequest
	if err := json.Unmarshal(body, &mergeRequests); err != nil {
		return nil, fmt.Errorf("failed to parse merge requests: %v", err)
	}

	return mergeRequests, nil
}

func (c *Client) GetProjectMergeRequests(projectPath string, state string) ([]MergeRequest, error) {
	encodedPath := strings.ReplaceAll(projectPath, "/", "%2F")
	endpoint := fmt.Sprintf("/projects/%s/merge_requests?per_page=100", encodedPath)

	if state != "" {
		endpoint += "&state=" + state
	}

	body, err := c.makeRequest(endpoint)
	if err != nil {
		return nil, err
	}

	var mergeRequests []MergeRequest
	if err := json.Unmarshal(body, &mergeRequests); err != nil {
		return nil, fmt.Errorf("failed to parse merge requests: %v", err)
	}

	return mergeRequests, nil
}

func (c *Client) GetMergeRequest(projectPath string, mergeRequestIID int) (*MergeRequest, error) {
	encodedPath := strings.ReplaceAll(projectPath, "/", "%2F")
	endpoint := fmt.Sprintf("/projects/%s/merge_requests/%d", encodedPath, mergeRequestIID)

	body, err := c.makeRequest(endpoint)
	if err != nil {
		return nil, err
	}

	var mergeRequest MergeRequest
	if err := json.Unmarshal(body, &mergeRequest); err != nil {
		return nil, fmt.Errorf("failed to parse merge request: %v", err)
	}

	return &mergeRequest, nil
}

func (c *Client) GetMergeRequestDiscussions(projectPath string, mergeRequestIID int) ([]Discussion, error) {
	return c.GetMergeRequestDiscussionsWithContext(context.Background(), projectPath, mergeRequestIID)
}

func (c *Client) GetMergeRequestDiscussionsWithContext(ctx context.Context, projectPath string, mergeRequestIID int) ([]Discussion, error) {
	encodedPath := strings.ReplaceAll(projectPath, "/", "%2F")

	var allDiscussions []Discussion
	page := 1
	perPage := 100

	for {
		endpoint := fmt.Sprintf("/projects/%s/merge_requests/%d/discussions?per_page=%d&page=%d", encodedPath, mergeRequestIID, perPage, page)

		body, err := c.makeRequestWithContext(ctx, endpoint)
		if err != nil {
			return nil, err
		}

		var discussions []Discussion
		if err := json.Unmarshal(body, &discussions); err != nil {
			return nil, fmt.Errorf("failed to parse discussions: %v", err)
		}

		allDiscussions = append(allDiscussions, discussions...)

		if len(discussions) < perPage {
			break
		}

		page++

		if page > 50 {
			break
		}
	}

	return allDiscussions, nil
}

func (c *Client) CreateMergeRequestNote(projectPath string, mergeRequestIID int, body string) (*Note, error) {
	encodedPath := strings.ReplaceAll(projectPath, "/", "%2F")
	url := fmt.Sprintf("%s/api/v4/projects/%s/merge_requests/%d/notes", c.BaseURL, encodedPath, mergeRequestIID)

	payload := map[string]string{
		"body": body,
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %v", err)
	}

	req, err := http.NewRequest("POST", url, strings.NewReader(string(jsonPayload)))
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

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("failed to create note: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	var note Note
	if err := json.Unmarshal(respBody, &note); err != nil {
		return nil, fmt.Errorf("failed to parse note response: %v", err)
	}

	return &note, nil
}

// User represents a GitLab user
type User struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	State    string `json:"state"`
	WebURL   string `json:"web_url"`
}

// GetCurrentUser returns information about the authenticated user
func (c *Client) GetCurrentUser() (*User, error) {
	respBody, err := c.makeRequest("/user")
	if err != nil {
		return nil, fmt.Errorf("failed to get current user: %v", err)
	}

	var user User
	if err := json.Unmarshal(respBody, &user); err != nil {
		return nil, fmt.Errorf("failed to parse user response: %v", err)
	}

	return &user, nil
}

// GetAssignedMergeRequestsByID fetches MRs by user ID instead of username
func (c *Client) GetAssignedMergeRequestsByID(userID int, state string) ([]MergeRequest, error) {
	endpoint := fmt.Sprintf("/merge_requests?assignee_id=%d&scope=all&per_page=100", userID)

	if state != "" {
		endpoint += "&state=" + state
	}

	body, err := c.makeRequest(endpoint)
	if err != nil {
		return nil, err
	}

	var mergeRequests []MergeRequest
	if err := json.Unmarshal(body, &mergeRequests); err != nil {
		return nil, fmt.Errorf("failed to parse merge requests: %v", err)
	}

	return mergeRequests, nil
}

// UpdateMergeRequestLabels updates the labels on a merge request
func (c *Client) UpdateMergeRequestLabels(projectID int, mergeRequestIID int, labels []string) error {
	// Ensure we're using the API endpoint
	baseURL := c.BaseURL
	if !strings.HasSuffix(baseURL, "/api/v4") {
		baseURL = strings.TrimRight(baseURL, "/") + "/api/v4"
	}
	
	endpoint := fmt.Sprintf("/projects/%d/merge_requests/%d", projectID, mergeRequestIID)
	fullURL := baseURL + endpoint

	// Create the request body with proper JSON format
	labelsStr := strings.Join(labels, ",")
	body := fmt.Sprintf(`{"labels": "%s"}`, labelsStr)

	req, err := http.NewRequest("PUT", fullURL, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %v", err)
	}

	// Use Private-Token header instead of Bearer for GitLab API
	req.Header.Set("Private-Token", c.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to update MR labels: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	return nil
}


// GetProjectByID gets project information by project ID
func (c *Client) GetProjectByID(projectID int) (*Project, error) {
	endpoint := fmt.Sprintf("/projects/%d", projectID)

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
