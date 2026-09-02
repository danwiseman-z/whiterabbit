// Package gh talks to GitHub through the gh CLI, so whiterabbit inherits
// whatever authentication gh already has.
package gh

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Kind labels the sort of activity an Event records.
type Kind string

const (
	KindCommit        Kind = "commit"
	KindIssueComment  Kind = "comment"
	KindReviewComment Kind = "review"
	KindOpened        Kind = "opened"
	KindClosed        Kind = "closed"
	KindMerged        Kind = "merged"
)

// Event is a single timestamped thing the user did in a repo.
type Event struct {
	Time  time.Time
	Kind  Kind
	Repo  string
	Title string
	URL   string
}

// Client runs gh subcommands.
type Client struct {
	// Bin is the gh executable, "gh" by default.
	Bin string
	// Concurrency limits how many gh processes run at once.
	Concurrency int
}

// New returns a Client with sensible defaults.
func New() *Client { return &Client{Bin: "gh", Concurrency: 6} }

func (c *Client) bin() string {
	if c.Bin == "" {
		return "gh"
	}
	return c.Bin
}

// output runs a gh subcommand and returns its stdout, folding stderr into the
// error so the user sees what GitHub actually said.
func (c *Client) output(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.bin(), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		if i := strings.Index(msg, "\n"); i > 0 {
			msg = msg[:i]
		}
		return nil, fmt.Errorf("%s", msg)
	}
	return stdout.Bytes(), nil
}

// api runs `gh api <path>` and decodes the JSON response into out.
func (c *Client) api(ctx context.Context, path string, out any) error {
	data, err := c.output(ctx, "api", "-H", "Accept: application/vnd.github+json", path)
	if err != nil {
		return fmt.Errorf("gh api %s: %w", path, err)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decoding %s: %w", path, err)
	}
	return nil
}

// Repo is one repository offered by the picker.
type Repo struct {
	NameWithOwner string    `json:"nameWithOwner"`
	Description   string    `json:"description"`
	PushedAt      time.Time `json:"pushedAt"`
	IsPrivate     bool      `json:"isPrivate"`
	IsArchived    bool      `json:"isArchived"`
	IsFork        bool      `json:"isFork"`
}

// Name is the repo without its owner prefix.
func (r Repo) Name() string {
	if i := strings.Index(r.NameWithOwner, "/"); i >= 0 {
		return r.NameWithOwner[i+1:]
	}
	return r.NameWithOwner
}

// ListRepos returns the repos owned by owner, most recently pushed first. An
// empty owner means the authenticated user, which is the only way to see their
// private repos as well as public ones. gh works out whether the owner is a
// user or an organization.
func (c *Client) ListRepos(ctx context.Context, owner string, limit int) ([]Repo, error) {
	if limit <= 0 {
		limit = 300
	}
	args := []string{"repo", "list"}
	if owner != "" {
		args = append(args, owner)
	}
	args = append(args, "--limit", strconv.Itoa(limit),
		"--json", "nameWithOwner,description,pushedAt,isPrivate,isArchived,isFork")
	data, err := c.output(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("listing repos for %q: %w", ownerLabel(owner), err)
	}
	var repos []Repo
	if err := json.Unmarshal(data, &repos); err != nil {
		return nil, fmt.Errorf("decoding repo list: %w", err)
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].PushedAt.After(repos[j].PushedAt) })
	return repos, nil
}

func ownerLabel(owner string) string {
	if owner == "" {
		return "your account"
	}
	return owner
}

// Owners lists the account names worth browsing: the authenticated user first,
// then every organization they belong to.
func (c *Client) Owners(ctx context.Context, login string) ([]string, error) {
	var orgs []struct {
		Login string `json:"login"`
	}
	if err := c.api(ctx, "user/orgs?per_page=100", &orgs); err != nil {
		return nil, err
	}
	owners := make([]string, 0, len(orgs)+1)
	if login != "" {
		owners = append(owners, login)
	}
	for _, o := range orgs {
		if o.Login != login {
			owners = append(owners, o.Login)
		}
	}
	return owners, nil
}

// CheckAuth reports whether gh is installed and logged in.
func (c *Client) CheckAuth(ctx context.Context) error {
	if _, err := exec.LookPath(c.bin()); err != nil {
		return fmt.Errorf("the gh CLI was not found on PATH: install it from https://cli.github.com")
	}
	cmd := exec.CommandContext(ctx, c.bin(), "auth", "status")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gh is not authenticated: run `gh auth login`")
	}
	return nil
}

