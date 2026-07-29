package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/mystaline/gitlag/internal/config"
	"github.com/mystaline/gitlag/internal/gitea"
)

type ScanResult struct {
	Repository   string                    `json:"repository"`
	SourceBranch string                    `json:"source_branch"`
	Parent       string                    `json:"parent"`
	Divergence   map[string]DivergenceInfo `json:"divergence"`
	Error        string                    `json:"error,omitempty"`
}

type DivergenceInfo struct {
	AheadCount       int    `json:"ahead"`
	BehindCount      int    `json:"behind"`
	AheadAdditions   int    `json:"ahead_additions"`
	AheadDeletions   int    `json:"ahead_deletions"`
	BehindAdditions  int    `json:"behind_additions"`
	BehindDeletions  int    `json:"behind_deletions"`
	IsContentSynced  bool   `json:"in_sync"`
	IsSquashMerged   bool   `json:"squash_merged"`
	EmptyAheadDiff   bool   `json:"empty_ahead_diff"`
	EmptyBehindDiff  bool   `json:"empty_behind_diff"`
	LastDate         string `json:"last_date"`
	LastAuthor       string `json:"last_author"`
}

type showWorkerSlot struct {
	branch string
	status string
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
	client := gitea.NewClient(cfg.Gitea.URL, cfg.Gitea.Token, orgs[0], cfg.Gitea.Timeout)

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

	repoClient := gitea.NewClient(cfg.Gitea.URL, cfg.Gitea.Token, targetOrg, cfg.Gitea.Timeout)

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

	maxWorkers := runtime.NumCPU()
	var (
		resultsMu      sync.Mutex
		wg             sync.WaitGroup
		semaphore      = make(chan struct{}, maxWorkers)
		completedCount int
		completedMu    sync.Mutex
		workerStates   = make([]showWorkerSlot, maxWorkers)
		statesMu       sync.Mutex
		done           chan bool
	)

	if format == "table" && len(targets) > 0 {
		fmt.Print("\033[s")
		done = make(chan bool)
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

					fmt.Print("\033[u\033[J")

					percent := float64(completedCount) / float64(len(targets)) * 100
					barWidth := 30
					filled := int(float64(barWidth) * float64(completedCount) / float64(len(targets)))
					bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)

					fmt.Printf("%s 📊 Comparing branches: [%s] %.0f%% (%d/%d)%s\n",
						colorCyan, bar, percent, completedCount, len(targets), colorReset)

					for i, state := range workerStates {
						if state.branch != "" {
							fmt.Printf("   %sSlot %d:%s %-30s %s%s%s\n",
								colorFaint, i+1, colorReset, state.branch,
								colorYellow, state.status, colorReset)
						} else {
							fmt.Printf("   %sSlot %d:%s %s-- idle --%s\n",
								colorFaint, i+1, colorReset, colorFaint, colorReset)
						}
					}

					completedMu.Unlock()
					statesMu.Unlock()
				}
			}
		}()
	}

	for _, target := range targets {
		wg.Add(1)
		semaphore <- struct{}{}

		go func(target string) {
			slot := -1
			statesMu.Lock()
			for s := 0; s < maxWorkers; s++ {
				if workerStates[s].branch == "" {
					slot = s
					workerStates[slot].branch = target
					workerStates[slot].status = "comparing..."
					break
				}
			}
			statesMu.Unlock()

			defer func() {
				statesMu.Lock()
				workerStates[slot].branch = ""
				workerStates[slot].status = ""
				statesMu.Unlock()

				completedMu.Lock()
				completedCount++
				completedMu.Unlock()
				<-semaphore
				wg.Done()
			}()

			statesMu.Lock()
			workerStates[slot].status = "API compare..."
			statesMu.Unlock()

			cmpr, apiErr := repoClient.CompareBranches(targetOrg, repoName, source, target)
			if apiErr != nil {
				statesMu.Lock()
				workerStates[slot].status = "branch info..."
				statesMu.Unlock()

				fmt.Fprintf(os.Stderr, "\n%s⚠ %s vs %s: %v%s\n", colorYellow, source, target, apiErr, colorReset)
				return
			}
			if cmpr == nil {
				return
			}

			statesMu.Lock()
			workerStates[slot].status = "branch info..."
			statesMu.Unlock()

			branchInfo, _ := repoClient.GetBranchInfo(targetOrg, repoName, target)
			contentSynced := (cmpr.AheadBy == 0 || cmpr.EmptyAheadDiff) && (cmpr.BehindBy == 0 || cmpr.EmptyBehindDiff)
			lastDate, lastAuthor := parseBranchInfo(branchInfo)

			resultsMu.Lock()
			result.Divergence[target] = DivergenceInfo{
				AheadCount:      cmpr.AheadBy,
				BehindCount:     cmpr.BehindBy,
				AheadAdditions:  cmpr.AheadAdditions,
				AheadDeletions:  cmpr.AheadDeletions,
				BehindAdditions: cmpr.BehindAdditions,
				BehindDeletions: cmpr.BehindDeletions,
				EmptyAheadDiff:  cmpr.EmptyAheadDiff,
				EmptyBehindDiff: cmpr.EmptyBehindDiff,
				LastDate:        lastDate,
				LastAuthor:      lastAuthor,
				IsContentSynced: contentSynced,
			}
			resultsMu.Unlock()
		}(target)
	}
	wg.Wait()

	if done != nil {
		done <- true
	}

	if format == "table" && len(targets) > 0 {
		fmt.Print("\033[u\033[J")
		fmt.Printf("%s ✅ Branch comparison complete%s\n", colorBold+colorGreen, colorReset)
	}

	switch format {
	case "json":
		outputShowJSON(result)
	case "csv":
		outputShowCSV(result)
	case "markdown", "md":
		outputShowMarkdown(result)
	default:
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

	var targets []string
	for t := range result.Divergence {
		targets = append(targets, t)
	}
	sort.Strings(targets)

	maxBranchLen := len("BRANCH")
	for _, b := range targets {
		if len(b) > maxBranchLen {
			maxBranchLen = len(b)
		}
	}

	type rowOut struct {
		col  string
		date string
	}

	colMinPad := 4
	var rows []rowOut
	maxColVis := 0

	for _, targetBranch := range targets {
		div := result.Divergence[targetBranch]

		var parts []string
		if div.AheadCount > 0 && !div.EmptyAheadDiff {
			parts = append(parts, fmt.Sprintf("%s↓ %d behind source%s", colorRed, div.AheadCount, colorReset))
		}
		if div.BehindCount > 0 && !div.EmptyBehindDiff {
			parts = append(parts, fmt.Sprintf("%s↑ %d ahead of source%s", colorYellow, div.BehindCount, colorReset))
		}
		if div.IsContentSynced {
			parts = append(parts, fmt.Sprintf("%s≡ identical content%s", colorCyan, colorReset))
		}
		if div.IsSquashMerged {
			parts = append(parts, fmt.Sprintf("%s⊙ squash merged%s", colorCyan, colorReset))
		}
		if len(parts) == 0 {
			if div.AheadCount > 0 || div.BehindCount > 0 {
				parts = append(parts, fmt.Sprintf("%s≡ identical content%s", colorCyan, colorReset))
			} else {
				parts = append(parts, fmt.Sprintf("%s✓ synced%s", colorGreen, colorReset))
			}
		}

		divergence := strings.Join(parts, "   ")

		pad := maxBranchLen - len(targetBranch)
		if pad < 0 { pad = 0 }
		col := fmt.Sprintf("  %s%s%s%*s %s", colorCyan, targetBranch, colorReset, pad, "", divergence)

		colVis := visibleLen(col)
		rows = append(rows, rowOut{col: col, date: div.LastDate})
		if colVis > maxColVis {
			maxColVis = colVis
		}
	}

	fmt.Fprintf(w, "\n%sEach branch compared to %s:%s\n", colorFaint, result.SourceBranch, colorReset)
	fmt.Fprintf(w, "  %sBRANCH%s%*s %sDIVERGENCE%s\n", colorFaint, colorReset, maxBranchLen-len("BRANCH"), "", colorFaint, colorReset)

	for _, r := range rows {
		line := r.col
		if r.date != "" {
			cur := visibleLen(line)
			pad := maxColVis - cur + colMinPad
			if pad < colMinPad { pad = colMinPad }
			line += strings.Repeat(" ", pad) + colorFaint + r.date + colorReset
		}
		fmt.Fprintf(w, "%s\n", line)
	}
}
func visibleLen(s string) int {
	for _, seq := range []string{"\033[0m", "\033[1m", "\033[2m", "\033[31m", "\033[32m", "\033[33m", "\033[36m"} {
		s = strings.ReplaceAll(s, seq, "")
	}
	return len(s)
}

