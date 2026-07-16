package terminalhandoff

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var ErrBusy = errors.New("terminal handoff is already in progress")

const defaultPostSpawnCleanupTimeout = 2 * time.Second

type State string

const (
	StateActiveTUI          State = "active_tui"
	StateSuspending         State = "suspending"
	StateExternalForeground State = "external_foreground"
	StateReacquiring        State = "reacquiring"
	StateCleanup            State = "cleanup"
)

type Size struct {
	Columns int
	Rows    int
}

func (size Size) validate() error {
	if size.Columns <= 0 || size.Rows <= 0 {
		return fmt.Errorf("terminal size must be positive: %dx%d", size.Columns, size.Rows)
	}
	return nil
}

// Snapshot holds the complete UI identity frozen immediately before a
// handoff. Opaque is deliberately uninterpreted by this package; callers use
// it for pane, drawer, focus, Job cursor, and Preview request state.
type Snapshot struct {
	opaque any
	size   Size
}

func NewSnapshot(opaque any, size Size) Snapshot {
	return Snapshot{opaque: opaque, size: size}
}

func (snapshot Snapshot) Opaque() any {
	return snapshot.opaque
}

func (snapshot Snapshot) TerminalSize() Size {
	return snapshot.size
}

// TerminalState is an opaque platform snapshot containing the saved termios
// and foreground process group.
type TerminalState struct {
	opaque any
}

func NewTerminalState(opaque any) TerminalState {
	return TerminalState{opaque: opaque}
}

func (state TerminalState) Opaque() any {
	return state.opaque
}

type Screen interface {
	Freeze() (Snapshot, error)
	StopInput() error
	ShowCursor() error
	LeaveAlternate() error
	LeaveRaw() error
	EnterAlternate() error
	EnterRaw() error
	RestoreCursor(Snapshot) error
	ReplayResize(Size) error
	Resume(Snapshot) error
}

type Platform interface {
	SaveTerminal() (TerminalState, error)
	GiveForeground(processGroup int) error
	ReclaimForeground(TerminalState) error
	RestoreTerminal(TerminalState) error
}

type ExitKind string

const (
	ExitNormal   ExitKind = "normal"
	ExitNonZero  ExitKind = "nonzero"
	ExitSignaled ExitKind = "signal"
	ExitPTYLoss  ExitKind = "pty_loss"
)

type Result struct {
	Kind     ExitKind
	ExitCode int
	Signal   string
	Err      error
}

type Process interface {
	ProcessGroup() int
	// Terminate stops the complete external process group and must make Wait
	// return. It is used when terminal ownership cannot be transferred after a
	// successful spawn.
	Terminate() error
	Wait() (Result, error)
}

type Launcher interface {
	Start(context.Context) (Process, error)
}

type LauncherFunc func(context.Context) (Process, error)

func (function LauncherFunc) Start(ctx context.Context) (Process, error) {
	return function(ctx)
}

type Controller struct {
	screen                  Screen
	platform                Platform
	postSpawnCleanupTimeout time.Duration

	mu             sync.Mutex
	state          State
	busy           bool
	cleanupPending bool
	session        *handoffSession
}

type handoffSession struct {
	restoreOnce sync.Once
	restoreDone chan struct{}
	restoreErr  error
	suspendOnce sync.Once
	suspendDone chan struct{}

	snapshot      Snapshot
	hasSnapshot   bool
	terminalState TerminalState
	terminalSaved bool
	abnormal      bool
	latestSize    Size
	resizeVersion uint64
}

func NewController(screen Screen, platform Platform) (*Controller, error) {
	if screen == nil {
		return nil, errors.New("create terminal handoff controller: screen is nil")
	}
	if platform == nil {
		return nil, errors.New("create terminal handoff controller: platform is nil")
	}
	return &Controller{
		screen: screen, platform: platform, postSpawnCleanupTimeout: defaultPostSpawnCleanupTimeout,
		state: StateActiveTUI,
	}, nil
}

func (controller *Controller) State() State {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	return controller.state
}

