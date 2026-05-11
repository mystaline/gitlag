package branch

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/mystaline/gitlag/internal/gitea"
)

// CalculateDivergenceFromAPI computes divergence using Gitea compare API for
// accurate ahead/behind counts, then performs a temp clone for ghost detection
// only when ahead > 0. This avoids shallow-clone bugs and eliminates permanent
// cache dependency for the compare command.
func CalculateDivergenceFromAPI(
	client *gitea.Client,
	owner, repo, cloneURL, source, target string,
	compareResult *gitea.CompareResult,
	branchInfo *gitea.BranchInfo,
) (*Divergence, error) {
	if !ValidateBranch(source) || !ValidateBranch(target) {
		return nil, fmt.Errorf("invalid branch name")
	}

	ahead := compareResult.AheadBy
	behind := compareResult.BehindBy

	var lastDate time.Time
	var lastAuthor string
	if branchInfo != nil {
		lastDate, lastAuthor = parseBranchTimestamp(branchInfo.Commit.Timestamp)
	}

	contentSynced := false
	squashMerged := false
	if ahead > 0 {
		contentSynced, squashMerged = DetectGhostViaTempClone(cloneURL, source, target)
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

// DetectGhostViaTempClone clones the repo to a temporary directory, unshallows it,
// and runs content-identical and squash-merge checks. Returns (contentSynced, squashMerged).
// The temp directory is cleaned up after the check.
func DetectGhostViaTempClone(cloneURL, source, target string) (bool, bool) {
	tempDir, err := os.MkdirTemp("", "gitlag-ghost-")
	if err != nil {
		return false, false
	}
	defer os.RemoveAll(tempDir)

	clone := exec.Command("git", "clone", "--depth=1", "--quiet", "--no-checkout", cloneURL, tempDir)
	clone.Stdout = nil
	clone.Stderr = nil
	if err := clone.Run(); err != nil {
		return false, false
	}

	sourceRef := source
	targetRef := target
	if !hasPrefix(sourceRef, "origin/") {
		sourceRef = "origin/" + sourceRef
	}
	if !hasPrefix(targetRef, "origin/") {
		targetRef = "origin/" + targetRef
	}

	// Fetch and unshallow both branches
	for _, ref := range []string{sourceRef, targetRef} {
		branch := stripPrefix(ref, "origin/")
		fetch := exec.Command("git", "-C", tempDir, "fetch", "--unshallow", "--quiet", "origin",
			branch+":refs/remotes/origin/"+branch)
		fetch.Stdout = nil
		fetch.Stderr = nil
		_ = fetch.Run()
	}

	// Remove shallow marker in case --unshallow wasn't enough
	shallowFile := filepath.Join(tempDir, ".git", "shallow")
	os.Remove(shallowFile)

	contentSynced := isContentIdentical(tempDir, sourceRef, targetRef)
	squashMerged := false
	if !contentSynced {
		squashMerged = isSquashMerged(tempDir, sourceRef, targetRef)
	}

	return contentSynced, squashMerged
}

// parseBranchTimestamp extracts time and author from a Gitea branch commit timestamp.
// Gitea returns timestamps in ISO 8601 format (e.g., "2026-04-13T10:49:15+07:00").
func parseBranchTimestamp(ts string) (time.Time, string) {
	if ts == "" {
		return time.Time{}, ""
	}

	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04:05-07:00",
		"2006-01-02 15:04:05 -0700",
	}
	for _, layout := range layouts {
		t, err := time.Parse(layout, ts)
		if err == nil {
			return t, ""
		}
	}
	return time.Time{}, ""
}

// hasPrefix reports whether s starts with prefix (unexported copy to avoid importing strings).
func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// stripPrefix removes prefix from s if present (unexported copy to avoid importing strings).
func stripPrefix(s, prefix string) string {
	if hasPrefix(s, prefix) {
		return s[len(prefix):]
	}
	return s
}
