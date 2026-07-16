// Package preview provides dependency-free, bounded rendering for built-in previews.
package preview

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var errJSONDepthBudget = fmt.Errorf("JSON depth exceeds preview budget")

type Kind string

const (
	KindText     Kind = "text"
	KindCode     Kind = "code"
	KindJSON     Kind = "json"
	KindBinary   Kind = "binary"
	KindImage    Kind = "image"
	KindMetadata Kind = "metadata"
)

type ViewMode string

const (
	ViewAuto     ViewMode = "auto"
	ViewRawJSON  ViewMode = "raw_json"
	ViewMetadata ViewMode = "metadata"
)

// ToggleView is a pure, fixed-state view reducer. Raw JSON is omitted when the
// current source is not one complete JSON value.
func ToggleView(current ViewMode, rawJSONAvailable bool) ViewMode {
	switch current {
	case "", ViewAuto:
		if rawJSONAvailable {
			return ViewRawJSON
		}
		return ViewMetadata
	case ViewRawJSON:
		return ViewMetadata
	case ViewMetadata:
		return ViewAuto
	default:
		return ViewAuto
	}
}

type Limits struct {
	MaxInputBytes    int
	MaxJSONBytes     int
	MaxJSONDepth     int
	MaxRenderedLines int
	MaxOutputBytes   int
	MaxImagePixels   uint64
	MaxStyleSpans    int
}

func DefaultLimits() Limits {
	return Limits{
		MaxInputBytes: 512 * 1024, MaxJSONBytes: 256 * 1024, MaxJSONDepth: 64,
		MaxRenderedLines: 10_000, MaxOutputBytes: 512 * 1024, MaxImagePixels: 40_000_000,
		MaxStyleSpans: 4096,
	}
}

type Request struct {
	Path string
	Data []byte
	View ViewMode

	Offset   uint64
	Complete bool

	HasFileSize bool
	FileSize    uint64
}

type ImageMetadata struct {
	MediaType string
	Width     int
	Height    int
}

type ContentMetadata struct {
	MediaType   string
	RangeStart  uint64
	RangeEnd    uint64
	HasFileSize bool
	FileSize    uint64
	Complete    bool
	Width       int
	Height      int
}

type SyntaxClass string

const (
	SyntaxPlain   SyntaxClass = "plain"
	SyntaxKeyword SyntaxClass = "keyword"
	SyntaxString  SyntaxClass = "string"
	SyntaxNumber  SyntaxClass = "number"
	SyntaxComment SyntaxClass = "comment"
)

// StyleSpan references byte offsets in Result.Text. It never carries terminal
// escape sequences; the terminal adapter chooses its own safe style API.
type StyleSpan struct {
	Start int
	End   int
	Class SyntaxClass
}

type Result struct {
	Kind            Kind
	View            ViewMode
	Text            string
	Summary         string
	Warning         string
	InputBytes      int
	Lines           int
	Partial         bool
	Truncated       bool
	Binary          bool
	Image           *ImageMetadata
	Metadata        *ContentMetadata
	Styles          []StyleSpan
	StylesTruncated bool
	DiscardedBytes  uint64
}

