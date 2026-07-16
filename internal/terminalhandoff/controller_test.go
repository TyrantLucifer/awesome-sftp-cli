package terminalhandoff

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunSuspendsTransfersForegroundAndRestoresInOrder(t *testing.T) {
	t.Parallel()

	recorder := &callRecorder{}
	snapshot := NewSnapshot(struct{ Pane string }{Pane: "right"}, Size{Columns: 132, Rows: 41})
	screen := newFakeScreen(recorder, snapshot)
	platform := newFakePlatform(recorder)
	process := &fakeProcess{recorder: recorder, processGroup: 712, result: Result{Kind: ExitNormal}}
	controller, err := NewController(screen, platform)
	if err != nil {
		t.Fatalf("NewController() error = %v", err)
	}

	result, err := controller.Run(context.Background(), LauncherFunc(func(context.Context) (Process, error) {
		recorder.add("start")
		return process, nil
	}))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Kind != ExitNormal {
		t.Fatalf("Run() result = %#v, want normal exit", result)
	}
	if got := controller.State(); got != StateActiveTUI {
		t.Fatalf("State() = %q, want %q", got, StateActiveTUI)
	}

	want := []string{
		"freeze", "stop_input", "show_cursor", "leave_alternate", "leave_raw",
		"save_terminal", "start", "give_foreground:712", "wait",
		"reclaim_foreground", "restore_terminal", "enter_alternate", "enter_raw",
		"restore_cursor", "replay_resize:132x41", "resume",
	}
	if got := recorder.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %q, want %q", got, want)
	}
	if got := screen.resumedSnapshot; got.Opaque() != snapshot.Opaque() {
		t.Fatalf("resumed snapshot = %#v, want frozen snapshot %#v", got, snapshot)
	}
}

func TestRunRoutesEveryExternalCompletionThroughTheSameRestorePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		result Result
	}{
		{name: "normal", result: Result{Kind: ExitNormal}},
		{name: "nonzero", result: Result{Kind: ExitNonZero, ExitCode: 23}},
		{name: "signal", result: Result{Kind: ExitSignaled, Signal: "TERM"}},
		{name: "pty loss", result: Result{Kind: ExitPTYLoss, Err: errors.New("input/output error")}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			recorder := &callRecorder{}
			screen := newFakeScreen(recorder, NewSnapshot(test.name, Size{Columns: 80, Rows: 24}))
			platform := newFakePlatform(recorder)
			controller, err := NewController(screen, platform)
			if err != nil {
				t.Fatalf("NewController() error = %v", err)
			}
			process := &fakeProcess{recorder: recorder, processGroup: 31, result: test.result}

			got, err := controller.Run(context.Background(), LauncherFunc(func(context.Context) (Process, error) {
				recorder.add("start")
				return process, nil
			}))
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if !reflect.DeepEqual(got, test.result) {
				t.Fatalf("Run() result = %#v, want %#v", got, test.result)
			}
			assertSingleRestore(t, recorder.snapshot())
		})
	}
}

func TestAbnormalExternalCompletionUsesCleanupState(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		result    Result
		wantState State
	}{
		{name: "normal", result: Result{Kind: ExitNormal}, wantState: StateReacquiring},
		{name: "nonzero", result: Result{Kind: ExitNonZero, ExitCode: 9}, wantState: StateCleanup},
		{name: "signal", result: Result{Kind: ExitSignaled, Signal: "INT"}, wantState: StateCleanup},
		{name: "pty loss", result: Result{Kind: ExitPTYLoss, Err: errors.New("lost")}, wantState: StateCleanup},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			recorder := &callRecorder{}
			screen := newFakeScreen(recorder, NewSnapshot("ui", Size{Columns: 80, Rows: 24}))
			platform := newFakePlatform(recorder)
			controller, err := NewController(screen, platform)
			if err != nil {
				t.Fatalf("NewController() error = %v", err)
			}
			screen.state = controller.State
			process := &fakeProcess{recorder: recorder, processGroup: 32, result: test.result}
			if _, err := controller.Run(context.Background(), LauncherFunc(func(context.Context) (Process, error) {
				return process, nil
			})); err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if screen.stateAtResume != test.wantState {
				t.Fatalf("state during resume = %q, want %q", screen.stateAtResume, test.wantState)
			}
		})
	}
}

