package edit

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
)

const (
	ConflictViewInputByteLimit  = 32 * 1024
	ConflictViewOutputByteLimit = 64 * 1024
	ConflictViewLineLimit       = 512
)

// ConflictView is a bounded, display-only comparison. It cannot carry an
// upload decision and therefore cannot authorize a mutation.
type ConflictView struct {
	Text      string
	Summary   string
	Truncated bool
}

type ConflictViewRequest struct {
	Local             []byte
	Remote            []byte
	RemoteObservation RemoteObservation
	LocalTruncated    bool
	RemoteTruncated   bool
}

// BuildConflictView renders the effect of overwriting the observed remote
// object with the retained local edit. Inputs and output are independently
// bounded so a conflict cannot grow terminal memory without limit.
func BuildConflictView(request ConflictViewRequest) ConflictView {
	local, localBounded := boundConflictInput(request.Local)
	remote, remoteBounded := boundConflictInput(request.Remote)
	truncated := request.LocalTruncated || request.RemoteTruncated || localBounded || remoteBounded

	localLines, localLinesBounded := conflictLines(local)
	remoteLines, remoteLinesBounded := conflictLines(remote)
	truncated = truncated || localLinesBounded || remoteLinesBounded

	var output conflictViewWriter
	switch request.RemoteObservation.Status {
	case RemoteDeleted:
		output.write("--- remote (deleted)\n+++ local\n")
		writeAddedLines(&output, localLines)
	case RemotePresent:
		if request.RemoteObservation.Kind != domain.EntryFile {
			output.write(fmt.Sprintf("--- remote (replaced by %s)\n+++ local\n", request.RemoteObservation.Kind))
			writeAddedLines(&output, localLines)
		} else {
			output.write("--- remote\n+++ local\n")
			writeLineDiff(&output, remoteLines, localLines)
		}
	default:
		output.write(fmt.Sprintf("--- remote (%s)\n+++ local\n", request.RemoteObservation.Status))
		writeAddedLines(&output, localLines)
	}
	truncated = truncated || output.truncated
	summary := "remote → local conflict diff"
	if truncated {
		summary += " (bounded; additional content omitted)"
	}
	return ConflictView{Text: output.builder.String(), Summary: summary, Truncated: truncated}
}

func boundConflictInput(value []byte) ([]byte, bool) {
	if len(value) <= ConflictViewInputByteLimit {
		return value, false
	}
	return value[:ConflictViewInputByteLimit], true
}

func conflictLines(value []byte) ([]string, bool) {
	if !utf8.Valid(value) || bytes.IndexByte(value, 0) >= 0 {
		const bytesPerLine = 16
		limit := min(len(value), ConflictViewLineLimit*bytesPerLine)
		lines := make([]string, 0, (limit+bytesPerLine-1)/bytesPerLine)
		for offset := 0; offset < limit; offset += bytesPerLine {
			end := min(offset+bytesPerLine, limit)
			lines = append(lines, fmt.Sprintf("%08x  %s", offset, hex.EncodeToString(value[offset:end])))
		}
		return lines, limit < len(value)
	}
	lines := strings.Split(string(value), "\n")
	if len(lines) != 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) <= ConflictViewLineLimit {
		return lines, false
	}
	return lines[:ConflictViewLineLimit], true
}

func writeLineDiff(output *conflictViewWriter, remote, local []string) {
	prefix := 0
	for prefix < len(remote) && prefix < len(local) && remote[prefix] == local[prefix] {
		output.write(" " + remote[prefix] + "\n")
		prefix++
	}
	suffix := 0
	for suffix < len(remote)-prefix && suffix < len(local)-prefix && remote[len(remote)-1-suffix] == local[len(local)-1-suffix] {
		suffix++
	}
	for _, line := range remote[prefix : len(remote)-suffix] {
		output.write("-" + line + "\n")
	}
	for _, line := range local[prefix : len(local)-suffix] {
		output.write("+" + line + "\n")
	}
	for _, line := range remote[len(remote)-suffix:] {
		output.write(" " + line + "\n")
	}
}

func writeAddedLines(output *conflictViewWriter, lines []string) {
	for _, line := range lines {
		output.write("+" + line + "\n")
	}
}

type conflictViewWriter struct {
	builder   strings.Builder
	truncated bool
}

func (writer *conflictViewWriter) write(value string) {
	remaining := ConflictViewOutputByteLimit - writer.builder.Len()
	if remaining <= 0 {
		writer.truncated = true
		return
	}
	if len(value) > remaining {
		value = value[:remaining]
		writer.truncated = true
	}
	writer.builder.WriteString(value)
}
