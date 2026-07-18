package docscheck

import (
	"fmt"
	"strings"
)

const setupGoCacheDependencyPath = "go.sum\ntools/go.sum"

type shellCommand struct {
	words []string
}

type safeGoBuildCommand struct {
	output           string
	trimpath         bool
	buildVCSDisabled bool
}

type nativePrefixCredit struct {
	fmtCheck     bool
	vet          bool
	test         bool
	testContract bool
	testRace     bool
	build        bool
	help         bool
	version      bool
}

func checkCIWorkflowPolicy(doc workflowDoc) []Finding {
	var findings []Finding
	add := func(line int, rule, message string) {
		findings = append(findings, Finding{Path: doc.path, Line: line, Rule: rule, Message: message})
	}

	required := []string{"quality", "native", "oldstable", "build"}
	jobs := make(map[string]workflowJob, len(doc.jobs))
	for _, job := range doc.jobs {
		jobs[job.id] = job
	}
	for _, id := range required {
		job, exists := jobs[id]
		if !exists {
			add(1, "workflow.ci_job", fmt.Sprintf("ci workflow must define job %q", id))
			continue
		}
		checkCIRequiredJobExecution(job, add)
	}
	if job, exists := jobs["quality"]; exists {
		checkCIQuality(job, add)
	}
	if job, exists := jobs["native"]; exists {
		checkCINative(job, add)
	}
	if job, exists := jobs["oldstable"]; exists {
		checkCIOldstable(job, add)
	}
	if job, exists := jobs["build"]; exists {
		checkCIBuild(job, add)
	}
	return findings
}

func checkCIRequiredJobExecution(job workflowJob, add func(int, string, string)) {
	if !jobHasApprovedExecutionShape(job) {
		add(job.line, "workflow.ci_job_context", fmt.Sprintf("required ci job %q must use only the approved job-level execution shape", job.id))
	}
	if job.ifExpr != nil {
		add(job.line, "workflow.ci_job_condition", fmt.Sprintf("required ci job %q must not define job-level if", job.id))
	}
	if job.needs != nil {
		add(job.line, "workflow.ci_job_needs", fmt.Sprintf("required ci job %q must be independent and must not define needs", job.id))
	}
	for _, step := range job.steps {
		if step.run == nil {
			continue
		}
		if strings.Contains(step.run.value, "GITHUB_ENV") || strings.Contains(step.run.value, "GITHUB_PATH") {
			add(job.line, "workflow.ci_environment_mutation", fmt.Sprintf("required ci job %q must not reference GITHUB_ENV or GITHUB_PATH in run steps", job.id))
			return
		}
	}
}

func checkCIQuality(job workflowJob, add func(int, string, string)) {
	valid := job.runsOn != nil && job.runsOn.value == "ubuntu-24.04" &&
		jobHasTrustedQualityPrefix(job)
	if !valid {
		add(job.line, "workflow.ci_quality", "quality job must run on ubuntu-24.04 and execute make check and make supply-chain")
	}
	if !qualityLifecycleUsesTrustedPersistentRoot(job) {
		add(job.line, "workflow.ci_quality_lifecycle_root", "quality installed lifecycle must place persistent HOME beneath the prepared owner-private test root")
	}
	if !qualityLifecycleProvesPinnedCacheUpgradeRecovery(job) {
		add(job.line, "workflow.ci_quality_pinned_cache", "quality installed lifecycle must preserve one frozen Stage 5 pinned cache identity across failed-upgrade recovery and rollback")
	}
}

func qualityLifecycleUsesTrustedPersistentRoot(job workflowJob) bool {
	for _, step := range job.steps {
		if step.name == nil || step.name.value != "Exercise deterministic public packaging and clean-home lifecycle" {
			continue
		}
		if step.run == nil {
			return false
		}
		script := step.run.value
		return strings.Contains(script, `trusted_root="/var/lib/amsftp-tests/$(id -u)"`) &&
			strings.Contains(script, `clean_home="${trusted_root}/public-package-home"`) &&
			!strings.Contains(script, `clean_home="${RUNNER_TEMP}`)
	}
	return true
}

