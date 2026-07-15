package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transport/openssh"
)

const SchemaVersion = 1

type SortKey string
type SortDirection string
type CachePolicy string

const (
	SortName     SortKey = "name"
	SortSize     SortKey = "size"
	SortModified SortKey = "modified"
	SortKind     SortKey = "kind"

	SortAscending  SortDirection = "ascending"
	SortDescending SortDirection = "descending"

	CacheEphemeral CachePolicy = "ephemeral"
)

type EndpointRef struct {
	Kind         domain.EndpointKind `json:"kind"`
	SSHHostAlias string              `json:"ssh_host_alias,omitempty"`
}

type SortState struct {
	Key              SortKey       `json:"key"`
	Direction        SortDirection `json:"direction"`
	DirectoriesFirst bool          `json:"directories_first"`
}

type Pane struct {
	Endpoint   EndpointRef `json:"endpoint"`
	Path       string      `json:"path"`
	Filter     string      `json:"filter,omitempty"`
	Sort       SortState   `json:"sort"`
	ShowHidden bool        `json:"show_hidden"`
}

type LayoutState struct {
	ActivePane  int `json:"active_pane"`
	PreviewRows int `json:"preview_rows"`
}

type Document struct {
	SchemaVersion int         `json:"schema_version"`
	UpdatedAt     time.Time   `json:"updated_at"`
	Panes         [2]Pane     `json:"panes"`
	Layout        LayoutState `json:"layout"`
	CachePolicy   CachePolicy `json:"cache_policy"`
}

type wirePane struct {
	Endpoint   EndpointRef   `json:"endpoint"`
	Path       ipc.WireBytes `json:"path"`
	Filter     string        `json:"filter,omitempty"`
	Sort       SortState     `json:"sort"`
	ShowHidden bool          `json:"show_hidden"`
}

type wireDocument struct {
	SchemaVersion int         `json:"schema_version"`
	UpdatedAt     time.Time   `json:"updated_at"`
	Panes         [2]wirePane `json:"panes"`
	Layout        LayoutState `json:"layout"`
	CachePolicy   CachePolicy `json:"cache_policy"`
}

func (d Document) Validate() error {
	if d.SchemaVersion != SchemaVersion {
		return fmt.Errorf("workspace schema_version %d is unsupported; want %d", d.SchemaVersion, SchemaVersion)
	}
	if d.UpdatedAt.IsZero() {
		return errors.New("workspace updated_at is required")
	}
	for index, paneState := range d.Panes {
		if err := paneState.validate(); err != nil {
			return fmt.Errorf("workspace pane %d: %w", index, err)
		}
	}
	if d.Layout.ActivePane != 0 && d.Layout.ActivePane != 1 {
		return errors.New("workspace layout active_pane must be 0 or 1")
	}
	if d.Layout.PreviewRows < 0 || d.Layout.PreviewRows > 20 {
		return errors.New("workspace layout preview_rows must be between 0 and 20")
	}
	if d.CachePolicy != CacheEphemeral {
		return errors.New("workspace cache_policy is unsupported")
	}
	return nil
}

func (p Pane) validate() error {
	if p.Path == "" || strings.IndexByte(p.Path, 0) >= 0 {
		return errors.New("path must be non-empty and contain no NUL")
	}
	switch p.Endpoint.Kind {
	case domain.EndpointLocal:
		if p.Endpoint.SSHHostAlias != "" {
			return errors.New("local endpoint must not contain an SSH host alias")
		}
		if !filepath.IsAbs(p.Path) || filepath.Clean(p.Path) != p.Path {
			return errors.New("local path must be canonical absolute")
		}
	case domain.EndpointSSH:
		if p.Endpoint.SSHHostAlias == "" {
			return errors.New("SSH endpoint requires a host alias")
		}
		if _, err := openssh.Arguments(p.Endpoint.SSHHostAlias); err != nil {
			return fmt.Errorf("SSH host alias: %w", err)
		}
		if !path.IsAbs(p.Path) || path.Clean(p.Path) != p.Path {
			return errors.New("SSH path must be canonical absolute")
		}
	default:
		return errors.New("endpoint kind is unsupported")
	}
	if len(p.Filter) > 4096 || strings.IndexByte(p.Filter, 0) >= 0 {
		return errors.New("filter is invalid")
	}
	switch p.Sort.Key {
	case SortName, SortSize, SortModified, SortKind:
	default:
		return errors.New("sort key is unsupported")
	}
	if p.Sort.Direction != SortAscending && p.Sort.Direction != SortDescending {
		return errors.New("sort direction is unsupported")
	}
	return nil
}

func Encode(w io.Writer, document Document) error {
	if err := document.Validate(); err != nil {
		return fmt.Errorf("encode workspace: %w", err)
	}
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(document.toWire()); err != nil {
		return fmt.Errorf("encode workspace: %w", err)
	}
	return nil
}

func Decode(r io.Reader) (Document, error) {
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	var wire wireDocument
	if err := decoder.Decode(&wire); err != nil {
		return Document{}, fmt.Errorf("decode workspace: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Document{}, errors.New("decode workspace: trailing JSON value")
		}
		return Document{}, fmt.Errorf("decode workspace trailing data: %w", err)
	}
	document, err := documentFromWire(wire)
	if err != nil {
		return Document{}, fmt.Errorf("decode workspace: %w", err)
	}
	if err := document.Validate(); err != nil {
		return Document{}, fmt.Errorf("decode workspace: %w", err)
	}
	return document, nil
}

func (d Document) toWire() wireDocument {
	result := wireDocument{
		SchemaVersion: d.SchemaVersion,
		UpdatedAt:     d.UpdatedAt,
		Layout:        d.Layout,
		CachePolicy:   d.CachePolicy,
	}
	for index, paneState := range d.Panes {
		result.Panes[index] = wirePane{
			Endpoint:   paneState.Endpoint,
			Path:       ipc.EncodeWireBytes([]byte(paneState.Path)),
			Filter:     paneState.Filter,
			Sort:       paneState.Sort,
			ShowHidden: paneState.ShowHidden,
		}
	}
	return result
}

func documentFromWire(wire wireDocument) (Document, error) {
	result := Document{
		SchemaVersion: wire.SchemaVersion,
		UpdatedAt:     wire.UpdatedAt,
		Layout:        wire.Layout,
		CachePolicy:   wire.CachePolicy,
	}
	for index, paneState := range wire.Panes {
		pathBytes, err := paneState.Path.Decode()
		if err != nil {
			return Document{}, fmt.Errorf("pane %d path: %w", index, err)
		}
		result.Panes[index] = Pane{
			Endpoint:   paneState.Endpoint,
			Path:       string(pathBytes),
			Filter:     paneState.Filter,
			Sort:       paneState.Sort,
			ShowHidden: paneState.ShowHidden,
		}
	}
	return result, nil
}
