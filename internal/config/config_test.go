package config

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseRepos(t *testing.T) {
	got := ParseRepos("owner/one, https://github.com/owner/two.git\n owner/one  github.com/owner/three/")
	want := []string{"owner/one", "owner/two", "owner/three"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseRepos() = %v, want %v", got, want)
	}
	if got := ParseRepos("  ,  "); len(got) != 0 {
		t.Errorf("ParseRepos(blank) = %v, want empty", got)
	}
}

func TestLoadMissingFileGivesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !reflect.DeepEqual(cfg.Settings, DefaultSettings()) {
		t.Errorf("settings = %+v, want defaults", cfg.Settings)
	}
	if len(cfg.Projects) != 0 {
		t.Errorf("projects = %v, want none", cfg.Projects)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.json")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	cfg.User = "octocat"
	cfg.Projects = []Project{{Name: "api", Repos: []string{"github.com/octocat/api", "octocat/api"}}}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("reload error = %v", err)
	}
	if got.User != "octocat" {
		t.Errorf("user = %q, want octocat", got.User)
	}
	want := []string{"octocat/api"}
	if !reflect.DeepEqual(got.Projects[0].Repos, want) {
		t.Errorf("repos = %v, want %v (normalized and de-duplicated)", got.Projects[0].Repos, want)
	}
}

func TestReposIsDeduplicatedAcrossProjects(t *testing.T) {
	cfg := &Config{Projects: []Project{
		{Name: "a", Repos: []string{"o/one", "o/two"}},
		{Name: "b", Repos: []string{"o/two", "o/three"}},
	}}
	want := []string{"o/one", "o/three", "o/two"}
	if got := cfg.Repos(); !reflect.DeepEqual(got, want) {
		t.Errorf("Repos() = %v, want %v", got, want)
	}
}

func TestNormalizeFillsZeroSettings(t *testing.T) {
	cfg := &Config{Settings: Settings{GapMinutes: 90}}
	cfg.normalize()
	if cfg.Settings.GapMinutes != 90 {
		t.Errorf("gap = %d, want the configured 90", cfg.Settings.GapMinutes)
	}
	if cfg.Settings.MinSessionMinutes != DefaultSettings().MinSessionMinutes {
		t.Errorf("min = %d, want default", cfg.Settings.MinSessionMinutes)
	}
}

func TestInProgressStatusesDefaultAndExplicitOff(t *testing.T) {
	cfg := &Config{}
	cfg.normalize()
	if !reflect.DeepEqual(cfg.Settings.InProgressStatuses, []string{"In Progress"}) {
		t.Errorf("missing statuses = %v, want the default", cfg.Settings.InProgressStatuses)
	}
	cfg = &Config{Settings: Settings{InProgressStatuses: []string{}}}
	cfg.normalize()
	if len(cfg.Settings.InProgressStatuses) != 0 {
		t.Errorf("explicit [] = %v, want it kept empty", cfg.Settings.InProgressStatuses)
	}
}
