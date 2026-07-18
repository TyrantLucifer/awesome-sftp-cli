package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/buildinfo"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/config"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/daemon"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/diagnostic"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/doctor"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/ipc"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/platform"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/redaction"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/supportbundle"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transfer"
)

const supportBundleOutputVersion = 1

var supportBundleConsentPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type supportBundleRuntime struct {
	compose func(context.Context) ([]supportbundle.Source, error)
	publish func(context.Context, string, []byte) error
}

type supportBundleSnapshot struct {
	Build          buildinfo.Info
	Config         config.Config
	ConfigStatus   string
	Doctor         doctor.Report
	Diagnostics    []diagnostic.Record
	Jobs           []transfer.JobView
	DaemonInfo     daemon.ClientInfo
	DaemonStatus   string
	DatabaseStatus string
	DatabaseDetail string
}

type supportBundleOptions struct {
	command string
	format  string
	consent string
	output  string
}

type supportBundleCreateResult struct {
	OutputVersion int    `json:"output_version"`
	Status        string `json:"status"`
	Size          int    `json:"size"`
	SHA256        string `json:"sha256"`
}

func runSupportBundle(ctx context.Context, args []string, stdout io.Writer, _ io.Writer) error {
	paths, _, err := platformResolveSupportBundlePaths()
	if err != nil {
		return machineCommandError(args, NewExitError(ExitConfig, errors.New("resolve support-bundle paths")))
	}
	runtime := supportBundleRuntime{
		compose: func(ctx context.Context) ([]supportbundle.Source, error) {
			return gatherSupportBundleSources(ctx, paths)
		},
		publish: supportbundle.Publish,
	}
	return runSupportBundleWithRuntime(ctx, args, stdout, runtime)
}

func runSupportBundleWithRuntime(ctx context.Context, args []string, stdout io.Writer, runtime supportBundleRuntime) error {
	options, err := parseSupportBundleOptions(args)
	if err != nil {
		return machineCommandError(args, err)
	}
	if runtime.compose == nil || runtime.publish == nil {
		return machineCommandError(args, NewExitError(ExitInternal, errors.New("support-bundle runtime is incomplete")))
	}
	sources, err := runtime.compose(ctx)
	if err != nil {
		return machineCommandError(args, NewExitError(ExitInternal, errors.New("compose support bundle failed")))
	}
	if options.command == "preview" {
		plan, err := supportbundle.Preview(sources)
		if err != nil {
			return machineCommandError(args, NewExitError(ExitInternal, errors.New("preview support bundle failed")))
		}
		return machineCommandError(args, writeSupportBundlePreview(stdout, options.format, plan))
	}

	bundle, err := supportbundle.Build(sources, options.consent)
	if err != nil {
		return machineCommandError(args, NewExitError(ExitConflict, errors.New("support-bundle preview consent no longer matches")))
	}
	if err := runtime.publish(ctx, options.output, bundle); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return machineCommandError(args, NewExitError(ExitCanceled, errors.New("support-bundle publication canceled")))
		}
		return machineCommandError(args, NewExitError(ExitConflict, errors.New("support-bundle publication failed")))
	}
	digest := sha256.Sum256(bundle)
	result := supportBundleCreateResult{OutputVersion: supportBundleOutputVersion, Status: "published", Size: len(bundle), SHA256: hex.EncodeToString(digest[:])}
	return machineCommandError(args, writeSupportBundleCreate(stdout, options.format, result))
}

func parseSupportBundleOptions(args []string) (supportBundleOptions, error) {
	if len(args) == 0 || args[0] != "preview" && args[0] != "create" {
		return supportBundleOptions{}, NewExitError(ExitUsage, errors.New("support-bundle command must be preview or create"))
	}
	options := supportBundleOptions{command: args[0]}
	flags := flag.NewFlagSet("support-bundle "+options.command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.format, "format", "human", "human or json")
	if options.command == "create" {
		flags.StringVar(&options.consent, "consent", "", "preview consent digest")
		flags.StringVar(&options.output, "output", "", "absolute output path")
	}
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return supportBundleOptions{}, NewExitError(ExitUsage, errors.New("support-bundle arguments are invalid"))
	}
	if options.format != "human" && options.format != "json" {
		return supportBundleOptions{}, NewExitError(ExitUsage, errors.New("support-bundle format must be human or json"))
	}
	if options.command == "create" {
		if !supportBundleConsentPattern.MatchString(options.consent) {
			return supportBundleOptions{}, NewExitError(ExitUsage, errors.New("support-bundle consent must be a lowercase SHA-256 digest"))
		}
		if !filepath.IsAbs(options.output) || filepath.Clean(options.output) != options.output {
			return supportBundleOptions{}, NewExitError(ExitUsage, errors.New("support-bundle output must be a canonical absolute path"))
		}
	}
	return options, nil
}

