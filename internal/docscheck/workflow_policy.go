package docscheck

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const workflowSyntaxMessage = "workflow policy cannot safely interpret this YAML construct"

var positiveDecimalPattern = regexp.MustCompile(`^[1-9][0-9]*$`)

var approvedActionCommits = map[string]string{
	"actions/checkout":          "9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0",
	"actions/setup-go":          "924ae3a1cded613372ab5595356fb5720e22ba16",
	"actions/upload-artifact":   "043fb46d1a93c77aae656e7c1c64a875d1fc6a0a",
	"actions/download-artifact": "3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c",
}

type policyYAMLKind uint8

const (
	policyYAMLEmptyNode policyYAMLKind = iota
	policyYAMLScalarNode
	policyYAMLMappingNode
	policyYAMLSequenceNode
)

type policyYAMLScalarStyle uint8

const (
	policyYAMLPlainScalar policyYAMLScalarStyle = iota
	policyYAMLSingleQuotedScalar
	policyYAMLDoubleQuotedScalar
	policyYAMLBlockScalar
)

type policyYAMLScalar struct {
	value string
	line  int
	style policyYAMLScalarStyle
}

type policyYAMLNode struct {
	kind        policyYAMLKind
	line        int
	scalar      policyYAMLScalar
	mappings    []policyYAMLMapping
	items       []*policyYAMLNode
	blockScalar bool
}

type policyYAMLMapping struct {
	key   policyYAMLScalar
	value *policyYAMLNode
}

type policyYAMLLine struct {
	index  int
	number int
	indent int
	text   string
}

type policyYAMLError struct {
	line int
}

type policyYAMLParser struct {
	lines []string
	index int
}

type policyFlowParser struct {
	text  string
	line  int
	index int
}

type workflowDoc struct {
	path           string
	on             *policyYAMLMapping
	topPermissions *policyYAMLMapping
	env            *policyYAMLNode
	defaults       *policyYAMLNode
	jobs           []workflowJob
}

type workflowJob struct {
	id               string
	line             int
	node             *policyYAMLNode
	runsOn           *policyYAMLScalar
	timeout          *policyYAMLScalar
	strategy         *policyYAMLNode
	workflowEnv      *policyYAMLNode
	env              *policyYAMLNode
	workflowDefaults *policyYAMLNode
	defaults         *policyYAMLNode
	ifExpr           *policyYAMLNode
	needs            *policyYAMLNode
	steps            []workflowStep
}

type workflowStep struct {
	line    int
	node    *policyYAMLNode
	name    *policyYAMLScalar
	uses    *policyYAMLScalar
	with    *policyYAMLNode
	env     *policyYAMLNode
	ifExpr  *policyYAMLScalar
	run     *policyYAMLScalar
	runLine int
}

func checkWorkflowPolicy(path string, lines []string) []Finding {
	root, parseErr := parsePolicyYAML(lines)
	if parseErr != nil {
		return []Finding{{Path: path, Line: parseErr.line, Rule: "workflow.syntax", Message: workflowSyntaxMessage}}
	}
	doc := collectWorkflowDoc(path, root)
	findings := checkGenericWorkflowPolicy(doc)
	if path == ".github/workflows/ci.yml" || path == ".github/workflows/nightly.yml" {
		findings = append(findings, checkWorkflowProvenancePolicy(doc)...)
	}
	if path == ".github/workflows/ci.yml" {
		findings = append(findings, checkCIWorkflowPolicy(doc)...)
	}
	return deduplicateWorkflowFindings(findings)
}

func parsePolicyYAML(lines []string) (*policyYAMLNode, *policyYAMLError) {
	parser := policyYAMLParser{lines: lines}
	line, parseErr := parser.peekSignificant()
	if parseErr != nil {
		return nil, parseErr
	}
	if line == nil {
		return &policyYAMLNode{kind: policyYAMLMappingNode, line: 1}, nil
	}
	if line.indent != 0 {
		return nil, &policyYAMLError{line: line.number}
	}
	root, parseErr := parser.parseBlock(0)
	if parseErr != nil {
		return nil, parseErr
	}
	remaining, parseErr := parser.peekSignificant()
	if parseErr != nil {
		return nil, parseErr
	}
	if remaining != nil {
		return nil, &policyYAMLError{line: remaining.number}
	}
	return root, nil
}

