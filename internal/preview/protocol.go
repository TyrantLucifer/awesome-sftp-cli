package preview

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

type ImageProtocol string

const (
	ImageProtocolNone   ImageProtocol = "none"
	ImageProtocolKitty  ImageProtocol = "kitty"
	ImageProtocolITerm2 ImageProtocol = "iterm2"
	ImageProtocolSixel  ImageProtocol = "sixel"
)

type ImageCapabilities struct {
	Kitty  bool
	ITerm2 bool
	Sixel  bool
}

func SelectImageProtocol(environment map[string]string, capabilities ImageCapabilities) ImageProtocol {
	term := environment["TERM"]
	if capabilities.Kitty && (strings.Contains(term, "kitty") || environment["KITTY_WINDOW_ID"] != "") {
		return ImageProtocolKitty
	}
	if capabilities.ITerm2 && environment["TERM_PROGRAM"] == "iTerm.app" {
		return ImageProtocolITerm2
	}
	if capabilities.Sixel && strings.Contains(strings.ToLower(term), "sixel") {
		return ImageProtocolSixel
	}
	return ImageProtocolNone
}

type ImageOutputLimits struct {
	MaxPayloadBytes int
	MaxOutputBytes  int
	ChunkBytes      int
}

func DefaultImageOutputLimits() ImageOutputLimits {
	return ImageOutputLimits{MaxPayloadBytes: 4 * 1024 * 1024, MaxOutputBytes: 6 * 1024 * 1024, ChunkBytes: 4096}
}

func EncodeTerminalImage(protocol ImageProtocol, mediaType string, payload []byte, limits ImageOutputLimits) ([]byte, error) {
	if limits.MaxPayloadBytes <= 0 || limits.MaxOutputBytes <= 0 || limits.ChunkBytes <= 0 {
		return nil, fmt.Errorf("encode terminal image: limits must be positive")
	}
	if mediaType != "image/png" {
		return nil, fmt.Errorf("encode terminal image: only verified PNG payloads are supported")
	}
	if len(payload) == 0 || len(payload) > limits.MaxPayloadBytes {
		return nil, fmt.Errorf("encode terminal image: payload length must be in [1,%d]", limits.MaxPayloadBytes)
	}
	encoded := base64.StdEncoding.EncodeToString(payload)
	var output []byte
	switch protocol {
	case ImageProtocolKitty:
		var buffer bytes.Buffer
		for offset := 0; offset < len(encoded); offset += limits.ChunkBytes {
			end := min(offset+limits.ChunkBytes, len(encoded))
			more := end < len(encoded)
			if offset == 0 {
				fmt.Fprintf(&buffer, "\x1b_Gf=100,a=T,q=2,m=%d;", boolInt(more))
			} else {
				fmt.Fprintf(&buffer, "\x1b_Gm=%d;", boolInt(more))
			}
			buffer.WriteString(encoded[offset:end])
			buffer.WriteString("\x1b\\")
			if buffer.Len() > limits.MaxOutputBytes {
				return nil, fmt.Errorf("encode terminal image: output exceeds %d bytes", limits.MaxOutputBytes)
			}
		}
		output = buffer.Bytes()
	case ImageProtocolITerm2:
		prefix := "\x1b]1337;File=inline=1;size=" + strconv.Itoa(len(payload)) + ":"
		if len(prefix)+len(encoded)+1 > limits.MaxOutputBytes {
			return nil, fmt.Errorf("encode terminal image: output exceeds %d bytes", limits.MaxOutputBytes)
		}
		output = make([]byte, 0, len(prefix)+len(encoded)+1)
		output = append(output, prefix...)
		output = append(output, encoded...)
		output = append(output, '\a')
	case ImageProtocolNone:
		return nil, fmt.Errorf("encode terminal image: no confirmed terminal image protocol")
	case ImageProtocolSixel:
		return nil, fmt.Errorf("encode terminal image: Sixel encoder is unavailable")
	default:
		return nil, fmt.Errorf("encode terminal image: unsupported protocol %q", protocol)
	}
	return append([]byte(nil), output...), nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
