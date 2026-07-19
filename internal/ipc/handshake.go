package ipc

import (
	"sort"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
)

type VersionRange struct {
	Major    uint16 `json:"major"`
	MinMinor uint16 `json:"min_minor"`
	MaxMinor uint16 `json:"max_minor"`
}

type ProtocolFeature struct {
	Name    string `json:"name"`
	Version uint16 `json:"version"`
}

type HelloRequest struct {
	ClientVersion    string            `json:"client_version"`
	ClientInstanceID string            `json:"client_instance_id"`
	Protocols        []VersionRange    `json:"protocols"`
	Features         []ProtocolFeature `json:"features"`
	EventTypes       []string          `json:"event_types"`
}

type HelloResponse struct {
	DaemonVersion string            `json:"daemon_version"`
	Selected      ProtocolVersion   `json:"selected"`
	Features      []ProtocolFeature `json:"features"`
	EventTypes    []string          `json:"event_types"`
	CurrentCursor EventCursor       `json:"current_cursor"`
	OldestCursor  EventCursor       `json:"oldest_cursor"`
}

func Negotiate(
	client []VersionRange,
	server []VersionRange,
	clientFeatures []ProtocolFeature,
	serverFeatures []ProtocolFeature,
) (ProtocolVersion, []ProtocolFeature, error) {
	version, ok := highestSharedVersion(client, server)
	if !ok {
		return ProtocolVersion{}, nil, &domain.OpError{
			Code:    domain.CodeProtocolIncompatible,
			Message: "no compatible protocol version",
			Retry:   domain.RetryAdvice{Kind: domain.RetryNever},
			Effect:  domain.EffectNone,
		}
	}

	return version, intersectFeatures(clientFeatures, serverFeatures), nil
}

func highestSharedVersion(client []VersionRange, server []VersionRange) (ProtocolVersion, bool) {
	var best ProtocolVersion
	found := false
	for _, clientRange := range client {
		if clientRange.MinMinor > clientRange.MaxMinor {
			continue
		}
		for _, serverRange := range server {
			if serverRange.MinMinor > serverRange.MaxMinor || clientRange.Major != serverRange.Major {
				continue
			}

			minimum := max(clientRange.MinMinor, serverRange.MinMinor)
			maximum := min(clientRange.MaxMinor, serverRange.MaxMinor)
			if minimum > maximum {
				continue
			}
			candidate := ProtocolVersion{Major: clientRange.Major, Minor: maximum}
			if !found || candidate.Major > best.Major || candidate.Major == best.Major && candidate.Minor > best.Minor {
				best = candidate
				found = true
			}
		}
	}
	return best, found
}

func intersectFeatures(client []ProtocolFeature, server []ProtocolFeature) []ProtocolFeature {
	clientVersions := highestFeatureVersions(client)
	serverVersions := highestFeatureVersions(server)

	names := make([]string, 0, len(clientVersions))
	for name := range clientVersions {
		if _, ok := serverVersions[name]; ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	var features []ProtocolFeature
	for _, name := range names {
		features = append(features, ProtocolFeature{
			Name:    name,
			Version: min(clientVersions[name], serverVersions[name]),
		})
	}
	return features
}

func highestFeatureVersions(features []ProtocolFeature) map[string]uint16 {
	versions := make(map[string]uint16, len(features))
	for _, feature := range features {
		if feature.Name == "" {
			continue
		}
		if current, ok := versions[feature.Name]; !ok || feature.Version > current {
			versions[feature.Name] = feature.Version
		}
	}
	return versions
}