func (controller *Controller) Run(ctx context.Context, launcher Launcher) (result Result, err error) {
	if ctx == nil {
		return Result{}, errors.New("run terminal handoff: context is nil")
	}
	if launcher == nil {
		return Result{}, errors.New("run terminal handoff: launcher is nil")
	}

	session, err := controller.begin()
	if err != nil {
		return Result{}, err
	}
	defer func() {
		controller.finishSuspension(session)
		if recovered := recover(); recovered != nil {
			controller.markAbnormal(session)
			_ = controller.Cleanup()
			controller.finish(session)
			panic(recovered)
		}
		restoreErr := controller.Cleanup()
		controller.finish(session)
		err = errors.Join(err, restoreErr)
	}()

	snapshot, freezeErr := controller.screen.Freeze()
	if freezeErr != nil {
		controller.markAbnormal(session)
		return Result{}, fmt.Errorf("freeze terminal UI: %w", freezeErr)
	}
	if sizeErr := snapshot.TerminalSize().validate(); sizeErr != nil {
		controller.markAbnormal(session)
		return Result{}, fmt.Errorf("freeze terminal UI: %w", sizeErr)
	}
	controller.recordSnapshot(session, snapshot)

	for _, operation := range []struct {
		name string
		call func() error
	}{
		{name: "stop screen input", call: controller.screen.StopInput},
		{name: "show cursor", call: controller.screen.ShowCursor},
		{name: "leave alternate screen", call: controller.screen.LeaveAlternate},
		{name: "leave raw mode", call: controller.screen.LeaveRaw},
	} {
		if operationErr := operation.call(); operationErr != nil {
			controller.markAbnormal(session)
			return Result{}, fmt.Errorf("suspend terminal UI: %s: %w", operation.name, operationErr)
		}
	}

	terminalState, saveErr := controller.platform.SaveTerminal()
	if saveErr != nil {
		controller.markAbnormal(session)
		return Result{}, fmt.Errorf("suspend terminal UI: save terminal: %w", saveErr)
	}
	controller.recordTerminalState(session, terminalState)

	process, startErr := launcher.Start(ctx)
	if startErr != nil {
		controller.markAbnormal(session)
		return Result{}, fmt.Errorf("start external process: %w", startErr)
	}
	if process == nil {
		controller.markAbnormal(session)
		return Result{}, errors.New("start external process: launcher returned a nil process")
	}
	processGroup := process.ProcessGroup()
	if processGroup <= 0 {
		controller.markAbnormal(session)
		return Result{}, errors.Join(
			fmt.Errorf("start external process: invalid process group %d", processGroup),
			controller.terminateAndReap(process),
		)
	}
	if foregroundErr := controller.platform.GiveForeground(processGroup); foregroundErr != nil {
		controller.markAbnormal(session)
		return Result{}, errors.Join(
			fmt.Errorf("give external process terminal foreground: %w", foregroundErr),
			controller.terminateAndReap(process),
		)
	}
	controller.setState(session, StateExternalForeground)
	controller.finishSuspension(session)

	result, waitErr := process.Wait()
	if waitErr != nil {
		controller.markAbnormal(session)
		return result, fmt.Errorf("wait for external process: %w", waitErr)
	}
	if result.Kind != ExitNormal {
		controller.markAbnormal(session)
	}
	return result, nil
}

func (controller *Controller) terminateAndReap(process Process) error {
	timeout := controller.postSpawnCleanupTimeout
	if timeout <= 0 {
		timeout = defaultPostSpawnCleanupTimeout
	}
	controller.mu.Lock()
	if controller.cleanupPending {
		controller.mu.Unlock()
		return errors.New("post-spawn cleanup is already pending")
	}
	controller.cleanupPending = true
	controller.mu.Unlock()
	cleanupDone := make(chan error, 1)
	go func() {
		defer func() {
			controller.mu.Lock()
			controller.cleanupPending = false
			controller.mu.Unlock()
		}()
		terminateErr := process.Terminate()
		_, waitErr := process.Wait()
		cleanupDone <- errors.Join(terminateErr, waitErr)
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case cleanupErr := <-cleanupDone:
		return cleanupErr
	case <-timer.C:
		return fmt.Errorf("post-spawn cleanup exceeded %s; termination and reap continue in background", timeout)
	}
}

func (controller *Controller) Resize(size Size) error {
	if err := size.validate(); err != nil {
		return fmt.Errorf("resize terminal handoff: %w", err)
	}

	controller.mu.Lock()
	session := controller.session
	if session != nil && controller.state != StateActiveTUI {
		session.latestSize = size
		session.resizeVersion++
		controller.mu.Unlock()
		return nil
	}
	controller.mu.Unlock()

	if err := controller.screen.ReplayResize(size); err != nil {
		return fmt.Errorf("resize active terminal UI: %w", err)
	}
	return nil
}

