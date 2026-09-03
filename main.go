// Command whiterabbit estimates time spent on projects from GitHub activity.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/danwiseman-z/whiterabbit/internal/config"
	"github.com/danwiseman-z/whiterabbit/internal/estimate"
	"github.com/danwiseman-z/whiterabbit/internal/gh"
	"github.com/danwiseman-z/whiterabbit/internal/tui"
)

// Build information, filled in by the linker at release time.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	var (
		dateFlag    = flag.String("date", "", "day to show, YYYY-MM-DD (default today)")
		configFlag  = flag.String("config", "", "path to config file")
		plainFlag   = flag.Bool("plain", false, "print the day's report and exit instead of starting the TUI")
		versionFlag = flag.Bool("version", false, "print the version and exit")
	)
	flag.Parse()

	if *versionFlag {
		fmt.Printf("whiterabbit %s (%s, built %s)\n", version, commit, date)
		return
	}

	if err := run(*dateFlag, *configFlag, *plainFlag); err != nil {
		fmt.Fprintln(os.Stderr, "whiterabbit:", err)
		os.Exit(1)
	}
}

func run(dateFlag, configFlag string, plain bool) error {
	day := time.Now()
	if dateFlag != "" {
		parsed, err := time.ParseInLocation("2006-01-02", dateFlag, time.Local)
		if err != nil {
			return fmt.Errorf("bad -date %q, want YYYY-MM-DD", dateFlag)
		}
		day = parsed
	}

	cfg, err := config.Load(configFlag)
	if err != nil {
		return err
	}
	client := gh.New()

	if plain {
		return report(cfg, client, day)
	}

	p := tea.NewProgram(tui.New(cfg, client, day), tea.WithAltScreen())
	_, err = p.Run()
	return err
}

// report prints a non-interactive summary for the day.
func report(cfg *config.Config, client *gh.Client, day time.Time) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	if err := client.CheckAuth(ctx); err != nil {
		return err
	}
	login := cfg.User
	if login == "" {
		l, err := client.Login(ctx)
		if err != nil {
			return err
		}
		login = l
	}
	if len(cfg.Projects) == 0 {
		return fmt.Errorf("no projects configured yet; run whiterabbit and press a to add one")
	}

	y, mo, d := day.Date()
	from := time.Date(y, mo, d, 0, 0, 0, 0, day.Location())
	to := from.AddDate(0, 0, 1)

	byRepo := map[string][]gh.Event{}
	for _, res := range client.Activity(ctx, cfg.Repos(), login, from, to) {
		if res.Err != nil {
			fmt.Fprintf(os.Stderr, "! %s: %v\n", res.Repo, res.Err)
			continue
		}
		byRepo[res.Repo] = res.Events
	}

	type line struct {
		name     string
		dur      time.Duration
		activity string
	}
	var lines []line
	var total time.Duration
	width := 7
	for _, p := range cfg.Projects {
		var events []gh.Event
		for _, repo := range p.Repos {
			events = append(events, byRepo[repo]...)
		}
		dur := estimate.Total(estimate.Sessions(events, cfg.Settings))
		total += dur
		if len(p.Name) > width {
			width = len(p.Name)
		}
		lines = append(lines, line{p.Name, dur, estimate.Summary(events)})
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].dur > lines[j].dur })

	fmt.Printf("%s\n\n", from.Format("Mon 2 Jan 2006"))
	for _, l := range lines {
		if l.dur == 0 {
			continue
		}
		fmt.Printf("  %-*s  %7s   %s\n", width, l.name, estimate.FormatDuration(l.dur), l.activity)
	}
	fmt.Printf("  %-*s  %7s\n", width, strings.Repeat("-", width), estimate.FormatDuration(total))

	if len(cfg.Settings.InProgressStatuses) > 0 {
		fmt.Println()
		printInProgress(cfg, client, ctx, width)
	}
	return nil
}

// printInProgress lists the open issues and PRs assigned to the user that sit
// in an in-progress column on a GitHub Project board.
func printInProgress(cfg *config.Config, client *gh.Client, ctx context.Context, width int) {
	items, err := client.InProgress(ctx, cfg.Settings.InProgressStatuses)
	if err != nil {
		fmt.Fprintf(os.Stderr, "! in progress: %v\n", err)
		return
	}
	fmt.Println("  in progress")
	if len(items) == 0 {
		fmt.Println("  (nothing assigned to you is in progress)")
		return
	}
	for _, it := range items {
		label := it.Repo
		for _, p := range cfg.Projects {
			if p.HasRepo(it.Repo) {
				label = p.Name
				break
			}
		}
		ref := it.Ref()
		if it.IsPR {
			ref = "PR " + ref
		}
		fmt.Printf("  %-*s  %-10s %s  (%s)\n", width, label, ref, it.Title, it.Project)
	}
}
