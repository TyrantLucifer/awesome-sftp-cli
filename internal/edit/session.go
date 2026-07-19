// Package edit defines the persistence-independent Stage 3 edit-session state
// machine. Observing an external process is deliberately separate from making
// an upload decision: an editor or opener exit can never authorize sync-back.
package edit

import (
	"fmt"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/cache"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
)

const (
	sessionIDHexLength  = 32
	sha256HexLength     = 64
	SyncBackPlanVersion = 2
	SyncBackOrigin      = "sync_back"
)

type SessionID string
type Version uint64
type SHA256 string

type Purpose string

const (
	PurposeEditor Purpose = "editor"
	PurposeOpener Purpose = "opener"
)

type State string

const (
	StateReady                      State = "ready"
	StateObserving                  State = "observing"
	StateNoChanges                  State = "no_changes"
	StateAwaitingUploadConfirmation State = "awaiting_upload_confirmation"
	StateRemoteChanged              State = "remote_changed"
	StateConflict                   State = "conflict"
	StateRecoveryRequired           State = "recovery_required"
	StateSyncBackFrozen             State = "sync_back_frozen"
	StateSkipped                    State = "skipped"
)

type Action string

const (
	ActionNoUpload        Action = "no_upload"
	ActionConfirmUpload   Action = "confirm_upload"
	ActionRefreshRemote   Action = "refresh_remote"
	ActionResolveConflict Action = "resolve_conflict"
	ActionRecoverLocal    Action = "recover_local"
)

type ExpectedPresence string

const (
	ExpectedPresent ExpectedPresence = "present"
	ExpectedAbsent  ExpectedPresence = "absent"
)

type RemotePrecondition struct {
	Presence      ExpectedPresence
	Kind          domain.EntryKind
	Fingerprint   domain.Fingerprint
	ContentSHA256 SHA256
}

type Baseline struct {
	SourceEntryID     cache.EntryID
	MaterializationID cache.MaterializationID
	Target            domain.Location
	ExpectedRemote    RemotePrecondition
	LocalSHA256       SHA256
}

type NewSessionRequest struct {
	ID       SessionID
	Purpose  Purpose
	Baseline Baseline
}

type LocalStatus string

const (
	LocalPresent     LocalStatus = "present"
	LocalDeleted     LocalStatus = "deleted"
	LocalUnavailable LocalStatus = "unavailable"
	LocalUnknown     LocalStatus = "unknown"
)

type LocalObservation struct {
	Status LocalStatus
	SHA256 SHA256
}

type RemoteStatus string

const (
	RemotePresent     RemoteStatus = "present"
	RemoteDeleted     RemoteStatus = "deleted"
	RemoteUnavailable RemoteStatus = "unavailable"
	RemoteUnknown     RemoteStatus = "unknown"
)

type RemoteObservation struct {
	Status        RemoteStatus
	Kind          domain.EntryKind
	Fingerprint   domain.Fingerprint
	ContentSHA256 SHA256
	Size          uint64
}

type PostEditorObservation struct {
	Local  LocalObservation
	Remote RemoteObservation
}

type Evaluation struct {
	Action        Action
	LocalChanged  bool
	RemoteChanged bool
}

type DecisionKind string

const (
	DecisionUpload    DecisionKind = "upload"
	DecisionOverwrite DecisionKind = "overwrite"
	DecisionSaveAs    DecisionKind = "save_as"
	DecisionSkip      DecisionKind = "skip"
)

type Decision struct {
	Kind           DecisionKind
	SaveAsTarget   domain.Location
	ObservedRemote RemoteObservation
	AuditReason    string
}

type PreconditionRenewal struct {
	Reason      string
	FromVersion Version
	ToVersion   Version
	Previous    RemotePrecondition
	Renewed     RemotePrecondition
}

type SyncBackRequest struct {
	PlanVersion                     int
	Origin                          string
	SessionID                       SessionID
	SessionVersion                  Version
	SourceEntryID                   cache.EntryID
	MaterializationID               cache.MaterializationID
	Target                          domain.Location
	LocalSHA256                     SHA256
	Decision                        DecisionKind
	OriginalDestinationPrecondition RemotePrecondition
	DestinationPrecondition         RemotePrecondition
	Renewal                         *PreconditionRenewal
}

