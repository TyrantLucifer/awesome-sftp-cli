package app

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/daemon"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/tui"
)

func TestClassifySSHConnectErrorDoesNotRetryAuthHostKeyOrConfig(t *testing.T) {
	tests := []struct {
		message string
		code    domain.Code
		retry   domain.RetryKind
	}{
		{message: "Permission denied (publickey,password)", code: domain.CodeAuthRequired, retry: domain.RetryAfterAuth},
		{message: "REMOTE HOST IDENTIFICATION HAS CHANGED", code: domain.CodePermissionDenied, retry: domain.RetryNever},
		{message: "subsystem request failed on channel 0", code: domain.CodeUnsupported, retry: domain.RetryNever},
		{message: "Connection refused", code: domain.CodeTransportInterrupted, retry: domain.RetryAfterReconnect},
	}
	for _, test := range tests {
		code, retry := classifySSHConnectError(errors.New(test.message))
		if code != test.code || retry != test.retry {
			t.Fatalf("classify %q = (%s, %s), want (%s, %s)", test.message, code, retry, test.code, test.retry)
		}
	}
}

func TestRunReconnectUsesBoundedBackoffAndStopsOnNonRetryableError(t *testing.T) {
	var sleeps []time.Duration
	policy := reconnectPolicy{
		Delays: []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond},
		Sleep: func(_ context.Context, delay time.Duration) error {
			sleeps = append(sleeps, delay)
			return nil
		},
	}
	attempts := 0
	err := runReconnect(context.Background(), policy, func() error {
		attempts++
		if attempts < 3 {
			return remoteRetryError(domain.RetryAfterReconnect)
		}
		return nil
	})
	if err != nil || attempts != 3 || !reflect.DeepEqual(sleeps, []time.Duration{10 * time.Millisecond, 20 * time.Millisecond}) {
		t.Fatalf("reconnect err=%v attempts=%d sleeps=%v", err, attempts, sleeps)
	}

	attempts = 0
	sleeps = nil
	err = runReconnect(context.Background(), policy, func() error {
		attempts++
		return remoteRetryError(domain.RetryAfterAuth)
	})
	if err == nil || attempts != 1 || len(sleeps) != 0 {
		t.Fatalf("non-retryable err=%v attempts=%d sleeps=%v", err, attempts, sleeps)
	}
}

func TestConnectDaemonAfterLossRetriesStartupRace(t *testing.T) {
	want := &daemon.Client{}
	attempts := 0
	policy := reconnectPolicy{
		Delays: []time.Duration{time.Millisecond},
		Sleep:  func(context.Context, time.Duration) error { return nil },
	}
	got, err := connectDaemonAfterLoss(context.Background(), policy, func(context.Context) (*daemon.Client, error) {
		attempts++
		if attempts == 1 {
			return nil, errors.New("previous daemon still owns the instance lock")
		}
		return want, nil
	})
	if err != nil || got != want || attempts != 2 {
		t.Fatalf("connect daemon after loss = (%p, %v), attempts=%d, want (%p, nil), attempts=2", got, err, attempts, want)
	}
}

func TestProviderCallFailureSeparatesEndpointAndDaemonLoss(t *testing.T) {
	code, retry, daemonLost := providerCallFailure(remoteRetryError(domain.RetryAfterReconnect))
	if code != domain.CodeTransportInterrupted || retry != domain.RetryAfterReconnect || daemonLost {
		t.Fatalf("remote failure = (%s, %s, %t)", code, retry, daemonLost)
	}
	code, retry, daemonLost = providerCallFailure(errors.New("local socket closed"))
	if code != domain.CodeTransportInterrupted || retry != domain.RetryAfterReconnect || !daemonLost {
		t.Fatalf("daemon failure = (%s, %s, %t)", code, retry, daemonLost)
	}
}

func TestDaemonConnectionLostFollowsWrappedTransportButNotRemotePolicy(t *testing.T) {
	if !daemonConnectionLost(&authRPCError{operation: "claim", cause: errors.New("socket closed")}) {
		t.Fatal("wrapped local transport was not treated as daemon loss")
	}
	serverCanceledClaim := &daemon.RemoteError{RPC: ipc.RPCError{Code: domain.CodeCanceled}}
	if !daemonConnectionLost(&authRPCError{operation: "claim", cause: serverCanceledClaim}) {
		t.Fatal("server-canceled authentication claim was not treated as daemon loss")
	}
	if daemonConnectionLost(&authRPCError{operation: "claim", cause: remoteRetryError(domain.RetryNever)}) {
		t.Fatal("structured remote failure was treated as daemon loss")
	}
	if daemonConnectionLost(context.Canceled) {
		t.Fatal("context cancellation was treated as daemon loss")
	}
}

