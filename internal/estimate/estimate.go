// Package estimate turns a stream of GitHub events into rough working sessions.
//
// The model is deliberately crude: events that happen close together belong to
// one sitting, each sitting is credited a little lead-in time because the work
// precedes the commit that records it, and every sitting is clamped between a
// floor and a ceiling.
package estimate

import (
	"fmt"
	"sort"
	"time"

	"github.com/danwiseman-z/whiterabbit/internal/config"
	"github.com/danwiseman-z/whiterabbit/internal/gh"
)

// Session is one continuous stretch of work inferred from nearby events.
type Session struct {
	Start  time.Time
	End    time.Time
	Events []gh.Event
}

// Duration is the estimated length of the session.
func (s Session) Duration() time.Duration { return s.End.Sub(s.Start) }

// Sessions groups events into sittings using the given settings. The input is
// copied before sorting, so the caller's slice is left alone.
func Sessions(events []gh.Event, st config.Settings) []Session {
	if len(events) == 0 {
		return nil
	}
	sorted := make([]gh.Event, len(events))
	copy(sorted, events)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Time.Before(sorted[j].Time) })

	gap := time.Duration(st.GapMinutes) * time.Minute
	leadIn := time.Duration(st.LeadInMinutes) * time.Minute
	min := time.Duration(st.MinSessionMinutes) * time.Minute
	max := time.Duration(st.MaxSessionMinutes) * time.Minute

	var groups [][]gh.Event
	current := []gh.Event{sorted[0]}
	for _, e := range sorted[1:] {
		if e.Time.Sub(current[len(current)-1].Time) > gap {
			groups = append(groups, current)
			current = nil
		}
		current = append(current, e)
	}
	groups = append(groups, current)

	sessions := make([]Session, 0, len(groups))
	for _, g := range groups {
		first, last := g[0].Time, g[len(g)-1].Time
		start := first.Add(-leadIn)
		end := last
		if end.Sub(start) < min {
			end = start.Add(min)
		}
		if end.Sub(start) > max {
			start = end.Add(-max)
		}
		// Never let a padded session reach back into the previous one.
		if n := len(sessions); n > 0 && start.Before(sessions[n-1].End) {
			start = sessions[n-1].End
			if end.Before(start) {
				end = start
			}
		}
		sessions = append(sessions, Session{Start: start, End: end, Events: g})
	}
	return sessions
}

// Total is the summed duration of all sessions.
func Total(sessions []Session) time.Duration {
	var total time.Duration
	for _, s := range sessions {
		total += s.Duration()
	}
	return total
}

// Counts tallies events by kind.
func Counts(events []gh.Event) map[gh.Kind]int {
	counts := map[gh.Kind]int{}
	for _, e := range events {
		counts[e.Kind]++
	}
	return counts
}

// Summary renders event counts as "3 commits · 2 comments", in a stable order.
func Summary(events []gh.Event) string {
	counts := Counts(events)
	order := []struct {
		kind      gh.Kind
		one, many string
	}{
		{gh.KindCommit, "commit", "commits"},
		{gh.KindMerged, "merge", "merges"},
		{gh.KindOpened, "opened", "opened"},
		{gh.KindClosed, "closed", "closed"},
		{gh.KindIssueComment, "comment", "comments"},
		{gh.KindReviewComment, "review", "reviews"},
	}
	var parts []string
	for _, o := range order {
		n := counts[o.kind]
		if n == 0 {
			continue
		}
		word := o.many
		if n == 1 {
			word = o.one
		}
		parts = append(parts, fmt.Sprintf("%d %s", n, word))
	}
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += " · " + p
	}
	return out
}

// FormatDuration renders a duration as "2h 15m", rounded to five minutes.
func FormatDuration(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	d = d.Round(5 * time.Minute)
	h := int(d / time.Hour)
	m := int((d % time.Hour) / time.Minute)
	switch {
	case h == 0:
		return fmt.Sprintf("%dm", m)
	case m == 0:
		return fmt.Sprintf("%dh", h)
	default:
		return fmt.Sprintf("%dh %02dm", h, m)
	}
}
