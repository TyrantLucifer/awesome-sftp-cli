package tui

import (
	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
)

// TCellTheme maps renderer-owned semantic roles to terminal styles. Keeping
// the palette in the adapter leaves the renderer deterministic and makes it
// possible to test layout without emitting terminal escape sequences.
type TCellTheme struct {
	styles [styleCount]tcell.Style
}

var defaultTCellTheme = newGraphiteTheme()

func newGraphiteTheme() TCellTheme {
	rgb := func(hex uint32) color.Color {
		return color.NewRGBColor(int32(hex>>16), int32(hex>>8&0xff), int32(hex&0xff))
	}

	canvas := rgb(0x0f141a)
	surface := rgb(0x171c24)
	elevated := rgb(0x1e2632)
	selection := rgb(0x25344a)
	inactiveSelection := rgb(0x1b222c)
	text := rgb(0xd8dee9)
	muted := rgb(0x7e8a9a)
	border := rgb(0x34404f)
	accent := rgb(0x7dcfff)
	success := rgb(0x9ece6a)
	warning := rgb(0xe0af68)
	danger := rgb(0xf7768e)
	symlink := rgb(0xbb9af7)

	base := tcell.StyleDefault.Foreground(text).Background(canvas)
	panel := tcell.StyleDefault.Foreground(text).Background(surface)
	modal := tcell.StyleDefault.Foreground(text).Background(elevated)

	theme := TCellTheme{}
	theme.styles[StyleCanvas] = base
	theme.styles[StylePlain] = base
	theme.styles[StyleMuted] = base.Foreground(muted)
	theme.styles[StyleHeader] = panel.Foreground(muted).Bold(true)
	theme.styles[StyleActiveHeader] = panel.Foreground(accent).Bold(true)
	theme.styles[StyleCursor] = base.Foreground(text).Background(selection).Bold(true)
	theme.styles[StyleInactiveCursor] = base.Foreground(text).Background(inactiveSelection)
	theme.styles[StyleSelected] = base.Foreground(accent).Background(inactiveSelection)
	theme.styles[StyleStatus] = panel.Foreground(text)
	theme.styles[StyleStatusAccent] = panel.Foreground(canvas).Background(accent).Bold(true)
	theme.styles[StylePreview] = panel.Foreground(text)
	theme.styles[StyleBorder] = base.Foreground(border)
	theme.styles[StyleDirectory] = base.Foreground(accent).Bold(true)
	theme.styles[StyleSymlink] = base.Foreground(symlink)
	theme.styles[StyleSuccess] = panel.Foreground(success).Bold(true)
	theme.styles[StyleWarning] = panel.Foreground(warning).Bold(true)
	theme.styles[StyleError] = panel.Foreground(danger).Bold(true)
	theme.styles[StyleModal] = modal
	theme.styles[StyleModalTitle] = modal.Foreground(accent).Bold(true)
	theme.styles[StyleModalMuted] = modal.Foreground(muted)
	theme.styles[StyleModalWarning] = modal.Foreground(warning).Bold(true)
	theme.styles[StyleModalError] = modal.Foreground(danger).Bold(true)
	theme.styles[StyleInput] = modal.Foreground(text)
	theme.styles[StyleTab] = panel.Foreground(muted)
	theme.styles[StyleActiveTab] = panel.Foreground(accent).Bold(true)
	return theme
}

func (t TCellTheme) style(role CellStyle) tcell.Style {
	if role >= styleCount {
		return t.styles[StylePlain]
	}
	return t.styles[role]
}
