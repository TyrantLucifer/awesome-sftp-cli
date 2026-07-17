package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/daemon"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/jobstore"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transfer"
)

const (
	JobCLIOutputVersion = PublicCLIContractVersion
	defaultJobCLILimit  = 50
	maxJobCLILimit      = 100
)

type jobRPC interface {
	Call(context.Context, string, any, any) error
}

type jobRPCConnector func(context.Context) (jobRPC, io.Closer, error)

type jobCommandOptions struct {
	command       string
	jobID         domain.JobID
	limit         int
	afterSequence int64
	format        string
	confirmation  string
}

func runJob(ctx context.Context, args []string, stdout io.Writer, _ io.Writer) error {
	return runJobWithConnector(ctx, args, stdout, func(ctx context.Context) (jobRPC, io.Closer, error) {
		paths, purpose, err := runtimePaths()
		if err != nil {
			return nil, nil, NewExitError(ExitConfig, fmt.Errorf("resolve runtime paths: %w", err))
		}
		client, err := connectDaemon(ctx, paths, purpose)
		if err != nil {
			return nil, nil, classifyJobConnectionError(fmt.Errorf("connect to daemon: %w", err))
		}
		return client, client, nil
	})
}

func runJobCommand(ctx context.Context, args []string, stdout io.Writer, rpc jobRPC) error {
	options, err := parseJobCommand(args)
	if err != nil {
		return NewExitError(ExitUsage, err)
	}
	return executeJobCommand(ctx, options, stdout, rpc)
}

func runJobWithConnector(ctx context.Context, args []string, stdout io.Writer, connector jobRPCConnector) error {
	options, err := parseJobCommand(args)
	if err != nil {
		return jobCommandError(args, NewExitError(ExitUsage, err))
	}
	if connector == nil {
		return jobCommandError(args, NewExitError(ExitInternal, errors.New("job RPC connector is not configured")))
	}
	rpc, closer, err := connector(ctx)
	if err != nil {
		return jobCommandError(args, err)
	}
	if closer == nil {
		return jobCommandError(args, NewExitError(ExitInternal, errors.New("job RPC connector returned no closer")))
	}
	commandErr := executeJobCommand(ctx, options, stdout, rpc)
	closeErr := closer.Close()
	if closeErr != nil {
		closeErr = NewExitError(ExitInternal, fmt.Errorf("close Job RPC client: %w", closeErr))
	}
	return jobCommandError(args, errors.Join(commandErr, closeErr))
}

func executeJobCommand(ctx context.Context, options jobCommandOptions, stdout io.Writer, rpc jobRPC) error {
	if rpc == nil {
		return NewExitError(ExitInternal, errors.New("job RPC client is not configured"))
	}

	switch options.command {
	case "list":
		var response daemon.JobListResponse
		if err := rpc.Call(ctx, daemon.JobList, daemon.JobListRequest{Limit: options.limit}, &response); err != nil {
			return classifyJobCLIError(err)
		}
		if options.format == "json" {
			return writeJobJSON(stdout, struct {
				OutputVersion int             `json:"output_version"`
				Jobs          []jobViewOutput `json:"jobs"`
			}{JobCLIOutputVersion, jobViewOutputs(response.Jobs)})
		}
		return writeHumanJobList(stdout, response.Jobs)
	case "events":
		var response daemon.JobEventsResponse
		request := daemon.JobEventsRequest{JobID: options.jobID, AfterSequence: options.afterSequence, Limit: options.limit}
		if err := rpc.Call(ctx, daemon.JobEvents, request, &response); err != nil {
			return classifyJobCLIError(err)
		}
		if options.format == "json" {
			events, err := jobEventOutputs(response.Events)
			if err != nil {
				return NewExitError(ExitInternal, err)
			}
			return writeJobJSON(stdout, struct {
				OutputVersion int              `json:"output_version"`
				Events        []jobEventOutput `json:"events"`
			}{JobCLIOutputVersion, events})
		}
		return writeHumanJobEvents(stdout, response.Events)
	case "pause", "resume", "cancel":
		name := map[string]string{"pause": daemon.JobPause, "resume": daemon.JobResume, "cancel": daemon.JobCancel}[options.command]
		var response daemon.JobSnapshotResponse
		if err := rpc.Call(ctx, name, daemon.JobControlRequest{JobID: options.jobID}, &response); err != nil {
			return classifyJobCLIError(err)
		}
		if options.format == "json" {
			return writeJobJSON(stdout, struct {
				OutputVersion int               `json:"output_version"`
				Snapshot      jobSnapshotOutput `json:"snapshot"`
			}{JobCLIOutputVersion, newJobSnapshotOutput(response.Snapshot)})
		}
		if _, err := fmt.Fprintf(stdout, "%s\t%s\n", response.Snapshot.JobID, response.Snapshot.State); err != nil {
			return NewExitError(ExitInternal, fmt.Errorf("write Job output: %w", err))
		}
		return nil
	default:
		return NewExitError(ExitInternal, errors.New("unreachable Job command"))
	}
}

