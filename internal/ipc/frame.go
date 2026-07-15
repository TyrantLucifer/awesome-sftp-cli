package ipc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

const maxConsecutiveEmptyFrameReads = 100

type Reader struct {
	r   io.Reader
	max uint32
}

type Writer struct {
	w   io.Writer
	max uint32
}

func NewReader(reader io.Reader, maximum uint32) (*Reader, error) {
	if reader == nil {
		return nil, errors.New("create frame reader: nil reader")
	}
	if err := validateFrameMaximum(maximum); err != nil {
		return nil, err
	}
	return &Reader{r: reader, max: maximum}, nil
}

func NewWriter(writer io.Writer, maximum uint32) (*Writer, error) {
	if writer == nil {
		return nil, errors.New("create frame writer: nil writer")
	}
	if err := validateFrameMaximum(maximum); err != nil {
		return nil, err
	}
	return &Writer{w: writer, max: maximum}, nil
}

func (r *Reader) ReadFrame() ([]byte, error) {
	var header [4]byte
	if err := readFull(r.r, header[:]); err != nil {
		return nil, fmt.Errorf("read frame header: %w", err)
	}

	length := binary.BigEndian.Uint32(header[:])
	if length == 0 {
		return nil, errors.New("read frame: zero-length payload")
	}
	if length > r.max {
		return nil, errors.New("read frame: payload exceeds configured limit")
	}

	payload := make([]byte, int(length))
	if err := readFull(r.r, payload); err != nil {
		return nil, fmt.Errorf("read frame payload: %w", err)
	}
	return payload, nil
}

func readFull(reader io.Reader, destination []byte) error {
	total := 0
	emptyReads := 0
	for total < len(destination) {
		read, err := reader.Read(destination[total:])
		total += read
		if total == len(destination) {
			return nil
		}

		if read > 0 {
			emptyReads = 0
		} else if err == nil {
			emptyReads++
			if emptyReads >= maxConsecutiveEmptyFrameReads {
				return io.ErrNoProgress
			}
		}

		if err != nil {
			if err == io.EOF && total > 0 {
				return io.ErrUnexpectedEOF
			}
			return err
		}
	}
	return nil
}

func (w *Writer) WriteFrame(payload []byte) error {
	if len(payload) == 0 {
		return errors.New("write frame: zero-length payload")
	}
	payloadLength := uint64(len(payload))
	if payloadLength > math.MaxUint32 {
		return errors.New("write frame: payload exceeds configured limit")
	}
	if payloadLength > uint64(w.max) {
		return errors.New("write frame: payload exceeds configured limit")
	}

	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(payloadLength))
	if err := writeAll(w.w, header[:]); err != nil {
		return fmt.Errorf("write frame header: %w", err)
	}
	if err := writeAll(w.w, payload); err != nil {
		return fmt.Errorf("write frame payload: %w", err)
	}
	return nil
}

func validateFrameMaximum(maximum uint32) error {
	if maximum == 0 {
		return errors.New("create frame codec: maximum is zero")
	}
	if maximum > MaxFrameBytes {
		return errors.New("create frame codec: maximum exceeds protocol limit")
	}
	return nil
}

func writeAll(writer io.Writer, value []byte) error {
	for len(value) > 0 {
		written, err := writer.Write(value)
		if written < 0 || written > len(value) {
			return io.ErrShortWrite
		}
		value = value[written:]
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}