func qualityLifecycleProvesPinnedCacheUpgradeRecovery(job workflowJob) bool {
	for _, step := range job.steps {
		if step.name == nil || step.name.value != "Exercise deterministic public packaging and clean-home lifecycle" {
			continue
		}
		if step.run == nil {
			return false
		}
		script := step.run.value
		ordered := []string{
			`old_cache_harness="${RUNNER_TEMP}/pinned-cache-stage5"`,
			`current_cache_harness="${RUNNER_TEMP}/pinned-cache-current"`,
			`failure_harness="${RUNNER_TEMP}/pinned-cache-failure"`,
			`cp -R internal/integration/pinned-cache-lifecycle "${old_source}/internal/integration/"`,
			`-o "${old_cache_harness}" ./internal/integration/pinned-cache-lifecycle`,
			`-o "${current_cache_harness}" ./internal/integration/pinned-cache-lifecycle`,
			`-o "${failure_harness}" ./internal/integration/pinned-cache-failure`,
			`"${old_cache_harness}" seed`,
			`"${old_cache_harness}" verify`,
			`"${current_cache_harness}" verify`,
			`"${failure_harness}" prepare`,
			`"${current_cache_harness}" verify`,
			`"${installed}" daemon`,
			`grep -F 'durable transfer service is unavailable'`,
			`"${installed}" daemon --resume-migration`,
			`"${current_cache_harness}" verify`,
			`"${old_binary}" daemon`,
			`"${current_cache_harness}" verify`,
		}
		cursor := 0
		for _, fragment := range ordered {
			relative := strings.Index(script[cursor:], fragment)
			if relative < 0 {
				return false
			}
			cursor += relative + len(fragment)
		}
		return true
	}
	return true
}

func checkCINative(job workflowJob, add func(int, string, string)) {
	if !jobHasExactOSMatrix(job) {
		add(job.line, "workflow.ci_native_matrix", "native job must run exactly the ubuntu-22.04, ubuntu-24.04, macos-15, and macos-15-intel matrix via matrix.os")
	}
	credit := trustedNativePrefixCredit(job)
	requirements := []struct {
		name  string
		found bool
	}{
		{name: "make fmt-check", found: credit.fmtCheck},
		{name: "make vet", found: credit.vet},
		{name: "make test", found: credit.test},
		{name: "make test-contract", found: credit.testContract},
		{name: "make test-race", found: credit.testRace},
		{name: "go build ./cmd/amsftp to runner.temp", found: credit.build},
		{name: "runner.temp binary --help", found: credit.help},
		{name: "runner.temp binary --version", found: credit.version},
	}
	for _, requirement := range requirements {
		if !requirement.found {
			add(job.line, "workflow.ci_native_command", fmt.Sprintf("native job is missing unconditional command %q", requirement.name))
		}
	}
	if !nativeLifecycleIsExact(job) {
		add(job.line, "workflow.ci_native_lifecycle", "native job must exercise the exact owner-private clean install, daemon, state-preserving uninstall lifecycle")
	}
}

func nativeLifecycleIsExact(job workflowJob) bool {
	offset := trustedPersistentTestRootOffset(job.steps)
	index := 11 + offset
	if len(job.steps) <= index || !jobHasApprovedExecutionShape(job) || !jobEnvironmentIsTrusted(job, "native-${{ matrix.os }}", "") {
		return false
	}
	step := job.steps[index]
	if step.name == nil || step.name.value != "Exercise native clean install and uninstall lifecycle" ||
		step.run == nil || step.ifExpr != nil || !stepRunUsesBlockScalar(step) || !stepHasSafeRunContext(job, step) ||
		!stepHasOnlyKeys(step, "name", "run") {
		return false
	}
	script := step.run.value
	ordered := []string{
		`set -euo pipefail`,
		`native_binary="${RUNNER_TEMP}/native/bin/amsftp"`,
		`install_root="${RUNNER_TEMP}/native/install"`,
		`prefix="${install_root}/prefix"`,
		`installed="${prefix}/bin/amsftp"`,
		`trusted_root="/var/lib/amsftp-tests/$(id -u)"`,
		`trusted_root="${HOME}/.amsftp-native-${GITHUB_RUN_ID}-${GITHUB_RUN_ATTEMPT}"`,
		`clean_home="${trusted_root}/clean-home"`,
		`test ! -e "${install_root}"`,
		`test ! -e "${clean_home}"`,
		`mkdir -p`,
		`"${prefix}/bin"`,
		`"${prefix}/share/man/man1"`,
		`"${prefix}/share/bash-completion/completions"`,
		`"${prefix}/share/zsh/site-functions"`,
		`"${prefix}/share/fish/vendor_completions.d"`,
		`"${clean_home}/config"`,
		`"${clean_home}/state"`,
		`"${clean_home}/cache"`,
		`"${clean_home}/runtime"`,
		`chmod 0700 "${clean_home}" "${clean_home}/config" "${clean_home}/state" "${clean_home}/cache" "${clean_home}/runtime"`,
		`install -m 0755 "${native_binary}" "${installed}"`,
		`install -m 0644 docs/man/amsftp.1 "${prefix}/share/man/man1/amsftp.1"`,
		`"${installed}" completion bash >"${prefix}/share/bash-completion/completions/amsftp"`,
		`"${installed}" completion zsh >"${prefix}/share/zsh/site-functions/_amsftp"`,
		`"${installed}" completion fish >"${prefix}/share/fish/vendor_completions.d/amsftp.fish"`,
		`test "$("${native_binary}" --version)" = "$("${installed}" --version)"`,
		`"HOME=${clean_home}"`,
		`"XDG_CONFIG_HOME=${clean_home}/config"`,
		`"XDG_STATE_HOME=${clean_home}/state"`,
		`"XDG_CACHE_HOME=${clean_home}/cache"`,
		`"XDG_RUNTIME_DIR=${clean_home}/runtime"`,
		`"TMPDIR=${clean_home}/runtime"`,
		`trap cleanup_daemon EXIT`,
		`env "${clean_env[@]}" "${installed}" daemon start --format json | grep -F '"running":true'`,
		`env "${clean_env[@]}" "${installed}" job list --format json | grep -F '"output_version":1'`,
		`env "${clean_env[@]}" "${installed}" daemon status --format json | grep -F '"running":true'`,
		`env "${clean_env[@]}" "${installed}" daemon stop --confirm stop --format json | grep -F '"running":false'`,
		`env "${clean_env[@]}" "${installed}" daemon status --format json | grep -F '"running":false'`,
		`database="${clean_home}/state/amsftp/amsftp.db"`,
		`database="${clean_home}/Library/Application Support/io.github.tyrantlucifer.amsftp/state/amsftp.db"`,
		`test -f "${database}"`,
		`rm -f`,
		`"${installed}"`,
		`"${prefix}/share/man/man1/amsftp.1"`,
		`"${prefix}/share/bash-completion/completions/amsftp"`,
		`"${prefix}/share/zsh/site-functions/_amsftp"`,
		`"${prefix}/share/fish/vendor_completions.d/amsftp.fish"`,
		`test ! -e "${installed}"`,
		`test -f "${database}"`,
		`trap - EXIT`,
	}
	cursor := 0
	for _, fragment := range ordered {
		relative := strings.Index(script[cursor:], fragment)
		if relative < 0 {
			return false
		}
		cursor += relative + len(fragment)
	}
	return strings.Count(script, `test -f "${database}"`) == 2 &&
		!strings.Contains(script, `clean_home="${RUNNER_TEMP}`) &&
		!strings.Contains(script, "rm -rf") && !strings.Contains(script, "rm --recursive")
}

