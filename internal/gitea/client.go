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
	AheadBy      int
	BehindBy     int
	EmptyBehindDiff bool // true when BehindBy>0 but target introduces no file changes vs source
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

	behind, behindFiles, err := c.totalCommitsAndFiles(owner, repo, source, target)
	if err != nil {
		return nil, err
	}
	if behind < 0 {
		return nil, nil
	}

	emptyBehindDiff := behind > 0 && behindFiles == 0
	return &CompareResult{AheadBy: ahead, BehindBy: behind, EmptyBehindDiff: emptyBehindDiff}, nil
}

// totalCommits calls compare/{base}...{head} and returns total_commits.
// Returns -1 if the endpoint returns 404 (branch missing).
func (c *Client) totalCommits(owner, repo, base, head string) (int, error) {
	n, _, err := c.totalCommitsAndFiles(owner, repo, base, head)
	return n, err
}

// totalCommitsAndFiles calls compare/{base}...{head} and returns (total_commits, file_count).
// Returns (-1, 0, nil) if the endpoint returns 404 (branch missing).
func (c *Client) totalCommitsAndFiles(owner, repo, base, head string) (int, int, error) {
	endpoint := fmt.Sprintf("%s/api/v1/repos/%s/%s/compare/%s...%s",
		c.baseURL, owner, repo, base, head)

	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("token %s", c.token))

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("api call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return -1, 0, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, 0, fmt.Errorf("api error: %d %s", resp.StatusCode, string(body))
	}

	var result struct {
		TotalCommits int              `json:"total_commits"`
		Files        []map[string]any `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, 0, fmt.Errorf("decode response: %w", err)
	}

	return result.TotalCommits, len(result.Files), nil
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
	ID        int      `json:"id"`
	Number    int      `json:"number"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	State     string   `json:"state"`
	HTMLURL   string   `json:"html_url"`
	CreatedAt string   `json:"created_at"`
	Head      PRBranch `json:"head"`
	Base      PRBranch `json:"base"`
	User      PRUser   `json:"user"`
}

type PRBranch struct {
	Label string `json:"label"`
	Ref   string `json:"ref"`
	SHA   string `json:"sha"`
}

type PRUser struct {
	Login string `json:"login"`
}

