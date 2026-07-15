package ipc

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
)

type WireMetadata struct {
	Size              *uint64               `json:"size,omitempty"`
	Mode              *uint32               `json:"mode,omitempty"`
	UID               *uint32               `json:"uid,omitempty"`
	GID               *uint32               `json:"gid,omitempty"`
	ModifiedAt        *time.Time            `json:"modified_at,omitempty"`
	ModifiedPrecision *domain.TimePrecision `json:"modified_precision,omitempty"`
	FileID            *string               `json:"file_id,omitempty"`
}

type WireFingerprint struct {
	Size              *uint64               `json:"size,omitempty"`
	ModifiedAt        *time.Time            `json:"modified_at,omitempty"`
	ModifiedPrecision *domain.TimePrecision `json:"modified_precision,omitempty"`
	FileID            *string               `json:"file_id,omitempty"`
	VersionID         *string               `json:"version_id,omitempty"`
	HashAlgorithm     *string               `json:"hash_algorithm,omitempty"`
	HashHex           *string               `json:"hash_hex,omitempty"`
}

type WireSymlinkInfo struct {
	RawTarget    WireBytes         `json:"raw_target"`
	ResolvedKind *domain.EntryKind `json:"resolved_kind,omitempty"`
}

type WireEntry struct {
	Location    WireLocation     `json:"location"`
	Name        WireBytes        `json:"name"`
	Kind        domain.EntryKind `json:"kind"`
	Metadata    WireMetadata     `json:"metadata"`
	Fingerprint WireFingerprint  `json:"fingerprint"`
	Symlink     *WireSymlinkInfo `json:"symlink,omitempty"`
}

func EncodeLocation(location domain.Location) WireLocation {
	return WireLocation{
		EndpointID: string(location.EndpointID),
		Path:       EncodeWireBytes([]byte(location.Path)),
	}
}

func DecodeLocation(wire WireLocation) (domain.Location, error) {
	endpointID, err := domain.ParseEndpointID(wire.EndpointID)
	if err != nil {
		return domain.Location{}, errors.New("decode wire location: invalid endpoint ID")
	}
	pathBytes, err := wire.Path.Decode()
	if err != nil {
		return domain.Location{}, fmt.Errorf("decode wire location path: %w", err)
	}
	location, err := domain.NewLocation(endpointID, domain.CanonicalPath(pathBytes))
	if err != nil {
		return domain.Location{}, fmt.Errorf("decode wire location: %w", err)
	}
	return location, nil
}

func EncodeEntry(entry domain.Entry) WireEntry {
	wire := WireEntry{
		Location: EncodeLocation(entry.Location),
		Name:     EncodeWireBytes([]byte(entry.Name)),
		Kind:     entry.Kind,
		Metadata: WireMetadata{
			Size:              clonePointer(entry.Metadata.Size),
			Mode:              clonePointer(entry.Metadata.Mode),
			UID:               clonePointer(entry.Metadata.UID),
			GID:               clonePointer(entry.Metadata.GID),
			ModifiedAt:        clonePointer(entry.Metadata.ModifiedAt),
			ModifiedPrecision: clonePointer(entry.Metadata.ModifiedPrecision),
			FileID:            clonePointer(entry.Metadata.FileID),
		},
		Fingerprint: encodeFingerprint(entry.Fingerprint),
	}
	if entry.Symlink != nil {
		wire.Symlink = &WireSymlinkInfo{
			RawTarget:    EncodeWireBytes([]byte(entry.Symlink.RawTarget)),
			ResolvedKind: clonePointer(entry.Symlink.ResolvedKind),
		}
	}
	return wire
}

func DecodeEntry(wire WireEntry) (domain.Entry, error) {
	location, err := DecodeLocation(wire.Location)
	if err != nil {
		return domain.Entry{}, err
	}
	nameBytes, err := wire.Name.Decode()
	if err != nil {
		return domain.Entry{}, fmt.Errorf("decode wire entry name: %w", err)
	}
	name := string(nameBytes)
	if name == "" || strings.IndexByte(name, 0) >= 0 || name != "/" && strings.Contains(name, "/") {
		return domain.Entry{}, errors.New("decode wire entry: invalid name")
	}
	if !knownEntryKind(wire.Kind) {
		return domain.Entry{}, errors.New("decode wire entry: invalid kind")
	}
	entry := domain.Entry{
		Location: location,
		Name:     name,
		Kind:     wire.Kind,
		Metadata: domain.Metadata{
			Size:              clonePointer(wire.Metadata.Size),
			Mode:              clonePointer(wire.Metadata.Mode),
			UID:               clonePointer(wire.Metadata.UID),
			GID:               clonePointer(wire.Metadata.GID),
			ModifiedAt:        clonePointer(wire.Metadata.ModifiedAt),
			ModifiedPrecision: clonePointer(wire.Metadata.ModifiedPrecision),
			FileID:            clonePointer(wire.Metadata.FileID),
		},
		Fingerprint: decodeFingerprint(wire.Fingerprint),
	}
	if wire.Symlink != nil {
		target, err := wire.Symlink.RawTarget.Decode()
		if err != nil {
			return domain.Entry{}, fmt.Errorf("decode wire symlink target: %w", err)
		}
		if strings.IndexByte(string(target), 0) >= 0 {
			return domain.Entry{}, errors.New("decode wire entry: symlink target contains NUL")
		}
		if wire.Symlink.ResolvedKind != nil && !knownEntryKind(*wire.Symlink.ResolvedKind) {
			return domain.Entry{}, errors.New("decode wire entry: invalid resolved symlink kind")
		}
		entry.Symlink = &domain.SymlinkInfo{
			RawTarget:    string(target),
			ResolvedKind: clonePointer(wire.Symlink.ResolvedKind),
		}
	}
	return entry, nil
}

func encodeFingerprint(value domain.Fingerprint) WireFingerprint {
	return WireFingerprint{
		Size:              clonePointer(value.Size),
		ModifiedAt:        clonePointer(value.ModifiedAt),
		ModifiedPrecision: clonePointer(value.ModifiedPrecision),
		FileID:            clonePointer(value.FileID),
		VersionID:         clonePointer(value.VersionID),
		HashAlgorithm:     clonePointer(value.HashAlgorithm),
		HashHex:           clonePointer(value.HashHex),
	}
}

func decodeFingerprint(value WireFingerprint) domain.Fingerprint {
	return domain.Fingerprint{
		Size:              clonePointer(value.Size),
		ModifiedAt:        clonePointer(value.ModifiedAt),
		ModifiedPrecision: clonePointer(value.ModifiedPrecision),
		FileID:            clonePointer(value.FileID),
		VersionID:         clonePointer(value.VersionID),
		HashAlgorithm:     clonePointer(value.HashAlgorithm),
		HashHex:           clonePointer(value.HashHex),
	}
}

func knownEntryKind(kind domain.EntryKind) bool {
	switch kind {
	case domain.EntryFile, domain.EntryDirectory, domain.EntrySymlink, domain.EntryOther:
		return true
	default:
		return false
	}
}

func clonePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
