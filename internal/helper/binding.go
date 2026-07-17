package helper

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transport/openssh"
)

const (
	MaxProbeStdoutBytes      = 4096
	MaxHelperRemotePathBytes = 1000
	maxHelperCommandBytes    = 1536
	maxHelperSuffixBytes     = 320
	helperTempBasenameBytes  = 44
)

type Observation struct {
	UID    uint32
	Home   string
	Target Target
}

func ParseBindingProbe(raw []byte) (Observation, error) {
	if len(raw) == 0 || len(raw) > MaxProbeStdoutBytes {
		return Observation{}, errors.New("parse helper binding probe: output length is invalid")
	}
	fields := strings.Split(string(raw), "\x00")
	if len(fields) != 6 || fields[5] != "" || fields[0] != "amsftp-helper-bind-v1" {
		return Observation{}, errors.New("parse helper binding probe: framing or header is invalid")
	}
	uid, err := parseCanonicalUint(fields[1], 10, 2147483647)
	if err != nil {
		return Observation{}, errors.New("parse helper binding probe: uid is invalid")
	}
	if err := ValidateSafeHome(fields[2]); err != nil {
		return Observation{}, fmt.Errorf("parse helper binding probe: home is invalid: %w", err)
	}
	target, err := normalizeProbeTarget(fields[3], fields[4])
	if err != nil {
		return Observation{}, err
	}
	return Observation{UID: uint32(uid), Home: fields[2], Target: target}, nil // #nosec G115 -- parser caps UID at 2^31-1 above.
}

func normalizeProbeTarget(osName, architecture string) (Target, error) {
	var normalizedOS string
	switch osName {
	case "Darwin":
		normalizedOS = "darwin"
	case "Linux":
		normalizedOS = "linux"
	default:
		return Target{}, errors.New("parse helper binding probe: operating system is unsupported")
	}
	var normalizedArch string
	switch architecture {
	case "x86_64":
		normalizedArch = "amd64"
	case "arm64", "aarch64":
		normalizedArch = "arm64"
	default:
		return Target{}, errors.New("parse helper binding probe: architecture is unsupported")
	}
	return Target{OS: normalizedOS, Arch: normalizedArch}, nil
}

func ValidateSafeHome(home string) error {
	if home == "" || home[0] != '/' || home == "/" || len(home) > MaxHelperRemotePathBytes {
		return errors.New("safe helper home: absolute non-root path is required")
	}
	if path.Clean(home) != home {
		return errors.New("safe helper home: path is not canonical")
	}
	for _, component := range strings.Split(home[1:], "/") {
		if len(component) == 0 || len(component) > 255 || component == "." || component == ".." {
			return errors.New("safe helper home: path component length or value is invalid")
		}
		for index := range component {
			value := component[index]
			if !isSafeHomeByte(value) {
				return errors.New("safe helper home: path component contains an unsafe byte")
			}
		}
	}
	return nil
}

func isSafeHomeByte(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z' || value >= '0' && value <= '9' || value == '.' || value == '_' || value == '-'
}

type InstallPlan struct {
	Directories []string
	FinalPath   string
}

func DeriveInstallPlan(home string, manifest Manifest) (InstallPlan, error) {
	if err := ValidateSafeHome(home); err != nil {
		return InstallPlan{}, err
	}
	if manifest.ProtocolMajor == 0 || manifest.OS == "" || manifest.Arch == "" || len(manifest.SHA256) != 64 {
		return InstallPlan{}, errors.New("derive helper install plan: parsed manifest is required")
	}
	components := []string{
		".local",
		"lib",
		"amsftp",
		"helpers",
		"p" + strconv.FormatUint(uint64(manifest.ProtocolMajor), 10),
		manifest.Version.String(),
		manifest.OS + "-" + manifest.Arch + "-" + manifest.SHA256,
	}
	suffix := "/" + strings.Join(append(append([]string(nil), components...), "amsftp"), "/")
	if len(suffix) > maxHelperSuffixBytes {
		return InstallPlan{}, errors.New("derive helper install plan: suffix exceeds byte limit")
	}
	directories := make([]string, 0, len(components))
	current := home
	for _, component := range components {
		if len(component) == 0 || len(component) > 255 {
			return InstallPlan{}, errors.New("derive helper install plan: component exceeds byte limit")
		}
		current += "/" + component
		if len(current) > MaxHelperRemotePathBytes {
			return InstallPlan{}, errors.New("derive helper install plan: directory exceeds byte limit")
		}
		directories = append(directories, current)
	}
	finalPath := current + "/amsftp"
	if len(finalPath) > MaxHelperRemotePathBytes {
		return InstallPlan{}, errors.New("derive helper install plan: final path exceeds byte limit")
	}
	if len(current)+1+helperTempBasenameBytes > MaxHelperRemotePathBytes {
		return InstallPlan{}, errors.New("derive helper install plan: temporary path exceeds byte limit")
	}
	return InstallPlan{Directories: directories, FinalPath: finalPath}, nil
}