func Render(request Request, limits Limits) Result {
	if err := limits.validate(); err != nil {
		return Result{Kind: KindText, Warning: err.Error()}
	}
	view := request.View
	if view == "" {
		view = ViewAuto
	}
	if view != ViewAuto && view != ViewRawJSON && view != ViewMetadata {
		return Result{Kind: KindText, Warning: "unsupported preview view"}
	}
	if request.Complete && request.Offset != 0 {
		return Result{Kind: KindText, View: view, Warning: "complete preview must start at offset zero"}
	}
	data := request.Data
	if uint64(len(data)) > math.MaxUint64-request.Offset { //nolint:gosec // slice length is non-negative
		return Result{Kind: KindText, View: view, Warning: "preview source range overflows"}
	}
	sourceEnd := request.Offset + uint64(len(data)) //nolint:gosec // guarded above
	if request.HasFileSize && sourceEnd > request.FileSize {
		return Result{Kind: KindText, View: view, Warning: "preview source range exceeds file size"}
	}
	if request.Complete && request.HasFileSize && sourceEnd != request.FileSize {
		return Result{Kind: KindText, View: view, Warning: "complete preview does not match file size"}
	}
	truncated := false
	discarded := uint64(0)
	if len(data) > limits.MaxInputBytes {
		discarded = uint64(len(data) - limits.MaxInputBytes) //nolint:gosec // non-negative slice lengths
		data = data[:limits.MaxInputBytes]
		truncated = true
	}
	rangeEnd := request.Offset + uint64(len(data)) //nolint:gosec // guarded above
	partial := !request.Complete || truncated
	result := Result{View: view, InputBytes: len(data), Partial: partial, Truncated: truncated, DiscardedBytes: discarded}
	if partial {
		result.Summary = fmt.Sprintf("partial preview: bytes %d..%d", request.Offset, rangeEnd)
		if truncated {
			result.Summary += fmt.Sprintf(" (%d bytes discarded; preview budget reached)", discarded)
		}
	}

	imageResult, imageOK := renderImage(request.Path, data, limits)
	if view == ViewMetadata {
		return renderMetadataResult(result, request, data, rangeEnd, imageResult, imageOK, limits)
	}
	if imageOK {
		imageResult.InputBytes = result.InputBytes
		imageResult.Partial = result.Partial
		imageResult.Truncated = result.Truncated
		imageResult.Summary = result.Summary
		imageResult.View = view
		imageResult.DiscardedBytes = discarded
		if view == ViewRawJSON {
			imageResult.Warning = "raw JSON view unavailable; showing image fallback"
		}
		if len(imageResult.Text) > limits.MaxOutputBytes {
			imageResult.Text = imageResult.Text[:limits.MaxOutputBytes]
			imageResult.Truncated = true
		}
		return imageResult
	}
	if !partial && looksLikeJSON(request.Path, data) {
		switch {
		case len(data) > limits.MaxJSONBytes:
			result.Warning = "JSON exceeds preview parse budget; showing text fallback"
		default:
			if view == ViewRawJSON {
				if err := scanJSON(data, limits.MaxJSONDepth); err == nil {
					result.Kind = KindJSON
					safe, sanitizedTruncated := sanitizeText(data, limits.MaxOutputBytes)
					result.Truncated = result.Truncated || sanitizedTruncated
					result.Text, result.Lines, result.Truncated = renderNumberedLines(safe, limits, result.Truncated)
					return result
				} else if err == errJSONDepthBudget {
					result.Warning = err.Error()
				} else {
					result.Warning = "invalid JSON; showing text fallback"
				}
				break
			}
			rendered, err := renderJSON(data, limits.MaxJSONDepth)
			switch err {
			case nil:
				result.Kind = KindJSON
				result.Text, result.Lines, result.Truncated = renderNumberedLines(rendered, limits, result.Truncated)
				return result
			case errJSONDepthBudget:
				result.Warning = err.Error()
			default:
				result.Warning = "invalid JSON; showing text fallback"
			}
		}
	}
	if looksBinary(data) {
		result.Kind = KindBinary
		result.Binary = true
		result.Text, result.Lines, result.Truncated = renderHex(data, request.Offset, limits, result.Truncated)
		return result
	}
	if view == ViewRawJSON && result.Warning == "" {
		result.Warning = "raw JSON view unavailable; showing text fallback"
	}
	if isCodePath(request.Path) {
		result.Kind = KindCode
	} else {
		result.Kind = KindText
	}
	safe, sanitizedTruncated := sanitizeText(data, limits.MaxOutputBytes)
	result.Truncated = result.Truncated || sanitizedTruncated
	result.Text, result.Lines, result.Truncated = renderNumberedLines(safe, limits, result.Truncated)
	if result.Kind == KindCode {
		result.Styles, result.StylesTruncated = highlightSyntax(result.Text, limits.MaxStyleSpans)
	}
	return result
}

func (limits Limits) validate() error {
	if limits.MaxInputBytes <= 0 || limits.MaxJSONBytes <= 0 || limits.MaxJSONDepth <= 0 || limits.MaxRenderedLines <= 0 || limits.MaxOutputBytes <= 0 || limits.MaxImagePixels == 0 || limits.MaxStyleSpans <= 0 {
		return fmt.Errorf("preview limits must all be positive")
	}
	return nil
}

