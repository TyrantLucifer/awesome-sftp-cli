package helper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

func TestBindingProbeParserRequiresByteZeroBoundariesAndSupportedNonRootTarget(t *testing.T) {
	valid := []byte("amsftp-helper-bind-v1\x001001\x00/home/alice\x00Linux\x00x86_64\x00")
	observation, err := ParseBindingProbe(valid)
	if err != nil {
		t.Fatal(err)
	}
	if observation.UID != 1001 || observation.Home != "/home/alice" || observation.Target != (Target{OS: "linux", Arch: "amd64"}) {
		t.Fatalf("observation = %#v", observation)
	}
	tests := [][]byte{
		append([]byte("banner"), valid...),
		[]byte("amsftp-helper-bind-v1\x000\x00/root\x00Linux\x00x86_64\x00"),
		[]byte("amsftp-helper-bind-v1\x001001\x00/home/alice\x00FreeBSD\x00x86_64\x00"),
		[]byte("amsftp-helper-bind-v1\x001001\x00/home/alice\x00Linux\x00mips\x00"),
		append(append([]byte(nil), valid...), 'x'),
		bytes.Repeat([]byte{'x'}, MaxProbeStdoutBytes+1),
	}
	for index, raw := range tests {
		if _, err := ParseBindingProbe(raw); err == nil {
			t.Fatalf("probe mutation %d succeeded", index)
		}
	}
}

func TestSafeHomeAndDerivedPlanEnforceComponentAndPathBounds(t *testing.T) {
	manifest, err := ParseManifestV1(fixtureManifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSafeHome("/home/alice"); err != nil {
		t.Fatal(err)
	}
	component255 := strings.Repeat("a", 255)
	if err := ValidateSafeHome("/" + component255); err != nil {
		t.Fatalf("255-byte component: %v", err)
	}
	for _, invalid := range []string{"", "relative", "/home/../alice", "/home/al ice", "/" + strings.Repeat("a", 256)} {
		if err := ValidateSafeHome(invalid); err == nil {
			t.Fatalf("safe home accepted %q", invalid)
		}
	}
	plan, err := DeriveInstallPlan("/home/alice", manifest)
	if err != nil {
		t.Fatal(err)
	}
	wantSuffix := "/.local/lib/amsftp/helpers/p1/4.0.0/linux-amd64-" + manifest.SHA256 + "/amsftp"
	if plan.FinalPath != "/home/alice"+wantSuffix || len(plan.FinalPath) > MaxHelperRemotePathBytes {
		t.Fatalf("plan = %#v", plan)
	}
	tooLongHome := "/" + strings.Repeat("a", 255) + "/" + strings.Repeat("b", 255) + "/" + strings.Repeat("c", 255) + "/" + strings.Repeat("d", 100)
	if err := ValidateSafeHome(tooLongHome); err != nil {
		t.Fatalf("home grammar should be valid before derived path bound: %v", err)
	}
	if _, err := DeriveInstallPlan(tooLongHome, manifest); err == nil {
		t.Fatal("1001+ byte derived path succeeded")
	}
}

func TestHelperFreshSSHArgumentsHaveOneRestrictedCommandAndNoBusinessInput(t *testing.T) {
	manifest, err := ParseManifestV1(fixtureManifest)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := DeriveInstallPlan("/home/alice", manifest)
	if err != nil {
		t.Fatal(err)
	}
	arguments, err := HelperSSHArguments("/usr/bin/ssh", "work-host", plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(arguments) != 18 || arguments[0] != "/usr/bin/ssh" || arguments[len(arguments)-2] != "work-host" || arguments[len(arguments)-1] != "exec "+plan.FinalPath+" helper serve" {
		t.Fatalf("arguments = %#v", arguments)
	}
	joined := strings.Join(arguments, "\n")
	for _, required := range []string{"-oGSSAPIDelegateCredentials=no", "-oControlMaster=no", "-oControlPath=none", "-oControlPersist=no"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("arguments missing %q", required)
		}
	}
	if strings.Contains(joined, "user-pattern") || strings.Contains(joined, manifest.KeyID) {
		t.Fatal("business or arbitrary manifest data entered the remote command")
	}
}

func TestArtifactValidationStreamsExpectedPlusOneAndMatchesManifest(t *testing.T) {
	data := []byte("amsftp Stage 4 fixture only\n")
	raw := manifestForArtifact(t, data)
	manifest, err := ParseManifestV1(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateArtifact(context.Background(), bytes.NewReader(data), manifest); err != nil {
		t.Fatal(err)
	}
	if err := ValidateArtifact(context.Background(), bytes.NewReader(append(data, 'x')), manifest); err == nil {
		t.Fatal("expected+1 artifact was accepted")
	}
	mutated := append([]byte(nil), data...)
	mutated[0] ^= 1
	if err := ValidateArtifact(context.Background(), bytes.NewReader(mutated), manifest); err == nil {
		t.Fatal("same-size hash mutation was accepted")
	}
}

func manifestForArtifact(t *testing.T, data []byte) []byte {
	t.Helper()
	digest := sha256.Sum256(data)
	return []byte(fmt.Sprintf("amsftp-helper-manifest-v1\nversion=4.0.0\nprotocol_major=1\nos=linux\narch=amd64\nsize=%d\nsha256=%x\nkey_id=fixture-rfc8032-nonrelease\nmin_client=4.0.0\n", len(data), digest))
}
