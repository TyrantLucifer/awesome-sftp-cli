// Package preview provides dependency-free, bounded rendering for built-in previews.
package preview

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
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
	KindText   Kind = "text"
	KindJSON   Kind = "json"
	KindBinary Kind = "binary"
	KindImage  Kind = "image"
)

type Limits struct {
	MaxInputBytes    int
	MaxJSONBytes     int
	MaxJSONDepth     int
	MaxRenderedLines int
	MaxOutputBytes   int
	MaxImagePixels   uint64
}

func DefaultLimits() Limits {
	return Limits{
		MaxInputBytes: 512 * 1024, MaxJSONBytes: 256 * 1024, MaxJSONDepth: 64,
		MaxRenderedLines: 10_000, MaxOutputBytes: 512 * 1024, MaxImagePixels: 40_000_000,
	}
}

type Request struct {
	Path     string
	Data     []byte
	Offset   uint64
	Complete bool
}

type ImageMetadata struct {
	MediaType string
	Width     int
	Height    int
}

type Result struct {
	Kind       Kind
	Text       string
	Summary    string
	Warning    string
	InputBytes int
	Lines      int
	Partial    bool
	Truncated  bool
	Binary     bool
	Image      *ImageMetadata
}

func Render(request Request, limits Limits) Result {
	if err := limits.validate(); err != nil {
		return Result{Kind: KindText, Warning: err.Error()}
	}
	data := request.Data
	truncated := false
	if len(data) > limits.MaxInputBytes {
		data = data[:limits.MaxInputBytes]
		truncated = true
	}
	partial := !request.Complete || truncated
	result := Result{InputBytes: len(data), Partial: partial, Truncated: truncated}
	if partial {
		result.Summary = fmt.Sprintf("partial preview: bytes %d..%d", request.Offset, request.Offset+uint64(len(data)))
		if truncated {
			result.Summary += " (preview budget reached)"
		}
	}

	if imageResult, ok := renderImage(request.Path, data, limits); ok {
		imageResult.InputBytes = result.InputBytes
		imageResult.Partial = result.Partial
		imageResult.Truncated = result.Truncated
		imageResult.Summary = result.Summary
		return imageResult
	}
	if !partial && looksLikeJSON(request.Path, data) {
		switch {
		case len(data) > limits.MaxJSONBytes:
			result.Warning = "JSON exceeds preview parse budget; showing text fallback"
		default:
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
		result.Text, result.Lines, result.Truncated = renderHex(data, limits, result.Truncated)
		return result
	}
	result.Kind = KindText
	safe := sanitizeText(data)
	result.Text, result.Lines, result.Truncated = renderNumberedLines(safe, limits, result.Truncated)
	return result
}

func (limits Limits) validate() error {
	if limits.MaxInputBytes <= 0 || limits.MaxJSONBytes <= 0 || limits.MaxJSONDepth <= 0 || limits.MaxRenderedLines <= 0 || limits.MaxOutputBytes <= 0 || limits.MaxImagePixels == 0 {
		return fmt.Errorf("preview limits must all be positive")
	}
	return nil
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

func sanitizeText(data []byte) []byte {
	var output strings.Builder
	for len(data) != 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 {
			output.WriteString(fmt.Sprintf("\\x%02x", data[0]))
			data = data[1:]
			continue
		}
		data = data[size:]
		switch {
		case r == '\n':
			output.WriteByte('\n')
		case r == '\r':
		case r == '\t':
			output.WriteString("    ")
		case unicode.IsControl(r):
			if r <= 0xff {
				output.WriteString(fmt.Sprintf("\\x%02x", r))
			} else {
				output.WriteString("\\u" + fmt.Sprintf("%04x", r))
			}
		default:
			output.WriteRune(r)
		}
	}
	return []byte(output.String())
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

func renderHex(data []byte, limits Limits, alreadyTruncated bool) (string, int, bool) {
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
		line := []byte(hex.Dump(data[offset:end]))
		line = line[:len(line)-1]
		copy(line[:8], fmt.Sprintf("%08x", offset))
		complete := appendNumberedLine(&output, width, index+1, line, limits.MaxOutputBytes)
		renderedLines++
		if !complete {
			truncated = true
			break
		}
	}
	return output.String(), renderedLines, truncated
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
