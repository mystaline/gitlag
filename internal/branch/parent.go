package branch

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/mystaline/gitlag/internal/config"
)

// DetectParent detects the parent branch using the Option C chain
// Priority:
// 1. Git upstream tracking (git rev-parse --abbrev-ref branch@{upstream})
// 2. Config branch_parents glob mapping
// 3. Branch naming convention (feature/* -> dev, fix/* -> staging, etc.)
// 4. Default to "main"
// Note: Works with both "dev" and "origin/dev" formats
func DetectParent(repoPath, branch string, cfg *config.Config) string {
	// Normalize branch name (remove origin/ prefix for matching)
	normalizedBranch := strings.TrimPrefix(branch, "origin/")

	// Step 1: Try git upstream
	if parent, ok := getGitUpstream(repoPath, normalizedBranch); ok {
		return parent
	}

	// Step 2: Try config mapping
	if parent, ok := matchConfigMapping(normalizedBranch, cfg.BranchParents); ok {
		return parent
	}

	// Step 3: Try branch naming convention
	if parent, ok := matchBranchConvention(normalizedBranch); ok {
		return parent
	}

	// Step 4: Default
	return "main"
}

// getGitUpstream checks if branch has upstream tracking
func getGitUpstream(repoPath, branch string) (string, bool) {
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "--abbrev-ref", branch+"@{upstream}")
	output, err := cmd.Output()
	if err != nil {
		return "", false
	}

	upstream := strings.TrimSpace(string(output))
	// Remove origin/ prefix if present
	upstream = strings.TrimPrefix(upstream, "origin/")

	// If output is same as input (no upstream found), git returns the input
	if upstream != branch && upstream != "" {
		return upstream, true
	}

	return "", false
}

// matchConfigMapping checks branch_parents config for glob matches
func matchConfigMapping(branch string, mapping map[string]string) (string, bool) {
	// Direct match first
	if parent, ok := mapping[branch]; ok {
		return parent, true
	}

	// Glob pattern matching
	for pattern, parent := range mapping {
		if globMatch(pattern, branch) {
			return parent, true
		}
	}

	return "", false
}

// matchBranchConvention detects parent from branch naming convention
// feature/* -> dev
// fix/* -> staging
// hotfix/* -> main
// dev -> staging
// staging -> main
func matchBranchConvention(branch string) (string, bool) {
	// Extract prefix (before /)
	parts := strings.Split(branch, "/")
	prefix := parts[0]

	switch prefix {
	case "feature":
		return "dev", true
	case "fix":
		return "staging", true
	case "hotfix":
		return "main", true
	case "dev":
		// dev, dev1.1, dev1.2 all -> staging
		return "staging", true
	case "staging":
		return "main", true
	}

	return "", false
}

// globMatch simple glob matching for patterns like "feature/*"
func globMatch(pattern, name string) bool {
	if pattern == "*" {
		return true
	}

	// Handle prefix*suffix patterns
	if strings.Contains(pattern, "*") {
		parts := strings.Split(pattern, "*")
		if len(parts) == 2 {
			prefix, suffix := parts[0], parts[1]
			if prefix != "" && !strings.HasPrefix(name, prefix) {
				return false
			}
			if suffix != "" && !strings.HasSuffix(name, suffix) {
				return false
			}
			return true
		}
	}

	return pattern == name
}

// GetAllBranches returns all remote-tracking branches (origin/*)
// This ensures gitlag uses consistent refs for divergence calculation
func GetAllBranches(repoPath string) ([]string, error) {
	// Get remote-tracking branches only (origin/*, excluding origin/HEAD)
	cmd := exec.Command("git", "-C", repoPath, "branch", "-r", "--format=%(refname:short)")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git branch -r: %w", err)
	}

	var branches []string
	seen := make(map[string]bool)

	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		// Skip empty lines and HEAD references
		if line == "" || strings.HasPrefix(line, "HEAD ->") {
			continue
		}
		// Keep origin/* format (e.g., "origin/dev", "origin/main")
		if !seen[line] {
			branches = append(branches, line)
			seen[line] = true
		}
	}

	return branches, nil
}
