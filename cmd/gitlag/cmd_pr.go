package main

import (
	"fmt"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/mystaline/gitlag/internal/config"
	"github.com/mystaline/gitlag/internal/gitea"
	"github.com/spf13/cobra"
)

type PRResult struct {
	Repository string
	Number     int
	Title      string
	HeadRef    string
	BaseRef    string
	Author     string
	CreatedAt  string
	AgeDays    int
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
					Number:     pr.Number,
					Title:      pr.Title,
					HeadRef:    pr.Head.Ref,
					BaseRef:    pr.Base.Ref,
					Author:     pr.User.Login,
					CreatedAt:  formatDate(pr.CreatedAt),
					AgeDays:    prAgeDays(pr.CreatedAt),
					URL:        pr.HTMLURL,
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

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func visualLen(s string) int {
	return utf8.RuneCountInString(ansiEscape.ReplaceAllString(s, ""))
}

func padRight(s string, width int) string {
	if pad := width - visualLen(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

func wrapWords(s string, width int) []string {
	if utf8.RuneCountInString(s) <= width {
		return []string{s}
	}
	var lines []string
	cur := ""
	for _, w := range strings.Fields(s) {
		switch {
		case cur == "":
			cur = w
		case utf8.RuneCountInString(cur)+1+utf8.RuneCountInString(w) <= width:
			cur += " " + w
		default:
			lines = append(lines, cur)
			cur = w
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

func wrapChars(s string, width int) []string {
	runes := []rune(s)
	var lines []string
	for len(runes) > width {
		lines = append(lines, string(runes[:width]))
		runes = runes[width:]
	}
	if len(runes) > 0 {
		lines = append(lines, string(runes))
	}
	return lines
}

func printPRTable(prs []PRResult) {
	const (
		wPR     = 5
		wRepo   = 22
		wTitle  = 38
		wRef    = 30
		wAuthor = 10
		wAge    = 5
	)
	widths := [6]int{wPR, wRepo, wTitle, wRef, wAuthor, wAge}

	sep := func(l, m, r string) string {
		var b strings.Builder
		b.WriteString(colorFaint)
		b.WriteString(l)
		for i, w := range widths {
			if i > 0 {
				b.WriteString(m)
			}
			b.WriteString(strings.Repeat("─", w+2))
		}
		b.WriteString(r)
		b.WriteString(colorReset)
		return b.String()
	}

	printRow := func(cells [6][]string) {
		maxL := 1
		for _, c := range cells {
			if len(c) > maxL {
				maxL = len(c)
			}
		}
		for i := 0; i < maxL; i++ {
			fmt.Print(colorFaint + "│" + colorReset)
			for j, c := range cells {
				line := ""
				if i < len(c) {
					line = c[i]
				}
				fmt.Print(" " + padRight(line, widths[j]) + " " + colorFaint + "│" + colorReset)
			}
			fmt.Println()
		}
	}

	fmt.Println(sep("┌", "┬", "┐"))
	printRow([6][]string{
		{colorBold + "PR" + colorReset},
		{colorBold + "REPOSITORY" + colorReset},
		{colorBold + "TITLE" + colorReset},
		{colorBold + "HEAD → BASE" + colorReset},
		{colorBold + "AUTHOR" + colorReset},
		{colorBold + "AGE" + colorReset},
	})
	fmt.Println(sep("├", "┼", "┤"))

	for _, pr := range prs {
		titleLines := wrapWords(pr.Title, wTitle)

		var refLines []string
		if utf8.RuneCountInString(pr.HeadRef+" → "+pr.BaseRef) <= wRef {
			refLines = []string{
				colorYellow + pr.HeadRef + colorReset + " " + colorFaint + "→" + colorReset + " " + colorGreen + pr.BaseRef + colorReset,
			}
		} else {
			for _, l := range wrapChars(pr.HeadRef, wRef) {
				refLines = append(refLines, colorYellow+l+colorReset)
			}
			refLines = append(refLines, colorFaint+"→"+colorReset+" "+colorGreen+pr.BaseRef+colorReset)
		}

		printRow([6][]string{
			{colorFaint + fmt.Sprintf("#%-4d", pr.Number) + colorReset},
			{colorCyan + truncate(pr.Repository, wRepo) + colorReset},
			titleLines,
			refLines,
			{colorFaint + truncate(pr.Author, wAuthor) + colorReset},
			{formatAge(pr.AgeDays)},
		})
	}

	fmt.Println(sep("└", "┴", "┘"))
}

func formatAge(days int) string {
	switch {
	case days <= 3:
		return fmt.Sprintf("%s%dd%s", colorGreen, days, colorReset)
	case days <= 14:
		return fmt.Sprintf("%s%dd%s", colorYellow, days, colorReset)
	default:
		return fmt.Sprintf("%s%dd%s", colorRed, days, colorReset)
	}
}

func prAgeDays(createdAt string) int {
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return 0
	}
	return int(time.Since(t).Hours() / 24)
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
