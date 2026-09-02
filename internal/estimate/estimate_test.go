package estimate

import (
	"testing"
	"time"

	"github.com/danwiseman-z/whiterabbit/internal/config"
	"github.com/danwiseman-z/whiterabbit/internal/gh"
)

func settings() config.Settings {
	return config.Settings{GapMinutes: 45, LeadInMinutes: 20, MinSessionMinutes: 15, MaxSessionMinutes: 240}
}

func at(hour, min int) time.Time {
	return time.Date(2026, 9, 2, hour, min, 0, 0, time.UTC)
}

func events(times ...time.Time) []gh.Event {
	out := make([]gh.Event, 0, len(times))
	for _, t := range times {
		out = append(out, gh.Event{Time: t, Kind: gh.KindCommit, Repo: "o/r"})
	}
	return out
}

func TestSessionsGroupsByGap(t *testing.T) {
	// 09:00 and 09:30 are one sitting; 14:00 starts another.
	got := Sessions(events(at(9, 0), at(9, 30), at(14, 0)), settings())
	if len(got) != 2 {
		t.Fatalf("want 2 sessions, got %d", len(got))
	}
	if !got[0].Start.Equal(at(8, 40)) || !got[0].End.Equal(at(9, 30)) {
		t.Errorf("first session = %s-%s, want 08:40-09:30", got[0].Start, got[0].End)
	}
	if len(got[0].Events) != 2 || len(got[1].Events) != 1 {
		t.Errorf("events split %d/%d, want 2/1", len(got[0].Events), len(got[1].Events))
	}
	if want := 50 * time.Minute; got[0].Duration() != want {
		t.Errorf("first duration = %s, want %s", got[0].Duration(), want)
	}
}

func TestSessionsUnsortedInputAndCallerSliceUntouched(t *testing.T) {
	in := events(at(14, 0), at(9, 0), at(9, 30))
	got := Sessions(in, settings())
	if len(got) != 2 {
		t.Fatalf("want 2 sessions, got %d", len(got))
	}
	if !in[0].Time.Equal(at(14, 0)) {
		t.Errorf("caller slice was reordered: %s", in[0].Time)
	}
}

func TestSessionsLoneEventGetsLeadIn(t *testing.T) {
	got := Sessions(events(at(11, 0)), settings())
	if len(got) != 1 {
		t.Fatalf("want 1 session, got %d", len(got))
	}
	// Lead-in (20m) is longer than the 15m floor, so it wins.
	if want := 20 * time.Minute; got[0].Duration() != want {
		t.Errorf("duration = %s, want %s", got[0].Duration(), want)
	}
}

func TestSessionsHonourMinimum(t *testing.T) {
	st := settings()
	st.LeadInMinutes = 0
	got := Sessions(events(at(11, 0)), st)
	if want := 15 * time.Minute; got[0].Duration() != want {
		t.Errorf("duration = %s, want the %s floor", got[0].Duration(), want)
	}
}

func TestSessionsCapAtMaximum(t *testing.T) {
	var evs []gh.Event
	for h := 0; h < 12; h++ {
		evs = append(evs, events(at(h, 0), at(h, 30))...)
	}
	got := Sessions(evs, settings())
	if len(got) != 1 {
		t.Fatalf("want 1 long session, got %d", len(got))
	}
	if want := 240 * time.Minute; got[0].Duration() != want {
		t.Errorf("duration = %s, want the %s cap", got[0].Duration(), want)
	}
}

func TestSessionsDoNotOverlap(t *testing.T) {
	// The second sitting's lead-in would otherwise reach back into the first.
	st := settings()
	st.GapMinutes = 15
	got := Sessions(events(at(9, 0), at(9, 20)), st)
	if len(got) != 2 {
		t.Fatalf("want 2 sessions, got %d", len(got))
	}
	if got[1].Start.Before(got[0].End) {
		t.Errorf("session 2 starts at %s, before session 1 ends at %s", got[1].Start, got[0].End)
	}
}

func TestTotal(t *testing.T) {
	got := Total(Sessions(events(at(9, 0), at(9, 30), at(14, 0)), settings()))
	if want := 70 * time.Minute; got != want {
		t.Errorf("total = %s, want %s", got, want)
	}
}

func TestSummary(t *testing.T) {
	evs := []gh.Event{
		{Kind: gh.KindCommit}, {Kind: gh.KindCommit}, {Kind: gh.KindIssueComment},
	}
	if got, want := Summary(evs), "2 commits · 1 comment"; got != want {
		t.Errorf("Summary() = %q, want %q", got, want)
	}
	if got := Summary(nil); got != "" {
		t.Errorf("Summary(nil) = %q, want empty", got)
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "-"},
		{-time.Minute, "-"},
		{20 * time.Minute, "20m"},
		{62 * time.Minute, "1h"},
		{135 * time.Minute, "2h 15m"},
		{2 * time.Hour, "2h"},
	}
	for _, c := range cases {
		if got := FormatDuration(c.in); got != c.want {
			t.Errorf("FormatDuration(%s) = %q, want %q", c.in, got, c.want)
		}
	}
}
