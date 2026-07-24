package docscheck

const setupGoCacheDependencyPath = "go.sum\ntools/go.sum"

func stepIsExactCheckout(step workflowStep) bool {
	if step.uses == nil || step.uses.value != "actions/checkout@"+approvedActionCommits["actions/checkout"] ||
		!stepHasOnlyKeys(step, "name", "uses", "with") {
		return false
	}
	return mappingHasExactScalars(step.with, map[string]string{"persist-credentials": "false"}) ||
		mappingHasExactScalars(step.with, map[string]string{"fetch-depth": "0", "persist-credentials": "false"})
}

func stepIsExactCurrentSetupGo(step workflowStep) bool {
	return step.uses != nil && step.uses.value == "actions/setup-go@"+approvedActionCommits["actions/setup-go"] &&
		stepHasOnlyKeys(step, "name", "uses", "with") &&
		mappingHasExactScalars(step.with, map[string]string{
			"go-version":            "1.26.5",
			"cache":                 "true",
			"cache-dependency-path": setupGoCacheDependencyPath,
		})
}

func mappingHasExactScalars(node *policyYAMLNode, want map[string]string) bool {
	if node == nil || node.kind != policyYAMLMappingNode || len(node.mappings) != len(want) {
		return false
	}
	for _, mapping := range node.mappings {
		value, exists := want[mapping.key.value]
		if !exists || mapping.value == nil || mapping.value.kind != policyYAMLScalarNode ||
			mapping.value.scalar.value != value {
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

func strategyHasApprovedShape(strategy *policyYAMLNode) bool {
	if strategy == nil || strategy.kind != policyYAMLMappingNode || len(strategy.mappings) != 2 {
		return false
	}
	failFast := policyYAMLNodeNamed(strategy, "fail-fast")
	matrix := policyYAMLNodeNamed(strategy, "matrix")
	return failFast != nil && failFast.kind == policyYAMLScalarNode &&
		failFast.scalar.style == policyYAMLPlainScalar && failFast.scalar.value == "false" && matrix != nil
}

func stepRunUsesBlockScalar(step workflowStep) bool {
	run := policyYAMLMappingNamed(step.node, "run")
	return run != nil && run.value != nil && run.value.blockScalar
}
