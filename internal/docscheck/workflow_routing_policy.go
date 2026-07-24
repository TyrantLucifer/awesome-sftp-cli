package docscheck

import (
	"fmt"
	"strings"
)

func checkFastWorkflowRoutingPolicy(doc workflowDoc) []Finding {
	var findings []Finding
	if !fastWorkflowEventsAreExact(doc.on) {
		line := 1
		if doc.on != nil {
			line = doc.on.key.line
		}
		findings = append(findings, Finding{
			Path: doc.path, Line: line, Rule: "workflow.fast_routing",
			Message: "fast ci must run exactly for pull requests and main pushes",
		})
	}

	jobs := make(map[string]workflowJob, len(doc.jobs))
	for _, job := range doc.jobs {
		jobs[job.id] = job
	}
	if _, exists := jobs["changes"]; !exists {
		findings = append(findings, Finding{
			Path: doc.path, Line: 1, Rule: "workflow.fast_job",
			Message: "fast ci must define the changes classifier job",
		})
	}
	required, exists := jobs["required"]
	if !exists {
		findings = append(findings, Finding{
			Path: doc.path, Line: 1, Rule: "workflow.fast_job",
			Message: "fast ci must define the stable required aggregator job",
		})
		return findings
	}
	wantNeeds := []string{"changes", "docs", "quality", "lint", "race", "workflow", "dependencies", "native", "auth", "release"}
	name := policyYAMLScalarNamed(required.node, "name")
	if name == nil || name.value != "required" || !policyYAMLScalarEquals(required.ifExpr, "always()") ||
		!scalarSequenceIsExactSet(required.needs, wantNeeds) {
		findings = append(findings, Finding{
			Path: doc.path, Line: required.line, Rule: "workflow.fast_required",
			Message: fmt.Sprintf("fast ci required job must be named required, use always(), and aggregate exactly %s", strings.Join(wantNeeds, ", ")),
		})
	}
	return findings
}

func fastWorkflowEventsAreExact(on *policyYAMLMapping) bool {
	if on == nil || on.value == nil || on.value.kind != policyYAMLMappingNode || len(on.value.mappings) != 2 {
		return false
	}
	pullRequest := policyYAMLNodeNamed(on.value, "pull_request")
	push := policyYAMLNodeNamed(on.value, "push")
	return eventFiltersAreExact(pullRequest, map[string][]string{"branches-ignore": {"release/**"}}) &&
		eventFiltersAreExact(push, map[string][]string{"branches": {"main"}})
}

func eventFiltersAreExact(node *policyYAMLNode, filters map[string][]string) bool {
	if node == nil || node.kind != policyYAMLMappingNode || len(node.mappings) != len(filters) {
		return false
	}
	for name, values := range filters {
		if !scalarSequenceIsExactSet(policyYAMLNodeNamed(node, name), values) {
			return false
		}
	}
	return true
}

func scalarSequenceIsExactSet(node *policyYAMLNode, values []string) bool {
	if node == nil || node.kind != policyYAMLSequenceNode || len(node.items) != len(values) {
		return false
	}
	want := make(map[string]struct{}, len(values))
	for _, value := range values {
		want[value] = struct{}{}
	}
	for _, item := range node.items {
		if item == nil || item.kind != policyYAMLScalarNode {
			return false
		}
		if _, exists := want[item.scalar.value]; !exists {
			return false
		}
		delete(want, item.scalar.value)
	}
	return len(want) == 0
}