type jobSnapshotOutput struct {
	JobID             domain.JobID `json:"job_id"`
	PlanID            string       `json:"plan_id"`
	State             string       `json:"state"`
	StateVersion      int64        `json:"state_version"`
	NextEventSequence int64        `json:"next_event_sequence"`
	PauseRequested    bool         `json:"pause_requested"`
	CancelRequested   bool         `json:"cancel_requested"`
	RetryAt           *time.Time   `json:"retry_at"`
	TerminalSummary   *string      `json:"terminal_summary"`
	CreatedAt         time.Time    `json:"created_at"`
	UpdatedAt         time.Time    `json:"updated_at"`
}

type jobLocationOutput struct {
	EndpointID domain.EndpointID `json:"endpoint_id"`
	Path       string            `json:"path"`
}

type jobViewOutput struct {
	Snapshot       jobSnapshotOutput `json:"snapshot"`
	Kind           string            `json:"kind"`
	Route          string            `json:"route"`
	PlannedRoute   string            `json:"planned_route"`
	DowngradedFrom string            `json:"downgraded_from"`
	RouteReason    string            `json:"route_reason"`
	Source         jobLocationOutput `json:"source"`
	Final          jobLocationOutput `json:"final"`
	Phase          string            `json:"phase"`
	Bytes          uint64            `json:"bytes"`
	BytesTotal     *uint64           `json:"bytes_total"`
	Items          uint64            `json:"items"`
	WaitingReason  string            `json:"waiting_reason"`
	RecentError    string            `json:"recent_error"`
	RecoveryResult string            `json:"recovery_result"`
}

