package tui

import (
	"reflect"
	"testing"
	"time"
)

func TestPickerEmptyQuerySortsRecentWorkspacesThenHosts(t *testing.T) {
	base := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)
	picker := NewPicker([]PickerChoice{
		{Kind: PickerHost, Name: "z-host"},
		{Kind: PickerWorkspace, Name: "older", Recent: base},
		{Kind: PickerHost, Name: "a-host"},
		{Kind: PickerWorkspace, Name: "newer", Recent: base.Add(time.Hour)},
	})
	if got, want := pickerNames(picker.Visible()), []string{"newer", "older", "a-host", "z-host"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("empty picker order = %#v, want %#v", got, want)
	}
}

func TestPickerExplicitQueryUsesFuzzyRankAndManualAlias(t *testing.T) {
	picker := NewPicker([]PickerChoice{
		{Kind: PickerWorkspace, Name: "recent-release", Recent: time.Now().Add(time.Hour)},
		{Kind: PickerHost, Name: "work-primary"},
		{Kind: PickerHost, Name: "other-work"},
		{Kind: PickerHost, Name: "warehouse"},
	})
	picker.SetQuery("wrk")
	visible := picker.Visible()
	if len(visible) != 3 || visible[0] != (PickerChoice{Kind: PickerManualHost, Name: "wrk"}) || visible[1].Name != "work-primary" || visible[2].Name != "other-work" {
		t.Fatalf("fuzzy picker = %#v", visible)
	}
	if selected, ok := picker.Selected(); !ok || selected != visible[0] {
		t.Fatalf("selected = %#v, %v", selected, ok)
	}
	picker.Move(1)
	if selected, _ := picker.Selected(); selected.Name != "work-primary" {
		t.Fatalf("moved selection = %#v", selected)
	}
	picker.SetQuery("WORK-PRIMARY")
	visible = picker.Visible()
	if len(visible) < 2 || visible[1].Name != "work-primary" {
		t.Fatalf("case-insensitive exact match = %#v", visible)
	}
}

func TestPickerSelectionIsClampedAcrossFiltering(t *testing.T) {
	picker := NewPicker([]PickerChoice{{Kind: PickerHost, Name: "alpha"}, {Kind: PickerHost, Name: "beta"}})
	picker.Move(20)
	if selected, _ := picker.Selected(); selected.Name != "beta" {
		t.Fatalf("clamped selection = %#v", selected)
	}
	picker.SetQuery("zzz")
	visible := picker.Visible()
	if len(visible) != 1 || visible[0].Kind != PickerManualHost || visible[0].Name != "zzz" {
		t.Fatalf("manual-only picker = %#v", visible)
	}
	picker.SetQuery("")
	if selected, _ := picker.Selected(); selected.Name != "alpha" {
		t.Fatalf("reset selection = %#v", selected)
	}
}

func pickerNames(choices []PickerChoice) []string {
	names := make([]string, len(choices))
	for index, choice := range choices {
		names[index] = choice.Name
	}
	return names
}