func (parser *policyYAMLParser) parseBlock(indent int) (*policyYAMLNode, *policyYAMLError) {
	line, parseErr := parser.peekSignificant()
	if parseErr != nil {
		return nil, parseErr
	}
	if line == nil || line.indent != indent {
		lineNumber := 1
		if line != nil {
			lineNumber = line.number
		}
		return nil, &policyYAMLError{line: lineNumber}
	}
	if policySequenceText(line.text) != "" || strings.TrimSpace(line.text) == "-" {
		return parser.parseBlockSequence(indent)
	}
	return parser.parseBlockMapping(indent)
}

func (parser *policyYAMLParser) parseBlockMapping(indent int) (*policyYAMLNode, *policyYAMLError) {
	node := &policyYAMLNode{kind: policyYAMLMappingNode}
	seen := make(map[string]struct{})
	for {
		line, parseErr := parser.peekSignificant()
		if parseErr != nil {
			return nil, parseErr
		}
		if line == nil || line.indent < indent {
			break
		}
		if line.indent > indent || policySequenceText(line.text) != "" || strings.TrimSpace(line.text) == "-" {
			return nil, &policyYAMLError{line: line.number}
		}
		key, rest, ok := splitPolicyMapping(line.text)
		if !ok {
			return nil, &policyYAMLError{line: line.number}
		}
		keyValue, ok := decodePolicyKey(key)
		if !ok || unsafePolicyKey(keyValue) {
			return nil, &policyYAMLError{line: line.number}
		}
		if _, duplicate := seen[keyValue]; duplicate {
			return nil, &policyYAMLError{line: line.number}
		}
		seen[keyValue] = struct{}{}
		parser.index = line.index + 1
		value, parseErr := parser.parseMappingValue(keyValue, rest, indent, line.number)
		if parseErr != nil {
			return nil, parseErr
		}
		if node.line == 0 {
			node.line = line.number
		}
		node.mappings = append(node.mappings, policyYAMLMapping{
			key:   policyYAMLScalar{value: keyValue, line: line.number},
			value: value,
		})
	}
	return node, nil
}

func (parser *policyYAMLParser) parseBlockSequence(indent int) (*policyYAMLNode, *policyYAMLError) {
	node := &policyYAMLNode{kind: policyYAMLSequenceNode}
	for {
		line, parseErr := parser.peekSignificant()
		if parseErr != nil {
			return nil, parseErr
		}
		if line == nil || line.indent < indent {
			break
		}
		if line.indent != indent {
			return nil, &policyYAMLError{line: line.number}
		}
		rest := policySequenceText(line.text)
		if rest == "" && strings.TrimSpace(line.text) != "-" {
			break
		}
		parser.index = line.index + 1
		if node.line == 0 {
			node.line = line.number
		}

		if strings.TrimSpace(rest) == "" {
			next, parseErr := parser.peekSignificant()
			if parseErr != nil {
				return nil, parseErr
			}
			if next == nil || next.indent <= indent {
				node.items = append(node.items, &policyYAMLNode{kind: policyYAMLEmptyNode, line: line.number})
				continue
			}
			item, parseErr := parser.parseBlock(next.indent)
			if parseErr != nil {
				return nil, parseErr
			}
			node.items = append(node.items, item)
			continue
		}

		key, valueText, mapping := splitPolicyMapping(rest)
		if !mapping {
			item, parseErr := parsePolicyValue(rest, line.number, false)
			if parseErr != nil {
				return nil, parseErr
			}
			node.items = append(node.items, item)
			continue
		}
		itemIndent := line.indent + strings.Index(line.text, strings.TrimLeft(line.text, " ")) + 2
		item, parseErr := parser.parseSequenceMappingItem(itemIndent, line.number, key, valueText)
		if parseErr != nil {
			return nil, parseErr
		}
		node.items = append(node.items, item)
	}
	return node, nil
}