func renderMetadataResult(base Result, request Request, data []byte, rangeEnd uint64, imageResult Result, imageOK bool, limits Limits) Result {
	mediaType := "text/plain"
	if imageOK && imageResult.Image != nil {
		mediaType = imageResult.Image.MediaType
	} else if looksLikeJSON(request.Path, data) {
		mediaType = "application/json"
	} else if looksBinary(data) {
		mediaType = "application/octet-stream"
	} else if isCodePath(request.Path) {
		mediaType = "text/x-source"
	}
	base.Kind = KindMetadata
	base.Metadata = &ContentMetadata{
		MediaType: mediaType, RangeStart: request.Offset, RangeEnd: rangeEnd,
		HasFileSize: request.HasFileSize, FileSize: request.FileSize, Complete: request.Complete && !base.Truncated,
	}
	if imageOK && imageResult.Image != nil {
		base.Metadata.Width = imageResult.Image.Width
		base.Metadata.Height = imageResult.Image.Height
	}
	status := "partial"
	if base.Metadata.Complete {
		status = "complete"
	}
	text := fmt.Sprintf("type: %s\nrange: %d..%d\ncaptured: %d bytes\nstatus: %s", mediaType, request.Offset, rangeEnd, len(data), status)
	if request.HasFileSize {
		text += fmt.Sprintf("\nsource size: %d bytes", request.FileSize)
	}
	if base.Metadata.Width != 0 && base.Metadata.Height != 0 {
		text += fmt.Sprintf("\ndimensions: %dx%d", base.Metadata.Width, base.Metadata.Height)
	}
	base.Text, base.Lines, base.Truncated = renderNumberedLines([]byte(text), limits, base.Truncated)
	return base
}

func renderImage(path string, data []byte, limits Limits) (Result, bool) {
	mediaType := http.DetectContentType(data)
	extensionType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if !strings.HasPrefix(mediaType, "image/") && !strings.HasPrefix(extensionType, "image/") {
		return Result{}, false
	}
	configuration, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || configuration.Width <= 0 || configuration.Height <= 0 {
		return Result{}, false
	}
	pixels := uint64(configuration.Width) * uint64(configuration.Height)
	result := Result{Kind: KindImage, Image: &ImageMetadata{MediaType: "image/" + format, Width: configuration.Width, Height: configuration.Height}}
	result.Text = fmt.Sprintf("image/%s  %dx%d  %d bytes", format, configuration.Width, configuration.Height, len(data))
	result.Lines = 1
	if pixels > limits.MaxImagePixels {
		result.Warning = "image dimensions exceed preview budget"
	}
	return result, true
}

func looksLikeJSON(path string, data []byte) bool {
	if strings.EqualFold(filepath.Ext(path), ".json") {
		return true
	}
	trimmed := bytes.TrimSpace(data)
	return len(trimmed) != 0 && (trimmed[0] == '{' || trimmed[0] == '[')
}

func isCodePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".c", ".cc", ".cpp", ".css", ".go", ".h", ".hpp", ".java", ".js", ".jsx", ".py", ".rb", ".rs", ".sh", ".ts", ".tsx", ".yaml", ".yml", ".zsh":
		return true
	default:
		return false
	}
}

var syntaxKeywords = map[string]struct{}{
	"break": {}, "case": {}, "class": {}, "const": {}, "continue": {}, "default": {},
	"defer": {}, "else": {}, "enum": {}, "false": {}, "for": {}, "func": {}, "function": {},
	"go": {}, "if": {}, "import": {}, "interface": {}, "let": {}, "map": {}, "nil": {},
	"null": {}, "package": {}, "range": {}, "return": {}, "select": {}, "struct": {}, "switch": {},
	"true": {}, "type": {}, "var": {}, "while": {},
}

