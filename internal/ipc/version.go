package ipc

import "math"

const (
	ProtocolMajor uint16 = 1
	ProtocolMinor uint16 = 0
	MaxFrameBytes uint32 = 8 * 1024 * 1024
)

type ProtocolVersion struct {
	Major uint16 `json:"major"`
	Minor uint16 `json:"minor"`
}

type EventCursor struct {
	Epoch    string `json:"epoch"`
	Sequence uint64 `json:"sequence"`
}

type CursorRelation string

const (
	CursorNext        CursorRelation = "next"
	CursorDuplicate   CursorRelation = "duplicate"
	CursorGap         CursorRelation = "gap"
	CursorEpochChange CursorRelation = "epoch_change"
)

func CompareCursor(previous, next EventCursor) CursorRelation {
	if previous.Epoch != next.Epoch {
		return CursorEpochChange
	}
	if previous.Sequence == next.Sequence {
		return CursorDuplicate
	}
	if previous.Sequence != math.MaxUint64 && next.Sequence == previous.Sequence+1 {
		return CursorNext
	}
	return CursorGap
}
