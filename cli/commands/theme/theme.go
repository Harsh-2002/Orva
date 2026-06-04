// Package theme is the single source of truth for the Orva CLI's color
// palette. It wraps charmbracelet/lipgloss, which auto-degrades truecolor →
// 256 → 16 colors and adapts to light/dark terminal backgrounds, so the same
// style definitions render sensibly on any OS and terminal.
//
// The palette mirrors the dashboard brand (frontend/src/style.css): a purple
// primary, plus the conventional success/danger/warning/info semantics. The
// primary uses an AdaptiveColor so the deep brand purple (#553F83, readable on
// a light background) flips to the lighter link violet (#8b7bd8) on the dark
// terminals most operators use.
//
// This package must NOT import the commands package — commands imports theme,
// never the reverse.
package theme

import "github.com/charmbracelet/lipgloss"

// Styles is the resolved set of lipgloss styles the CLI renders through. When
// color is disabled every field is a plain pass-through style that emits no
// ANSI escapes, so callers never need to branch on color themselves — they
// always call s.Success.Render(...) and get the right thing.
type Styles struct {
	Primary  lipgloss.Style // brand accent (banners, prompts, headings)
	Success  lipgloss.Style // green — confirmations, ✓
	Error    lipgloss.Style // red — failures, ✗
	Warn     lipgloss.Style // amber — warnings
	Info     lipgloss.Style // sky — informational
	Muted    lipgloss.Style // dim — secondary text, thinking, hints
	Banner   lipgloss.Style // bold primary — the chat banner line
	Prompt   lipgloss.Style // the REPL input glyph
	DiffAdd  lipgloss.Style // green — added diff lines
	DiffDel  lipgloss.Style // red — removed diff lines
	DiffHunk lipgloss.Style // cyan — @@ hunk headers
	DiffMeta lipgloss.Style // bold — +++/--- file headers

	enabled bool
}

// Enabled reports whether the styles emit color.
func (s *Styles) Enabled() bool { return s.enabled }

// New builds the style set. When enabled is false every style is a no-op
// pass-through (no color, no bold), so the caller's existing color gate
// (--no-color / NO_COLOR / non-TTY) stays the single decision point.
func New(enabled bool) *Styles {
	if !enabled {
		plain := lipgloss.NewStyle()
		return &Styles{
			Primary:  plain,
			Success:  plain,
			Error:    plain,
			Warn:     plain,
			Info:     plain,
			Muted:    plain,
			Banner:   plain,
			Prompt:   plain,
			DiffAdd:  plain,
			DiffDel:  plain,
			DiffHunk: plain,
			DiffMeta: plain,
		}
	}

	var (
		primary = lipgloss.AdaptiveColor{Light: "#553F83", Dark: "#8b7bd8"}
		success = lipgloss.Color("#22c55e")
		danger  = lipgloss.Color("#ef4444")
		warn    = lipgloss.Color("#eab308")
		info    = lipgloss.Color("#38bdf8")
		muted   = lipgloss.AdaptiveColor{Light: "#6b7280", Dark: "#9aa0aa"}
		cyan    = lipgloss.Color("#22d3ee")
	)

	return &Styles{
		Primary:  lipgloss.NewStyle().Foreground(primary),
		Success:  lipgloss.NewStyle().Foreground(success),
		Error:    lipgloss.NewStyle().Foreground(danger),
		Warn:     lipgloss.NewStyle().Foreground(warn),
		Info:     lipgloss.NewStyle().Foreground(info),
		Muted:    lipgloss.NewStyle().Foreground(muted),
		Banner:   lipgloss.NewStyle().Foreground(primary).Bold(true),
		Prompt:   lipgloss.NewStyle().Foreground(primary).Bold(true),
		DiffAdd:  lipgloss.NewStyle().Foreground(success),
		DiffDel:  lipgloss.NewStyle().Foreground(danger),
		DiffHunk: lipgloss.NewStyle().Foreground(cyan),
		DiffMeta: lipgloss.NewStyle().Bold(true),
		enabled:  true,
	}
}
