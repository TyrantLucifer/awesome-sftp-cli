package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	SchemaVersion = 1

	protocolMaxFrameBytes uint32 = 8 * 1024 * 1024
	hardMaxPageSize       uint32 = 4096
)

type Config struct {
	SchemaVersion int           `json:"schema_version"`
	IPC           IPCConfig     `json:"ipc"`
	Listing       ListingConfig `json:"listing"`
}

type IPCConfig struct {
	MaxFrameBytes uint32 `json:"max_frame_bytes"`
}

type ListingConfig struct {
	DefaultPageSize uint32 `json:"default_page_size"`
	MaxPageSize     uint32 `json:"max_page_size"`
}

func Default() Config {
	return Config{
		SchemaVersion: SchemaVersion,
		IPC: IPCConfig{
			MaxFrameBytes: protocolMaxFrameBytes,
		},
		Listing: ListingConfig{
			DefaultPageSize: 256,
			MaxPageSize:     hardMaxPageSize,
		},
	}
}

func Decode(r io.Reader) (Config, error) {
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()

	var config Config
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}

	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Config{}, errors.New("decode config: trailing JSON value")
		}
		return Config{}, fmt.Errorf("decode config trailing data: %w", err)
	}

	if err := config.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate config: %w", err)
	}
	return config, nil
}

func (c Config) Validate() error {
	if c.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schema_version %d is unsupported; want %d", c.SchemaVersion, SchemaVersion)
	}
	if c.IPC.MaxFrameBytes == 0 {
		return errors.New("ipc.max_frame_bytes must be greater than zero")
	}
	if c.IPC.MaxFrameBytes > protocolMaxFrameBytes {
		return fmt.Errorf("ipc.max_frame_bytes %d exceeds protocol maximum %d", c.IPC.MaxFrameBytes, protocolMaxFrameBytes)
	}
	if c.Listing.DefaultPageSize == 0 {
		return errors.New("listing.default_page_size must be greater than zero")
	}
	if c.Listing.MaxPageSize == 0 {
		return errors.New("listing.max_page_size must be greater than zero")
	}
	if c.Listing.MaxPageSize > hardMaxPageSize {
		return fmt.Errorf("listing.max_page_size %d exceeds hard maximum %d", c.Listing.MaxPageSize, hardMaxPageSize)
	}
	if c.Listing.DefaultPageSize > c.Listing.MaxPageSize {
		return fmt.Errorf(
			"listing.default_page_size %d exceeds listing.max_page_size %d",
			c.Listing.DefaultPageSize,
			c.Listing.MaxPageSize,
		)
	}
	return nil
}