type Session struct {
	id           SessionID
	purpose      Purpose
	version      Version
	state        State
	baseline     Baseline
	currentLocal SHA256
	lastRemote   RemoteObservation
	evaluation   Evaluation
	frozen       bool
}

// PersistentSession is the bounded, body-free representation required to
// reconstruct the edit state machine after daemon or client restart.
type PersistentSession struct {
	ID           SessionID         `json:"id"`
	Purpose      Purpose           `json:"purpose"`
	Version      Version           `json:"version"`
	State        State             `json:"state"`
	Baseline     Baseline          `json:"baseline"`
	CurrentLocal SHA256            `json:"current_local_sha256,omitempty"`
	LastRemote   RemoteObservation `json:"last_remote"`
	Evaluation   Evaluation        `json:"evaluation"`
	Frozen       bool              `json:"frozen"`
}

func ParseSessionID(value string) (SessionID, error) {
	if err := validateLowerHex("edit session ID", value, sessionIDHexLength); err != nil {
		return "", err
	}
	return SessionID(value), nil
}

func ParseSHA256(value string) (SHA256, error) {
	if err := validateLowerHex("SHA-256", value, sha256HexLength); err != nil {
		return "", err
	}
	return SHA256(value), nil
}

func NewSession(request NewSessionRequest) (Session, error) {
	if _, err := ParseSessionID(string(request.ID)); err != nil {
		return Session{}, fmt.Errorf("create edit session: %w", err)
	}
	switch request.Purpose {
	case PurposeEditor, PurposeOpener:
	default:
		return Session{}, fmt.Errorf("create edit session: unknown purpose %q", request.Purpose)
	}
	if err := validateBaseline(request.Baseline); err != nil {
		return Session{}, err
	}
	return Session{
		id:       request.ID,
		purpose:  request.Purpose,
		version:  1,
		state:    StateReady,
		baseline: cloneBaseline(request.Baseline),
	}, nil
}

func (session Session) ID() SessionID              { return session.id }
func (session Session) Purpose() Purpose           { return session.purpose }
func (session Session) Version() Version           { return session.version }
func (session Session) State() State               { return session.state }
func (session Session) HasFrozenSyncBack() bool    { return session.frozen }
func (session Session) Baseline() Baseline         { return cloneBaseline(session.baseline) }
func (session Session) CurrentLocalSHA256() SHA256 { return session.currentLocal }
func (session Session) LastRemoteObservation() RemoteObservation {
	return cloneRemoteObservation(session.lastRemote)
}
func (session Session) Evaluation() Evaluation { return session.evaluation }

func (session Session) Persistent() PersistentSession {
	return PersistentSession{
		ID: session.id, Purpose: session.purpose, Version: session.version, State: session.state,
		Baseline: cloneBaseline(session.baseline), CurrentLocal: session.currentLocal,
		LastRemote: cloneRemoteObservation(session.lastRemote), Evaluation: session.evaluation, Frozen: session.frozen,
	}
}

func RestorePersistent(value PersistentSession) (Session, error) {
	base, err := NewSession(NewSessionRequest{ID: value.ID, Purpose: value.Purpose, Baseline: value.Baseline})
	if err != nil {
		return Session{}, fmt.Errorf("restore edit session: %w", err)
	}
	if value.Version < 1 || !knownState(value.State) {
		return Session{}, fmt.Errorf("restore edit session: invalid version or state")
	}
	if value.CurrentLocal != "" {
		if _, err := ParseSHA256(string(value.CurrentLocal)); err != nil {
			return Session{}, fmt.Errorf("restore edit session: %w", err)
		}
	}
	if err := validatePersistentState(value); err != nil {
		return Session{}, err
	}
	base.version = value.Version
	base.state = value.State
	base.currentLocal = value.CurrentLocal
	base.lastRemote = cloneRemoteObservation(value.LastRemote)
	base.evaluation = value.Evaluation
	base.frozen = value.Frozen
	return base, nil
}

