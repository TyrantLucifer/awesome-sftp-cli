package preview

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image/png"
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
	switch protocol {
	case ImageProtocolKitty, ImageProtocolITerm2:
	case ImageProtocolNone:
		return nil, fmt.Errorf("encode terminal image: no confirmed terminal image protocol")
	case ImageProtocolSixel:
		return nil, fmt.Errorf("encode terminal image: Sixel encoder is unavailable")
	default:
		return nil, fmt.Errorf("encode terminal image: unsupported protocol %q", protocol)
	}
	if mediaType != "image/png" {
		return nil, fmt.Errorf("encode terminal image: only verified PNG payloads are supported")
	}
	if len(payload) == 0 || len(payload) > limits.MaxPayloadBytes {
		return nil, fmt.Errorf("encode terminal image: payload length must be in [1,%d]", limits.MaxPayloadBytes)
	}
	encodedLength := base64.StdEncoding.EncodedLen(len(payload))
	projected, err := projectedImageOutputBytes(protocol, len(payload), encodedLength, limits)
	if err != nil {
		return nil, err
	}
	if err := verifyPNG(payload); err != nil {
		return nil, fmt.Errorf("encode terminal image: payload is not a valid PNG")
	}
	configuration, err := png.DecodeConfig(bytes.NewReader(payload))
	if err != nil || configuration.Width <= 0 || configuration.Height <= 0 {
		return nil, fmt.Errorf("encode terminal image: payload is not a valid PNG")
	}
	encoded := base64.StdEncoding.EncodeToString(payload)
	var output []byte
	switch protocol {
	case ImageProtocolKitty:
		var buffer bytes.Buffer
		buffer.Grow(projected)
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
		}
		output = buffer.Bytes()
	case ImageProtocolITerm2:
		prefix := "\x1b]1337;File=inline=1;size=" + strconv.Itoa(len(payload)) + ":"
		output = make([]byte, 0, projected)
		output = append(output, prefix...)
		output = append(output, encoded...)
		output = append(output, '\a')
	}
	return output, nil
}

func verifyPNG(payload []byte) error {
	if len(payload) < 8 || !bytes.Equal(payload[:8], []byte("\x89PNG\r\n\x1a\n")) {
		return fmt.Errorf("invalid PNG signature")
	}
	remaining := payload[8:]
	chunkIndex := 0
	hasImageData := false
	for len(remaining) != 0 {
		if len(remaining) < 12 {
			return fmt.Errorf("truncated PNG chunk")
		}
		dataBytes := uint64(binary.BigEndian.Uint32(remaining[:4]))
		chunkBytes := dataBytes + 12
		if chunkBytes > uint64(len(remaining)) { //nolint:gosec // len is non-negative
			return fmt.Errorf("truncated PNG chunk data")
		}
		end := int(chunkBytes) //nolint:gosec // bounded by len(remaining)
		chunkType := remaining[4:8]
		chunkDataEnd := 8 + int(dataBytes) //nolint:gosec // bounded by len(remaining)
		wantCRC := binary.BigEndian.Uint32(remaining[chunkDataEnd:end])
		if crc32.ChecksumIEEE(remaining[4:chunkDataEnd]) != wantCRC {
			return fmt.Errorf("invalid PNG chunk checksum")
		}
		switch string(chunkType) {
		case "IHDR":
			if chunkIndex != 0 || dataBytes != 13 {
				return fmt.Errorf("invalid PNG header")
			}
		case "IDAT":
			hasImageData = true
		case "IEND":
			if dataBytes != 0 || !hasImageData || end != len(remaining) {
				return fmt.Errorf("invalid PNG end")
			}
			return nil
		}
		chunkIndex++
		remaining = remaining[end:]
	}
	return fmt.Errorf("missing PNG end")
}

func projectedImageOutputBytes(protocol ImageProtocol, payloadBytes, encodedBytes int, limits ImageOutputLimits) (int, error) {
	projected := uint64(encodedBytes)        //nolint:gosec // encodedBytes is non-negative and derived from a slice length
	maximum := uint64(limits.MaxOutputBytes) //nolint:gosec // positivity validated by caller
	if projected > maximum {
		return 0, fmt.Errorf("encode terminal image: output exceeds %d bytes", limits.MaxOutputBytes)
	}
	switch protocol {
	case ImageProtocolKitty:
		chunkBytes := uint64(limits.ChunkBytes) //nolint:gosec // positivity validated by caller
		chunks := (projected + chunkBytes - 1) / chunkBytes
		firstEnvelope := uint64(len("\x1b_Gf=100,a=T,q=2,m=0;") + len("\x1b\\"))
		continuedEnvelope := uint64(len("\x1b_Gm=0;") + len("\x1b\\"))
		if firstEnvelope > maximum-projected {
			return 0, fmt.Errorf("encode terminal image: output exceeds %d bytes", limits.MaxOutputBytes)
		}
		projected += firstEnvelope
		if chunks > 1 {
			continuedChunks := chunks - 1
			if continuedChunks > (maximum-projected)/continuedEnvelope {
				return 0, fmt.Errorf("encode terminal image: output exceeds %d bytes", limits.MaxOutputBytes)
			}
			projected += continuedChunks * continuedEnvelope
		}
	case ImageProtocolITerm2:
		prefix := "\x1b]1337;File=inline=1;size=" + strconv.Itoa(payloadBytes) + ":"
		envelope := uint64(len(prefix) + 1)
		if envelope > maximum-projected {
			return 0, fmt.Errorf("encode terminal image: output exceeds %d bytes", limits.MaxOutputBytes)
		}
		projected += envelope
	}
	return int(projected), nil //nolint:gosec // bounded by the positive int MaxOutputBytes
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
