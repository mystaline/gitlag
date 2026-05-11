package main

import (
	"fmt"
	"path"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mystaline/gitlag/internal/config"
	"github.com/mystaline/gitlag/internal/gitea"
	"github.com/mystaline/gitlag/internal/repo"
)

type BulkResult struct {
	Repository      string
	Comparisons     []BranchComparison
	ResolvedTargets []string
	Duration        time.Duration
	PrepTime        time.Duration
	Error           string
}

type BranchComparison struct {
	TargetBranch string
	Info         DivergenceInfo
	InSync       bool
	IsMissing    bool
}

type workerStatus struct {
	repoName string
	status   string
}

func bulkImpl(configPath string, noFetch bool, format, source string, targets []string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return err
	}

	fmt.Printf("\n%s 🔍 Discovering Gitea repositories...%s\n", colorCyan, colorReset)
	orgs := cfg.GetOrgs()
	client := gitea.NewClient(cfg.Gitea.URL, cfg.Gitea.Token, orgs[0])
	var repos []gitea.Repository

	for _, repoConfig := range cfg.Gitea.Repos {
		orgRepos, err := client.ListOrgRepos(repoConfig.Org)
		if err != nil {
			if cfg.Offline {
				mgr := repo.NewManager(cfg.CacheDir, repoConfig.Org)
				orgRepos, err = mgr.ListLocalRepos()
				if err != nil {
					return fmt.Errorf("list repos from %s: %w", repoConfig.Org, err)
				}
			} else {
				return fmt.Errorf("list repos from %s: %w", repoConfig.Org, err)
			}
		}

		// Filter repos using per-org include/exclude patterns
		for _, r := range orgRepos {
			if gitea.MatchFilter(r.Name, repoConfig.Include, repoConfig.Exclude) {
				repos = append(repos, r)
			}
		}
	}

	fmt.Printf("\r%s ✅ Discovered %d repositories in %s%s\n", colorGreen, len(repos), strings.Join(orgs, ", "), colorReset)
	fmt.Printf("   Source: %s%s%s | Targets: %s%s%s\n\n", colorBold, source, colorReset, colorBold, strings.Join(targets, ", "), colorReset)

	start := time.Now()

	maxWorkers := runtime.NumCPU()

	var (
		results []BulkResult
		resultsMu sync.Mutex
		wg sync.WaitGroup
		semaphore = make(chan struct{}, maxWorkers) 
		
		// Status tracking
		completedCount int
		completedMu    sync.Mutex
		workerStates   = make([]workerStatus, maxWorkers)
		statesMu       sync.Mutex
	)

	// Clean up any leftovers from previous terminal state
	fmt.Print("\033[s") // Save cursor position

	// UI Refresh Loop
	done := make(chan bool)
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				statesMu.Lock()
				completedMu.Lock()
				
				// Reset to saved position
				fmt.Print("\033[u\033[J") 
				
				// Show total progress
				percent := float64(completedCount) / float64(len(repos)) * 100
				barWidth := 30
				filled := int(float64(barWidth) * float64(completedCount) / float64(len(repos)))
				bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
				
				fmt.Printf("%s 📊 Progress: [%s] %.0f%% (%d/%d)%s\n", colorCyan, bar, percent, completedCount, len(repos), colorReset)
				
				// Show active workers
				for i, state := range workerStates {
					if state.repoName != "" {
						fmt.Printf("   %s Slot %d:%s %-30s %s%s%s\n", colorFaint, i+1, colorReset, state.repoName, colorYellow, state.status, colorReset)
					} else {
						fmt.Printf("   %s Slot %d:%s %s-- idle --%s\n", colorFaint, i+1, colorReset, colorFaint, colorReset)
					}
				}
				
				completedMu.Unlock()
				statesMu.Unlock()
			}
		}
	}()

	for i, r := range repos {
		wg.Add(1)
		go func(i int, r gitea.Repository) {
			defer wg.Done()

			slot := -1
			semaphore <- struct{}{}
			statesMu.Lock()
			for s := 0; s < maxWorkers; s++ {
				if workerStates[s].repoName == "" {
					slot = s
					workerStates[slot].repoName = r.Name
					workerStates[slot].status = "API compare..."
					break
				}
			}
			statesMu.Unlock()

			defer func() {
				statesMu.Lock()
				workerStates[slot].repoName = ""
				workerStates[slot].status = ""
				statesMu.Unlock()

				completedMu.Lock()
				completedCount++
				completedMu.Unlock()
				<-semaphore
			}()

			repoStart := time.Now()

			orgName := extractOrgFromURL(r.CloneURL)
			if orgName == "" {
				orgs := cfg.GetOrgs()
				orgName = orgs[0]
			}
			repoClient := gitea.NewClient(cfg.Gitea.URL, cfg.Gitea.Token, orgName)

			// Resolve glob patterns via API
			var availBranches []string
			var resolvedTargets []string
			if hasGlobPattern(targets) {
				availBranches, _ = repoClient.ListBranches(orgName, r.Name)
				resolvedTargets = resolveTargetPatterns(targets, availBranches)
			} else {
				resolvedTargets = targets
			}

			result := BulkResult{
				Repository:      r.Name,
				PrepTime:        0,
				ResolvedTargets: resolvedTargets,
			}

			for _, target := range resolvedTargets {
				statesMu.Lock()
				workerStates[slot].status = "API compare " + target + "..."
				statesMu.Unlock()

				cmpr, apiErr := repoClient.CompareBranches(orgName, r.Name, source, target)
				if apiErr != nil {
					result.Comparisons = append(result.Comparisons, BranchComparison{
						TargetBranch: target,
						IsMissing:   true,
					})
					continue
				}
				if cmpr == nil {
					result.Comparisons = append(result.Comparisons, BranchComparison{
						TargetBranch: target,
						IsMissing:   true,
					})
					continue
				}

				branchInfo, _ := repoClient.GetBranchInfo(orgName, r.Name, target)

				lastDate, lastAuthor := parseBranchInfo(branchInfo)

				contentSynced := false
				squashMerged := false

				info := DivergenceInfo{
					AheadCount:      cmpr.AheadBy,
					BehindCount:     cmpr.BehindBy,
					IsContentSynced: contentSynced,
					IsSquashMerged:  squashMerged,
					LastDate:        lastDate,
					LastAuthor:       lastAuthor,
				}

				result.Comparisons = append(result.Comparisons, BranchComparison{
					TargetBranch: target,
					Info:         info,
					InSync:       cmpr.AheadBy == 0 && cmpr.BehindBy == 0,
				})
			}

			resultsMu.Lock()
			result.Duration = time.Since(repoStart)
			results = append(results, result)
			resultsMu.Unlock()
		}(i, r)
	}

	wg.Wait()
	done <- true
	totalDuration := time.Since(start)

	// Final clear of the status area
	fmt.Print("\033[u\033[J")

	fmt.Printf(" ✅ %sAnalysis Complete%s (total time: %s%v%s)\n", colorBold+colorGreen, colorReset, colorBold, totalDuration.Round(time.Millisecond), colorReset)
	fmt.Printf("%s --------------------------------------------------------------------------------%s\n\n", colorFaint, colorReset)

	// Compute union of all resolved targets (patterns may yield different branches per repo)
	displayTargets := unionResolvedTargets(results)
	if len(displayTargets) == 0 {
		displayTargets = targets
	}
	outputBulkTable(results, source, displayTargets)

	return nil
}