func checkCIOldstable(job workflowJob, add func(int, string, string)) {
	if !jobHasExactOSMatrix(job) {
		add(job.line, "workflow.ci_oldstable_matrix", "oldstable job must run exactly the ubuntu-22.04, ubuntu-24.04, macos-15, and macos-15-intel matrix via matrix.os")
	}
	if !jobHasTrustedOldstablePrefix(job) {
		add(job.line, "workflow.ci_oldstable_toolchain", "oldstable job must select Go 1.25.12 with actions/setup-go")
	}
	if value := policyYAMLScalarNamed(job.env, "GOTOOLCHAIN"); value == nil || value.value != "local" {
		add(job.line, "workflow.ci_oldstable_local", "oldstable job must set job-level GOTOOLCHAIN to local")
	}
	if !jobHasTrustedOldstablePrefix(job) {
		add(job.line, "workflow.ci_oldstable_check", "oldstable job must execute unconditional make check")
	}
}

func checkCIBuild(job workflowJob, add func(int, string, string)) {
	if job.runsOn == nil || job.runsOn.value != "ubuntu-24.04" || !jobHasExactBuildMatrix(job) {
		add(job.line, "workflow.ci_build_matrix", "build job matrix must contain exactly the four approved GOOS/GOARCH/artifact tuples")
	}
	if !jobHasTrustedBuildPrefix(job) {
		add(job.line, "workflow.ci_build_command", "build job must cross-build each matrix tuple with CGO_ENABLED=0, -trimpath, and -buildvcs=false into runner.temp")
	}
}

func jobHasTrustedQualityPrefix(job workflowJob) bool {
	if len(job.steps) < 6 || !jobHasApprovedExecutionShape(job) || !jobEnvironmentIsTrusted(job, "quality", "") ||
		!stepIsExactCheckout(job.steps[0]) || !stepIsExactCurrentSetupGo(job.steps[1]) {
		return false
	}
	offset := trustedPersistentTestRootOffset(job.steps)
	if len(job.steps) < 6+offset {
		return false
	}
	for index, target := range []string{"check", "lint", "fuzz-smoke", "supply-chain"} {
		step := job.steps[index+2+offset]
		if !stepExecutesMakeTarget(job, step, target) || !stepHasTrustedMakeEnvironment(job, step, "quality", target) {
			return false
		}
	}
	return true
}

