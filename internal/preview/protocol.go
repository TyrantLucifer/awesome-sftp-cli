package preview

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
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
	MaxPixels       uint64
}

func DefaultImageOutputLimits() ImageOutputLimits {
	return ImageOutputLimits{MaxPayloadBytes: 4 * 1024 * 1024, MaxOutputBytes: 6 * 1024 * 1024, ChunkBytes: 4096, MaxPixels: 1_000_000}
}

func EncodeTerminalImage(protocol ImageProtocol, mediaType string, payload []byte, limits ImageOutputLimits) ([]byte, error) {
	if limits.MaxPayloadBytes <= 0 || limits.MaxOutputBytes <= 0 || limits.ChunkBytes <= 0 || limits.MaxPixels == 0 {
		return nil, fmt.Errorf("encode terminal image: limits must be positive")
	}
	switch protocol {
	case ImageProtocolKitty, ImageProtocolITerm2, ImageProtocolSixel:
	case ImageProtocolNone:
		return nil, fmt.Errorf("encode terminal image: no confirmed terminal image protocol")
	default:
		return nil, fmt.Errorf("encode terminal image: unsupported protocol")
	}
	if mediaType != "image/png" {
		return nil, fmt.Errorf("encode terminal image: only verified PNG payloads are supported")
	}
	if len(payload) == 0 || len(payload) > limits.MaxPayloadBytes {
		return nil, fmt.Errorf("encode terminal image: payload length must be in [1,%d]", limits.MaxPayloadBytes)
	}
	projected := 0
	if protocol != ImageProtocolSixel {
		encodedLength := base64.StdEncoding.EncodedLen(len(payload))
		var err error
		projected, err = projectedImageOutputBytes(protocol, len(payload), encodedLength, limits)
		if err != nil {
			return nil, err
		}
	}
	if err := verifyPNG(payload); err != nil {
		return nil, fmt.Errorf("encode terminal image: payload is not a valid PNG")
	}
	configuration, err := png.DecodeConfig(bytes.NewReader(payload))
	if err != nil || configuration.Width <= 0 || configuration.Height <= 0 {
		return nil, fmt.Errorf("encode terminal image: payload is not a valid PNG")
	}
	pixels := uint64(configuration.Width) * uint64(configuration.Height)
	if pixels > limits.MaxPixels {
		return nil, fmt.Errorf("encode terminal image: pixel count %d exceeds %d", pixels, limits.MaxPixels)
	}
	if protocol == ImageProtocolSixel {
		projected, err = projectedSixelOutputBytes(configuration.Width, configuration.Height, limits.MaxOutputBytes)
		if err != nil {
			return nil, err
		}
		decoded, decodeErr := png.Decode(bytes.NewReader(payload))
		if decodeErr != nil {
			return nil, fmt.Errorf("encode terminal image: payload is not a valid PNG")
		}
		return encodeSixel(decoded, projected), nil
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

const sixelPaletteSize = 16

func projectedSixelOutputBytes(width, height, maximum int) (int, error) {
	if width <= 0 || height <= 0 || maximum <= 0 {
		return 0, fmt.Errorf("encode terminal image: invalid Sixel dimensions or output budget")
	}
	projected := uint64(len("\x1bPq") + len("\x1b\\"))
	projected += uint64(len(fmt.Sprintf("\"1;1;%d;%d", width, height)))
	for palette := 0; palette < sixelPaletteSize; palette++ {
		projected += uint64(len(sixelPaletteDefinition(palette)))
	}
	bands := (uint64(height) + 5) / 6 //nolint:gosec // positive int
	perBand := uint64(1)
	for palette := 0; palette < sixelPaletteSize; palette++ {
		perBand += uint64(len(strconv.Itoa(palette))+1) + uint64(width) + 1 // #n, pixels, carriage return
	}
	maximumBytes := uint64(maximum)
	if projected > maximumBytes || bands > (maximumBytes-projected)/perBand {
		return 0, fmt.Errorf("encode terminal image: output exceeds %d bytes", maximum)
	}
	projected += bands * perBand
	return int(projected), nil //nolint:gosec // bounded by positive int maximum
}

func encodeSixel(source image.Image, capacity int) []byte {
	bounds := source.Bounds()
	var output bytes.Buffer
	output.Grow(capacity)
	output.WriteString("\x1bPq")
	fmt.Fprintf(&output, "\"1;1;%d;%d", bounds.Dx(), bounds.Dy())
	for palette := 0; palette < sixelPaletteSize; palette++ {
		output.WriteString(sixelPaletteDefinition(palette))
	}
	for top := bounds.Min.Y; top < bounds.Max.Y; top += 6 {
		for palette := 0; palette < sixelPaletteSize; palette++ {
			fmt.Fprintf(&output, "#%d", palette)
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				bits := byte(0)
				for row := 0; row < 6 && top+row < bounds.Max.Y; row++ {
					if sixelPaletteIndex(source.At(x, top+row)) == palette {
						bits |= 1 << row
					}
				}
				output.WriteByte(63 + bits)
			}
			output.WriteByte('$')
		}
		output.WriteByte('-')
	}
	output.WriteString("\x1b\\")
	return output.Bytes()
}

func sixelPaletteDefinition(index int) string {
	red := (index >> 3) & 1
	green := (index >> 2) & 1
	blue := index & 3
	return fmt.Sprintf("#%d;2;%d;%d;%d", index, red*100, green*100, blue*100/3)
}

func sixelPaletteIndex(value interface{ RGBA() (r, g, b, a uint32) }) int {
	red, green, blue, _ := value.RGBA()
	return int((red>>15)<<3 | (green>>15)<<2 | (blue >> 14))
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