func (parser *policyYAMLParser) parseSequenceMappingItem(indent, lineNumber int, firstKey, firstValue string) (*policyYAMLNode, *policyYAMLError) {
	node := &policyYAMLNode{kind: policyYAMLMappingNode, line: lineNumber}
	seen := make(map[string]struct{})
	appendEntry := func(keyText, valueText string, entryLine int) *policyYAMLError {
		key, ok := decodePolicyKey(keyText)
		if !ok || unsafePolicyKey(key) {
			return &policyYAMLError{line: entryLine}
		}
		if _, duplicate := seen[key]; duplicate {
			return &policyYAMLError{line: entryLine}
		}
		seen[key] = struct{}{}
		value, parseErr := parser.parseMappingValue(key, valueText, indent, entryLine)
		if parseErr != nil {
			return parseErr
		}
		node.mappings = append(node.mappings, policyYAMLMapping{
			key:   policyYAMLScalar{value: key, line: entryLine},
			value: value,
		})
		return nil
	}
	if parseErr := appendEntry(firstKey, firstValue, lineNumber); parseErr != nil {
		return nil, parseErr
	}
	for {
		line, parseErr := parser.peekSignificant()
		if parseErr != nil {
			return nil, parseErr
		}
		if line == nil || line.indent < indent {
			break
		}
		if line.indent > indent {
			return nil, &policyYAMLError{line: line.number}
		}
		if policySequenceText(line.text) != "" || strings.TrimSpace(line.text) == "-" {
			break
		}
		key, valueText, ok := splitPolicyMapping(line.text)
		if !ok {
			return nil, &policyYAMLError{line: line.number}
		}
		parser.index = line.index + 1
		if parseErr := appendEntry(key, valueText, line.number); parseErr != nil {
			return nil, parseErr
		}
	}
	return node, nil
}

func (parser *policyYAMLParser) parseMappingValue(key, text string, indent, line int) (*policyYAMLNode, *policyYAMLError) {
	trimmed := strings.TrimSpace(text)
	if isPolicyBlockScalarIndicator(trimmed) {
		if key != "run" {
			return nil, &policyYAMLError{line: line}
		}
		return parser.parseRunBlockScalar(indent, line, trimmed), nil
	}
	if trimmed != "" {
		return parsePolicyValue(trimmed, line, key == "run")
	}
	next, parseErr := parser.peekSignificant()
	if parseErr != nil {
		return nil, parseErr
	}
	if next == nil || next.indent <= indent {
		return &policyYAMLNode{kind: policyYAMLEmptyNode, line: line}, nil
	}
	return parser.parseBlock(next.indent)
}

func (parser *policyYAMLParser) parseRunBlockScalar(parentIndent, line int, indicator string) *policyYAMLNode {
	start := parser.index
	end := start
	minimumIndent := -1
	for end < len(parser.lines) {
		raw := strings.TrimSuffix(parser.lines[end], "\r")
		if strings.TrimSpace(raw) == "" {
			end++
			continue
		}
		indent := leadingSpaces(raw)
		if indent <= parentIndent {
			break
		}
		if minimumIndent < 0 || indent < minimumIndent {
			minimumIndent = indent
		}
		end++
	}
	if minimumIndent < 0 {
		minimumIndent = parentIndent + 1
	}
	parts := make([]string, 0, end-start)
	for index := start; index < end; index++ {
		raw := strings.TrimSuffix(parser.lines[index], "\r")
		if strings.TrimSpace(raw) == "" {
			parts = append(parts, "")
			continue
		}
		if len(raw) >= minimumIndent {
			raw = raw[minimumIndent:]
		}
		parts = append(parts, raw)
	}
	parser.index = end
	separator := "\n"
	if strings.HasPrefix(indicator, ">") {
		separator = " "
	}
	return &policyYAMLNode{
		kind:        policyYAMLScalarNode,
		line:        line,
		scalar:      policyYAMLScalar{value: strings.Join(parts, separator), line: line, style: policyYAMLBlockScalar},
		blockScalar: true,
	}
}

