package ipc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestFrameRoundTripUsesUint32BigEndianHeader(t *testing.T) {
	payload := []byte{0xff, 0x00, 'x'}
	var wire bytes.Buffer

	writer, err := NewWriter(&wire, 16)
	if err != nil {
		t.Fatalf("NewWriter(): %v", err)
	}
	if err := writer.WriteFrame(payload); err != nil {
		t.Fatalf("WriteFrame(): %v", err)
	}
	wantWire := []byte{0x00, 0x00, 0x00, 0x03, 0xff, 0x00, 'x'}
	if !bytes.Equal(wire.Bytes(), wantWire) {
		t.Fatalf("wire = %x, want %x", wire.Bytes(), wantWire)
	}

	reader, err := NewReader(bytes.NewReader(wire.Bytes()), 16)
	if err != nil {
		t.Fatalf("NewReader(): %v", err)
	}
	got, err := reader.ReadFrame()
	if err != nil {
		t.Fatalf("ReadFrame(): %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload = %x, want %x", got, payload)
	}
}

func TestFrameProtocolMaximumPayloadBoundary(t *testing.T) {
	payload := bytes.Repeat([]byte{0xa5}, int(MaxFrameBytes))
	var wire bytes.Buffer
	writer, err := NewWriter(&wire, MaxFrameBytes)
	if err != nil {
		t.Fatalf("NewWriter(): %v", err)
	}
	if err := writer.WriteFrame(payload); err != nil {
		t.Fatalf("WriteFrame(MaxFrameBytes): %v", err)
	}
	if got := binary.BigEndian.Uint32(wire.Bytes()[:4]); got != MaxFrameBytes {
		t.Fatalf("header length = %d, want %d", got, MaxFrameBytes)
	}

	reader, err := NewReader(bytes.NewReader(wire.Bytes()), MaxFrameBytes)
	if err != nil {
		t.Fatalf("NewReader(): %v", err)
	}
	got, err := reader.ReadFrame()
	if err != nil {
		t.Fatalf("ReadFrame(MaxFrameBytes): %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("maximum-size payload changed during round trip")
	}

	var rejected bytes.Buffer
	writer, err = NewWriter(&rejected, MaxFrameBytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteFrame(make([]byte, int(MaxFrameBytes)+1)); err == nil {
		t.Fatal("WriteFrame(MaxFrameBytes+1) error = nil")
	}
	if rejected.Len() != 0 {
		t.Fatalf("over-limit writer emitted %d bytes", rejected.Len())
	}

	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, MaxFrameBytes+1)
	readerSource := &headerThenFailReader{header: header}
	reader, err = NewReader(readerSource, MaxFrameBytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadFrame(); err == nil {
		t.Fatal("ReadFrame(MaxFrameBytes+1) error = nil")
	}
	if readerSource.bodyRead {
		t.Fatal("ReadFrame(MaxFrameBytes+1) read the body")
	}
}

func TestFrameConstructorsRejectInvalidBounds(t *testing.T) {
	tests := map[string]func() error{
		"nil reader": func() error {
			_, err := NewReader(nil, 1)
			return err
		},
		"nil writer": func() error {
			_, err := NewWriter(nil, 1)
			return err
		},
		"zero reader limit": func() error {
			_, err := NewReader(bytes.NewReader(nil), 0)
			return err
		},
		"zero writer limit": func() error {
			_, err := NewWriter(io.Discard, 0)
			return err
		},
		"reader above protocol limit": func() error {
			_, err := NewReader(bytes.NewReader(nil), MaxFrameBytes+1)
			return err
		},
		"writer above protocol limit": func() error {
			_, err := NewWriter(io.Discard, MaxFrameBytes+1)
			return err
		},
	}

	for name, check := range tests {
		t.Run(name, func(t *testing.T) {
			if err := check(); err == nil {
				t.Fatal("constructor error = nil")
			}
		})
	}
}

func TestReaderRejectsZeroOverLimitAndTruncatedFrames(t *testing.T) {
	header := func(length uint32) []byte {
		value := make([]byte, 4)
		binary.BigEndian.PutUint32(value, length)
		return value
	}

	tests := map[string]struct {
		wire []byte
		max  uint32
	}{
		"zero length":      {wire: header(0), max: 8},
		"over limit":       {wire: append(header(9), make([]byte, 9)...), max: 8},
		"truncated header": {wire: []byte{0, 0}, max: 8},
		"truncated body":   {wire: append(header(4), []byte{1, 2}...), max: 8},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			reader, err := NewReader(bytes.NewReader(test.wire), test.max)
			if err != nil {
				t.Fatalf("NewReader(): %v", err)
			}
			if _, err := reader.ReadFrame(); err == nil {
				t.Fatal("ReadFrame() error = nil")
			}
		})
	}
}

