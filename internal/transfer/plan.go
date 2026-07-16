// Package transfer owns frozen operation plans and bounded transfer execution.
package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/job"
	providerapi "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/jobstore"
)

const DefaultBufferBytes = 256 * 1024

var planIDPattern = regexp.MustCompile(`^plan_[a-z2-7]{26}$`)

type ClipboardKind string

const (
	ClipboardCopy ClipboardKind = "copy"
	ClipboardCut  ClipboardKind = "cut"
)

type OperationKind string

const (
	OperationCopy OperationKind = "copy"
	OperationMove OperationKind = "move"
)

type ConflictPolicy string

const (
	ConflictAsk        ConflictPolicy = "ask"
	ConflictOverwrite  ConflictPolicy = "overwrite"
	ConflictSkip       ConflictPolicy = "skip"
	ConflictAutoRename ConflictPolicy = "auto_rename"
)

type Route string

const (
	RouteLocal     Route = "local"
	RouteSFTPRelay Route = "sftp_relay"
)

type Verification string

const VerifySHA256 Verification = "stream_sha256"

type FileRef struct {
	Location           domain.Location           `json:"location"`
	Kind               domain.EntryKind          `json:"kind"`
	Fingerprint        domain.Fingerprint        `json:"fingerprint"`
	CapabilityRevision domain.CapabilityRevision `json:"capability_revision"`
}

type Intent struct {
	Clipboard            ClipboardKind   `json:"clipboard"`
	Source               FileRef         `json:"source"`
	DestinationDirectory domain.Location `json:"destination_directory"`
	Name                 string          `json:"name"`
	ConflictPolicy       ConflictPolicy  `json:"conflict_policy"`
	ConflictConfirmed    bool            `json:"conflict_confirmed"`
}

type CapabilityBinding struct {
	Revision   domain.CapabilityRevision `json:"revision"`
	Capability domain.Capability         `json:"capability"`
}

type Plan struct {
	Version               uint16            `json:"version"`
	PlanID                string            `json:"plan_id"`
	JobID                 domain.JobID      `json:"job_id"`
	Kind                  OperationKind     `json:"kind"`
	SourceEndpoint        domain.Endpoint   `json:"source_endpoint"`
	DestinationEndpoint   domain.Endpoint   `json:"destination_endpoint"`
	Source                FileRef           `json:"source"`
	DestinationDirectory  domain.Location   `json:"destination_directory"`
	RequestedName         string            `json:"requested_name"`
	Final                 domain.Location   `json:"final"`
	Part                  domain.Location   `json:"part"`
	SourceCapability      CapabilityBinding `json:"source_capability"`
	DestinationCapability CapabilityBinding `json:"destination_capability"`
	Route                 Route             `json:"route"`
	Verification          Verification      `json:"verification"`
	ConflictPolicy        ConflictPolicy    `json:"conflict_policy"`
	BufferBytes           uint32            `json:"buffer_bytes"`
	Discovery             *DiscoveryBudget  `json:"discovery,omitempty"`
	FrozenAt              time.Time         `json:"frozen_at"`
}

type FreezeRequest struct {
	Intent    Intent
	RequestID domain.RequestID
	PlanID    string
	JobID     domain.JobID
	EventID   domain.EventID
	Now       time.Time
}

type Resolver interface {
	Resolve(domain.EndpointID) (providerapi.Provider, error)
}

type PlanAcquirer interface {
	Acquire(context.Context, Plan) (release func(), err error)
}

type MapResolver map[domain.EndpointID]providerapi.Provider

func (resolver MapResolver) Resolve(endpointID domain.EndpointID) (providerapi.Provider, error) {
	implementation := resolver[endpointID]
	if implementation == nil {
		return nil, &domain.OpError{
			Code:       domain.CodeNotFound,
			Message:    "endpoint is not registered",
			Operation:  "resolve_provider",
			EndpointID: endpointID,
			Retry:      domain.RetryAdvice{Kind: domain.RetryNever},
			Effect:     domain.EffectNone,
		}
	}
	return implementation, nil
}

type Planner struct{ resolver Resolver }

func NewPlanner(resolver Resolver) *Planner { return &Planner{resolver: resolver} }

