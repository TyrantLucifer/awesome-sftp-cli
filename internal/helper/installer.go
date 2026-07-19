package helper

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
)

var (
	ErrConsentDeclined     = errors.New("helper install consent declined")
	ErrPlanChanged         = errors.New("helper install plan changed; new consent is required")
	ErrRemoteNotExist      = errors.New("helper remote path does not exist")
	ErrRemoteAlreadyExists = errors.New("helper remote path already exists")
)

type RemoteKind string

const (
	RemoteRegular   RemoteKind = "regular"
	RemoteDirectory RemoteKind = "directory"
	RemoteSymlink   RemoteKind = "symlink"
	RemoteOther     RemoteKind = "other"
)

type RemoteAttrs struct {
	Kind RemoteKind
	UID  uint32
	Mode uint32
	Size uint64
}

type RemoteWriteHandle interface {
	Chmod(context.Context, uint32) error
	Stat(context.Context) (RemoteAttrs, error)
	Write(context.Context, []byte) (int, error)
	Close(context.Context) error
}

// InstallRemote is deliberately narrower than a general command or filesystem
// API. Implementations must provide lstat semantics, exclusive creation, and a
// standard no-replace publication primitive.
type InstallRemote interface {
	Probe(context.Context) (Observation, error)
	RealPath(context.Context, string) (string, error)
	Lstat(context.Context, string) (RemoteAttrs, error)
	Mkdir(context.Context, string, uint32) error
	OpenExclusive(context.Context, string) (RemoteWriteHandle, error)
	OpenRead(context.Context, string) (io.ReadCloser, error)
	PublishNoReplace(context.Context, string, string) error
	RemoveExact(context.Context, string) error
}

type ArtifactOpener func(context.Context) (io.ReadCloser, error)

func ReopenBytes(value []byte) ArtifactOpener {
	frozen := append([]byte(nil), value...)
	return func(ctx context.Context) (io.ReadCloser, error) {
		if ctx == nil {
			return nil, errors.New("open helper artifact: context is required")
		}
		return io.NopCloser(strings.NewReader(string(frozen))), nil
	}
}

type PreliminaryConsent struct {
	EndpointID       domain.EndpointID
	EndpointLabel    string
	DeclaredTarget   Target
	Version          Version
	ArtifactSource   string
	ObservedUID      *uint32
	ActualHome       string
	MappingStatement string
	ProbeSideEffects string
}

type PreliminaryApproval struct {
	Approved                bool
	SharedSessionStableHome bool
}

type FinalConsent struct {
	EndpointID        domain.EndpointID
	Observation       Observation
	CreateDirectories []string
	FinalPath         string
	TempBasename      string
	FileMode          uint32
	Version           Version
	KeyID             string
	ArtifactSource    string
	Size              uint64
	SHA256            string
	Floor             Version
	HighWaterDecision HighWaterDecision
	BoundaryStatement string
	PlanDigest        string
}

type FinalApproval struct {
	Approved   bool
	PlanDigest string
}

type InstallConsent interface {
	ApprovePreliminary(context.Context, PreliminaryConsent) (PreliminaryApproval, error)
	ApproveFinal(context.Context, FinalConsent) (FinalApproval, error)
}

type InstallRequest struct {
	EndpointID     domain.EndpointID
	EndpointLabel  string
	RawManifest    []byte
	RawSignature   []byte
	Verifier       Verifier
	Policy         Policy
	HighWater      *HighWater
	State          *StateStore
	Consent        InstallConsent
	Remote         InstallRemote
	Artifact       ArtifactOpener
	ArtifactSource string
	Repair         bool
	Handshake      func(context.Context, string, Manifest) error
}

type InstallResult struct {
	FinalPath string
	Decision  HighWaterDecision
	Enabled   bool
}

type Installer struct {
	// Tests in this package inject deterministic bytes. Production callers have
	// no setter and therefore always use crypto/rand.Reader.
	entropy io.Reader
}

type installSnapshot struct {
	Observation       Observation
	RealHome          string
	Plan              InstallPlan
	CreateDirectories []string
	FinalExists       bool
	checks            []snapshotCheck
}

type snapshotCheck struct {
	Path   string
	Exists bool
	Attrs  RemoteAttrs
}

