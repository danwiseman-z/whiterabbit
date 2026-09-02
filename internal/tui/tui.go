// Package tui implements the whiterabbit terminal interface.
package tui

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/danwiseman-z/whiterabbit/internal/config"
	"github.com/danwiseman-z/whiterabbit/internal/estimate"
	"github.com/danwiseman-z/whiterabbit/internal/gh"
)

type state int

const (
	stateDay state = iota
	stateDetail
	stateProjects
	stateForm
	statePicker
)

// Row is one project's estimated activity for the selected day.
type Row struct {
	Project  config.Project
	Events   []gh.Event
	Sessions []estimate.Session
	Dur      time.Duration
}

type dayData struct {
	byRepo   map[string][]gh.Event
	warnings []string
}

type readyMsg struct {
	login string
	err   error
}

type dayMsg struct {
	key  string
	data *dayData
	err  error
}

// Model is the bubbletea model for the whole app.
type Model struct {
	cfg    *config.Config
	client *gh.Client
	login  string

	state         state
	day           time.Time
	width, height int

	loading  bool
	err      error
	status   string
	warnings []string

	rows       []Row
	cache      map[string]*dayData
	cursor     int
	projCursor int
	detail     int
	confirming bool

	spinner spinner.Model
	vp      viewport.Model
	vpReady bool
	form    form
	picker  picker
	owners  []string
}

// New builds the initial model for the given day.
func New(cfg *config.Config, client *gh.Client, day time.Time) Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = subtleStyle
	return Model{
		cfg:     cfg,
		client:  client,
		login:   cfg.User,
		day:     startOfDay(day),
		cache:   map[string]*dayData{},
		spinner: sp,
		width:   80,
		height:  24,
	}
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

func dayKey(t time.Time) string { return t.Format("2006-01-02") }

// Init kicks off the auth check and the first fetch.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.ready())
}

// ready verifies gh is usable and resolves the login to attribute work to.
func (m Model) ready() tea.Cmd {
	client, login := m.client, m.login
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := client.CheckAuth(ctx); err != nil {
			return readyMsg{err: err}
		}
		if login != "" {
			return readyMsg{login: login}
		}
		l, err := client.Login(ctx)
		return readyMsg{login: l, err: err}
	}
}