func (planner *Planner) Capture(ctx context.Context, location domain.Location) (FileRef, error) {
	implementation, err := planner.resolve(location.EndpointID)
	if err != nil {
		return FileRef{}, err
	}
	snapshot, readCapability, err := requireCapability(ctx, implementation, "capture", location, "read")
	if err != nil {
		return FileRef{}, err
	}
	entry, err := implementation.Stat(ctx, providerapi.StatRequest{Location: location})
	if err != nil {
		return FileRef{}, err
	}
	if entry.Kind != domain.EntryFile && entry.Kind != domain.EntryDirectory {
		return FileRef{}, planError(domain.CodeInvalidArgument, "capture", location, "copy requires a regular file or directory", domain.RetryNever)
	}
	if entry.Fingerprint.Strength() == domain.FingerprintWeak {
		return FileRef{}, planError(domain.CodeUnsupported, "capture", location, "source has no stable fingerprint", domain.RetryAfterReplan)
	}
	_ = readCapability
	return FileRef{
		Location:           entry.Location,
		Kind:               entry.Kind,
		Fingerprint:        cloneFingerprint(entry.Fingerprint),
		CapabilityRevision: snapshot.Capabilities.Revision,
	}, nil
}

func (planner *Planner) FreezeCopy(ctx context.Context, request FreezeRequest) (Plan, jobstore.CreateRequest, error) {
	if err := validateFreezeRequest(request); err != nil {
		return Plan{}, jobstore.CreateRequest{}, err
	}
	sourceProvider, err := planner.resolve(request.Intent.Source.Location.EndpointID)
	if err != nil {
		return Plan{}, jobstore.CreateRequest{}, err
	}
	destinationProvider, err := planner.resolve(request.Intent.DestinationDirectory.EndpointID)
	if err != nil {
		return Plan{}, jobstore.CreateRequest{}, err
	}
	sourceSnapshot, sourceCapability, err := requireCapability(ctx, sourceProvider, "plan_copy", request.Intent.Source.Location, "read")
	if err != nil {
		return Plan{}, jobstore.CreateRequest{}, err
	}
	destinationSnapshot, destinationCapability, err := requireCapability(ctx, destinationProvider, "plan_copy", request.Intent.DestinationDirectory, "write")
	if err != nil {
		return Plan{}, jobstore.CreateRequest{}, err
	}
	if _, ok := destinationProvider.(providerapi.MutableProvider); !ok {
		return Plan{}, jobstore.CreateRequest{}, planError(domain.CodeUnsupported, "plan_copy", request.Intent.DestinationDirectory, "destination has no mutation facet", domain.RetryAfterReplan)
	}

	currentSource, err := sourceProvider.Stat(ctx, providerapi.StatRequest{Location: request.Intent.Source.Location})
	if err != nil {
		return Plan{}, jobstore.CreateRequest{}, err
	}
	if currentSource.Kind != request.Intent.Source.Kind || !reflect.DeepEqual(currentSource.Fingerprint, request.Intent.Source.Fingerprint) ||
		sourceSnapshot.Capabilities.Revision != request.Intent.Source.CapabilityRevision {
		return Plan{}, jobstore.CreateRequest{}, planError(domain.CodeConflict, "plan_copy", request.Intent.Source.Location, "captured source changed before planning", domain.RetryAfterConflict)
	}
	directory, err := destinationProvider.Stat(ctx, providerapi.StatRequest{Location: request.Intent.DestinationDirectory})
	if err != nil {
		return Plan{}, jobstore.CreateRequest{}, err
	}
	if directory.Kind != domain.EntryDirectory {
		return Plan{}, jobstore.CreateRequest{}, planError(domain.CodeInvalidArgument, "plan_copy", request.Intent.DestinationDirectory, "destination is not a directory", domain.RetryNever)
	}

	final := childLocation(request.Intent.DestinationDirectory, request.Intent.Name)
	finalExists, err := locationExists(ctx, destinationProvider, final)
	if err != nil {
		return Plan{}, jobstore.CreateRequest{}, err
	}
	if finalExists && request.Intent.ConflictPolicy == ConflictAutoRename {
		final, err = chooseAutoRename(ctx, destinationProvider, request.Intent.DestinationDirectory, request.Intent.Name)
		if err != nil {
			return Plan{}, jobstore.CreateRequest{}, err
		}
	}
	if request.Intent.Source.Kind == domain.EntryDirectory && final.EndpointID == request.Intent.Source.Location.EndpointID &&
		pathWithin(request.Intent.Source.Location.Path, final.Path) {
		return Plan{}, jobstore.CreateRequest{}, planError(domain.CodeInvalidArgument, "plan_copy", final, "directory destination is inside the frozen source tree", domain.RetryNever)
	}
	part := childLocation(request.Intent.DestinationDirectory, "."+path.Base(string(final.Path))+".part-"+string(request.JobID))
	if exists, err := locationExists(ctx, destinationProvider, part); err != nil {
		return Plan{}, jobstore.CreateRequest{}, err
	} else if exists {
		return Plan{}, jobstore.CreateRequest{}, planError(domain.CodeAlreadyExists, "plan_copy", part, "job part location already exists", domain.RetryAfterConflict)
	}

	kind := OperationCopy
	if request.Intent.Clipboard == ClipboardCut {
		kind = OperationMove
	}
	plan := Plan{
		Version:              1,
		PlanID:               request.PlanID,
		JobID:                request.JobID,
		Kind:                 kind,
		SourceEndpoint:       sourceProvider.Descriptor(),
		DestinationEndpoint:  destinationProvider.Descriptor(),
		Source:               cloneFileRef(request.Intent.Source),
		DestinationDirectory: request.Intent.DestinationDirectory,
		RequestedName:        request.Intent.Name,
		Final:                final,
		Part:                 part,
		SourceCapability: CapabilityBinding{
			Revision:   sourceSnapshot.Capabilities.Revision,
			Capability: sourceCapability,
		},
		DestinationCapability: CapabilityBinding{
			Revision:   destinationSnapshot.Capabilities.Revision,
			Capability: destinationCapability,
		},
		Route:          chooseRoute(sourceProvider.Descriptor(), destinationProvider.Descriptor()),
		Verification:   VerifySHA256,
		ConflictPolicy: request.Intent.ConflictPolicy,
		BufferBytes:    DefaultBufferBytes,
		FrozenAt:       request.Now.UTC().Truncate(time.Second),
	}
	if plan.Source.Kind == domain.EntryDirectory {
		budget := DefaultDiscoveryBudget
		plan.Discovery = &budget
	}
	initialState := job.StateQueued
	if finalExists && (request.Intent.ConflictPolicy == ConflictAsk || request.Intent.ConflictPolicy == ConflictOverwrite && !request.Intent.ConflictConfirmed) {
		initialState = job.StateAwaitingConfirmation
	}
	create, err := createRequest(plan, request, initialState)
	if err != nil {
		return Plan{}, jobstore.CreateRequest{}, err
	}
	return plan, create, nil
}

