package keymap

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

type Context string
type Action string

const (
	ContextNormal Context = "normal"
	ContextVisual Context = "visual"
)

const (
	ActionParent                Action = "parent"
	ActionDown                  Action = "down"
	ActionUp                    Action = "up"
	ActionBottom                Action = "bottom"
	ActionOpen                  Action = "open"
	ActionVisual                Action = "visual"
	ActionVisualLine            Action = "visual_line"
	ActionMark                  Action = "mark"
	ActionFilter                Action = "filter"
	ActionFilenameSearch        Action = "filename_search"
	ActionSave                  Action = "save"
	ActionSort                  Action = "sort"
	ActionToggleHidden          Action = "toggle_hidden"
	ActionRefresh               Action = "refresh"
	ActionPath                  Action = "path"
	ActionEndpoint              Action = "endpoint"
	ActionCopy                  Action = "copy"
	ActionCut                   Action = "cut"
	ActionPaste                 Action = "paste"
	ActionDelete                Action = "delete"
	ActionRename                Action = "rename"
	ActionRepeat                Action = "repeat"
	ActionEdit                  Action = "edit"
	ActionOpenExternal          Action = "open_external"
	ActionEditRecovery          Action = "edit_recovery"
	ActionCommand               Action = "command"
	ActionPreviewDrawer         Action = "preview_drawer"
	ActionJobs                  Action = "jobs"
	ActionLogDrawer             Action = "log_drawer"
	ActionJobPause              Action = "job_pause"
	ActionJobResume             Action = "job_resume"
	ActionJobCancel             Action = "job_cancel"
	ActionConflictOverwrite     Action = "conflict_overwrite"
	ActionConflictSkip          Action = "conflict_skip"
	ActionConflictAutoRename    Action = "conflict_auto_rename"
	ActionConflictOverwriteAll  Action = "conflict_overwrite_all"
	ActionConflictSkipAll       Action = "conflict_skip_all"
	ActionConflictAutoRenameAll Action = "conflict_auto_rename_all"
)

type Override struct {
	Context Context `json:"context"`
	Input   string  `json:"input"`
	Action  Action  `json:"action"`
}

type binding struct {
	input      string
	action     Action
	remappable bool
}

var defaults = []binding{
	{input: "A", action: ActionConflictAutoRenameAll},
	{input: "C", action: ActionJobCancel},
	{input: "D", action: ActionDelete},
	{input: "E", action: ActionEditRecovery, remappable: true},
	{input: "G", action: ActionBottom},
	{input: "H", action: ActionToggleHidden, remappable: true},
	{input: "J", action: ActionJobs, remappable: true},
	{input: "K", action: ActionPreviewDrawer, remappable: true},
	{input: "L", action: ActionLogDrawer, remappable: true},
	{input: "P", action: ActionJobPause},
	{input: "R", action: ActionRefresh, remappable: true},
	{input: "S", action: ActionSave, remappable: true},
	{input: "U", action: ActionJobResume},
	{input: "V", action: ActionVisualLine, remappable: true},
	{input: "W", action: ActionConflictOverwriteAll},
	{input: "X", action: ActionConflictSkipAll},
	{input: "!", action: ActionCommand},
	{input: ".", action: ActionRepeat},
	{input: "/", action: ActionFilter, remappable: true},
	{input: "a", action: ActionConflictAutoRename},
	{input: "c", action: ActionEndpoint, remappable: true},
	{input: "d", action: ActionCut, remappable: true},
	{input: "e", action: ActionEdit, remappable: true},
	{input: "f", action: ActionFilenameSearch, remappable: true},
	{input: "g", action: ActionPath},
	{input: "h", action: ActionParent, remappable: true},
	{input: "j", action: ActionDown, remappable: true},
	{input: "k", action: ActionUp, remappable: true},
	{input: "l", action: ActionOpen, remappable: true},
	{input: "o", action: ActionOpenExternal, remappable: true},
	{input: "p", action: ActionPaste, remappable: true},
	{input: "r", action: ActionRename, remappable: true},
	{input: "s", action: ActionSort, remappable: true},
	{input: "v", action: ActionVisual, remappable: true},
	{input: "w", action: ActionConflictOverwrite},
	{input: "x", action: ActionConflictSkip},
	{input: "y", action: ActionCopy, remappable: true},
	{input: " ", action: ActionMark, remappable: true},
}

