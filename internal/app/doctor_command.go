package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/config"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/doctor"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/platform"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/statefs"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/transport/openssh"
)

const doctorLowDiskBytes = uint64(64 * 1024 * 1024)

type doctorRuntime struct {
	paths              platform.Paths
	purpose            platform.ValidationPurpose
	pathExists         func(string) (bool, error)
	loadConfig         func(string) (config.Config, error)
	validateDirectory  func(string, platform.ValidationPurpose) error
	validateSocket     func(string, platform.ValidationPurpose) error
	dialDaemon         func(context.Context, string, platform.ValidationPurpose) error
	validateExecutable func(string) error
	inspectDatabase    func(context.Context, string) (uint64, error)
	availableBytes     func(string) (uint64, error)
	inspectOpenSSH     func(context.Context, string) (openssh.ConfigInspection, error)
	dialEndpoint       func(context.Context, string, string) error
}

type doctorOptions struct {
	format   string
	endpoint string
}

func runDoctor(ctx context.Context, args []string, stdout io.Writer, _ io.Writer) error {
	paths, _, err := platform.ResolvePaths(platform.Overrides{})
	if err != nil {
		return machineCommandError(args, NewExitError(ExitConfig, errors.New("resolve doctor paths")))
	}
	return runDoctorWithRuntime(ctx, args, stdout, systemDoctorRuntime(paths))
}

func systemDoctorRuntime(paths platform.Paths) doctorRuntime {
	purpose := platform.RuntimeValidationPurpose(paths)
	return doctorRuntime{
		paths: paths, purpose: purpose,
		pathExists: func(path string) (bool, error) {
			_, err := os.Lstat(path)
			if errors.Is(err, os.ErrNotExist) {
				return false, nil
			}
			return err == nil, err
		},
		loadConfig:        loadApplicationConfig,
		validateDirectory: platform.ValidatePrivateDirectory,
		validateSocket:    platform.ValidatePrivateSocket,
		dialDaemon: func(ctx context.Context, path string, purpose platform.ValidationPurpose) error {
			probeCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
			defer cancel()
			connection, err := platform.DialControlSocket(probeCtx, path, purpose)
			if err != nil {
				return err
			}
			return connection.Close()
		},
		validateExecutable: platform.ValidateExecutable,
		inspectDatabase:    statefs.InspectDatabase,
		availableBytes:     statefs.AvailableFilesystemBytes,
		inspectOpenSSH: func(ctx context.Context, alias string) (openssh.ConfigInspection, error) {
			probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			return openssh.InspectConfig(probeCtx, openssh.DefaultBinary, alias)
		},
		dialEndpoint: func(ctx context.Context, host, port string) error {
			probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			connection, err := (&net.Dialer{}).DialContext(probeCtx, "tcp", net.JoinHostPort(host, port))
			if err != nil {
				return err
			}
			return connection.Close()
		},
	}
}

func runDoctorWithRuntime(ctx context.Context, args []string, stdout io.Writer, runtime doctorRuntime) error {
	options, err := parseDoctorOptions(args)
	if err != nil {
		return machineCommandError(args, err)
	}
	report := doctor.Run(ctx, doctorProbes(runtime, options.endpoint), options.endpoint != "")
	switch options.format {
	case "json":
		encoder := json.NewEncoder(stdout)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(report); err != nil {
			return machineCommandError(args, NewExitError(ExitInternal, errors.New("encode doctor report")))
		}
	case "human":
		for _, result := range report.Results {
			if _, err := fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", result.Code, result.Status, result.DetailCode, result.Remediation); err != nil {
				return machineCommandError(args, NewExitError(ExitInternal, errors.New("write doctor report")))
			}
		}
	}
	return nil
}

