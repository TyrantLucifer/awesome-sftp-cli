package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/auth"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/buildinfo"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/daemon"
	helperruntime "github.com/TyrantLucifer/awesome-sftp-cli/internal/helper"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/platform"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/state/jobstore"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/transport/openssh"
)

const helperLifecycleHandshakeTimeout = 10 * time.Second

func configureDaemonHelperLifecycle(ctx context.Context, paths platform.Paths, sessions *daemon.ProviderSessions, jobs *jobstore.Store, broker *auth.Broker) error {
	if ctx == nil || sessions == nil || jobs == nil || broker == nil {
		return errors.New("configure daemon Helper lifecycle: owner dependencies are incomplete")
	}
	info := buildinfo.Current()
	target := helperruntime.Target{OS: info.GOOS, Arch: info.GOARCH}
	if _, err := helperruntime.ProductionReleaseAssets(info.Version, target); err != nil {
		return errors.New("configure daemon Helper lifecycle: build is not a canonical release")
	}
	state, err := helperruntime.NewStateStore(filepath.Join(paths.StateDir, "helpers"))
	if err != nil {
		return err
	}
	source := helperruntime.NewProductionReleaseSource()
	manager, err := helperruntime.NewLifecycleManager(helperruntime.LifecycleManagerConfig{
		Version:  info.Version,
		Target:   target,
		State:    state,
		Verifier: helperruntime.NewProductionVerifier(),
		Policy:   helperruntime.NewProductionPolicy(),
		Leaser:   jobs,
		ResolveRelease: func(resolveContext context.Context, version string, target helperruntime.Target, verifier helperruntime.Verifier, policy helperruntime.Policy) (helperruntime.LifecycleRelease, error) {
			resolved, resolveErr := source.Resolve(resolveContext, version, target, verifier, policy)
			if resolveErr != nil {
				return nil, resolveErr
			}
			return resolved, nil
		},
		OpenRemote: func(openContext context.Context, hostAlias string) (helperruntime.LifecycleRemoteLease, error) {
			return openDaemonHelperRemote(openContext, broker, hostAlias)
		},
		// The public command is still closed before RPC and no final-plan
		// confirmation channel exists yet. Returning nil keeps install/upgrade
		// closed even if production metadata is accidentally provisioned early.
		Consent: func(helperruntime.LifecycleRequest) helperruntime.InstallConsent { return nil },
	})
	if err != nil {
		return err
	}
	if err := manager.Recover(ctx); err != nil {
		return fmt.Errorf("recover daemon Helper lifecycle: %w", err)
	}
	sessions.SetHelperLifecycle(manager)
	return nil
}

func openDaemonHelperRemote(ctx context.Context, broker *auth.Broker, hostAlias string) (helperruntime.LifecycleRemoteLease, error) {
	attempt, err := broker.BeginAttempt(ctx, hostAlias, authenticationTimeout)
	if err != nil {
		return helperruntime.LifecycleRemoteLease{}, fmt.Errorf("start Helper authentication attempt: %w", err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = attempt.Close()
		}
	}()
	executable, err := os.Executable()
	if err != nil {
		return helperruntime.LifecycleRemoteLease{}, fmt.Errorf("find Helper authentication executable: %w", err)
	}
	if err := platform.ValidateExecutable(executable); err != nil {
		return helperruntime.LifecycleRemoteLease{}, fmt.Errorf("validate Helper authentication executable: %w", err)
	}
	environment, err := auth.OpenSSHEnvironment(os.Environ(), executable, attempt.Token())
	if err != nil {
		return helperruntime.LifecycleRemoteLease{}, fmt.Errorf("prepare Helper authentication environment: %w", err)
	}
	redactions := []string{string(attempt.Token())}
	sshConfig := openssh.Config{HostAlias: hostAlias, Environment: environment, Redact: redactions, Fresh: true}
	transport, err := openssh.Dial(ctx, sshConfig)
	if err != nil {
		return helperruntime.LifecycleRemoteLease{}, fmt.Errorf("open Helper SFTP transport: %w", err)
	}
	remote, err := helperruntime.NewSFTPInstallRemote(helperruntime.SFTPInstallRemoteConfig{
		Client: transport.Client(),
		Probe: func(probeContext context.Context) (helperruntime.Observation, error) {
			return helperruntime.RunOpenSSHBindingProbe(probeContext, helperruntime.BindingProbeConfig{
				SSHPath: openssh.DefaultBinary, HostAlias: hostAlias, Environment: environment,
				Redact: redactions, Timeout: helperLifecycleHandshakeTimeout,
			})
		},
		LinkAttributes: func(probeContext context.Context, remotePath string) (openssh.SFTPAttributes, error) {
			return openssh.ProbeLinkAttributes(probeContext, sshConfig, remotePath)
		},
		MkdirExact: func(mkdirContext context.Context, remotePath string, mode uint32) error {
			return openssh.MkdirExact(mkdirContext, sshConfig, remotePath, mode)
		},
	})
	if err != nil {
		_ = transport.Close()
		return helperruntime.LifecycleRemoteLease{}, err
	}
	lease := helperruntime.LifecycleRemoteLease{
		Remote: remote,
		Handshake: func(handshakeContext context.Context, finalPath string, manifest helperruntime.Manifest) (returnErr error) {
			session, startErr := helperruntime.StartOpenSSHSession(handshakeContext, helperruntime.OpenSSHSessionConfig{
				SSHPath: openssh.DefaultBinary, HostAlias: hostAlias,
				Plan: helperruntime.InstallPlan{FinalPath: finalPath}, Environment: environment, Redact: redactions,
				Hello: helperruntime.ClientHello{
					MinimumProtocol: manifest.ProtocolMajor, MaximumProtocol: manifest.ProtocolMajor,
					MaximumFrame: helperruntime.MaxHelperFrameBytes, MaximumConcurrent: 1, ClientVersion: manifest.Version,
				},
				HandshakeTimeout: helperLifecycleHandshakeTimeout,
			})
			if startErr != nil {
				return startErr
			}
			defer func() { returnErr = errors.Join(returnErr, session.Close()) }()
			return helperruntime.ValidateEnabledClient(helperruntime.EnablePlan{Manifest: manifest, FinalPath: finalPath}, session.Client(), nil)
		},
		Close: func() error {
			return errors.Join(transport.Close(), attempt.Close())
		},
	}
	ok = true
	return lease, nil
}