func TestSpawnFailureUsesRestoreAndPreservesBothErrors(t *testing.T) {
	t.Parallel()

	recorder := &callRecorder{}
	screen := newFakeScreen(recorder, NewSnapshot("ui", Size{Columns: 90, Rows: 30}))
	platform := newFakePlatform(recorder)
	restoreErr := errors.New("restore termios")
	platform.fail["restore_terminal"] = restoreErr
	controller, err := NewController(screen, platform)
	if err != nil {
		t.Fatalf("NewController() error = %v", err)
	}
	spawnErr := errors.New("executable disappeared")

	_, err = controller.Run(context.Background(), LauncherFunc(func(context.Context) (Process, error) {
		recorder.add("start")
		return nil, spawnErr
	}))
	if !errors.Is(err, spawnErr) || !errors.Is(err, restoreErr) {
		t.Fatalf("Run() error = %v, want spawn and restore errors", err)
	}
	assertSingleRestore(t, recorder.snapshot())
	if got := controller.State(); got != StateActiveTUI {
		t.Fatalf("State() = %q, want active after failed spawn cleanup", got)
	}
}

func TestEverySuspendFailureAttemptsConservativeRestore(t *testing.T) {
	t.Parallel()

	for _, operation := range []string{"stop_input", "show_cursor", "leave_alternate", "leave_raw", "save_terminal"} {
		operation := operation
		t.Run(operation, func(t *testing.T) {
			t.Parallel()

			recorder := &callRecorder{}
			screen := newFakeScreen(recorder, NewSnapshot("ui", Size{Columns: 70, Rows: 20}))
			platform := newFakePlatform(recorder)
			failure := errors.New(operation + " failed")
			if operation == "save_terminal" {
				platform.fail[operation] = failure
			} else {
				screen.fail[operation] = failure
			}
			controller, err := NewController(screen, platform)
			if err != nil {
				t.Fatalf("NewController() error = %v", err)
			}
			started := false

			_, err = controller.Run(context.Background(), LauncherFunc(func(context.Context) (Process, error) {
				started = true
				return nil, nil
			}))
			if !errors.Is(err, failure) {
				t.Fatalf("Run() error = %v, want %v", err, failure)
			}
			if started {
				t.Fatal("external process started after suspension failure")
			}
			calls := recorder.snapshot()
			if count(calls, "resume") != 1 || count(calls, "enter_alternate") != 1 || count(calls, "enter_raw") != 1 {
				t.Fatalf("calls = %q, want one conservative screen restore", calls)
			}
			if operation == "save_terminal" && count(calls, "restore_terminal") != 0 {
				t.Fatalf("calls = %q, restored terminal without saved state", calls)
			}
		})
	}
}

func TestRestoreContinuesAfterEveryReacquireFailure(t *testing.T) {
	t.Parallel()

	for _, operation := range []string{
		"reclaim_foreground", "restore_terminal", "enter_alternate", "enter_raw", "restore_cursor", "replay_resize", "resume",
	} {
		operation := operation
		t.Run(operation, func(t *testing.T) {
			t.Parallel()

			recorder := &callRecorder{}
			screen := newFakeScreen(recorder, NewSnapshot("ui", Size{Columns: 101, Rows: 29}))
			platform := newFakePlatform(recorder)
			failure := errors.New(operation + " failed")
			if operation == "reclaim_foreground" || operation == "restore_terminal" {
				platform.fail[operation] = failure
			} else {
				screen.fail[operation] = failure
			}
			controller, err := NewController(screen, platform)
			if err != nil {
				t.Fatalf("NewController() error = %v", err)
			}
			process := &fakeProcess{recorder: recorder, processGroup: 41, result: Result{Kind: ExitNormal}}

			_, err = controller.Run(context.Background(), LauncherFunc(func(context.Context) (Process, error) {
				recorder.add("start")
				return process, nil
			}))
			if !errors.Is(err, failure) {
				t.Fatalf("Run() error = %v, want %v", err, failure)
			}
			calls := recorder.snapshot()
			if count(calls, "resume") != 1 {
				t.Fatalf("calls = %q, restore stopped before final resume", calls)
			}
			if got := controller.State(); got != StateActiveTUI {
				t.Fatalf("State() = %q, want active after best-effort restore", got)
			}
		})
	}
}