func writeSupportBundlePreview(writer io.Writer, format string, plan supportbundle.Plan) error {
	if format == "json" {
		encoder := json.NewEncoder(writer)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(plan); err != nil {
			return NewExitError(ExitInternal, errors.New("encode support-bundle preview"))
		}
		return nil
	}
	for _, file := range plan.Files {
		if _, err := fmt.Fprintf(writer, "%s\t%s\t%d\t%s\n", file.Name, file.Sensitivity, file.Size, file.SHA256); err != nil {
			return NewExitError(ExitInternal, errors.New("write support-bundle preview"))
		}
	}
	if _, err := fmt.Fprintf(writer, "consent\t%s\n", plan.ConsentDigest); err != nil {
		return NewExitError(ExitInternal, errors.New("write support-bundle preview"))
	}
	return nil
}

func writeSupportBundleCreate(writer io.Writer, format string, result supportBundleCreateResult) error {
	if format == "json" {
		encoder := json.NewEncoder(writer)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(result); err != nil {
			return NewExitError(ExitInternal, errors.New("encode support-bundle result"))
		}
		return nil
	}
	if _, err := fmt.Fprintf(writer, "%s\t%d\t%s\n", result.Status, result.Size, result.SHA256); err != nil {
		return NewExitError(ExitInternal, errors.New("write support-bundle result"))
	}
	return nil
}

// platformResolveSupportBundlePaths is kept separate so the command never prepares or mutates runtime paths.
func platformResolveSupportBundlePaths() (platform.Paths, []platform.Diagnostic, error) {
	return platform.ResolvePaths(platform.Overrides{})
}

