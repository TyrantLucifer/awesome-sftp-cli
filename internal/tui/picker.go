package tui

import (
	"sort"
	"strings"
	"time"
)

type PickerKind string

const (
	PickerWorkspace  PickerKind = "workspace"
	PickerHost       PickerKind = "host"
	PickerManualHost PickerKind = "manual_host"
)

type PickerChoice struct {
	Kind    PickerKind
	Name    string
	Recent  time.Time
	Problem string
}

type Picker struct {
	choices  []PickerChoice
	visible  []PickerChoice
	query    string
	selected int
}

func NewPicker(choices []PickerChoice) Picker {
	picker := Picker{choices: append([]PickerChoice(nil), choices...)}
	picker.rebuild()
	return picker
}

func (p *Picker) SetQuery(query string) {
	p.query = query
	p.selected = 0
	p.rebuild()
}

func (p Picker) Query() string { return p.query }

func (p Picker) Visible() []PickerChoice {
	return append([]PickerChoice(nil), p.visible...)
}

func (p *Picker) Move(delta int) {
	if len(p.visible) == 0 {
		p.selected = 0
		return
	}
	p.selected = min(max(p.selected+delta, 0), len(p.visible)-1)
}

func (p Picker) Selected() (PickerChoice, bool) {
	if p.selected < 0 || p.selected >= len(p.visible) {
		return PickerChoice{}, false
	}
	return p.visible[p.selected], true
}

func (p Picker) SelectedIndex() int { return p.selected }

func (p *Picker) rebuild() {
	if p.query == "" {
		p.visible = append(p.visible[:0], p.choices...)
		sort.SliceStable(p.visible, func(left, right int) bool {
			leftChoice, rightChoice := p.visible[left], p.visible[right]
			if leftChoice.Kind != rightChoice.Kind {
				return leftChoice.Kind == PickerWorkspace
			}
			if leftChoice.Kind == PickerWorkspace && !leftChoice.Recent.Equal(rightChoice.Recent) {
				return leftChoice.Recent.After(rightChoice.Recent)
			}
			return leftChoice.Name < rightChoice.Name
		})
	} else {
		type rankedChoice struct {
			choice PickerChoice
			score  int
		}
		ranked := make([]rankedChoice, 0, len(p.choices))
		for _, choice := range p.choices {
			score, ok := fuzzyScore(choice.Name, p.query)
			if ok {
				ranked = append(ranked, rankedChoice{choice: choice, score: score})
			}
		}
		sort.SliceStable(ranked, func(left, right int) bool {
			if ranked[left].score != ranked[right].score {
				return ranked[left].score < ranked[right].score
			}
			return ranked[left].choice.Name < ranked[right].choice.Name
		})
		p.visible = p.visible[:0]
		p.visible = append(p.visible, PickerChoice{Kind: PickerManualHost, Name: p.query})
		for _, item := range ranked {
			p.visible = append(p.visible, item.choice)
		}
	}
	if len(p.visible) == 0 {
		p.selected = 0
	} else {
		p.selected = min(max(p.selected, 0), len(p.visible)-1)
	}
}

func fuzzyScore(value, query string) (int, bool) {
	value = strings.ToLower(value)
	query = strings.ToLower(query)
	if query == "" {
		return 0, true
	}
	if value == query {
		return 0, true
	}
	if strings.HasPrefix(value, query) {
		return 10 + len(value) - len(query), true
	}
	if index := strings.Index(value, query); index >= 0 {
		return 100 + index + len(value) - len(query), true
	}
	queryRunes := []rune(query)
	matched := 0
	first := -1
	last := -1
	for index, candidate := range []rune(value) {
		if matched >= len(queryRunes) || candidate != queryRunes[matched] {
			continue
		}
		if first < 0 {
			first = index
		}
		last = index
		matched++
	}
	if matched != len(queryRunes) {
		return 0, false
	}
	return 1000 + first + (last - first + 1 - len(queryRunes)), true
}
