package gh

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMatchStatus(t *testing.T) {
	wanted := []string{"In Progress", " doing "}
	for status, want := range map[string]bool{
		"In Progress": true,
		"in progress": true,
		"Doing":       true,
		"Todo":        false,
		"":            false,
	} {
		if got := MatchStatus(status, wanted); got != want {
			t.Errorf("MatchStatus(%q) = %v, want %v", status, got, want)
		}
	}
}

// fakeGH writes a script that stands in for gh, replying with body on stdout
// or failing with stderr when it is non-empty.
func fakeGH(t *testing.T, body, stderr string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake gh script needs a POSIX shell")
	}
	script := "#!/bin/sh\n"
	if stderr != "" {
		script += "echo '" + stderr + "' >&2\nexit 1\n"
	} else {
		script += "cat <<'JSON'\n" + body + "\nJSON\n"
	}
	path := filepath.Join(t.TempDir(), "gh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

const assignedFixture = `{"data":{"search":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[
  {"__typename":"Issue","number":12,"title":"Add projects view","url":"https://github.com/o/r/issues/12",
   "updatedAt":"2026-09-01T10:00:00Z","repository":{"nameWithOwner":"o/r"},
   "projectItems":{"nodes":[
     {"project":{"title":"Roadmap"},"fieldValueByName":{"name":"In Progress"}},
     {"project":{"title":"Backlog"},"fieldValueByName":{"name":"Todo"}}]}},
  {"__typename":"PullRequest","number":40,"title":"Fix paging","url":"https://github.com/o/api/pull/40",
   "isDraft":true,"updatedAt":"2026-09-02T10:00:00Z","repository":{"nameWithOwner":"o/api"},
   "projectItems":{"nodes":[{"project":{"title":"Roadmap"},"fieldValueByName":{"name":"in progress"}}]}},
  {"__typename":"Issue","number":7,"title":"No board","url":"https://github.com/o/r/issues/7",
   "updatedAt":"2026-09-02T10:00:00Z","repository":{"nameWithOwner":"o/r"},
   "projectItems":{"nodes":[{"project":{"title":"Roadmap"},"fieldValueByName":null}]}}
]}}}`

func TestInProgressFiltersByStatus(t *testing.T) {
	c := &Client{Bin: fakeGH(t, assignedFixture, "")}
	items, err := c.InProgress(context.Background(), []string{"In Progress"})
	if err != nil {
		t.Fatalf("InProgress() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2: %+v", len(items), items)
	}
	if items[0].Repo != "o/r" || items[0].Number != 12 || items[0].Project != "Roadmap" || items[0].IsPR {
		t.Errorf("first item = %+v", items[0])
	}
	if !items[1].IsPR || !items[1].IsDraft || items[1].Status != "in progress" {
		t.Errorf("second item = %+v", items[1])
	}
}

func TestInProgressReportsMissingScope(t *testing.T) {
	c := &Client{Bin: fakeGH(t, "", "gh: The projectItems field requires one of the following scopes: [read:project]")}
	if _, err := c.InProgress(context.Background(), []string{"In Progress"}); err != ErrProjectScope {
		t.Errorf("error = %v, want ErrProjectScope", err)
	}
}

func TestInProgressWithNoStatusesSkipsGH(t *testing.T) {
	c := &Client{Bin: "/definitely/not/a/real/gh"}
	items, err := c.InProgress(context.Background(), nil)
	if err != nil || items != nil {
		t.Errorf("got %v, %v; want nil, nil", items, err)
	}
}

func TestRef(t *testing.T) {
	issue := WorkItem{Repo: "o/api", Number: 12}
	pr := WorkItem{Repo: "o/api", Number: 40, IsPR: true}
	for got, want := range map[string]string{
		issue.Ref():      "#12",
		pr.Ref():         "PR #40",
		issue.RepoName(): "api",
	} {
		if got != want {
			t.Errorf("Ref = %q, want %q", got, want)
		}
	}
}
