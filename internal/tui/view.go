package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/danwiseman-z/whiterabbit/internal/estimate"
	"github.com/danwiseman-z/whiterabbit/internal/gh"
)

// View renders the current screen.
func (m Model) View() string {
	if m.err != nil {
		return fmt.Sprintf("\n %s\n\n %s\n\n %s\n",
			titleStyle.Render("whiterabbit"),
			errStyle.Render(m.err.Error()),
			helpStyle.Render("q quit"))
	}
	switch m.state {
	case stateDetail:
		return m.detailView()
	case stateProjects:
		return m.projectsView()
	case stateForm:
		return m.formView()
	case statePicker:
		return m.pickerView()
	default:
		return m.dayView()
	}
}

func (m Model) header() string {
	label := m.day.Format("Mon 2 Jan 2006")
	switch daysFromToday := int(startOfDay(time.Now()).Sub(m.day).Hours() / 24); daysFromToday {
	case 0:
		label += "  (today)"
	case 1:
		label += "  (yesterday)"
	}
	left := titleStyle.Render("whiterabbit")
	right := label
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		gap = 1
	}
	return " " + left + strings.Repeat(" ", gap) + right
}

func (m Model) footer(help string) string {
	var lines []string
	for _, w := range m.warnings {
		lines = append(lines, " "+warnStyle.Render("! "+w))
	}
	if m.status != "" {
		lines = append(lines, " "+subtleStyle.Render(m.status))
	}
	lines = append(lines, " "+helpStyle.Render(help))
	return strings.Join(lines, "\n")
}

func (m Model) dayView() string {
	var b strings.Builder
	b.WriteString(m.header() + "\n\n")

	switch {
	case m.loading:
		b.WriteString(" " + m.spinner.View() + subtleStyle.Render("reading github...") + "\n")
	case len(m.cfg.Projects) == 0:
		b.WriteString(" " + subtleStyle.Render("No projects yet. Press a to add one.") + "\n")
	default:
		nameWidth := 12
		for _, r := range m.rows {
			if n := lipgloss.Width(r.Project.Name); n > nameWidth {
				nameWidth = n
			}
		}
		if nameWidth > 30 {
			nameWidth = 30
		}
		b.WriteString("   " + headerStyle.Render(pad("PROJECT", nameWidth)+"  "+padLeft("TIME", 7)+"   ACTIVITY") + "\n")
		for i, r := range m.rows {
			cursor := "   "
			name := pad(truncate(r.Project.Name, nameWidth), nameWidth)
			if i == m.cursor {
				cursor = " " + selectStyle.Render(">") + " "
				name = selectStyle.Render(name)
			}
			activity := estimate.Summary(r.Events)
			dur := estimate.FormatDuration(r.Dur)
			if r.Dur == 0 {
				dur = subtleStyle.Render(padLeft("-", 7))
				activity = subtleStyle.Render("no activity")
			} else {
				dur = padLeft(dur, 7)
			}
			line := cursor + name + "  " + dur + "   " + activity
			b.WriteString(truncate(line, m.width) + "\n")
		}
		b.WriteString("   " + subtleStyle.Render(strings.Repeat("-", nameWidth+9)) + "\n")
		b.WriteString("   " + pad(totalStyle.Render("total"), nameWidth) + "  " +
			totalStyle.Render(padLeft(estimate.FormatDuration(m.Total()), 7)) + "\n")
	}

	b.WriteString("\n")
	b.WriteString(m.footer("h/l day  H/L week  t today  enter detail  r refresh  p projects  a add  q quit"))
	return b.String()
}

func (m Model) detailView() string {
	if m.detail >= len(m.rows) {
		return m.dayView()
	}
	r := m.rows[m.detail]
	head := " " + titleStyle.Render(r.Project.Name) + subtleStyle.Render(
		fmt.Sprintf("  %s  ·  %s  ·  %d events",
			m.day.Format("Mon 2 Jan 2006"),
			estimate.FormatDuration(r.Dur),
			len(r.Events)))
	body := m.vp.View()
	if !m.vpReady {
		body = m.detailBody()
	}
	return head + "\n\n" + body + "\n" + m.footer("up/down scroll  esc back  q back")
}