func (parser *policyYAMLParser) peekSignificant() (*policyYAMLLine, *policyYAMLError) {
	for index := parser.index; index < len(parser.lines); index++ {
		raw := strings.TrimSuffix(parser.lines[index], "\r")
		indent := 0
		for indent < len(raw) && (raw[indent] == ' ' || raw[indent] == '\t') {
			if raw[indent] == '\t' {
				return nil, &policyYAMLError{line: index + 1}
			}
			indent++
		}
		text, ok := sanitizePolicyYAMLLine(raw[indent:])
		if !ok {
			return nil, &policyYAMLError{line: index + 1}
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		return &policyYAMLLine{index: index, number: index + 1, indent: indent, text: strings.TrimSpace(text)}, nil
	}
	return nil, nil
}

func sanitizePolicyYAMLLine(text string) (string, bool) {
	quote := byte(0)
	escaped := false
	flow := make([]byte, 0, 4)
	for index := 0; index < len(text); index++ {
		character := text[index]
		if quote != 0 {
			if quote == '\'' && character == '\'' && index+1 < len(text) && text[index+1] == '\'' {
				index++
				continue
			}
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
		if strings.HasPrefix(text[index:], "${{") {
			closing := strings.Index(text[index+3:], "}}")
			if closing < 0 {
				return "", false
			}
			index += 3 + closing + 1
			continue
		}
		if character == '#' && (index == 0 || isYAMLWhitespace(text[index-1])) {
			text = text[:index]
			break
		}
		switch character {
		case '{', '[':
			flow = append(flow, character)
		case '}':
			if len(flow) == 0 || flow[len(flow)-1] != '{' {
				return "", false
			}
			flow = flow[:len(flow)-1]
		case ']':
			if len(flow) == 0 || flow[len(flow)-1] != '[' {
				return "", false
			}
			flow = flow[:len(flow)-1]
		}
	}
	return text, quote == 0 && len(flow) == 0
}

func splitPolicyMapping(text string) (string, string, bool) {
	quote := byte(0)
	escaped := false
	flowDepth := 0
	for index := 0; index < len(text); index++ {
		character := text[index]
		if quote != 0 {
			if quote == '\'' && character == '\'' && index+1 < len(text) && text[index+1] == '\'' {
				index++
				continue
			}
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
		if strings.HasPrefix(text[index:], "${{") {
			closing := strings.Index(text[index+3:], "}}")
			if closing < 0 {
				return "", "", false
			}
			index += 3 + closing + 1
			continue
		}
		switch character {
		case '{', '[':
			flowDepth++
		case '}', ']':
			flowDepth--
		case ':':
			if flowDepth == 0 && (index+1 == len(text) || isYAMLWhitespace(text[index+1]) || strings.ContainsRune("{[", rune(text[index+1]))) {
				return strings.TrimSpace(text[:index]), strings.TrimSpace(text[index+1:]), true
			}
		}
	}
	return "", "", false
}

func policySequenceText(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "-" {
		return ""
	}
	if strings.HasPrefix(trimmed, "- ") {
		return strings.TrimSpace(trimmed[2:])
	}
	return ""
}

func decodePolicyKey(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false
	}
	if trimmed[0] != '\'' && trimmed[0] != '"' {
		return trimmed, !strings.ContainsAny(trimmed, " \t")
	}
	value, end, ok := parsePolicyQuotedScalar(trimmed, 0)
	return value, ok && strings.TrimSpace(trimmed[end:]) == ""
}

func parsePolicyValue(text string, line int, allowShell bool) (*policyYAMLNode, *policyYAMLError) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return &policyYAMLNode{kind: policyYAMLEmptyNode, line: line}, nil
	}
	if trimmed[0] == '{' || trimmed[0] == '[' || trimmed[0] == '\'' || trimmed[0] == '"' {
		parser := policyFlowParser{text: trimmed, line: line}
		node, ok := parser.parseValue("")
		parser.skipSpace()
		if !ok || parser.index != len(parser.text) {
			return nil, &policyYAMLError{line: line}
		}
		return node, nil
	}
	if !allowShell && unsafePolicyScalar(trimmed) {
		return nil, &policyYAMLError{line: line}
	}
	return &policyYAMLNode{kind: policyYAMLScalarNode, line: line, scalar: policyYAMLScalar{value: trimmed, line: line}}, nil
}

