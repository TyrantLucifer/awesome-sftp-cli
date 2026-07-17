package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/daemon"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/job"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/state/jobstore"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transfer"
)

const testJobID = domain.JobID("job_aaaaaaaaaaaaaaaaaaaaaaaaaa")

type fakeJobRPC struct {
	t       *testing.T
	wantRPC string
	check   func(any)
	respond func(any)
	err     error
	calls   int
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

func (fake *fakeJobRPC) Call(_ context.Context, name string, request, response any) error {
	fake.t.Helper()
	fake.calls++
	if name != fake.wantRPC {
		fake.t.Fatalf("RPC name = %q, want %q", name, fake.wantRPC)
	}
	if fake.check != nil {
		fake.check(request)
	}
	if fake.respond != nil {
		fake.respond(response)
	}
	return fake.err
}

func TestJobListUsesBoundedRPCAndVersionedJSON(t *testing.T) {
	now := time.Date(2026, 7, 18, 1, 2, 3, 0, time.UTC)
	fake := &fakeJobRPC{t: t, wantRPC: daemon.JobList}
	fake.check = func(request any) {
		got, ok := request.(daemon.JobListRequest)
		if !ok || got.Limit != 7 {
			t.Fatalf("request = %#v, want limit 7", request)
		}
	}
	fake.respond = func(response any) {
		got := response.(*daemon.JobListResponse)
		got.Jobs = []transfer.JobView{{
			Snapshot: jobstore.Snapshot{JobID: testJobID, State: job.StateRunning, CreatedAt: now, UpdatedAt: now},
			Kind:     transfer.OperationCopy,
			Route:    transfer.RouteSFTPRelay,
			Bytes:    12,
		}}
	}

	var stdout bytes.Buffer
	if err := runJobCommand(t.Context(), []string{"list", "--limit", "7", "--format", "json"}, &stdout, fake); err != nil {
		t.Fatal(err)
	}
	var output struct {
		OutputVersion int `json:"output_version"`
		Jobs          []struct {
			Snapshot struct {
				JobID domain.JobID `json:"job_id"`
				State job.State    `json:"state"`
			} `json:"snapshot"`
			Kind  transfer.OperationKind `json:"kind"`
			Route transfer.Route         `json:"route"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode output %q: %v", stdout.String(), err)
	}
	if output.OutputVersion != JobCLIOutputVersion || len(output.Jobs) != 1 || output.Jobs[0].Snapshot.JobID != testJobID || output.Jobs[0].Snapshot.State != job.StateRunning || output.Jobs[0].Kind != transfer.OperationCopy || output.Jobs[0].Route != transfer.RouteSFTPRelay {
		t.Fatalf("output = %#v", output)
	}
	if strings.Contains(stdout.String(), `"JobID"`) {
		t.Fatalf("machine output leaked Go field names: %s", stdout.String())
	}
	if fake.calls != 1 {
		t.Fatalf("RPC calls = %d, want 1", fake.calls)
	}
}

func TestJobJSONEmptyPagesUseDocumentedDefaultsAndArrays(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		rpcName string
		check   func(any)
		member  string
	}{
		{
			name: "list", args: []string{"list", "--format", "json"}, rpcName: daemon.JobList, member: `"jobs":[]`,
			check: func(request any) {
				if got := request.(daemon.JobListRequest); got.Limit != defaultJobCLILimit {
					t.Fatalf("list limit = %d, want %d", got.Limit, defaultJobCLILimit)
				}
			},
		},
		{
			name: "events", args: []string{"events", string(testJobID), "--format", "json"}, rpcName: daemon.JobEvents, member: `"events":[]`,
			check: func(request any) {
				got := request.(daemon.JobEventsRequest)
				if got.Limit != defaultJobCLILimit || got.AfterSequence != 0 {
					t.Fatalf("events cursor = (%d, %d), want (0, %d)", got.AfterSequence, got.Limit, defaultJobCLILimit)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeJobRPC{t: t, wantRPC: tt.rpcName, check: tt.check}
			var stdout bytes.Buffer
			if err := runJobCommand(t.Context(), tt.args, &stdout, fake); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(stdout.String(), tt.member) || strings.Contains(stdout.String(), ":null") {
				t.Fatalf("empty-page output = %q", stdout.String())
			}
		})
	}
}

func TestJobEventsUsesValidatedCursorAndHumanOutput(t *testing.T) {
	fake := &fakeJobRPC{t: t, wantRPC: daemon.JobEvents}
	fake.check = func(request any) {
		got, ok := request.(daemon.JobEventsRequest)
		if !ok || got.JobID != testJobID || got.AfterSequence != 4 || got.Limit != 9 {
			t.Fatalf("request = %#v", request)
		}
	}
	fake.respond = func(response any) {
		got := response.(*daemon.JobEventsResponse)
		got.Events = []jobstore.EventRecord{{JobID: testJobID, Sequence: 5, Kind: "job_started", PayloadJSON: `{}`}}
	}

	var stdout bytes.Buffer
	if err := runJobCommand(t.Context(), []string{"events", string(testJobID), "--after", "4", "--limit", "9"}, &stdout, fake); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{string(testJobID), "5", "job_started"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("human output %q does not contain %q", stdout.String(), want)
		}
	}
}

func TestJobCancelRequiresExactJobIDConfirmationBeforeRPC(t *testing.T) {
	for _, args := range [][]string{
		{"cancel", string(testJobID)},
		{"cancel", string(testJobID), "--confirm", "job_bbbbbbbbbbbbbbbbbbbbbbbbbb"},
	} {
		fake := &fakeJobRPC{t: t}
		err := runJobCommand(t.Context(), args, &bytes.Buffer{}, fake)
		if err == nil || exitCode(err) != ExitUsage {
			t.Fatalf("args %q error = %v, exit = %d", args, err, exitCode(err))
		}
		if fake.calls != 0 {
			t.Fatalf("args %q made %d RPC calls before confirmation", args, fake.calls)
		}
	}
}

func TestRunJobValidatesBeforeConnecting(t *testing.T) {
	for _, args := range [][]string{
		{"list", "--limit", "0"},
		{"cancel", string(testJobID)},
	} {
		connections := 0
		err := runJobWithConnector(t.Context(), args, &bytes.Buffer{}, func(context.Context) (jobRPC, io.Closer, error) {
			connections++
			return &fakeJobRPC{t: t}, nopCloser{}, nil
		})
		if err == nil || exitCode(err) != ExitUsage {
			t.Fatalf("args %q error = %v, exit = %d", args, err, exitCode(err))
		}
		if connections != 0 {
			t.Fatalf("args %q made %d daemon connections before validation", args, connections)
		}
	}
}

func TestJobControlRoutesAndReturnsVersionedSnapshot(t *testing.T) {
	tests := []struct {
		command string
		rpc     string
		extra   []string
	}{
		{command: "pause", rpc: daemon.JobPause},
		{command: "resume", rpc: daemon.JobResume},
		{command: "cancel", rpc: daemon.JobCancel, extra: []string{"--confirm", string(testJobID)}},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			fake := &fakeJobRPC{t: t, wantRPC: tt.rpc}
			fake.check = func(request any) {
				got, ok := request.(daemon.JobControlRequest)
				if !ok || got.JobID != testJobID {
					t.Fatalf("request = %#v", request)
				}
			}
			fake.respond = func(response any) {
				got := response.(*daemon.JobSnapshotResponse)
				got.Snapshot = jobstore.Snapshot{JobID: testJobID, State: job.StatePaused}
			}
			args := append([]string{tt.command, string(testJobID), "--format", "json"}, tt.extra...)
			var stdout bytes.Buffer
			if err := runJobCommand(t.Context(), args, &stdout, fake); err != nil {
				t.Fatal(err)
			}
			var output struct {
				OutputVersion int `json:"output_version"`
				Snapshot      struct {
					JobID domain.JobID `json:"job_id"`
					State job.State    `json:"state"`
				} `json:"snapshot"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
				t.Fatal(err)
			}
			if output.OutputVersion != JobCLIOutputVersion || output.Snapshot.JobID != testJobID {
				t.Fatalf("output = %#v", output)
			}
			if strings.Contains(stdout.String(), `"JobID"`) {
				t.Fatalf("machine output leaked Go field names: %s", stdout.String())
			}
		})
	}
}

