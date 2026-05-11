package main

import (
	"fmt"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mystaline/gitlag/internal/config"
	"github.com/mystaline/gitlag/internal/gitea"
	"github.com/spf13/cobra"
)

type PRResult struct {
	Repository string
	Title      string
	HeadRef    string
	BaseRef    string
	Author     string
	CreatedAt  string
	URL        string
}

func runPR(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	fmt.Printf("\n%s 🔍 Discovering open pull requests across repos...%s\n", colorCyan, colorReset)
	orgs := cfg.GetOrgs()
	client := gitea.NewClient(cfg.Gitea.URL, cfg.Gitea.Token, orgs[0])
	var repos []gitea.Repository

	for _, repoConfig := range cfg.Gitea.Repos {
		orgRepos, err := client.ListOrgRepos(repoConfig.Org)
		if err != nil {
			return fmt.Errorf("list repos from %s: %w", repoConfig.Org, err)
		}
		for _, r := range orgRepos {
			if gitea.MatchFilter(r.Name, repoConfig.Include, repoConfig.Exclude) {
				repos = append(repos, r)
			}
		}
	}

	fmt.Printf("\r%s ✅ Discovered %d repositories in %s%s\n", colorGreen, len(repos), strings.Join(orgs, ", "), colorReset)

	start := time.Now()
	maxWorkers := runtime.NumCPU()

	var (
		allPRs   []PRResult
		resultsMu sync.Mutex
		wg        sync.WaitGroup
		semaphore = make(chan struct{}, maxWorkers)
		repoCount int
		prCount   int
		countMu   sync.Mutex
	)

	for _, r := range repos {
		wg.Add(1)
		semaphore <- struct{}{}
		go func(repo gitea.Repository) {
			defer wg.Done()
			defer func() { <-semaphore }()

			orgName := extractOrgFromURL(repo.CloneURL)
			if orgName == "" {
				orgName = orgs[0]
			}
			repoClient := gitea.NewClient(cfg.Gitea.URL, cfg.Gitea.Token, orgName)

			pulls, err := repoClient.ListPullRequests(orgName, repo.Name)
			if err != nil {
				return
			}
			if len(pulls) == 0 {
				return
			}

			var repoPRs []PRResult
			for _, pr := range pulls {
				prResult := PRResult{
					Repository: repo.Name,
					Title:      pr.Title,
					HeadRef:    pr.Head.Ref,
					BaseRef:    pr.Base.Ref,
					Author:     pr.User.Login,
					CreatedAt:  formatDate(pr.CreatedAt),
					URL:         pr.HTMLURL,
				}

				repoPRs = append(repoPRs, prResult)
			}

			resultsMu.Lock()
			allPRs = append(allPRs, repoPRs...)
			resultsMu.Unlock()

			countMu.Lock()
			repoCount++
			prCount += len(repoPRs)
			countMu.Unlock()
		}(r)
	}
	wg.Wait()

	totalDuration := time.Since(start)

	sort.Slice(allPRs, func(i, j int) bool {
		if allPRs[i].Repository != allPRs[j].Repository {
			return allPRs[i].Repository < allPRs[j].Repository
		}
		return allPRs[i].CreatedAt > allPRs[j].CreatedAt
	})

	if len(allPRs) == 0 {
		fmt.Printf("\n%s ✅ No open pull requests found across %d repos.%s\n", colorGreen, len(repos), colorReset)
		return nil
	}

	printPRTable(allPRs)

	fmt.Printf("\n%s 💡 %d open PRs across %d repos (total time: %v)%s\n",
		colorFaint, prCount, repoCount, totalDuration.Round(time.Millisecond), colorReset)
	return nil
}

func printPRTable(prs []PRResult) {
	fmt.Printf("%s┌──────────────────────────────┬────────────────────────────────────────────────┬──────────────────────────────────────┬────────────┬────────────┐%s\n",
		colorFaint, colorReset)
	fmt.Printf("%s│%s %s %s│%s %s %s│%s %s %s│%s %s %s│%s %s %s│%s\n",
		colorFaint, colorReset, colorBold+"REPOSITORY"+colorReset, colorFaint,
		colorReset, colorBold+"TITLE"+colorReset, colorFaint,
		colorReset, colorBold+"HEAD → BASE"+colorReset, colorFaint,
		colorReset, colorBold+"AUTHOR"+colorReset, colorFaint,
		colorReset, colorBold+"DATE"+colorReset, colorFaint,
		colorReset)
	fmt.Printf("%s├──────────────────────────────┼────────────────────────────────────────────────┼──────────────────────────────────────┼────────────┼────────────┤%s\n",
		colorFaint, colorReset)

	for _, pr := range prs {
		repoCol := fmt.Sprintf("%s%-28s%s", colorCyan, truncate(pr.Repository, 28), colorReset)
		titleCol := truncate(pr.Title, 48)
		headBase := fmt.Sprintf("%s%-20s%s %s→%s %s%s%s",
			colorYellow, pr.HeadRef, colorReset, colorFaint, colorReset, colorGreen, pr.BaseRef, colorReset)
		authorCol := fmt.Sprintf("%s%-10s%s", colorFaint, truncate(pr.Author, 10), colorReset)
		dateCol := fmt.Sprintf("%s%-10s%s", colorFaint, pr.CreatedAt, colorReset)

		fmt.Printf("%s│%s %s %s│%s %-48s %s│%s %-36s %s│%s %-10s %s│%s %-10s %s│%s\n",
			colorFaint, colorReset, repoCol, colorFaint,
			colorReset, titleCol, colorFaint,
			colorReset, headBase, colorFaint,
			colorReset, authorCol, colorFaint,
			colorReset, dateCol, colorFaint,
			colorReset)
	}
	fmt.Printf("%s└──────────────────────────────┴────────────────────────────────────────────────┴──────────────────────────────────────┴────────────┴────────────┘%s\n",
		colorFaint, colorReset)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

func formatDate(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return iso[:10]
	}
	return t.Format("2006-01-02")
}