// detailBody lists each inferred session and the events inside it.
func (m Model) detailBody() string {
	if m.detail >= len(m.rows) {
		return ""
	}
	r := m.rows[m.detail]
	if len(r.Sessions) == 0 {
		return " " + subtleStyle.Render("no activity in these repos on this day")
	}
	var b strings.Builder
	for i, s := range r.Sessions {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(" " + sessionStyle.Render(fmt.Sprintf("%s - %s",
			s.Start.Local().Format("15:04"), s.End.Local().Format("15:04"))) +
			subtleStyle.Render("  ~"+estimate.FormatDuration(s.Duration())) + "\n")
		for _, e := range s.Events {
			line := fmt.Sprintf("   %s  %s  %s  %s",
				subtleStyle.Render(e.Time.Local().Format("15:04")),
				kindStyle.Render(pad(string(e.Kind), 7)),
				shortRepo(e.Repo),
				e.Title)
			b.WriteString(truncate(line, m.width) + "\n")
		}
	}
	return b.String()
}

func (m Model) projectsView() string {
	var b strings.Builder
	b.WriteString(truncate(" "+titleStyle.Render("projects")+
		subtleStyle.Render("  "+m.cfg.Path()), m.width) + "\n\n")
	if len(m.cfg.Projects) == 0 {
		b.WriteString(" " + subtleStyle.Render("Nothing here yet. Press a to add a project.") + "\n")
	}
	for i, p := range m.cfg.Projects {
		cursor := "   "
		name := p.Name
		if i == m.projCursor {
			cursor = " " + selectStyle.Render(">") + " "
			name = selectStyle.Render(name)
		}
		repos := subtleStyle.Render(joinRepos(p.Repos))
		if len(p.Repos) == 0 {
			repos = warnStyle.Render("no repos linked")
		}
		b.WriteString(truncate(cursor+name+"  "+repos, m.width) + "\n")
	}
	b.WriteString("\n")
	if m.confirming && m.projCursor < len(m.cfg.Projects) {
		b.WriteString(" " + errStyle.Render(fmt.Sprintf("delete %q? y/n", m.cfg.Projects[m.projCursor].Name)) + "\n")
	}
	b.WriteString(m.footer("a add  e edit  d delete  esc back"))
	return b.String()
}

func (m Model) formView() string {
	title := "new project"
	if m.form.editing >= 0 {
		title = "edit project"
	}
	var b strings.Builder
	b.WriteString(" " + titleStyle.Render(title) + "\n\n")
	b.WriteString(" " + fieldLabel("name", m.form.focus == 0) + "\n  " + m.form.name.View() + "\n\n")

	b.WriteString(" " + fieldLabel(fmt.Sprintf("repos (%d)", len(m.form.repos)), m.form.focus == 1) + "\n")
	if len(m.form.repos) == 0 {
		b.WriteString("  " + subtleStyle.Render("none yet - press b to browse an account") + "\n")
	}
	for i, r := range m.form.repos {
		cursor := "  "
		line := r
		if m.form.focus == 1 && i == m.form.repoCur {
			cursor = selectStyle.Render(" >")
			line = selectStyle.Render(r)
		}
		b.WriteString(truncate(cursor+" "+line, m.width) + "\n")
	}
	b.WriteString("\n")
	if m.form.err != "" {
		b.WriteString(" " + errStyle.Render(m.form.err) + "\n\n")
	}
	help := "tab repos  ·  enter next  ·  ctrl+s save  ·  esc cancel"
	if m.form.focus == 1 {
		help = "b browse repos  ·  x remove  ·  tab name  ·  ctrl+s save  ·  esc cancel"
	}
	b.WriteString(" " + helpStyle.Render(help))
	return b.String()
}

