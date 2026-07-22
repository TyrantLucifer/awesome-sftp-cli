package tui

import (
	"github.com/gdamore/tcell/v3"
)

type TCellSurface struct {
	screen tcell.Screen
	theme  *TCellTheme
}

func NewTCellSurface(screen tcell.Screen) *TCellSurface {
	return &TCellSurface{screen: screen, theme: &defaultTCellTheme}
}

func (s *TCellSurface) Size() (int, int) {
	return s.screen.Size()
}

func (s *TCellSurface) Clear() {
	s.screen.SetStyle(s.theme.style(StyleCanvas))
	s.screen.Clear()
}

func (s *TCellSurface) Fill(x, y, width int, style CellStyle) {
	screenWidth, screenHeight := s.screen.Size()
	if width <= 0 || y < 0 || y >= screenHeight || x >= screenWidth || x+width <= 0 {
		return
	}
	cellStyle := s.theme.style(style)
	for column := max(0, x); column < min(screenWidth, x+width); column++ {
		s.screen.SetContent(column, y, ' ', nil, cellStyle)
	}
}

func (s *TCellSurface) PutClipped(x, y, width int, text string, style CellStyle) {
	if width <= 0 {
		return
	}
	text = SanitizeTerminalText(text)
	cellStyle := s.theme.style(style)
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
	return TranslateTCellEventWithKeymap(event, mode, DefaultKeymap())
}

func TranslateTCellEventWithKeymap(event tcell.Event, mode Mode, keymap Keymap) (Action, bool) {
	switch event := event.(type) {
	case *tcell.EventResize:
		width, height := event.Size()
		return Resize{Width: width, Height: height}, true
	case *tcell.EventKey:
		switch event.Key() {
		case tcell.KeyEnter:
			if mode == ModeFilter || mode == ModeAuth || mode == ModeWorkspace || mode == ModePath || mode == ModeEndpoint || mode == ModeRename || mode == ModeMoveConfirm || mode == ModeDeleteConfirm || mode == ModeCommand || mode == ModeCommandConfirm || mode == ModeEditDecision || mode == ModeEditSaveAs || mode == ModeEditLaunchConfirm || mode == ModeEditRecovery || mode == ModeCacheClearConfirm || mode == ModeFilenameSearch || mode == ModeContentSearch || mode == ModeContentSearchConfirm {
				return KeyPress{Key: KeySubmit}, true
			}
		case tcell.KeyTab:
			return KeyPress{Key: KeyTab}, true
		case tcell.KeyEscape:
			return KeyPress{Key: KeyEscape}, true
		case tcell.KeyBackspace:
			return KeyPress{Key: KeyBackspace}, true
		case tcell.KeyDown:
			if mode == ModeNormal || mode == ModeVisual || mode == ModeFilter || mode == ModeEndpoint {
				return KeyPress{Key: KeyDown}, true
			}
		case tcell.KeyUp:
			if mode == ModeNormal || mode == ModeVisual || mode == ModeFilter || mode == ModeEndpoint {
				return KeyPress{Key: KeyUp}, true
			}
		case tcell.KeyLeft:
			if mode == ModeNormal || mode == ModeVisual {
				return KeyPress{Key: KeyParent}, true
			}
		case tcell.KeyRight:
			if mode == ModeNormal || mode == ModeVisual {
				return KeyPress{Key: KeyOpen}, true
			}
		case tcell.KeyRune:
			if mode == ModeEditRecovery {
				switch event.Str() {
				case "j":
					return KeyPress{Key: KeyDown}, true
				case "k":
					return KeyPress{Key: KeyUp}, true
				case "K":
					return KeyPress{Key: KeyPreviewDrawer}, true
				default:
					return nil, false
				}
			}
			if mode == ModeFilter || mode == ModeAuth || mode == ModeWorkspace || mode == ModePath || mode == ModeEndpoint || mode == ModeRename || mode == ModeCommand || mode == ModeEditSaveAs || mode == ModeFilenameSearch || mode == ModeContentSearch {
				return TextInput{Text: event.Str()}, true
			}
			if value := event.Str(); len(value) == 1 && value[0] >= '0' && value[0] <= '9' {
				return CountDigit{Digit: value[0] - '0'}, true
			}
			if key, ok := keymap.lookup(mode, event.Str()); ok {
				return KeyPress{Key: key}, true
			}
			return TextInput{Text: event.Str()}, true
		}
	}
	return nil, false
}
