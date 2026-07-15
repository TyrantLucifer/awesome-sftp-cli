package tui

import (
	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
)

type TCellSurface struct {
	screen tcell.Screen
}

func NewTCellSurface(screen tcell.Screen) *TCellSurface {
	return &TCellSurface{screen: screen}
}

func (s *TCellSurface) Size() (int, int) {
	return s.screen.Size()
}

func (s *TCellSurface) Clear() {
	s.screen.Clear()
}

func (s *TCellSurface) PutClipped(x, y, width int, text string, style CellStyle) {
	if width <= 0 {
		return
	}
	text = SanitizeTerminalText(text)
	cellStyle := tcellStyle(style)
	column := x
	boundary := x + width
	for text != "" && column < boundary {
		remainder, cellWidth := s.screen.Put(column, y, text, cellStyle)
		if cellWidth <= 0 || remainder == text {
			break
		}
		if column+cellWidth > boundary {
			s.screen.Put(column, y, " ", cellStyle)
			break
		}
		column += cellWidth
		text = remainder
	}
}

func TranslateTCellEvent(event tcell.Event, mode Mode) (Action, bool) {
	switch event := event.(type) {
	case *tcell.EventResize:
		width, height := event.Size()
		return Resize{Width: width, Height: height}, true
	case *tcell.EventKey:
		switch event.Key() {
		case tcell.KeyEnter:
			if mode == ModeAuth {
				return KeyPress{Key: KeySubmit}, true
			}
		case tcell.KeyTab:
			return KeyPress{Key: KeyTab}, true
		case tcell.KeyEscape:
			return KeyPress{Key: KeyEscape}, true
		case tcell.KeyBackspace:
			return KeyPress{Key: KeyBackspace}, true
		case tcell.KeyRune:
			if mode == ModeFilter || mode == ModeAuth {
				return TextInput{Text: event.Str()}, true
			}
			switch event.Str() {
			case "h":
				return KeyPress{Key: KeyParent}, true
			case "j":
				return KeyPress{Key: KeyDown}, true
			case "k":
				return KeyPress{Key: KeyUp}, true
			case "l":
				return KeyPress{Key: KeyOpen}, true
			case "v":
				return KeyPress{Key: KeyVisual}, true
			case "V":
				return KeyPress{Key: KeyVisualLine}, true
			case " ":
				return KeyPress{Key: KeyMark}, true
			case "/":
				return KeyPress{Key: KeyFilter}, true
			default:
				return TextInput{Text: event.Str()}, true
			}
		}
	}
	return nil, false
}

func tcellStyle(style CellStyle) tcell.Style {
	switch style {
	case StyleHeader:
		return tcell.StyleDefault.Bold(true)
	case StyleActiveHeader:
		return tcell.StyleDefault.Bold(true).Reverse(true)
	case StyleCursor:
		return tcell.StyleDefault.Reverse(true)
	case StyleSelected:
		return tcell.StyleDefault.Underline(true)
	case StyleStatus:
		return tcell.StyleDefault.Reverse(true)
	case StylePreview:
		return tcell.StyleDefault.Foreground(color.Silver)
	case StyleError:
		return tcell.StyleDefault.Foreground(color.Red)
	default:
		return tcell.StyleDefault
	}
}
