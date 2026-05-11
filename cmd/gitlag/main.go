package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	configPath string
	noFetch    bool
	format     string
)

const (
	colorGreen  = "\033[32m"
	colorRed    = "\033[31m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
	colorBold   = "\033[1m"
	colorFaint  = "\033[2m"
	colorReset  = "\033[0m"
)

var rootCmd = &cobra.Command{
	Use:   "gitlag",
	Short: "Track branch divergence across repos",
	Long:  `gitlag scans your organization's repos and reports commits ahead/behind between branches.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Prevent git from hang on interactive prompts
		os.Setenv("GIT_TERMINAL_PROMPT", "0")
		os.Setenv("GIT_SSH_COMMAND", "ssh -o BatchMode=yes")
		return nil
	},
}

var showCmd = &cobra.Command{
	Use:   "show",
	Short: "Show divergence for a specific branch",
	Long:  `Show branch divergence for a specific repo and source branch.`,
	RunE: runShow,
}

var compareCmd = &cobra.Command{
	Use:   "compare",
	Short: "Bulk compare a source branch against multiple targets",
	Long:  `Bulk compare a source branch (e.g. staging) against multiple target branches (e.g. dev, dev-alpha) across all repos.`,
	RunE: runCompare,
}

var prCmd = &cobra.Command{
	Use:   "pr",
	Short: "List open pull requests across all repos with branch divergence",
	Long:  `Scans all configured repos for open pull requests and shows head→base divergence for each.`,
	RunE: runPR,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&configPath, "config", "", "path to config file (default: ~/.gitlag.yaml)")
	rootCmd.PersistentFlags().BoolVar(&noFetch, "no-fetch", false, "use cache without fetching")
	rootCmd.PersistentFlags().StringVar(&format, "format", "table", "output format: table or json")

	showCmd.Flags().StringP("source", "s", "dev", "source branch to compare from")
	showCmd.Flags().StringP("repo", "r", "", "repository name (required)")

	compareCmd.Flags().StringP("source", "s", "staging", "source branch to compare from (base)")
	compareCmd.Flags().StringSliceP("targets", "t", []string{}, "comma-separated target branches to compare against (e.g. dev,dev-alpha)")
	_ = compareCmd.MarkFlagRequired("targets")

	rootCmd.AddCommand(showCmd)
	rootCmd.AddCommand(compareCmd)
	rootCmd.AddCommand(prCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func runShow(cmd *cobra.Command, args []string) error {
	source, _ := cmd.Flags().GetString("source")
	repo, _ := cmd.Flags().GetString("repo")
	if repo == "" {
		return fmt.Errorf("--repo is required")
	}
	return show(configPath, noFetch, format, repo, source)
}

func runCompare(cmd *cobra.Command, args []string) error {
	source, _ := cmd.Flags().GetString("source")
	targets, _ := cmd.Flags().GetStringSlice("targets")
	return bulkImpl(configPath, noFetch, format, source, targets)
}

// show displays branch divergence for a single repo
func show(configPath string, noFetch bool, format, repoName, source string) error {
	return showImpl(configPath, noFetch, format, repoName, source)
}