func parseDoctorOptions(args []string) (doctorOptions, error) {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	format := flags.String("format", "human", "human or json")
	endpoint := flags.String("endpoint", "", "SSH host alias")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return doctorOptions{}, NewExitError(ExitUsage, errors.New("doctor arguments are invalid"))
	}
	if *format != "human" && *format != "json" {
		return doctorOptions{}, NewExitError(ExitUsage, errors.New("doctor format must be human or json"))
	}
	if *endpoint != "" {
		if err := openssh.ValidateHostAlias(*endpoint); err != nil {
			return doctorOptions{}, NewExitError(ExitUsage, errors.New("doctor endpoint is invalid"))
		}
	}
	return doctorOptions{format: *format, endpoint: *endpoint}, nil
}

func doctorProbes(runtime doctorRuntime, endpoint string) map[doctor.Code]doctor.Probe {
	var configOnce sync.Once
	var applicationConfig config.Config
	var configErr error
	loadConfig := func() (config.Config, error) {
		configOnce.Do(func() { applicationConfig, configErr = runtime.loadConfig(runtime.paths.ConfigFile) })
		return applicationConfig, configErr
	}

	var endpointOnce sync.Once
	var endpointConfig openssh.ConfigInspection
	var endpointErr error
	inspectEndpoint := func(ctx context.Context) (openssh.ConfigInspection, error) {
		endpointOnce.Do(func() { endpointConfig, endpointErr = runtime.inspectOpenSSH(ctx, endpoint) })
		return endpointConfig, endpointErr
	}

	return map[doctor.Code]doctor.Probe{
		doctor.CheckConfig: func(context.Context) (doctor.Observation, error) {
			exists, err := runtime.pathExists(runtime.paths.ConfigFile)
			if err != nil {
				return doctor.Observation{}, err
			}
			if _, err := loadConfig(); err != nil {
				return doctor.Observation{}, err
			}
			if !exists {
				return doctorObservation(doctor.Pass, "config_defaults"), nil
			}
			return doctorObservation(doctor.Pass, "config_valid"), nil
		},
		doctor.CheckRuntimeDirectory: pathDoctorProbe(runtime, runtime.paths.RuntimeDir, runtime.purpose, "runtime_private", "runtime_not_created", runtime.validateDirectory),
		doctor.CheckSocket: func(context.Context) (doctor.Observation, error) {
			exists, err := runtime.pathExists(runtime.paths.ControlSocket)
			if err != nil {
				return doctor.Observation{}, err
			}
			if !exists {
				return doctorObservation(doctor.Warn, "socket_not_created"), nil
			}
			if err := runtime.validateSocket(runtime.paths.ControlSocket, runtime.purpose); err != nil {
				return doctor.Observation{}, err
			}
			return doctorObservation(doctor.Pass, "socket_private"), nil
		},
		doctor.CheckDaemon: func(ctx context.Context) (doctor.Observation, error) {
			exists, err := runtime.pathExists(runtime.paths.ControlSocket)
			if err != nil {
				return doctor.Observation{}, err
			}
			if !exists {
				return doctorObservation(doctor.Warn, "daemon_not_running"), nil
			}
			if err := runtime.dialDaemon(ctx, runtime.paths.ControlSocket, runtime.purpose); err != nil {
				return doctor.Observation{}, err
			}
			return doctorObservation(doctor.Pass, "daemon_reachable"), nil
		},
		doctor.CheckOpenSSH: func(context.Context) (doctor.Observation, error) {
			if err := runtime.validateExecutable(openssh.DefaultBinary); err != nil {
				return doctor.Observation{}, err
			}
			return doctorObservation(doctor.Pass, "openssh_validated"), nil
		},
		doctor.CheckKnownHosts: func(ctx context.Context) (doctor.Observation, error) {
			if endpoint == "" {
				return doctorObservation(doctor.Skipped, "endpoint_not_requested"), nil
			}
			inspection, err := inspectEndpoint(ctx)
			if err != nil {
				return doctor.Observation{}, err
			}
			switch inspection.StrictHostKeyChecking {
			case "no", "off", "false":
				return doctorObservation(doctor.Fail, "known_hosts_disabled"), nil
			}
			if !inspection.KnownHostsConfigured {
				return doctorObservation(doctor.Fail, "known_hosts_unconfigured"), nil
			}
			return doctorObservation(doctor.Pass, "known_hosts_policy_valid"), nil
		},
		doctor.CheckDatabase: func(ctx context.Context) (doctor.Observation, error) {
			exists, err := runtime.pathExists(runtime.paths.DatabaseFile)
			if err != nil {
				return doctor.Observation{}, err
			}
			if !exists {
				return doctorObservation(doctor.Warn, "database_not_created"), nil
			}
			if _, err := runtime.inspectDatabase(ctx, runtime.paths.DatabaseFile); err != nil {
				if errors.Is(err, statefs.ErrDatabaseActive) {
					return doctorObservation(doctor.Warn, "database_active"), nil
				}
				return doctor.Observation{}, err
			}
			return doctorObservation(doctor.Pass, "database_healthy"), nil
		},
		doctor.CheckCache: pathDoctorProbe(runtime, runtime.paths.CacheDir, platform.ValidatePersistent, "cache_private", "cache_not_created", runtime.validateDirectory),
		doctor.CheckHelper: func(context.Context) (doctor.Observation, error) {
			loaded, err := loadConfig()
			if err != nil {
				return doctor.Observation{}, err
			}
			if !loaded.Helper.Enabled {
				return doctorObservation(doctor.Pass, "helper_disabled"), nil
			}
			if endpoint == "" {
				return doctorObservation(doctor.Warn, "helper_endpoint_required"), nil
			}
			return doctorObservation(doctor.Warn, "helper_distribution_closed"), nil
		},
		doctor.CheckDiskSpace: func(context.Context) (doctor.Observation, error) {
			root, err := nearestExistingDoctorPath(runtime, runtime.paths.StateDir)
			if err != nil {
				return doctor.Observation{}, err
			}
			available, err := runtime.availableBytes(root)
			if err != nil {
				return doctor.Observation{}, err
			}
			if available < doctorLowDiskBytes {
				return doctorObservation(doctor.Warn, "disk_space_low"), nil
			}
			return doctorObservation(doctor.Pass, "disk_space_available"), nil
		},
		doctor.CheckEndpoint: func(ctx context.Context) (doctor.Observation, error) {
			inspection, err := inspectEndpoint(ctx)
			if err != nil {
				return doctor.Observation{}, err
			}
			if inspection.ProxyConfigured {
				return doctorObservation(doctor.Warn, "endpoint_proxy_not_probed"), nil
			}
			if err := runtime.dialEndpoint(ctx, inspection.Hostname, inspection.Port); err != nil {
				return doctor.Observation{}, err
			}
			return doctorObservation(doctor.Pass, "endpoint_reachable"), nil
		},
	}
}

func pathDoctorProbe(runtime doctorRuntime, path string, purpose platform.ValidationPurpose, presentDetail, absentDetail string, validate func(string, platform.ValidationPurpose) error) doctor.Probe {
	return func(context.Context) (doctor.Observation, error) {
		exists, err := runtime.pathExists(path)
		if err != nil {
			return doctor.Observation{}, err
		}
		if !exists {
			return doctorObservation(doctor.Warn, absentDetail), nil
		}
		if err := validate(path, purpose); err != nil {
			return doctor.Observation{}, err
		}
		return doctorObservation(doctor.Pass, presentDetail), nil
	}
}

func nearestExistingDoctorPath(runtime doctorRuntime, path string) (string, error) {
	for {
		exists, err := runtime.pathExists(path)
		if err != nil {
			return "", err
		}
		if exists {
			return path, nil
		}
		parent := filepath.Dir(path)
		if parent == path || strings.TrimSpace(parent) == "" {
			return "", errors.New("doctor disk path has no existing ancestor")
		}
		path = parent
	}
}

func doctorObservation(status doctor.Status, detail string) doctor.Observation {
	return doctor.Observation{Status: status, DetailCode: detail}
}