func trustedNativePrefixCredit(job workflowJob) nativePrefixCredit {
	const (
		nativeDirectory = "${{ runner.temp }}/native/bin"
		nativeOutput    = nativeDirectory + "/amsftp"
	)
	var credit nativePrefixCredit
	if len(job.steps) < 11 || !jobHasApprovedExecutionShape(job) || !jobEnvironmentIsTrusted(job, "native-${{ matrix.os }}", "") ||
		!stepIsExactCheckout(job.steps[0]) || !stepIsExactCurrentSetupGo(job.steps[1]) {
		return credit
	}
	offset := trustedPersistentTestRootOffset(job.steps)
	if len(job.steps) < 11+offset {
		return credit
	}

	makeCredits := []*bool{&credit.fmtCheck, &credit.vet, &credit.test, &credit.testContract, &credit.testRace}
	for index, target := range []string{"fmt-check", "vet", "test", "test-contract", "test-race"} {
		step := job.steps[index+2+offset]
		if !stepExecutesMakeTarget(job, step, target) || !stepHasTrustedMakeEnvironment(job, step, "native", target) {
			return credit
		}
		*makeCredits[index] = true
	}

	directory, ok := trustedMkdirPath(job, job.steps[7+offset])
	if !ok || directory != nativeDirectory || !isRunnerTempPath(directory) {
		return credit
	}
	build, ok := trustedGoBuild(job, job.steps[8+offset], false)
	if !ok || !build.trimpath || build.output != nativeOutput || !isRunnerTempPath(build.output) {
		return credit
	}
	credit.build = true
	if !stepExecutesExactBinaryFlag(job, job.steps[9+offset], build.output, "--help") {
		return credit
	}
	credit.help = true
	if !stepExecutesExactBinaryFlag(job, job.steps[10+offset], build.output, "--version") {
		return credit
	}
	credit.version = true
	return credit
}

func jobHasTrustedOldstablePrefix(job workflowJob) bool {
	if len(job.steps) < 3 || !jobHasApprovedExecutionShape(job) || !jobEnvironmentIsTrusted(job, "oldstable-${{ matrix.os }}", "local") ||
		!stepIsExactCheckout(job.steps[0]) || !stepIsExactOldstableSetupGo(job.steps[1]) {
		return false
	}
	offset := trustedPersistentTestRootOffset(job.steps)
	if len(job.steps) < 3+offset {
		return false
	}
	step := job.steps[2+offset]
	return stepExecutesOldstableMakeTarget(job, step, "check") && stepHasTrustedMakeEnvironment(job, step, "oldstable", "check")
}

func jobHasTrustedBuildPrefix(job workflowJob) bool {
	const (
		buildDirectory = "${{ runner.temp }}/build/${{ matrix.artifact }}"
		buildOutput    = buildDirectory + "/${{ matrix.artifact }}"
	)
	if len(job.steps) < 4 || !jobHasApprovedExecutionShape(job) || !jobEnvironmentIsTrusted(job, "build-${{ matrix.artifact }}", "") ||
		!stepIsExactCheckout(job.steps[0]) || !stepIsExactCurrentSetupGo(job.steps[1]) {
		return false
	}
	directory, ok := trustedMkdirPath(job, job.steps[2])
	if !ok {
		return false
	}
	build, ok := trustedGoBuild(job, job.steps[3], true)
	return ok && directory == buildDirectory && build.trimpath && build.output == buildOutput &&
		effectiveEnvValue(job.workflowEnv, job.env, job.steps[3].env, "CGO_ENABLED") == "0" &&
		effectiveEnvValue(job.workflowEnv, job.env, job.steps[3].env, "GOOS") == "${{ matrix.goos }}" &&
		effectiveEnvValue(job.workflowEnv, job.env, job.steps[3].env, "GOARCH") == "${{ matrix.goarch }}"
}

func stepIsExactCheckout(step workflowStep) bool {
	if step.uses == nil || step.uses.value != "actions/checkout@"+approvedActionCommits["actions/checkout"] || !stepHasOnlyKeys(step, "name", "uses", "with") {
		return false
	}
	return mappingHasExactScalars(step.with, map[string]string{"persist-credentials": "false"}) ||
		mappingHasExactScalars(step.with, map[string]string{"fetch-depth": "0", "persist-credentials": "false"})
}

func stepIsExactCurrentSetupGo(step workflowStep) bool {
	return step.uses != nil && step.uses.value == "actions/setup-go@"+approvedActionCommits["actions/setup-go"] &&
		stepHasOnlyKeys(step, "name", "uses", "with") &&
		mappingHasExactScalars(step.with, map[string]string{
			"go-version-file":       "go.mod",
			"cache":                 "true",
			"cache-dependency-path": setupGoCacheDependencyPath,
		})
}

func stepIsExactOldstableSetupGo(step workflowStep) bool {
	return step.uses != nil && step.uses.value == "actions/setup-go@"+approvedActionCommits["actions/setup-go"] &&
		stepHasOnlyKeys(step, "name", "uses", "with") &&
		mappingHasExactScalars(step.with, map[string]string{
			"go-version":            "1.25.12",
			"cache":                 "true",
			"cache-dependency-path": setupGoCacheDependencyPath,
		})
}

