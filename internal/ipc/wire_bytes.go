package ipc

import (
	"encoding/base64"
	"fmt"
)

type WireBytes struct {
	Base64 string `json:"base64"`
}

func EncodeWireBytes(value []byte) WireBytes {
	return WireBytes{Base64: base64.StdEncoding.EncodeToString(value)}
}

func (w WireBytes) Decode() ([]byte, error) {
	value, err := base64.StdEncoding.DecodeString(w.Base64)
	if err != nil {
		return nil, fmt.Errorf("decode wire bytes: %w", err)
	}
	return value, nil
}

type WireLocation struct {
	EndpointID string    `json:"endpoint_id"`
	Path       WireBytes `json:"path"`
}