// Login returns the login of the authenticated user.
func (c *Client) Login(ctx context.Context) (string, error) {
	var user struct {
		Login string `json:"login"`
	}
	if err := c.api(ctx, "user", &user); err != nil {
		return "", err
	}
	if user.Login == "" {
		return "", fmt.Errorf("gh returned an empty login")
	}
	return user.Login, nil
}

// RepoResult is the activity found in one repo, or the error that stopped us.
type RepoResult struct {
	Repo   string
	Events []Event
	Err    error
}

// Activity fetches every event the login produced in the given repos between
// from (inclusive) and to (exclusive). Repos are queried in parallel and a
// failure in one repo does not stop the others.
func (c *Client) Activity(ctx context.Context, repos []string, login string, from, to time.Time) []RepoResult {
	limit := c.Concurrency
	if limit <= 0 {
		limit = 6
	}
	results := make([]RepoResult, len(repos))
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for i, repo := range repos {
		wg.Add(1)
		go func(i int, repo string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			events, err := c.RepoActivity(ctx, repo, login, from, to)
			results[i] = RepoResult{Repo: repo, Events: events, Err: err}
		}(i, repo)
	}
	wg.Wait()
	return results
}

// RepoActivity collects one repo's events for the window.
func (c *Client) RepoActivity(ctx context.Context, repo, login string, from, to time.Time) ([]Event, error) {
	since := from.UTC().Format(time.RFC3339)
	esc := url.PathEscape

	var events []Event
	inWindow := func(t time.Time) bool {
		return !t.Before(from) && t.Before(to)
	}

	// Commits authored by the user, on every branch they pushed to.
	commits, err := c.commits(ctx, repo, login, from, to)
	if err != nil {
		return nil, err
	}
	events = append(events, commits...)

	// Comments on issues and pull requests, and review comments on diffs.
	for _, src := range []struct {
		path string
		kind Kind
	}{
		{fmt.Sprintf("repos/%s/issues/comments?since=%s&per_page=100", repo, esc(since)), KindIssueComment},
		{fmt.Sprintf("repos/%s/pulls/comments?since=%s&per_page=100", repo, esc(since)), KindReviewComment},
	} {
		var comments []struct {
			CreatedAt time.Time `json:"created_at"`
			HTMLURL   string    `json:"html_url"`
			Body      string    `json:"body"`
			User      struct {
				Login string `json:"login"`
			} `json:"user"`
		}
		if err := c.api(ctx, src.path, &comments); err != nil {
			return nil, err
		}
		for _, cmt := range comments {
			if cmt.User.Login != login || !inWindow(cmt.CreatedAt) {
				continue
			}
			events = append(events, Event{
				Time:  cmt.CreatedAt,
				Kind:  src.kind,
				Repo:  repo,
				Title: firstLine(cmt.Body),
				URL:   cmt.HTMLURL,
			})
		}
	}

	// Issues and pull requests the user opened or closed.
	var issues []struct {
		Number    int        `json:"number"`
		Title     string     `json:"title"`
		HTMLURL   string     `json:"html_url"`
		CreatedAt time.Time  `json:"created_at"`
		ClosedAt  *time.Time `json:"closed_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		PullRequest *struct{} `json:"pull_request"`
	}
	path := fmt.Sprintf("repos/%s/issues?state=all&since=%s&per_page=100", repo, esc(since))
	if err := c.api(ctx, path, &issues); err != nil {
		return nil, err
	}
	for _, is := range issues {
		if is.User.Login != login {
			// Closing another person's issue still shows up as a merge or a
			// comment; only self-authored items are attributed here.
			continue
		}
		label := "issue"
		if is.PullRequest != nil {
			label = "PR"
		}
		title := fmt.Sprintf("%s #%d %s", label, is.Number, is.Title)
		if inWindow(is.CreatedAt) {
			events = append(events, Event{Time: is.CreatedAt, Kind: KindOpened, Repo: repo, Title: title, URL: is.HTMLURL})
		}
		if is.ClosedAt != nil && inWindow(*is.ClosedAt) {
			events = append(events, Event{Time: *is.ClosedAt, Kind: KindClosed, Repo: repo, Title: title, URL: is.HTMLURL})
		}
	}

	// Pull requests the user merged, including ones somebody else opened.
	var pulls []struct {
		Number   int        `json:"number"`
		Title    string     `json:"title"`
		HTMLURL  string     `json:"html_url"`
		MergedAt *time.Time `json:"merged_at"`
		MergedBy *struct {
			Login string `json:"login"`
		} `json:"merged_by"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	path = fmt.Sprintf("repos/%s/pulls?state=closed&sort=updated&direction=desc&per_page=100", repo)
	if err := c.api(ctx, path, &pulls); err != nil {
		return nil, err
	}
	for _, pr := range pulls {
		if pr.UpdatedAt.Before(from) {
			// The list is newest first, so everything past here is older.
			break
		}
		if pr.MergedAt == nil || !inWindow(*pr.MergedAt) {
			continue
		}
		if pr.MergedBy == nil || pr.MergedBy.Login != login {
			continue
		}
		events = append(events, Event{
			Time:  *pr.MergedAt,
			Kind:  KindMerged,
			Repo:  repo,
			Title: fmt.Sprintf("PR #%d %s", pr.Number, pr.Title),
			URL:   pr.HTMLURL,
		})
	}

	sort.Slice(events, func(i, j int) bool { return events[i].Time.Before(events[j].Time) })
	return events, nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:117] + "..."
	}
	if s == "" {
		s = "(no text)"
	}
	return s
}