func (planner *Planner) resolve(endpointID domain.EndpointID) (providerapi.Provider, error) {
	if planner == nil || planner.resolver == nil {
		return nil, errors.New("transfer planner: nil resolver")
	}
	return planner.resolver.Resolve(endpointID)
}

func requireCapability(ctx context.Context, implementation providerapi.Provider, operation string, location domain.Location, name domain.CapabilityName) (domain.EndpointSnapshot, domain.Capability, error) {
	snapshot, err := implementation.Snapshot(ctx)
	if err != nil {
		return domain.EndpointSnapshot{}, domain.Capability{}, err
	}
	if snapshot.State != domain.StateReady && snapshot.State != domain.StateDegraded {
		return domain.EndpointSnapshot{}, domain.Capability{}, planError(domain.CodeTransportInterrupted, operation, location, "endpoint is not operational", domain.RetryAfterReconnect)
	}
	if !snapshot.Capabilities.Complete {
		return domain.EndpointSnapshot{}, domain.Capability{}, planError(domain.CodeCapabilityLost, operation, location, "capability snapshot is incomplete", domain.RetryAfterReplan)
	}
	capability, ok := snapshot.Capabilities.Lookup(name)
	if !ok {
		return domain.EndpointSnapshot{}, domain.Capability{}, planError(domain.CodeUnsupported, operation, location, fmt.Sprintf("endpoint lacks %q capability", name), domain.RetryAfterReplan)
	}
	return snapshot, capability, nil
}

