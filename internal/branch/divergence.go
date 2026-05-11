package branch

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type Divergence struct {
	SourceBranch     string
	TargetBranch     string
	AheadCount       int
	BehindCount      int
	IsContentSynced bool // full tree identical, history diverged
	IsSquashMerged  bool // source's changed files are in target, but target moved on
	LastCommitDate   time.Time
	LastCommitAuthor string
}

// CalculateDivergence computes commits ahead/behind between two branches
func CalculateDivergence(repoPath, source, target string) (*Divergence, error) {
	if !ValidateBranch(source) || !ValidateBranch(target) {
		return nil, fmt.Errorf("invalid branch name")
	}

	ahead, err := getAheadCount(repoPath, source, target)
	if err != nil {
		return nil, err
	}

	behind, err := getBehindCount(repoPath, source, target)
	if err != nil {
		return nil, err
	}

	// Get target branch's last commit info
	lastDate, lastAuthor, err := getLastCommitInfo(repoPath, target)
	if err != nil {
		// Non-fatal, continue with empty values
		lastDate = time.Time{}
		lastAuthor = ""
	}

	// Squash-merge detection: source is ahead but its changes are already in target.
	// Works even when target has moved forward (behind > 0).
	contentSynced := false
	squashMerged := false
	if ahead > 0 {
		contentSynced = isContentIdentical(repoPath, source, target)
		if !contentSynced {
			squashMerged = isSquashMerged(repoPath, source, target)
		}
	}

	return &Divergence{
		SourceBranch:    source,
		TargetBranch:    target,
		AheadCount:      ahead,
		BehindCount:     behind,
		IsContentSynced: contentSynced,
		IsSquashMerged:  squashMerged,
		LastCommitDate:   lastDate,
		LastCommitAuthor: lastAuthor,
	}, nil
}

// isContentIdentical returns true when source and target have exactly the same file tree.
func isContentIdentical(repoPath, source, target string) bool {
	return exec.Command("git", "-C", repoPath, "diff", "--quiet", source, target).Run() == nil
}

// isSquashMerged returns true when a squash commit exists in target whose file trees
// match source's trees for the files source changed. Reliable even when target has
// since modified those same files further. Scans up to maxSquashScan commits in
// MB..target to avoid being slow on branches with massive behind counts.
const maxSquashScan = 100

func isSquashMerged(repoPath, source, target string) bool {
	mbOut, err := exec.Command("git", "-C", repoPath, "merge-base", source, target).Output()
	if err != nil {
		return false
	}
	mergeBase := strings.TrimSpace(string(mbOut))

	// Files changed by TARGET (the feature branch) vs merge base.
	filesOut, err := exec.Command("git", "-C", repoPath, "diff", "--name-only", mergeBase, target).Output()
	if err != nil {
		return false
	}
	changedFiles := strings.Fields(string(filesOut))
	if len(changedFiles) == 0 {
		return false
	}

	// Tree hashes of those files in TARGET.
	targetHashes := lsTreeHashes(repoPath, target, changedFiles)
	if len(targetHashes) == 0 {
		return false
	}

	// Scan commits in MB..SOURCE (base branch) looking for a squash commit
	// whose tree matches target's trees for those files.
	commitsOut, err := exec.Command("git", "-C", repoPath, "log", "--format=%H", mergeBase+".."+source).Output()
	if err != nil {
		return false
	}

	for i, commit := range strings.Fields(string(commitsOut)) {
		if i >= maxSquashScan {
			break
		}
		if hashesMatch(targetHashes, lsTreeHashes(repoPath, commit, changedFiles)) {
			return true
		}
	}
	return false
}

// lsTreeHashes returns a map of filename→blob-hash for the given files at ref.
// Files absent from the ref (deleted) are omitted from the map.
func lsTreeHashes(repoPath, ref string, files []string) map[string]string {
	args := append([]string{"-C", repoPath, "ls-tree", ref, "--"}, files...)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil
	}
	result := make(map[string]string)
	for _, line := range strings.Split(string(out), "\n") {
		// format: "100644 blob <hash>\t<filename>"  or space-separated
		parts := strings.Fields(line)
		if len(parts) >= 4 {
			result[parts[3]] = parts[2]
		}
	}
	return result
}

// hashesMatch returns true when both maps have the same keys and values.
func hashesMatch(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func getAheadCount(repoPath, source, target string) (int, error) {
	// Commits in target but not in source: source..target
	cmd := exec.Command("git", "-C", repoPath, "rev-list", "--count", source+".."+target)
	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("git rev-list ahead: %w", err)
	}

	count, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		return 0, fmt.Errorf("parse ahead count: %w", err)
	}

	return count, nil
}

func getBehindCount(repoPath, source, target string) (int, error) {
	// Commits in source but not in target: target..source
	cmd := exec.Command("git", "-C", repoPath, "rev-list", "--count", target+".."+source)
	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("git rev-list behind: %w", err)
	}

	count, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		return 0, fmt.Errorf("parse behind count: %w", err)
	}

	return count, nil
}

func getLastCommitInfo(repoPath, branch string) (time.Time, string, error) {
	// Get last commit timestamp and author
	cmd := exec.Command("git", "-C", repoPath, "log", "-1", "--format=%ci|%an", branch)
	output, err := cmd.Output()
	if err != nil {
		return time.Time{}, "", fmt.Errorf("git log: %w", err)
	}

	parts := strings.Split(strings.TrimSpace(string(output)), "|")
	if len(parts) < 2 {
		return time.Time{}, "", fmt.Errorf("unexpected format")
	}

	// Parse ISO 8601 timestamp (e.g., "2026-04-13 10:49:15 +0000")
	layout := "2006-01-02 15:04:05 -0700"
	t, err := time.Parse(layout, parts[0])
	if err != nil {
		return time.Time{}, "", fmt.Errorf("parse timestamp: %w", err)
	}

	return t, parts[1], nil
}

// ValidateBranch checks if branch name is safe
func ValidateBranch(branch string) bool {
	for _, c := range branch {
		if !((c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '.' || c == '_' || c == '-' || c == '/') {
			return false
		}
	}
	return len(branch) > 0
}