func stepPreparesTrustedPersistentTestRoot(step workflowStep) bool {
	const script = `set -euo pipefail
if test "${RUNNER_OS}" = Linux; then
  sudo install -d -o root -g root -m 0755 /var/lib/amsftp-tests
  sudo install -d -o "$(id -u)" -g "$(id -g)" -m 0700 "/var/lib/amsftp-tests/$(id -u)"
fi`
	return step.name != nil && step.name.value == "Prepare trusted persistent test root" &&
		step.run != nil && strings.TrimSpace(step.run.value) == script &&
		stepHasOnlyKeys(step, "name", "run")
}

func trustedPersistentTestRootOffset(steps []workflowStep) int {
	if len(steps) > 2 && stepPreparesTrustedPersistentTestRoot(steps[2]) {
		return 1
	}
	return 0
}

func mappingHasExactScalars(node *policyYAMLNode, want map[string]string) bool {
	if node == nil || node.kind != policyYAMLMappingNode || len(node.mappings) != len(want) {
		return false
	}
	for _, mapping := range node.mappings {
		value, exists := want[mapping.key.value]
		if !exists || mapping.value == nil || mapping.value.kind != policyYAMLScalarNode || mapping.value.scalar.value != value {
			return false
		}
	}
	return true
}

func stepHasOnlyKeys(step workflowStep, allowed ...string) bool {
	if step.node == nil || step.node.kind != policyYAMLMappingNode {
		return false
	}
	want := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		want[key] = struct{}{}
	}
	for _, mapping := range step.node.mappings {
		if _, ok := want[mapping.key.value]; !ok {
			return false
		}
	}
	return true
}

func stepHasTrustedMakeEnvironment(job workflowJob, step workflowStep, jobID, target string) bool {
	allowed := []string{"MAKEFLAGS", "GNUMAKEFLAGS", "MAKEFILES", "GOFLAGS"}
	pathsAllowed := target == "check" || jobID == "native" && (target == "test" || target == "test-contract")
	if pathsAllowed {
		allowed = append(allowed, "BUILD_DIR", "COVERAGE_DIR")
	}
	if jobID == "oldstable" {
		allowed = append(allowed, "GOTOOLCHAIN")
	}
	if !stepHasOnlyKeys(step, "name", "run", "env") || !environmentHasOnlyKeys(step.env, allowed...) ||
		makeEnvironmentChangesExecution(job, step) || goFlagsChangeExecution(job, step) {
		return false
	}
	if jobID == "oldstable" && effectiveEnvValue(job.workflowEnv, job.env, step.env, "GOTOOLCHAIN") != "local" {
		return false
	}
	if !pathsAllowed {
		return true
	}
	return environmentPathMatchesJob(step.env, "BUILD_DIR", jobID, "build") &&
		environmentPathMatchesJob(step.env, "COVERAGE_DIR", jobID, "coverage")
}

func trustedGoBuild(job workflowJob, step workflowStep, cross bool) (safeGoBuildCommand, bool) {
	allowed := []string{"GOFLAGS"}
	if cross {
		allowed = append(allowed, "CGO_ENABLED", "GOOS", "GOARCH")
	}
	if !stepHasOnlyKeys(step, "name", "run", "env") || !environmentHasOnlyKeys(step.env, allowed...) {
		return safeGoBuildCommand{}, false
	}
	command, ok := singleSafeShellCommand(job, step)
	if !ok || goFlagsChangeExecution(job, step) {
		return safeGoBuildCommand{}, false
	}
	build, ok := parseSafeGoBuildCommand(command.words)
	if !ok || build.buildVCSDisabled != cross {
		return safeGoBuildCommand{}, false
	}
	return build, true
}

func trustedMkdirPath(job workflowJob, step workflowStep) (string, bool) {
	if !stepHasOnlyKeys(step, "name", "run") {
		return "", false
	}
	command, ok := singleSafeShellCommand(job, step)
	if !ok || len(command.words) != 3 || command.words[0] != "mkdir" || command.words[1] != "-p" {
		return "", false
	}
	return command.words[2], true
}

func stepExecutesExactBinaryFlag(job workflowJob, step workflowStep, binary, flag string) bool {
	if !stepHasOnlyKeys(step, "name", "run") {
		return false
	}
	command, ok := singleSafeShellCommand(job, step)
	return ok && len(command.words) == 2 && command.words[0] == binary && command.words[1] == flag
}

func environmentHasOnlyKeys(environment *policyYAMLNode, allowed ...string) bool {
	if environment == nil {
		return true
	}
	if environment.kind != policyYAMLMappingNode {
		return false
	}
	want := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		want[key] = struct{}{}
	}
	for _, mapping := range environment.mappings {
		if _, ok := want[mapping.key.value]; !ok || mapping.value == nil ||
			(mapping.value.kind != policyYAMLScalarNode && mapping.value.kind != policyYAMLEmptyNode) {
			return false
		}
	}
	return true
}

