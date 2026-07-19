package docscheck

import (
	"fmt"
	"slices"
	"strings"
)

const (
	workflowProvenanceRecordRule       = "workflow.provenance_record"
	workflowProvenanceVerificationRule = "workflow.provenance_verification"

	canonicalProducerMessage  = "workflow must use the canonical ordered provenance producer profile: exact LEG/matrix, artifact hash dataflow, Record provenance, and pinned bound uploads"
	canonicalCollectorMessage = "workflow compare job must use the canonical ordered provenance collector profile: exact needs, pinned downloads, env/porcelain manifest, target fields, and build/repro/accepted hash bindings"
)

type provenanceRecordKind uint8

const (
	standardProvenanceRecord provenanceRecordKind = iota
	buildArtifactProvenanceRecord
	reproArtifactProvenanceRecord
)

type provenanceMatrixKind uint8

const (
	noProvenanceMatrix provenanceMatrixKind = iota
	osProvenanceMatrix
	buildProvenanceMatrix
	reproProvenanceMatrix
	fuzzProvenanceMatrix
	concurrencyProvenanceMatrix
)

type provenanceProducerProfile struct {
	jobID              string
	leg                string
	runsOn             string
	timeout            string
	needs              []string
	matrix             provenanceMatrixKind
	record             provenanceRecordKind
	expectedLegs       []string
	environment        map[string]string
	comparisonArtifact string
}

type provenanceArtifactGroup struct {
	artifact  string
	goos      string
	goarch    string
	buildLeg  string
	reproALeg string
	reproBLeg string
}

type provenanceWorkflowProfile struct {
	producers          []provenanceProducerProfile
	compareNeeds       []string
	expectedLegs       []string
	fileCount          int
	comparisonArtifact string
	artifactGroups     []provenanceArtifactGroup
}

func checkWorkflowProvenancePolicy(doc workflowDoc) []Finding {
	profile, ok := canonicalProvenanceWorkflowProfile(doc.path)
	if !ok {
		return nil
	}

	jobs := make(map[string]workflowJob, len(doc.jobs))
	for _, job := range doc.jobs {
		jobs[job.id] = job
	}

	var findings []Finding
	producerIDs := make(map[string]struct{}, len(profile.producers))
	for _, expected := range profile.producers {
		producerIDs[expected.jobID] = struct{}{}
		job, exists := jobs[expected.jobID]
		if exists && canonicalProvenanceProducer(doc, job, expected) {
			continue
		}
		line := 1
		if exists {
			line = job.line
		}
		findings = append(findings, Finding{
			Path:    doc.path,
			Line:    line,
			Rule:    workflowProvenanceRecordRule,
			Message: canonicalProducerMessage,
		})
	}

	for _, job := range doc.jobs {
		if job.id == "compare" {
			continue
		}
		if _, expected := producerIDs[job.id]; expected {
			continue
		}
		if jobContainsProvenanceStep(job) {
			findings = append(findings, Finding{
				Path:    doc.path,
				Line:    job.line,
				Rule:    workflowProvenanceRecordRule,
				Message: canonicalProducerMessage,
			})
		}
	}

	compare, exists := jobs["compare"]
	if !exists || !canonicalProvenanceCollector(doc, compare, profile) {
		line := 1
		if exists {
			line = compare.line
		}
		findings = append(findings, Finding{
			Path:    doc.path,
			Line:    line,
			Rule:    workflowProvenanceVerificationRule,
			Message: canonicalCollectorMessage,
		})
	}

	return findings
}