func validateFreezeRequest(request FreezeRequest) error {
	if _, err := domain.ParseRequestID(string(request.RequestID)); err != nil {
		return fmt.Errorf("freeze copy: %w", err)
	}
	if _, err := domain.ParseJobID(string(request.JobID)); err != nil {
		return fmt.Errorf("freeze copy: %w", err)
	}
	if _, err := domain.ParseEventID(string(request.EventID)); err != nil {
		return fmt.Errorf("freeze copy: %w", err)
	}
	if !planIDPattern.MatchString(request.PlanID) || request.Now.Unix() <= 0 {
		return errors.New("freeze copy: invalid plan ID or time")
	}
	if request.Intent.Clipboard != ClipboardCopy && request.Intent.Clipboard != ClipboardCut {
		return errors.New("freeze copy: invalid clipboard kind")
	}
	if (request.Intent.Source.Kind != domain.EntryFile && request.Intent.Source.Kind != domain.EntryDirectory) || request.Intent.Source.Location.EndpointID == "" || request.Intent.Source.Location.Path == "" {
		return errors.New("freeze copy: invalid source reference")
	}
	if request.Intent.DestinationDirectory.EndpointID == "" || request.Intent.DestinationDirectory.Path == "" {
		return errors.New("freeze copy: invalid destination")
	}
	if request.Intent.Name == "" || request.Intent.Name == "." || request.Intent.Name == ".." || path.Base(request.Intent.Name) != request.Intent.Name || strings.IndexByte(request.Intent.Name, 0) >= 0 {
		return errors.New("freeze copy: destination name is not one path component")
	}
	switch request.Intent.ConflictPolicy {
	case ConflictAsk, ConflictOverwrite, ConflictSkip, ConflictAutoRename:
	default:
		return errors.New("freeze copy: invalid conflict policy")
	}
	return nil
}

func createRequest(plan Plan, request FreezeRequest, initialState job.State) (jobstore.CreateRequest, error) {
	sourceJSON, err := json.Marshal(plan.Source)
	if err != nil {
		return jobstore.CreateRequest{}, fmt.Errorf("freeze copy: encode source: %w", err)
	}
	destination := struct {
		Directory domain.Location `json:"directory"`
		Final     domain.Location `json:"final"`
		Part      domain.Location `json:"part"`
	}{Directory: plan.DestinationDirectory, Final: plan.Final, Part: plan.Part}
	destinationJSON, err := json.Marshal(destination)
	if err != nil {
		return jobstore.CreateRequest{}, fmt.Errorf("freeze copy: encode destination: %w", err)
	}
	sourceString := string(sourceJSON)
	destinationString := string(destinationJSON)
	planJSON, err := json.Marshal(plan)
	if err != nil {
		return jobstore.CreateRequest{}, fmt.Errorf("freeze copy: encode frozen plan: %w", err)
	}
	planString := string(planJSON)
	steps := []jobstore.Step{
		{Kind: "transfer", SourceJSON: &sourceString, DestinationJSON: &destinationString},
		{Kind: "verify", SourceJSON: &sourceString, DestinationJSON: &destinationString},
		{Kind: "commit", DestinationJSON: &destinationString},
	}
	if plan.Source.Kind == domain.EntryDirectory {
		steps = []jobstore.Step{
			{Kind: "discover", SourceJSON: &sourceString},
			{Kind: "mkdir", DestinationJSON: &destinationString},
			{Kind: "transfer", SourceJSON: &sourceString, DestinationJSON: &destinationString},
			{Kind: "verify", SourceJSON: &sourceString, DestinationJSON: &destinationString},
			{Kind: "commit", DestinationJSON: &destinationString},
		}
	}
	create := jobstore.CreateRequest{
		PlanID:          plan.PlanID,
		RequestID:       request.RequestID,
		JobID:           plan.JobID,
		Kind:            string(plan.Kind),
		SourceJSON:      sourceString,
		DestinationJSON: &planString,
		Route:           string(plan.Route),
		Verification:    string(plan.Verification),
		ConflictPolicy:  string(plan.ConflictPolicy),
		RiskClass:       "filesystem_write",
		InitialState:    initialState,
		EventID:         request.EventID,
		Now:             request.Now,
		Steps:           steps,
	}
	if initialState == job.StateAwaitingConfirmation {
		create.InitialConflict = &jobstore.ConflictSeed{
			StepIndex: 0, Class: "destination_exists", SourceJSON: sourceString, DestinationJSON: destinationString,
		}
	}
	return create, nil
}