func TestJobEventsJSONUsesStableFieldsAndEmbeddedPayload(t *testing.T) {
	fake := &fakeJobRPC{t: t, wantRPC: daemon.JobEvents}
	fake.respond = func(response any) {
		got := response.(*daemon.JobEventsResponse)
		got.Events = []jobstore.EventRecord{{
			JobID:       testJobID,
			Sequence:    2,
			EventID:     "evt_aaaaaaaaaaaaaaaaaaaaaaaaaa",
			Kind:        "job_started",
			PayloadJSON: `{"route":"sftp_relay"}`,
			CreatedAt:   time.Date(2026, 7, 18, 1, 2, 3, 0, time.UTC),
		}}
	}
	var stdout bytes.Buffer
	if err := runJobCommand(t.Context(), []string{"events", string(testJobID), "--format", "json"}, &stdout, fake); err != nil {
		t.Fatal(err)
	}
	var output struct {
		OutputVersion int `json:"output_version"`
		Events        []struct {
			JobID     domain.JobID   `json:"job_id"`
			Sequence  int64          `json:"sequence"`
			EventID   domain.EventID `json:"event_id"`
			Kind      string         `json:"kind"`
			Payload   map[string]any `json:"payload"`
			CreatedAt time.Time      `json:"created_at"`
		} `json:"events"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	if output.OutputVersion != JobCLIOutputVersion || len(output.Events) != 1 || output.Events[0].JobID != testJobID || output.Events[0].Sequence != 2 || output.Events[0].Payload["route"] != "sftp_relay" {
		t.Fatalf("output = %#v", output)
	}
	if strings.Contains(stdout.String(), `"PayloadJSON"`) {
		t.Fatalf("machine output leaked storage field names: %s", stdout.String())
	}
}

func TestJobCommandRejectsInvalidOrUnboundedArgumentsBeforeRPC(t *testing.T) {
	tests := [][]string{
		{},
		{"unknown"},
		{"list", "--limit", "0"},
		{"list", "--limit", "101"},
		{"list", "--format", "yaml"},
		{"events", "not-a-job"},
		{"events", string(testJobID), "--after", "-1"},
		{"pause", string(testJobID), "extra"},
	}
	for _, args := range tests {
		fake := &fakeJobRPC{t: t}
		err := runJobCommand(t.Context(), args, &bytes.Buffer{}, fake)
		if err == nil || exitCode(err) != ExitUsage {
			t.Fatalf("args %q error = %v, exit = %d", args, err, exitCode(err))
		}
		if fake.calls != 0 {
			t.Fatalf("args %q made %d RPC calls", args, fake.calls)
		}
	}
}

func TestJobRPCFailuresUseStablePublicExitClasses(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want ExitCode
	}{
		{name: "authentication", err: &daemon.RemoteError{RPC: ipc.RPCError{Code: domain.CodeAuthRequired}}, want: ExitAuthentication},
		{name: "network", err: &daemon.RemoteError{RPC: ipc.RPCError{Code: domain.CodeTransportInterrupted}}, want: ExitNetwork},
		{name: "conflict", err: &daemon.RemoteError{RPC: ipc.RPCError{Code: domain.CodeConflict}}, want: ExitConflict},
		{name: "canceled", err: &daemon.RemoteError{RPC: ipc.RPCError{Code: domain.CodeCanceled}}, want: ExitCanceled},
		{name: "internal", err: errors.New("closed"), want: ExitInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeJobRPC{t: t, wantRPC: daemon.JobList, err: tt.err}
			err := runJobCommand(t.Context(), []string{"list"}, &bytes.Buffer{}, fake)
			if err == nil || exitCode(err) != tt.want {
				t.Fatalf("error = %v, exit = %d, want %d", err, exitCode(err), tt.want)
			}
		})
	}
}

func TestJobDaemonConnectionFailuresUseNetworkOrCancellationExit(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want ExitCode
	}{
		{name: "dial failure", err: errors.New("dial unix: unavailable"), want: ExitNetwork},
		{name: "deadline", err: context.DeadlineExceeded, want: ExitNetwork},
		{name: "canceled", err: context.Canceled, want: ExitCanceled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := classifyJobConnectionError(tt.err)
			if exitCode(err) != tt.want {
				t.Fatalf("error = %v, exit = %d, want %d", err, exitCode(err), tt.want)
			}
		})
	}
}

func TestRunWritesVersionedJobJSONErrorWithoutHumanPrefix(t *testing.T) {
	remote := &daemon.RemoteError{
		RequestID: "req_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		RPC: ipc.RPCError{
			Code:    domain.CodeAuthRequired,
			Message: "authentication required",
			Retry:   domain.RetryAdvice{Kind: domain.RetryAfterAuth},
			Effect:  domain.EffectNone,
		},
	}
	handlers := Handlers{Job: func(context.Context, []string, io.Writer, io.Writer) error {
		return machineCommandError([]string{"list", "--format", "json"}, classifyJobCLIError(remote))
	}}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if got := Run(t.Context(), []string{"job", "list", "--format", "json"}, &stdout, &stderr, handlers); got != int(ExitAuthentication) {
		t.Fatalf("exit = %d, want %d", got, ExitAuthentication)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
	var output struct {
		OutputVersion int `json:"output_version"`
		Error         struct {
			ExitCode  int                 `json:"exit_code"`
			Class     string              `json:"class"`
			Message   string              `json:"message"`
			RequestID domain.RequestID    `json:"request_id"`
			ErrorCode domain.Code         `json:"error_code"`
			Retry     domain.RetryKind    `json:"retry"`
			Effect    domain.EffectStatus `json:"effect"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stderr.Bytes(), &output); err != nil {
		t.Fatalf("stderr is not one JSON object: %q: %v", stderr.String(), err)
	}
	if output.OutputVersion != JobCLIOutputVersion || output.Error.ExitCode != int(ExitAuthentication) || output.Error.Class != "authentication" || output.Error.RequestID != remote.RequestID || output.Error.ErrorCode != domain.CodeAuthRequired || output.Error.Retry != domain.RetryAfterAuth || output.Error.Effect != domain.EffectNone {
		t.Fatalf("output = %#v", output)
	}
	if output.Error.Message != remote.Error() || strings.Contains(stderr.String(), "amsftp: job handler") {
		t.Fatalf("machine error = %q", stderr.String())
	}
}
