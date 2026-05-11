package main

import (
	"os/exec"
	"strings"
)

// GetDefaultBranch returns the default branch of a repo
func GetDefaultBranch(repoPath string) (string, error) {
	// Try to get symbolic ref for origin/HEAD
	cmd := exec.Command("git", "-C", repoPath, "symbolic-ref", "refs/remotes/origin/HEAD")
	output, err := cmd.Output()
	if err == nil {
		// Output is "refs/remotes/origin/branch_name"
		ref := strings.TrimSpace(string(output))
		branch := strings.TrimPrefix(ref, "refs/remotes/origin/")
		if branch != "" {
			return branch, nil
		}
	}

	// Fallback to main or master
	branches := []string{"main", "master", "develop"}
	for _, b := range branches {
		cmd := exec.Command("git", "-C", repoPath, "rev-parse", "--verify", "refs/remotes/origin/"+b)
		if err := cmd.Run(); err == nil {
			return b, nil
		}
	}

	return "main", nil
}
