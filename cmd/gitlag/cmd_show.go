package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"

	"github.com/mystaline/gitlag/internal/config"
	"github.com/mystaline/gitlag/internal/gitea"
)

type ScanResult struct {
	Repository   string
	SourceBranch string
	Parent       string
	Divergence   map[string]DivergenceInfo
	Error        string
}

type DivergenceInfo struct {
	AheadCount      int
	BehindCount     int
	IsContentSynced bool
	IsSquashMerged  bool
	LastDate        string
	LastAuthor      string
}

func showImpl(configPath string, noFetch bool, format, repoName, source string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	var targetOrg string
	orgs := cfg.GetOrgs()
	client := gitea.NewClient(cfg.Gitea.URL, cfg.Gitea.Token, orgs[0])

	for _, repoConfig := range cfg.Gitea.Repos {
		orgRepos, err := client.ListOrgRepos(repoConfig.Org)
		if err != nil {
			continue
		}
		for _, r := range orgRepos {
			if r.Name == repoName && gitea.MatchFilter(r.Name, repoConfig.Include, repoConfig.Exclude) {
				targetOrg = repoConfig.Org
				break
			}
		}
		if targetOrg != "" {
			break
		}
	}
	if targetOrg == "" {
		return fmt.Errorf("repo not found in any configured org: %s", repoName)
	}

	repoClient := gitea.NewClient(cfg.Gitea.URL, cfg.Gitea.Token, targetOrg)

	branches, err := repoClient.ListBranches(targetOrg, repoName)
	if err != nil {
		return fmt.Errorf("list branches: %w", err)
	}

	result := ScanResult{
		Repository:   repoName,
		SourceBranch: source,
		Parent:       detectParentAPI(branches, source, cfg),
		Divergence:   make(map[string]DivergenceInfo),
	}

	// Filter branches
	var targets []string
	for _, b := range branches {
		if b == source {
			continue
		}
		if strings.HasSuffix(b, "-build") || strings.HasSuffix(b, "-builds") {
			continue
		}
		targets = append(targets, b)
	}

	// Concurrent compare
	var mu sync.Mutex
	maxWorkers := runtime.NumCPU()
	sem := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup

	for _, target := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(target string) {
			defer wg.Done()
			defer func() { <-sem }()

			cmpr, apiErr := repoClient.CompareBranches(targetOrg, repoName, source, target)
			if apiErr != nil || cmpr == nil {
				return
			}

			branchInfo, _ := repoClient.GetBranchInfo(targetOrg, repoName, target)
			lastDate, lastAuthor := parseBranchInfo(branchInfo)

			mu.Lock()
			result.Divergence[target] = DivergenceInfo{
				AheadCount:  cmpr.AheadBy,
				BehindCount: cmpr.BehindBy,
				LastDate:    lastDate,
				LastAuthor:  lastAuthor,
			}
			mu.Unlock()
		}(target)
	}
	wg.Wait()

	if format == "json" {
		outputShowJSON(result)
	} else {
		outputShowTable(result)
	}
	return nil
}

func detectParentAPI(branches []string, source string, cfg *config.Config) string {
	for _, b := range branches {
		if b == source {
			continue
		}
		if strings.HasPrefix(source, "dev") && b == "staging" {
			return "staging"
		}
		if source == "staging" && b == "main" {
			return "main"
		}
	}
	if strings.HasPrefix(source, "feature/") {
		return "dev"
	}
	if strings.HasPrefix(source, "fix/") {
		return "staging"
	}
	if strings.HasPrefix(source, "hotfix/") {
		return "main"
	}
	if strings.HasPrefix(source, "dev") {
		return "staging"
	}
	if source == "staging" {
		return "main"
	}
	return "main"
}

func outputShowTable(result ScanResult) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	defer w.Flush()

	fmt.Fprintf(w, "%sRepository:%s %s\n", colorGreen, colorReset, result.Repository)
	fmt.Fprintf(w, "%sBranch:%s %s\n", colorGreen, colorReset, result.SourceBranch)
	fmt.Fprintf(w, "%sParent:%s %s\n", colorGreen, colorReset, result.Parent)

	if result.Error != "" {
		fmt.Fprintf(w, "%sError:%s %s\n", colorRed, colorReset, result.Error)
		return
	}

	fmt.Fprintf(w, "\n%sEach branch compared to %s:%s\n", colorFaint, result.SourceBranch, colorReset)
	fmt.Fprintf(w, "  %s%-38s %s\n", colorFaint+"BRANCH"+colorReset, "", "DIVERGENCE")

	var targets []string
	for t := range result.Divergence {
		targets = append(targets, t)
	}
	sort.Strings(targets)

	for _, targetBranch := range targets {
		div := result.Divergence[targetBranch]

		var parts []string
		if div.AheadCount > 0 {
			parts = append(parts, fmt.Sprintf("%s↑ %d ahead of source%s", colorYellow, div.AheadCount, colorReset))
		}
		if div.BehindCount > 0 {
			parts = append(parts, fmt.Sprintf("%s↓ %d behind source%s", colorRed, div.BehindCount, colorReset))
		}
		if div.IsContentSynced {
			parts = append(parts, fmt.Sprintf("%s≡ identical content%s", colorCyan, colorReset))
		}
		if div.IsSquashMerged {
			parts = append(parts, fmt.Sprintf("%s⊙ squash merged%s", colorCyan, colorReset))
		}
		if len(parts) == 0 {
			parts = append(parts, fmt.Sprintf("%s✓ synced%s", colorGreen, colorReset))
		}

		divergence := strings.Join(parts, "   ")

		line := fmt.Sprintf("  %s%-36s%s %s", colorCyan, targetBranch, colorReset, divergence)
		if div.LastDate != "" {
			line += fmt.Sprintf("   %s%s%s", colorFaint, div.LastDate, colorReset)
		}
		fmt.Fprintf(w, "%s\n", line)
	}
}

func outputShowJSON(result ScanResult) {
	data := map[string]interface{}{
		"repository":    result.Repository,
		"source_branch": result.SourceBranch,
		"parent":        result.Parent,
		"divergence":    result.Divergence,
	}
	if result.Error != "" {
		data["error"] = result.Error
	}
	json.NewEncoder(os.Stdout).Encode(data)
}
