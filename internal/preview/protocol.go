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

// ImageCapabilityProof can only be created from a bounded terminal probe
// response. Environment variables and terminal names are intentionally not
// sufficient proof that emitting a graphics control sequence is safe.
type ImageCapabilityProof struct {
	protocol ImageProtocol
}

// ImageCapabilityProbe returns the bounded query whose response is accepted by
// ConfirmImageCapability. The application must route the reply away from
// normal key input and apply its own short read deadline.
func ImageCapabilityProbe(protocol ImageProtocol) ([]byte, error) {
	switch protocol {
	case ImageProtocolKitty:
		return []byte("\x1b_Gi=31,a=q,t=d,f=24,s=1,v=1;AAAA\x1b\\"), nil
	case ImageProtocolITerm2:
		return []byte("\x1b[>0q"), nil
	case ImageProtocolSixel:
		return []byte("\x1b[c"), nil
	case ImageProtocolNone:
		return nil, fmt.Errorf("terminal image probe: no protocol requested")
	default:
		return nil, fmt.Errorf("terminal image probe: unsupported protocol")
	}
}

// Protocol returns the actively confirmed protocol, or none for a zero proof.
func (proof ImageCapabilityProof) Protocol() ImageProtocol {
	if proof.protocol == "" {
		return ImageProtocolNone
	}
	return proof.protocol
}

// ConfirmImageCapability validates an exact response to a protocol-specific
// active probe. The caller owns sending the query and bounding its read; this
// function independently rejects oversized, concatenated, or control-injected
// replies.
func ConfirmImageCapability(protocol ImageProtocol, response []byte) (ImageCapabilityProof, error) {
	if len(response) == 0 || len(response) > 256 {
		return ImageCapabilityProof{}, fmt.Errorf("confirm terminal image protocol: invalid response size")
	}
	confirmed := false
	switch protocol {
	case ImageProtocolKitty:
		confirmed = bytes.Equal(response, []byte("\x1b_Gi=31;OK\x1b\\"))
	case ImageProtocolITerm2:
		const prefix = "\x1bP>|iTerm2 "
		const suffix = "\x1b\\"
		if bytes.HasPrefix(response, []byte(prefix)) && bytes.HasSuffix(response, []byte(suffix)) {
			version := response[len(prefix) : len(response)-len(suffix)]
			confirmed = len(version) > 0 && len(version) <= 64 && safeTerminalVersion(version)
		}
	case ImageProtocolSixel:
		confirmed = sixelCapabilityResponse(response)
	case ImageProtocolNone:
		return ImageCapabilityProof{}, fmt.Errorf("confirm terminal image protocol: no protocol requested")
	default:
		return ImageCapabilityProof{}, fmt.Errorf("confirm terminal image protocol: unsupported protocol")
	}
	if !confirmed {
		return ImageCapabilityProof{}, fmt.Errorf("confirm terminal image protocol: probe did not confirm capability")
	}
	return ImageCapabilityProof{protocol: protocol}, nil
}

func safeTerminalVersion(value []byte) bool {
	for _, character := range value {
		if character != ' ' && character != '.' && character != '-' && character != '_' &&
			(character < '0' || character > '9') && (character < 'A' || character > 'Z') && (character < 'a' || character > 'z') {
			return false
		}
	}
	return true
}

func sixelCapabilityResponse(response []byte) bool {
	if len(response) < len("\x1b[?4c") || !bytes.HasPrefix(response, []byte("\x1b[?")) || response[len(response)-1] != 'c' {
		return false
	}
	parameters := response[len("\x1b[?") : len(response)-1]
	for _, parameter := range bytes.Split(parameters, []byte{';'}) {
		if len(parameter) == 0 {
			return false
		}
		value := 0
		for _, digit := range parameter {
			if digit < '0' || digit > '9' {
				return false
			}
			value = value*10 + int(digit-'0')
			if value > 9999 {
				return false
			}
		}
		if value == 4 {
			return true
		}
	}
	return false
}

// SelectImageProtocol selects among separately discovered capability hints. Its
// result is not an active proof and cannot be passed to the live encoder.
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

const (
	// Save cursor before output, then reset SGR and restore it afterwards. Each
	// protocol payload also carries its own mandatory APC/OSC/DCS terminator.
	terminalImageSafetyPrefix = "\x1b7"
	terminalImageSafetySuffix = "\x1b[0m\x1b8"
)

// EncodeTerminalImageWithProof is the only encoder entry point suitable for
// live terminal output. A zero or fabricated proof cannot select a protocol.
func EncodeTerminalImageWithProof(proof ImageCapabilityProof, mediaType string, payload []byte, limits ImageOutputLimits) ([]byte, error) {
	if proof.Protocol() == ImageProtocolNone {
		return nil, fmt.Errorf("encode terminal image: capability is not actively confirmed")
	}
	return EncodeTerminalImage(proof.Protocol(), mediaType, payload, limits)
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
		return wrapTerminalImage(encodeSixel(decoded, projected)), nil
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
	return wrapTerminalImage(output), nil
}

func wrapTerminalImage(payload []byte) []byte {
	output := make([]byte, 0, len(terminalImageSafetyPrefix)+len(payload)+len(terminalImageSafetySuffix))
	output = append(output, terminalImageSafetyPrefix...)
	output = append(output, payload...)
	output = append(output, terminalImageSafetySuffix...)
	return output
}

const sixelPaletteSize = 16

func projectedSixelOutputBytes(width, height, maximum int) (int, error) {
	if width <= 0 || height <= 0 || maximum <= 0 {
		return 0, fmt.Errorf("encode terminal image: invalid Sixel dimensions or output budget")
	}
	projected := uint64(len(terminalImageSafetyPrefix) + len("\x1bPq") + len("\x1b\\") + len(terminalImageSafetySuffix))
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
	projected := uint64(encodedBytes) + uint64(len(terminalImageSafetyPrefix)+len(terminalImageSafetySuffix)) //nolint:gosec // encodedBytes is non-negative and derived from a slice length
	maximum := uint64(limits.MaxOutputBytes)                                                                  //nolint:gosec // positivity validated by caller
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
