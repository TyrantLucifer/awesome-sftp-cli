package docscheck

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var actionCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

type yamlTokenKind uint8

const (
	yamlScalar yamlTokenKind = iota
	yamlColon
	yamlComma
	yamlFlowMapStart
	yamlFlowMapEnd
	yamlFlowSequenceStart
	yamlFlowSequenceEnd
)

type yamlToken struct {
	kind  yamlTokenKind
	value string
}

type yamlMapping struct {
	key   string
	value []yamlToken
	depth int
}

type yamlNodeKind uint8

const (
	yamlEmptyNode yamlNodeKind = iota
	yamlScalarNode
	yamlMappingNode
	yamlSequenceNode
)

type yamlNode struct {
	kind     yamlNodeKind
	scalar   string
	mappings []yamlNodeMapping
	items    []yamlNode
}

type yamlNodeMapping struct {
	key   string
	value yamlNode
}

type yamlNodeParser struct {
	tokens []yamlToken
	index  int
}

type workflowBlockFrame struct {
	indent int
	key    string
	step   *workflowStepState
}

type workflowStepState struct {
	action              string
	pendingPersistLines []int
}

func checkWorkflows(root string) []Finding {
	const workflowDirectory = ".github/workflows"
	var findings []Finding
	for _, name := range []string{"ci.yml", "fast-ci.yml", "nightly.yml"} {
		path := filepath.ToSlash(filepath.Join(workflowDirectory, name))
		if !requiredWorkflowIsRegular(root, path) {
			findings = append(findings, Finding{Path: path, Line: 1, Rule: "workflow.required", Message: "required workflow file is missing or is not a regular file"})
		}
	}

	workflowRoot, err := resolveRepositoryPath(root, workflowDirectory)
	if err != nil {
		return findings
	}
	info, err := os.Stat(workflowRoot)
	if err != nil || !info.IsDir() {
		return findings
	}
	entries, err := os.ReadDir(workflowRoot)
	if err != nil {
		return append(findings, readFailure(workflowDirectory, err))
	}

	var paths []string
	for _, entry := range entries {
		extension := strings.ToLower(filepath.Ext(entry.Name()))
		if entry.Type().IsRegular() && (extension == ".yml" || extension == ".yaml") {
			paths = append(paths, filepath.ToSlash(filepath.Join(".github", "workflows", entry.Name())))
		}
	}
	sort.Strings(paths)

	for _, path := range paths {
		lines, err := readLines(root, path)
		if err != nil {
			findings = append(findings, readFailure(path, err))
			continue
		}
		findings = append(findings, checkWorkflowPolicy(path, lines)...)
	}
	return findings
}

func requiredWorkflowIsRegular(root, relativePath string) bool {
	target := filepath.Clean(filepath.Join(root, filepath.FromSlash(relativePath)))
	if filepath.IsAbs(relativePath) || !pathWithin(root, target) {
		return false
	}

	current := root
	parts := strings.Split(filepath.Clean(filepath.FromSlash(relativePath)), string(filepath.Separator))
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return false
		}
		if index == len(parts)-1 {
			return info.Mode().IsRegular()
		}
		if !info.IsDir() {
			return false
		}
	}
	return false
}

func checkWorkflowLines(path string, lines []string) []Finding {
	var findings []Finding
	var stack []workflowBlockFrame
	reported := make(map[string]bool)
	addFinding := func(line int, rule, message string) {
		findingKey := fmt.Sprintf("%d:%s", line, rule)
		if reported[findingKey] {
			return
		}
		reported[findingKey] = true
		findings = append(findings, Finding{Path: path, Line: line, Rule: rule, Message: message})
	}

	for index, line := range lines {
		lineNumber := index + 1
		tokens := lexYAMLLine(line)
		if len(tokens) == 0 {
			continue
		}
		indent := leadingSpaces(line)
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}

		if isBlockSequenceLine(line) {
			sequencePath := workflowBlockPath(stack)
			var step *workflowStepState
			if isWorkflowStepsPath(sequencePath) {
				step = &workflowStepState{}
			}
			stack = append(stack, workflowBlockFrame{indent: indent, key: "[]", step: step})
		}

		blockPath := workflowBlockPath(stack)
		for _, mapping := range yamlMappings(tokens) {
			if mapping.depth != 0 {
				continue
			}
			value := parseYAMLNode(mapping.value)
			step := workflowBlockStep(stack)
			checkWorkflowMapping(blockPath, mapping.key, value, lineNumber, step, addFinding)
			walkWorkflowNode(appendWorkflowPath(blockPath, mapping.key), value, lineNumber, step, addFinding)
			if len(mapping.value) == 0 {
				stack = append(stack, workflowBlockFrame{indent: blockMappingIndent(line), key: mapping.key})
			}
			break
		}
	}
	return findings
}