func validatePersistentState(value PersistentSession) error {
	zeroRemote := RemoteObservation{}
	zeroEvaluation := Evaluation{}
	switch value.State {
	case StateReady:
		if value.Version != 1 || value.CurrentLocal != "" || value.LastRemote != zeroRemote || value.Evaluation != zeroEvaluation || value.Frozen {
			return fmt.Errorf("restore edit session: contradictory ready state")
		}
	case StateObserving:
		if value.Version != 2 || value.CurrentLocal != "" || value.LastRemote != zeroRemote || value.Evaluation != zeroEvaluation || value.Frozen {
			return fmt.Errorf("restore edit session: contradictory observing state")
		}
	case StateNoChanges:
		if value.Version != 3 || value.CurrentLocal == "" || value.Evaluation != (Evaluation{Action: ActionNoUpload}) || value.Frozen {
			return fmt.Errorf("restore edit session: contradictory no-change state")
		}
	case StateAwaitingUploadConfirmation:
		if value.Version != 3 || value.CurrentLocal == "" || value.Evaluation != (Evaluation{Action: ActionConfirmUpload, LocalChanged: true}) || value.Frozen {
			return fmt.Errorf("restore edit session: contradictory upload-confirmation state")
		}
	case StateRemoteChanged:
		if value.Version != 3 || value.CurrentLocal == "" || value.Evaluation != (Evaluation{Action: ActionRefreshRemote, RemoteChanged: true}) || value.Frozen {
			return fmt.Errorf("restore edit session: contradictory remote-change state")
		}
	case StateConflict:
		if value.Version != 3 || value.CurrentLocal == "" || value.Evaluation != (Evaluation{Action: ActionResolveConflict, LocalChanged: true, RemoteChanged: true}) || value.Frozen {
			return fmt.Errorf("restore edit session: contradictory conflict state")
		}
	case StateRecoveryRequired:
		if value.Version != 3 || value.Evaluation.Action != ActionRecoverLocal || value.Frozen {
			return fmt.Errorf("restore edit session: contradictory recovery state")
		}
	case StateSyncBackFrozen:
		if value.Version != 4 || value.CurrentLocal == "" || !value.Frozen || value.Evaluation.Action != ActionConfirmUpload && value.Evaluation.Action != ActionResolveConflict {
			return fmt.Errorf("restore edit session: contradictory frozen sync-back state")
		}
	case StateSkipped:
		if value.Version != 4 || value.Frozen || !decisionStateForAction(value.Evaluation.Action) {
			return fmt.Errorf("restore edit session: contradictory skipped state")
		}
	default:
		return fmt.Errorf("restore edit session: unknown state %q", value.State)
	}
	return nil
}

func decisionStateForAction(action Action) bool {
	switch action {
	case ActionNoUpload, ActionConfirmUpload, ActionRefreshRemote, ActionResolveConflict, ActionRecoverLocal:
		return true
	default:
		return false
	}
}

func knownState(value State) bool {
	switch value {
	case StateReady, StateObserving, StateNoChanges, StateAwaitingUploadConfirmation, StateRemoteChanged,
		StateConflict, StateRecoveryRequired, StateSyncBackFrozen, StateSkipped:
		return true
	default:
		return false
	}
}

func (session Session) BeginObservation(expectedVersion Version) (Session, error) {
	if err := session.requireVersion(expectedVersion); err != nil {
		return Session{}, err
	}
	if session.state != StateReady {
		return Session{}, transitionError(session.state, "begin observation")
	}
	next := session
	next.state = StateObserving
	next.version++
	return next, nil
}

func (session Session) Observe(expectedVersion Version, observation PostEditorObservation) (Session, Evaluation, error) {
	if err := session.requireVersion(expectedVersion); err != nil {
		return Session{}, Evaluation{}, err
	}
	if session.state != StateObserving {
		return Session{}, Evaluation{}, transitionError(session.state, "record post-editor observation")
	}

	result, state, currentLocal := evaluate(session.baseline, observation)
	next := session
	next.version++
	next.state = state
	next.currentLocal = currentLocal
	next.lastRemote = cloneRemoteObservation(observation.Remote)
	next.evaluation = result
	return next, result, nil
}

