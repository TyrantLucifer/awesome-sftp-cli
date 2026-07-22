package selfupdate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	Formula                 = "TyrantLucifer/tap/amsftp"
	defaultLatestURL        = "https://github.com/TyrantLucifer/awesome-sftp-cli/releases/latest"
	defaultReleaseRoot      = "https://github.com/TyrantLucifer/awesome-sftp-cli/releases/download"
	maximumMetadataBytes    = 1 << 20
	maximumCommandOutput    = 1 << 20
	maximumReleaseRedirects = 8
	defaultHTTPTimeout      = 30 * time.Second
)

type Channel string

const (
	ChannelHomebrew   Channel = "homebrew"
	ChannelStandalone Channel = "standalone"
)

type Installation struct {
	Channel          Channel
	Prefix           string
	BrewExecutable   string
	LaunchExecutable string
}

type Plan struct {
	Installation
	CurrentVersion string
	TargetVersion  string
	Available      bool
}

type Command struct {
	Path string
	Args []string
	Env  []string
}

type Runner func(context.Context, Command) ([]byte, error)

type Config struct {
	GOOS           string
	GOARCH         string
	Executable     string
	CurrentVersion string
	HTTPClient     *http.Client
	LatestURL      string
	ReleaseRoot    string
	AllowHTTP      bool
	Run            Runner
}

type Manager struct {
	currentVersion releaseVersion
	currentRaw     string
	installation   Installation
	client         *http.Client
	latestURL      string
	latestHost     string
	latestTagPath  string
	releaseRoot    string
	run            Runner
}

type uncertainApplyError struct {
	message string
}

func (err *uncertainApplyError) Error() string { return err.message }

func ApplyEffectUnknown(err error) bool {
	var uncertain *uncertainApplyError
	return errors.As(err, &uncertain)
}

func New(config Config) (*Manager, error) {
	if config.GOOS != "darwin" && config.GOOS != "linux" {
		return nil, errors.New("self-update supports only macOS and Linux")
	}
	if config.GOARCH != "amd64" && config.GOARCH != "arm64" {
		return nil, errors.New("self-update supports only amd64 and arm64")
	}
	current, err := parseReleaseVersion(config.CurrentVersion)
	if err != nil {
		return nil, errors.New("self-update requires an installed release build")
	}
	installation, err := DetectInstallation(config.Executable)
	if err != nil {
		return nil, err
	}
	latestURL := config.LatestURL
	if latestURL == "" {
		latestURL = defaultLatestURL
	}
	releaseRoot := config.ReleaseRoot
	if releaseRoot == "" {
		releaseRoot = defaultReleaseRoot
	}
	var latestParsed *url.URL
	for index, rawURL := range []string{latestURL, releaseRoot} {
		parsed, parseErr := url.Parse(rawURL)
		schemeAllowed := parseErr == nil && (parsed.Scheme == "https" || config.AllowHTTP && parsed.Scheme == "http")
		if parseErr != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || !schemeAllowed {
			return nil, errors.New("self-update release source is invalid")
		}
		if index == 0 {
			latestParsed = parsed
		}
	}
	if latestParsed == nil || !strings.HasSuffix(latestParsed.Path, "/releases/latest") {
		return nil, errors.New("self-update latest-release source is invalid")
	}
	client := http.DefaultClient
	if config.HTTPClient != nil {
		client = config.HTTPClient
	}
	clientCopy := *client
	if clientCopy.Timeout == 0 {
		clientCopy.Timeout = defaultHTTPTimeout
	}
	priorRedirect := clientCopy.CheckRedirect
	clientCopy.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= maximumReleaseRedirects {
			return errors.New("too many release redirects")
		}
		schemeAllowed := request.URL.Scheme == "https" || config.AllowHTTP && request.URL.Scheme == "http"
		if !schemeAllowed {
			return errors.New("release redirect is not HTTPS")
		}
		if priorRedirect != nil {
			return priorRedirect(request, via)
		}
		return nil
	}
	runner := config.Run
	if runner == nil {
		runner = runCommand
	}
	return &Manager{
		currentVersion: current, currentRaw: config.CurrentVersion,
		installation: installation, client: &clientCopy, latestURL: latestURL,
		latestHost: latestParsed.Host, latestTagPath: strings.TrimSuffix(latestParsed.Path, "/releases/latest") + "/releases/tag/v",
		releaseRoot: strings.TrimSuffix(releaseRoot, "/"), run: runner,
	}, nil
}