func environmentPathMatchesJob(environment *policyYAMLNode, name, jobID, leaf string) bool {
	value, present := environmentValue(environment, name)
	return present && value == "${{ runner.temp }}/"+jobID+"/"+leaf
}

func jobEnvironmentIsTrusted(job workflowJob, leg, toolchain string) bool {
	controls := []string{"MAKEFLAGS", "GNUMAKEFLAGS", "MAKEFILES", "GOFLAGS", "GOTOOLCHAIN"}
	if !environmentHasOnlyKeys(job.workflowEnv, controls...) ||
		!environmentHasOnlyKeys(job.env, append(controls, "LEG")...) {
		return false
	}
	if value, present := environmentValue(job.env, "LEG"); present && value != leg {
		return false
	}
	if toolchain == "" {
		return strings.TrimSpace(effectiveEnvValue(job.workflowEnv, job.env, nil, "GOTOOLCHAIN")) == ""
	}
	return effectiveEnvValue(job.workflowEnv, job.env, nil, "GOTOOLCHAIN") == toolchain
}

func jobHasApprovedExecutionShape(job workflowJob) bool {
	if job.node == nil || job.node.kind != policyYAMLMappingNode {
		return false
	}
	allowed := map[string]struct{}{
		"name":            {},
		"runs-on":         {},
		"timeout-minutes": {},
		"env":             {},
		"steps":           {},
	}
	if job.id != "quality" {
		allowed["strategy"] = struct{}{}
	}
	for _, mapping := range job.node.mappings {
		if _, ok := allowed[mapping.key.value]; !ok {
			return false
		}
	}
	if job.id == "quality" {
		return job.strategy == nil
	}
	return strategyHasApprovedShape(job.strategy)
}

func strategyHasApprovedShape(strategy *policyYAMLNode) bool {
	if strategy == nil || strategy.kind != policyYAMLMappingNode || len(strategy.mappings) != 2 {
		return false
	}
	failFast := policyYAMLNodeNamed(strategy, "fail-fast")
	matrix := policyYAMLNodeNamed(strategy, "matrix")
	return failFast != nil && failFast.kind == policyYAMLScalarNode &&
		failFast.scalar.style == policyYAMLPlainScalar && failFast.scalar.value == "false" && matrix != nil
}

func jobHasExactOSMatrix(job workflowJob) bool {
	if job.runsOn == nil || job.runsOn.style != policyYAMLPlainScalar || job.runsOn.value != "${{ matrix.os }}" ||
		!strategyHasApprovedShape(job.strategy) {
		return false
	}
	matrix := policyYAMLNodeNamed(job.strategy, "matrix")
	if matrix == nil || matrix.kind != policyYAMLMappingNode || len(matrix.mappings) != 1 ||
		matrix.mappings[0].key.style != policyYAMLPlainScalar || matrix.mappings[0].key.value != "os" {
		return false
	}
	osValues := matrix.mappings[0].value
	want := map[string]struct{}{
		"ubuntu-22.04":   {},
		"ubuntu-24.04":   {},
		"macos-15":       {},
		"macos-15-intel": {},
	}
	if osValues == nil || osValues.kind != policyYAMLSequenceNode || len(osValues.items) != len(want) {
		return false
	}
	seen := make(map[string]struct{}, len(want))
	for _, item := range osValues.items {
		if item.kind != policyYAMLScalarNode || item.scalar.style != policyYAMLPlainScalar {
			return false
		}
		if _, approved := want[item.scalar.value]; !approved {
			return false
		}
		if _, duplicate := seen[item.scalar.value]; duplicate {
			return false
		}
		seen[item.scalar.value] = struct{}{}
	}
	return len(seen) == len(want)
}

func jobHasExactBuildMatrix(job workflowJob) bool {
	if !strategyHasApprovedShape(job.strategy) {
		return false
	}
	matrix := policyYAMLNodeNamed(job.strategy, "matrix")
	if matrix == nil || matrix.kind != policyYAMLMappingNode || len(matrix.mappings) != 1 || matrix.mappings[0].key.value != "include" {
		return false
	}
	include := matrix.mappings[0].value
	if include == nil || include.kind != policyYAMLSequenceNode || len(include.items) != 4 {
		return false
	}
	want := map[string]struct{}{
		"darwin\x00arm64\x00amsftp-darwin-arm64": {},
		"darwin\x00amd64\x00amsftp-darwin-amd64": {},
		"linux\x00arm64\x00amsftp-linux-arm64":   {},
		"linux\x00amd64\x00amsftp-linux-amd64":   {},
	}
	seen := make(map[string]struct{}, 4)
	for _, item := range include.items {
		if item.kind != policyYAMLMappingNode || len(item.mappings) != 3 {
			return false
		}
		goos := policyYAMLScalarNamed(item, "goos")
		goarch := policyYAMLScalarNamed(item, "goarch")
		artifact := policyYAMLScalarNamed(item, "artifact")
		if goos == nil || goarch == nil || artifact == nil {
			return false
		}
		tuple := goos.value + "\x00" + goarch.value + "\x00" + artifact.value
		if _, approved := want[tuple]; !approved {
			return false
		}
		if _, duplicate := seen[tuple]; duplicate {
			return false
		}
		seen[tuple] = struct{}{}
	}
	return len(seen) == len(want)
}