// fetch loads a day's activity for every repo referenced by any project.
func (m Model) fetch(day time.Time) tea.Cmd {
	client, login := m.client, m.login
	repos := m.cfg.Repos()
	key := dayKey(day)
	from := day
	to := day.AddDate(0, 0, 1)
	return func() tea.Msg {
		if len(repos) == 0 {
			return dayMsg{key: key, data: &dayData{byRepo: map[string][]gh.Event{}}}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		data := &dayData{byRepo: map[string][]gh.Event{}}
		for _, res := range client.Activity(ctx, repos, login, from, to) {
			if res.Err != nil {
				data.warnings = append(data.warnings, fmt.Sprintf("%s: %v", res.Repo, res.Err))
				continue
			}
			data.byRepo[res.Repo] = res.Events
		}
		return dayMsg{key: key, data: data}
	}
}

// load shows a cached day immediately, or starts a fetch for it.
func (m *Model) load(force bool) tea.Cmd {
	if m.login == "" {
		return nil
	}
	key := dayKey(m.day)
	if force {
		delete(m.cache, key)
	}
	if data, ok := m.cache[key]; ok {
		m.apply(data)
		return nil
	}
	m.loading = true
	m.rows = nil
	m.warnings = nil
	return tea.Batch(m.spinner.Tick, m.fetch(m.day))
}

// apply turns fetched events into per-project rows.
func (m *Model) apply(data *dayData) {
	m.warnings = data.warnings
	rows := make([]Row, 0, len(m.cfg.Projects))
	for _, p := range m.cfg.Projects {
		var events []gh.Event
		for _, repo := range p.Repos {
			events = append(events, data.byRepo[repo]...)
		}
		sessions := estimate.Sessions(events, m.cfg.Settings)
		sort.Slice(events, func(i, j int) bool { return events[i].Time.Before(events[j].Time) })
		rows = append(rows, Row{
			Project:  p,
			Events:   events,
			Sessions: sessions,
			Dur:      estimate.Total(sessions),
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Dur != rows[j].Dur {
			return rows[i].Dur > rows[j].Dur
		}
		return rows[i].Project.Name < rows[j].Project.Name
	})
	m.rows = rows
	if m.cursor >= len(rows) {
		m.cursor = max(0, len(rows)-1)
	}
}

// Total is the estimated time across every project for the day.
func (m Model) Total() time.Duration {
	var total time.Duration
	for _, r := range m.rows {
		total += r.Dur
	}
	return total
}

// Update handles messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.vp.Width = msg.Width
		m.vp.Height = max(3, msg.Height-6)
		m.vpReady = true
		if m.state == stateDetail {
			m.vp.SetContent(m.detailBody())
		}
		return m, nil

	case spinner.TickMsg:
		if !m.loading && !m.picker.loading {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case readyMsg:
		if msg.err != nil {
			m.err = msg.err
			m.loading = false
			return m, nil
		}
		m.login = msg.login
		if m.cfg.User == "" {
			m.cfg.User = msg.login
			_ = m.cfg.Save()
		}
		return m, m.load(false)

	case dayMsg:
		if msg.err != nil {
			m.err = msg.err
			m.loading = false
			return m, nil
		}
		m.cache[msg.key] = msg.data
		if msg.key == dayKey(m.day) {
			m.loading = false
			m.err = nil
			m.apply(msg.data)
		}
		return m, nil

	case ownersMsg:
		if msg.err != nil {
			m.picker.err = msg.err.Error()
			return m, nil
		}
		m.owners = msg.owners
		return m, nil

	case reposMsg:
		if m.state != statePicker || msg.owner != m.picker.loadedOwner {
			return m, nil
		}
		m.picker.loading = false
		if msg.err != nil {
			m.picker.err = msg.err.Error()
			m.picker.stage = 0
			return m, nil
		}
		m.picker.repos = msg.repos
		m.picker.cursor = 0
		m.picker.filter.SetValue("")
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.state {
	case stateForm:
		return m.handleFormKey(msg)
	case statePicker:
		return m.handlePickerKey(msg)
	}

	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "q":
		if m.state == stateDay {
			return m, tea.Quit
		}
	}

	switch m.state {
	case stateDay:
		return m.handleDayKey(msg)
	case stateDetail:
		return m.handleDetailKey(msg)
	case stateProjects:
		return m.handleProjectsKey(msg)
	}
	return m, nil
}

func (m Model) handleDayKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.status = ""
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.rows)-1 {
			m.cursor++
		}
	case "left", "h":
		m.day = m.day.AddDate(0, 0, -1)
		return m, m.load(false)
	case "right", "l":
		if !m.day.AddDate(0, 0, 1).After(startOfDay(time.Now())) {
			m.day = m.day.AddDate(0, 0, 1)
			return m, m.load(false)
		}
		m.status = "that day has not happened yet"
	case "H":
		m.day = m.day.AddDate(0, 0, -7)
		return m, m.load(false)
	case "L":
		next := m.day.AddDate(0, 0, 7)
		if next.After(startOfDay(time.Now())) {
			next = startOfDay(time.Now())
		}
		m.day = next
		return m, m.load(false)
	case "t":
		m.day = startOfDay(time.Now())
		return m, m.load(false)
	case "r":
		return m, m.load(true)
	case "p":
		m.state = stateProjects
		m.confirming = false
	case "a":
		return m.openForm(-1), textinput.Blink
	case "enter":
		if m.cursor < len(m.rows) && len(m.rows[m.cursor].Events) > 0 {
			m.detail = m.cursor
			m.state = stateDetail
			m.vp.Height = max(3, m.height-6)
			m.vp.Width = m.width
			m.vp.SetContent(m.detailBody())
			m.vp.GotoTop()
		}
	}
	return m, nil
}

func (m Model) handleDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "backspace", "q", "enter":
		m.state = stateDay
		return m, nil
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m Model) handleProjectsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.confirming {
		switch msg.String() {
		case "y", "Y":
			i := m.projCursor
			if i < len(m.cfg.Projects) {
				name := m.cfg.Projects[i].Name
				m.cfg.Projects = append(m.cfg.Projects[:i], m.cfg.Projects[i+1:]...)
				m.confirming = false
				if m.projCursor >= len(m.cfg.Projects) {
					m.projCursor = max(0, len(m.cfg.Projects)-1)
				}
				return m, m.saveAndReload(fmt.Sprintf("deleted %q", name))
			}
			m.confirming = false
		default:
			m.confirming = false
		}
		return m, nil
	}

	switch msg.String() {
	case "esc", "q", "p", "backspace":
		m.state = stateDay
	case "up", "k":
		if m.projCursor > 0 {
			m.projCursor--
		}
	case "down", "j":
		if m.projCursor < len(m.cfg.Projects)-1 {
			m.projCursor++
		}
	case "a":
		return m.openForm(-1), textinput.Blink
	case "e", "enter":
		if m.projCursor < len(m.cfg.Projects) {
			return m.openForm(m.projCursor), textinput.Blink
		}
	case "d":
		if m.projCursor < len(m.cfg.Projects) {
			m.confirming = true
		}
	}
	return m, nil
}

// saveAndReload persists the config and refetches, since the repo set changed.
func (m *Model) saveAndReload(status string) tea.Cmd {
	m.status = status
	if err := m.cfg.Save(); err != nil {
		m.err = err
		return nil
	}
	m.cache = map[string]*dayData{}
	return m.load(false)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