func (parser *policyFlowParser) parseValue(stops string) (*policyYAMLNode, bool) {
	parser.skipSpace()
	if parser.index >= len(parser.text) {
		return &policyYAMLNode{kind: policyYAMLEmptyNode, line: parser.line}, true
	}
	switch parser.text[parser.index] {
	case '{':
		return parser.parseMapping()
	case '[':
		return parser.parseSequence()
	case '\'', '"':
		quote := parser.text[parser.index]
		value, end, ok := parsePolicyQuotedScalar(parser.text, parser.index)
		if !ok {
			return nil, false
		}
		parser.index = end
		style := policyYAMLSingleQuotedScalar
		if quote == '"' {
			style = policyYAMLDoubleQuotedScalar
		}
		return &policyYAMLNode{
			kind:   policyYAMLScalarNode,
			line:   parser.line,
			scalar: policyYAMLScalar{value: value, line: parser.line, style: style},
		}, true
	default:
		start := parser.index
		for parser.index < len(parser.text) && !strings.ContainsRune(stops, rune(parser.text[parser.index])) {
			if strings.HasPrefix(parser.text[parser.index:], "${{") {
				closing := strings.Index(parser.text[parser.index+3:], "}}")
				if closing < 0 {
					return nil, false
				}
				parser.index += 3 + closing + 2
				continue
			}
			parser.index++
		}
		value := strings.TrimSpace(parser.text[start:parser.index])
		if value == "" || unsafePolicyScalar(value) {
			return nil, false
		}
		return &policyYAMLNode{kind: policyYAMLScalarNode, line: parser.line, scalar: policyYAMLScalar{value: value, line: parser.line}}, true
	}
}

func (parser *policyFlowParser) parseMapping() (*policyYAMLNode, bool) {
	parser.index++
	node := &policyYAMLNode{kind: policyYAMLMappingNode, line: parser.line}
	seen := make(map[string]struct{})
	for {
		parser.skipSpace()
		if parser.index >= len(parser.text) {
			return nil, false
		}
		if parser.text[parser.index] == '}' {
			parser.index++
			return node, true
		}
		keyStart := parser.index
		var key string
		if parser.text[parser.index] == '\'' || parser.text[parser.index] == '"' {
			var ok bool
			key, parser.index, ok = parsePolicyQuotedScalar(parser.text, parser.index)
			if !ok {
				return nil, false
			}
		} else {
			for parser.index < len(parser.text) && parser.text[parser.index] != ':' {
				parser.index++
			}
			key = strings.TrimSpace(parser.text[keyStart:parser.index])
		}
		parser.skipSpace()
		if parser.index >= len(parser.text) || parser.text[parser.index] != ':' || key == "" || unsafePolicyKey(key) {
			return nil, false
		}
		if _, duplicate := seen[key]; duplicate {
			return nil, false
		}
		seen[key] = struct{}{}
		parser.index++
		value, ok := parser.parseValue(",}")
		if !ok {
			return nil, false
		}
		node.mappings = append(node.mappings, policyYAMLMapping{
			key:   policyYAMLScalar{value: key, line: parser.line},
			value: value,
		})
		parser.skipSpace()
		if parser.index < len(parser.text) && parser.text[parser.index] == ',' {
			parser.index++
			continue
		}
		if parser.index < len(parser.text) && parser.text[parser.index] == '}' {
			continue
		}
		return nil, false
	}
}

func (parser *policyFlowParser) parseSequence() (*policyYAMLNode, bool) {
	parser.index++
	node := &policyYAMLNode{kind: policyYAMLSequenceNode, line: parser.line}
	for {
		parser.skipSpace()
		if parser.index >= len(parser.text) {
			return nil, false
		}
		if parser.text[parser.index] == ']' {
			parser.index++
			return node, true
		}
		item, ok := parser.parseValue(",]")
		if !ok {
			return nil, false
		}
		node.items = append(node.items, item)
		parser.skipSpace()
		if parser.index < len(parser.text) && parser.text[parser.index] == ',' {
			parser.index++
			continue
		}
		if parser.index < len(parser.text) && parser.text[parser.index] == ']' {
			continue
		}
		return nil, false
	}
}