func DetectInstallation(executable string) (Installation, error) {
	if executable == "" || !filepath.IsAbs(executable) || filepath.Clean(executable) != executable {
		return Installation{}, errors.New("detect self-update installation: executable path is invalid")
	}
	components := splitAbsolutePath(executable)
	if len(components) >= 5 {
		last := len(components) - 1
		if components[last] == "amsftp" && components[last-1] == "bin" && components[last-3] == "amsftp" && components[last-4] == "Cellar" {
			prefixComponents := components[:last-4]
			prefix := string(filepath.Separator) + filepath.Join(prefixComponents...)
			if prefix == string(filepath.Separator) {
				return Installation{}, errors.New("detect self-update installation: Homebrew prefix is unsafe")
			}
			return Installation{
				Channel: ChannelHomebrew, Prefix: prefix,
				BrewExecutable: filepath.Join(prefix, "bin", "brew"), LaunchExecutable: filepath.Join(prefix, "bin", "amsftp"),
			}, nil
		}
	}
	if len(components) >= 3 && components[len(components)-2] == "bin" && components[len(components)-1] == "amsftp" {
		prefix := string(filepath.Separator) + filepath.Join(components[:len(components)-2]...)
		if prefix != string(filepath.Separator) {
			return Installation{Channel: ChannelStandalone, Prefix: prefix, LaunchExecutable: executable}, nil
		}
	}
	return Installation{}, errors.New("detect self-update installation: unsupported executable layout")
}

