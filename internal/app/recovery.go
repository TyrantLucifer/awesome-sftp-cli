package app

import (
	"context"
	"errors"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/daemon"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/retrypolicy"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/tui"
)

type paneRecoveryPhase uint8

const (
	paneRecoveryIdle paneRecoveryPhase = iota
	paneRecoveryConnecting
	paneRecoveryValidating
)

type paneRecovery struct {
	phase      paneRecoveryPhase
	generation uint64
	intent     tui.Intent
	fallback   bool
}

func (r *paneRecovery) beginConnection() {
	*r = paneRecovery{phase: paneRecoveryConnecting}
}

func (r *paneRecovery) connecting() bool {
	return r.phase == paneRecoveryConnecting
}

func (r *paneRecovery) connectionFailed() {
	*r = paneRecovery{}
}

func (r *paneRecovery) connected() {
	*r = paneRecovery{phase: paneRecoveryValidating}
}

func (r *paneRecovery) listingStarted(generation uint64, intent tui.Intent) {
	if r.phase != paneRecoveryValidating || generation == 0 || intent.Kind != tui.IntentList {
		return
	}
	r.generation = generation
	r.intent = intent
}

func (r *paneRecovery) listingFailed(failure tui.ListingFailed) (tui.Intent, bool) {
	if r.phase != paneRecoveryValidating || r.generation == 0 || failure.Generation != r.generation {
		return tui.Intent{}, false
	}
	if failure.Code != domain.CodeNotFound && failure.Code != domain.CodePermissionDenied {
		*r = paneRecovery{}
		return tui.Intent{}, false
	}
	parent, ok := recoveryParent(failure.Location)
	if !ok {
		*r = paneRecovery{}
		return tui.Intent{}, false
	}
	next := r.intent
	next.Location = parent
	r.generation = 0
	r.fallback = true
	return next, true
}

func (r *paneRecovery) listingCompleted(page tui.ListingPage) bool {
	if r.phase != paneRecoveryValidating || r.generation == 0 || page.Generation != r.generation || !page.Done {
		return false
	}
	recovered := r.fallback
	*r = paneRecovery{}
	return recovered
}

type reconnectPolicy struct {
	Delays              []time.Duration
	DaemonShutdownGrace time.Duration
	Sleep               func(context.Context, time.Duration) error
	Jitter              func(time.Duration) time.Duration
}

const daemonLossShutdownPollInterval = 25 * time.Millisecond

var errDaemonControlSocketStillPresent = errors.New("control socket still exists after connection failure")

func defaultReconnectPolicy() reconnectPolicy {
	return newReconnectPolicy(retrypolicy.DefaultReconnectDelays())
}

func newReconnectPolicy(delays []time.Duration) reconnectPolicy {
	return reconnectPolicy{
		Delays: append([]time.Duration(nil), delays...),
		Sleep: func(ctx context.Context, delay time.Duration) error {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-timer.C:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
		Jitter: func(delay time.Duration) time.Duration {
			spread := delay / 5
			if spread <= 0 {
				return delay
			}
			// #nosec G404 -- retry timing jitter is not a security-sensitive random value.
			return delay - spread + time.Duration(rand.Int64N(int64(2*spread)+1))
		},
	}
}

func runReconnect(ctx context.Context, policy reconnectPolicy, attempt func() error) error {
	if policy.Sleep == nil {
		policy.Sleep = defaultReconnectPolicy().Sleep
	}
	var lastErr error
	for index := 0; index <= len(policy.Delays); index++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		lastErr = attempt()
		if lastErr == nil {
			return nil
		}
		if !retryAfterReconnect(lastErr) || index == len(policy.Delays) {
			return lastErr
		}
		delay := policy.Delays[index]
		if policy.Jitter != nil {
			delay = policy.Jitter(delay)
		}
		if err := policy.Sleep(ctx, delay); err != nil {
			return err
		}
	}
	return lastErr
}

func connectDaemonAfterLoss(
	ctx context.Context,
	policy reconnectPolicy,
	connect func(context.Context) (*daemon.Client, error),
) (*daemon.Client, error) {
	if policy.Sleep == nil {
		policy.Sleep = defaultReconnectPolicy().Sleep
	}
	var (
		lastErr error
		waited  time.Duration
	)
	for index := 0; index <= len(policy.Delays); index++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		client, err := connect(ctx)
		if err == nil {
			return client, nil
		}
		lastErr = err
		if index == len(policy.Delays) {
			break
		}
		delay := policy.Delays[index]
		if policy.Jitter != nil {
			delay = policy.Jitter(delay)
		}
		if err := policy.Sleep(ctx, delay); err != nil {
			return nil, err
		}
		waited += delay
	}
	remaining := policy.DaemonShutdownGrace - waited
	for errors.Is(lastErr, errDaemonControlSocketStillPresent) && remaining > 0 {
		delay := min(daemonLossShutdownPollInterval, remaining)
		if err := policy.Sleep(ctx, delay); err != nil {
			return nil, err
		}
		remaining -= delay
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		client, err := connect(ctx)
		if err == nil {
			return client, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func retryAfterReconnect(err error) bool {
	var remote *daemon.RemoteError
	return errors.As(err, &remote) && remote.RPC.Retry.Kind == domain.RetryAfterReconnect
}

func classifySSHConnectError(err error) (domain.Code, domain.RetryKind) {
	if errors.Is(err, context.Canceled) {
		return domain.CodeCanceled, domain.RetryNever
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return domain.CodeTimeout, domain.RetryAfterReconnect
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "host identification has changed"),
		strings.Contains(message, "host key verification failed"):
		return domain.CodePermissionDenied, domain.RetryNever
	case strings.Contains(message, "permission denied"),
		strings.Contains(message, "authentication failed"):
		return domain.CodeAuthRequired, domain.RetryAfterAuth
	case strings.Contains(message, "subsystem request failed"),
		strings.Contains(message, "subsystem is not available"):
		return domain.CodeUnsupported, domain.RetryNever
	case strings.Contains(message, "connection refused"),
		strings.Contains(message, "connection timed out"),
		strings.Contains(message, "connection reset"),
		strings.Contains(message, "connection closed"),
		strings.Contains(message, "no route to host"),
		strings.Contains(message, "unexpected eof"):
		return domain.CodeTransportInterrupted, domain.RetryAfterReconnect
	default:
		return domain.CodeInternal, domain.RetryNever
	}
}