func (session Session) Decide(expectedVersion Version, decision Decision) (Session, *SyncBackRequest, error) {
	if err := session.requireVersion(expectedVersion); err != nil {
		return Session{}, nil, err
	}

	switch decision.Kind {
	case DecisionSkip:
		if !decisionState(session.state) {
			return Session{}, nil, transitionError(session.state, "skip edit")
		}
		next := session
		next.version++
		next.state = StateSkipped
		return next, nil, nil
	case DecisionUpload:
		if session.state != StateAwaitingUploadConfirmation {
			return Session{}, nil, transitionError(session.state, "confirm upload")
		}
		return session.freeze(decision.Kind, session.baseline.Target, session.baseline.ExpectedRemote, nil)
	case DecisionSaveAs:
		if session.state != StateAwaitingUploadConfirmation && session.state != StateConflict {
			return Session{}, nil, transitionError(session.state, "save as")
		}
		if _, err := domain.NewLocation(decision.SaveAsTarget.EndpointID, decision.SaveAsTarget.Path); err != nil {
			return Session{}, nil, fmt.Errorf("save edit as: %w", err)
		}
		return session.freeze(decision.Kind, decision.SaveAsTarget, RemotePrecondition{Presence: ExpectedAbsent}, nil)
	case DecisionOverwrite:
		if session.state != StateConflict {
			return Session{}, nil, transitionError(session.state, "overwrite conflict")
		}
		if err := validateAuditReason(decision.AuditReason); err != nil {
			return Session{}, nil, err
		}
		renewed, err := preconditionFromObservation(decision.ObservedRemote)
		if err != nil {
			return Session{}, nil, fmt.Errorf("overwrite conflict: %w", err)
		}
		nextVersion := session.version + 1
		renewal := &PreconditionRenewal{
			Reason:      decision.AuditReason,
			FromVersion: session.version,
			ToVersion:   nextVersion,
			Previous:    clonePrecondition(session.baseline.ExpectedRemote),
			Renewed:     clonePrecondition(renewed),
		}
		return session.freeze(decision.Kind, session.baseline.Target, renewed, renewal)
	default:
		return Session{}, nil, fmt.Errorf("decide edit session: unknown decision %q", decision.Kind)
	}
}

func (session Session) freeze(kind DecisionKind, target domain.Location, effective RemotePrecondition, renewal *PreconditionRenewal) (Session, *SyncBackRequest, error) {
	if _, err := ParseSHA256(string(session.currentLocal)); err != nil {
		return Session{}, nil, fmt.Errorf("freeze sync-back: current local SHA-256 is uncertain: %w", err)
	}
	next := session
	next.version++
	next.state = StateSyncBackFrozen
	next.frozen = true
	request := &SyncBackRequest{
		PlanVersion:                     SyncBackPlanVersion,
		Origin:                          SyncBackOrigin,
		SessionID:                       session.id,
		SessionVersion:                  next.version,
		SourceEntryID:                   session.baseline.SourceEntryID,
		MaterializationID:               session.baseline.MaterializationID,
		Target:                          target,
		LocalSHA256:                     session.currentLocal,
		Decision:                        kind,
		OriginalDestinationPrecondition: clonePrecondition(session.baseline.ExpectedRemote),
		DestinationPrecondition:         clonePrecondition(effective),
		Renewal:                         cloneRenewal(renewal),
	}
	return next, request, nil
}

func evaluate(baseline Baseline, observation PostEditorObservation) (Evaluation, State, SHA256) {
	localChanged, localReliable, currentLocal := compareLocal(baseline.LocalSHA256, observation.Local)
	remoteChanged, remoteReliable := compareRemote(baseline.ExpectedRemote, observation.Remote)
	result := Evaluation{LocalChanged: localChanged, RemoteChanged: remoteChanged}

	if !localReliable {
		result.Action = ActionRecoverLocal
		return result, StateRecoveryRequired, ""
	}
	if !remoteReliable {
		result.Action = ActionRecoverLocal
		return result, StateRecoveryRequired, currentLocal
	}
	if !localChanged && !remoteChanged {
		result.Action = ActionNoUpload
		return result, StateNoChanges, currentLocal
	}
	if localChanged && !remoteChanged {
		result.Action = ActionConfirmUpload
		return result, StateAwaitingUploadConfirmation, currentLocal
	}
	if !localChanged {
		result.Action = ActionRefreshRemote
		return result, StateRemoteChanged, currentLocal
	}
	result.Action = ActionResolveConflict
	return result, StateConflict, currentLocal
}