type Map struct {
	contexts map[Context]map[string]Action
}

func New(overrides []Override) (Map, error) {
	normal := defaultContext()
	contexts := map[Context]map[string]Action{
		ContextNormal: normal,
		ContextVisual: cloneContext(normal),
	}
	seen := make(map[string]struct{}, len(overrides))
	for index, override := range overrides {
		mapping, ok := contexts[override.Context]
		if !ok {
			return Map{}, fmt.Errorf("keymap override %d context %q is unsupported", index, override.Context)
		}
		metadata, ok := defaultForAction(override.Action)
		if !ok {
			return Map{}, fmt.Errorf("keymap override %d action %q is unknown", index, override.Action)
		}
		if !metadata.remappable {
			return Map{}, fmt.Errorf("keymap override %d action %q is reserved", index, override.Action)
		}
		if override.Context == ContextNormal && override.Input == "q" {
			return Map{}, fmt.Errorf("keymap override %d input %q is reserved for the fixed normal-mode quit action", index, override.Input)
		}
		if err := validateInput(override.Input); err != nil {
			return Map{}, fmt.Errorf("keymap override %d: %w", index, err)
		}
		identity := string(override.Context) + "\x00" + string(override.Action)
		if _, duplicate := seen[identity]; duplicate {
			return Map{}, fmt.Errorf("keymap override %d duplicates action %q in context %q", index, override.Action, override.Context)
		}
		seen[identity] = struct{}{}
		if current, occupied := mapping[override.Input]; occupied && current != override.Action {
			return Map{}, fmt.Errorf("keymap override %d input %q conflicts with action %q", index, displayInput(override.Input), current)
		}
		for input, action := range mapping {
			if action == override.Action {
				delete(mapping, input)
				break
			}
		}
		mapping[override.Input] = override.Action
	}
	return Map{contexts: contexts}, nil
}

func Default() Map {
	mapping, err := New(nil)
	if err != nil {
		panic(err)
	}
	return mapping
}

func (m Map) Lookup(context Context, input string) (Action, bool) {
	mapping, ok := m.contexts[context]
	if !ok {
		return "", false
	}
	action, ok := mapping[input]
	return action, ok
}

// InputForAction returns the effective input assigned to one action in a context.
func (m Map) InputForAction(context Context, target Action) (string, bool) {
	mapping, ok := m.contexts[context]
	if !ok {
		return "", false
	}
	for input, action := range mapping {
		if action == target {
			return input, true
		}
	}
	return "", false
}

func DefaultSnapshotText() string {
	lines := make([]string, 0, len(defaults))
	for _, item := range defaults {
		status := "reserved"
		if item.remappable {
			status = "remappable"
		}
		lines = append(lines, fmt.Sprintf("normal %s %s %s", displayInput(item.input), item.action, status))
	}
	return strings.Join(lines, "\n")
}

func defaultContext() map[string]Action {
	result := make(map[string]Action, len(defaults))
	for _, item := range defaults {
		result[item.input] = item.action
	}
	return result
}

func cloneContext(input map[string]Action) map[string]Action {
	result := make(map[string]Action, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func defaultForAction(action Action) (binding, bool) {
	for _, item := range defaults {
		if item.action == action {
			return item, true
		}
	}
	return binding{}, false
}

func validateInput(input string) error {
	if !utf8.ValidString(input) || utf8.RuneCountInString(input) != 1 {
		return errorsForInput(input, "must be exactly one valid UTF-8 rune")
	}
	r, _ := utf8.DecodeRuneInString(input)
	if r < 0x20 && r != ' ' || r == 0x7f {
		return errorsForInput(input, "contains a control rune")
	}
	if r >= '0' && r <= '9' {
		return errorsForInput(input, "is reserved for Vim count prefixes")
	}
	if existing, ok := defaultForInput(input); ok && !existing.remappable {
		return errorsForInput(input, "is reserved by a dangerous or sequence action")
	}
	return nil
}

func errorsForInput(input, reason string) error {
	return fmt.Errorf("input %q %s", displayInput(input), reason)
}

func defaultForInput(input string) (binding, bool) {
	for _, item := range defaults {
		if item.input == input {
			return item, true
		}
	}
	return binding{}, false
}

func displayInput(input string) string {
	if input == " " {
		return "<space>"
	}
	return input
}