// DecodePlan reconstructs a frozen plan from its durable operation row and
// rejects partial or internally inconsistent records.
func DecodePlan(record jobstore.PlanRecord, jobID domain.JobID) (Plan, error) {
	if record.DestinationJSON == nil {
		return Plan{}, errors.New("decode transfer plan: durable plan payload is absent")
	}
	var plan Plan
	if err := json.Unmarshal([]byte(*record.DestinationJSON), &plan); err != nil {
		return Plan{}, fmt.Errorf("decode transfer plan: %w", err)
	}
	if err := validateExecution(plan); err != nil {
		return Plan{}, fmt.Errorf("decode transfer plan: %w", err)
	}
	sourceJSON, err := json.Marshal(plan.Source)
	if err != nil {
		return Plan{}, fmt.Errorf("decode transfer plan source: %w", err)
	}
	if plan.JobID != jobID || plan.PlanID != record.PlanID || string(sourceJSON) != record.SourceJSON ||
		string(plan.Kind) != record.Kind || string(plan.Route) != record.Route ||
		string(plan.Verification) != record.Verification || string(plan.ConflictPolicy) != record.ConflictPolicy ||
		!plan.FrozenAt.Equal(record.FrozenAt) {
		return Plan{}, errors.New("decode transfer plan: indexed columns disagree with frozen payload")
	}
	return plan, nil
}

func chooseRoute(source, destination domain.Endpoint) Route {
	if source.Kind == domain.EndpointLocal && destination.Kind == domain.EndpointLocal {
		return RouteLocal
	}
	return RouteSFTPRelay
}

func childLocation(directory domain.Location, name string) domain.Location {
	return domain.Location{EndpointID: directory.EndpointID, Path: domain.CanonicalPath(path.Join(string(directory.Path), name))}
}

func pathWithin(root, candidate domain.CanonicalPath) bool {
	rootPath := strings.TrimSuffix(string(root), "/")
	candidatePath := string(candidate)
	if rootPath == "" {
		return strings.HasPrefix(candidatePath, "/")
	}
	return candidatePath == rootPath || strings.HasPrefix(candidatePath, rootPath+"/")
}

func locationExists(ctx context.Context, implementation providerapi.Provider, location domain.Location) (bool, error) {
	_, err := implementation.Stat(ctx, providerapi.StatRequest{Location: location})
	if err == nil {
		return true, nil
	}
	if domain.IsCode(err, domain.CodeNotFound) {
		return false, nil
	}
	return false, err
}

func chooseAutoRename(ctx context.Context, implementation providerapi.Provider, directory domain.Location, name string) (domain.Location, error) {
	extension := path.Ext(name)
	stem := strings.TrimSuffix(name, extension)
	for suffix := 1; suffix <= 1000; suffix++ {
		candidate := childLocation(directory, fmt.Sprintf("%s (%d)%s", stem, suffix, extension))
		exists, err := locationExists(ctx, implementation, candidate)
		if err != nil {
			return domain.Location{}, err
		}
		if !exists {
			return candidate, nil
		}
	}
	return domain.Location{}, planError(domain.CodeResourceExhausted, "plan_copy", directory, "auto-rename candidate budget exhausted", domain.RetryAfterConflict)
}

func cloneFileRef(reference FileRef) FileRef {
	reference.Fingerprint = cloneFingerprint(reference.Fingerprint)
	return reference
}

func cloneFingerprint(value domain.Fingerprint) domain.Fingerprint {
	clone := value
	if value.Size != nil {
		owned := *value.Size
		clone.Size = &owned
	}
	if value.ModifiedAt != nil {
		owned := value.ModifiedAt.UTC()
		clone.ModifiedAt = &owned
	}
	if value.ModifiedPrecision != nil {
		owned := *value.ModifiedPrecision
		clone.ModifiedPrecision = &owned
	}
	if value.FileID != nil {
		owned := *value.FileID
		clone.FileID = &owned
	}
	if value.VersionID != nil {
		owned := *value.VersionID
		clone.VersionID = &owned
	}
	if value.HashAlgorithm != nil {
		owned := *value.HashAlgorithm
		clone.HashAlgorithm = &owned
	}
	if value.HashHex != nil {
		owned := *value.HashHex
		clone.HashHex = &owned
	}
	return clone
}

func planError(code domain.Code, operation string, location domain.Location, message string, retry domain.RetryKind) error {
	owned := location
	return &domain.OpError{
		Code:       code,
		Message:    message,
		Operation:  operation,
		EndpointID: location.EndpointID,
		Location:   &owned,
		Retry:      domain.RetryAdvice{Kind: retry},
		Effect:     domain.EffectNone,
	}
}