func TestRecoveryParentWalksTowardRootWithoutChangingEndpoint(t *testing.T) {
	location := domain.Location{EndpointID: domain.EndpointID("ep_aaaaaaaaaaaaaaaaaaaaaaaaaa"), Path: "/srv/missing/deep"}
	parent, ok := recoveryParent(location)
	if !ok || parent.EndpointID != location.EndpointID || parent.Path != "/srv/missing" {
		t.Fatalf("recovery parent = %#v, %t", parent, ok)
	}
	if _, ok := recoveryParent(domain.Location{EndpointID: location.EndpointID, Path: "/"}); ok {
		t.Fatal("root unexpectedly has a recovery parent")
	}
}

func TestPaneRecoveryFallbackPreservesEndpointTransaction(t *testing.T) {
	endpoint := domain.Endpoint{
		ID:           domain.EndpointID("ep_cccccccccccccccccccccccccc"),
		Kind:         domain.EndpointSSH,
		DisplayName:  "work",
		SSHHostAlias: "work",
	}
	deep := domain.Location{EndpointID: endpoint.ID, Path: "/srv/missing/deep"}
	initial := tui.Intent{
		Kind:                 tui.IntentList,
		Pane:                 tui.Left,
		Location:             deep,
		Endpoint:             endpoint,
		Connection:           domain.StateReady,
		CapabilityGeneration: 7,
		CommitEndpoint:       true,
	}

	var recovery paneRecovery
	recovery.beginConnection()
	recovery.connected()
	recovery.listingStarted(41, initial)
	fallback, ok := recovery.listingFailed(tui.ListingFailed{
		Pane:       tui.Left,
		Generation: 41,
		Code:       domain.CodeNotFound,
		Location:   deep,
	})
	if !ok {
		t.Fatal("not-found recovery did not request the nearest parent")
	}
	want := initial
	want.Location.Path = "/srv/missing"
	if !reflect.DeepEqual(fallback, want) {
		t.Fatalf("fallback intent = %#v, want %#v", fallback, want)
	}
}

func TestPaneRecoveryCompletesOnlyCurrentFallbackListing(t *testing.T) {
	endpointID := domain.EndpointID("ep_cccccccccccccccccccccccccc")
	deep := domain.Location{EndpointID: endpointID, Path: "/srv/missing/deep"}
	initial := tui.Intent{Kind: tui.IntentList, Pane: tui.Left, Location: deep}

	var recovery paneRecovery
	recovery.beginConnection()
	recovery.connected()
	recovery.listingStarted(41, initial)
	fallback, ok := recovery.listingFailed(tui.ListingFailed{
		Pane:       tui.Left,
		Generation: 41,
		Code:       domain.CodeNotFound,
		Location:   deep,
	})
	if !ok {
		t.Fatal("not-found recovery did not request the nearest parent")
	}
	recovery.listingStarted(42, fallback)
	if recovery.listingCompleted(tui.ListingPage{Pane: tui.Left, Generation: 41, Done: true}) {
		t.Fatal("stale listing completed recovery")
	}
	if !recovery.listingCompleted(tui.ListingPage{Pane: tui.Left, Generation: 42, Done: true}) {
		t.Fatal("current fallback listing did not complete recovery")
	}
	if recovery.listingCompleted(tui.ListingPage{Pane: tui.Left, Generation: 42, Done: true}) {
		t.Fatal("completed recovery remained active")
	}
}