func canonicalProvenanceWorkflowProfile(path string) (provenanceWorkflowProfile, bool) {
	switch path {
	case ".github/workflows/ci.yml":
		legs := []string{
			"quality",
			"auth-integration",
			"native-ubuntu-22.04",
			"native-ubuntu-24.04",
			"native-ubuntu-24.04-arm",
			"native-macos-15",
			"native-macos-15-intel",
			"oldstable-ubuntu-22.04",
			"oldstable-ubuntu-24.04",
			"oldstable-macos-15",
			"oldstable-macos-15-intel",
			"build-amsftp-darwin-arm64",
			"build-amsftp-darwin-amd64",
			"build-amsftp-linux-arm64",
			"build-amsftp-linux-amd64",
			"repro-amsftp-darwin-arm64-a",
			"repro-amsftp-darwin-arm64-b",
			"repro-amsftp-darwin-amd64-a",
			"repro-amsftp-darwin-amd64-b",
			"repro-amsftp-linux-arm64-a",
			"repro-amsftp-linux-arm64-b",
			"repro-amsftp-linux-amd64-a",
			"repro-amsftp-linux-amd64-b",
			"reproducibility-compare",
		}
		return provenanceWorkflowProfile{
			producers: []provenanceProducerProfile{
				{
					jobID: "quality", leg: "quality", runsOn: "ubuntu-24.04", timeout: "30",
					matrix: noProvenanceMatrix, record: standardProvenanceRecord,
					expectedLegs: legs[0:1], environment: map[string]string{"LEG": "quality"},
				},
				{
					jobID: "auth-integration", leg: "auth-integration", runsOn: "ubuntu-24.04", timeout: "30",
					needs:  []string{"quality"},
					matrix: noProvenanceMatrix, record: standardProvenanceRecord,
					expectedLegs: legs[1:2], environment: map[string]string{"LEG": "auth-integration"},
				},
				{
					jobID: "native", leg: "native-${{ matrix.os }}", runsOn: "${{ matrix.os }}", timeout: "45",
					matrix: osProvenanceMatrix, record: standardProvenanceRecord,
					expectedLegs: legs[2:7], environment: map[string]string{"LEG": "native-${{ matrix.os }}"},
				},
				{
					jobID: "oldstable", leg: "oldstable-${{ matrix.os }}", runsOn: "${{ matrix.os }}", timeout: "30",
					matrix: osProvenanceMatrix, record: standardProvenanceRecord,
					expectedLegs: legs[7:11], environment: map[string]string{"GOTOOLCHAIN": "local", "LEG": "oldstable-${{ matrix.os }}"},
				},
				{
					jobID: "build", leg: "build-${{ matrix.artifact }}", runsOn: "ubuntu-24.04", timeout: "20",
					matrix: buildProvenanceMatrix, record: buildArtifactProvenanceRecord,
					expectedLegs: legs[11:15], environment: map[string]string{"LEG": "build-${{ matrix.artifact }}"},
				},
				{
					jobID: "reproducibility", leg: "repro-${{ matrix.artifact }}-${{ matrix.replica }}", runsOn: "ubuntu-24.04", timeout: "20",
					matrix: reproProvenanceMatrix, record: reproArtifactProvenanceRecord,
					expectedLegs: legs[15:23], environment: map[string]string{"LEG": "repro-${{ matrix.artifact }}-${{ matrix.replica }}"},
				},
				{
					jobID: "reproducibility-compare", leg: "reproducibility-compare", runsOn: "ubuntu-24.04", timeout: "15",
					needs: []string{"reproducibility"}, matrix: noProvenanceMatrix, record: standardProvenanceRecord,
					expectedLegs: legs[23:24], environment: map[string]string{"LEG": "reproducibility-compare"},
					comparisonArtifact: "reproducibility-comparison",
				},
			},
			compareNeeds:       []string{"quality", "auth-integration", "native", "oldstable", "build", "reproducibility", "reproducibility-compare"},
			expectedLegs:       legs,
			fileCount:          48,
			comparisonArtifact: "reproducibility-comparison",
			artifactGroups: []provenanceArtifactGroup{
				{artifact: "amsftp-darwin-arm64", goos: "darwin", goarch: "arm64", buildLeg: "build-amsftp-darwin-arm64", reproALeg: "repro-amsftp-darwin-arm64-a", reproBLeg: "repro-amsftp-darwin-arm64-b"},
				{artifact: "amsftp-darwin-amd64", goos: "darwin", goarch: "amd64", buildLeg: "build-amsftp-darwin-amd64", reproALeg: "repro-amsftp-darwin-amd64-a", reproBLeg: "repro-amsftp-darwin-amd64-b"},
				{artifact: "amsftp-linux-arm64", goos: "linux", goarch: "arm64", buildLeg: "build-amsftp-linux-arm64", reproALeg: "repro-amsftp-linux-arm64-a", reproBLeg: "repro-amsftp-linux-arm64-b"},
				{artifact: "amsftp-linux-amd64", goos: "linux", goarch: "amd64", buildLeg: "build-amsftp-linux-amd64", reproALeg: "repro-amsftp-linux-amd64-a", reproBLeg: "repro-amsftp-linux-amd64-b"},
			},
		}, true
	case ".github/workflows/nightly.yml":
		legs := []string{
			"fuzz-amd64-FuzzFrameDecoder",
			"fuzz-arm64-FuzzFrameDecoder",
			"fuzz-amd64-FuzzEnvelopeDecode",
			"fuzz-arm64-FuzzEnvelopeDecode",
			"fuzz-amd64-FuzzWireBytes",
			"fuzz-arm64-FuzzWireBytes",
			"fuzz-amd64-FuzzNormalizePath",
			"fuzz-arm64-FuzzNormalizePath",
			"concurrency-amd64",
			"concurrency-arm64",
			"repro-amsftp-darwin-arm64-a",
			"repro-amsftp-darwin-arm64-b",
			"repro-amsftp-darwin-amd64-a",
			"repro-amsftp-darwin-amd64-b",
			"repro-amsftp-linux-arm64-a",
			"repro-amsftp-linux-arm64-b",
			"repro-amsftp-linux-amd64-a",
			"repro-amsftp-linux-amd64-b",
			"reproducibility-compare",
		}
		return provenanceWorkflowProfile{
			producers: []provenanceProducerProfile{
				{
					jobID: "fuzz", leg: "fuzz-${{ matrix.arch }}-${{ matrix.target }}", runsOn: "${{ matrix.runner }}", timeout: "30",
					matrix: fuzzProvenanceMatrix, record: standardProvenanceRecord,
					expectedLegs: legs[0:8], environment: map[string]string{"LEG": "fuzz-${{ matrix.arch }}-${{ matrix.target }}"},
				},
				{
					jobID: "concurrency", leg: "concurrency-${{ matrix.arch }}", runsOn: "${{ matrix.runner }}", timeout: "30",
					matrix: concurrencyProvenanceMatrix, record: standardProvenanceRecord,
					expectedLegs: legs[8:10], environment: map[string]string{"LEG": "concurrency-${{ matrix.arch }}"},
				},
				{
					jobID: "reproducibility", leg: "repro-${{ matrix.artifact }}-${{ matrix.replica }}", runsOn: "ubuntu-24.04", timeout: "20",
					matrix: reproProvenanceMatrix, record: reproArtifactProvenanceRecord,
					expectedLegs: legs[10:18], environment: map[string]string{"LEG": "repro-${{ matrix.artifact }}-${{ matrix.replica }}"},
				},
				{
					jobID: "reproducibility-compare", leg: "reproducibility-compare", runsOn: "ubuntu-24.04", timeout: "15",
					needs: []string{"reproducibility"}, matrix: noProvenanceMatrix, record: standardProvenanceRecord,
					expectedLegs: legs[18:19], environment: map[string]string{"LEG": "reproducibility-compare"},
					comparisonArtifact: "nightly-reproducibility-comparison",
				},
			},
			compareNeeds:       []string{"fuzz", "concurrency", "reproducibility", "reproducibility-compare"},
			expectedLegs:       legs,
			fileCount:          38,
			comparisonArtifact: "nightly-reproducibility-comparison",
			artifactGroups: []provenanceArtifactGroup{
				{artifact: "amsftp-darwin-arm64", goos: "darwin", goarch: "arm64", reproALeg: "repro-amsftp-darwin-arm64-a", reproBLeg: "repro-amsftp-darwin-arm64-b"},
				{artifact: "amsftp-darwin-amd64", goos: "darwin", goarch: "amd64", reproALeg: "repro-amsftp-darwin-amd64-a", reproBLeg: "repro-amsftp-darwin-amd64-b"},
				{artifact: "amsftp-linux-arm64", goos: "linux", goarch: "arm64", reproALeg: "repro-amsftp-linux-arm64-a", reproBLeg: "repro-amsftp-linux-arm64-b"},
				{artifact: "amsftp-linux-amd64", goos: "linux", goarch: "amd64", reproALeg: "repro-amsftp-linux-amd64-a", reproBLeg: "repro-amsftp-linux-amd64-b"},
			},
		}, true
	default:
		return provenanceWorkflowProfile{}, false
	}
}

func canonicalProvenanceProducer(doc workflowDoc, job workflowJob, profile provenanceProducerProfile) bool {
	if doc.env != nil || doc.defaults != nil || job.workflowEnv != nil || job.workflowDefaults != nil || job.defaults != nil || job.ifExpr != nil {
		return false
	}
	if !jobHasCanonicalProducerKeys(job, profile) || job.runsOn == nil || job.runsOn.style != policyYAMLPlainScalar || job.runsOn.value != profile.runsOn ||
		job.timeout == nil || job.timeout.style != policyYAMLPlainScalar || job.timeout.value != profile.timeout ||
		!mappingHasExactScalars(job.env, profile.environment) || !canonicalNeeds(job.needs, profile.needs) ||
		!canonicalProvenanceMatrix(job, profile.matrix) {
		return false
	}
	expandedLegs, ok := expandedProvenanceLegs(job, profile)
	if !ok || !stringSlicesEqual(expandedLegs, profile.expectedLegs) {
		return false
	}

	if !canonicalProducerSteps(job, profile) {
		return false
	}
	provenanceSteps := 0
	for _, step := range job.steps {
		if stepContainsProvenance(step) {
			provenanceSteps++
		}
	}
	return provenanceSteps == 2
}

func canonicalProducerSteps(job workflowJob, profile provenanceProducerProfile) bool {
	if len(job.steps) < 2 {
		return false
	}
	recordIndex := len(job.steps) - 2
	uploadIndex := len(job.steps) - 1
	if !canonicalRecordStep(job.steps[recordIndex], "Record provenance", canonicalRecordLines(profile.record)) ||
		!canonicalUploadStep(job.steps[uploadIndex], "Upload provenance") {
		return false
	}

	switch profile.jobID {
	case "quality":
		return canonicalQualityPreviewBundleHandoff(job.steps[:recordIndex])
	case "auth-integration":
		return len(job.steps) == 13 && canonicalAuthIntegrationPrefix(job.steps[:recordIndex])
	case "build":
		return len(job.steps) == 8 && canonicalBuildProducerPrefix(job.steps[:recordIndex])
	case "fuzz":
		return len(job.steps) == 5 && canonicalFuzzProducerPrefix(job.steps[:recordIndex])
	case "concurrency":
		return len(job.steps) == 5 && canonicalConcurrencyProducerPrefix(job.steps[:recordIndex])
	case "reproducibility":
		return len(job.steps) == 8 && canonicalReproProducerPrefix(job.steps[:recordIndex])
	case "reproducibility-compare":
		return len(job.steps) == 7 && canonicalReproComparisonPrefix(job.steps[:recordIndex], profile.comparisonArtifact)
	default:
		return true
	}
}

