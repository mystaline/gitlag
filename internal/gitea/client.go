package gitea

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Repository struct {
	Name          string `json:"name"`
	CloneURL      string `json:"clone_url"`
	DefaultBranch string `json:"default_branch"`
	Empty         bool   `json:"empty"`
}

type Client struct {
	baseURL string
	token   string
	org     string
	client  *http.Client
}

func NewClient(baseURL, token, org string) *Client {
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		token:   token,
		org:     org,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) ListOrgRepos(orgs ...string) ([]Repository, error) {
	org := c.org
	if len(orgs) > 0 {
		org = orgs[0]
	}

	var repos []Repository
	page := 1

	for page <= 100 {
		pageRepos, hasMore, err := c.listPageOrg(org, page)
		if err != nil {
			return nil, err
		}

		repos = append(repos, pageRepos...)

		if !hasMore {
			break
		}
		page++
	}

	return repos, nil
}

func (c *Client) listPageOrg(org string, page int) ([]Repository, bool, error) {
	endpoint := fmt.Sprintf("%s/api/v1/orgs/%s/repos?limit=50&page=%d", c.baseURL, org, page)

	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, false, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("token %s", c.token))

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("api call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, false, fmt.Errorf("api error: %d %s", resp.StatusCode, string(body))
	}

	var repos []Repository
	if err := json.NewDecoder(resp.Body).Decode(&repos); err != nil {
		return nil, false, fmt.Errorf("decode response: %w", err)
	}

	return repos, len(repos) > 0, nil
}

// Deprecated: use ListOrgRepos(org) instead
func (c *Client) listPage(page int) ([]Repository, bool, error) {
	return c.listPageOrg(c.org, page)
}

// MatchFilter checks if repo name matches include/exclude patterns
func MatchFilter(repoName string, include, exclude []string) bool {
	for _, pattern := range exclude {
		if glob(pattern, repoName) {
			return false
		}
	}

	for _, pattern := range include {
		if glob(pattern, repoName) {
			return true
		}
	}

	return false
}

// Simple glob matching: * matches any sequence
func glob(pattern, name string) bool {
	if pattern == "*" {
		return true
	}

	// Handle prefix*suffix patterns
	if strings.Contains(pattern, "*") {
		parts := strings.Split(pattern, "*")
		if !strings.HasPrefix(name, parts[0]) {
			return false
		}
		if len(parts) > 1 && !strings.HasSuffix(name, parts[len(parts)-1]) {
			return false
		}
		return true
	}

	return pattern == name
}

// GetRepoURL returns clone URL for a repo
func (c *Client) GetRepoURL(org, repoName string) string {
	// HTTP clone URL format
	return fmt.Sprintf("%s/%s/%s.git", c.baseURL, org, repoName)
}

// CompareResult holds the outcome of the Gitea compare API.
// AheadBy = commits in source that target does NOT have (source unique).
// BehindBy = commits in target that source does NOT have (target unique).
type CompareResult struct {
	AheadBy  int
	BehindBy int
}

// CompareBranches uses the Gitea compare API to get accurate ahead/behind counts
// without requiring a local clone. source and target are branch names (without origin/ prefix).
// Returns nil, nil if either comparison endpoint returns 404 (branch missing).
func (c *Client) CompareBranches(owner, repo, source, target string) (*CompareResult, error) {
	ahead, err := c.totalCommits(owner, repo, target, source)
	if err != nil {
		return nil, err
	}
	if ahead < 0 {
		return nil, nil
	}

	behind, err := c.totalCommits(owner, repo, source, target)
	if err != nil {
		return nil, err
	}
	if behind < 0 {
		return nil, nil
	}

	return &CompareResult{AheadBy: ahead, BehindBy: behind}, nil
}

// totalCommits calls compare/{base}...{head} and returns total_commits.
// Returns -1 if the endpoint returns 404 (branch missing).
func (c *Client) totalCommits(owner, repo, base, head string) (int, error) {
	endpoint := fmt.Sprintf("%s/api/v1/repos/%s/%s/compare/%s...%s",
		c.baseURL, owner, repo, base, head)

	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("token %s", c.token))

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("api call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return -1, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("api error: %d %s", resp.StatusCode, string(body))
	}

	var result struct {
		TotalCommits int `json:"total_commits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("decode response: %w", err)
	}

	return result.TotalCommits, nil
}

// BranchInfo holds the latest commit details returned by the Gitea branch API.
type BranchInfo struct {
	Name   string       `json:"name"`
	Commit BranchCommit `json:"commit"`
}

// BranchCommit holds the commit metadata.
type BranchCommit struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
}

// GetBranchInfo fetches branch metadata including the latest commit timestamp.
// Returns nil, nil if the branch does not exist (HTTP 404).
func (c *Client) GetBranchInfo(owner, repo, branch string) (*BranchInfo, error) {
	endpoint := fmt.Sprintf("%s/api/v1/repos/%s/%s/branches/%s",
		c.baseURL, owner, repo, branch)

	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("token %s", c.token))

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("api call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("api error: %d %s", resp.StatusCode, string(body))
	}

	var info BranchInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &info, nil
}

// ListBranches returns all branch names for a repo via the Gitea API.
func (c *Client) ListBranches(owner, repo string) ([]string, error) {
	var all []string
	page := 1
	for page <= 100 {
		endpoint := fmt.Sprintf("%s/api/v1/repos/%s/%s/branches?limit=50&page=%d",
			c.baseURL, owner, repo, page)

		req, err := http.NewRequest("GET", endpoint, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Authorization", fmt.Sprintf("token %s", c.token))

		resp, err := c.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("api call: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("api error: %d %s", resp.StatusCode, string(body))
		}

		var branches []BranchInfo
		if err := json.NewDecoder(resp.Body).Decode(&branches); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("decode response: %w", err)
		}
		resp.Body.Close()

		if len(branches) == 0 {
			break
		}

		for _, b := range branches {
			all = append(all, b.Name)
		}
		page++
	}
	return all, nil
}

// PullRequest holds the subset of Gitea PR fields we need.
type PullRequest struct {
	ID        int       `json:"id"`
	Title     string    `json:"title"`
	State     string    `json:"state"`
	HTMLURL   string    `json:"html_url"`
	CreatedAt string    `json:"created_at"`
	Head      PRBranch  `json:"head"`
	Base      PRBranch  `json:"base"`
	User      PRUser    `json:"user"`
}

type PRBranch struct {
	Label string `json:"label"`
	Ref   string `json:"ref"`
}

type PRUser struct {
	Login string `json:"login"`
}

// ListPullRequests returns open pull requests for a repo (paginated).
func (c *Client) ListPullRequests(owner, repo string) ([]PullRequest, error) {
	var all []PullRequest
	page := 1
	for page <= 100 {
		endpoint := fmt.Sprintf("%s/api/v1/repos/%s/%s/pulls?state=open&limit=50&page=%d",
			c.baseURL, owner, repo, page)

		req, err := http.NewRequest("GET", endpoint, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Authorization", fmt.Sprintf("token %s", c.token))

		resp, err := c.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("api call: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("api error: %d %s", resp.StatusCode, string(body))
		}

		var pulls []PullRequest
		if err := json.NewDecoder(resp.Body).Decode(&pulls); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("decode response: %w", err)
		}
		resp.Body.Close()

		if len(pulls) == 0 {
			break
		}
		all = append(all, pulls...)
		page++
	}
	return all, nil
}