func TestCleanupIsConcurrentAndIdempotent(t *testing.T) {
	t.Parallel()

	recorder := &callRecorder{}
	screen := newFakeScreen(recorder, NewSnapshot("ui", Size{Columns: 81, Rows: 25}))
	platform := newFakePlatform(recorder)
	waitStarted := make(chan struct{})
	releaseWait := make(chan struct{})
	process := &fakeProcess{
		recorder: recorder, processGroup: 51, result: Result{Kind: ExitNormal},
		waitStarted: waitStarted, releaseWait: releaseWait,
	}
	controller, err := NewController(screen, platform)
	if err != nil {
		t.Fatalf("NewController() error = %v", err)
	}
	runDone := make(chan error, 1)
	go func() {
		_, runErr := controller.Run(context.Background(), LauncherFunc(func(context.Context) (Process, error) {
			recorder.add("start")
			return process, nil
		}))
		runDone <- runErr
	}()
	<-waitStarted

	const cleaners = 16
	errorsByCleaner := make(chan error, cleaners)
	var group sync.WaitGroup
	for range cleaners {
		group.Add(1)
		go func() {
			defer group.Done()
			errorsByCleaner <- controller.Cleanup()
		}()
	}
	group.Wait()
	close(errorsByCleaner)
	for cleanupErr := range errorsByCleaner {
		if cleanupErr != nil {
			t.Errorf("Cleanup() error = %v", cleanupErr)
		}
	}
	close(releaseWait)
	if err := <-runDone; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if err := controller.Cleanup(); err != nil {
		t.Fatalf("second Cleanup() error = %v", err)
	}
	assertSingleRestore(t, recorder.snapshot())
}

func TestCleanupDuringSuspensionWaitsForACompleteRestorableState(t *testing.T) {
	t.Parallel()

	recorder := &callRecorder{}
	screen := newFakeScreen(recorder, NewSnapshot("ui", Size{Columns: 82, Rows: 26}))
	freezeStarted := make(chan struct{})
	releaseFreeze := make(chan struct{})
	screen.freezeStarted = freezeStarted
	screen.releaseFreeze = releaseFreeze
	controller, err := NewController(screen, newFakePlatform(recorder))
	if err != nil {
		t.Fatalf("NewController() error = %v", err)
	}
	process := &fakeProcess{recorder: recorder, processGroup: 52, result: Result{Kind: ExitNormal}}
	runDone := make(chan error, 1)
	go func() {
		_, runErr := controller.Run(context.Background(), LauncherFunc(func(context.Context) (Process, error) {
			recorder.add("start")
			return process, nil
		}))
		runDone <- runErr
	}()
	<-freezeStarted
	cleanupDone := make(chan error, 1)
	go func() { cleanupDone <- controller.Cleanup() }()
	select {
	case cleanupErr := <-cleanupDone:
		t.Fatalf("Cleanup() returned during suspension with %v", cleanupErr)
	case <-time.After(25 * time.Millisecond):
	}
	close(releaseFreeze)
	if err := <-runDone; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if err := <-cleanupDone; err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	assertSingleRestore(t, recorder.snapshot())
}

func TestPanicStillRestoresThenRepanics(t *testing.T) {
	t.Parallel()

	recorder := &callRecorder{}
	screen := newFakeScreen(recorder, NewSnapshot("ui", Size{Columns: 88, Rows: 27}))
	platform := newFakePlatform(recorder)
	controller, err := NewController(screen, platform)
	if err != nil {
		t.Fatalf("NewController() error = %v", err)
	}

	func() {
		defer func() {
			if recovered := recover(); recovered != "boom" {
				t.Fatalf("panic = %#v, want boom", recovered)
			}
		}()
		_, _ = controller.Run(context.Background(), LauncherFunc(func(context.Context) (Process, error) {
			recorder.add("start")
			panic("boom")
		}))
	}()
	assertSingleRestore(t, recorder.snapshot())
}

