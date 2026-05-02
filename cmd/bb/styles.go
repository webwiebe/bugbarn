package main

import "github.com/charmbracelet/lipgloss"

var (
	primaryColor = lipgloss.Color("39")
	successColor = lipgloss.Color("82")
	warningColor = lipgloss.Color("214")
	errorColor   = lipgloss.Color("196")
	accentColor  = lipgloss.Color("86")

	borderColor = lipgloss.Color("240")
	selectedBg  = lipgloss.Color("237")
	titleColor  = lipgloss.Color("230")
	subtleColor = lipgloss.Color("243")
	dimColor    = lipgloss.Color("241")
	brightColor = lipgloss.Color("250")
	footerBg    = lipgloss.Color("235")

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(titleColor).
			Background(primaryColor).
			Padding(0, 1)

	headerStyle = lipgloss.NewStyle().
			Foreground(titleColor).
			Background(primaryColor).
			Padding(0, 2).
			Bold(true)

	footerStyle = lipgloss.NewStyle().
			Foreground(subtleColor).
			Background(footerBg).
			Padding(0, 2)

	selectedStyle = lipgloss.NewStyle().
			Foreground(titleColor).
			Background(selectedBg).
			Bold(true).
			PaddingLeft(1).
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(accentColor)

	normalStyle = lipgloss.NewStyle().PaddingLeft(2)

	statusUnresolved = lipgloss.NewStyle().Foreground(errorColor).Bold(true)
	statusRegressed  = lipgloss.NewStyle().Foreground(warningColor).Bold(true)
	statusResolved   = lipgloss.NewStyle().Foreground(successColor).Bold(true)
	statusMuted      = lipgloss.NewStyle().Foreground(subtleColor).Bold(true)

	countStyle   = lipgloss.NewStyle().Foreground(dimColor)
	projectStyle = lipgloss.NewStyle().Foreground(accentColor)
	timeStyle    = lipgloss.NewStyle().Foreground(subtleColor)

	helpKeyStyle = lipgloss.NewStyle().Foreground(accentColor).Bold(true)
	helpStyle    = lipgloss.NewStyle().Foreground(dimColor)
	helpSep      = lipgloss.NewStyle().Foreground(borderColor)
)

func statusStyle(status string) lipgloss.Style {
	switch status {
	case "unresolved":
		return statusUnresolved
	case "regressed":
		return statusRegressed
	case "resolved":
		return statusResolved
	case "muted":
		return statusMuted
	default:
		return lipgloss.NewStyle()
	}
}

func statusIcon(status string) string {
	switch status {
	case "unresolved":
		return "●"
	case "regressed":
		return "◆"
	case "resolved":
		return "✓"
	case "muted":
		return "○"
	default:
		return "?"
	}
}

func helpItem(key, desc string) string {
	return helpKeyStyle.Render(key) + helpSep.Render(" ") + helpStyle.Render(desc)
}
