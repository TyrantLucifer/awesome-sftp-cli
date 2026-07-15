package domain

import (
	"errors"
	"fmt"
	"sort"
)

type CapabilityName string

type CapabilityConstraint struct {
	Name  string
	Value string
}

type Capability struct {
	Name        CapabilityName
	Version     uint16
	Constraints []CapabilityConstraint
}

type CapabilityRevision struct {
	SessionID  SessionID
	Generation uint64
}

type CapabilitySnapshot struct {
	Revision CapabilityRevision
	Complete bool
	Items    []Capability
}

func NewCapabilitySnapshot(
	revision CapabilityRevision,
	complete bool,
	items []Capability,
) (CapabilitySnapshot, error) {
	if revision.SessionID == "" {
		return CapabilitySnapshot{}, errors.New("create capability snapshot: session ID is empty")
	}
	if revision.Generation == 0 {
		return CapabilitySnapshot{}, errors.New("create capability snapshot: generation must be greater than zero")
	}

	ownedItems := cloneCapabilities(items)
	sort.Slice(ownedItems, func(left int, right int) bool {
		return ownedItems[left].Name < ownedItems[right].Name
	})
	for index := 1; index < len(ownedItems); index++ {
		if ownedItems[index-1].Name == ownedItems[index].Name {
			return CapabilitySnapshot{}, fmt.Errorf(
				"create capability snapshot: duplicate capability %q",
				ownedItems[index].Name,
			)
		}
	}

	return CapabilitySnapshot{
		Revision: revision,
		Complete: complete,
		Items:    ownedItems,
	}, nil
}

func (s CapabilitySnapshot) Lookup(name CapabilityName) (Capability, bool) {
	index := sort.Search(len(s.Items), func(index int) bool {
		return s.Items[index].Name >= name
	})
	if index == len(s.Items) || s.Items[index].Name != name {
		return Capability{}, false
	}

	return cloneCapability(s.Items[index]), true
}

func cloneCapabilities(items []Capability) []Capability {
	if items == nil {
		return nil
	}

	cloned := make([]Capability, len(items))
	for index, item := range items {
		cloned[index] = cloneCapability(item)
	}
	return cloned
}

func cloneCapability(capability Capability) Capability {
	cloned := capability
	if capability.Constraints != nil {
		cloned.Constraints = make([]CapabilityConstraint, len(capability.Constraints))
		copy(cloned.Constraints, capability.Constraints)
	}
	return cloned
}
