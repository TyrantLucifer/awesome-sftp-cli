package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/ipc"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/transport/openssh"
)

const (
	legacySchemaVersion = 1
	SchemaVersion       = 2
)

type SortKey string
type SortDirection string
type CachePolicy string
type DrawerMode string
type FocusTarget string

const (
	SortName     SortKey = "name"
	SortSize     SortKey = "size"
	SortModified SortKey = "modified"
	SortKind     SortKey = "kind"

	SortAscending  SortDirection = "ascending"
	SortDescending SortDirection = "descending"

	CacheLRU           CachePolicy = "lru"
	CacheEphemeral     CachePolicy = "ephemeral"
	CachePinnedOffline CachePolicy = "pinned_offline"

	DrawerClosed  DrawerMode = "closed"
	DrawerPreview DrawerMode = "preview"
	DrawerJobs    DrawerMode = "jobs"
	DrawerLog     DrawerMode = "log"

	FocusPane   FocusTarget = "pane"
	FocusDrawer FocusTarget = "drawer"
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
	ActivePane int         `json:"active_pane"`
	Drawer     DrawerState `json:"drawer"`
}

type DrawerState struct {
	Mode  DrawerMode  `json:"mode"`
	Focus FocusTarget `json:"focus"`
	Rows  int         `json:"rows"`
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

type wireLayoutV1 struct {
	ActivePane  int `json:"active_pane"`
	PreviewRows int `json:"preview_rows"`
}

type wireDocumentV1 struct {
	SchemaVersion int          `json:"schema_version"`
	UpdatedAt     time.Time    `json:"updated_at"`
	Panes         [2]wirePane  `json:"panes"`
	Layout        wireLayoutV1 `json:"layout"`
	CachePolicy   CachePolicy  `json:"cache_policy"`
}

type wireDocumentV2 struct {
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
	layout := d.Layout.normalized()
	if layout.ActivePane != 0 && layout.ActivePane != 1 {
		return errors.New("workspace layout active_pane must be 0 or 1")
	}
	switch layout.Drawer.Mode {
	case DrawerClosed, DrawerPreview, DrawerJobs, DrawerLog:
	default:
		return errors.New("workspace layout drawer mode is unsupported")
	}
	switch layout.Drawer.Focus {
	case FocusPane, FocusDrawer:
	default:
		return errors.New("workspace layout drawer focus is unsupported")
	}
	if layout.Drawer.Mode == DrawerClosed && layout.Drawer.Focus != FocusPane {
		return errors.New("workspace layout closed drawer must focus pane")
	}
	if layout.Drawer.Rows < 0 || layout.Drawer.Rows > 20 {
		return errors.New("workspace layout drawer rows must be between 0 and 20")
	}
	switch d.CachePolicy {
	case CacheLRU, CacheEphemeral, CachePinnedOffline:
	default:
		return errors.New("workspace cache_policy is unsupported")
	}
	return nil
}

func (l LayoutState) normalized() LayoutState {
	if l.Drawer == (DrawerState{}) {
		l.Drawer = DrawerState{Mode: DrawerClosed, Focus: FocusPane}
	}
	return l
}

func (d Document) MarshalJSON() ([]byte, error) {
	if err := d.Validate(); err != nil {
		return nil, fmt.Errorf("encode workspace: %w", err)
	}
	return json.Marshal(d.toWire())
}

func (d *Document) UnmarshalJSON(data []byte) error {
	if d == nil {
		return errors.New("decode workspace: nil destination")
	}
	decoded, err := Decode(bytes.NewReader(data))
	if err != nil {
		return err
	}
	*d = decoded
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
	raw, err := decodeRawDocument(r)
	if err != nil {
		return Document{}, err
	}
	sourceVersion, err := decodeSchemaVersion(raw)
	if err != nil {
		return Document{}, err
	}
	var document Document
	switch sourceVersion {
	case legacySchemaVersion:
		var wire wireDocumentV1
		if err := decodeStrictDocument(raw, &wire); err != nil {
			return Document{}, err
		}
		document, err = documentFromWireV1(wire)
	case SchemaVersion:
		var wire wireDocumentV2
		if err := decodeStrictDocument(raw, &wire); err != nil {
			return Document{}, err
		}
		document, err = documentFromWireV2(wire)
	}
	if err != nil {
		return Document{}, fmt.Errorf("decode workspace: %w", err)
	}
	if err := document.Validate(); err != nil {
		return Document{}, fmt.Errorf("decode workspace: %w", err)
	}
	return document, nil
}

func decodeSchemaVersion(raw []byte) (int, error) {
	var envelope struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return 0, fmt.Errorf("decode workspace: %w", err)
	}
	switch envelope.SchemaVersion {
	case legacySchemaVersion, SchemaVersion:
		return envelope.SchemaVersion, nil
	default:
		return 0, fmt.Errorf("decode workspace: workspace schema_version %d is unsupported; want %d or %d", envelope.SchemaVersion, legacySchemaVersion, SchemaVersion)
	}
}

func decodeRawDocument(r io.Reader) (json.RawMessage, error) {
	decoder := json.NewDecoder(r)
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode workspace: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("decode workspace: trailing JSON value")
		}
		return nil, fmt.Errorf("decode workspace trailing data: %w", err)
	}
	return raw, nil
}

func decodeStrictDocument(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode workspace: %w", err)
	}
	return nil
}

func (d Document) toWire() wireDocumentV2 {
	result := wireDocumentV2{
		SchemaVersion: d.SchemaVersion,
		UpdatedAt:     d.UpdatedAt,
		Layout:        d.Layout.normalized(),
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

func documentFromWireV1(wire wireDocumentV1) (Document, error) {
	if wire.SchemaVersion != legacySchemaVersion {
		return Document{}, fmt.Errorf("workspace schema_version %d is unsupported; want %d", wire.SchemaVersion, legacySchemaVersion)
	}
	if wire.CachePolicy != CacheEphemeral {
		return Document{}, errors.New("workspace version 1 cache_policy is unsupported")
	}
	if wire.Layout.ActivePane != 0 && wire.Layout.ActivePane != 1 {
		return Document{}, errors.New("workspace layout active_pane must be 0 or 1")
	}
	if wire.Layout.PreviewRows < 0 || wire.Layout.PreviewRows > 20 {
		return Document{}, errors.New("workspace layout preview_rows must be between 0 and 20")
	}
	drawer := DrawerState{Mode: DrawerClosed, Focus: FocusPane}
	if wire.Layout.PreviewRows > 0 {
		drawer.Mode = DrawerPreview
		drawer.Rows = wire.Layout.PreviewRows
	}
	result, err := documentFromWirePanes(wire.UpdatedAt, wire.Panes)
	if err != nil {
		return Document{}, err
	}
	result.Layout = LayoutState{ActivePane: wire.Layout.ActivePane, Drawer: drawer}
	result.CachePolicy = CacheEphemeral
	return result, nil
}

func documentFromWireV2(wire wireDocumentV2) (Document, error) {
	if wire.Layout.Drawer == (DrawerState{}) {
		return Document{}, errors.New("workspace version 2 drawer is required")
	}
	result, err := documentFromWirePanes(wire.UpdatedAt, wire.Panes)
	if err != nil {
		return Document{}, err
	}
	result.Layout = wire.Layout
	result.CachePolicy = wire.CachePolicy
	return result, nil
}

func documentFromWirePanes(updatedAt time.Time, panes [2]wirePane) (Document, error) {
	result := Document{
		SchemaVersion: SchemaVersion,
		UpdatedAt:     updatedAt,
	}
	for index, paneState := range panes {
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