type jobEventOutput struct {
	JobID     domain.JobID    `json:"job_id"`
	Sequence  int64           `json:"sequence"`
	EventID   domain.EventID  `json:"event_id"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

func newJobSnapshotOutput(snapshot jobstore.Snapshot) jobSnapshotOutput {
	return jobSnapshotOutput{
		JobID:             snapshot.JobID,
		PlanID:            snapshot.PlanID,
		State:             string(snapshot.State),
		StateVersion:      snapshot.StateVersion,
		NextEventSequence: snapshot.NextEventSequence,
		PauseRequested:    snapshot.PauseRequested,
		CancelRequested:   snapshot.CancelRequested,
		RetryAt:           snapshot.RetryAt,
		TerminalSummary:   snapshot.TerminalSummary,
		CreatedAt:         snapshot.CreatedAt,
		UpdatedAt:         snapshot.UpdatedAt,
	}
}

func newJobLocationOutput(location domain.Location) jobLocationOutput {
	return jobLocationOutput{EndpointID: location.EndpointID, Path: string(location.Path)}
}

func jobViewOutputs(views []transfer.JobView) []jobViewOutput {
	outputs := make([]jobViewOutput, 0, len(views))
	for _, view := range views {
		outputs = append(outputs, jobViewOutput{
			Snapshot:       newJobSnapshotOutput(view.Snapshot),
			Kind:           string(view.Kind),
			Route:          string(view.Route),
			PlannedRoute:   string(view.PlannedRoute),
			DowngradedFrom: string(view.DowngradedFrom),
			RouteReason:    string(view.RouteReason),
			Source:         newJobLocationOutput(view.Source),
			Final:          newJobLocationOutput(view.Final),
			Phase:          string(view.Phase),
			Bytes:          view.Bytes,
			BytesTotal:     view.BytesTotal,
			Items:          view.Items,
			WaitingReason:  view.WaitingReason,
			RecentError:    view.RecentError,
			RecoveryResult: view.RecoveryResult,
		})
	}
	return outputs
}

func jobEventOutputs(events []jobstore.EventRecord) ([]jobEventOutput, error) {
	outputs := make([]jobEventOutput, 0, len(events))
	for _, event := range events {
		payload := json.RawMessage(event.PayloadJSON)
		if !json.Valid(payload) {
			return nil, fmt.Errorf("job event %d has invalid stored JSON payload", event.Sequence)
		}
		outputs = append(outputs, jobEventOutput{
			JobID: event.JobID, Sequence: event.Sequence, EventID: event.EventID,
			Kind: event.Kind, Payload: payload, CreatedAt: event.CreatedAt,
		})
	}
	return outputs, nil
}

func parseJobCommand(args []string) (jobCommandOptions, error) {
	options := jobCommandOptions{limit: defaultJobCLILimit, format: "human"}
	if len(args) == 0 {
		return options, errors.New("job requires one of: list, events, pause, resume, cancel")
	}
	options.command = args[0]
	if options.command != "list" && options.command != "events" && options.command != "pause" && options.command != "resume" && options.command != "cancel" {
		return options, fmt.Errorf("unknown job command %q", options.command)
	}

	seen := make(map[string]bool)
	positional := make([]string, 0, 1)
	for index := 1; index < len(args); index++ {
		argument := args[index]
		if !strings.HasPrefix(argument, "--") {
			positional = append(positional, argument)
			continue
		}
		if argument != "--limit" && argument != "--after" && argument != "--format" && argument != "--confirm" {
			return options, fmt.Errorf("unknown job option %q", argument)
		}
		if seen[argument] {
			return options, fmt.Errorf("job option %q may be provided only once", argument)
		}
		seen[argument] = true
		if index+1 >= len(args) {
			return options, fmt.Errorf("job option %q requires a value", argument)
		}
		index++
		value := args[index]
		switch argument {
		case "--limit":
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 1 || parsed > maxJobCLILimit {
				return options, fmt.Errorf("job --limit must be an integer from 1 through %d", maxJobCLILimit)
			}
			options.limit = parsed
		case "--after":
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil || parsed < 0 {
				return options, errors.New("job --after must be a non-negative integer")
			}
			options.afterSequence = parsed
		case "--format":
			if value != "human" && value != "json" {
				return options, errors.New("job --format must be human or json")
			}
			options.format = value
		case "--confirm":
			options.confirmation = value
		}
	}

	if options.command == "list" {
		if len(positional) != 0 || seen["--after"] || seen["--confirm"] {
			return options, errors.New("job list accepts only --limit and --format")
		}
		return options, nil
	}
	if len(positional) != 1 {
		return options, fmt.Errorf("job %s requires exactly one Job ID", options.command)
	}
	jobID, err := domain.ParseJobID(positional[0])
	if err != nil {
		return options, fmt.Errorf("job %s Job ID: %w", options.command, err)
	}
	options.jobID = jobID

	switch options.command {
	case "events":
		if seen["--confirm"] {
			return options, errors.New("job events does not accept --confirm")
		}
	case "pause", "resume":
		if seen["--limit"] || seen["--after"] || seen["--confirm"] {
			return options, fmt.Errorf("job %s accepts only a Job ID and --format", options.command)
		}
	case "cancel":
		if seen["--limit"] || seen["--after"] {
			return options, errors.New("job cancel accepts only a Job ID, --confirm, and --format")
		}
		if options.confirmation != string(jobID) {
			return options, fmt.Errorf("job cancel requires --confirm %s", jobID)
		}
	}
	return options, nil
}

func writeJobJSON(stdout io.Writer, value any) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return NewExitError(ExitInternal, fmt.Errorf("write Job JSON: %w", err))
	}
	return nil
}

func writeHumanJobList(stdout io.Writer, jobs []transfer.JobView) error {
	if len(jobs) == 0 {
		_, err := io.WriteString(stdout, "No Jobs.\n")
		return classifyJobOutputError(err)
	}
	if _, err := io.WriteString(stdout, "JOB_ID\tSTATE\tKIND\tROUTE\tBYTES\tTOTAL\n"); err != nil {
		return classifyJobOutputError(err)
	}
	for _, view := range jobs {
		total := "-"
		if view.BytesTotal != nil {
			total = strconv.FormatUint(*view.BytesTotal, 10)
		}
		if _, err := fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%d\t%s\n", view.Snapshot.JobID, view.Snapshot.State, view.Kind, view.Route, view.Bytes, total); err != nil {
			return classifyJobOutputError(err)
		}
	}
	return nil
}

func writeHumanJobEvents(stdout io.Writer, events []jobstore.EventRecord) error {
	if len(events) == 0 {
		_, err := io.WriteString(stdout, "No Job events.\n")
		return classifyJobOutputError(err)
	}
	if _, err := io.WriteString(stdout, "JOB_ID\tSEQUENCE\tCREATED_AT\tKIND\tPAYLOAD\n"); err != nil {
		return classifyJobOutputError(err)
	}
	for _, event := range events {
		createdAt := event.CreatedAt.UTC().Format(time.RFC3339Nano)
		if _, err := fmt.Fprintf(stdout, "%s\t%d\t%s\t%s\t%s\n", event.JobID, event.Sequence, createdAt, event.Kind, event.PayloadJSON); err != nil {
			return classifyJobOutputError(err)
		}
	}
	return nil
}

func classifyJobOutputError(err error) error {
	if err == nil {
		return nil
	}
	return NewExitError(ExitInternal, fmt.Errorf("write Job output: %w", err))
}

func classifyJobCLIError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return NewExitError(ExitCanceled, err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return NewExitError(ExitNetwork, err)
	}
	summary := daemon.DiagnosticSummary(err)
	code := ExitInternal
	switch summary.ErrorCode {
	case domain.CodeInvalidArgument, domain.CodeUnsupported:
		code = ExitUsage
	case domain.CodePermissionDenied, domain.CodeAuthRequired:
		code = ExitAuthentication
	case domain.CodeTransportInterrupted, domain.CodeTimeout, domain.CodeCapabilityLost, domain.CodeProtocolIncompatible:
		code = ExitNetwork
	case domain.CodeNotFound, domain.CodeAlreadyExists, domain.CodeConflict:
		code = ExitConflict
	case domain.CodeCanceled:
		code = ExitCanceled
	}
	return NewExitError(code, err)
}

func classifyJobConnectionError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return NewExitError(ExitCanceled, err)
	}
	return NewExitError(ExitNetwork, err)
}

type jobMachineError struct {
	err      error
	envelope jobErrorEnvelope
}

type jobErrorEnvelope struct {
	OutputVersion int                `json:"output_version"`
	Error         jobErrorDescriptor `json:"error"`
}

type jobErrorDescriptor struct {
	ExitCode  int                 `json:"exit_code"`
	Class     string              `json:"class"`
	Message   string              `json:"message"`
	RequestID domain.RequestID    `json:"request_id"`
	ErrorCode domain.Code         `json:"error_code"`
	Retry     domain.RetryKind    `json:"retry"`
	Effect    domain.EffectStatus `json:"effect"`
}

func (err *jobMachineError) Error() string { return err.err.Error() }

func (err *jobMachineError) Unwrap() error { return err.err }

func (err *jobMachineError) RenderCLIError(writer io.Writer) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(err.envelope)
}

func jobCommandError(args []string, err error) error {
	if err == nil || !jobJSONRequested(args) {
		return err
	}
	code := exitCode(err)
	summary := daemon.DiagnosticSummary(err)
	return &jobMachineError{
		err: err,
		envelope: jobErrorEnvelope{
			OutputVersion: JobCLIOutputVersion,
			Error: jobErrorDescriptor{
				ExitCode:  int(code),
				Class:     exitClass(code),
				Message:   err.Error(),
				RequestID: summary.RequestID,
				ErrorCode: summary.ErrorCode,
				Retry:     summary.Retry,
				Effect:    summary.Effect,
			},
		},
	}
}

func jobJSONRequested(args []string) bool {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == "--format" && args[index+1] == "json" {
			return true
		}
	}
	return false
}

func exitClass(code ExitCode) string {
	switch code {
	case ExitSuccess:
		return "success"
	case ExitInternal:
		return "internal"
	case ExitUsage:
		return "usage"
	case ExitConfig:
		return "configuration"
	case ExitAuthentication:
		return "authentication"
	case ExitNetwork:
		return "network"
	case ExitConflict:
		return "conflict"
	case ExitPartial:
		return "partial_completion"
	case ExitCanceled:
		return "canceled"
	default:
		return "internal"
	}
}