func gatherSupportBundleSources(ctx context.Context, paths platform.Paths) ([]supportbundle.Source, error) {
	loadedConfig, configErr := loadApplicationConfig(paths.ConfigFile)
	configStatus := "valid"
	if configErr != nil {
		loadedConfig = config.Default()
		configStatus = "invalid"
	} else if _, err := os.Lstat(paths.ConfigFile); errors.Is(err, os.ErrNotExist) {
		configStatus = "defaults"
	}
	runtime := systemDoctorRuntime(paths)
	report := doctor.Run(ctx, doctorProbes(runtime, ""), false)
	snapshot := supportBundleSnapshot{
		Build: buildinfo.Current(), Config: loadedConfig, ConfigStatus: configStatus, Doctor: report,
		DaemonStatus: "unavailable", DatabaseStatus: "unavailable", DatabaseDetail: "database_unavailable",
	}
	for _, result := range report.Results {
		if result.Code == doctor.CheckDatabase {
			snapshot.DatabaseStatus = string(result.Status)
			snapshot.DatabaseDetail = result.DetailCode
			break
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	client, exists, err := probeDaemon(probeCtx, paths, platform.RuntimeValidationPurpose(paths))
	if err == nil && exists && client != nil {
		snapshot.DaemonStatus = "reachable"
		snapshot.DaemonInfo = client.Info()
		var diagnostics daemon.DiagnosticListResponse
		if callErr := client.Call(probeCtx, daemon.DiagnosticList, daemon.DiagnosticListRequest{Limit: 256}, &diagnostics); callErr == nil {
			snapshot.Diagnostics = diagnostics.Records
		}
		var jobs daemon.JobListResponse
		if callErr := client.Call(probeCtx, daemon.JobList, daemon.JobListRequest{Limit: 100}, &jobs); callErr == nil {
			snapshot.Jobs = jobs.Jobs
		}
		_ = client.Close()
	} else if exists {
		snapshot.DaemonStatus = "unhealthy"
	}
	return composeSupportBundleSources(snapshot)
}

type supportBundleVersion struct {
	OutputVersion int    `json:"output_version"`
	Version       string `json:"version"`
	Commit        string `json:"commit"`
	Dirty         bool   `json:"dirty"`
	GoVersion     string `json:"go_version"`
}

type supportBundlePlatform struct {
	OutputVersion int    `json:"output_version"`
	GOOS          string `json:"goos"`
	GOARCH        string `json:"goarch"`
}

type supportBundleConfigShape struct {
	OutputVersion         int    `json:"output_version"`
	Status                string `json:"status"`
	SchemaVersion         int    `json:"schema_version"`
	HelperEnabled         bool   `json:"helper_enabled"`
	DirectTransferEnabled bool   `json:"direct_transfer_enabled"`
	EditorConfigured      bool   `json:"editor_configured"`
	OpenerConfigured      bool   `json:"opener_configured"`
	PreviewerCount        int    `json:"previewer_count"`
	KeymapOverrideCount   int    `json:"keymap_override_count"`
}

type reviewedDiagnostic struct {
	Sequence  uint64 `json:"sequence"`
	Time      string `json:"time,omitempty"`
	Level     string `json:"level,omitempty"`
	Component string `json:"component,omitempty"`
	Event     string `json:"event,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
}

type reviewedJob struct {
	State           string  `json:"state,omitempty"`
	Kind            string  `json:"kind,omitempty"`
	Route           string  `json:"route,omitempty"`
	PlannedRoute    string  `json:"planned_route,omitempty"`
	DowngradedFrom  string  `json:"downgraded_from,omitempty"`
	RouteReason     string  `json:"route_reason,omitempty"`
	Phase           string  `json:"phase,omitempty"`
	Bytes           uint64  `json:"bytes"`
	BytesTotal      *uint64 `json:"bytes_total,omitempty"`
	Items           uint64  `json:"items"`
	PauseRequested  bool    `json:"pause_requested"`
	CancelRequested bool    `json:"cancel_requested"`
}

type reviewedCapability struct {
	Name    string `json:"name"`
	Version uint16 `json:"version"`
}

func composeSupportBundleSources(snapshot supportBundleSnapshot) ([]supportbundle.Source, error) {
	version := supportBundleVersion{supportBundleOutputVersion, reviewedSystem(snapshot.Build.Version), reviewedSystem(snapshot.Build.Commit), snapshot.Build.Dirty, reviewedSystem(snapshot.Build.GoVersion)}
	platformSnapshot := supportBundlePlatform{supportBundleOutputVersion, reviewedSystem(snapshot.Build.GOOS), reviewedSystem(snapshot.Build.GOARCH)}
	shape := supportBundleConfigShape{
		OutputVersion: supportBundleOutputVersion, Status: reviewedSystem(snapshot.ConfigStatus), SchemaVersion: snapshot.Config.SchemaVersion,
		HelperEnabled: snapshot.Config.Helper.Enabled, DirectTransferEnabled: snapshot.Config.DirectTransfer.Enabled,
		EditorConfigured: snapshot.Config.External.Editor != nil, OpenerConfigured: snapshot.Config.External.Opener != nil,
		PreviewerCount: len(snapshot.Config.External.Previewers), KeymapOverrideCount: len(snapshot.Config.Keymap.Bindings),
	}
	doctorReport := reviewedDoctorReport(snapshot.Doctor)
	reviewedDiagnostics := make([]reviewedDiagnostic, 0, len(snapshot.Diagnostics))
	for _, record := range snapshot.Diagnostics {
		reviewedDiagnostics = append(reviewedDiagnostics, reviewedDiagnostic{
			Sequence: record.Sequence, Time: reviewedOptionalSystem(record.Time.UTC().Format(time.RFC3339Nano)),
			Level: reviewedOptionalSystem(record.Level), Component: reviewedOptionalSystem(record.Component),
			Event: reviewedOptionalSystem(record.Event), ErrorCode: reviewedOptionalSystem(string(record.ErrorCode)),
		})
	}
	reviewedJobs := make([]reviewedJob, 0, len(snapshot.Jobs))
	for _, view := range snapshot.Jobs {
		reviewedJobs = append(reviewedJobs, reviewedJob{
			State: reviewedOptionalSystem(string(view.Snapshot.State)), Kind: reviewedOptionalSystem(string(view.Kind)),
			Route: reviewedOptionalSystem(string(view.Route)), PlannedRoute: reviewedOptionalSystem(string(view.PlannedRoute)),
			DowngradedFrom: reviewedOptionalSystem(string(view.DowngradedFrom)), RouteReason: reviewedOptionalSystem(string(view.RouteReason)),
			Phase: reviewedOptionalSystem(string(view.Phase)), Bytes: view.Bytes, BytesTotal: view.BytesTotal, Items: view.Items,
			PauseRequested: view.Snapshot.PauseRequested, CancelRequested: view.Snapshot.CancelRequested,
		})
	}
	features := make([]reviewedCapability, 0, len(snapshot.DaemonInfo.Features))
	for _, feature := range snapshot.DaemonInfo.Features {
		features = append(features, reviewedCapability{Name: reviewedSystem(feature.Name), Version: feature.Version})
	}
	events := make([]string, 0, len(snapshot.DaemonInfo.EventTypes))
	for _, event := range snapshot.DaemonInfo.EventTypes {
		events = append(events, reviewedSystem(event))
	}

	type diagnosticsDocument struct {
		OutputVersion int                  `json:"output_version"`
		Records       []reviewedDiagnostic `json:"records"`
	}
	type jobsDocument struct {
		OutputVersion int           `json:"output_version"`
		Jobs          []reviewedJob `json:"jobs"`
	}
	type databaseDocument struct {
		OutputVersion int    `json:"output_version"`
		Status        string `json:"status"`
		Detail        string `json:"detail"`
	}
	type capabilitiesDocument struct {
		OutputVersion      int                  `json:"output_version"`
		Status             string               `json:"status"`
		DaemonVersion      string               `json:"daemon_version"`
		Protocol           ipc.ProtocolVersion  `json:"protocol"`
		Features           []reviewedCapability `json:"features"`
		EventTypes         []string             `json:"event_types"`
		HelperDistribution string               `json:"helper_distribution"`
		Level2Transfer     string               `json:"level2_transfer"`
	}
	documents := []struct {
		name        string
		sensitivity redaction.Sensitivity
		value       any
	}{
		{"version.json", redaction.SystemMetadata, version},
		{"platform.json", redaction.SystemMetadata, platformSnapshot},
		{"config-shape.json", redaction.Pseudonymous, shape},
		{"doctor.json", redaction.SystemMetadata, doctorReport},
		{"diagnostics.json", redaction.Pseudonymous, diagnosticsDocument{supportBundleOutputVersion, reviewedDiagnostics}},
		{"jobs.json", redaction.Pseudonymous, jobsDocument{supportBundleOutputVersion, reviewedJobs}},
		{"database-health.json", redaction.SystemMetadata, databaseDocument{supportBundleOutputVersion, reviewedSystem(snapshot.DatabaseStatus), reviewedSystem(snapshot.DatabaseDetail)}},
		{"capabilities.json", redaction.SystemMetadata, capabilitiesDocument{supportBundleOutputVersion, reviewedSystem(snapshot.DaemonStatus), reviewedSystem(snapshot.DaemonInfo.DaemonVersion), snapshot.DaemonInfo.Protocol, features, events, "closed", "closed"}},
	}
	sources := make([]supportbundle.Source, 0, len(documents))
	for _, document := range documents {
		encoded, err := json.Marshal(document.value)
		if err != nil {
			return nil, fmt.Errorf("compose support bundle: encode %s: %w", document.name, err)
		}
		sources = append(sources, supportbundle.Source{Name: document.name, Sensitivity: document.sensitivity, Bytes: encoded})
	}
	return sources, nil
}

func reviewedDoctorReport(report doctor.Report) doctor.Report {
	reviewed := doctor.Report{OutputVersion: report.OutputVersion, Results: make([]doctor.Result, 0, len(report.Results))}
	for _, result := range report.Results {
		reviewed.Results = append(reviewed.Results, doctor.Result{
			Code: doctor.Code(reviewedSystem(string(result.Code))), Status: doctor.Status(reviewedSystem(string(result.Status))),
			Severity: doctor.Severity(reviewedSystem(string(result.Severity))), DetailCode: reviewedSystem(result.DetailCode),
			Remediation: reviewedSystem(result.Remediation),
		})
	}
	return reviewed
}

func reviewedSystem(value string) string {
	if redaction.ReviewedExportString(redaction.SystemMetadata, value) {
		return value
	}
	if exported, include := redaction.ExportString(redaction.SystemMetadata, value); include {
		return exported
	}
	return redaction.Placeholder
}

func reviewedOptionalSystem(value string) string {
	if value == "" {
		return ""
	}
	return reviewedSystem(value)
}