func TestPanicCleanupContinuesWhenARestoreOperationAlsoPanics(t *testing.T) {
	t.Parallel()

	recorder := &callRecorder{}
	screen := newFakeScreen(recorder, NewSnapshot("ui", Size{Columns: 89, Rows: 28}))
	screen.panicAt["enter_alternate"] = "restore boom"
	controller, err := NewController(screen, newFakePlatform(recorder))
	if err != nil {
		t.Fatalf("NewController() error = %v", err)
	}

	func() {
		defer func() {
			if recovered := recover(); recovered != "boom" {
				t.Fatalf("panic = %#v, want original boom", recovered)
			}
		}()
		_, _ = controller.Run(context.Background(), LauncherFunc(func(context.Context) (Process, error) {
			panic("boom")
		}))
	}()
	if got := count(recorder.snapshot(), "resume"); got != 1 {
		t.Fatalf("resume count = %d in %q, want cleanup to continue", got, recorder.snapshot())
	}
}

func TestResizeCoalescesToLatestValueDuringExternalForeground(t *testing.T) {
	t.Parallel()

	recorder := &callRecorder{}
	screen := newFakeScreen(recorder, NewSnapshot("ui", Size{Columns: 80, Rows: 24}))
	platform := newFakePlatform(recorder)
	waitStarted := make(chan struct{})
	releaseWait := make(chan struct{})
	process := &fakeProcess{
		recorder: recorder, processGroup: 61, result: Result{Kind: ExitNormal},
		waitStarted: waitStarted, releaseWait: releaseWait,
	}
	controller, err := NewController(screen, platform)
	if err != nil {
		t.Fatalf("NewController() error = %v", err)
	}
	runDone := make(chan error, 1)
	go func() {
		_, runErr := controller.Run(context.Background(), LauncherFunc(func(context.Context) (Process, error) {
			recorder.add("start")
			return process, nil
		}))
		runDone <- runErr
	}()
	<-waitStarted

	for _, size := range []Size{{Columns: 100, Rows: 30}, {Columns: 120, Rows: 35}, {Columns: 140, Rows: 45}} {
		if err := controller.Resize(size); err != nil {
			t.Fatalf("Resize(%#v) error = %v", size, err)
		}
	}
	close(releaseWait)
	if err := <-runDone; err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	calls := recorder.snapshot()
	if countPrefix(calls, "replay_resize:") != 1 {
		t.Fatalf("calls = %q, want one coalesced resize replay", calls)
	}
	if count(calls, "replay_resize:140x45") != 1 {
		t.Fatalf("calls = %q, want latest resize", calls)
	}
}

func TestResizeArrivingDuringResumeIsNotLost(t *testing.T) {
	t.Parallel()

	recorder := &callRecorder{}
	screen := newFakeScreen(recorder, NewSnapshot("ui", Size{Columns: 80, Rows: 24}))
	resumeStarted := make(chan struct{})
	releaseResume := make(chan struct{})
	screen.resumeStarted = resumeStarted
	screen.releaseResume = releaseResume
	controller, err := NewController(screen, newFakePlatform(recorder))
	if err != nil {
		t.Fatalf("NewController() error = %v", err)
	}
	process := &fakeProcess{recorder: recorder, processGroup: 62, result: Result{Kind: ExitNormal}}
	runDone := make(chan error, 1)
	go func() {
		_, runErr := controller.Run(context.Background(), LauncherFunc(func(context.Context) (Process, error) {
			recorder.add("start")
			return process, nil
		}))
		runDone <- runErr
	}()
	<-resumeStarted
	if err := controller.Resize(Size{Columns: 151, Rows: 47}); err != nil {
		t.Fatalf("Resize() error = %v", err)
	}
	close(releaseResume)
	if err := <-runDone; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := count(recorder.snapshot(), "replay_resize:151x47"); got != 1 {
		t.Fatalf("latest resize replay count = %d in %q, want 1", got, recorder.snapshot())
	}
}

func TestResizeWhileActiveIsAppliedImmediately(t *testing.T) {
	t.Parallel()

	recorder := &callRecorder{}
	screen := newFakeScreen(recorder, NewSnapshot("ui", Size{Columns: 80, Rows: 24}))
	controller, err := NewController(screen, newFakePlatform(recorder))
	if err != nil {
		t.Fatalf("NewController() error = %v", err)
	}
	if err := controller.Resize(Size{Columns: 99, Rows: 33}); err != nil {
		t.Fatalf("Resize() error = %v", err)
	}
	if got := recorder.snapshot(); !reflect.DeepEqual(got, []string{"replay_resize:99x33"}) {
		t.Fatalf("calls = %q, want immediate resize", got)
	}
}

