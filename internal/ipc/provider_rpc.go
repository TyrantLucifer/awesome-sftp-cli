package ipc

import (
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
)

type WireEndpoint struct {
	ID           string              `json:"id"`
	Kind         domain.EndpointKind `json:"kind"`
	DisplayName  string              `json:"display_name"`
	SSHHostAlias string              `json:"ssh_host_alias,omitempty"`
}

type WireCapability struct {
	Name        domain.CapabilityName         `json:"name"`
	Version     uint16                        `json:"version"`
	Constraints []domain.CapabilityConstraint `json:"constraints,omitempty"`
}

type ProviderEndpointsResponse struct {
	Endpoints []WireEndpoint `json:"endpoints"`
}

type ProviderConnectSSHRequest struct {
	HostAlias string `json:"host_alias"`
}
type ProviderConnectSSHResponse struct {
	Endpoint WireEndpoint `json:"endpoint"`
}

type ProviderReleaseRequest struct {
	EndpointID string `json:"endpoint_id"`
}

type ProviderReleaseResponse struct{}

type ProviderSnapshotRequest struct {
	EndpointID string `json:"endpoint_id"`
}

type ProviderSnapshotResponse struct {
	EndpointID string                 `json:"endpoint_id"`
	SessionID  string                 `json:"session_id"`
	State      domain.ConnectionState `json:"state"`
	Generation uint64                 `json:"generation"`
	Complete   bool                   `json:"complete"`
	Items      []WireCapability       `json:"items"`
}

type ProviderNormalizeRequest struct {
	EndpointID string        `json:"endpoint_id"`
	Base       *WireLocation `json:"base,omitempty"`
	Input      WireBytes     `json:"input"`
}

type ProviderNormalizeResponse struct {
	Location WireLocation `json:"location"`
}

type ProviderListRequest struct {
	Location WireLocation        `json:"location"`
	Cursor   provider.PageCursor `json:"cursor,omitempty"`
	Limit    uint32              `json:"limit"`
	Sort     *provider.SortHint  `json:"sort,omitempty"`
}

type ProviderListResponse struct {
	Entries              []WireEntry              `json:"entries"`
	NextCursor           provider.PageCursor      `json:"next_cursor,omitempty"`
	Done                 bool                     `json:"done"`
	RequestedSortApplied bool                     `json:"requested_sort_applied"`
	Consistency          provider.ListConsistency `json:"consistency"`
	DirectoryFingerprint WireFingerprint          `json:"directory_fingerprint"`
}

type ProviderStatRequest struct {
	Location       WireLocation `json:"location"`
	FollowSymlinks bool         `json:"follow_symlinks"`
}

type ProviderStatResponse struct {
	Entry WireEntry `json:"entry"`
}

type ProviderReadRequest struct {
	Location            WireLocation     `json:"location"`
	Offset              int64            `json:"offset"`
	Limit               uint32           `json:"limit"`
	ExpectedFingerprint *WireFingerprint `json:"expected_fingerprint,omitempty"`
}

type ProviderReadResponse struct {
	Info ReadInfoWire `json:"info"`
	Data WireBytes    `json:"data"`
	EOF  bool         `json:"eof"`
}

type ProviderHashRequest struct {
	Location            WireLocation     `json:"location"`
	MaxBytes            uint64           `json:"max_bytes"`
	ExpectedFingerprint *WireFingerprint `json:"expected_fingerprint,omitempty"`
}

type ProviderHashResponse struct {
	Info   ReadInfoWire `json:"info"`
	SHA256 string       `json:"sha256"`
	Size   uint64       `json:"size"`
}

type ReadInfoWire struct {
	Entry       WireEntry       `json:"entry"`
	Fingerprint WireFingerprint `json:"fingerprint"`
}

func EncodeEndpoint(endpoint domain.Endpoint) WireEndpoint {
	return WireEndpoint{
		ID:           string(endpoint.ID),
		Kind:         endpoint.Kind,
		DisplayName:  endpoint.DisplayName,
		SSHHostAlias: endpoint.SSHHostAlias,
	}
}

func EncodeFingerprint(value domain.Fingerprint) WireFingerprint {
	return encodeFingerprint(value)
}

func DecodeFingerprint(value WireFingerprint) domain.Fingerprint {
	return decodeFingerprint(value)
}