func compareLocal(baseline SHA256, observation LocalObservation) (changed bool, reliable bool, current SHA256) {
	if observation.Status != LocalPresent {
		return observation.Status == LocalDeleted, false, ""
	}
	if _, err := ParseSHA256(string(observation.SHA256)); err != nil {
		return false, false, ""
	}
	return observation.SHA256 != baseline, true, observation.SHA256
}

func compareRemote(baseline RemotePrecondition, observation RemoteObservation) (changed bool, reliable bool) {
	switch observation.Status {
	case RemoteDeleted:
		return baseline.Presence != ExpectedAbsent, true
	case RemotePresent:
		if knownEntryKind(observation.Kind) && baseline.Presence == ExpectedPresent && observation.Kind != baseline.Kind {
			return true, true
		}
		if !reliableRemoteIdentity(observation.Kind, observation.Fingerprint) {
			return false, false
		}
		if baseline.Presence != ExpectedPresent {
			return true, true
		}
		if observation.Kind == domain.EntryFile {
			if _, err := ParseSHA256(string(baseline.ContentSHA256)); err != nil {
				return false, false
			}
			if _, err := ParseSHA256(string(observation.ContentSHA256)); err != nil {
				return false, false
			}
		}
		return !fingerprintEqual(observation.Fingerprint, baseline.Fingerprint) || observation.ContentSHA256 != baseline.ContentSHA256, true
	default:
		return false, false
	}
}

func preconditionFromObservation(observation RemoteObservation) (RemotePrecondition, error) {
	switch observation.Status {
	case RemoteDeleted:
		return RemotePrecondition{Presence: ExpectedAbsent}, nil
	case RemotePresent:
		if !reliableRemoteIdentity(observation.Kind, observation.Fingerprint) {
			return RemotePrecondition{}, fmt.Errorf("observed remote identity is uncertain")
		}
		if observation.Kind == domain.EntryFile {
			if _, err := ParseSHA256(string(observation.ContentSHA256)); err != nil {
				return RemotePrecondition{}, fmt.Errorf("observed remote content identity is uncertain")
			}
		}
		return RemotePrecondition{Presence: ExpectedPresent, Kind: observation.Kind, Fingerprint: cloneFingerprint(observation.Fingerprint), ContentSHA256: observation.ContentSHA256}, nil
	default:
		return RemotePrecondition{}, fmt.Errorf("observed remote identity is unavailable")
	}
}

func validateBaseline(baseline Baseline) error {
	if _, err := cache.ParseEntryID(string(baseline.SourceEntryID)); err != nil {
		return fmt.Errorf("create edit session baseline: %w", err)
	}
	if _, err := cache.ParseMaterializationID(string(baseline.MaterializationID)); err != nil {
		return fmt.Errorf("create edit session baseline: %w", err)
	}
	if _, err := domain.NewLocation(baseline.Target.EndpointID, baseline.Target.Path); err != nil {
		return fmt.Errorf("create edit session baseline target: %w", err)
	}
	if _, err := ParseSHA256(string(baseline.LocalSHA256)); err != nil {
		return fmt.Errorf("create edit session baseline: %w", err)
	}
	if baseline.ExpectedRemote.Presence != ExpectedPresent || baseline.ExpectedRemote.Kind != domain.EntryFile || !reliableRemoteIdentity(baseline.ExpectedRemote.Kind, baseline.ExpectedRemote.Fingerprint) {
		return fmt.Errorf("create edit session baseline: expected remote identity is not reliably present")
	}
	if baseline.ExpectedRemote.ContentSHA256 != "" {
		if _, err := ParseSHA256(string(baseline.ExpectedRemote.ContentSHA256)); err != nil {
			return fmt.Errorf("create edit session baseline: expected remote content identity is uncertain: %w", err)
		}
	}
	return nil
}