func canonicalAuthIntegrationPrefix(steps []workflowStep) bool {
	if len(steps) != 11 {
		return false
	}
	return stepIsExactCheckout(steps[0]) && stepIsExactCurrentSetupGo(steps[1]) &&
		canonicalRunStep(steps[2], "Install isolated authentication fixtures", []string{
			`set -euo pipefail`,
			`sudo apt-get update`,
			`sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y \`,
			`  expect \`,
			`  krb5-admin-server \`,
			`  krb5-kdc \`,
			`  krb5-user \`,
			`  netcat-openbsd \`,
			`  openssh-server \`,
			`  proftpd-core \`,
			`  proftpd-mod-crypto`,
		}) &&
		canonicalRunStep(steps[3], "Capture current OpenSSH version", []string{
			`set -euo pipefail`,
			`current_version="$(/usr/bin/ssh -V 2>&1)"`,
			`case "${current_version}" in`,
			`  OpenSSH_*) ;;`,
			`  *)`,
			`    printf 'system ssh did not report an OpenSSH version\n' >&2`,
			`    exit 1`,
			`    ;;`,
			`esac`,
			`mkdir -p "${RUNNER_TEMP}/auth-integration"`,
			`printf '%s\n' "${current_version}" | tee "${RUNNER_TEMP}/auth-integration/openssh-current-version"`,
		}) &&
		canonicalRunStep(steps[4], "Capture current MIT Kerberos version", []string{
			`set -euo pipefail`,
			`kerberos_version="$(/usr/bin/klist -V 2>&1)"`,
			`case "${kerberos_version}" in`,
			`  Kerberos\ 5\ version\ *) ;;`,
			`  *)`,
			`    printf 'system klist did not report an MIT Kerberos version\n' >&2`,
			`    exit 1`,
			`    ;;`,
			`esac`,
			`mkdir -p "${RUNNER_TEMP}/auth-integration"`,
			`printf '%s\n' "${kerberos_version}" | tee "${RUNNER_TEMP}/auth-integration/kerberos-current-version"`,
		}) &&
		canonicalRunStep(steps[5], "Capture current ProFTPD vendor SFTP version", []string{
			`set -euo pipefail`,
			`proftpd_version="$(/usr/sbin/proftpd -v 2>&1)"`,
			`case "${proftpd_version}" in`,
			`  ProFTPD\ Version\ *) ;;`,
			`  *)`,
			`    printf 'system proftpd did not report a ProFTPD version\n' >&2`,
			`    exit 1`,
			`    ;;`,
			`esac`,
			`{`,
			`  printf '%s\n' "${proftpd_version}"`,
			`  dpkg-query -W -f='${Package}=${Version}\n' proftpd-core proftpd-mod-crypto | LC_ALL=C sort`,
			`} | tee "${RUNNER_TEMP}/auth-integration/proftpd-current-version"`,
		}) &&
		canonicalPreviewBundleDownload(steps[6]) &&
		canonicalRunStep(steps[7], "Verify and extract internal preview bundle", []string{
			`set -euo pipefail`,
			`bundle="${RUNNER_TEMP}/auth-integration/bundle"`,
			`test "$(find "${bundle}" -mindepth 1 -maxdepth 1 -type f | wc -l | tr -d '[:space:]')" = 7`,
			`(cd "${bundle}" && sha256sum -c checksums.txt)`,
			`archive="${bundle}/amsftp_0.1.0-internal_linux_amd64.tar.gz"`,
			`install_root="${RUNNER_TEMP}/auth-integration/install"`,
			`test -f "${archive}"`,
			`test ! -e "${install_root}"`,
			`mkdir -p "${install_root}"`,
			`tar -xzf "${archive}" -C "${install_root}"`,
			`installed="${install_root}/amsftp_0.1.0-internal_linux_amd64/amsftp"`,
			`test -x "${installed}"`,
			`"${installed}" --version | grep -F "0.1.0-internal commit=${GITHUB_SHA} dirty=false"`,
			`tar -xOf "${archive}" amsftp_0.1.0-internal_linux_amd64/VERSION.json | grep -F "\"commit\":\"${GITHUB_SHA}\""`,
		}) &&
		canonicalRunStep(steps[8], "Run real OpenSSH authentication matrix", []string{
			`set -euo pipefail`,
			`sudo env \`,
			`  AMSFTP_AUTH_BINARY="${RUNNER_TEMP}/auth-integration/install/amsftp_0.1.0-internal_linux_amd64/amsftp" \`,
			`  AMSFTP_AUTH_EXPECT_OPENSSH_VERSION="$(cat "${RUNNER_TEMP}/auth-integration/openssh-current-version")" \`,
			`  AMSFTP_AUTH_ROOT="/tmp/amsftp-auth-${GITHUB_RUN_ID}-${GITHUB_RUN_ATTEMPT}" \`,
			`  bash ./internal/integration/hosted-auth.sh`,
		}) &&
		canonicalRunStep(steps[9], "Run real MIT Kerberos/GSSAPI matrix", []string{
			`set -euo pipefail`,
			`sudo env \`,
			`  AMSFTP_KERBEROS_BINARY="${RUNNER_TEMP}/auth-integration/install/amsftp_0.1.0-internal_linux_amd64/amsftp" \`,
			`  AMSFTP_KERBEROS_EXPECT_VERSION="$(cat "${RUNNER_TEMP}/auth-integration/kerberos-current-version")" \`,
			`  AMSFTP_KERBEROS_ROOT="/tmp/amsftp-kerberos-${GITHUB_RUN_ID}-${GITHUB_RUN_ATTEMPT}" \`,
			`  bash ./internal/integration/hosted-kerberos.sh`,
		}) &&
		canonicalRunStep(steps[10], "Run real ProFTPD vendor SFTP matrix", []string{
			`set -euo pipefail`,
			`AMSFTP_VENDOR_BINARY="${RUNNER_TEMP}/auth-integration/install/amsftp_0.1.0-internal_linux_amd64/amsftp" \`,
			`  AMSFTP_VENDOR_SFTP_EXPECT_VERSION_FILE="${RUNNER_TEMP}/auth-integration/proftpd-current-version" \`,
			`  AMSFTP_VENDOR_SFTP_ROOT="/tmp/amsftp-vendor-sftp-${GITHUB_RUN_ID}-${GITHUB_RUN_ATTEMPT}" \`,
			`  bash ./internal/integration/hosted-vendor-sftp.sh`,
		})
}

func canonicalQualityPreviewBundleHandoff(steps []workflowStep) bool {
	packaging := -1
	for index, step := range steps {
		if step.name != nil && step.name.value == "Exercise deterministic internal preview packaging and clean-home lifecycle" {
			if packaging != -1 {
				return false
			}
			packaging = index
		}
	}
	return packaging >= 0 && packaging+1 < len(steps) && canonicalPreviewBundleUpload(steps[packaging+1])
}

func canonicalPreviewBundleUpload(step workflowStep) bool {
	return canonicalBuildArtifactUpload(
		step,
		"Upload internal preview bundle",
		"amsftp-internal-preview-${{ github.sha }}",
		"${{ runner.temp }}/internal-preview-first",
	)
}

func canonicalPreviewBundleDownload(step workflowStep) bool {
	return step.name != nil && step.name.value == "Download internal preview bundle" && step.uses != nil &&
		step.uses.value == "actions/download-artifact@"+approvedActionCommits["actions/download-artifact"] &&
		nodeHasExactKeys(step.node, "name", "uses", "with") &&
		mappingHasExactScalars(step.with, map[string]string{
			"name": "amsftp-internal-preview-${{ github.sha }}", "path": "${{ runner.temp }}/auth-integration/bundle",
		})
}

func canonicalProvenanceCollector(doc workflowDoc, job workflowJob, profile provenanceWorkflowProfile) bool {
	if doc.env != nil || doc.defaults != nil || job.workflowEnv != nil || job.workflowDefaults != nil || job.defaults != nil || job.ifExpr != nil ||
		profile.fileCount != len(profile.expectedLegs)*2 || !producerLegsMatchManifest(profile) || !artifactGroupsMatchProducerLegs(profile) ||
		!nodeHasExactKeys(job.node, "needs", "runs-on", "timeout-minutes", "env", "steps") ||
		job.runsOn == nil || job.runsOn.style != policyYAMLPlainScalar || job.runsOn.value != "ubuntu-24.04" ||
		job.timeout == nil || job.timeout.style != policyYAMLPlainScalar || job.timeout.value != "15" ||
		!mappingHasExactScalars(job.env, map[string]string{"LEG": "compare"}) || !canonicalNeeds(job.needs, profile.compareNeeds) ||
		job.strategy != nil || len(job.steps) != 7 || profile.comparisonArtifact == "" || len(profile.artifactGroups) != 4 {
		return false
	}
	return stepIsExactCheckout(job.steps[0]) && canonicalCompareSetupGo(job.steps[1]) &&
		canonicalDownloadStep(job.steps[2]) &&
		canonicalComparisonDownloadStep(job.steps[3], profile.comparisonArtifact) &&
		canonicalRunStep(job.steps[4], "Verify prerequisite provenance", canonicalVerificationLines(profile)) &&
		canonicalRecordStep(job.steps[5], "Record comparison provenance", canonicalComparisonRecordLines()) &&
		canonicalUploadStep(job.steps[6], "Upload comparison provenance")
}

func producerLegsMatchManifest(profile provenanceWorkflowProfile) bool {
	produced := make([]string, 0, len(profile.expectedLegs))
	for _, producer := range profile.producers {
		produced = append(produced, producer.expectedLegs...)
	}
	return stringSlicesEqual(produced, profile.expectedLegs)
}

func artifactGroupsMatchProducerLegs(profile provenanceWorkflowProfile) bool {
	var produced []string
	for _, producer := range profile.producers {
		if producer.record == buildArtifactProvenanceRecord || producer.record == reproArtifactProvenanceRecord {
			produced = append(produced, producer.expectedLegs...)
		}
	}
	bound := make([]string, 0, len(produced))
	for _, group := range profile.artifactGroups {
		if group.artifact != "amsftp-"+group.goos+"-"+group.goarch || group.reproALeg == "" || group.reproBLeg == "" {
			return false
		}
		if group.buildLeg != "" {
			bound = append(bound, group.buildLeg)
		}
		bound = append(bound, group.reproALeg, group.reproBLeg)
	}
	if len(produced) != len(bound) {
		return false
	}
	producedSet := make(map[string]struct{}, len(produced))
	for _, leg := range produced {
		if _, duplicate := producedSet[leg]; duplicate {
			return false
		}
		producedSet[leg] = struct{}{}
	}
	for _, leg := range bound {
		if _, exists := producedSet[leg]; !exists {
			return false
		}
		delete(producedSet, leg)
	}
	return len(producedSet) == 0
}

func expandedProvenanceLegs(job workflowJob, profile provenanceProducerProfile) ([]string, bool) {
	if profile.matrix == noProvenanceMatrix {
		return []string{profile.leg}, profile.leg != "" && !strings.Contains(profile.leg, "${{ matrix.")
	}
	matrix := policyYAMLNodeNamed(job.strategy, "matrix")
	if matrix == nil || matrix.kind != policyYAMLMappingNode || len(matrix.mappings) != 1 {
		return nil, false
	}
	var rows []*policyYAMLNode
	switch matrix.mappings[0].key.value {
	case "os":
		values := matrix.mappings[0].value
		if values == nil || values.kind != policyYAMLSequenceNode {
			return nil, false
		}
		for _, value := range values.items {
			if value.kind != policyYAMLScalarNode {
				return nil, false
			}
			rows = append(rows, &policyYAMLNode{
				kind: policyYAMLMappingNode,
				mappings: []policyYAMLMapping{{
					key:   policyYAMLScalar{value: "os"},
					value: value,
				}},
			})
		}
	case "include":
		include := matrix.mappings[0].value
		if include == nil || include.kind != policyYAMLSequenceNode {
			return nil, false
		}
		rows = include.items
	default:
		return nil, false
	}

	legs := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.kind != policyYAMLMappingNode {
			return nil, false
		}
		leg := profile.leg
		for _, mapping := range row.mappings {
			if mapping.value == nil || mapping.value.kind != policyYAMLScalarNode {
				return nil, false
			}
			leg = strings.ReplaceAll(leg, "${{ matrix."+mapping.key.value+" }}", mapping.value.scalar.value)
		}
		if leg == "" || strings.Contains(leg, "${{ matrix.") {
			return nil, false
		}
		legs = append(legs, leg)
	}
	return legs, true
}

func jobHasCanonicalProducerKeys(job workflowJob, profile provenanceProducerProfile) bool {
	keys := []string{"runs-on", "timeout-minutes", "env", "steps"}
	if profile.matrix != noProvenanceMatrix {
		keys = append(keys, "strategy")
	}
	if len(profile.needs) > 0 {
		keys = append(keys, "needs")
	}
	return nodeHasExactKeys(job.node, keys...)
}

func nodeHasExactKeys(node *policyYAMLNode, keys ...string) bool {
	if node == nil || node.kind != policyYAMLMappingNode || len(node.mappings) != len(keys) {
		return false
	}
	want := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		want[key] = struct{}{}
	}
	for _, mapping := range node.mappings {
		if _, exists := want[mapping.key.value]; !exists {
			return false
		}
	}
	return true
}

func canonicalNeeds(node *policyYAMLNode, expected []string) bool {
	if len(expected) == 0 {
		return node == nil
	}
	if len(expected) == 1 {
		return node != nil && node.kind == policyYAMLScalarNode && node.scalar.style == policyYAMLPlainScalar && node.scalar.value == expected[0]
	}
	if node == nil || node.kind != policyYAMLSequenceNode || len(node.items) != len(expected) {
		return false
	}
	for index, value := range expected {
		item := node.items[index]
		if item.kind != policyYAMLScalarNode || item.scalar.style != policyYAMLPlainScalar || item.scalar.value != value {
			return false
		}
	}
	return true
}

func canonicalProvenanceMatrix(job workflowJob, kind provenanceMatrixKind) bool {
	switch kind {
	case noProvenanceMatrix:
		return job.strategy == nil
	case osProvenanceMatrix:
		return canonicalOSMatrix(job)
	case buildProvenanceMatrix:
		return canonicalIncludeMatrix(job, []string{"goos", "goarch", "artifact"}, [][]string{
			{"darwin", "arm64", "amsftp-darwin-arm64"},
			{"darwin", "amd64", "amsftp-darwin-amd64"},
			{"linux", "arm64", "amsftp-linux-arm64"},
			{"linux", "amd64", "amsftp-linux-amd64"},
		})
	case reproProvenanceMatrix:
		return canonicalIncludeMatrix(job, []string{"goos", "goarch", "artifact", "replica"}, [][]string{
			{"darwin", "arm64", "amsftp-darwin-arm64", "a"},
			{"darwin", "arm64", "amsftp-darwin-arm64", "b"},
			{"darwin", "amd64", "amsftp-darwin-amd64", "a"},
			{"darwin", "amd64", "amsftp-darwin-amd64", "b"},
			{"linux", "arm64", "amsftp-linux-arm64", "a"},
			{"linux", "arm64", "amsftp-linux-arm64", "b"},
			{"linux", "amd64", "amsftp-linux-amd64", "a"},
			{"linux", "amd64", "amsftp-linux-amd64", "b"},
		})
	case fuzzProvenanceMatrix:
		return canonicalIncludeMatrix(job, []string{"runner", "arch", "package", "target"}, [][]string{
			{"ubuntu-24.04", "amd64", "./internal/ipc", "FuzzFrameDecoder"},
			{"ubuntu-24.04-arm", "arm64", "./internal/ipc", "FuzzFrameDecoder"},
			{"ubuntu-24.04", "amd64", "./internal/ipc", "FuzzEnvelopeDecode"},
			{"ubuntu-24.04-arm", "arm64", "./internal/ipc", "FuzzEnvelopeDecode"},
			{"ubuntu-24.04", "amd64", "./internal/ipc", "FuzzWireBytes"},
			{"ubuntu-24.04-arm", "arm64", "./internal/ipc", "FuzzWireBytes"},
			{"ubuntu-24.04", "amd64", "./internal/provider/fake", "FuzzNormalizePath"},
			{"ubuntu-24.04-arm", "arm64", "./internal/provider/fake", "FuzzNormalizePath"},
		})
	case concurrencyProvenanceMatrix:
		return canonicalIncludeMatrix(job, []string{"runner", "arch"}, [][]string{
			{"ubuntu-24.04", "amd64"},
			{"ubuntu-24.04-arm", "arm64"},
		})
	default:
		return false
	}
}

func canonicalOSMatrix(job workflowJob) bool {
	if !strategyHasApprovedShape(job.strategy) {
		return false
	}
	matrix := policyYAMLNodeNamed(job.strategy, "matrix")
	if matrix == nil || matrix.kind != policyYAMLMappingNode || len(matrix.mappings) != 1 || matrix.mappings[0].key.value != "os" {
		return false
	}
	values := matrix.mappings[0].value
	expected := []string{"ubuntu-22.04", "ubuntu-24.04", "macos-15", "macos-15-intel"}
	if job.id == "native" {
		expected = []string{"ubuntu-22.04", "ubuntu-24.04", "ubuntu-24.04-arm", "macos-15", "macos-15-intel"}
	}
	if values == nil || values.kind != policyYAMLSequenceNode || len(values.items) != len(expected) {
		return false
	}
	for index, value := range expected {
		item := values.items[index]
		if item.kind != policyYAMLScalarNode || item.scalar.style != policyYAMLPlainScalar || item.scalar.value != value {
			return false
		}
	}
	return true
}

func canonicalIncludeMatrix(job workflowJob, keys []string, rows [][]string) bool {
	if !strategyHasApprovedShape(job.strategy) {
		return false
	}
	matrix := policyYAMLNodeNamed(job.strategy, "matrix")
	if matrix == nil || matrix.kind != policyYAMLMappingNode || len(matrix.mappings) != 1 || matrix.mappings[0].key.value != "include" {
		return false
	}
	include := matrix.mappings[0].value
	if include == nil || include.kind != policyYAMLSequenceNode || len(include.items) != len(rows) {
		return false
	}
	for rowIndex, expected := range rows {
		row := include.items[rowIndex]
		if row.kind != policyYAMLMappingNode || len(row.mappings) != len(keys) || len(expected) != len(keys) {
			return false
		}
		for column, key := range keys {
			mapping := row.mappings[column]
			if mapping.key.value != key || mapping.value == nil || mapping.value.kind != policyYAMLScalarNode ||
				mapping.value.scalar.style != policyYAMLPlainScalar || mapping.value.scalar.value != expected[column] {
				return false
			}
		}
	}
	return true
}

func canonicalRecordStep(step workflowStep, name string, expected []string) bool {
	return canonicalRunStep(step, name, expected)
}

func canonicalRunStep(step workflowStep, name string, expected []string) bool {
	return step.name != nil && step.name.value == name && step.run != nil && stepRunUsesBlockScalar(step) &&
		nodeHasExactKeys(step.node, "name", "run") && stringSlicesEqual(normalizedShellLines(step.run.value), expected)
}

func canonicalPlainRunStep(step workflowStep, name, command string, environment map[string]string) bool {
	keys := []string{"name", "run"}
	if name == "" {
		keys = []string{"run"}
	} else if step.name == nil || step.name.value != name {
		return false
	}
	if len(environment) > 0 {
		keys = append(keys, "env")
	}
	environmentMatches := step.env == nil
	if len(environment) > 0 {
		environmentMatches = mappingHasExactScalars(step.env, environment)
	}
	return step.run != nil && !stepRunUsesBlockScalar(step) && step.run.style == policyYAMLPlainScalar &&
		step.run.value == command && nodeHasExactKeys(step.node, keys...) && environmentMatches
}

func canonicalBuildProducerPrefix(steps []workflowStep) bool {
	if len(steps) != 6 {
		return false
	}
	return stepIsExactCheckout(steps[0]) && stepIsExactCurrentSetupGo(steps[1]) &&
		canonicalPlainRunStep(steps[2], "", `mkdir -p "${{ runner.temp }}/build/${{ matrix.artifact }}"`, nil) &&
		canonicalPlainRunStep(steps[3], "Cross-build", `go build -trimpath -buildvcs=false -o "${{ runner.temp }}/build/${{ matrix.artifact }}/${{ matrix.artifact }}" ./cmd/amsftp`, map[string]string{
			"CGO_ENABLED": "0", "GOOS": "${{ matrix.goos }}", "GOARCH": "${{ matrix.goarch }}",
		}) &&
		canonicalRunStep(steps[4], "Record build metadata", canonicalBuildMetadataLines("build")) &&
		canonicalBuildArtifactUpload(steps[5], "Upload build artifact", "build-${{ matrix.artifact }}", "${{ runner.temp }}/build/${{ matrix.artifact }}")
}

func canonicalFuzzProducerPrefix(steps []workflowStep) bool {
	return len(steps) == 3 && stepIsExactCheckout(steps[0]) && stepIsExactCurrentSetupGo(steps[1]) &&
		canonicalPlainRunStep(
			steps[2],
			"Fuzz one target",
			`go test -run='^$' -fuzz="^${{ matrix.target }}$" -fuzztime=10m "${{ matrix.package }}"`,
			nil,
		)
}

func canonicalConcurrencyProducerPrefix(steps []workflowStep) bool {
	return len(steps) == 3 && stepIsExactCheckout(steps[0]) && stepIsExactCurrentSetupGo(steps[1]) &&
		canonicalPlainRunStep(
			steps[2],
			"Repeat concurrency contracts",
			`go test -count=100 -run='^(TestNthCallFaultIsConsumedOnce|TestDelayUsesManualClock|TestGateHonorsContextCancellation|TestFaultCallSequencesAreLinearized)$' ./internal/provider/fake`,
			nil,
		)
}

func canonicalReproProducerPrefix(steps []workflowStep) bool {
	if len(steps) != 6 {
		return false
	}
	return stepIsExactCheckout(steps[0]) && canonicalCompareSetupGo(steps[1]) &&
		canonicalRunStep(steps[2], "Prepare independent caches", canonicalPrepareReproCacheLines()) &&
		canonicalPlainRunStep(steps[3], "Reproducible build", `go build -trimpath -buildvcs=false -o "${{ runner.temp }}/repro/${{ matrix.artifact }}/${{ matrix.artifact }}" ./cmd/amsftp`, map[string]string{
			"CGO_ENABLED": "0", "GOOS": "${{ matrix.goos }}", "GOARCH": "${{ matrix.goarch }}",
		}) &&
		canonicalRunStep(steps[4], "Record build metadata", canonicalBuildMetadataLines("repro")) &&
		canonicalBuildArtifactUpload(steps[5], "Upload reproducible artifact", "repro-${{ matrix.artifact }}-${{ matrix.replica }}", "${{ runner.temp }}/repro/${{ matrix.artifact }}")
}

func canonicalReproComparisonPrefix(steps []workflowStep, comparisonArtifact string) bool {
	if len(steps) != 5 || comparisonArtifact == "" {
		return false
	}
	return stepIsExactCheckout(steps[0]) && canonicalCompareSetupGo(steps[1]) &&
		canonicalReproArtifactDownload(steps[2]) &&
		canonicalRunStep(steps[3], "Compare independent builds", canonicalReproComparisonLines()) &&
		canonicalComparisonEvidenceUpload(steps[4], comparisonArtifact)
}

func canonicalBuildArtifactUpload(step workflowStep, name, artifactName, path string) bool {
	return step.name != nil && step.name.value == name && step.uses != nil &&
		step.uses.value == "actions/upload-artifact@"+approvedActionCommits["actions/upload-artifact"] &&
		nodeHasExactKeys(step.node, "name", "uses", "with") &&
		mappingHasExactScalars(step.with, map[string]string{
			"name": artifactName, "path": path, "if-no-files-found": "error",
		})
}

func canonicalReproArtifactDownload(step workflowStep) bool {
	return step.name != nil && step.name.value == "Download reproducible artifacts" && step.uses != nil &&
		step.uses.value == "actions/download-artifact@"+approvedActionCommits["actions/download-artifact"] &&
		nodeHasExactKeys(step.node, "name", "uses", "with") &&
		mappingHasExactScalars(step.with, map[string]string{
			"pattern": "repro-*", "path": "${{ runner.temp }}/repro-downloads",
		})
}

func canonicalComparisonEvidenceUpload(step workflowStep, comparisonArtifact string) bool {
	return canonicalBuildArtifactUpload(
		step,
		"Upload comparison evidence",
		comparisonArtifact,
		"${{ runner.temp }}/accepted-sha256.txt",
	)
}

func canonicalUploadStep(step workflowStep, name string) bool {
	return step.name != nil && step.name.value == name && step.uses != nil &&
		step.uses.value == "actions/upload-artifact@"+approvedActionCommits["actions/upload-artifact"] &&
		nodeHasExactKeys(step.node, "name", "uses", "with") &&
		mappingHasExactScalars(step.with, map[string]string{
			"name":              "provenance-${{ env.LEG }}",
			"path":              "${{ runner.temp }}/provenance/${{ env.LEG }}.*",
			"if-no-files-found": "error",
		})
}

func canonicalDownloadStep(step workflowStep) bool {
	return step.name != nil && step.name.value == "Download provenance" && step.uses != nil &&
		step.uses.value == "actions/download-artifact@"+approvedActionCommits["actions/download-artifact"] &&
		nodeHasExactKeys(step.node, "name", "uses", "with") &&
		mappingHasExactScalars(step.with, map[string]string{
			"pattern":        "provenance-*",
			"path":           "${{ runner.temp }}/provenance-downloads",
			"merge-multiple": "true",
		})
}

func canonicalComparisonDownloadStep(step workflowStep, comparisonArtifact string) bool {
	return step.name != nil && step.name.value == "Download reproducibility comparison" && step.uses != nil &&
		step.uses.value == "actions/download-artifact@"+approvedActionCommits["actions/download-artifact"] &&
		nodeHasExactKeys(step.node, "name", "uses", "with") &&
		mappingHasExactScalars(step.with, map[string]string{
			"name": comparisonArtifact,
			"path": "${{ runner.temp }}/reproducibility-comparison",
		})
}

func canonicalCompareSetupGo(step workflowStep) bool {
	return step.uses != nil && step.uses.value == "actions/setup-go@"+approvedActionCommits["actions/setup-go"] &&
		nodeHasExactKeys(step.node, "uses", "with") &&
		mappingHasExactScalars(step.with, map[string]string{
			"go-version-file": "go.mod",
			"cache":           "false",
		})
}

func canonicalPrepareReproCacheLines() []string {
	return []string{
		`set -euo pipefail`,
		`build_cache="${RUNNER_TEMP}/repro-cache/${{ matrix.artifact }}-${{ matrix.replica }}/build"`,
		`module_cache="${RUNNER_TEMP}/repro-cache/${{ matrix.artifact }}-${{ matrix.replica }}/modules"`,
		`mkdir -p "${build_cache}" "${module_cache}" "${RUNNER_TEMP}/repro/${{ matrix.artifact }}"`,
		`printf 'GOCACHE=%s\n' "${build_cache}" >>"${GITHUB_ENV}"`,
		`printf 'GOMODCACHE=%s\n' "${module_cache}" >>"${GITHUB_ENV}"`,
	}
}

func canonicalBuildMetadataLines(root string) []string {
	return []string{
		`set -euo pipefail`,
		fmt.Sprintf(`directory="${RUNNER_TEMP}/%s/${{ matrix.artifact }}"`, root),
		`binary="${directory}/${{ matrix.artifact }}"`,
		`sha256sum "${binary}" | awk '{print $1}' >"${directory}/${{ matrix.artifact }}.sha256"`,
		`go version -m "${binary}" >"${directory}/${{ matrix.artifact }}.version-m"`,
		`test -s "${directory}/${{ matrix.artifact }}.sha256"`,
		`test -s "${directory}/${{ matrix.artifact }}.version-m"`,
	}
}

func canonicalReproComparisonLines() []string {
	return []string{
		`set -euo pipefail`,
		`root="${RUNNER_TEMP}/repro-downloads"`,
		`expected="${RUNNER_TEMP}/expected-repro-artifacts.txt"`,
		`actual="${RUNNER_TEMP}/actual-repro-artifacts.txt"`,
		`comparison="${RUNNER_TEMP}/accepted-sha256.txt"`,
		`: >"${expected}"`,
		`: >"${comparison}"`,
		`for name in \`,
		`  amsftp-darwin-arm64 \`,
		`  amsftp-darwin-amd64 \`,
		`  amsftp-linux-arm64 \`,
		`  amsftp-linux-amd64; do`,
		`  printf 'repro-%s-a\nrepro-%s-b\n' "${name}" "${name}" >>"${expected}"`,
		`  directory_a="${root}/repro-${name}-a"`,
		`  directory_b="${root}/repro-${name}-b"`,
		`  for directory in "${directory_a}" "${directory_b}"; do`,
		`    test "$(find "${directory}" -mindepth 1 -maxdepth 1 -print | wc -l | tr -d '[:space:]')" -eq 3`,
		`    test -f "${directory}/${name}"`,
		`    test -f "${directory}/${name}.sha256"`,
		`    test -s "${directory}/${name}.version-m"`,
		`    grep -Eq '^[0-9a-f]{64}$' "${directory}/${name}.sha256"`,
		`  done`,
		`  saved_a="$(cat "${directory_a}/${name}.sha256")"`,
		`  saved_b="$(cat "${directory_b}/${name}.sha256")"`,
		`  actual_a="$(sha256sum "${directory_a}/${name}" | awk '{print $1}')"`,
		`  actual_b="$(sha256sum "${directory_b}/${name}" | awk '{print $1}')"`,
		`  test "${saved_a}" = "${actual_a}"`,
		`  test "${saved_b}" = "${actual_b}"`,
		`  test "${actual_a}" = "${actual_b}"`,
		`  cmp -s "${directory_a}/${name}" "${directory_b}/${name}"`,
		`  printf '%s=%s\n' "${name}" "${actual_a}" >>"${comparison}"`,
		`done`,
		`find "${root}" -mindepth 1 -maxdepth 1 -type d -exec basename {} \; | LC_ALL=C sort >"${actual}"`,
		`LC_ALL=C sort -o "${expected}" "${expected}"`,
		`cmp -s "${expected}" "${actual}"`,
		`test "$(wc -l <"${comparison}" | tr -d '[:space:]')" -eq 4`,
	}
}

func canonicalRecordLines(kind provenanceRecordKind) []string {
	lines := []string{"set -euo pipefail"}
	switch kind {
	case buildArtifactProvenanceRecord:
		lines = append(lines,
			`directory="${RUNNER_TEMP}/build/${{ matrix.artifact }}"`,
			`binary="${directory}/${{ matrix.artifact }}"`,
		)
	case reproArtifactProvenanceRecord:
		lines = append(lines,
			`directory="${RUNNER_TEMP}/repro/${{ matrix.artifact }}"`,
			`binary="${directory}/${{ matrix.artifact }}"`,
		)
	}
	if kind == buildArtifactProvenanceRecord || kind == reproArtifactProvenanceRecord {
		lines = append(lines,
			`artifact_sha256="$(sha256sum "${binary}" | awk '{print $1}')"`,
			`saved_sha256="$(cat "${directory}/${{ matrix.artifact }}.sha256")"`,
			`test "${artifact_sha256}" = "${saved_sha256}"`,
			`printf '%s\n' "${artifact_sha256}" | grep -Eq '^[0-9a-f]{64}$'`,
		)
	}
	lines = append(lines,
		`mkdir -p "${RUNNER_TEMP}/provenance"`,
		`porcelain="${RUNNER_TEMP}/provenance/${LEG}.porcelain"`,
		`record="${RUNNER_TEMP}/provenance/${LEG}.env"`,
		`: "${RUNNER_OS:?RUNNER_OS is required}"`,
		`: "${RUNNER_ARCH:?RUNNER_ARCH is required}"`,
		`: "${ImageOS:?ImageOS is required}"`,
		`: "${ImageVersion:?ImageVersion is required}"`,
	)
	if kind == buildArtifactProvenanceRecord || kind == reproArtifactProvenanceRecord {
		lines = append(lines,
			`go_env_goos="$(GOOS='${{ matrix.goos }}' GOARCH='${{ matrix.goarch }}' go env GOOS)"`,
			`go_env_goarch="$(GOOS='${{ matrix.goos }}' GOARCH='${{ matrix.goarch }}' go env GOARCH)"`,
		)
	} else {
		lines = append(lines,
			`go_env_goos="$(go env GOOS)"`,
			`go_env_goarch="$(go env GOARCH)"`,
		)
	}
	lines = append(lines,
		`test -n "${go_env_goos}"`,
		`test -n "${go_env_goarch}"`,
		`git status --porcelain=v1 -uall >"${porcelain}"`,
		`{`,
		`  printf 'schema=amsftp-provenance-v1\n'`,
		`  printf 'workflow=%s\n' "${GITHUB_WORKFLOW}"`,
		`  printf 'job=%s\n' "${GITHUB_JOB}"`,
		`  printf 'leg=%s\n' "${LEG}"`,
		`  printf 'head=%s\n' "$(git rev-parse HEAD)"`,
		`  printf 'tree=%s\n' "$(git rev-parse 'HEAD^{tree}')"`,
		`  printf 'go_version=%s\n' "$(go version)"`,
		`  printf 'runner_os=%s\n' "${RUNNER_OS}"`,
		`  printf 'runner_arch=%s\n' "${RUNNER_ARCH}"`,
		`  printf 'runner_image_os=%s\n' "${ImageOS}"`,
		`  printf 'runner_image_version=%s\n' "${ImageVersion}"`,
		`  printf 'go_env_goos=%s\n' "${go_env_goos}"`,
		`  printf 'go_env_goarch=%s\n' "${go_env_goarch}"`,
	)
	if kind == buildArtifactProvenanceRecord || kind == reproArtifactProvenanceRecord {
		lines = append(lines,
			`  printf 'target_goos=%s\n' '${{ matrix.goos }}'`,
			`  printf 'target_goarch=%s\n' '${{ matrix.goarch }}'`,
			`  printf 'artifact=%s\n' '${{ matrix.artifact }}'`,
			`  printf 'artifact_sha256=%s\n' "${artifact_sha256}"`,
		)
	}
	return append(lines,
		`  if test -s "${porcelain}"; then`,
		`    printf 'status=dirty\n'`,
		`  else`,
		`    printf 'status=clean\n'`,
		`  fi`,
		`} >"${record}"`,
		`test ! -s "${porcelain}"`,
	)
}

func canonicalComparisonRecordLines() []string {
	lines := canonicalRecordLines(standardProvenanceRecord)
	insertAt := len(lines) - 7
	identity := []string{
		`  printf 'workflow_ref=%s\n' "${GITHUB_WORKFLOW_REF}"`,
		`  printf 'workflow_sha=%s\n' "${GITHUB_WORKFLOW_SHA}"`,
		`  printf 'event=%s\n' "${GITHUB_EVENT_NAME}"`,
		`  printf 'run_id=%s\n' "${GITHUB_RUN_ID}"`,
		`  printf 'run_attempt=%s\n' "${GITHUB_RUN_ATTEMPT}"`,
	}
	result := make([]string, 0, len(lines)+len(identity))
	result = append(result, lines[:insertAt]...)
	result = append(result, identity...)
	result = append(result, lines[insertAt:]...)
	return result
}

func canonicalVerificationLines(profile provenanceWorkflowProfile) []string {
	lines := []string{
		`set -euo pipefail`,
		`root="${RUNNER_TEMP}/provenance-downloads"`,
		`manifest="${RUNNER_TEMP}/expected-provenance.txt"`,
		`actual_env="${RUNNER_TEMP}/actual-provenance-env.txt"`,
		`actual_porcelain="${RUNNER_TEMP}/actual-provenance-porcelain.txt"`,
		`accepted_root="${RUNNER_TEMP}/reproducibility-comparison"`,
		`accepted="${accepted_root}/accepted-sha256.txt"`,
		`bindings="${RUNNER_TEMP}/expected-artifact-bindings.txt"`,
		`cat >"${manifest}" <<'LEGS'`,
	}
	lines = append(lines, profile.expectedLegs...)
	lines = append(lines,
		`LEGS`,
		fmt.Sprintf(`test "$(wc -l <"${manifest}" | tr -d '[:space:]')" -eq %d`, len(profile.expectedLegs)),
		`find "${root}" -mindepth 1 -maxdepth 1 -type f -name '*.env' -exec basename {} .env \; | LC_ALL=C sort >"${actual_env}"`,
		`find "${root}" -mindepth 1 -maxdepth 1 -type f -name '*.porcelain' -exec basename {} .porcelain \; | LC_ALL=C sort >"${actual_porcelain}"`,
		`LC_ALL=C sort -o "${manifest}" "${manifest}"`,
		`cmp -s "${manifest}" "${actual_env}"`,
		`cmp -s "${manifest}" "${actual_porcelain}"`,
		fmt.Sprintf(`test "$(find "${root}" -mindepth 1 -print | wc -l | tr -d '[:space:]')" -eq %d`, profile.fileCount),
		`cat >"${bindings}" <<'BINDINGS'`,
	)
	for _, group := range profile.artifactGroups {
		lines = append(lines, strings.Join([]string{group.artifact, group.goos, group.goarch, group.buildLeg, group.reproALeg, group.reproBLeg}, "|"))
	}
	lines = append(lines,
		`BINDINGS`,
		fmt.Sprintf(`test "$(wc -l <"${bindings}" | tr -d '[:space:]')" -eq %d`, len(profile.artifactGroups)),
		`test "$(find "${accepted_root}" -mindepth 1 -print | wc -l | tr -d '[:space:]')" -eq 1`,
		`test -f "${accepted}"`,
		`test ! -L "${accepted}"`,
		fmt.Sprintf(`test "$(wc -l <"${accepted}" | tr -d '[:space:]')" -eq %d`, len(profile.artifactGroups)),
		`expected_head="$(git rev-parse HEAD)"`,
		`expected_tree="$(git rev-parse 'HEAD^{tree}')"`,
		`field_value() {`,
		`  key="$1"`,
		`  file="$2"`,
		`  count="$(grep -c "^${key}=" "${file}" || true)"`,
		`  if test "${count}" -ne 1; then`,
		`    printf 'provenance field %s occurs %s times in %s\n' "${key}" "${count}" "${file}" >&2`,
		`    return 1`,
		`  fi`,
		`  sed -n "s/^${key}=//p" "${file}"`,
		`}`,
		`while IFS= read -r leg; do`,
		`  record="${root}/${leg}.env"`,
		`  porcelain="${root}/${leg}.porcelain"`,
		`  test -f "${record}"`,
		`  test -f "${porcelain}"`,
		`  test ! -s "${porcelain}"`,
		`  expected_fields=14`,
		`  case "${leg}" in`,
		`    build-*|repro-*) expected_fields=18 ;;`,
		`  esac`,
		`  test "$(wc -l <"${record}" | tr -d '[:space:]')" -eq "${expected_fields}"`,
		`  test "$(field_value schema "${record}")" = amsftp-provenance-v1`,
		`  test -n "$(field_value workflow "${record}")"`,
		`  test -n "$(field_value job "${record}")"`,
		`  test "$(field_value leg "${record}")" = "${leg}"`,
		`  test "$(field_value head "${record}")" = "${expected_head}"`,
		`  test "$(field_value tree "${record}")" = "${expected_tree}"`,
		`  test -n "$(field_value go_version "${record}")"`,
		`  test -n "$(field_value runner_os "${record}")"`,
		`  test -n "$(field_value runner_arch "${record}")"`,
		`  test -n "$(field_value runner_image_os "${record}")"`,
		`  test -n "$(field_value runner_image_version "${record}")"`,
		`  test -n "$(field_value go_env_goos "${record}")"`,
		`  test -n "$(field_value go_env_goarch "${record}")"`,
		`  test "$(field_value status "${record}")" = clean`,
	)
	if slices.Contains(profile.expectedLegs, "native-ubuntu-24.04-arm") {
		lines = append(lines,
			`  case "${leg}" in`,
			`    native-ubuntu-24.04-arm)`,
			`      test "$(field_value runner_os "${record}")" = Linux`,
			`      test "$(field_value runner_arch "${record}")" = ARM64`,
			`      test "$(field_value go_env_goos "${record}")" = linux`,
			`      test "$(field_value go_env_goarch "${record}")" = arm64`,
			`      ;;`,
			`  esac`,
		)
	}
	lines = append(lines,
		`done <"${manifest}"`,
		`artifact_hash() {`,
		`  leg="$1"`,
		`  expected_artifact="$2"`,
		`  expected_goos="$3"`,
		`  expected_goarch="$4"`,
		`  record="${root}/${leg}.env"`,
		`  test "$(field_value target_goos "${record}")" = "${expected_goos}" || return 1`,
		`  test "$(field_value target_goarch "${record}")" = "${expected_goarch}" || return 1`,
		`  test "$(field_value go_env_goos "${record}")" = "${expected_goos}" || return 1`,
		`  test "$(field_value go_env_goarch "${record}")" = "${expected_goarch}" || return 1`,
		`  test "$(field_value artifact "${record}")" = "${expected_artifact}" || return 1`,
		`  hash="$(field_value artifact_sha256 "${record}")" || return 1`,
		`  printf '%s\n' "${hash}" | grep -Eq '^[0-9a-f]{64}$' || return 1`,
		`  printf '%s\n' "${hash}"`,
		`}`,
		`while IFS='|' read -r artifact target_goos target_goarch build_leg repro_a_leg repro_b_leg; do`,
		`  accepted_hash="$(field_value "${artifact}" "${accepted}")"`,
		`  printf '%s\n' "${accepted_hash}" | grep -Eq '^[0-9a-f]{64}$'`,
		`  repro_a_hash="$(artifact_hash "${repro_a_leg}" "${artifact}" "${target_goos}" "${target_goarch}")"`,
		`  repro_b_hash="$(artifact_hash "${repro_b_leg}" "${artifact}" "${target_goos}" "${target_goarch}")"`,
		`  test "${repro_a_hash}" = "${accepted_hash}"`,
		`  test "${repro_b_hash}" = "${accepted_hash}"`,
		`  if test -n "${build_leg}"; then`,
		`    build_hash="$(artifact_hash "${build_leg}" "${artifact}" "${target_goos}" "${target_goarch}")"`,
		`    test "${build_hash}" = "${accepted_hash}"`,
		`  fi`,
		`done <"${bindings}"`,
	)
	return lines
}

func normalizedShellLines(script string) []string {
	// The YAML parser already removes the block scalar's common content indent.
	// Preserve every content-line byte and discard only one chomped final newline.
	lines := strings.Split(script, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func stringSlicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func jobContainsProvenanceStep(job workflowJob) bool {
	for _, step := range job.steps {
		if stepContainsProvenance(step) {
			return true
		}
	}
	return false
}

func stepContainsProvenance(step workflowStep) bool {
	if step.name != nil && strings.Contains(strings.ToLower(step.name.value), "provenance") {
		return true
	}
	if step.run != nil && (strings.Contains(step.run.value, "amsftp-provenance-v1") || strings.Contains(step.run.value, "/provenance/")) {
		return true
	}
	return nodeContainsScalarFragment(step.with, "provenance-") || nodeContainsScalarFragment(step.with, "/provenance")
}

func nodeContainsScalarFragment(node *policyYAMLNode, fragment string) bool {
	if node == nil {
		return false
	}
	switch node.kind {
	case policyYAMLScalarNode:
		return strings.Contains(node.scalar.value, fragment)
	case policyYAMLMappingNode:
		for _, mapping := range node.mappings {
			if nodeContainsScalarFragment(mapping.value, fragment) {
				return true
			}
		}
	case policyYAMLSequenceNode:
		for _, item := range node.items {
			if nodeContainsScalarFragment(item, fragment) {
				return true
			}
		}
	}
	return false
}
