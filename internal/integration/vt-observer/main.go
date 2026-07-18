package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/gdamore/tcell/v3/vt"
)

var synchronizedUpdateEnd = []byte("\x1b[?2026l")

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stderr))
}

func run(arguments []string, input io.Reader, stderr io.Writer) int {
	flags := flag.NewFlagSet("vt-observer", flag.ContinueOnError)
	flags.SetOutput(stderr)
	checkpoint := flags.Int("checkpoint", 0, "ignore synchronized frames ending before this byte offset")
	columns := flags.Int("columns", 200, "terminal columns")
	rows := flags.Int("rows", 30, "terminal rows")
	final := flags.Bool("final", false, "match only the final visible screen")
	absent := flags.String("absent", "", "pattern that must be absent from the final visible screen")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	patterns := flags.Args()
	if *checkpoint < 0 || *columns <= 0 || *rows <= 0 || len(patterns) == 0 {
		fmt.Fprintln(stderr, "checkpoint, terminal size, and at least one pattern are required")
		return 2
	}
	output, err := io.ReadAll(input)
	if err != nil {
		fmt.Fprintf(stderr, "read terminal stream: %v\n", err)
		return 2
	}
	var matched bool
	if *final {
		matched, err = observeFinal(output, *columns, *rows, patterns, *absent)
	} else {
		if *absent != "" {
			fmt.Fprintln(stderr, "absent requires final screen matching")
			return 2
		}
		matched, err = observe(output, *checkpoint, *columns, *rows, patterns)
	}
	if err != nil {
		fmt.Fprintf(stderr, "replay terminal stream: %v\n", err)
		return 2
	}
	if !matched {
		return 1
	}
	return 0
}

func observeFinal(output []byte, columns, rows int, patterns []string, absent string) (bool, error) {
	terminal := vt.NewMockTerm(vt.MockOptSize{X: vt.Col(columns), Y: vt.Row(rows)})
	if err := terminal.Start(); err != nil {
		return false, err
	}
	defer terminal.Stop()
	if _, err := terminal.Write(output); err != nil {
		return false, err
	}
	screen := visibleScreen(terminal, columns, rows)
	for _, pattern := range patterns {
		if !strings.Contains(screen, pattern) {
			return false, nil
		}
	}
	return absent == "" || !strings.Contains(screen, absent), nil
}

func observe(output []byte, checkpoint, columns, rows int, patterns []string) (bool, error) {
	terminal := vt.NewMockTerm(vt.MockOptSize{X: vt.Col(columns), Y: vt.Row(rows)})
	if err := terminal.Start(); err != nil {
		return false, err
	}
	defer terminal.Stop()

	observed := make(map[string]bool, len(patterns))
	offset := 0
	for offset < len(output) {
		relativeEnd := bytes.Index(output[offset:], synchronizedUpdateEnd)
		end := len(output)
		synchronized := false
		if relativeEnd >= 0 {
			end = offset + relativeEnd + len(synchronizedUpdateEnd)
			synchronized = true
		}
		if _, err := terminal.Write(output[offset:end]); err != nil {
			return false, err
		}
		offset = end
		if synchronized && offset >= checkpoint {
			recordVisiblePatterns(terminal, columns, rows, patterns, observed)
		}
	}
	if len(output) >= checkpoint {
		recordVisiblePatterns(terminal, columns, rows, patterns, observed)
	}
	return len(observed) == len(patterns), nil
}

func recordVisiblePatterns(terminal vt.MockTerm, columns, rows int, patterns []string, observed map[string]bool) {
	screen := visibleScreen(terminal, columns, rows)
	for _, pattern := range patterns {
		if strings.Contains(screen, pattern) {
			observed[pattern] = true
		}
	}
}

func visibleScreen(terminal vt.MockTerm, columns, rows int) string {
	var visible strings.Builder
	visible.Grow((columns + 1) * rows)
	for row := 0; row < rows; row++ {
		if row > 0 {
			visible.WriteByte('\n')
		}
		for column := 0; column < columns; column++ {
			cell := terminal.GetCell(vt.Coord{X: vt.Col(column), Y: vt.Row(row)})
			if cell.C == "" {
				visible.WriteByte(' ')
				continue
			}
			visible.WriteString(cell.C)
		}
	}
	return visible.String()
}