func TestPaneRecoveryFallbackCommitsReconnectedEndpointAtParent(t *testing.T) {
	oldEndpoint := domain.Endpoint{
		ID:           domain.EndpointID("ep_aaaaaaaaaaaaaaaaaaaaaaaaaa"),
		Kind:         domain.EndpointSSH,
		DisplayName:  "work",
		SSHHostAlias: "work",
	}
	newEndpoint := oldEndpoint
	newEndpoint.ID = domain.EndpointID("ep_cccccccccccccccccccccccccc")
	oldLocation := domain.Location{EndpointID: oldEndpoint.ID, Path: "/srv/missing/deep"}
	newLocation := domain.Location{EndpointID: newEndpoint.ID, Path: oldLocation.Path}
	rightEndpoint := domain.Endpoint{ID: domain.EndpointID("ep_bbbbbbbbbbbbbbbbbbbbbbbbbb"), Kind: domain.EndpointLocal, DisplayName: "local"}
	rightLocation := domain.Location{EndpointID: rightEndpoint.ID, Path: "/"}
	model := tui.NewModel(tui.NewPaneState(oldEndpoint, oldLocation), tui.NewPaneState(rightEndpoint, rightLocation))

	model, intents := tui.Reduce(model, tui.PaneConnected{
		Pane:              tui.Left,
		Endpoint:          newEndpoint,
		Location:          newLocation,
		State:             domain.StateReady,
		PreserveCommitted: true,
	})
	if len(intents) != 1 {
		t.Fatalf("connected intents = %#v, want one list", intents)
	}
	initial := intents[0]
	var recovery paneRecovery
	recovery.beginConnection()
	recovery.connected()
	recovery.listingStarted(41, initial)
	model, _ = tui.Reduce(model, tui.BeginListing{
		Pane:           tui.Left,
		Generation:     41,
		Location:       initial.Location,
		Endpoint:       initial.Endpoint,
		Connection:     initial.Connection,
		CommitEndpoint: initial.CommitEndpoint,
	})
	failure := tui.ListingFailed{
		Pane:       tui.Left,
		Generation: 41,
		Code:       domain.CodeNotFound,
		Location:   newLocation,
	}
	model, _ = tui.Reduce(model, failure)
	fallback, ok := recovery.listingFailed(failure)
	if !ok {
		t.Fatal("not-found recovery did not request the nearest parent")
	}
	recovery.listingStarted(42, fallback)
	model, _ = tui.Reduce(model, tui.BeginListing{
		Pane:           tui.Left,
		Generation:     42,
		Location:       fallback.Location,
		Endpoint:       fallback.Endpoint,
		Connection:     fallback.Connection,
		CommitEndpoint: fallback.CommitEndpoint,
	})
	if model.Panes[tui.Left].Listing.Generation != 42 {
		t.Fatalf("fallback generation = %d, want 42", model.Panes[tui.Left].Listing.Generation)
	}
	page := tui.ListingPage{Pane: tui.Left, Generation: 42, Entries: []domain.Entry{{
		Location: domain.Location{EndpointID: newEndpoint.ID, Path: "/srv/missing/recovered.txt"},
		Name:     "recovered.txt",
		Kind:     domain.EntryFile,
	}}, Done: true}
	model, _ = tui.Reduce(model, page)
	if !recovery.listingCompleted(page) {
		t.Fatal("successful parent page did not complete recovery")
	}
	pane := model.Panes[tui.Left]
	if pane.Endpoint != newEndpoint || pane.Location != fallback.Location || !reflect.DeepEqual(pane.VisibleNames(), []string{"recovered.txt"}) {
		t.Fatalf("recovered pane = %#v names=%#v", pane, pane.VisibleNames())
	}
}

func TestPaneRecoveryConnectionFailureClearsConnectingState(t *testing.T) {
	var recovery paneRecovery
	recovery.beginConnection()
	if !recovery.connecting() {
		t.Fatal("started recovery is not connecting")
	}
	recovery.connectionFailed()
	if recovery.connecting() {
		t.Fatal("failed recovery remained connecting")
	}
}

func TestPaneRecoveryTerminalListingFailureStopsFallback(t *testing.T) {
	endpointID := domain.EndpointID("ep_cccccccccccccccccccccccccc")
	deep := domain.Location{EndpointID: endpointID, Path: "/srv/missing/deep"}
	var recovery paneRecovery
	recovery.beginConnection()
	recovery.connected()
	recovery.listingStarted(41, tui.Intent{Kind: tui.IntentList, Pane: tui.Left, Location: deep})
	if _, ok := recovery.listingFailed(tui.ListingFailed{
		Pane:       tui.Left,
		Generation: 41,
		Code:       domain.CodeInternal,
		Location:   deep,
	}); ok {
		t.Fatal("internal failure unexpectedly requested fallback")
	}
	if _, ok := recovery.listingFailed(tui.ListingFailed{
		Pane:       tui.Left,
		Generation: 41,
		Code:       domain.CodeNotFound,
		Location:   deep,
	}); ok {
		t.Fatal("terminal listing failure left recovery validation active")
	}
}

func remoteRetryError(retry domain.RetryKind) error {
	return &daemon.RemoteError{RPC: ipc.RPCError{
		Code:    domain.CodeTransportInterrupted,
		Message: "connection failed",
		Retry:   domain.RetryAdvice{Kind: retry},
		Effect:  domain.EffectNone,
	}}
}