func (parser *policyFlowParser) skipSpace() {
	for parser.index < len(parser.text) && isYAMLWhitespace(parser.text[parser.index]) {
		parser.index++
	}
}

func parsePolicyQuotedScalar(text string, start int) (string, int, bool) {
	quote := text[start]
	if quote == '\'' {
		var value strings.Builder
		for index := start + 1; index < len(text); index++ {
			if text[index] != '\'' {
				value.WriteByte(text[index])
				continue
			}
			if index+1 < len(text) && text[index+1] == '\'' {
				value.WriteByte('\'')
				index++
				continue
			}
			return value.String(), index + 1, true
		}
		return "", len(text), false
	}
	escaped := false
	for index := start + 1; index < len(text); index++ {
		if escaped {
			escaped = false
			continue
		}
		if text[index] == '\\' {
			escaped = true
			continue
		}
		if text[index] != '"' {
			continue
		}
		raw := text[start : index+1]
		value, err := strconv.Unquote(raw)
		return value, index + 1, err == nil
	}
	return "", len(text), false
}

func unsafePolicyKey(key string) bool {
	return key == "<<" || unsafePolicyScalar(key)
}

func unsafePolicyScalar(value string) bool {
	withoutExpressions := value
	for {
		start := strings.Index(withoutExpressions, "${{")
		if start < 0 {
			break
		}
		end := strings.Index(withoutExpressions[start+3:], "}}")
		if end < 0 {
			return true
		}
		end += start + 5
		withoutExpressions = withoutExpressions[:start] + " " + withoutExpressions[end:]
	}
	for _, field := range strings.Fields(withoutExpressions) {
		if strings.HasPrefix(field, "&") || strings.HasPrefix(field, "*") || strings.HasPrefix(field, "!") {
			return true
		}
	}
	return false
}

func isPolicyBlockScalarIndicator(value string) bool {
	switch value {
	case "|", "|-", "|+", ">", ">-", ">+":
		return true
	default:
		return false
	}
}

func collectWorkflowDoc(path string, root *policyYAMLNode) workflowDoc {
	doc := workflowDoc{path: path}
	if root == nil || root.kind != policyYAMLMappingNode {
		return doc
	}
	if mapping := policyYAMLMappingNamed(root, "on"); mapping != nil {
		doc.on = mapping
	}
	if mapping := policyYAMLMappingNamed(root, "permissions"); mapping != nil {
		doc.topPermissions = mapping
	}
	doc.env = policyYAMLNodeNamed(root, "env")
	doc.defaults = policyYAMLNodeNamed(root, "defaults")
	jobsMapping := policyYAMLMappingNamed(root, "jobs")
	if jobsMapping == nil || jobsMapping.value.kind != policyYAMLMappingNode {
		return doc
	}
	for _, mapping := range jobsMapping.value.mappings {
		job := workflowJob{
			id:               mapping.key.value,
			line:             mapping.key.line,
			node:             mapping.value,
			workflowEnv:      doc.env,
			workflowDefaults: doc.defaults,
		}
		if mapping.value.kind == policyYAMLMappingNode {
			job.runsOn = policyYAMLScalarNamed(mapping.value, "runs-on")
			job.timeout = policyYAMLScalarNamed(mapping.value, "timeout-minutes")
			job.strategy = policyYAMLNodeNamed(mapping.value, "strategy")
			job.env = policyYAMLNodeNamed(mapping.value, "env")
			job.defaults = policyYAMLNodeNamed(mapping.value, "defaults")
			job.ifExpr = policyYAMLNodeNamed(mapping.value, "if")
			job.needs = policyYAMLNodeNamed(mapping.value, "needs")
			steps := policyYAMLNodeNamed(mapping.value, "steps")
			if steps != nil && steps.kind == policyYAMLSequenceNode {
				for _, item := range steps.items {
					step := workflowStep{line: item.line, node: item}
					if item.kind == policyYAMLMappingNode {
						step.name = policyYAMLScalarNamed(item, "name")
						step.uses = policyYAMLScalarNamed(item, "uses")
						step.with = policyYAMLNodeNamed(item, "with")
						step.env = policyYAMLNodeNamed(item, "env")
						step.ifExpr = policyYAMLScalarNamed(item, "if")
						step.run = policyYAMLScalarNamed(item, "run")
						if mapping := policyYAMLMappingNamed(item, "run"); mapping != nil {
							step.runLine = mapping.key.line
						}
					}
					job.steps = append(job.steps, step)
				}
			}
		}
		doc.jobs = append(doc.jobs, job)
	}
	return doc
}