func outputShowCSV(result ScanResult) {
	branches := make([]string, 0, len(result.Divergence))
	for b := range result.Divergence {
		branches = append(branches, b)
	}
	sort.Strings(branches)

	w := csv.NewWriter(os.Stdout)
	_ = w.Write([]string{"repository", "source_branch", "branch", "ahead", "behind", "ahead_additions", "ahead_deletions", "behind_additions", "behind_deletions", "in_sync", "last_date", "last_author"})
	for _, b := range branches {
		info := result.Divergence[b]
		_ = w.Write([]string{
			result.Repository,
			result.SourceBranch,
			b,
			strconv.Itoa(info.AheadCount),
			strconv.Itoa(info.BehindCount),
			strconv.Itoa(info.AheadAdditions),
			strconv.Itoa(info.AheadDeletions),
			strconv.Itoa(info.BehindAdditions),
			strconv.Itoa(info.BehindDeletions),
			strconv.FormatBool(info.IsContentSynced),
			info.LastDate,
			info.LastAuthor,
		})
	}
	w.Flush()
}

func outputShowMarkdown(result ScanResult) {
	branches := make([]string, 0, len(result.Divergence))
	for b := range result.Divergence {
		branches = append(branches, b)
	}
	sort.Strings(branches)

	fmt.Printf("## %s (source: `%s`)\n\n", result.Repository, result.SourceBranch)
	fmt.Println("| Branch | Ahead | Behind | Ahead +/- | Behind +/- | Last Date |")
	fmt.Println("|--------|------:|-------:|:---------:|:----------:|-----------|")
	for _, b := range branches {
		info := result.Divergence[b]
		aheadDisplay := strconv.Itoa(info.AheadCount)
		if info.EmptyAheadDiff {
			aheadDisplay = "≡ identical"
		}
		behindDisplay := strconv.Itoa(info.BehindCount)
		if info.EmptyBehindDiff {
			behindDisplay = "≡ identical"
		}
		aheadDelta := fmt.Sprintf("+%d/-%d", info.AheadAdditions, info.AheadDeletions)
		behindDelta := fmt.Sprintf("+%d/-%d", info.BehindAdditions, info.BehindDeletions)
		fmt.Printf("| `%s` | %s | %s | %s | %s | %s |\n",
			b, aheadDisplay, behindDisplay, aheadDelta, behindDelta, info.LastDate)
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