// PRFile is one entry from the /pulls/{index}/files API response.
type PRFile struct {
	Filename  string `json:"filename"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Changes   int    `json:"changes"`
}

// PRStats holds summary metrics for a pull request diff.
type PRStats struct {
	Commits   int
	Files     int
	Additions int
	Deletions int
	SentBytes int // total bytes sent to the AI (diff + full file content)
}

// GetPullRequest fetches a single pull request by number.
func (c *Client) GetPullRequest(owner, repo string, number int) (*PullRequest, error) {
	endpoint := fmt.Sprintf("%s/api/v1/repos/%s/%s/pulls/%d", c.baseURL, owner, repo, number)

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
		return nil, fmt.Errorf("PR #%d not found in %s/%s", number, owner, repo)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("api error: %d %s", resp.StatusCode, string(body))
	}

	var pr PullRequest
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &pr, nil
}

const maxDiffBytes = 150_000

// GetPullRequestDiff returns a unified diff for the PR using the true merge base
// (parent of the oldest PR commit) rather than the base branch tip. This ensures
// only the commits belonging to this PR are reflected in the diff.
func (c *Client) GetPullRequestDiff(owner, repo string, number int) (string, PRStats, error) {
	pr, err := c.GetPullRequest(owner, repo, number)
	if err != nil {
		return "", PRStats{}, err
	}

	mergeBase, commitCount, err := c.prMergeBase(owner, repo, number)
	if err != nil {
		// Fall back to base branch SHA if merge base lookup fails
		mergeBase = pr.Base.SHA
	}

	files, err := c.listPRFiles(owner, repo, number)
	if err != nil {
		return "", PRStats{}, err
	}

	stats := PRStats{
		Commits: commitCount,
		Files:   len(files),
	}
	for _, f := range files {
		stats.Additions += f.Additions
		stats.Deletions += f.Deletions
	}

	var sb strings.Builder
	totalBytes := 0

	for _, f := range files {
		if totalBytes >= maxDiffBytes {
			sb.WriteString("\n[remaining files omitted — 150 KB budget reached]\n")
			break
		}

		var baseContent, headContent string

		if f.Status != "added" {
			baseContent, _ = c.rawFileContent(owner, repo, mergeBase, f.Filename)
		}
		if f.Status != "deleted" {
			headContent, _ = c.rawFileContent(owner, repo, pr.Head.SHA, f.Filename)
		}

		fileDiff := buildUnifiedDiff(f.Filename, baseContent, headContent, 3)
		if fileDiff == "" && headContent == "" {
			continue
		}

		section := fileDiff
		remaining := maxDiffBytes - totalBytes - len(fileDiff)
		if headContent != "" {
			if len(headContent) <= remaining {
				section += fmt.Sprintf("\n// full file: %s\n%s\n", f.Filename, headContent)
			} else {
				section += fmt.Sprintf("\n// full file: %s [omitted — %d bytes exceeds remaining budget]\n", f.Filename, len(headContent))
			}
		}

		sb.WriteString(section)
		totalBytes += len(section)
	}

	stats.SentBytes = totalBytes
	return sb.String(), stats, nil
}

// prMergeBase returns the SHA of the common ancestor (merge base) for the PR
// and the number of commits in the PR.
// It fetches the PR's commit list (newest-first) and returns the parent of the oldest commit.
func (c *Client) prMergeBase(owner, repo string, number int) (sha string, commitCount int, err error) {
	endpoint := fmt.Sprintf("%s/api/v1/repos/%s/%s/pulls/%d/commits", c.baseURL, owner, repo, number)
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("token %s", c.token))

	resp, err := c.client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("commits: %d", resp.StatusCode)
	}

	var commits []struct {
		SHA     string `json:"sha"`
		Parents []struct {
			SHA string `json:"sha"`
		} `json:"parents"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&commits); err != nil {
		return "", 0, err
	}
	if len(commits) == 0 {
		return "", 0, fmt.Errorf("no commits in PR")
	}

	// Commits are newest-first; oldest is last.
	oldest := commits[len(commits)-1]
	if len(oldest.Parents) == 0 {
		return "", len(commits), fmt.Errorf("oldest commit has no parent")
	}
	return oldest.Parents[0].SHA, len(commits), nil
}

// listPRFiles returns changed files for a PR via the Gitea API.
func (c *Client) listPRFiles(owner, repo string, number int) ([]PRFile, error) {
	endpoint := fmt.Sprintf("%s/api/v1/repos/%s/%s/pulls/%d/files", c.baseURL, owner, repo, number)
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

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("files error: %d %s", resp.StatusCode, string(body))
	}

	var files []PRFile
	if err := json.NewDecoder(resp.Body).Decode(&files); err != nil {
		return nil, fmt.Errorf("decode files: %w", err)
	}
	return files, nil
}

// rawFileContent fetches raw file content at a specific commit SHA.
// Uses ?token query param since Gitea web raw endpoints don't accept Authorization headers.
func (c *Client) rawFileContent(owner, repo, sha, filepath string) (string, error) {
	rawURL := fmt.Sprintf("%s/%s/%s/raw/commit/%s/%s?token=%s",
		c.baseURL, owner, repo, sha, filepath, c.token)

	resp, err := c.client.Get(rawURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("raw fetch %d", resp.StatusCode)
	}

	const fileLimit = 200_000
	data, err := io.ReadAll(io.LimitReader(resp.Body, fileLimit+1))
	if err != nil {
		return "", err
	}
	if len(data) > fileLimit {
		return string(data[:fileLimit]) + "\n// [file truncated at 200 KB — remainder omitted]\n", nil
	}
	return string(data), nil
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
