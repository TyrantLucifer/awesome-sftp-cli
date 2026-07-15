package tui

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
)

func TestVisibleWindowUsesBoundedOverscan(t *testing.T) {
	window := ComputeWindow(50_000, 25_000, 20, 3)
	if got := window.End - window.Start; got > 26 {
		t.Fatalf("window size = %d, want at most 26", got)
	}
	if window.VisibleStart > 25_000 || window.VisibleEnd <= 25_000 {
		t.Fatalf("cursor is outside visible window: %#v", window)
	}

	last := ComputeWindow(50_000, 49_999, 20, 3)
	if last.End != 50_000 || last.VisibleEnd != 50_000 {
		t.Fatalf("last window = %#v", last)
	}
}

func TestRendererVisitsOnlyWindowRowsForFiftyThousandEntries(t *testing.T) {
	model := modelWithEntryCount(t, 50_000)
	model.Panes[Left].Cursor = 25_000
	surface := newMemorySurface(80, 24)

	stats := Render(surface, model, RenderOptions{Overscan: 2})
	if stats.VisitedEntries > 2*(stats.ListRows+4) {
		t.Fatalf("visited entries = %d, list rows = %d", stats.VisitedEntries, stats.ListRows)
	}
	if strings.Contains(surface.String(), "entry-49999") {
		t.Fatal("renderer visited an off-window entry")
	}
}

func TestRendererSnapshotShowsTwoPanesFocusReadOnlyAndPartialState(t *testing.T) {
	model := testModel(t)
	left := model.Panes[Left]
	left.Listing.Partial = true
	model.Panes[Left] = left
	surface := newMemorySurface(48, 8)

	Render(surface, model, RenderOptions{Overscan: 1})
	got := surface.String()
	for _, want := range []string{"[left] /left", " right  /right", "> dir", "READ-ONLY", "partial"} {
		if !strings.Contains(got, want) {
			t.Fatalf("snapshot missing %q:\n%s", want, got)
		}
	}
}