func checkWorkflowMapping(path []string, key string, value yamlNode, line int, step *workflowStepState, addFinding func(int, string, string)) {
	if len(path) == 0 && key == "on" && yamlNodeScalarEquals(value, "pull_request_target") {
		addFinding(line, "workflow.pull_request_target", "pull_request_target is prohibited")
	}
	if isWorkflowOnPath(path) && key == "pull_request_target" {
		addFinding(line, "workflow.pull_request_target", "pull_request_target is prohibited")
	}

	if (len(path) == 0 || isWorkflowJobPath(path)) && key == "permissions" && yamlNodeScalarEqualsAny(value, "write", "write-all") {
		addFinding(line, "workflow.permissions_write", "workflow permissions must not grant write access")
	}
	if isWorkflowPermissionsPath(path) && yamlNodeScalarEqualsAny(value, "write", "write-all") {
		addFinding(line, "workflow.permissions_write", fmt.Sprintf("permission %q grants write access", key))
	}

	if key == "uses" && (isWorkflowJobPath(path) || isWorkflowStepPath(path)) {
		action, ok := yamlNodeScalar(value)
		if !ok {
			return
		}
		if isWorkflowStepPath(path) && step != nil {
			step.action = action
			if isCheckoutAction(action) {
				for _, pendingLine := range step.pendingPersistLines {
					addFinding(pendingLine, "workflow.persist_credentials", "checkout credentials must not persist")
				}
			}
			step.pendingPersistLines = nil
		}
		if !isPinnedAction(action) {
			addFinding(line, "workflow.action_pin", fmt.Sprintf("action %q must use a 40-character lowercase commit SHA", action))
		}
	}

	if key == "persist-credentials" && isWorkflowStepWithPath(path) && yamlNodeScalarEquals(value, "true") && step != nil {
		switch {
		case isCheckoutAction(step.action):
			addFinding(line, "workflow.persist_credentials", "checkout credentials must not persist")
		case step.action == "":
			step.pendingPersistLines = append(step.pendingPersistLines, line)
		}
	}
}

func walkWorkflowNode(path []string, node yamlNode, line int, step *workflowStepState, addFinding func(int, string, string)) {
	switch node.kind {
	case yamlScalarNode:
		if isWorkflowOnSequencePath(path) && node.scalar == "pull_request_target" {
			addFinding(line, "workflow.pull_request_target", "pull_request_target is prohibited")
		}
	case yamlMappingNode:
		for _, mapping := range node.mappings {
			checkWorkflowMapping(path, mapping.key, mapping.value, line, step, addFinding)
			walkWorkflowNode(appendWorkflowPath(path, mapping.key), mapping.value, line, step, addFinding)
		}
	case yamlSequenceNode:
		for _, item := range node.items {
			itemPath := appendWorkflowPath(path, "[]")
			itemStep := step
			if isWorkflowStepsPath(path) {
				itemStep = &workflowStepState{}
			}
			walkWorkflowNode(itemPath, item, line, itemStep, addFinding)
		}
	}
}

func appendWorkflowPath(path []string, element string) []string {
	result := make([]string, len(path), len(path)+1)
	copy(result, path)
	return append(result, element)
}

func workflowBlockPath(stack []workflowBlockFrame) []string {
	path := make([]string, 0, len(stack))
	for _, frame := range stack {
		path = append(path, frame.key)
	}
	return path
}

func workflowBlockStep(stack []workflowBlockFrame) *workflowStepState {
	for index := len(stack) - 1; index >= 0; index-- {
		if stack[index].step != nil {
			return stack[index].step
		}
	}
	return nil
}

func isBlockSequenceLine(line string) bool {
	trimmed := strings.TrimLeft(line, " ")
	return trimmed == "-" || strings.HasPrefix(trimmed, "- ")
}

func blockMappingIndent(line string) int {
	indent := leadingSpaces(line)
	if indent >= len(line) || line[indent] != '-' {
		return indent
	}
	index := indent + 1
	for index < len(line) && line[index] == ' ' {
		index++
	}
	return index
}

func isWorkflowOnPath(path []string) bool {
	return len(path) == 1 && path[0] == "on" ||
		len(path) == 2 && path[0] == "on" && path[1] == "[]"
}

func isWorkflowOnSequencePath(path []string) bool {
	return len(path) == 2 && path[0] == "on" && path[1] == "[]"
}

