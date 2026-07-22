package tui

import "github.com/TyrantLucifer/awesome-sftp-cli/internal/keymap"

type Keymap struct {
	mapping    keymap.Map
	configured bool
}

func NewKeymap(overrides []keymap.Override) (Keymap, error) {
	mapping, err := keymap.New(overrides)
	if err != nil {
		return Keymap{}, err
	}
	return Keymap{mapping: mapping, configured: true}, nil
}

func DefaultKeymap() Keymap {
	return Keymap{mapping: keymap.Default()}
}

func (m Keymap) lookup(mode Mode, input string) (Key, bool) {
	context := keymap.ContextNormal
	if mode == ModeVisual || mode == ModeVisualLine {
		context = keymap.ContextVisual
	}
	action, ok := m.mapping.Lookup(context, input)
	return Key(action), ok
}

func (m Keymap) inputFor(mode Mode, action Key) (string, bool) {
	context := keymap.ContextNormal
	if mode == ModeVisual || mode == ModeVisualLine {
		context = keymap.ContextVisual
	}
	return m.mapping.InputForAction(context, keymap.Action(action))
}