func TestBackgroundWorkContinuesWhileExternalProcessOwnsTerminal(t *testing.T) {
	t.Parallel()

	recorder := &callRecorder{}
	screen := newFakeScreen(recorder, NewSnapshot("ui", Size{Columns: 80, Rows: 24}))
	waitStarted := make(chan struct{})
	releaseWait := make(chan struct{})
	process := &fakeProcess{
		recorder: recorder, processGroup: 71, result: Result{Kind: ExitNormal},
		waitStarted: waitStarted, releaseWait: releaseWait,
	}
	controller, err := NewController(screen, newFakePlatform(recorder))
	if err != nil {
		t.Fatalf("NewController() error = %v", err)
	}
	runDone := make(chan error, 1)
	go func() {
		_, runErr := controller.Run(context.Background(), LauncherFunc(func(context.Context) (Process, error) {
			recorder.add("start")
			return process, nil
		}))
		runDone <- runErr
	}()
	<-waitStarted

	var progress atomic.Int64
	backgroundDone := make(chan struct{})
	go func() {
		for range 1000 {
			progress.Add(1)
		}
		close(backgroundDone)
	}()
	select {
	case <-backgroundDone:
	case <-time.After(2 * time.Second):
		t.Fatal("background work was coupled to terminal handoff")
	}
	if got := progress.Load(); got != 1000 {
		t.Fatalf("background progress = %d, want 1000", got)
	}
	close(releaseWait)
	if err := <-runDone; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestControllerRejectsInvalidDependenciesAndConcurrentRun(t *testing.T) {
	t.Parallel()

	recorder := &callRecorder{}
	screen := newFakeScreen(recorder, NewSnapshot("ui", Size{Columns: 80, Rows: 24}))
	platform := newFakePlatform(recorder)
	if _, err := NewController(nil, platform); err == nil {
		t.Fatal("NewController(nil, platform) succeeded")
	}
	if _, err := NewController(screen, nil); err == nil {
		t.Fatal("NewController(screen, nil) succeeded")
	}
	controller, err := NewController(screen, platform)
	if err != nil {
		t.Fatalf("NewController() error = %v", err)
	}
	waitStarted := make(chan struct{})
	releaseWait := make(chan struct{})
	process := &fakeProcess{
		recorder: recorder, processGroup: 81, result: Result{Kind: ExitNormal},
		waitStarted: waitStarted, releaseWait: releaseWait,
	}
	firstDone := make(chan error, 1)
	go func() {
		_, runErr := controller.Run(context.Background(), LauncherFunc(func(context.Context) (Process, error) {
			return process, nil
		}))
		firstDone <- runErr
	}()
	<-waitStarted
	if _, err := controller.Run(context.Background(), LauncherFunc(func(context.Context) (Process, error) {
		return process, nil
	})); !errors.Is(err, ErrBusy) {
		t.Fatalf("concurrent Run() error = %v, want ErrBusy", err)
	}
	close(releaseWait)
	if err := <-firstDone; err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
}

func assertSingleRestore(t *testing.T, calls []string) {
	t.Helper()
	for _, operation := range []string{
		"reclaim_foreground", "restore_terminal", "enter_alternate", "enter_raw", "restore_cursor", "resume",
	} {
		if got := count(calls, operation); got != 1 {
			t.Fatalf("%s count = %d in %q, want 1", operation, got, calls)
		}
	}
	if got := countPrefix(calls, "replay_resize:"); got != 1 {
		t.Fatalf("resize replay count = %d in %q, want 1", got, calls)
	}
}

type callRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (recorder *callRecorder) add(call string) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.calls = append(recorder.calls, call)
}

func (recorder *callRecorder) snapshot() []string {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]string(nil), recorder.calls...)
}

type fakeScreen struct {
	recorder        *callRecorder
	snapshot        Snapshot
	fail            map[string]error
	panicAt         map[string]any
	resumedSnapshot Snapshot
	resumeStarted   chan struct{}
	releaseResume   chan struct{}
	freezeStarted   chan struct{}
	releaseFreeze   chan struct{}
	state           func() State
	stateAtResume   State
}

