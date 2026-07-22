package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/daemon"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/ipc"
)

func TestUpgradeCommandValidatesBeforeRuntimeActivity(t *testing.T) {
	for _, args := range [][]string{{"unexpected"}, {"--format"}, {"--format", "yaml"}, {"--format", "json", "--format", "human"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			plans := 0
			runtime := upgradeCommandRuntime{plan: func(context.Context) (upgradePlan, error) {
				plans++
				return upgradePlan{}, nil
			}}
			err := runUpgradeWithRuntime(t.Context(), args, &bytes.Buffer{}, runtime)
			if err == nil || exitCode(err) != ExitUsage {
				t.Fatalf("args %q error = %v, exit = %d", args, err, exitCode(err))
			}
			if plans != 0 {
				t.Fatalf("args %q performed %d preflight calls before validation", args, plans)
			}
		})
	}
}

func TestUpgradeAlreadyCurrentDoesNotTouchDaemon(t *testing.T) {
	probes := 0
	runtime := upgradeCommandRuntime{
		plan: func(context.Context) (upgradePlan, error) {
			return upgradePlan{Channel: upgradeChannelStandalone, CurrentVersion: "0.1.7", TargetVersion: "0.1.7"}, nil
		},
		probe: func(context.Context) (daemonControlClient, bool, error) {
			probes++
			return nil, false, nil
		},
	}
	var stdout bytes.Buffer
	if err := runUpgradeWithRuntime(t.Context(), []string{"--format", "json"}, &stdout, runtime); err != nil {
		t.Fatal(err)
	}
	if probes != 0 {
		t.Fatalf("already-current upgrade probed daemon %d times", probes)
	}
	var output upgradeCommandOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	if output.Status != "already_current" || output.Channel != upgradeChannelStandalone || output.Version != "0.1.7" || output.DaemonRestarted {
		t.Fatalf("output = %#v", output)
	}
}

func TestUpgradeStopsAppliesRestartsAndVerifiesInOrder(t *testing.T) {
	var events []string
	client := &fakeDaemonControlClient{info: testDaemonInfo()}
	started := &fakeDaemonControlClient{info: testDaemonInfo()}
	runtime := upgradeCommandRuntime{
		plan: func(context.Context) (upgradePlan, error) {
			events = append(events, "plan")
			return upgradePlan{Channel: upgradeChannelHomebrew, CurrentVersion: "0.1.7", TargetVersion: "0.1.8", Available: true}, nil
		},
		probe: func(context.Context) (daemonControlClient, bool, error) {
			events = append(events, "probe")
			return client, true, nil
		},
		waitStopped: func(context.Context) error {
			events = append(events, "wait_stopped")
			if client.closed != 1 {
				return errors.New("client was not closed before wait")
			}
			return nil
		},
		apply: func(context.Context, upgradePlan) error {
			events = append(events, "apply")
			return nil
		},
		start: func(context.Context) (daemonControlClient, error) {
			events = append(events, "start")
			return started, nil
		},
		verify: func(context.Context, upgradePlan, daemonControlClient) error {
			events = append(events, "verify")
			return nil
		},
	}
	var stdout bytes.Buffer
	if err := runUpgradeWithRuntime(t.Context(), nil, &stdout, runtime); err != nil {
		t.Fatal(err)
	}
	want := []string{"plan", "probe", "wait_stopped", "apply", "start", "verify"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
	if !client.shutdownRequest.ForUpgrade || client.callName != daemon.RequestShutdown || client.closed != 1 || started.closed != 1 {
		t.Fatalf("old client = %#v, started client = %#v", client, started)
	}
	if !strings.Contains(stdout.String(), "Upgraded amsftp from 0.1.7 to 0.1.8") || !strings.Contains(stdout.String(), "daemon restarted") {
		t.Fatalf("output = %q", stdout.String())
	}
}

func TestUpgradeLeavesStoppedDaemonStopped(t *testing.T) {
	starts := 0
	var verifiedClient daemonControlClient
	runtime := upgradeCommandRuntime{
		plan: func(context.Context) (upgradePlan, error) {
			return upgradePlan{Channel: upgradeChannelStandalone, CurrentVersion: "0.1.7", TargetVersion: "0.1.8", Available: true}, nil
		},
		probe: func(context.Context) (daemonControlClient, bool, error) { return nil, false, nil },
		apply: func(context.Context, upgradePlan) error { return nil },
		start: func(context.Context) (daemonControlClient, error) {
			starts++
			return nil, errors.New("must not start")
		},
		verify: func(_ context.Context, _ upgradePlan, client daemonControlClient) error {
			verifiedClient = client
			return nil
		},
	}
	if err := runUpgradeWithRuntime(t.Context(), nil, &bytes.Buffer{}, runtime); err != nil {
		t.Fatal(err)
	}
	if starts != 0 || verifiedClient != nil {
		t.Fatalf("starts = %d, verified client = %#v", starts, verifiedClient)
	}
}

func TestUpgradeRefusesActiveJobBeforePackageMutation(t *testing.T) {
	client := &fakeDaemonControlClient{
		info: testDaemonInfo(),
		callError: &daemon.RemoteError{RPC: ipc.RPCError{
			Code: domain.CodeConflict, Message: "daemon has active Jobs",
			Retry: domain.RetryAdvice{Kind: domain.RetryAfterConflict}, Effect: domain.EffectNone,
		}},
	}
	applies := 0
	runtime := upgradeCommandRuntime{
		plan: func(context.Context) (upgradePlan, error) {
			return upgradePlan{Channel: upgradeChannelHomebrew, CurrentVersion: "0.1.7", TargetVersion: "0.1.8", Available: true}, nil
		},
		probe: func(context.Context) (daemonControlClient, bool, error) { return client, true, nil },
		apply: func(context.Context, upgradePlan) error { applies++; return nil },
	}
	err := runUpgradeWithRuntime(t.Context(), nil, &bytes.Buffer{}, runtime)
	if err == nil || exitCode(err) != ExitConflict || !strings.Contains(err.Error(), "active Jobs") {
		t.Fatalf("error = %v, exit = %d", err, exitCode(err))
	}
	if applies != 0 || client.closed != 1 {
		t.Fatalf("applies = %d, client closes = %d", applies, client.closed)
	}
}

func TestUpgradeApplyFailureAttemptsDaemonRecovery(t *testing.T) {
	client := &fakeDaemonControlClient{info: testDaemonInfo()}
	recovery := &fakeDaemonControlClient{info: testDaemonInfo()}
	starts := 0
	runtime := upgradeCommandRuntime{
		plan: func(context.Context) (upgradePlan, error) {
			return upgradePlan{Channel: upgradeChannelStandalone, CurrentVersion: "0.1.7", TargetVersion: "0.1.8", Available: true}, nil
		},
		probe:       func(context.Context) (daemonControlClient, bool, error) { return client, true, nil },
		waitStopped: func(context.Context) error { return nil },
		apply:       func(context.Context, upgradePlan) error { return errors.New("package manager failed with secret path") },
		start:       func(context.Context) (daemonControlClient, error) { starts++; return recovery, nil },
	}
	err := runUpgradeWithRuntime(t.Context(), []string{"--format", "json"}, &bytes.Buffer{}, runtime)
	if err == nil || exitCode(err) != ExitInternal || starts != 1 || recovery.closed != 1 {
		t.Fatalf("error = %v, exit = %d, starts = %d, recovery closes = %d", err, exitCode(err), starts, recovery.closed)
	}
}