func HelperSSHArguments(sshPath, hostAlias string, plan InstallPlan) ([]string, error) {
	if err := validateAbsoluteExecutable(sshPath); err != nil {
		return nil, err
	}
	if err := openssh.ValidateHostAlias(hostAlias); err != nil {
		return nil, fmt.Errorf("build helper SSH arguments: %w", err)
	}
	if !isSafeHelperExecutablePath(plan.FinalPath) {
		return nil, errors.New("build helper SSH arguments: final path is invalid")
	}
	command := "exec " + plan.FinalPath + " helper serve"
	if len(command) > maxHelperCommandBytes || !isPrintableASCII(command) {
		return nil, errors.New("build helper SSH arguments: remote command is invalid")
	}
	return []string{
		sshPath,
		"-T",
		"-oEscapeChar=none",
		"-oForwardAgent=no",
		"-oForwardX11=no",
		"-oPermitLocalCommand=no",
		"-oClearAllForwardings=yes",
		"-oRemoteCommand=none",
		"-oStdinNull=no",
		"-oForkAfterAuthentication=no",
		"-oTunnel=no",
		"-oGSSAPIDelegateCredentials=no",
		"-oControlMaster=no",
		"-oControlPath=none",
		"-oControlPersist=no",
		"-oSessionType=default",
		hostAlias,
		command,
	}, nil
}

func isSafeHelperExecutablePath(value string) bool {
	if value == "" || value == "/" || len(value) > MaxHelperRemotePathBytes || !path.IsAbs(value) || path.Clean(value) != value {
		return false
	}
	components := strings.Split(value[1:], "/")
	for _, component := range components {
		if component == "" || len(component) > 255 || !allBytes(component, isSafeHomeByte) {
			return false
		}
	}
	if len(components) < 9 {
		return false
	}
	suffix := components[len(components)-8:]
	if suffix[0] != ".local" || suffix[1] != "lib" || suffix[2] != "amsftp" || suffix[3] != "helpers" || suffix[7] != "amsftp" || !strings.HasPrefix(suffix[4], "p") {
		return false
	}
	if _, err := parseCanonicalUint(strings.TrimPrefix(suffix[4], "p"), 5, 65535); err != nil {
		return false
	}
	if _, err := parseReleaseVersion(suffix[5]); err != nil {
		return false
	}
	target := strings.Split(suffix[6], "-")
	return len(target) == 3 && (target[0] == "darwin" || target[0] == "linux") &&
		(target[1] == "amd64" || target[1] == "arm64") && len(target[2]) == 64 && isLowerHex(target[2])
}

func validateAbsoluteExecutable(value string) error {
	if value == "" || value[0] != '/' || path.Clean(value) != value || !isPrintableASCII(value) {
		return errors.New("build helper SSH arguments: SSH executable path is invalid")
	}
	return nil
}

func isPrintableASCII(value string) bool {
	for index := range value {
		if value[index] < 0x20 || value[index] > 0x7e {
			return false
		}
	}
	return true
}

func ValidateArtifact(ctx context.Context, reader io.Reader, manifest Manifest) error {
	if ctx == nil || reader == nil || manifest.Size == 0 || manifest.Size > MaxHelperArtifactBytes || len(manifest.SHA256) != 64 {
		return errors.New("validate helper artifact: context, reader, or manifest is invalid")
	}
	hash := sha256.New()
	limited := &io.LimitedReader{R: reader, N: int64(manifest.Size) + 1}
	buffer := make([]byte, 32*1024)
	var total uint64
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("validate helper artifact: %w", err)
		}
		read, err := limited.Read(buffer)
		if read > 0 {
			total += uint64(read)
			_, _ = hash.Write(buffer[:read])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("validate helper artifact: read: %w", err)
		}
		if read == 0 {
			return errors.New("validate helper artifact: reader made no progress")
		}
	}
	if total != manifest.Size {
		return fmt.Errorf("validate helper artifact: size %d does not match manifest %d", total, manifest.Size)
	}
	if hex.EncodeToString(hash.Sum(nil)) != manifest.SHA256 {
		return errors.New("validate helper artifact: SHA-256 does not match manifest")
	}
	return nil
}