func newFakeScreen(recorder *callRecorder, snapshot Snapshot) *fakeScreen {
	return &fakeScreen{
		recorder: recorder,
		snapshot: snapshot,
		fail:     make(map[string]error),
		panicAt:  make(map[string]any),
	}
}

func (screen *fakeScreen) Freeze() (Snapshot, error) {
	screen.recorder.add("freeze")
	if screen.freezeStarted != nil {
		close(screen.freezeStarted)
	}
	if screen.releaseFreeze != nil {
		<-screen.releaseFreeze
	}
	return screen.snapshot, screen.fail["freeze"]
}

func (screen *fakeScreen) StopInput() error             { return screen.call("stop_input") }
func (screen *fakeScreen) ShowCursor() error            { return screen.call("show_cursor") }
func (screen *fakeScreen) LeaveAlternate() error        { return screen.call("leave_alternate") }
func (screen *fakeScreen) LeaveRaw() error              { return screen.call("leave_raw") }
func (screen *fakeScreen) EnterAlternate() error        { return screen.call("enter_alternate") }
func (screen *fakeScreen) EnterRaw() error              { return screen.call("enter_raw") }
func (screen *fakeScreen) RestoreCursor(Snapshot) error { return screen.call("restore_cursor") }

func (screen *fakeScreen) ReplayResize(size Size) error {
	screen.recorder.add(fmt.Sprintf("replay_resize:%dx%d", size.Columns, size.Rows))
	return screen.fail["replay_resize"]
}

func (screen *fakeScreen) Resume(snapshot Snapshot) error {
	screen.recorder.add("resume")
	screen.resumedSnapshot = snapshot
	if screen.state != nil {
		screen.stateAtResume = screen.state()
	}
	if screen.resumeStarted != nil {
		close(screen.resumeStarted)
	}
	if screen.releaseResume != nil {
		<-screen.releaseResume
	}
	return screen.fail["resume"]
}

func (screen *fakeScreen) call(name string) error {
	screen.recorder.add(name)
	if panicValue, ok := screen.panicAt[name]; ok {
		panic(panicValue)
	}
	return screen.fail[name]
}

type fakePlatform struct {
	recorder *callRecorder
	fail     map[string]error
	state    TerminalState
}

func newFakePlatform(recorder *callRecorder) *fakePlatform {
	return &fakePlatform{
		recorder: recorder,
		fail:     make(map[string]error),
		state:    NewTerminalState("termios+pgrp"),
	}
}

func (platform *fakePlatform) SaveTerminal() (TerminalState, error) {
	platform.recorder.add("save_terminal")
	return platform.state, platform.fail["save_terminal"]
}

func (platform *fakePlatform) GiveForeground(processGroup int) error {
	platform.recorder.add(fmt.Sprintf("give_foreground:%d", processGroup))
	return platform.fail["give_foreground"]
}

func (platform *fakePlatform) ReclaimForeground(TerminalState) error {
	platform.recorder.add("reclaim_foreground")
	return platform.fail["reclaim_foreground"]
}

func (platform *fakePlatform) RestoreTerminal(TerminalState) error {
	platform.recorder.add("restore_terminal")
	return platform.fail["restore_terminal"]
}

type fakeProcess struct {
	recorder     *callRecorder
	processGroup int
	result       Result
	waitErr      error
	waitStarted  chan struct{}
	releaseWait  chan struct{}
}

func (process *fakeProcess) ProcessGroup() int { return process.processGroup }

func (process *fakeProcess) Wait() (Result, error) {
	process.recorder.add("wait")
	if process.waitStarted != nil {
		close(process.waitStarted)
	}
	if process.releaseWait != nil {
		<-process.releaseWait
	}
	return process.result, process.waitErr
}

func count(calls []string, target string) int {
	total := 0
	for _, call := range calls {
		if call == target {
			total++
		}
	}
	return total
}

func countPrefix(calls []string, prefix string) int {
	total := 0
	for _, call := range calls {
		if len(call) >= len(prefix) && call[:len(prefix)] == prefix {
			total++
		}
	}
	return total
}
