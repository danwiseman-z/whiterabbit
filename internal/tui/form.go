package tui

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/danwiseman-z/whiterabbit/internal/config"
	"github.com/danwiseman-z/whiterabbit/internal/gh"
)

// form edits one project: a name, and a list of repos built with the picker.
type form struct {
	name    textinput.Model
	repos   []string
	repoCur int
	focus   int // 0 = name, 1 = repo list
	editing int // index into cfg.Projects, or -1 when adding
	err     string
}

// picker browses an owner's repositories. Stage 0 chooses the owner, stage 1
// toggles repos on and off; selections survive switching owners, so one trip
// through the picker can pull repos from several accounts.
type picker struct {
	stage       int
	owner       textinput.Model
	filter      textinput.Model
	ownerCur    int
	loadedOwner string
	repos       []gh.Repo
	cursor      int
	selected    map[string]bool
	loading     bool
	err         string
}

type ownersMsg struct {
	owners []string
	err    error
}

type reposMsg struct {
	owner string
	repos []gh.Repo
	err   error
}

// openForm switches to the add/edit form. index -1 means a new project.
func (m Model) openForm(index int) Model {
	name := textinput.New()
	name.Prompt = ""
	name.CharLimit = 60
	name.Width = 48
	name.Placeholder = "project name"

	f := form{name: name, editing: index}
	if index >= 0 && index < len(m.cfg.Projects) {
		p := m.cfg.Projects[index]
		f.name.SetValue(p.Name)
		f.repos = append([]string(nil), p.Repos...)
	}
	f.name.Focus()
	m.form = f
	m.state = stateForm
	return m
}

func (m Model) handleFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.state = stateProjects
		return m, nil
	case "ctrl+s":
		return m.submitForm()
	case "tab", "shift+tab":
		return m.toggleFormFocus(), textinput.Blink
	}

	if m.form.focus == 0 {
		switch msg.String() {
		case "enter", "down":
			return m.toggleFormFocus(), nil
		}
		var cmd tea.Cmd
		m.form.name, cmd = m.form.name.Update(msg)
		return m, cmd
	}

	// The repo list has focus.
	switch msg.String() {
	case "up", "k":
		if m.form.repoCur > 0 {
			m.form.repoCur--
		} else {
			return m.toggleFormFocus(), textinput.Blink
		}
	case "down", "j":
		if m.form.repoCur < len(m.form.repos)-1 {
			m.form.repoCur++
		}
	case "x", "d", "delete", "backspace":
		if m.form.repoCur < len(m.form.repos) {
			m.form.repos = append(m.form.repos[:m.form.repoCur], m.form.repos[m.form.repoCur+1:]...)
			if m.form.repoCur >= len(m.form.repos) {
				m.form.repoCur = max(0, len(m.form.repos)-1)
			}
		}
	case "b", "enter", " ", "a":
		return m.openPicker()
	}
	return m, nil
}

func (m Model) toggleFormFocus() Model {
	m.form.focus = 1 - m.form.focus
	if m.form.focus == 0 {
		m.form.name.Focus()
	} else {
		m.form.name.Blur()
	}
	return m
}

func (m Model) submitForm() (tea.Model, tea.Cmd) {
	name := strings.TrimSpace(m.form.name.Value())
	if name == "" {
		m.form.err = "a project needs a name"
		m.form.focus = 0
		m.form.name.Focus()
		return m, textinput.Blink
	}
	p := config.Project{Name: name, Repos: config.NormalizeRepos(m.form.repos)}
	verb := "added"
	if m.form.editing >= 0 && m.form.editing < len(m.cfg.Projects) {
		m.cfg.Projects[m.form.editing] = p
		verb = "updated"
	} else {
		m.cfg.Projects = append(m.cfg.Projects, p)
		m.projCursor = len(m.cfg.Projects) - 1
	}
	m.state = stateProjects
	return m, m.saveAndReload(verb + " \"" + name + "\"")
}

// openPicker starts the repo browser, preselecting whatever the form already has.
func (m Model) openPicker() (tea.Model, tea.Cmd) {
	owner := textinput.New()
	owner.Prompt = ""
	owner.CharLimit = 100
	owner.Width = 40
	owner.Placeholder = "user or organization"
	owner.Focus()

	filter := textinput.New()
	filter.Prompt = ""
	filter.CharLimit = 60
	filter.Width = 40
	filter.Placeholder = "type to filter"

	selected := map[string]bool{}
	for _, r := range m.form.repos {
		selected[r] = true
	}
	m.picker = picker{owner: owner, filter: filter, selected: selected}
	m.state = statePicker

	cmds := []tea.Cmd{textinput.Blink}
	if len(m.owners) == 0 {
		cmds = append(cmds, m.fetchOwners())
	}
	return m, tea.Batch(cmds...)
}

func (m Model) fetchOwners() tea.Cmd {
	client, login := m.client, m.login
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		owners, err := client.Owners(ctx, login)
		return ownersMsg{owners: owners, err: err}
	}
}

func (m Model) fetchRepos(owner string) tea.Cmd {
	client, login := m.client, m.login
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		// An empty owner asks gh for the authenticated user's own repos, which
		// is the only listing that includes their private ones.
		lookup := owner
		if lookup == login {
			lookup = ""
		}
		repos, err := client.ListRepos(ctx, lookup, 300)
		return reposMsg{owner: owner, repos: repos, err: err}
	}
}