func checkGenericWorkflowPolicy(doc workflowDoc) []Finding {
	var findings []Finding
	add := func(line int, rule, message string) {
		findings = append(findings, Finding{Path: doc.path, Line: line, Rule: rule, Message: message})
	}

	if doc.on != nil {
		checkWorkflowEvents(doc.on.value, doc.on.key.line, add)
	}
	if doc.topPermissions == nil || !isExactReadPermissions(doc.topPermissions.value) {
		line := 1
		if doc.topPermissions != nil {
			line = doc.topPermissions.key.line
		}
		add(line, "workflow.top_permissions", "top-level permissions must be exactly contents: read")
	}
	if doc.topPermissions != nil {
		checkWritePermissions(doc.topPermissions.value, doc.topPermissions.key.line, add)
	}

	for _, job := range doc.jobs {
		if job.node.kind != policyYAMLMappingNode {
			add(job.line, "workflow.job_timeout", fmt.Sprintf("job %q must set timeout-minutes to a positive integer", job.id))
			continue
		}
		if permissions := policyYAMLMappingNamed(job.node, "permissions"); permissions != nil {
			checkWritePermissions(permissions.value, permissions.key.line, add)
		}
		if uses := policyYAMLScalarNamed(job.node, "uses"); uses != nil {
			checkWorkflowAction(*uses, add)
		}
		if occurrence := policyYAMLMappingNamed(job.node, "continue-on-error"); occurrence != nil {
			add(occurrence.key.line, "workflow.continue_on_error", "continue-on-error is forbidden")
		}
		if job.timeout == nil {
			add(job.line, "workflow.job_timeout", fmt.Sprintf("job %q must set timeout-minutes to a positive integer", job.id))
		} else if !positiveDecimalPattern.MatchString(job.timeout.value) {
			add(job.timeout.line, "workflow.job_timeout", fmt.Sprintf("job %q must set timeout-minutes to a positive integer", job.id))
		}
		if job.strategy != nil && job.strategy.kind == policyYAMLMappingNode && policyYAMLNodeNamed(job.strategy, "matrix") != nil {
			failFast := policyYAMLNodeNamed(job.strategy, "fail-fast")
			if failFast == nil || failFast.kind != policyYAMLScalarNode {
				add(job.line, "workflow.matrix_fail_fast", fmt.Sprintf("matrix job %q must set strategy.fail-fast to false", job.id))
			} else if failFast.scalar.style != policyYAMLPlainScalar || !strings.EqualFold(failFast.scalar.value, "false") {
				add(failFast.scalar.line, "workflow.matrix_fail_fast", fmt.Sprintf("matrix job %q must set strategy.fail-fast to false", job.id))
			}
		}

		for _, step := range job.steps {
			if occurrence := policyYAMLMappingNamed(step.node, "continue-on-error"); occurrence != nil {
				add(occurrence.key.line, "workflow.continue_on_error", "continue-on-error is forbidden")
			}
			if step.uses == nil {
				continue
			}
			checkWorkflowAction(*step.uses, add)
			if !isCheckoutAction(step.uses.value) {
				continue
			}
			persist := policyYAMLScalarNamed(step.with, "persist-credentials")
			if persist == nil {
				add(step.uses.line, "workflow.persist_credentials", "checkout must explicitly set persist-credentials to false")
			} else if !strings.EqualFold(persist.value, "false") {
				add(persist.line, "workflow.persist_credentials", "checkout persist-credentials must be false")
			}
		}
	}
	return findings
}