func workflowStepHasKey(step workflowStep, key string) bool {
	return step.node != nil && policyYAMLMappingNamed(step.node, key) != nil
}

func jobHasMakeTarget(job workflowJob, target string) bool {
	for _, step := range job.steps {
		if stepExecutesMakeTarget(job, step, target) {
			return true
		}
	}
	return false
}

func stepExecutesOldstableMakeTarget(job workflowJob, step workflowStep, target string) bool {
	return stepExecutesMakeTarget(job, step, target) &&
		effectiveEnvValue(job.workflowEnv, job.env, step.env, "GOTOOLCHAIN") == "local"
}

func stepExecutesMakeTarget(job workflowJob, step workflowStep, target string) bool {
	if makeEnvironmentChangesExecution(job, step) {
		return false
	}
	command, ok := singleSafeShellCommand(job, step)
	if !ok {
		return false
	}
	words := command.words
	return len(words) == 2 && words[0] == "make" && words[1] == target
}

func makeEnvironmentChangesExecution(job workflowJob, step workflowStep) bool {
	for _, name := range []string{"MAKEFLAGS", "GNUMAKEFLAGS", "MAKEFILES"} {
		if strings.TrimSpace(effectiveEnvValue(job.workflowEnv, job.env, step.env, name)) != "" {
			return true
		}
	}
	return false
}

func goFlagsChangeExecution(job workflowJob, step workflowStep) bool {
	return strings.TrimSpace(effectiveEnvValue(job.workflowEnv, job.env, step.env, "GOFLAGS")) != ""
}