// maxTips bounds how many branch heads one repo is willing to walk.
const maxTips = 25

// emptySHA is what a branch deletion reports as its resulting commit.
const emptySHA = "0000000000000000000000000000000000000000"

// commits collects the user's commits in the window across every branch they
// touched. GitHub's commit list only walks one branch at a time and defaults to
// the default branch, so work done on a feature branch that has not been merged
// yet is invisible unless we ask about that branch specifically.
func (c *Client) commits(ctx context.Context, repo, login string, from, to time.Time) ([]Event, error) {
	esc := url.PathEscape
	base := fmt.Sprintf("repos/%s/commits?author=%s&since=%s&until=%s&per_page=100",
		repo, esc(login), esc(from.UTC().Format(time.RFC3339)), esc(to.UTC().Format(time.RFC3339)))

	// The default branch first, then every other tip the user pushed to.
	paths := []string{base}
	for _, tip := range c.commitTips(ctx, repo, login, from) {
		paths = append(paths, base+"&sha="+esc(tip))
	}

	var events []Event
	seen := map[string]bool{}
	for i, path := range paths {
		var commits []struct {
			SHA     string `json:"sha"`
			HTMLURL string `json:"html_url"`
			Commit  struct {
				Message string `json:"message"`
				Author  struct {
					Date time.Time `json:"date"`
				} `json:"author"`
			} `json:"commit"`
		}
		if err := c.api(ctx, path, &commits); err != nil {
			// An empty repo answers 409, and a tip whose branch has since been
			// deleted answers 404. Neither should sink the whole repo.
			if i > 0 || strings.Contains(err.Error(), "Git Repository is empty") {
				continue
			}
			return nil, err
		}
		for _, cm := range commits {
			date := cm.Commit.Author.Date
			if seen[cm.SHA] || date.Before(from) || !date.Before(to) {
				continue
			}
			seen[cm.SHA] = true
			events = append(events, Event{
				Time:  date,
				Kind:  KindCommit,
				Repo:  repo,
				Title: firstLine(cm.Commit.Message),
				URL:   cm.HTMLURL,
			})
		}
	}
	return events, nil
}

// commitTips returns commit SHAs to walk back from, one per branch the user
// pushed to on or after `from`. It reads the repository activity feed, which
// records every push with its resulting SHA, so branches that were deleted
// after merging still resolve.
func (c *Client) commitTips(ctx context.Context, repo, login string, from time.Time) []string {
	const perPage = 100
	var acts []struct {
		After     string    `json:"after"`
		Ref       string    `json:"ref"`
		Timestamp time.Time `json:"timestamp"`
		Actor     struct {
			Login string `json:"login"`
		} `json:"actor"`
	}

	var tips []string
	seen := map[string]bool{}
	add := func(sha string) {
		if sha == "" || sha == emptySHA || seen[sha] || len(tips) >= maxTips {
			return
		}
		seen[sha] = true
		tips = append(tips, sha)
	}

	// The feed is newest first and uses opaque cursors rather than page
	// numbers, so we read one page and note whether it reached far enough back.
	reachedBack := false
	if err := c.api(ctx, fmt.Sprintf("repos/%s/activity?per_page=%d", repo, perPage), &acts); err != nil {
		// Activity is not readable everywhere; the default branch still works.
		return nil
	}
	for _, a := range acts {
		if a.Timestamp.Before(from) {
			reachedBack = true
			break
		}
		if a.Actor.Login == login {
			add(a.After)
		}
	}
	if len(acts) < perPage {
		reachedBack = true
	}
	if reachedBack {
		return tips
	}

	// The activity feed was too short to cover the day being asked about, so
	// fall back to walking the branches that exist right now.
	var branches []struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	if err := c.api(ctx, fmt.Sprintf("repos/%s/branches?per_page=100", repo), &branches); err != nil {
		return tips
	}
	for _, b := range branches {
		add(b.Commit.SHA)
	}
	return tips
}
