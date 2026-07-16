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
	if !partial && len(data) <= limits.MaxJSONBytes && looksLikeJSON(request.Path, data) {
		if rendered, depth, ok := renderJSON(data); ok {
			if depth <= limits.MaxJSONDepth {
				result.Kind = KindJSON
				result.Text, result.Lines, result.Truncated = renderNumberedLines(rendered, limits, result.Truncated)
				return result
			}
			result.Warning = "JSON depth exceeds preview budget"
		}
	}
	if looksBinary(data) {
		result.Kind = KindBinary
		result.Binary = true
		result.Text, result.Lines, result.Truncated = renderNumberedLines([]byte(hex.Dump(data)), limits, result.Truncated)
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

func renderJSON(data []byte) ([]byte, int, bool) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, 0, false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, 0, false
	}
	depth := jsonDepth(value, 1)
	rendered, err := json.MarshalIndent(value, "", "  ")
	return rendered, depth, err == nil
}

func jsonDepth(value any, current int) int {
	maximum := current
	switch typed := value.(type) {
	case []any:
		for _, child := range typed {
			maximum = max(maximum, jsonDepth(child, current+1))
		}
	case map[string]any:
		for _, child := range typed {
			maximum = max(maximum, jsonDepth(child, current+1))
		}
	}
	return maximum
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
	lines := strings.Split(string(data), "\n")
	if len(lines) != 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	truncated := alreadyTruncated
	if len(lines) > limits.MaxRenderedLines {
		lines = lines[:limits.MaxRenderedLines]
		truncated = true
	}
	width := len(strconv.Itoa(max(1, len(lines))))
	var output strings.Builder
	for index, line := range lines {
		formatted := fmt.Sprintf("%*d  %s", width, index+1, line)
		if index != len(lines)-1 {
			formatted += "\n"
		}
		remaining := limits.MaxOutputBytes - output.Len()
		if remaining <= 0 {
			truncated = true
			break
		}
		if len(formatted) > remaining {
			output.WriteString(formatted[:remaining])
			truncated = true
			break
		}
		output.WriteString(formatted)
	}
	return output.String(), min(len(lines), limits.MaxRenderedLines), truncated
}