func TestReaderReportsTruncationWithUnderlyingError(t *testing.T) {
	tests := map[string][]byte{
		"header": {0, 0},
		"body":   {0, 0, 0, 2, 1},
	}
	for name, wire := range tests {
		t.Run(name, func(t *testing.T) {
			reader, err := NewReader(bytes.NewReader(wire), 8)
			if err != nil {
				t.Fatal(err)
			}
			_, err = reader.ReadFrame()
			if !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("ReadFrame() error = %v, want wrapped unexpected EOF", err)
			}
		})
	}
}

func TestReaderReportsEOFBeforeHeader(t *testing.T) {
	reader, err := NewReader(bytes.NewReader(nil), 8)
	if err != nil {
		t.Fatal(err)
	}
	_, err = reader.ReadFrame()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("ReadFrame() error = %v, want wrapped EOF", err)
	}
}

func TestReaderRejectsExcessiveNoProgress(t *testing.T) {
	wire := []byte{0, 0, 0, 1, 'x'}
	tests := map[string]struct {
		stallOffset int
		wantContext string
	}{
		"header": {stallOffset: 0, wantContext: "frame header"},
		"body":   {stallOffset: 4, wantContext: "frame payload"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			source := &intermittentNoProgressReader{
				data:               wire,
				emptyReadsByOffset: map[int]int{test.stallOffset: 101},
			}
			reader, err := NewReader(source, 1)
			if err != nil {
				t.Fatal(err)
			}

			_, err = reader.ReadFrame()
			if !errors.Is(err, io.ErrNoProgress) {
				t.Fatalf("ReadFrame() error = %v, want wrapped no progress", err)
			}
			if !strings.Contains(err.Error(), test.wantContext) {
				t.Fatalf("ReadFrame() error = %q, want %q context", err, test.wantContext)
			}
		})
	}
}

func TestReaderRecoversFromTemporaryNoProgress(t *testing.T) {
	wire := []byte{0, 0, 0, 1, 'x'}
	for name, stallOffset := range map[string]int{
		"header": 0,
		"body":   4,
	} {
		t.Run(name, func(t *testing.T) {
			source := &intermittentNoProgressReader{
				data:               wire,
				emptyReadsByOffset: map[int]int{stallOffset: 2},
			}
			reader, err := NewReader(source, 1)
			if err != nil {
				t.Fatal(err)
			}

			got, err := reader.ReadFrame()
			if err != nil {
				t.Fatalf("ReadFrame(): %v", err)
			}
			if !bytes.Equal(got, []byte{'x'}) {
				t.Fatalf("ReadFrame() = %q, want x", got)
			}
		})
	}
}

func TestReaderResetsNoProgressCountAfterProgress(t *testing.T) {
	source := &intermittentNoProgressReader{
		data:               []byte{0, 0, 0, 2, 'x', 'y'},
		emptyReadsByOffset: map[int]int{4: 99, 5: 99},
		maximumChunk:       1,
	}
	reader, err := NewReader(source, 2)
	if err != nil {
		t.Fatal(err)
	}

	got, err := reader.ReadFrame()
	if err != nil {
		t.Fatalf("ReadFrame(): %v", err)
	}
	if !bytes.Equal(got, []byte("xy")) {
		t.Fatalf("ReadFrame() = %q, want xy", got)
	}
}