func splitAbsolutePath(value string) []string {
	trimmed := strings.TrimPrefix(filepath.ToSlash(value), "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

func (manager *Manager) Plan(ctx context.Context) (Plan, error) {
	var target string
	var err error
	switch manager.installation.Channel {
	case ChannelHomebrew:
		target, err = manager.homebrewTarget(ctx)
	case ChannelStandalone:
		target, err = manager.standaloneTarget(ctx)
	default:
		err = errors.New("self-update installation channel is unsupported")
	}
	if err != nil {
		return Plan{}, err
	}
	targetVersion, err := parseReleaseVersion(target)
	if err != nil {
		return Plan{}, errors.New("self-update target version is invalid")
	}
	comparison := targetVersion.compare(manager.currentVersion)
	if comparison < 0 {
		return Plan{}, errors.New("self-update refuses a release-channel downgrade")
	}
	return Plan{
		Installation: manager.installation, CurrentVersion: manager.currentRaw,
		TargetVersion: target, Available: comparison > 0,
	}, nil
}

func (manager *Manager) homebrewTarget(ctx context.Context) (string, error) {
	if _, err := manager.run(ctx, Command{Path: manager.installation.BrewExecutable, Args: []string{"update"}}); err != nil {
		return "", errors.New("refresh Homebrew metadata")
	}
	raw, err := manager.run(ctx, Command{Path: manager.installation.BrewExecutable, Args: []string{"info", "--json=v2", Formula}})
	if err != nil {
		return "", errors.New("read Homebrew formula metadata")
	}
	if len(raw) > maximumMetadataBytes {
		return "", errors.New("homebrew formula metadata exceeds the size limit")
	}
	var document struct {
		Formulae []struct {
			Name     string `json:"name"`
			Versions struct {
				Stable string `json:"stable"`
			} `json:"versions"`
		} `json:"formulae"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&document); err != nil || len(document.Formulae) != 1 || document.Formulae[0].Name != "amsftp" {
		return "", errors.New("homebrew formula metadata is invalid")
	}
	if _, err := parseReleaseVersion(document.Formulae[0].Versions.Stable); err != nil {
		return "", errors.New("homebrew formula release version is invalid")
	}
	return document.Formulae[0].Versions.Stable, nil
}

func (manager *Manager) standaloneTarget(ctx context.Context) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, manager.latestURL, nil)
	if err != nil {
		return "", errors.New("create latest-release request")
	}
	request.Header.Set("User-Agent", "amsftp-self-update")
	response, err := manager.client.Do(request)
	if err != nil {
		return "", errors.New("resolve latest release")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maximumMetadataBytes))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", errors.New("latest release returned an unsuccessful status")
	}
	if response.Request.URL.Host != manager.latestHost || response.Request.URL.RawQuery != "" || !strings.HasPrefix(response.Request.URL.Path, manager.latestTagPath) {
		return "", errors.New("latest release did not resolve to a version tag")
	}
	version := strings.TrimPrefix(response.Request.URL.Path, manager.latestTagPath)
	if strings.Contains(version, "/") {
		return "", errors.New("latest release tag is invalid")
	}
	if _, err := parseReleaseVersion(version); err != nil {
		return "", errors.New("latest release tag is invalid")
	}
	return version, nil
}

func (manager *Manager) Apply(ctx context.Context, plan Plan) error {
	if err := validatePlan(plan, manager.installation, manager.currentRaw); err != nil {
		return err
	}
	if !plan.Available {
		return nil
	}
	switch plan.Channel {
	case ChannelHomebrew:
		_, err := manager.run(ctx, Command{
			Path: plan.BrewExecutable, Args: []string{"upgrade", Formula}, Env: []string{"HOMEBREW_NO_AUTO_UPDATE=1"},
		})
		if err != nil {
			return &uncertainApplyError{message: "homebrew upgrade effect is uncertain"}
		}
		return nil
	case ChannelStandalone:
		return manager.applyStandalone(ctx, plan)
	default:
		return errors.New("self-update installation channel is unsupported")
	}
}

func validatePlan(plan Plan, installation Installation, current string) error {
	if plan.Installation != installation || plan.CurrentVersion != current {
		return errors.New("self-update plan does not match the current installation")
	}
	currentVersion, currentErr := parseReleaseVersion(plan.CurrentVersion)
	targetVersion, targetErr := parseReleaseVersion(plan.TargetVersion)
	if currentErr != nil || targetErr != nil || targetVersion.compare(currentVersion) < 0 || plan.Available != (targetVersion.compare(currentVersion) > 0) {
		return errors.New("self-update plan version transition is invalid")
	}
	return nil
}

func (manager *Manager) applyStandalone(ctx context.Context, plan Plan) error {
	base := manager.releaseRoot + "/v" + plan.TargetVersion
	checksums, err := manager.fetchBounded(ctx, base+"/checksums.txt")
	if err != nil {
		return errors.New("download release checksums")
	}
	installer, err := manager.fetchBounded(ctx, base+"/install.sh")
	if err != nil {
		return errors.New("download release installer")
	}
	expected, err := installerChecksum(checksums)
	if err != nil {
		return err
	}
	actual := sha256.Sum256(installer)
	if hex.EncodeToString(actual[:]) != expected {
		return errors.New("release installer checksum mismatch")
	}
	temporaryDirectory, err := os.MkdirTemp("", "amsftp-upgrade-*")
	if err != nil {
		return errors.New("create self-update temporary directory")
	}
	defer os.RemoveAll(temporaryDirectory)
	installerPath := filepath.Join(temporaryDirectory, "install.sh")
	// #nosec G306 -- the checksum-verified installer must be executable and remains owner-only in a 0700 temporary directory.
	if err := os.WriteFile(installerPath, installer, 0o700); err != nil {
		return errors.New("stage release installer")
	}
	_, err = manager.run(ctx, Command{
		Path: "/bin/sh",
		Args: []string{installerPath, "--version", plan.TargetVersion, "--prefix", plan.Prefix, "--no-start-daemon"},
		Env:  []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin"},
	})
	if err != nil {
		return &uncertainApplyError{message: "release installer effect is uncertain"}
	}
	return nil
}

func (manager *Manager) fetchBounded(ctx context.Context, rawURL string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "amsftp-self-update")
	response, err := manager.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, errors.New("release asset returned an unsuccessful status")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maximumMetadataBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maximumMetadataBytes {
		return nil, errors.New("release asset exceeds the size limit")
	}
	return raw, nil
}

func installerChecksum(raw []byte) (string, error) {
	if len(raw) == 0 || len(raw) > maximumMetadataBytes {
		return "", errors.New("release checksum manifest is invalid")
	}
	found := ""
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != "install.sh" {
			continue
		}
		if found != "" || len(fields[0]) != sha256.Size*2 {
			return "", errors.New("release checksum manifest has no unique installer checksum")
		}
		if _, err := hex.DecodeString(fields[0]); err != nil || strings.ToLower(fields[0]) != fields[0] {
			return "", errors.New("release installer checksum is invalid")
		}
		found = fields[0]
	}
	if found == "" {
		return "", errors.New("release checksum manifest has no unique installer checksum")
	}
	return found, nil
}

func (manager *Manager) Verify(ctx context.Context, plan Plan) error {
	if err := validatePlan(plan, manager.installation, manager.currentRaw); err != nil {
		return err
	}
	raw, err := manager.run(ctx, Command{Path: plan.LaunchExecutable, Args: []string{"--version"}})
	if err != nil {
		return errors.New("execute upgraded binary")
	}
	if !strings.HasPrefix(string(raw), plan.TargetVersion+" ") {
		return errors.New("upgraded binary reports an unexpected version")
	}
	return nil
}

type releaseVersion struct {
	major uint64
	minor uint64
	patch uint64
}

func parseReleaseVersion(value string) (releaseVersion, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 3 || len(value) > 32 {
		return releaseVersion{}, errors.New("release version is invalid")
	}
	values := [3]uint64{}
	for index, part := range parts {
		if part == "" || len(part) > 1 && part[0] == '0' {
			return releaseVersion{}, errors.New("release version is invalid")
		}
		parsed, err := strconv.ParseUint(part, 10, 31)
		if err != nil {
			return releaseVersion{}, errors.New("release version is invalid")
		}
		values[index] = parsed
	}
	return releaseVersion{major: values[0], minor: values[1], patch: values[2]}, nil
}

func (version releaseVersion) compare(other releaseVersion) int {
	left := [3]uint64{version.major, version.minor, version.patch}
	right := [3]uint64{other.major, other.minor, other.patch}
	for index := range left {
		if left[index] < right[index] {
			return -1
		}
		if left[index] > right[index] {
			return 1
		}
	}
	return 0
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (buffer *limitedBuffer) Write(value []byte) (int, error) {
	if len(value) > buffer.limit-buffer.Len() {
		return 0, errors.New("command output exceeds the size limit")
	}
	return buffer.Buffer.Write(value)
}

func runCommand(ctx context.Context, command Command) ([]byte, error) {
	if command.Path == "" || !filepath.IsAbs(command.Path) {
		return nil, errors.New("self-update command path is invalid")
	}
	// #nosec G204 -- Manager constructs fixed argv from strict versions and validated installation paths; no shell command string is accepted.
	process := exec.CommandContext(ctx, command.Path, command.Args...)
	process.Env = mergeEnvironment(os.Environ(), command.Env)
	output := &limitedBuffer{limit: maximumCommandOutput}
	process.Stdout = output
	process.Stderr = output
	if err := process.Run(); err != nil {
		return nil, fmt.Errorf("run self-update command: %w", err)
	}
	return append([]byte(nil), output.Bytes()...), nil
}

func mergeEnvironment(base, overrides []string) []string {
	result := append([]string(nil), base...)
	for _, override := range overrides {
		key, _, ok := strings.Cut(override, "=")
		if !ok || key == "" {
			continue
		}
		filtered := result[:0]
		prefix := key + "="
		for _, value := range result {
			if !strings.HasPrefix(value, prefix) {
				filtered = append(filtered, value)
			}
		}
		result = append(filtered, override)
	}
	return result
}
