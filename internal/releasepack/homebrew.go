package releasepack

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// HomebrewFormulaRequest binds the channel formula to the exact four final
// release archives. The caller must pass the final reviewed project license;
// the renderer never selects or infers one.
type HomebrewFormulaRequest struct {
	Version  string
	License  string
	Archives []Archive
}

// HomebrewPreviewFormulaRequest renders the same formula contract against a
// loopback-only asset server for the pre-publication tap lifecycle. It is not a
// public channel and cannot admit a production release origin.
type HomebrewPreviewFormulaRequest struct {
	HomebrewFormulaRequest
	AssetBaseURL string
}

// BuildHomebrewFormula renders the deterministic AMSFTP formula for an
// immutable GitHub Release. It does not publish the formula or admit any
// archive as a production Helper trust root.
func BuildHomebrewFormula(request HomebrewFormulaRequest) ([]byte, error) {
	baseURL := fmt.Sprintf("https://github.com/TyrantLucifer/awesome-sftp-cli/releases/download/v%s", request.Version)
	return buildHomebrewFormula(request, baseURL)
}

// BuildHomebrewPreviewFormula renders a formula for a local pre-publication
// channel exercise. Only an exact IPv4 loopback HTTP origin with an explicit
// non-zero port is accepted.
func BuildHomebrewPreviewFormula(request HomebrewPreviewFormulaRequest) ([]byte, error) {
	if !validHomebrewPreviewBaseURL(request.AssetBaseURL) {
		return nil, errors.New("build Homebrew preview formula: asset base URL must be exact loopback HTTP with an explicit port")
	}
	return buildHomebrewFormula(request.HomebrewFormulaRequest, request.AssetBaseURL)
}

func buildHomebrewFormula(request HomebrewFormulaRequest, baseURL string) ([]byte, error) {
	archives, err := validateHomebrewFormulaRequest(request)
	if err != nil {
		return nil, err
	}

	var output strings.Builder
	output.WriteString("class Amsftp < Formula\n")
	output.WriteString("  desc \"Vim-first two-pane SFTP file manager\"\n")
	output.WriteString("  homepage \"https://github.com/TyrantLucifer/awesome-sftp-cli\"\n")
	fmt.Fprintf(&output, "  version %q\n", request.Version)
	fmt.Fprintf(&output, "  license %q\n", request.License)
	output.WriteByte('\n')
	writeHomebrewPlatform(&output, "macos", archives[Target{OS: "darwin", Arch: "arm64"}], archives[Target{OS: "darwin", Arch: "amd64"}], baseURL)
	output.WriteByte('\n')
	writeHomebrewPlatform(&output, "linux", archives[Target{OS: "linux", Arch: "arm64"}], archives[Target{OS: "linux", Arch: "amd64"}], baseURL)
	output.WriteString(`
  def install
    bin.install "amsftp"
    man1.install "share/man/man1/amsftp.1"
    generate_completions_from_executable(bin/"amsftp", "completion")
  end

  test do
`)
	fmt.Fprintf(&output, "    assert_match %q, shell_output(\"#{bin}/amsftp --version\")\n", request.Version)
	output.WriteString("  end\nend\n")
	return []byte(output.String()), nil
}

func validHomebrewPreviewBaseURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	host, portText, err := net.SplitHostPort(parsed.Host)
	if err != nil || host != "127.0.0.1" {
		return false
	}
	port, err := strconv.Atoi(portText)
	return err == nil && port > 0 && port <= 65535
}

func validateHomebrewFormulaRequest(request HomebrewFormulaRequest) (map[Target]Archive, error) {
	if !validReleaseVersion(request.Version) || !validHomebrewLicense(request.License) {
		return nil, errors.New("build Homebrew formula: release version or project license is invalid")
	}
	if len(request.Archives) != len(Targets) {
		return nil, errors.New("build Homebrew formula: exact four-archive set is required")
	}
	archives := make(map[Target]Archive, len(Targets))
	for _, archive := range request.Archives {
		if !validTarget(archive.Target) || archive.Name != archiveName(request.Version, archive.Target) || len(archive.Bytes) == 0 || len(archive.Bytes) > maxReleaseInputBytes {
			return nil, errors.New("build Homebrew formula: archive identity or bytes are invalid")
		}
		if _, duplicate := archives[archive.Target]; duplicate {
			return nil, errors.New("build Homebrew formula: duplicate archive target")
		}
		archives[archive.Target] = archive
	}
	for _, target := range Targets {
		if _, exists := archives[target]; !exists {
			return nil, errors.New("build Homebrew formula: archive target is missing")
		}
	}
	return archives, nil
}

func validHomebrewLicense(value string) bool {
	// Homebrew's single-license form accepts an SPDX identifier string. More
	// complex expressions require a different Ruby DSL shape, so reject them
	// instead of rendering a syntactically valid but semantically wrong formula.
	return validSPDXExpression(value) && !strings.ContainsAny(value, " ()")
}

func writeHomebrewPlatform(output *strings.Builder, platform string, arm, intel Archive, baseURL string) {
	fmt.Fprintf(output, "  on_%s do\n", platform)
	writeHomebrewArchitecture(output, "arm", arm, baseURL)
	writeHomebrewArchitecture(output, "intel", intel, baseURL)
	output.WriteString("  end\n")
}

func writeHomebrewArchitecture(output *strings.Builder, architecture string, archive Archive, baseURL string) {
	fmt.Fprintf(output, "    on_%s do\n", architecture)
	fmt.Fprintf(output, "      url %q\n", baseURL+"/"+archive.Name)
	fmt.Fprintf(output, "      sha256 %q\n", digestBytes(archive.Bytes))
	output.WriteString("    end\n")
}