func (m Model) handlePickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.picker.stage == 0 {
		return m.handleOwnerKey(msg)
	}
	return m.handleRepoKey(msg)
}

func (m Model) handleOwnerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	owners := m.visibleOwners()
	switch msg.String() {
	case "esc":
		m.state = stateForm
		return m, nil
	case "up":
		if m.picker.ownerCur > 0 {
			m.picker.ownerCur--
		}
		return m, nil
	case "down":
		if m.picker.ownerCur < len(owners)-1 {
			m.picker.ownerCur++
		}
		return m, nil
	case "enter":
		typed := strings.TrimSpace(m.picker.owner.Value())
		// A full owner/repo pair is added directly, no browsing needed.
		if strings.Contains(typed, "/") {
			added := config.ParseRepos(typed)
			if len(added) == 0 {
				m.picker.err = "that does not look like owner/repo"
				return m, nil
			}
			for _, r := range added {
				m.picker.selected[r] = true
			}
			return m.applyPicker(), nil
		}
		owner := typed
		if len(owners) > 0 && m.picker.ownerCur < len(owners) {
			owner = owners[m.picker.ownerCur]
		}
		if owner == "" {
			owner = m.login
		}
		m.picker.stage = 1
		m.picker.owner.Blur()
		m.picker.filter.Focus()
		m.picker.loading = true
		m.picker.err = ""
		m.picker.loadedOwner = owner
		m.picker.repos = nil
		return m, tea.Batch(m.spinner.Tick, m.fetchRepos(owner))
	}

	var cmd tea.Cmd
	m.picker.owner, cmd = m.picker.owner.Update(msg)
	m.picker.ownerCur = 0
	m.picker.err = ""
	return m, cmd
}

func (m Model) handleRepoKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	visible := m.visibleRepos()
	switch msg.String() {
	case "esc":
		m.picker.stage = 0
		m.picker.filter.Blur()
		m.picker.owner.Focus()
		m.picker.err = ""
		return m, textinput.Blink
	case "enter":
		return m.applyPicker(), nil
	case "up":
		if m.picker.cursor > 0 {
			m.picker.cursor--
		}
		return m, nil
	case "down":
		if m.picker.cursor < len(visible)-1 {
			m.picker.cursor++
		}
		return m, nil
	case "pgup":
		m.picker.cursor = max(0, m.picker.cursor-10)
		return m, nil
	case "pgdown":
		m.picker.cursor = min(len(visible)-1, m.picker.cursor+10)
		return m, nil
	case " ":
		if m.picker.cursor < len(visible) {
			name := m.picker.repos[visible[m.picker.cursor]].NameWithOwner
			if m.picker.selected[name] {
				delete(m.picker.selected, name)
			} else {
				m.picker.selected[name] = true
			}
		}
		return m, nil
	case "ctrl+a":
		// Select every repo the filter is currently showing, or clear them all
		// if they are already selected.
		all := true
		for _, i := range visible {
			if !m.picker.selected[m.picker.repos[i].NameWithOwner] {
				all = false
				break
			}
		}
		for _, i := range visible {
			name := m.picker.repos[i].NameWithOwner
			if all {
				delete(m.picker.selected, name)
			} else {
				m.picker.selected[name] = true
			}
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.picker.filter, cmd = m.picker.filter.Update(msg)
	m.picker.cursor = 0
	return m, cmd
}

// applyPicker writes the selection back into the form and returns to it.
func (m Model) applyPicker() Model {
	seen := map[string]bool{}
	var out []string
	// Keep the order the form already had for repos that are still selected.
	for _, r := range m.form.repos {
		if m.picker.selected[r] && !seen[r] {
			out = append(out, r)
			seen[r] = true
		}
	}
	// Then newly picked repos, in the order the listing showed them.
	for _, r := range m.picker.repos {
		if m.picker.selected[r.NameWithOwner] && !seen[r.NameWithOwner] {
			out = append(out, r.NameWithOwner)
			seen[r.NameWithOwner] = true
		}
	}
	// Anything selected during an earlier owner's listing.
	var rest []string
	for r := range m.picker.selected {
		if !seen[r] {
			rest = append(rest, r)
		}
	}
	sort.Strings(rest)
	out = append(out, rest...)

	m.form.repos = out
	if m.form.repoCur >= len(out) {
		m.form.repoCur = max(0, len(out)-1)
	}
	m.form.focus = 1
	m.form.name.Blur()
	m.state = stateForm
	return m
}

// visibleOwners are the known owners matching what has been typed so far.
func (m Model) visibleOwners() []string {
	q := strings.ToLower(strings.TrimSpace(m.picker.owner.Value()))
	if q == "" {
		return m.owners
	}
	var out []string
	for _, o := range m.owners {
		if strings.Contains(strings.ToLower(o), q) {
			out = append(out, o)
		}
	}
	return out
}

// visibleRepos are indexes into picker.repos matching the filter.
func (m Model) visibleRepos() []int {
	q := strings.ToLower(strings.TrimSpace(m.picker.filter.Value()))
	out := make([]int, 0, len(m.picker.repos))
	for i, r := range m.picker.repos {
		if q == "" ||
			strings.Contains(strings.ToLower(r.NameWithOwner), q) ||
			strings.Contains(strings.ToLower(r.Description), q) {
			out = append(out, i)
		}
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
