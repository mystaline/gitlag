package main

import (
	"context"
	"fmt"
	"os"

	"github.com/mystaline/gitlag/internal/ai"
	"github.com/mystaline/gitlag/internal/config"
	"github.com/mystaline/gitlag/internal/gitea"
	"github.com/spf13/cobra"
)

func runReview(cmd *cobra.Command, args []string) error {
	repoName, _ := cmd.Flags().GetString("repo")
	prNumber, _ := cmd.Flags().GetInt("pr")
	orgFlag, _ := cmd.Flags().GetString("org")

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	if cfg.AI.Provider == "" {
		return fmt.Errorf("ai.provider not set — add ai.provider and ai.model to gitlag.yaml")
	}

	org := orgFlag
	if org == "" {
		org = findOrgForRepo(cfg, repoName)
		if org == "" {
			return fmt.Errorf("cannot determine org for repo %q — use --org to specify", repoName)
		}
	}

	client := gitea.NewClient(cfg.Gitea.URL, cfg.Gitea.Token, org, cfg.Gitea.Timeout)

	fmt.Printf("\n%s 🔍 Fetching PR #%d from %s/%s...%s\n", colorCyan, prNumber, org, repoName, colorReset)
	pr, err := client.GetPullRequest(org, repoName, prNumber)
	if err != nil {
		return fmt.Errorf("fetch PR: %w", err)
	}

	fmt.Printf("%s 📄 Fetching diff...%s\n", colorCyan, colorReset)
	diff, _, err := client.GetPullRequestDiff(org, repoName, prNumber)
	if err != nil {
		return fmt.Errorf("fetch diff: %w", err)
	}

	reviewer, err := ai.NewReviewer(cfg.AI.Provider, cfg.AI.Model)
	if err != nil {
		return fmt.Errorf("init reviewer: %w", err)
	}

	prCtx := ai.PRContext{
		Repo:    repoName,
		Org:     org,
		Number:  prNumber,
		Title:   pr.Title,
		Body:    pr.Body,
		Author:  pr.User.Login,
		HeadRef: pr.Head.Ref,
		BaseRef: pr.Base.Ref,
		Diff:    diff,
	}

	fmt.Printf("%s 🤖 Reviewing with %s / %s...%s\n\n", colorCyan, cfg.AI.Provider, cfg.AI.Model, colorReset)
	printReviewHeader(pr.Title, pr.Head.Ref, pr.Base.Ref, pr.User.Login)
	fmt.Println()

	if err := reviewer.Review(context.Background(), prCtx, os.Stdout); err != nil {
		return fmt.Errorf("review: %w", err)
	}

	fmt.Println()
	return nil
}

func printReviewHeader(title, head, base, author string) {
	fmt.Printf("%s╔══════════════════════════════════════════════════════════════╗%s\n", colorFaint, colorReset)
	fmt.Printf("%s║%s %s%s%s\n", colorFaint, colorReset, colorBold, truncate(title, 60), colorReset)
	fmt.Printf("%s║%s %s%s%s → %s%s%s  by %s%s%s\n",
		colorFaint, colorReset,
		colorYellow, head, colorReset,
		colorGreen, base, colorReset,
		colorFaint, author, colorReset)
	fmt.Printf("%s╚══════════════════════════════════════════════════════════════╝%s\n", colorFaint, colorReset)
}

func findOrgForRepo(cfg *config.Config, repoName string) string {
	for _, repoConfig := range cfg.Gitea.Repos {
		if gitea.MatchFilter(repoName, repoConfig.Include, repoConfig.Exclude) {
			return repoConfig.Org
		}
	}
	return ""
}
