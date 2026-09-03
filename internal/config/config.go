// Package config handles loading and saving the whiterabbit config file.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Project is a named bucket of GitHub repos whose activity is tracked together.
type Project struct {
	Name  string   `json:"name"`
	Repos []string `json:"repos"`
}

// HasRepo reports whether the project tracks repo, ignoring case.
func (p Project) HasRepo(repo string) bool {
	for _, r := range p.Repos {
		if strings.EqualFold(r, repo) {
			return true
		}
	}
	return false
}

// Settings tune how raw GitHub events are turned into time estimates.
type Settings struct {
	// GapMinutes is the idle gap that splits one work session from the next.
	GapMinutes int `json:"gap_minutes"`
	// LeadInMinutes is credited before the first event of a session, since the
	// work happens before the commit or comment that records it.
	LeadInMinutes int `json:"lead_in_minutes"`
	// MinSessionMinutes is the floor for any session.
	MinSessionMinutes int `json:"min_session_minutes"`
	// MaxSessionMinutes caps a single session so one long-running day of
	// scattered events cannot report an absurd total.
	MaxSessionMinutes int `json:"max_session_minutes"`
	// InProgressStatuses are the GitHub Project status columns that count as
	// "in progress" for the assigned-to-me list on the day view. Matching
	// ignores case. An empty list turns the section off.
	InProgressStatuses []string `json:"in_progress_statuses"`
}

// Config is the whole on-disk state.
type Config struct {
	// User is the GitHub login to attribute activity to. Empty means "look it
	// up from gh on first use".
	User     string    `json:"user"`
	Projects []Project `json:"projects"`
	Settings Settings  `json:"settings"`

	path string
}

// DefaultSettings are used for a fresh config and to fill in zero values.
func DefaultSettings() Settings {
	return Settings{
		GapMinutes:         45,
		LeadInMinutes:      20,
		MinSessionMinutes:  15,
		MaxSessionMinutes:  240,
		InProgressStatuses: []string{"In Progress"},
	}
}

// DefaultPath is the config location, honouring WHITERABBIT_CONFIG and
// XDG_CONFIG_HOME.
func DefaultPath() (string, error) {
	if p := os.Getenv("WHITERABBIT_CONFIG"); p != "" {
		return p, nil
	}
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "whiterabbit", "config.json"), nil
}

// Load reads the config at path, or returns an empty default config if the
// file does not exist yet.
func Load(path string) (*Config, error) {
	if path == "" {
		p, err := DefaultPath()
		if err != nil {
			return nil, err
		}
		path = p
	}
	cfg := &Config{path: path, Settings: DefaultSettings()}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	cfg.path = path
	cfg.normalize()
	return cfg, nil
}

// Path is where this config lives on disk.
func (c *Config) Path() string { return c.path }

func (c *Config) normalize() {
	d := DefaultSettings()
	if c.Settings.GapMinutes <= 0 {
		c.Settings.GapMinutes = d.GapMinutes
	}
	if c.Settings.LeadInMinutes < 0 {
		c.Settings.LeadInMinutes = d.LeadInMinutes
	}
	if c.Settings.MinSessionMinutes <= 0 {
		c.Settings.MinSessionMinutes = d.MinSessionMinutes
	}
	if c.Settings.MaxSessionMinutes <= 0 {
		c.Settings.MaxSessionMinutes = d.MaxSessionMinutes
	}
	if c.Settings.InProgressStatuses == nil {
		// Absent from an older config file; an explicit [] is a deliberate off.
		c.Settings.InProgressStatuses = d.InProgressStatuses
	}
	for i := range c.Projects {
		c.Projects[i].Repos = NormalizeRepos(c.Projects[i].Repos)
	}
}

// Save atomically writes the config back to disk.
func (c *Config) Save() error {
	c.normalize()
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

// Repos is every distinct repo referenced by any project.
func (c *Config) Repos() []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range c.Projects {
		for _, r := range p.Repos {
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	sort.Strings(out)
	return out
}

// NormalizeRepos cleans up user-entered repo references: it trims whitespace,
// strips a github.com URL prefix and a trailing .git, drops empties and
// de-duplicates while preserving order.
func NormalizeRepos(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, r := range in {
		r = strings.TrimSpace(r)
		r = strings.TrimPrefix(r, "https://")
		r = strings.TrimPrefix(r, "http://")
		r = strings.TrimPrefix(r, "github.com/")
		r = strings.TrimSuffix(r, ".git")
		r = strings.Trim(r, "/")
		if r == "" || seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	return out
}

// ParseRepos splits a comma or whitespace separated list of repos.
func ParseRepos(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == '\n' || r == ' ' || r == '\t'
	})
	return NormalizeRepos(fields)
}