func reliableRemoteIdentity(kind domain.EntryKind, fingerprint domain.Fingerprint) bool {
	if !knownEntryKind(kind) {
		return false
	}
	return fingerprint.Strength() != domain.FingerprintWeak
}

func knownEntryKind(kind domain.EntryKind) bool {
	switch kind {
	case domain.EntryFile, domain.EntryDirectory, domain.EntrySymlink, domain.EntryOther:
		return true
	default:
		return false
	}
}

func fingerprintEqual(left, right domain.Fingerprint) bool {
	return optionalEqual(left.Size, right.Size) &&
		optionalTimeEqual(left.ModifiedAt, right.ModifiedAt) &&
		optionalEqual(left.ModifiedPrecision, right.ModifiedPrecision) &&
		optionalEqual(left.FileID, right.FileID) &&
		optionalEqual(left.VersionID, right.VersionID) &&
		optionalEqual(left.HashAlgorithm, right.HashAlgorithm) &&
		optionalEqual(left.HashHex, right.HashHex)
}

func optionalEqual[T comparable](left, right *T) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func optionalTimeEqual(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func validateAuditReason(reason string) error {
	if reason == "" || len(reason) > 256 {
		return fmt.Errorf("overwrite conflict: audit reason length must be in [1,256]")
	}
	for _, character := range reason {
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf("overwrite conflict: audit reason contains control characters")
		}
	}
	return nil
}

func (session Session) requireVersion(expected Version) error {
	if expected != session.version {
		return fmt.Errorf("edit session %q version conflict: have %d, expected %d", session.id, session.version, expected)
	}
	return nil
}

func decisionState(state State) bool {
	switch state {
	case StateNoChanges, StateAwaitingUploadConfirmation, StateRemoteChanged, StateConflict, StateRecoveryRequired:
		return true
	default:
		return false
	}
}

func transitionError(state State, action string) error {
	return fmt.Errorf("edit session state %q cannot %s", state, action)
}

func validateLowerHex(label, value string, width int) error {
	if len(value) != width {
		return fmt.Errorf("parse %s: expected %d lowercase hexadecimal characters", label, width)
	}
	for index := range value {
		character := value[index]
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return fmt.Errorf("parse %s: expected %d lowercase hexadecimal characters", label, width)
		}
	}
	return nil
}

func cloneBaseline(baseline Baseline) Baseline {
	baseline.ExpectedRemote = clonePrecondition(baseline.ExpectedRemote)
	return baseline
}

func clonePrecondition(precondition RemotePrecondition) RemotePrecondition {
	precondition.Fingerprint = cloneFingerprint(precondition.Fingerprint)
	return precondition
}

func cloneRemoteObservation(observation RemoteObservation) RemoteObservation {
	observation.Fingerprint = cloneFingerprint(observation.Fingerprint)
	return observation
}

func cloneRenewal(renewal *PreconditionRenewal) *PreconditionRenewal {
	if renewal == nil {
		return nil
	}
	copy := *renewal
	copy.Previous = clonePrecondition(renewal.Previous)
	copy.Renewed = clonePrecondition(renewal.Renewed)
	return &copy
}

func cloneFingerprint(fingerprint domain.Fingerprint) domain.Fingerprint {
	copy := fingerprint
	copy.Size = cloneValue(fingerprint.Size)
	copy.ModifiedAt = cloneTime(fingerprint.ModifiedAt)
	copy.ModifiedPrecision = cloneValue(fingerprint.ModifiedPrecision)
	copy.FileID = cloneValue(fingerprint.FileID)
	copy.VersionID = cloneValue(fingerprint.VersionID)
	copy.HashAlgorithm = cloneValue(fingerprint.HashAlgorithm)
	copy.HashHex = cloneValue(fingerprint.HashHex)
	return copy
}

func cloneValue[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