func highlightSyntax(text string, maximum int) ([]StyleSpan, bool) {
	spans := make([]StyleSpan, 0, min(maximum, 64))
	appendSpan := func(start, end int, class SyntaxClass) bool {
		if len(spans) == maximum {
			return false
		}
		spans = append(spans, StyleSpan{Start: start, End: end, Class: class})
		return true
	}
	for index := 0; index < len(text); {
		switch {
		case text[index] == '\n' || text[index] == ' ' || text[index] == '\t':
			index++
		case text[index] == '/' && index+1 < len(text) && text[index+1] == '/':
			end := index + 2
			for end < len(text) && text[end] != '\n' {
				end++
			}
			if !appendSpan(index, end, SyntaxComment) {
				return spans, true
			}
			index = end
		case text[index] == '#':
			end := index + 1
			for end < len(text) && text[end] != '\n' {
				end++
			}
			if !appendSpan(index, end, SyntaxComment) {
				return spans, true
			}
			index = end
		case text[index] == '\'' || text[index] == '"' || text[index] == '`':
			quote := text[index]
			end := index + 1
			for end < len(text) && text[end] != '\n' {
				if text[end] == '\\' && end+1 < len(text) {
					end += 2
					continue
				}
				end++
				if text[end-1] == quote {
					break
				}
			}
			if !appendSpan(index, end, SyntaxString) {
				return spans, true
			}
			index = end
		case text[index] >= '0' && text[index] <= '9':
			end := index + 1
			for end < len(text) && ((text[end] >= '0' && text[end] <= '9') || text[end] == '.' || text[end] == '_') {
				end++
			}
			if !appendSpan(index, end, SyntaxNumber) {
				return spans, true
			}
			index = end
		case isASCIIIdentifierStart(text[index]):
			end := index + 1
			for end < len(text) && isASCIIIdentifierContinue(text[end]) {
				end++
			}
			if _, ok := syntaxKeywords[text[index:end]]; ok {
				if !appendSpan(index, end, SyntaxKeyword) {
					return spans, true
				}
			}
			index = end
		default:
			_, size := utf8.DecodeRuneInString(text[index:])
			index += size
		}
	}
	return spans, false
}

func isASCIIIdentifierStart(value byte) bool {
	return value == '_' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func isASCIIIdentifierContinue(value byte) bool {
	return isASCIIIdentifierStart(value) || value >= '0' && value <= '9'
}

func renderJSON(data []byte, maxDepth int) ([]byte, error) {
	if err := scanJSON(data, maxDepth); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("decode JSON trailing data")
	}
	rendered, err := json.MarshalIndent(value, "", "  ")
	return rendered, err
}

func scanJSON(data []byte, maxDepth int) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	depth := 0
	roots := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if delimiter, ok := token.(json.Delim); ok {
			switch delimiter {
			case '{', '[':
				if depth == 0 {
					roots++
				}
				depth++
				if depth > maxDepth {
					return errJSONDepthBudget
				}
			case '}', ']':
				depth--
			}
			continue
		}
		if depth == 0 {
			roots++
		}
	}
	if roots != 1 || depth != 0 {
		return fmt.Errorf("JSON must contain exactly one complete value")
	}
	return nil
}

func looksBinary(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	total := len(data)
	invalid := 0
	for len(data) != 0 {
		if data[0] == 0 {
			return true
		}
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 {
			invalid++
		}
		data = data[size:]
	}
	return invalid*20 > total
}

func sanitizeText(data []byte, maximum int) ([]byte, bool) {
	output := make([]byte, 0, min(maximum, len(data)))
	hexDigits := "0123456789abcdef"
	appendBytes := func(value ...byte) bool {
		if len(value) > maximum-len(output) {
			return false
		}
		output = append(output, value...)
		return true
	}
	for len(data) != 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 {
			if !appendBytes('\\', 'x', hexDigits[data[0]>>4], hexDigits[data[0]&0xf]) {
				return output, true
			}
			data = data[1:]
			continue
		}
		data = data[size:]
		switch {
		case r == '\n':
			if !appendBytes('\n') {
				return output, true
			}
		case r == '\r':
		case r == '\t':
			if !appendBytes(' ', ' ', ' ', ' ') {
				return output, true
			}
		case unicode.IsControl(r):
			if r <= 0xff {
				value := byte(r)
				if !appendBytes('\\', 'x', hexDigits[value>>4], hexDigits[value&0xf]) {
					return output, true
				}
			} else {
				if !appendBytes('\\', 'u', hexDigits[(r>>12)&0xf], hexDigits[(r>>8)&0xf], hexDigits[(r>>4)&0xf], hexDigits[r&0xf]) {
					return output, true
				}
			}
		default:
			var buffer [utf8.UTFMax]byte
			encodedBytes := utf8.EncodeRune(buffer[:], r)
			if !appendBytes(buffer[:encodedBytes]...) {
				return output, true
			}
		}
	}
	return output, false
}