func isRunnerTempPath(value string) bool {
	const prefix = "${{ runner.temp }}/"
	if !strings.HasPrefix(value, prefix) || len(value) == len(prefix) || strings.ContainsRune(value, '\\') {
		return false
	}
	suffix := strings.TrimPrefix(value, prefix)
	if strings.ContainsRune(suffix, '$') {
		return false
	}
	for _, segment := range strings.Split(suffix, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func parseSafeGoBuildCommand(words []string) (safeGoBuildCommand, bool) {
	if len(words) < 4 || words[0] != "go" || words[1] != "build" {
		return safeGoBuildCommand{}, false
	}

	var result safeGoBuildCommand
	outputSeen := false
	targetSeen := false
	for index := 2; index < len(words); index++ {
		switch word := words[index]; {
		case word == "-trimpath":
			if result.trimpath {
				return safeGoBuildCommand{}, false
			}
			result.trimpath = true
		case word == "-buildvcs=false":
			if result.buildVCSDisabled {
				return safeGoBuildCommand{}, false
			}
			result.buildVCSDisabled = true
		case word == "-o":
			if outputSeen || index+1 >= len(words) || words[index+1] == "" {
				return safeGoBuildCommand{}, false
			}
			outputSeen = true
			index++
			result.output = words[index]
		case strings.HasPrefix(word, "-o="):
			if outputSeen || len(word) == len("-o=") {
				return safeGoBuildCommand{}, false
			}
			outputSeen = true
			result.output = strings.TrimPrefix(word, "-o=")
		case word == "./cmd/amsftp":
			if targetSeen {
				return safeGoBuildCommand{}, false
			}
			targetSeen = true
		default:
			return safeGoBuildCommand{}, false
		}
	}
	return result, outputSeen && targetSeen
}

func singleSafeShellCommand(job workflowJob, step workflowStep) (shellCommand, bool) {
	if step.ifExpr != nil || step.run == nil || stepRunUsesBlockScalar(step) || !stepHasSafeRunContext(job, step) {
		return shellCommand{}, false
	}
	if shellScriptHasUnsafeControl(step.run.value) {
		return shellCommand{}, false
	}
	segments := splitShellSegments(step.run.value)
	if len(segments) != 1 {
		return shellCommand{}, false
	}
	words, ok := shellWords(segments[0])
	if !ok || len(words) == 0 {
		return shellCommand{}, false
	}
	return shellCommand{words: words}, true
}

func stepRunUsesBlockScalar(step workflowStep) bool {
	run := policyYAMLMappingNamed(step.node, "run")
	return run != nil && run.value != nil && run.value.blockScalar
}

func stepHasSafeRunContext(job workflowJob, step workflowStep) bool {
	if workflowStepHasKey(step, "shell") || workflowStepHasKey(step, "working-directory") {
		return false
	}
	return defaultsHaveSafeRunContext(job.workflowDefaults) && defaultsHaveSafeRunContext(job.defaults)
}

func defaultsHaveSafeRunContext(defaults *policyYAMLNode) bool {
	if defaults == nil {
		return true
	}
	if defaults.kind != policyYAMLMappingNode {
		return false
	}
	run := policyYAMLNodeNamed(defaults, "run")
	if run == nil {
		return true
	}
	if run.kind != policyYAMLMappingNode {
		return false
	}
	return policyYAMLMappingNamed(run, "shell") == nil && policyYAMLMappingNamed(run, "working-directory") == nil
}

func shellScriptHasUnsafeControl(script string) bool {
	quote := byte(0)
	escaped := false
	for index := 0; index < len(script); index++ {
		character := script[index]
		if quote == '\'' {
			if character == '\'' {
				quote = 0
			}
			continue
		}
		if escaped {
			escaped = false
			continue
		}
		if character == '\\' {
			escaped = true
			continue
		}
		if quote == '"' {
			if character == '"' {
				quote = 0
				continue
			}
			if character == '`' || character == '$' && index+1 < len(script) && script[index+1] == '(' {
				return true
			}
			continue
		}
		if character == '\'' || character == '"' {
			quote = character
			continue
		}
		if character == '$' && index+1 < len(script) && script[index+1] == '(' {
			return true
		}
		switch character {
		case '\n', '\r', ';', '|', '&', '`', '(', ')':
			return true
		}
	}
	return quote != 0 || escaped
}

func effectiveEnvValue(workflowEnv, jobEnv, stepEnv *policyYAMLNode, name string) string {
	for _, environment := range []*policyYAMLNode{stepEnv, jobEnv, workflowEnv} {
		if value, present := environmentValue(environment, name); present {
			return value
		}
	}
	return ""
}

func environmentValue(environment *policyYAMLNode, name string) (string, bool) {
	mapping := policyYAMLMappingNamed(environment, name)
	if mapping == nil || mapping.value == nil {
		return "", false
	}
	switch mapping.value.kind {
	case policyYAMLEmptyNode:
		return "", true
	case policyYAMLScalarNode:
		return mapping.value.scalar.value, true
	default:
		return "<non-scalar environment value>", true
	}
}

func splitShellSegments(script string) []string {
	var segments []string
	start := 0
	quote := byte(0)
	escaped := false
	for index := 0; index < len(script); index++ {
		character := script[index]
		if quote != 0 {
			if quote == '"' && escaped {
				escaped = false
				continue
			}
			if quote == '"' && character == '\\' {
				escaped = true
				continue
			}
			if character == quote {
				quote = 0
			}
			continue
		}
		if character == '\'' || character == '"' {
			quote = character
			continue
		}
		if character == '#' && (index == 0 || isYAMLWhitespace(script[index-1])) {
			lineEnd := strings.IndexByte(script[index:], '\n')
			if lineEnd < 0 {
				script = script[:index]
				break
			}
			script = script[:index] + script[index+lineEnd:]
			index--
			continue
		}
		separatorLength := 0
		switch {
		case character == '\n' || character == ';':
			separatorLength = 1
		case index+1 < len(script) && (script[index:index+2] == "&&" || script[index:index+2] == "||"):
			separatorLength = 2
		}
		if separatorLength == 0 {
			continue
		}
		segments = append(segments, strings.TrimSpace(script[start:index]))
		index += separatorLength - 1
		start = index + 1
	}
	if start <= len(script) {
		segments = append(segments, strings.TrimSpace(script[start:]))
	}
	return segments
}

func shellWords(command string) ([]string, bool) {
	var words []string
	var current strings.Builder
	quote := byte(0)
	escaped := false
	flush := func() {
		if current.Len() > 0 {
			words = append(words, current.String())
			current.Reset()
		}
	}
	for index := 0; index < len(command); index++ {
		character := command[index]
		if quote != 0 {
			if quote == '"' && escaped {
				current.WriteByte(character)
				escaped = false
				continue
			}
			if quote == '"' && character == '\\' {
				escaped = true
				continue
			}
			if character == quote {
				quote = 0
				continue
			}
			current.WriteByte(character)
			continue
		}
		if character == '\'' || character == '"' {
			quote = character
			continue
		}
		if character == '\\' && index+1 < len(command) {
			index++
			current.WriteByte(command[index])
			continue
		}
		if isYAMLWhitespace(character) {
			flush()
			continue
		}
		current.WriteByte(character)
	}
	if quote != 0 || escaped {
		return nil, false
	}
	flush()
	return words, true
}
