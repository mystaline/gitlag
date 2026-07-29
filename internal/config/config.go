package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Offline       bool              `yaml:"offline"`
	CacheDir      string            `yaml:"cache_dir"`
	Gitea         GiteaConfig       `yaml:"gitea"`
	AI            AIConfig          `yaml:"ai"`
	Include       []string          `yaml:"include"`
	Exclude       []string          `yaml:"exclude"`
	BranchParents map[string]string `yaml:"branch_parents"`
}

type AIConfig struct {
	Provider string `yaml:"provider"` // anthropic | deepseek | kimi
	Model    string `yaml:"model"`
}

type GiteaConfig struct {
	URL     string        `yaml:"url"`
	Token   string        `yaml:"token"`
	Timeout time.Duration `yaml:"timeout"`
	Repos   []RepoConfig  `yaml:"repos"`
	// Deprecated: use Repos instead. Kept for backward compatibility.
	Orgs []string `yaml:"orgs"`
	Org  string   `yaml:"org"`
}

type RepoConfig struct {
	Org     string   `yaml:"org"`
	Include []string `yaml:"include"`
	Exclude []string `yaml:"exclude"`
}

func Load(path string) (*Config, error) {
	if path == "" {
		path = filepath.Join(os.ExpandEnv("$HOME"), ".gitlag.yaml")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return defaults(), nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	expanded := os.ExpandEnv(string(data))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	cfg.normalize()

	return &cfg, nil
}

func defaults() *Config {
	return &Config{
		Offline:       false,
		CacheDir:      filepath.Join(os.ExpandEnv("$HOME"), ".gitlag-cache"),
		Include:       []string{"*"},
		Exclude:       []string{},
		BranchParents: make(map[string]string),
	}
}

func (c *Config) normalize() {
	if c.CacheDir == "" {
		c.CacheDir = filepath.Join(os.ExpandEnv("$HOME"), ".gitlag-cache")
	}
	if c.Gitea.Timeout <= 0 {
		c.Gitea.Timeout = 30 * time.Second
	}
	c.CacheDir = strings.ReplaceAll(c.CacheDir, "~", os.ExpandEnv("$HOME"))

	if c.Include == nil {
		c.Include = []string{"*"}
	}
	if c.Exclude == nil {
		c.Exclude = []string{}
	}
	if c.BranchParents == nil {
		c.BranchParents = make(map[string]string)
	}

	// Backward compatibility: convert old format to new Repos format
	if len(c.Gitea.Repos) == 0 {
		// If old Orgs/Org fields are set, convert to Repos
		if len(c.Gitea.Orgs) > 0 {
			for _, org := range c.Gitea.Orgs {
				c.Gitea.Repos = append(c.Gitea.Repos, RepoConfig{
					Org:     org,
					Include: c.Include,
					Exclude: c.Exclude,
				})
			}
		} else if c.Gitea.Org != "" {
			c.Gitea.Repos = []RepoConfig{
				{
					Org:     c.Gitea.Org,
					Include: c.Include,
					Exclude: c.Exclude,
				},
			}
		}
	}

	// Normalize include/exclude for each repo if not specified
	for i := range c.Gitea.Repos {
		if len(c.Gitea.Repos[i].Include) == 0 {
			c.Gitea.Repos[i].Include = []string{"*"}
		}
		if c.Gitea.Repos[i].Exclude == nil {
			c.Gitea.Repos[i].Exclude = []string{}
		}
	}
}

func (c *Config) Validate() error {
	if !c.Offline {
		if c.Gitea.URL == "" {
			return fmt.Errorf("gitea.url required (unless offline: true)")
		}
		if len(c.Gitea.Repos) == 0 {
			return fmt.Errorf("gitea.repos required (unless offline: true)")
		}
		if c.Gitea.Token == "" {
			return fmt.Errorf("gitea.token required (unless offline: true)")
		}
	}
	return nil
}

// GetOrgs returns list of all configured organizations
func (c *Config) GetOrgs() []string {
	var orgs []string
	seen := make(map[string]bool)
	for _, repo := range c.Gitea.Repos {
		if !seen[repo.Org] {
			orgs = append(orgs, repo.Org)
			seen[repo.Org] = true
		}
	}
	return orgs
}