func checkWorkflowEvents(node *policyYAMLNode, keyLine int, add func(int, string, string)) {
	switch node.kind {
	case policyYAMLScalarNode:
		if strings.EqualFold(node.scalar.value, "pull_request_target") {
			add(node.scalar.line, "workflow.pull_request_target", "pull_request_target is prohibited")
		}
	case policyYAMLSequenceNode:
		for _, item := range node.items {
			if item.kind == policyYAMLScalarNode && strings.EqualFold(item.scalar.value, "pull_request_target") {
				add(item.scalar.line, "workflow.pull_request_target", "pull_request_target is prohibited")
			}
		}
	case policyYAMLMappingNode:
		for _, mapping := range node.mappings {
			if strings.EqualFold(mapping.key.value, "pull_request_target") {
				add(mapping.key.line, "workflow.pull_request_target", "pull_request_target is prohibited")
			}
		}
	default:
		_ = keyLine
	}
}

func isExactReadPermissions(node *policyYAMLNode) bool {
	if node == nil || node.kind != policyYAMLMappingNode || len(node.mappings) != 1 {
		return false
	}
	mapping := node.mappings[0]
	return mapping.key.value == "contents" && policyYAMLScalarEquals(mapping.value, "read")
}

func checkWritePermissions(node *policyYAMLNode, keyLine int, add func(int, string, string)) {
	if node == nil {
		return
	}
	if node.kind == policyYAMLScalarNode {
		if strings.EqualFold(node.scalar.value, "write") || strings.EqualFold(node.scalar.value, "write-all") {
			add(keyLine, "workflow.permissions_write", "workflow permissions must not grant write access")
		}
		return
	}
	if node.kind != policyYAMLMappingNode {
		return
	}
	for _, mapping := range node.mappings {
		if policyYAMLScalarEquals(mapping.value, "write") || policyYAMLScalarEquals(mapping.value, "write-all") {
			add(mapping.key.line, "workflow.permissions_write", fmt.Sprintf("permission %q grants write access", mapping.key.value))
		}
	}
}

func checkWorkflowAction(action policyYAMLScalar, add func(int, string, string)) {
	value := action.value
	if strings.HasPrefix(value, "./") {
		return
	}
	if strings.HasPrefix(value, "docker://") {
		add(action.line, "workflow.action_pin", fmt.Sprintf("action %q must use a 40-character lowercase commit SHA", value))
		return
	}
	separator := strings.LastIndexByte(value, '@')
	if separator < 1 || !actionCommitPattern.MatchString(value[separator+1:]) {
		add(action.line, "workflow.action_pin", fmt.Sprintf("action %q must use a 40-character lowercase commit SHA", value))
		return
	}
	name := value[:separator]
	approved, official := approvedActionCommits[strings.ToLower(name)]
	if official && value[separator+1:] != approved {
		add(action.line, "workflow.action_version", fmt.Sprintf("action %q must use approved commit %q", strings.ToLower(name), approved))
	}
}

func policyYAMLMappingNamed(node *policyYAMLNode, name string) *policyYAMLMapping {
	if node == nil || node.kind != policyYAMLMappingNode {
		return nil
	}
	for index := range node.mappings {
		if node.mappings[index].key.value == name {
			return &node.mappings[index]
		}
	}
	return nil
}

func policyYAMLNodeNamed(node *policyYAMLNode, name string) *policyYAMLNode {
	mapping := policyYAMLMappingNamed(node, name)
	if mapping == nil {
		return nil
	}
	return mapping.value
}

func policyYAMLScalarNamed(node *policyYAMLNode, name string) *policyYAMLScalar {
	value := policyYAMLNodeNamed(node, name)
	if value == nil || value.kind != policyYAMLScalarNode {
		return nil
	}
	return &value.scalar
}

func policyYAMLScalarEquals(node *policyYAMLNode, value string) bool {
	return node != nil && node.kind == policyYAMLScalarNode && strings.EqualFold(node.scalar.value, value)
}

func deduplicateWorkflowFindings(findings []Finding) []Finding {
	seen := make(map[string]struct{}, len(findings))
	result := make([]Finding, 0, len(findings))
	for _, finding := range findings {
		key := fmt.Sprintf("%s\x00%d\x00%s\x00%s", finding.Path, finding.Line, finding.Rule, finding.Message)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, finding)
	}
	return result
}
