package domain

import (
	"errors"
	"strings"
)

type CanonicalPath string

type Location struct {
	EndpointID EndpointID
	Path       CanonicalPath
}

func NewLocation(endpointID EndpointID, path CanonicalPath) (Location, error) {
	if endpointID == "" {
		return Location{}, errors.New("create location: endpoint ID is empty")
	}
	if path == "" {
		return Location{}, errors.New("create location: canonical path is empty")
	}
	if strings.IndexByte(string(path), 0) >= 0 {
		return Location{}, errors.New("create location: canonical path contains NUL")
	}

	return Location{EndpointID: endpointID, Path: path}, nil
}

type NormalizeRequest struct {
	EndpointID EndpointID
	Base       *Location
	Input      string
}
