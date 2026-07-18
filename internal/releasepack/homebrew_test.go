package releasepack

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestHomebrewFormulaBindsExactFourImmutableReleaseArchives(t *testing.T) {
	request := homebrewFormulaFixture()
	formula, err := BuildHomebrewFormula(request)
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(formula)
	for _, archive := range request.Archives {
		digest := sha256.Sum256(archive.Bytes)
		url := "https://github.com/TyrantLucifer/awsome-sftp-cli/releases/download/v1.0.0/" + archive.Name
		if strings.Count(rendered, url) != 1 || strings.Count(rendered, hex.EncodeToString(digest[:])) != 1 {
			t.Fatalf("formula does not bind %s exactly once:\n%s", archive.Name, rendered)
		}
	}
	for _, contract := range []string{
		`class Amsftp < Formula`,
		`homepage "https://github.com/TyrantLucifer/awsome-sftp-cli"`,
		`version "1.0.0"`,
		`license "BSD-3-Clause"`,
		`on_macos do`, `on_linux do`, `on_arm do`, `on_intel do`,
	} {
		if !strings.Contains(rendered, contract) {
			t.Fatalf("formula missing %q:\n%s", contract, rendered)
		}
	}
	for _, block := range []string{
		"  on_macos do\n    on_arm do\n      url \"https://github.com/TyrantLucifer/awsome-sftp-cli/releases/download/v1.0.0/amsftp_1.0.0_darwin_arm64.tar.gz\"",
		"    on_intel do\n      url \"https://github.com/TyrantLucifer/awsome-sftp-cli/releases/download/v1.0.0/amsftp_1.0.0_darwin_amd64.tar.gz\"",
		"  on_linux do\n    on_arm do\n      url \"https://github.com/TyrantLucifer/awsome-sftp-cli/releases/download/v1.0.0/amsftp_1.0.0_linux_arm64.tar.gz\"",
		"    on_intel do\n      url \"https://github.com/TyrantLucifer/awsome-sftp-cli/releases/download/v1.0.0/amsftp_1.0.0_linux_amd64.tar.gz\"",
	} {
		if !strings.Contains(rendered, block) {
			t.Fatalf("formula has an incorrect platform/architecture mapping:\n%s", rendered)
		}
	}
}

func TestHomebrewFormulaInstallsBinaryManCompletionsAndVersionSmoke(t *testing.T) {
	formula, err := BuildHomebrewFormula(homebrewFormulaFixture())
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(formula)
	for _, contract := range []string{
		`bin.install "amsftp"`,
		`man1.install "share/man/man1/amsftp.1"`,
		`generate_completions_from_executable(bin/"amsftp", "completion")`,
		`assert_match "1.0.0", shell_output("#{bin}/amsftp --version")`,
	} {
		if strings.Count(rendered, contract) != 1 {
			t.Fatalf("formula contract %q count != 1:\n%s", contract, rendered)
		}
	}
	if strings.Contains(rendered, "service do") || strings.Contains(rendered, "helper") {
		t.Fatalf("formula must not create an unadmitted service or Helper trust path:\n%s", rendered)
	}
}

func TestHomebrewFormulaIsDeterministicAndRejectsUnboundInputs(t *testing.T) {
	request := homebrewFormulaFixture()
	first, err := BuildHomebrewFormula(request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildHomebrewFormula(request)
	if err != nil || string(first) != string(second) || len(first) == 0 || first[len(first)-1] != '\n' {
		t.Fatalf("deterministic formula = %v, %q", err, second)
	}

	for name, mutate := range map[string]func(*HomebrewFormulaRequest){
		"prerelease version": func(value *HomebrewFormulaRequest) { value.Version = "1.0.0-rc1" },
		"missing license":    func(value *HomebrewFormulaRequest) { value.License = "" },
		"invalid license":    func(value *HomebrewFormulaRequest) { value.License = "MIT OR" },
		"compound license":   func(value *HomebrewFormulaRequest) { value.License = "MIT OR Apache-2.0" },
		"missing archive":    func(value *HomebrewFormulaRequest) { value.Archives = value.Archives[:3] },
		"duplicate target": func(value *HomebrewFormulaRequest) {
			value.Archives[3] = value.Archives[2]
		},
		"wrong name": func(value *HomebrewFormulaRequest) {
			value.Archives[0].Name = "amsftp_1.0.0_darwin_x86_64.tar.gz"
		},
		"empty bytes": func(value *HomebrewFormulaRequest) { value.Archives[0].Bytes = nil },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := homebrewFormulaFixture()
			mutate(&candidate)
			if _, err := BuildHomebrewFormula(candidate); err == nil {
				t.Fatal("invalid Homebrew input was accepted")
			}
		})
	}
}

func homebrewFormulaFixture() HomebrewFormulaRequest {
	archives := make([]Archive, 0, len(Targets))
	for _, target := range Targets {
		archives = append(archives, Archive{
			Name:   "amsftp_1.0.0_" + target.OS + "_" + target.Arch + ".tar.gz",
			Target: target,
			Bytes:  []byte("final release archive for " + target.OS + "/" + target.Arch),
		})
	}
	return HomebrewFormulaRequest{Version: "1.0.0", License: "BSD-3-Clause", Archives: archives}
}