// resolveTargetPatterns expands glob patterns (e.g. "v1.1/*") against the available
// remote branches for a single repo. Exact names pass through unchanged.
func resolveTargetPatterns(patterns []string, allBranches []string) []string {
	seen := make(map[string]bool)
	var resolved []string
	for _, pattern := range patterns {
		if !strings.ContainsAny(pattern, "*?[") {
			if !seen[pattern] {
				seen[pattern] = true
				resolved = append(resolved, pattern)
			}
			continue
		}
		for _, b := range allBranches {
			name := strings.TrimPrefix(b, "origin/")
			if ok, _ := path.Match(pattern, name); ok && !seen[name] {
				seen[name] = true
				resolved = append(resolved, name)
			}
		}
	}
	return resolved
}

// unionResolvedTargets returns sorted unique target branch names across all results.
func unionResolvedTargets(results []BulkResult) []string {
	seen := make(map[string]bool)
	var all []string
	for _, r := range results {
		for _, t := range r.ResolvedTargets {
			if !seen[t] {
				seen[t] = true
				all = append(all, t)
			}
		}
	}
	sort.Strings(all)
	return all
}

func outputBulkTable(results []BulkResult, source string, targets []string) {
	sort.Slice(results, func(i, j int) bool {
		return results[i].Repository < results[j].Repository
	})

	repoWidth := 10
	for _, r := range results {
		if len(r.Repository) > repoWidth {
			repoWidth = len(r.Repository)
		}
	}

	targetWidths := make([]int, len(targets))
	for i, t := range targets {
		w := len(t)
		if w < 6 {
			w = 6
		}
		targetWidths[i] = w
	}
	timeWidth := 8

	colWidths := append([]int{repoWidth}, targetWidths...)
	colWidths = append(colWidths, timeWidth)

	printBorder := func(left, sep, right string) {
		fmt.Print(colorFaint + left)
		for i, w := range colWidths {
			if i > 0 {
				fmt.Print(sep)
			}
			fmt.Print(strings.Repeat("─", w+2))
		}
		fmt.Println(right + colorReset)
	}

	padAlign := func(s string, vis, width int, center bool) string {
		if vis >= width {
			return s
		}
		if center {
			left := (width - vis) / 2
			right := width - vis - left
			return strings.Repeat(" ", left) + s + strings.Repeat(" ", right)
		}
		return s + strings.Repeat(" ", width-vis)
	}

	// Caption: source → target
	fmt.Printf("%s  %s%s%s %s→%s %s%s%s\n",
		colorFaint, colorBold, source, colorReset, colorFaint, colorReset, colorBold, strings.Join(targets, ", "), colorReset)

	// Top Border
	printBorder("┌", "┬", "┐")

	// Header Row
	fmt.Printf("%s│%s %s %s", colorFaint, colorReset, padAlign(colorBold+"REPOSITORY"+colorReset, 10, repoWidth, false), colorFaint)
	for i, t := range targets {
		headerText := strings.ToUpper(t)
		coloredHeader := fmt.Sprintf("%s%s%s", colorBold, headerText, colorReset)
		fmt.Printf("│%s %s %s", colorReset, padAlign(coloredHeader, len(headerText), targetWidths[i], true), colorFaint)
	}
	fmt.Printf("│%s %s %s│%s\n", colorReset, padAlign(colorBold+"TIME"+colorReset, 4, timeWidth, true), colorFaint, colorReset)

	// Middle Border
	printBorder("├", "┼", "┤")

	// Data Rows
	for _, r := range results {
		coloredRepo := fmt.Sprintf("%s%s%s", colorCyan, r.Repository, colorReset)
		fmt.Printf("%s│%s %s %s", colorFaint, colorReset, padAlign(coloredRepo, len(r.Repository), repoWidth, false), colorFaint)

		if r.Error != "" {
			errText := fmt.Sprintf("Error: %s", r.Error)
			fmt.Printf("│%s %s ", colorReset, padAlign(colorRed+errText+colorReset, len(errText), targetWidths[0], false))
			// Fill remaining columns if any
			for i := 1; i < len(targets); i++ {
				fmt.Printf("%s│%s %s ", colorFaint, colorReset, padAlign("", 0, targetWidths[i], false))
			}
			fmt.Printf("%s│%s %s %s│%s\n", colorFaint, colorReset, padAlign("", 0, timeWidth, false), colorFaint, colorReset)
			continue
		}

		compMap := make(map[string]BranchComparison)
		for _, c := range r.Comparisons {
			compMap[c.TargetBranch] = c
		}

		for i, target := range targets {
			comp, ok := compMap[target]
			
			colored := ""
			visible := 0

			if !ok || comp.IsMissing {
				colored = fmt.Sprintf("%s—%s", colorFaint, colorReset)
				visible = 1
			} else if comp.Info.IsContentSynced {
				colored = fmt.Sprintf("%s≡ identical%s", colorCyan, colorReset)
				visible = 10
			} else if comp.Info.IsSquashMerged {
				colored = fmt.Sprintf("%s⊙ squashed%s", colorCyan, colorReset)
				visible = 10
			} else if comp.InSync {
				colored = fmt.Sprintf("%s✔ sync%s", colorGreen, colorReset)
				visible = 6
			} else {
				if comp.Info.AheadCount > 0 {
					val := fmt.Sprintf("↑ %dA", comp.Info.AheadCount)
					colored += fmt.Sprintf("%s%s%s", colorYellow, val, colorReset)
					visible += len([]rune(val))
				}
				if comp.Info.BehindCount > 0 {
					if colored != "" {
						colored += " "
						visible += 1
					}
					val := fmt.Sprintf("↓ %dB", comp.Info.BehindCount)
					colored += fmt.Sprintf("%s%s%s", colorRed, val, colorReset)
					visible += len([]rune(val))
				}
			}

			fmt.Printf("│%s %s %s", colorReset, padAlign(colored, visible, targetWidths[i], true), colorFaint)
		}
		
		timeStr := r.Duration.Round(time.Millisecond).String()
		fmt.Printf("│%s %s %s│%s\n", colorReset, padAlign(colorFaint+timeStr+colorReset, len(timeStr), timeWidth, true), colorFaint, colorReset)
	}

	// Bottom Border
	printBorder("└", "┴", "┘")

	fmt.Printf("\n%s 💡 ✔ sync | ↑ source ahead | ↓ source behind | ≡ content identical | ⊙ squash merged | — missing%s\n", colorFaint, colorReset)
}

// extractOrgFromURL extracts organization name from clone URL
// Format: https://host/org/repo.git or https://host/org/repo
func extractOrgFromURL(cloneURL string) string {
	url := strings.TrimSuffix(cloneURL, ".git")
	parts := strings.Split(url, "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2]
	}
	return ""
}

// parseBranchInfo extracts last commit date and author from a Gitea BranchInfo.
func parseBranchInfo(info *gitea.BranchInfo) (date, author string) {
	if info == nil || info.Commit.Timestamp == "" {
		return "", ""
	}
	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04:05-07:00",
		"2006-01-02 15:04:05 -0700",
	}
	for _, layout := range layouts {
		t, err := time.Parse(layout, info.Commit.Timestamp)
		if err == nil && !t.IsZero() {
			return t.Format("2006-01-02"), ""
		}
	}
	return "", ""
}

// hasGlobPattern returns true if any pattern in the list contains glob characters.
func hasGlobPattern(patterns []string) bool {
	for _, p := range patterns {
		if strings.ContainsAny(p, "*?[") {
			return true
		}
	}
	return false
}