func (i Installer) Install(ctx context.Context, request InstallRequest) (InstallResult, error) {
	if ctx == nil || request.EndpointID == "" || request.Consent == nil || request.Remote == nil || request.Artifact == nil || request.HighWater == nil && request.State == nil || request.HighWater != nil && request.State != nil || request.Handshake == nil {
		return InstallResult{}, errors.New("install helper: request is incomplete")
	}
	rawManifest := append([]byte(nil), request.RawManifest...)
	rawSignature := append([]byte(nil), request.RawSignature...)
	manifest, err := verifyCurrentPolicy(request.Verifier, request.Policy, rawManifest, rawSignature)
	if err != nil {
		return InstallResult{}, err
	}
	if request.State != nil {
		if err := request.State.StageMetadata(request.EndpointID, rawManifest, rawSignature); err != nil {
			return InstallResult{}, fmt.Errorf("install helper: persist exact signed metadata: %w", err)
		}
	}
	preliminary := PreliminaryConsent{
		EndpointID:       request.EndpointID,
		EndpointLabel:    request.EndpointLabel,
		DeclaredTarget:   manifest.Target(),
		Version:          manifest.Version,
		ArtifactSource:   request.ArtifactSource,
		MappingStatement: "SFTP, binding-probe, and formal exec must map to one shared-session-stable canonical home; unknown or node-local mappings remain Level 0",
		ProbeSideEffects: "the read-only probe may still cause sshd audit/lastlog, atime, or login-shell startup side effects",
	}
	preliminaryApproval, err := request.Consent.ApprovePreliminary(ctx, preliminary)
	if err != nil {
		return InstallResult{}, fmt.Errorf("install helper: preliminary consent: %w", err)
	}
	if !preliminaryApproval.Approved || !preliminaryApproval.SharedSessionStableHome {
		return InstallResult{}, ErrConsentDeclined
	}

	first, err := inspectInstallSnapshot(ctx, request.Remote, manifest)
	if err != nil {
		return InstallResult{}, err
	}
	decision, err := checkInstallHighWater(request, manifest)
	if err != nil {
		return InstallResult{}, err
	}
	if err := validateArtifactFromOpener(ctx, request.Artifact, manifest); err != nil {
		return InstallResult{}, err
	}
	floor := request.Policy.floors[manifest.FloorKey()]
	digest := snapshotDigest(manifest, first, decision)
	finalView := FinalConsent{
		EndpointID:        request.EndpointID,
		Observation:       first.Observation,
		CreateDirectories: append([]string(nil), first.CreateDirectories...),
		FinalPath:         first.Plan.FinalPath,
		TempBasename:      ".amsftp.tmp-<32-lowerhex>",
		FileMode:          0700,
		Version:           manifest.Version,
		KeyID:             manifest.KeyID,
		ArtifactSource:    request.ArtifactSource,
		Size:              manifest.Size,
		SHA256:            manifest.SHA256,
		Floor:             floor,
		HighWaterDecision: decision,
		BoundaryStatement: "path, uid, and mode checks are compatibility and POSIX-DAC preflights; they do not prove node, mount, object, ACL, same-euid, root, admin, or server isolation",
		PlanDigest:        digest,
	}
	approval, err := request.Consent.ApproveFinal(ctx, finalView)
	if err != nil {
		return InstallResult{}, fmt.Errorf("install helper: final consent: %w", err)
	}
	if !approval.Approved {
		return InstallResult{}, ErrConsentDeclined
	}
	if approval.PlanDigest != digest {
		return InstallResult{}, ErrPlanChanged
	}

	if _, err := verifyCurrentPolicy(request.Verifier, request.Policy, rawManifest, rawSignature); err != nil {
		return InstallResult{}, err
	}
	second, err := inspectInstallSnapshot(ctx, request.Remote, manifest)
	if err != nil {
		return InstallResult{}, fmt.Errorf("%w: %w", ErrPlanChanged, err)
	}
	secondDecision, err := checkInstallHighWater(request, manifest)
	if err != nil {
		return InstallResult{}, err
	}
	if snapshotDigest(manifest, second, secondDecision) != digest {
		return InstallResult{}, ErrPlanChanged
	}
	if err := validateArtifactFromOpener(ctx, request.Artifact, manifest); err != nil {
		return InstallResult{}, err
	}
	if second.FinalExists {
		if err := request.Handshake(ctx, second.Plan.FinalPath, manifest); err != nil {
			return InstallResult{}, fmt.Errorf("install helper: existing artifact handshake: %w", err)
		}
		if err := commitInstalledState(request, manifest, second.Plan.FinalPath, rawSignature); err != nil {
			return InstallResult{}, err
		}
		return InstallResult{FinalPath: second.Plan.FinalPath, Decision: secondDecision, Enabled: true}, nil
	}

	for _, directory := range second.CreateDirectories {
		if err := request.Remote.Mkdir(ctx, directory, 0700); err != nil {
			return InstallResult{}, fmt.Errorf("install helper: create %q: %w", directory, err)
		}
		attrs, err := request.Remote.Lstat(ctx, directory)
		if err != nil || !exactOwned(attrs, second.Observation.UID, RemoteDirectory, 0700, 0) {
			return InstallResult{}, fmt.Errorf("install helper: created directory %q failed exact attribute verification", directory)
		}
	}

	entropy := i.entropy
	if entropy == nil {
		entropy = rand.Reader
	}
	tempPath, err := deriveTempPath(second.Plan.FinalPath, entropy)
	if err != nil {
		return InstallResult{}, err
	}
	handle, err := request.Remote.OpenExclusive(ctx, tempPath)
	if err != nil {
		return InstallResult{}, fmt.Errorf("install helper: exclusive temp open: %w", err)
	}
	tempPresent := true
	defer func() {
		_ = handle.Close(context.Background())
		if tempPresent {
			cleanupExactTemp(context.Background(), request.Remote, tempPath, second.Observation.UID)
		}
	}()
	if err := handle.Chmod(ctx, 0600); err != nil {
		return InstallResult{}, fmt.Errorf("install helper: chmod temp before content: %w", err)
	}
	if err := verifyOpenTemp(ctx, request.Remote, handle, tempPath, second.Observation.UID, 0, 0600); err != nil {
		return InstallResult{}, err
	}
	if err := streamArtifactToHandle(ctx, request.Artifact, handle, manifest); err != nil {
		return InstallResult{}, err
	}
	if err := verifyOpenTemp(ctx, request.Remote, handle, tempPath, second.Observation.UID, manifest.Size, 0600); err != nil {
		return InstallResult{}, err
	}
	if err := validateRemoteArtifact(ctx, request.Remote, tempPath, manifest); err != nil {
		return InstallResult{}, err
	}
	if err := handle.Chmod(ctx, 0700); err != nil {
		return InstallResult{}, fmt.Errorf("install helper: chmod verified temp executable: %w", err)
	}
	if err := verifyOpenTemp(ctx, request.Remote, handle, tempPath, second.Observation.UID, manifest.Size, 0700); err != nil {
		return InstallResult{}, err
	}
	if err := handle.Close(ctx); err != nil {
		return InstallResult{}, fmt.Errorf("install helper: close temp: %w", err)
	}
	if err := request.Remote.PublishNoReplace(ctx, tempPath, second.Plan.FinalPath); err != nil {
		return InstallResult{}, fmt.Errorf("install helper: standard no-replace publish: %w", err)
	}
	tempPresent = false
	attrs, err := request.Remote.Lstat(ctx, second.Plan.FinalPath)
	if err != nil || !exactOwned(attrs, second.Observation.UID, RemoteRegular, 0700, manifest.Size) {
		return InstallResult{}, errors.New("install helper: published final attributes are invalid")
	}
	if err := validateRemoteArtifact(ctx, request.Remote, second.Plan.FinalPath, manifest); err != nil {
		return InstallResult{}, err
	}
	if err := request.Handshake(ctx, second.Plan.FinalPath, manifest); err != nil {
		return InstallResult{}, fmt.Errorf("install helper: handshake: %w", err)
	}
	if err := commitInstalledState(request, manifest, second.Plan.FinalPath, rawSignature); err != nil {
		return InstallResult{}, err
	}
	return InstallResult{FinalPath: second.Plan.FinalPath, Decision: secondDecision, Enabled: true}, nil
}

