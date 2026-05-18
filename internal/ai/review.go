package ai

import (
	"context"
	"fmt"
	"io"
)

// PRContext holds all information needed to review a pull request.
type PRContext struct {
	Repo    string
	Org     string
	Number  int
	Title   string
	Body    string
	Author  string
	HeadRef string
	BaseRef string
	Diff    string
}

// Reviewer sends a PR review to an AI provider and streams output to out.
type Reviewer interface {
	Review(ctx context.Context, pr PRContext, out io.Writer) error
}

func buildPrompt(pr PRContext) string {
	body := pr.Body
	if body == "" {
		body = "(no description provided)"
	}
	diff := pr.Diff
	if diff == "" {
		diff = "(no diff available)"
	}

	return fmt.Sprintf(`You are a senior software engineer performing a pull request review. Be direct and concise.

Provide your response in this structure:

## Goal Summary
What is this PR trying to achieve? (2-4 sentences, infer from diff if description is sparse)

## Key Changes
- (bullet list of main areas/files modified, max 6 items)

## Code Review
Specific issues, bugs, concerns, or suggestions. Be concrete — reference file names or logic when relevant. Skip generic praise.
Important: before flagging a variable as unused or dead code, trace ALL its usages in the full file shown — not just in the changed or commented-out section.

## Verdict
One of: ✅ LGTM | ⚠️ Minor concerns | ❌ Needs rework

---
Repository: %s/%s
PR #%d: %s
Author: %s
Branch: %s → %s

Description:
%s

Diff:
%s`, pr.Org, pr.Repo, pr.Number, pr.Title, pr.Author, pr.HeadRef, pr.BaseRef, body, diff)
}