// Cleanup restores the current handoff exactly once. It is safe to call from
// normal completion, signal/PTY-loss handling, panic defers, and concurrent
// shutdown paths.
func (controller *Controller) Cleanup() error {
	controller.mu.Lock()
	session := controller.session
	controller.mu.Unlock()
	if session == nil {
		return nil
	}
	<-session.suspendDone

	session.restoreOnce.Do(func() {
		session.restoreErr = controller.restore(session)
		close(session.restoreDone)
	})
	<-session.restoreDone
	return session.restoreErr
}

func (controller *Controller) begin() (*handoffSession, error) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.busy || controller.cleanupPending {
		return nil, ErrBusy
	}
	session := &handoffSession{
		restoreDone: make(chan struct{}),
		suspendDone: make(chan struct{}),
	}
	controller.busy = true
	controller.session = session
	controller.state = StateSuspending
	return session, nil
}

func (controller *Controller) finishSuspension(session *handoffSession) {
	session.suspendOnce.Do(func() {
		close(session.suspendDone)
	})
}

func (controller *Controller) recordSnapshot(session *handoffSession, snapshot Snapshot) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.session != session {
		return
	}
	session.snapshot = snapshot
	session.hasSnapshot = true
	session.latestSize = snapshot.TerminalSize()
	session.resizeVersion = 1
}

func (controller *Controller) recordTerminalState(session *handoffSession, state TerminalState) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.session != session {
		return
	}
	session.terminalState = state
	session.terminalSaved = true
}

func (controller *Controller) markAbnormal(session *handoffSession) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.session == session {
		session.abnormal = true
	}
}

func (controller *Controller) setState(session *handoffSession, state State) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.session == session {
		controller.state = state
	}
}

func (controller *Controller) restore(session *handoffSession) error {
	controller.mu.Lock()
	if controller.session != session {
		controller.mu.Unlock()
		return nil
	}
	if session.abnormal {
		controller.state = StateCleanup
	} else {
		controller.state = StateReacquiring
	}
	hasSnapshot := session.hasSnapshot
	terminalSaved := session.terminalSaved
	terminalState := session.terminalState
	snapshot := session.snapshot
	controller.mu.Unlock()

	if !hasSnapshot {
		controller.setState(session, StateActiveTUI)
		return nil
	}

	var restoreErr error
	if terminalSaved {
		restoreErr = errors.Join(
			restoreErr,
			callRestore("reclaim terminal foreground", func() error {
				return controller.platform.ReclaimForeground(terminalState)
			}),
			callRestore("restore termios", func() error {
				return controller.platform.RestoreTerminal(terminalState)
			}),
		)
	}
	restoreErr = errors.Join(
		restoreErr,
		callRestore("enter alternate screen", controller.screen.EnterAlternate),
		callRestore("enter raw mode", controller.screen.EnterRaw),
		callRestore("restore cursor", func() error {
			return controller.screen.RestoreCursor(snapshot)
		}),
	)
	replayedVersion, resizeErr := controller.replayLatestResize(session)
	restoreErr = errors.Join(restoreErr, resizeErr)
	restoreErr = errors.Join(restoreErr, callRestore("resume terminal UI", func() error {
		return controller.screen.Resume(snapshot)
	}))

	controller.mu.Lock()
	latestSize := session.latestSize
	latestVersion := session.resizeVersion
	controller.state = StateActiveTUI
	controller.mu.Unlock()
	if latestVersion != replayedVersion {
		restoreErr = errors.Join(restoreErr, callRestore("replay terminal resize", func() error {
			return controller.screen.ReplayResize(latestSize)
		}))
	}
	return restoreErr
}

func (controller *Controller) replayLatestResize(session *handoffSession) (uint64, error) {
	var replayErr error
	for {
		controller.mu.Lock()
		size := session.latestSize
		version := session.resizeVersion
		controller.mu.Unlock()

		replayErr = errors.Join(replayErr, callRestore("replay terminal resize", func() error {
			return controller.screen.ReplayResize(size)
		}))

		controller.mu.Lock()
		unchanged := session.resizeVersion == version
		controller.mu.Unlock()
		if unchanged {
			return version, replayErr
		}
	}
}

func (controller *Controller) finish(session *handoffSession) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.session != session {
		return
	}
	controller.state = StateActiveTUI
	controller.session = nil
	controller.busy = false
}

func callRestore(operation string, call func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("restore terminal UI: %s panicked: %v", operation, recovered)
		}
	}()
	if callErr := call(); callErr != nil {
		return fmt.Errorf("restore terminal UI: %s: %w", operation, callErr)
	}
	return nil
}