func isWorkflowPermissionsPath(path []string) bool {
	return len(path) == 1 && path[0] == "permissions" ||
		len(path) == 3 && path[0] == "jobs" && path[2] == "permissions"
}

func isWorkflowJobPath(path []string) bool {
	return len(path) == 2 && path[0] == "jobs"
}

func isWorkflowStepsPath(path []string) bool {
	return len(path) == 3 && path[0] == "jobs" && path[2] == "steps"
}

func isWorkflowStepPath(path []string) bool {
	return len(path) == 4 && isWorkflowStepsPath(path[:3]) && path[3] == "[]"
}

func isWorkflowStepWithPath(path []string) bool {
	return len(path) == 5 && isWorkflowStepPath(path[:4]) && path[4] == "with"
}

func isCheckoutAction(action string) bool {
	separator := strings.LastIndexByte(action, '@')
	if separator >= 0 {
		action = action[:separator]
	}
	return strings.EqualFold(action, "actions/checkout")
}

func isPinnedAction(action string) bool {
	if strings.HasPrefix(action, "./") {
		return true
	}
	if strings.HasPrefix(action, "docker://") {
		return false
	}
	separator := strings.LastIndexByte(action, '@')
	return separator >= 1 && actionCommitPattern.MatchString(action[separator+1:])
}

func lexYAMLLine(line string) []yamlToken {
	var tokens []yamlToken
	for index := 0; index < len(line); {
		if isYAMLWhitespace(line[index]) {
			index++
			continue
		}
		if line[index] == '#' {
			break
		}
		if line[index] == '-' && index+1 < len(line) && isYAMLWhitespace(line[index+1]) {
			index++
			continue
		}

		switch line[index] {
		case '\'', '"':
			value, next := scanYAMLQuotedScalar(line, index)
			tokens = append(tokens, yamlToken{kind: yamlScalar, value: value})
			index = next
		case ':':
			if isYAMLMappingColon(line, index) {
				tokens = append(tokens, yamlToken{kind: yamlColon})
				index++
				continue
			}
			value, next := scanYAMLPlainScalar(line, index)
			tokens = append(tokens, yamlToken{kind: yamlScalar, value: value})
			index = next
		case ',':
			tokens = append(tokens, yamlToken{kind: yamlComma})
			index++
		case '{':
			tokens = append(tokens, yamlToken{kind: yamlFlowMapStart})
			index++
		case '}':
			tokens = append(tokens, yamlToken{kind: yamlFlowMapEnd})
			index++
		case '[':
			tokens = append(tokens, yamlToken{kind: yamlFlowSequenceStart})
			index++
		case ']':
			tokens = append(tokens, yamlToken{kind: yamlFlowSequenceEnd})
			index++
		default:
			value, next := scanYAMLPlainScalar(line, index)
			if next == index {
				index++
				continue
			}
			tokens = append(tokens, yamlToken{kind: yamlScalar, value: value})
			index = next
		}
	}
	return tokens
}

func scanYAMLQuotedScalar(line string, start int) (string, int) {
	quote := line[start]
	if quote == '\'' {
		var value strings.Builder
		for index := start + 1; index < len(line); index++ {
			if line[index] != '\'' {
				value.WriteByte(line[index])
				continue
			}
			if index+1 < len(line) && line[index+1] == '\'' {
				value.WriteByte('\'')
				index++
				continue
			}
			return value.String(), index + 1
		}
		return value.String(), len(line)
	}

	escaped := false
	for index := start + 1; index < len(line); index++ {
		if escaped {
			escaped = false
			continue
		}
		if line[index] == '\\' {
			escaped = true
			continue
		}
		if line[index] != '"' {
			continue
		}
		raw := line[start : index+1]
		value, err := strconv.Unquote(raw)
		if err != nil {
			return raw[1 : len(raw)-1], index + 1
		}
		return value, index + 1
	}
	return line[start+1:], len(line)
}

func scanYAMLPlainScalar(line string, start int) (string, int) {
	index := start
	for index < len(line) {
		character := line[index]
		if isYAMLWhitespace(character) || strings.ContainsRune(",{}[]", rune(character)) {
			break
		}
		if character == ':' && isYAMLMappingColon(line, index) {
			break
		}
		index++
	}
	return line[start:index], index
}

func isYAMLWhitespace(character byte) bool {
	return character == ' ' || character == '\t' || character == '\r' || character == '\n'
}

func isYAMLMappingColon(line string, index int) bool {
	if index+1 == len(line) {
		return true
	}
	next := line[index+1]
	return isYAMLWhitespace(next) || strings.ContainsRune(",{}[]", rune(next))
}