func checkInstallHighWater(request InstallRequest, manifest Manifest) (HighWaterDecision, error) {
	if request.State != nil {
		return request.State.Check(request.EndpointID, manifest, request.Repair)
	}
	return request.HighWater.Check(request.EndpointID, manifest, request.Repair)
}

func commitInstalledState(request InstallRequest, manifest Manifest, finalPath string, rawSignature []byte) error {
	if request.State != nil {
		return request.State.CommitEnabled(request.EndpointID, manifest, rawSignature, finalPath)
	}
	return request.HighWater.Commit(request.EndpointID, manifest)
}

func verifyCurrentPolicy(verifier Verifier, policy Policy, rawManifest, rawSignature []byte) (Manifest, error) {
	if err := verifier.Verify(rawManifest, rawSignature); err != nil {
		return Manifest{}, fmt.Errorf("install helper: current signature policy: %w", err)
	}
	manifest, err := ParseManifestV1(rawManifest)
	if err != nil {
		return Manifest{}, err
	}
	if err := policy.Check(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func inspectInstallSnapshot(ctx context.Context, remote InstallRemote, manifest Manifest) (installSnapshot, error) {
	if err := validateProbeUtilities(ctx, remote, nil); err != nil {
		return installSnapshot{}, err
	}
	observation, err := remote.Probe(ctx)
	if err != nil {
		return installSnapshot{}, fmt.Errorf("install helper: binding probe: %w", err)
	}
	return inspectInstallSnapshotFromObservation(ctx, remote, manifest, observation)
}

func inspectInstallSnapshotFromObservation(ctx context.Context, remote InstallRemote, manifest Manifest, observation Observation) (installSnapshot, error) {
	if observation.Target != manifest.Target() {
		return installSnapshot{}, errors.New("install helper: observed target does not match signed target")
	}
	if err := ValidateSafeHome(observation.Home); err != nil || observation.UID == 0 || observation.UID > 2147483647 {
		return installSnapshot{}, errors.New("install helper: binding observation is unsafe")
	}
	realHome, err := remote.RealPath(ctx, ".")
	if err != nil {
		return installSnapshot{}, fmt.Errorf("install helper: SFTP realpath: %w", err)
	}
	if realHome != observation.Home {
		return installSnapshot{}, errors.New("install helper: SFTP and exec namespaces do not share the canonical home")
	}
	snapshot := installSnapshot{Observation: observation, RealHome: realHome}
	if err := validateProbeUtilities(ctx, remote, &snapshot); err != nil {
		return installSnapshot{}, err
	}
	homeAttrs, err := remote.Lstat(ctx, observation.Home)
	if err != nil || homeAttrs.Kind != RemoteDirectory || homeAttrs.UID != observation.UID || homeAttrs.Mode&0022 != 0 || homeAttrs.Mode&^uint32(0777) != 0 {
		return installSnapshot{}, errors.New("install helper: canonical home attributes are unsafe")
	}
	snapshot.checks = append(snapshot.checks, snapshotCheck{Path: observation.Home, Exists: true, Attrs: homeAttrs})
	plan, err := DeriveInstallPlan(observation.Home, manifest)
	if err != nil {
		return installSnapshot{}, err
	}
	snapshot.Plan = plan

	// Existing ancestor/final inspection follows only after binding and utility
	// validation have succeeded in both namespaces.
	return inspectInstallPlanPaths(ctx, remote, manifest, snapshot)
}

func validateProbeUtilities(ctx context.Context, remote InstallRemote, snapshot *installSnapshot) error {
	for _, utility := range []struct {
		path string
		kind RemoteKind
	}{
		{path: "/usr", kind: RemoteDirectory},
		{path: "/usr/bin", kind: RemoteDirectory},
		{path: "/usr/bin/printf", kind: RemoteRegular},
		{path: "/usr/bin/id", kind: RemoteRegular},
		{path: "/usr/bin/uname", kind: RemoteRegular},
	} {
		attrs, err := remote.Lstat(ctx, utility.path)
		if err != nil || attrs.Kind != utility.kind || attrs.UID != 0 || attrs.Mode&0022 != 0 || attrs.Mode&^uint32(0777) != 0 || utility.kind == RemoteRegular && attrs.Mode&0111 == 0 {
			return fmt.Errorf("install helper: absolute utility %q is not root-owned and immutable to group/other", utility.path)
		}
		if snapshot != nil {
			snapshot.checks = append(snapshot.checks, snapshotCheck{Path: utility.path, Exists: true, Attrs: attrs})
		}
	}
	return nil
}

func inspectInstallPlanPaths(ctx context.Context, remote InstallRemote, manifest Manifest, snapshot installSnapshot) (installSnapshot, error) {
	plan := snapshot.Plan
	for index, directory := range plan.Directories {
		attrs, err := remote.Lstat(ctx, directory)
		if errors.Is(err, ErrRemoteNotExist) {
			snapshot.checks = append(snapshot.checks, snapshotCheck{Path: directory})
			snapshot.CreateDirectories = append(snapshot.CreateDirectories, directory)
			continue
		}
		if err != nil {
			return installSnapshot{}, fmt.Errorf("install helper: lstat ancestor %q: %w", directory, err)
		}
		valid := attrs.Kind == RemoteDirectory && attrs.Mode&^uint32(0777) == 0
		if index < 2 {
			valid = valid && (attrs.UID == 0 || attrs.UID == snapshot.Observation.UID) && attrs.Mode&0022 == 0
		} else {
			valid = valid && exactOwned(attrs, snapshot.Observation.UID, RemoteDirectory, 0700, 0)
		}
		if !valid {
			return installSnapshot{}, fmt.Errorf("install helper: ancestor %q attributes are unsafe", directory)
		}
		snapshot.checks = append(snapshot.checks, snapshotCheck{Path: directory, Exists: true, Attrs: attrs})
	}
	finalAttrs, err := remote.Lstat(ctx, plan.FinalPath)
	if errors.Is(err, ErrRemoteNotExist) {
		snapshot.checks = append(snapshot.checks, snapshotCheck{Path: plan.FinalPath})
		return snapshot, nil
	}
	if err != nil {
		return installSnapshot{}, fmt.Errorf("install helper: lstat final: %w", err)
	}
	if !exactOwned(finalAttrs, snapshot.Observation.UID, RemoteRegular, 0700, manifest.Size) {
		return installSnapshot{}, errors.New("install helper: existing final attributes do not match signed artifact")
	}
	if err := validateRemoteArtifact(ctx, remote, plan.FinalPath, manifest); err != nil {
		return installSnapshot{}, fmt.Errorf("install helper: existing final verification: %w", err)
	}
	snapshot.FinalExists = true
	snapshot.checks = append(snapshot.checks, snapshotCheck{Path: plan.FinalPath, Exists: true, Attrs: finalAttrs})
	return snapshot, nil
}

func snapshotDigest(manifest Manifest, snapshot installSnapshot, decision HighWaterDecision) string {
	hash := sha256.New()
	writeDigestField(hash, hex.EncodeToString(sha256Sum(manifest.Raw)))
	writeDigestField(hash, strconv.FormatUint(uint64(snapshot.Observation.UID), 10))
	writeDigestField(hash, snapshot.Observation.Home)
	writeDigestField(hash, snapshot.Observation.Target.OS)
	writeDigestField(hash, snapshot.Observation.Target.Arch)
	writeDigestField(hash, snapshot.RealHome)
	writeDigestField(hash, snapshot.Plan.FinalPath)
	writeDigestField(hash, string(decision))
	for _, check := range snapshot.checks {
		writeDigestField(hash, check.Path)
		writeDigestField(hash, strconv.FormatBool(check.Exists))
		writeDigestField(hash, string(check.Attrs.Kind))
		writeDigestField(hash, strconv.FormatUint(uint64(check.Attrs.UID), 10))
		writeDigestField(hash, strconv.FormatUint(uint64(check.Attrs.Mode), 8))
		writeDigestField(hash, strconv.FormatUint(check.Attrs.Size, 10))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func sha256Sum(value []byte) []byte {
	sum := sha256.Sum256(value)
	return sum[:]
}

func writeDigestField(writer io.Writer, value string) {
	_, _ = io.WriteString(writer, strconv.Itoa(len(value))+":"+value+"\n")
}

func validateArtifactFromOpener(ctx context.Context, opener ArtifactOpener, manifest Manifest) error {
	reader, err := opener(ctx)
	if err != nil {
		return fmt.Errorf("install helper: open local artifact: %w", err)
	}
	defer reader.Close()
	return ValidateArtifact(ctx, reader, manifest)
}

func deriveTempPath(finalPath string, entropy io.Reader) (string, error) {
	value := make([]byte, 16)
	if _, err := io.ReadFull(entropy, value); err != nil {
		return "", fmt.Errorf("install helper: temp entropy: %w", err)
	}
	tempPath := path.Dir(finalPath) + "/.amsftp.tmp-" + hex.EncodeToString(value)
	if len(path.Base(tempPath)) != helperTempBasenameBytes || len(tempPath) > MaxHelperRemotePathBytes {
		return "", errors.New("install helper: derived temp path exceeds bounds")
	}
	return tempPath, nil
}

func verifyOpenTemp(ctx context.Context, remote InstallRemote, handle RemoteWriteHandle, tempPath string, uid uint32, size uint64, mode uint32) error {
	handleAttrs, err := handle.Stat(ctx)
	if err != nil {
		return fmt.Errorf("install helper: temp handle stat: %w", err)
	}
	pathAttrs, err := remote.Lstat(ctx, tempPath)
	if err != nil {
		return fmt.Errorf("install helper: temp path lstat: %w", err)
	}
	if !exactOwned(handleAttrs, uid, RemoteRegular, mode, size) || !exactOwned(pathAttrs, uid, RemoteRegular, mode, size) {
		return errors.New("install helper: temp handle/path attributes are invalid")
	}
	return nil
}

func exactOwned(attrs RemoteAttrs, uid uint32, kind RemoteKind, mode uint32, size uint64) bool {
	return attrs.Kind == kind && attrs.UID == uid && attrs.Mode == mode && attrs.Size == size
}

func streamArtifactToHandle(ctx context.Context, opener ArtifactOpener, handle RemoteWriteHandle, manifest Manifest) error {
	reader, err := opener(ctx)
	if err != nil {
		return fmt.Errorf("install helper: reopen local artifact: %w", err)
	}
	defer reader.Close()
	hash := sha256.New()
	buffer := make([]byte, 32*1024)
	remaining := manifest.Size
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("install helper: upload: %w", err)
		}
		want := len(buffer)
		if uint64(want) > remaining {
			// remaining is smaller than len(buffer), so it always fits in int.
			want = int(remaining) // #nosec G115 -- bounded by the 32 KiB buffer length.
		}
		read, readErr := reader.Read(buffer[:want])
		if read > 0 {
			_, _ = hash.Write(buffer[:read])
			if err := writeAllContext(ctx, handle, buffer[:read]); err != nil {
				return err
			}
			remaining -= uint64(read)
		}
		if readErr != nil && readErr != io.EOF {
			return fmt.Errorf("install helper: read local artifact: %w", readErr)
		}
		if read == 0 {
			return errors.New("install helper: local artifact is shorter than signed size")
		}
	}
	one := []byte{0}
	read, readErr := reader.Read(one)
	if read > 0 || readErr != nil && readErr != io.EOF {
		if readErr != nil && readErr != io.EOF {
			return fmt.Errorf("install helper: expected+1 read: %w", readErr)
		}
		return errors.New("install helper: local artifact exceeds signed size")
	}
	if hex.EncodeToString(hash.Sum(nil)) != manifest.SHA256 {
		return errors.New("install helper: uploaded source SHA-256 differs from signed manifest")
	}
	return nil
}

func writeAllContext(ctx context.Context, handle RemoteWriteHandle, value []byte) error {
	for len(value) > 0 {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("install helper: upload: %w", err)
		}
		written, err := handle.Write(ctx, value)
		if written > 0 {
			value = value[written:]
		}
		if err != nil {
			return fmt.Errorf("install helper: temp write: %w", err)
		}
		if written == 0 {
			return errors.New("install helper: temp writer made no progress")
		}
	}
	return nil
}

func validateRemoteArtifact(ctx context.Context, remote InstallRemote, remotePath string, manifest Manifest) error {
	reader, err := remote.OpenRead(ctx, remotePath)
	if err != nil {
		return fmt.Errorf("install helper: remote readback open: %w", err)
	}
	defer reader.Close()
	if err := ValidateArtifact(ctx, reader, manifest); err != nil {
		return fmt.Errorf("install helper: remote readback: %w", err)
	}
	return nil
}

func cleanupExactTemp(ctx context.Context, remote InstallRemote, tempPath string, uid uint32) {
	parent := path.Dir(tempPath)
	parentAttrs, err := remote.Lstat(ctx, parent)
	if err != nil || !exactOwned(parentAttrs, uid, RemoteDirectory, 0700, 0) {
		return
	}
	tempAttrs, err := remote.Lstat(ctx, tempPath)
	if err != nil || tempAttrs.Kind != RemoteRegular || tempAttrs.UID != uid || tempAttrs.Mode&^uint32(0777) != 0 {
		return
	}
	_ = remote.RemoveExact(ctx, tempPath)
}