// pickerView is the two-stage repo browser: pick an owner, then pick repos.
func (m Model) pickerView() string {
	var b strings.Builder
	name := strings.TrimSpace(m.form.name.Value())
	if name == "" {
		name = "this project"
	}
	b.WriteString(" " + titleStyle.Render("add repos") + subtleStyle.Render("  to "+name) + "\n\n")

	if m.picker.stage == 0 {
		b.WriteString(" " + headerStyle.Render("owner") + "\n  " + m.picker.owner.View() + "\n\n")
		owners := m.visibleOwners()
		switch {
		case len(m.owners) == 0:
			b.WriteString("  " + subtleStyle.Render("looking up your accounts...") + "\n")
		case len(owners) == 0:
			b.WriteString("  " + subtleStyle.Render("no known account matches - press enter to try it anyway") + "\n")
		}
		for i, o := range owners {
			cursor := "  "
			line := o
			if i == m.picker.ownerCur {
				cursor = selectStyle.Render(" >")
				line = selectStyle.Render(o)
			}
			b.WriteString(cursor + " " + line + "\n")
		}
		b.WriteString("\n")
		if m.picker.err != "" {
			b.WriteString(" " + errStyle.Render(m.picker.err) + "\n\n")
		}
		b.WriteString(" " + helpStyle.Render("enter list repos  ·  or type owner/repo to add it directly  ·  esc back"))
		return b.String()
	}

	if m.picker.loading {
		b.WriteString(" " + m.spinner.View() + subtleStyle.Render("listing "+m.picker.loadedOwner+"...") + "\n\n")
		b.WriteString(" " + helpStyle.Render("esc back"))
		return b.String()
	}

	visible := m.visibleRepos()
	b.WriteString(" " + titleStyle.Render(m.picker.loadedOwner) + subtleStyle.Render(fmt.Sprintf(
		"  %d repos  ·  %d selected", len(m.picker.repos), len(m.picker.selected))) + "\n\n")
	b.WriteString(" " + headerStyle.Render("filter") + "  " + m.picker.filter.View() + "\n\n")

	nameWidth := 10
	for _, i := range visible {
		if n := len(m.picker.repos[i].Name()); n > nameWidth {
			nameWidth = n
		}
	}
	if nameWidth > 40 {
		nameWidth = 40
	}

	rows := max(3, m.height-13)
	cursorIdx := -1
	if m.picker.cursor < len(visible) {
		cursorIdx = visible[m.picker.cursor]
	}
	start := windowStart(m.picker.cursor, len(visible), rows)
	end := min(len(visible), start+rows)
	if len(visible) == 0 {
		b.WriteString("  " + subtleStyle.Render("nothing matches that filter") + "\n")
	}
	for _, idx := range visible[start:end] {
		r := m.picker.repos[idx]
		mark := "[ ]"
		if m.picker.selected[r.NameWithOwner] {
			mark = selectStyle.Render("[x]")
		}
		cursor := "  "
		label := pad(truncate(r.Name(), nameWidth), nameWidth)
		if idx == cursorIdx {
			cursor = selectStyle.Render(" >")
			label = selectStyle.Render(label)
		}
		meta := kindStyle.Render(pad(repoTags(r), 9) + "  " + pad(ago(r.PushedAt), 9))
		line := cursor + " " + mark + " " + label + "  " + meta + "  " + subtleStyle.Render(r.Description)
		b.WriteString(truncate(line, m.width) + "\n")
	}
	if len(visible) > rows {
		b.WriteString("  " + subtleStyle.Render(fmt.Sprintf("showing %d-%d of %d", start+1, end, len(visible))) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(" " + helpStyle.Render("space toggle  ·  ctrl+a all  ·  enter done  ·  esc other owner  ·  type to filter"))
	return b.String()
}

// windowStart keeps the cursor inside a scrolling window of the given height.
func windowStart(cursor, count, height int) int {
	if count <= height || cursor < height/2 {
		return 0
	}
	start := cursor - height/2
	if start+height > count {
		start = count - height
	}
	return max(0, start)
}

func repoTags(r gh.Repo) string {
	switch {
	case r.IsArchived:
		return "archived"
	case r.IsFork:
		return "fork"
	case r.IsPrivate:
		return "private"
	default:
		return "public"
	}
}

// ago renders a coarse "how long since this was pushed".
func ago(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < 24*time.Hour:
		return "today"
	case d < 48*time.Hour:
		return "yesterday"
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("Jan 2006")
	}
}

func fieldLabel(s string, focused bool) string {
	if focused {
		return selectStyle.Render(s)
	}
	return headerStyle.Render(s)
}

func shortRepo(repo string) string {
	if i := strings.Index(repo, "/"); i >= 0 {
		return repo[i+1:]
	}
	return repo
}

func joinRepos(repos []string) string { return strings.Join(repos, ", ") }

func pad(s string, w int) string {
	if n := lipgloss.Width(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

func padLeft(s string, w int) string {
	if n := lipgloss.Width(s); n < w {
		return strings.Repeat(" ", w-n) + s
	}
	return s
}

func truncate(s string, w int) string {
	if w <= 0 || lipgloss.Width(s) <= w {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(w).Render(s)
}