func renderNumberedLines(data []byte, limits Limits, alreadyTruncated bool) (string, int, bool) {
	truncated := alreadyTruncated
	lineCount, hasMore := boundedLineCount(data, limits.MaxRenderedLines)
	truncated = truncated || hasMore
	width := len(strconv.Itoa(max(1, lineCount)))
	var output strings.Builder
	output.Grow(min(limits.MaxOutputBytes, len(data)))
	start := 0
	renderedLines := 0
	for index := 0; index < lineCount; index++ {
		end := bytes.IndexByte(data[start:], '\n')
		if end < 0 {
			end = len(data)
		} else {
			end += start
		}
		complete := appendNumberedLine(&output, width, index+1, data[start:end], limits.MaxOutputBytes)
		renderedLines++
		if !complete {
			truncated = true
			break
		}
		start = end + 1
	}
	return output.String(), renderedLines, truncated
}

func boundedLineCount(data []byte, maximum int) (int, bool) {
	lines := 0
	start := 0
	for start < len(data) {
		if lines == maximum {
			return lines, true
		}
		lines++
		newline := bytes.IndexByte(data[start:], '\n')
		if newline < 0 {
			return lines, false
		}
		start += newline + 1
	}
	return lines, false
}

func renderHex(data []byte, sourceOffset uint64, limits Limits, alreadyTruncated bool) (string, int, bool) {
	totalLines := (len(data) + 15) / 16
	lineCount := min(totalLines, limits.MaxRenderedLines)
	truncated := alreadyTruncated || totalLines > lineCount
	width := len(strconv.Itoa(max(1, lineCount)))
	var output strings.Builder
	output.Grow(min(limits.MaxOutputBytes, lineCount*80))
	renderedLines := 0
	for index := 0; index < lineCount; index++ {
		offset := index * 16
		end := min(offset+16, len(data))
		line := formatHexLine(sourceOffset+uint64(offset), data[offset:end]) //nolint:gosec // Render checked source range arithmetic
		complete := appendNumberedLine(&output, width, index+1, line, limits.MaxOutputBytes)
		renderedLines++
		if !complete {
			truncated = true
			break
		}
	}
	return output.String(), renderedLines, truncated
}

func formatHexLine(offset uint64, data []byte) []byte {
	const hexDigits = "0123456789abcdef"
	line := make([]byte, 0, 70)
	for shift := 60; shift >= 0; shift -= 4 {
		line = append(line, hexDigits[(offset>>shift)&0xf])
	}
	line = append(line, ' ', ' ')
	for index := 0; index < 16; index++ {
		if index < len(data) {
			line = append(line, hexDigits[data[index]>>4], hexDigits[data[index]&0xf], ' ')
		} else {
			line = append(line, ' ', ' ', ' ')
		}
	}
	line = append(line, ' ', '|')
	for _, value := range data {
		if value >= 0x20 && value <= 0x7e {
			line = append(line, value)
		} else {
			line = append(line, '.')
		}
	}
	line = append(line, '|')
	return line
}

func appendNumberedLine(output *strings.Builder, width, number int, line []byte, maximum int) bool {
	prefix := fmt.Sprintf("%*d  ", width, number)
	required := len(prefix) + len(line)
	if output.Len() != 0 {
		required++
	}
	remaining := maximum - output.Len()
	if required <= remaining {
		if output.Len() != 0 {
			output.WriteByte('\n')
		}
		output.WriteString(prefix)
		output.Write(line)
		return true
	}
	if remaining <= 0 {
		return false
	}
	if output.Len() != 0 {
		output.WriteByte('\n')
		remaining--
		if remaining == 0 {
			return false
		}
	}
	if len(prefix) >= remaining {
		output.WriteString(prefix[:remaining])
		return false
	}
	output.WriteString(prefix)
	remaining -= len(prefix)
	if remaining != 0 {
		output.Write(line[:min(len(line), remaining)])
	}
	return false
}