func TestRenderPickerShowsChoicesSelectionAndSanitizedProblem(t *testing.T) {
	picker := NewPicker([]PickerChoice{
		{Kind: PickerWorkspace, Name: "recent", Problem: "corrupt\x1b[2Jstate"},
		{Kind: PickerHost, Name: "server"},
	})
	surface := newMemorySurface(48, 8)
	RenderPicker(surface, picker, "Choose a workspace or SSH host")
	got := surface.String()
	for _, want := range []string{"Open workspace or SSH host", "> workspace  recent", "host       server", "corrupt�[2Jstate", "Choose a workspace or SSH host"} {
		if !strings.Contains(got, want) {
			t.Fatalf("picker render missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\x1b") {
		t.Fatalf("picker render contains raw escape:\n%s", got)
	}
}

func TestRenderPickerStillExplainsManualEntryAtMinimumSize(t *testing.T) {
	picker := NewPicker(nil)
	surface := newMemorySurface(12, 3)
	RenderPicker(surface, picker, "")
	if got := surface.String(); !strings.Contains(got, "Host:") {
		t.Fatalf("minimum picker render = %q", got)
	}
}

func TestRendererUsesLocalFallbackForActivePaneWithoutDisplayName(t *testing.T) {
	model := modelWithEntryCount(t, 1)
	surface := newMemorySurface(40, 8)
	Render(surface, model, RenderOptions{Overscan: 1})
	if got := surface.String(); !strings.Contains(got, "[local] /left") {
		t.Fatalf("snapshot missing active local fallback:\n%s", got)
	}
}

func TestRendererMasksAuthenticationAnswer(t *testing.T) {
	model := testModel(t)
	model, _ = Reduce(model, AuthChallengeReceived{ChallengeID: "challenge-1", Endpoint: "work-host", Prompt: "Password:", Kind: "secret"})
	model, _ = Reduce(model, TextInput{Text: "stage1-secret-canary"})
	surface := newMemorySurface(64, 12)

	Render(surface, model, RenderOptions{Overscan: 1})
	got := surface.String()
	for _, want := range []string{"Authentication — work-host", "Password:", "••••"} {
		if !strings.Contains(got, want) {
			t.Fatalf("auth modal missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "stage1-secret-canary") {
		t.Fatalf("auth modal rendered plaintext secret:\n%s", got)
	}
}

func TestRendererShowsWorkspaceSaveModal(t *testing.T) {
	model := testModel(t)
	model, _ = Reduce(model, KeyPress{Key: KeySave})
	model, _ = Reduce(model, TextInput{Text: "release"})
	surface := newMemorySurface(64, 12)
	Render(surface, model, RenderOptions{Overscan: 1})
	got := surface.String()
	for _, want := range []string{"Save workspace", "Name: release", "[Enter] save  [Esc] cancel"} {
		if !strings.Contains(got, want) {
			t.Fatalf("workspace modal missing %q:\n%s", want, got)
		}
	}
}

func TestSanitizeTerminalTextRemovesControlsAndInvalidUTF8(t *testing.T) {
	got := SanitizeTerminalText("safe\x1b[31m\n\x00\xff")
	if !utf8.ValidString(got) {
		t.Fatalf("sanitized text is invalid UTF-8: %x", []byte(got))
	}
	for _, forbidden := range []string{"\x1b", "\n", "\x00"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("sanitized text %q contains %q", got, forbidden)
		}
	}
}

func BenchmarkRenderFiftyThousandEntries(b *testing.B) {
	model := modelWithEntryCount(b, 50_000)
	model.Panes[Left].Cursor = 25_000
	surface := newMemorySurface(120, 40)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		Render(surface, model, RenderOptions{Overscan: 2})
	}
}

func BenchmarkMoveFiftyThousandEntries(b *testing.B) {
	model := modelWithEntryCount(b, 50_000)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		model, _ = Reduce(model, KeyPress{Key: KeyDown})
	}
}

type testingTB interface {
	Helper()
	Fatal(...any)
}

func modelWithEntryCount(tb testingTB, count int) Model {
	tb.Helper()
	leftLocation, err := domain.NewLocation(leftEndpointID, "/left")
	if err != nil {
		tb.Fatal(err)
	}
	rightLocation, err := domain.NewLocation(rightEndpointID, "/right")
	if err != nil {
		tb.Fatal(err)
	}
	left := NewPaneState(domain.Endpoint{ID: leftEndpointID, Kind: domain.EndpointLocal}, leftLocation)
	right := NewPaneState(domain.Endpoint{ID: rightEndpointID, Kind: domain.EndpointLocal}, rightLocation)
	entries := make([]domain.Entry, count)
	for index := range entries {
		name := fmt.Sprintf("entry-%05d", index)
		entries[index] = testEntry(leftEndpointID, "/left/"+name, domain.EntryFile)
	}
	left = paneWithEntries(left, entries)
	return NewModel(left, right)
}

type memorySurface struct {
	width  int
	height int
	cells  [][]rune
}

func newMemorySurface(width, height int) *memorySurface {
	surface := &memorySurface{width: width, height: height}
	surface.Clear()
	return surface
}

func (s *memorySurface) Size() (int, int) { return s.width, s.height }

func (s *memorySurface) Clear() {
	s.cells = make([][]rune, s.height)
	for row := range s.cells {
		s.cells[row] = []rune(strings.Repeat(" ", s.width))
	}
}

func (s *memorySurface) PutClipped(x, y, width int, text string, _ CellStyle) {
	if y < 0 || y >= s.height || x < 0 || x >= s.width || width <= 0 {
		return
	}
	column := x
	for _, char := range text {
		if column >= x+width || column >= s.width {
			break
		}
		s.cells[y][column] = char
		column++
	}
}

func (s *memorySurface) String() string {
	rows := make([]string, len(s.cells))
	for index := range s.cells {
		rows[index] = strings.TrimRight(string(s.cells[index]), " ")
	}
	return strings.Join(rows, "\n")
}
