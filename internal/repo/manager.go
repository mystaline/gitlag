// Package repo manages a local cache of cloned repositories.
package repo

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mystaline/gitlag/internal/gitea"
)

var validBranchRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/-]*$`)

// isValidBranchName checks that a branch name is safe to pass to git commands.
func isValidBranchName(name string) bool {
	return validBranchRe.MatchString(name)
}

// Manager clones and updates repos into a local cache directory.
type Manager struct {
	cacheDir string
	env      []string
}

// NewManager creates a Manager that stores repos under cacheDir/org.
func NewManager(cacheDir, org string) *Manager {
	return &Manager{
		cacheDir: filepath.Join(cacheDir, org),
		env:      append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_SSH_COMMAND=ssh -o BatchMode=yes"),
	}
}

func (m *Manager) runGit(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Env = m.env
	return cmd.Run()
}

// SyncRepos clones any repo not yet cached and pulls updates for existing ones.
// When noFetch is true, already-cloned repos are left as-is.
func (m *Manager) SyncRepos(repos []gitea.Repository, noFetch bool) error {
	if err := os.MkdirAll(m.cacheDir, 0o755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}
	for _, r := range repos {
		if err := m.syncRepo(r, noFetch); err != nil {
			return fmt.Errorf("sync %s: %w", r.Name, err)
		}
	}
	return nil
}

func (m *Manager) syncRepo(r gitea.Repository, noFetch bool) error {
	dest := m.GetRepoPath(r.Name)

	// Clone if the repo doesn't exist locally yet.
	if _, err := os.Stat(filepath.Join(dest, ".git")); os.IsNotExist(err) {
		cmd := exec.Command("git", "clone", "--depth=1", "--quiet", r.CloneURL, dest)
		cmd.Env = m.env
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("git clone: %w", err)
		}
		return nil
	}

	if noFetch {
		return nil
	}

	// Fetch and reset the currently checked-out branch, not the default branch.
	// The cache dir is shared with local development — a hard reset to
	// origin/main while another branch is checked out causes divergence.
	currentBranch := getCurrentBranch(dest)
	if currentBranch != "HEAD" {
		fetch := exec.Command("git", "-C", dest, "fetch", "--depth=1", "--quiet", "origin", currentBranch)
		fetch.Env = m.env
		fetch.Stdout = nil
		fetch.Stderr = nil
		_ = fetch.Run() // best-effort

		reset := exec.Command("git", "-C", dest, "reset", "--hard", "--quiet", "origin/"+currentBranch)
		reset.Env = m.env
		reset.Stdout = nil
		reset.Stderr = nil
		_ = reset.Run() // best-effort
	}

	// Also fetch all other branches (for gitlag branch divergence analysis)
	fetchAll := exec.Command("git", "-C", dest, "fetch", "--depth=1", "--quiet", "origin", "refs/heads/*:refs/remotes/origin/*")
	fetchAll.Env = m.env
	fetchAll.Stdout = nil
	fetchAll.Stderr = nil
	_ = fetchAll.Run() // Best effort - some branches may not exist

	return nil
}

// SyncBranch fetches and checks out a specific branch for a repo.
// If the branch doesn't exist remotely, returns false with no error.
func (m *Manager) SyncBranch(repoName, cloneURL, branch string) (bool, error) {
	if !isValidBranchName(branch) {
		return false, fmt.Errorf("invalid branch name: %s", branch)
	}

	dest := m.GetRepoPath(repoName)

	// Clone if the repo doesn't exist locally yet.
	if _, err := os.Stat(filepath.Join(dest, ".git")); os.IsNotExist(err) {
		cmd := exec.Command("git", "clone", "--depth=1", "--quiet", "--branch", branch, "--", cloneURL, dest)
		cmd.Env = m.env
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Run(); err != nil {
			return false, fmt.Errorf("git clone %s@%s failed", repoName, branch)
		}
		return true, nil
	}

	// Fetch the specific branch.
	fetch := exec.Command("git", "-C", dest, "fetch", "--depth=1", "--quiet", "origin", "--", branch)
	fetch.Env = m.env
	fetch.Stdout = nil
	fetch.Stderr = nil
	if err := fetch.Run(); err != nil {
		return false, nil
	}

	// Checkout using FETCH_HEAD
	checkout := exec.Command("git", "-C", dest, "checkout", "--quiet", "-B", branch, "FETCH_HEAD")
	checkout.Env = m.env
	checkout.Stdout = nil
	checkout.Stderr = nil
	if err := checkout.Run(); err != nil {
		return false, fmt.Errorf("checkout %s failed", branch)
	}

	return true, nil
}

// GetOrgPath returns the local path for the organization directory.
func (m *Manager) GetOrgPath() string {
	return m.cacheDir
}

// ListLocalRepos lists all directories in the org cache as repositories.
func (m *Manager) ListLocalRepos() ([]gitea.Repository, error) {
	orgPath := m.GetOrgPath()
	entries, err := os.ReadDir(orgPath)
	if err != nil {
		return nil, err
	}

	var repos []gitea.Repository
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			repos = append(repos, gitea.Repository{
				Name: entry.Name(),
			})
		}
	}
	return repos, nil
}

// GetRepoPath returns the local path for a given repo name.
func (m *Manager) GetRepoPath(name string) string {
	return filepath.Join(m.cacheDir, name)
}

// CheckoutCommit checks out a specific commit or ref in the cached repo.
// It fetches the ref first (in case the shallow clone doesn't have it),
// then does a detached HEAD checkout.
func (m *Manager) CheckoutCommit(repoName, ref string) error {
	dest := m.GetRepoPath(repoName)
	if _, err := os.Stat(filepath.Join(dest, ".git")); os.IsNotExist(err) {
		return fmt.Errorf("repo %s not cloned yet", repoName)
	}

	// Attempt to fetch the ref.
	fetch := exec.Command("git", "-C", dest, "fetch", "--quiet", "origin", ref)
	fetch.Env = m.env
	fetch.Stdout = nil
	fetch.Stderr = nil
	_ = fetch.Run() // best-effort

	// Try checkout — works for branches, tags, and commit hashes.
	checkout := exec.Command("git", "-C", dest, "checkout", "--quiet", "--detach", ref)
	checkout.Env = m.env
	checkout.Stdout = nil
	checkout.Stderr = nil
	if err := checkout.Run(); err != nil {
		// If ref is a branch name, try FETCH_HEAD instead.
		checkout2 := exec.Command("git", "-C", dest, "checkout", "--quiet", "--detach", "FETCH_HEAD")
		checkout2.Env = m.env
		checkout2.Stdout = nil
		checkout2.Stderr = nil
		_ = checkout2.Run()
	}

	return nil
}

// CloneAndUnshallowTemporarily clones a repo directly from Gitea to temp,
// unshallows it for analysis, then cleans up after use.
// Returns (tempPath, cleanupFunc, error)
func (m *Manager) CloneAndUnshallowTemporarily(repoName, cloneURL string) (string, func() error, error) {
	// Create temp directory
	tempDir := filepath.Join(os.TempDir(), "gitlag-"+repoName+"-"+randomSuffix())
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return "", nil, fmt.Errorf("create temp dir: %w", err)
	}

	// Clone from Gitea directly (shallow)
	cmd := exec.Command("git", "clone", "--depth=1", "--quiet", cloneURL, tempDir)
	cmd.Env = m.env
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		os.RemoveAll(tempDir)
		return "", nil, fmt.Errorf("clone from Gitea: %w", err)
	}

	// Fetch all branches (clone only gets default branch)
	fetchAll := exec.Command("git", "-C", tempDir, "fetch", "--quiet", "origin", "refs/heads/*:refs/remotes/origin/*")
	fetchAll.Env = m.env
	fetchAll.Stdout = nil
	fetchAll.Stderr = nil
	_ = fetchAll.Run() // Best effort - all branches may not exist

	// Unshallow to get full history for accurate divergence calculation
	unshallow := exec.Command("git", "-C", tempDir, "fetch", "--unshallow", "--quiet", "origin")
	unshallow.Env = m.env
	unshallow.Stdout = nil
	unshallow.Stderr = nil
	if err := unshallow.Run(); err != nil {
		os.RemoveAll(tempDir)
		return "", nil, fmt.Errorf("unshallow: %w", err)
	}

	// Cleanup function
	cleanup := func() error {
		return os.RemoveAll(tempDir)
	}

	return tempDir, cleanup, nil
}

// PrepareForAnalysis ensures a cached repo is ready for divergence calculation.
// It fetches all remote branches and unshallows if needed.
func (m *Manager) PrepareForAnalysis(repoName string, cloneURL string) (string, error) {
	dest := m.GetRepoPath(repoName)

	// If it doesn't exist, clone it (shallow first is fine)
	if _, err := os.Stat(filepath.Join(dest, ".git")); os.IsNotExist(err) {
		cmd := exec.Command("git", "clone", "--depth=1", "--quiet", cloneURL, dest)
		cmd.Env = m.env
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("initial clone: %w", err)
		}
	}

	// Fetch all branches to remote refs
	cmdFetch := exec.Command("git", "-C", dest, "fetch", "--quiet", "origin", "refs/heads/*:refs/remotes/origin/*")
	cmdFetch.Env = m.env
	_ = cmdFetch.Run()

	// Check if shallow
	if _, err := os.Stat(filepath.Join(dest, ".git", "shallow")); err == nil {
		// Instead of full unshallow (which can be very slow), we fetch a reasonable depth
		// for divergence analysis. 500 commits is usually enough for bug-fix tracking.
		deepen := exec.Command("git", "-C", dest, "fetch", "--depth=500", "--quiet", "--", "origin")
		deepen.Env = m.env
		if err := deepen.Run(); err != nil {
			// If deepening fails, try a smaller depth
			fetchMore := exec.Command("git", "-C", dest, "fetch", "--depth=100", "--quiet", "origin")
			fetchMore.Env = m.env
			_ = fetchMore.Run()
		}
	}

	return dest, nil
}

// PrepareForBranches ensures specific branches are ready for analysis.
func (m *Manager) PrepareForBranches(repoName string, cloneURL string, branches []string) (string, error) {
	dest := m.GetRepoPath(repoName)

	// If it doesn't exist, clone it (shallower is better)
	if _, err := os.Stat(filepath.Join(dest, ".git")); os.IsNotExist(err) {
		// Use --no-checkout to avoid heavy local processing
		cmd := exec.Command("git", "clone", "--depth=1", "--quiet", "--no-checkout", cloneURL, dest)
		cmd.Env = m.env
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("initial clone: %w", err)
		}
	}

	// Fetch only required branches
	for _, b := range branches {
		ref := strings.TrimPrefix(b, "origin/")

		// Fetch with depth to ensure common ancestor is likely present
		// Use --force to ensure we update local remote refs even if they diverged
		cmdFetch := exec.Command("git", "-C", dest, "fetch", "--depth=500", "--quiet", "--force", "origin", ref+":refs/remotes/origin/"+ref)
		cmdFetch.Env = m.env
		_ = cmdFetch.Run()
	}

	// Verify we have a common ancestor for the two main branches if provided
	if len(branches) >= 2 {
		b1 := branches[0]
		b2 := branches[1]
		if !strings.HasPrefix(b1, "origin/") { b1 = "origin/" + b1 }
		if !strings.HasPrefix(b2, "origin/") { b2 = "origin/" + b2 }

		// Check for merge base
		cmdMB := exec.Command("git", "-C", dest, "merge-base", b1, b2)
		cmdMB.Env = m.env
		if err := cmdMB.Run(); err != nil {
			// No merge base? Try to deepen significantly
			cmdDeepen := exec.Command("git", "-C", dest, "fetch", "--depth=1000", "--quiet", "origin")
			cmdDeepen.Env = m.env
			_ = cmdDeepen.Run()
		}
	}

	return dest, nil
}

// getCurrentBranch returns the currently checked-out branch, or "HEAD" if detached.
func getCurrentBranch(dest string) string {
	cmd := exec.Command("git", "-C", dest, "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		return "HEAD"
	}
	return strings.TrimSpace(string(out))
}

// randomSuffix generates a random suffix for temp directories
func randomSuffix() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	suffix := make([]byte, 8)
	for i := range suffix {
		suffix[i] = chars[i%len(chars)]
	}
	return string(suffix)
}

// isBranchNotFound checks if git output indicates a missing remote ref.
func isBranchNotFound(out []byte) bool {
	s := strings.ToLower(string(out))
	return strings.Contains(s, "couldn't find remote ref") ||
		strings.Contains(s, "remote branch") && strings.Contains(s, "not found") ||
		strings.Contains(s, "remote ref does not exist")
}

// firstLine returns the first non-empty line from git output for clean error messages.
func firstLine(out []byte) string {
	s := strings.TrimSpace(string(out))
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx]
	}
	return s
}
