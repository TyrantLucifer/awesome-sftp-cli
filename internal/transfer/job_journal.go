package transfer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/state/jobstore"
)

// JobJournal stores the transfer checkpoint in the Version 1 Job schema. The
// daemon owns the concrete Store; workers receive only this narrow journal.
type JobJournal struct {
	Store     *jobstore.Store
	StepIndex int
	Now       func() time.Time
}

type checkpointLocationPayload struct {
	Part                domain.Location    `json:"part"`
	PartFingerprint     domain.Fingerprint `json:"part_fingerprint"`
	Final               domain.Location    `json:"final"`
	ChecksumHex         string             `json:"checksum_hex,omitempty"`
	Outcome             Outcome            `json:"outcome,omitempty"`
	Items               uint64             `json:"items,omitempty"`
	CurrentPath         string             `json:"current_path,omitempty"`
	DirectoryRootOwned  bool               `json:"directory_root_owned,omitempty"`
	ActualRoute         Route              `json:"actual_route,omitempty"`
	DowngradedFrom      Route              `json:"downgraded_from,omitempty"`
	RouteReason         RouteReason        `json:"route_reason,omitempty"`
	DirectFormatVersion uint16             `json:"direct_format_version,omitempty"`
	DirectNonce         string             `json:"direct_nonce,omitempty"`
}

func (journal JobJournal) Load(ctx context.Context, jobID domain.JobID) (*Checkpoint, error) {
	if journal.Store == nil {
		return nil, errors.New("load transfer checkpoint: nil Job store")
	}
	record, err := journal.Store.GetCheckpoint(ctx, jobID, journal.StepIndex)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var sourceFingerprint domain.Fingerprint
	if err := json.Unmarshal([]byte(record.SourceFingerprint), &sourceFingerprint); err != nil {
		return nil, fmt.Errorf("load transfer checkpoint: decode source fingerprint: %w", err)
	}
	var location checkpointLocationPayload
	if err := json.Unmarshal([]byte(record.PartLocationJSON), &location); err != nil {
		return nil, fmt.Errorf("load transfer checkpoint: decode part identity: %w", err)
	}
	return &Checkpoint{
		JobID:               record.JobID,
		Phase:               Phase(record.Phase),
		Offset:              record.VerifiedOffset,
		SourceFingerprint:   sourceFingerprint,
		Part:                location.Part,
		PartFingerprint:     location.PartFingerprint,
		ChecksumState:       append([]byte(nil), record.ChecksumState...),
		ChecksumHex:         location.ChecksumHex,
		Final:               location.Final,
		Outcome:             location.Outcome,
		Items:               location.Items,
		CurrentPath:         location.CurrentPath,
		DirectoryRootOwned:  location.DirectoryRootOwned,
		ActualRoute:         location.ActualRoute,
		DowngradedFrom:      location.DowngradedFrom,
		RouteReason:         location.RouteReason,
		DirectFormatVersion: location.DirectFormatVersion,
		DirectNonce:         location.DirectNonce,
	}, nil
}

func (journal JobJournal) Save(ctx context.Context, checkpoint Checkpoint) error {
	if journal.Store == nil {
		return errors.New("save transfer checkpoint: nil Job store")
	}
	sourceFingerprint, err := json.Marshal(checkpoint.SourceFingerprint)
	if err != nil {
		return fmt.Errorf("save transfer checkpoint: encode source fingerprint: %w", err)
	}
	location, err := json.Marshal(checkpointLocationPayload{
		Part:                checkpoint.Part,
		PartFingerprint:     checkpoint.PartFingerprint,
		Final:               checkpoint.Final,
		ChecksumHex:         checkpoint.ChecksumHex,
		Outcome:             checkpoint.Outcome,
		Items:               checkpoint.Items,
		CurrentPath:         checkpoint.CurrentPath,
		DirectoryRootOwned:  checkpoint.DirectoryRootOwned,
		ActualRoute:         checkpoint.ActualRoute,
		DowngradedFrom:      checkpoint.DowngradedFrom,
		RouteReason:         checkpoint.RouteReason,
		DirectFormatVersion: checkpoint.DirectFormatVersion,
		DirectNonce:         checkpoint.DirectNonce,
	})
	if err != nil {
		return fmt.Errorf("save transfer checkpoint: encode part identity: %w", err)
	}
	now := time.Now
	if journal.Now != nil {
		now = journal.Now
	}
	return journal.Store.SaveCheckpoint(ctx, jobstore.CheckpointRequest{
		JobID:             checkpoint.JobID,
		StepIndex:         journal.StepIndex,
		Phase:             string(checkpoint.Phase),
		VerifiedOffset:    checkpoint.Offset,
		SourceFingerprint: string(sourceFingerprint),
		PartLocationJSON:  string(location),
		ChecksumState:     append([]byte(nil), checkpoint.ChecksumState...),
		Now:               now(),
	})
}