func parseYAMLNode(tokens []yamlToken) yamlNode {
	parser := yamlNodeParser{tokens: tokens}
	return parser.parseNode()
}

func (parser *yamlNodeParser) parseNode() yamlNode {
	if parser.index >= len(parser.tokens) {
		return yamlNode{}
	}

	switch parser.tokens[parser.index].kind {
	case yamlScalar:
		value := parser.tokens[parser.index].value
		parser.index++
		return yamlNode{kind: yamlScalarNode, scalar: value}
	case yamlFlowMapStart:
		return parser.parseMappingNode()
	case yamlFlowSequenceStart:
		return parser.parseSequenceNode()
	default:
		parser.index++
		return yamlNode{}
	}
}

func (parser *yamlNodeParser) parseMappingNode() yamlNode {
	parser.index++
	node := yamlNode{kind: yamlMappingNode}
	for parser.index < len(parser.tokens) && parser.tokens[parser.index].kind != yamlFlowMapEnd {
		if parser.tokens[parser.index].kind == yamlComma {
			parser.index++
			continue
		}
		if parser.index+1 >= len(parser.tokens) || parser.tokens[parser.index].kind != yamlScalar || parser.tokens[parser.index+1].kind != yamlColon {
			parser.index++
			continue
		}

		key := parser.tokens[parser.index].value
		parser.index += 2
		value := yamlNode{}
		if parser.index < len(parser.tokens) && parser.tokens[parser.index].kind != yamlComma && parser.tokens[parser.index].kind != yamlFlowMapEnd {
			value = parser.parseNode()
		}
		node.mappings = append(node.mappings, yamlNodeMapping{key: key, value: value})
	}
	if parser.index < len(parser.tokens) && parser.tokens[parser.index].kind == yamlFlowMapEnd {
		parser.index++
	}
	return node
}

func (parser *yamlNodeParser) parseSequenceNode() yamlNode {
	parser.index++
	node := yamlNode{kind: yamlSequenceNode}
	for parser.index < len(parser.tokens) && parser.tokens[parser.index].kind != yamlFlowSequenceEnd {
		if parser.tokens[parser.index].kind == yamlComma {
			parser.index++
			continue
		}
		node.items = append(node.items, parser.parseNode())
	}
	if parser.index < len(parser.tokens) && parser.tokens[parser.index].kind == yamlFlowSequenceEnd {
		parser.index++
	}
	return node
}

func yamlNodeScalar(node yamlNode) (string, bool) {
	return node.scalar, node.kind == yamlScalarNode
}

func yamlNodeScalarEquals(node yamlNode, value string) bool {
	scalar, ok := yamlNodeScalar(node)
	return ok && strings.EqualFold(scalar, value)
}

func yamlNodeScalarEqualsAny(node yamlNode, values ...string) bool {
	scalar, ok := yamlNodeScalar(node)
	if !ok {
		return false
	}
	for _, value := range values {
		if strings.EqualFold(scalar, value) {
			return true
		}
	}
	return false
}

func yamlMappings(tokens []yamlToken) []yamlMapping {
	var mappings []yamlMapping
	for index := 0; index+1 < len(tokens); index++ {
		if tokens[index].kind != yamlScalar || tokens[index+1].kind != yamlColon {
			continue
		}
		mappings = append(mappings, yamlMapping{
			key:   tokens[index].value,
			value: yamlMappingValue(tokens, index+1),
			depth: yamlDepthBefore(tokens, index+1),
		})
	}
	return mappings
}

func yamlMappingValue(tokens []yamlToken, colon int) []yamlToken {
	startDepth := yamlDepthBefore(tokens, colon)
	depth := startDepth
	start := colon + 1
	for index := start; index < len(tokens); index++ {
		switch tokens[index].kind {
		case yamlFlowMapStart, yamlFlowSequenceStart:
			depth++
		case yamlFlowMapEnd, yamlFlowSequenceEnd:
			if depth == startDepth {
				return tokens[start:index]
			}
			depth--
		case yamlComma:
			if depth == startDepth {
				return tokens[start:index]
			}
		}
	}
	return tokens[start:]
}

func yamlDepthBefore(tokens []yamlToken, end int) int {
	depth := 0
	for _, token := range tokens[:end] {
		switch token.kind {
		case yamlFlowMapStart, yamlFlowSequenceStart:
			depth++
		case yamlFlowMapEnd, yamlFlowSequenceEnd:
			depth--
		}
	}
	return depth
}

func leadingSpaces(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}
