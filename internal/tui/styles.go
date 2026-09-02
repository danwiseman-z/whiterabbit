package tui

import "github.com/charmbracelet/lipgloss"

var (
	accent = lipgloss.AdaptiveColor{Light: "#5B21B6", Dark: "#B39DFF"}
	muted  = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#8A8FA3"}
	warn   = lipgloss.AdaptiveColor{Light: "#B45309", Dark: "#F0B429"}
	danger = lipgloss.AdaptiveColor{Light: "#B91C1C", Dark: "#FF6B6B"}

	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(accent)
	subtleStyle  = lipgloss.NewStyle().Foreground(muted)
	headerStyle  = lipgloss.NewStyle().Bold(true).Foreground(muted)
	selectStyle  = lipgloss.NewStyle().Bold(true).Foreground(accent)
	totalStyle   = lipgloss.NewStyle().Bold(true)
	warnStyle    = lipgloss.NewStyle().Foreground(warn)
	errStyle     = lipgloss.NewStyle().Foreground(danger)
	helpStyle    = lipgloss.NewStyle().Foreground(muted)
	sessionStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)
	kindStyle    = lipgloss.NewStyle().Foreground(muted)
)