func TestReaderRejectsOverLimitBeforeReadingBody(t *testing.T) {
	readerSource := &headerThenFailReader{header: []byte{0, 0, 0, 9}}
	reader, err := NewReader(readerSource, 8)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadFrame(); err == nil {
		t.Fatal("ReadFrame() error = nil")
	}
	if readerSource.bodyRead {
		t.Fatal("ReadFrame() read the body of an over-limit frame")
	}
}

func TestReaderReadsConsecutiveFrames(t *testing.T) {
	wire := []byte{0, 0, 0, 1, 'a', 0, 0, 0, 2, 'b', 'c'}
	reader, err := NewReader(bytes.NewReader(wire), 2)
	if err != nil {
		t.Fatal(err)
	}
	for index, want := range [][]byte{{'a'}, {'b', 'c'}} {
		got, err := reader.ReadFrame()
		if err != nil {
			t.Fatalf("ReadFrame(%d): %v", index, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("ReadFrame(%d) = %q, want %q", index, got, want)
		}
	}
}

func TestWriterRejectsInvalidPayloadWithoutWriting(t *testing.T) {
	for name, payload := range map[string][]byte{
		"zero":       nil,
		"over limit": make([]byte, 9),
	} {
		t.Run(name, func(t *testing.T) {
			var destination bytes.Buffer
			writer, err := NewWriter(&destination, 8)
			if err != nil {
				t.Fatal(err)
			}
			if err := writer.WriteFrame(payload); err == nil {
				t.Fatal("WriteFrame() error = nil")
			}
			if destination.Len() != 0 {
				t.Fatalf("destination received %d bytes", destination.Len())
			}
		})
	}
}

func TestWriterLoopsOnShortWrites(t *testing.T) {
	destination := &chunkWriter{maximum: 2}
	writer, err := NewWriter(destination, 8)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteFrame([]byte("abcd")); err != nil {
		t.Fatalf("WriteFrame(): %v", err)
	}
	want := []byte{0, 0, 0, 4, 'a', 'b', 'c', 'd'}
	if !bytes.Equal(destination.Bytes(), want) {
		t.Fatalf("wire = %x, want %x", destination.Bytes(), want)
	}
	if destination.calls < 4 {
		t.Fatalf("Write() calls = %d, want short-write loop", destination.calls)
	}
}

func TestWriterRejectsZeroProgress(t *testing.T) {
	writer, err := NewWriter(zeroProgressWriter{}, 8)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteFrame([]byte("x")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("WriteFrame() error = %v, want wrapped short write", err)
	}
}

type headerThenFailReader struct {
	header   []byte
	read     bool
	bodyRead bool
}

func (r *headerThenFailReader) Read(destination []byte) (int, error) {
	if !r.read {
		r.read = true
		return copy(destination, r.header), nil
	}
	r.bodyRead = true
	return 0, errors.New("body must not be read")
}

type intermittentNoProgressReader struct {
	data               []byte
	emptyReadsByOffset map[int]int
	offset             int
	maximumChunk       int
}

func (r *intermittentNoProgressReader) Read(destination []byte) (int, error) {
	if r.emptyReadsByOffset[r.offset] > 0 {
		r.emptyReadsByOffset[r.offset]--
		return 0, nil
	}
	if r.offset == len(r.data) {
		return 0, io.EOF
	}
	if r.maximumChunk > 0 && len(destination) > r.maximumChunk {
		destination = destination[:r.maximumChunk]
	}
	written := copy(destination, r.data[r.offset:])
	r.offset += written
	return written, nil
}

type chunkWriter struct {
	bytes.Buffer
	maximum int
	calls   int
}

func (w *chunkWriter) Write(value []byte) (int, error) {
	w.calls++
	if len(value) > w.maximum {
		value = value[:w.maximum]
	}
	return w.Buffer.Write(value)
}

type zeroProgressWriter struct{}

func (zeroProgressWriter) Write([]byte) (int, error) {
	return 0, nil
}
