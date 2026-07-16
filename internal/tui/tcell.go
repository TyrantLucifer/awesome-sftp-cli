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
			if mode == ModeAuth || mode == ModeWorkspace || mode == ModePath || mode == ModeEndpoint || mode == ModeRename || mode == ModeMoveConfirm || mode == ModeDeleteConfirm {
				return KeyPress{Key: KeySubmit}, true
			}
		case tcell.KeyTab:
			return KeyPress{Key: KeyTab}, true
		case tcell.KeyEscape:
			return KeyPress{Key: KeyEscape}, true
		case tcell.KeyBackspace:
			return KeyPress{Key: KeyBackspace}, true
		case tcell.KeyRune:
			if mode == ModeFilter || mode == ModeAuth || mode == ModeWorkspace || mode == ModePath || mode == ModeEndpoint || mode == ModeRename {
				return TextInput{Text: event.Str()}, true
			}
			if value := event.Str(); len(value) == 1 && value[0] >= '0' && value[0] <= '9' {
				return CountDigit{Digit: value[0] - '0'}, true
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
			case "S":
				return KeyPress{Key: KeySave}, true
			case "s":
				return KeyPress{Key: KeySort}, true
			case "H":
				return KeyPress{Key: KeyToggleHidden}, true
			case "R":
				return KeyPress{Key: KeyRefresh}, true
			case "g":
				return KeyPress{Key: KeyPath}, true
			case "c":
				return KeyPress{Key: KeyEndpoint}, true
			case "y":
				return KeyPress{Key: KeyCopy}, true
			case "d":
				return KeyPress{Key: KeyCut}, true
			case "D":
				return KeyPress{Key: KeyDelete}, true
			case "r":
				return KeyPress{Key: KeyRename}, true
			case ".":
				return KeyPress{Key: KeyRepeat}, true
			case "p":
				return KeyPress{Key: KeyPaste}, true
			case "K":
				return KeyPress{Key: KeyPreviewDrawer}, true
			case "J":
				return KeyPress{Key: KeyJobs}, true
			case "L":
				return KeyPress{Key: KeyLogDrawer}, true
			case "P":
				return KeyPress{Key: KeyJobPause}, true
			case "U":
				return KeyPress{Key: KeyJobResume}, true
			case "C":
				return KeyPress{Key: KeyJobCancel}, true
			case "w":
				return KeyPress{Key: KeyConflictOverwrite}, true
			case "x":
				return KeyPress{Key: KeyConflictSkip}, true
			case "a":
				return KeyPress{Key: KeyConflictAutoRename}, true
			case "W":
				return KeyPress{Key: KeyConflictOverwriteAll}, true
			case "X":
				return KeyPress{Key: KeyConflictSkipAll}, true
			case "A":
				return KeyPress{Key: KeyConflictAutoRenameAll}, true
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
