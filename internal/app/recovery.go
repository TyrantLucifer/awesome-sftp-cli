package app

import (
	"context"
	"errors"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/daemon"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
)

type reconnectPolicy struct {
	Delays []time.Duration
	Sleep  func(context.Context, time.Duration) error
	Jitter func(time.Duration) time.Duration
}

func defaultReconnectPolicy() reconnectPolicy {
	return reconnectPolicy{
		Delays: []time.Duration{100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond},
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
