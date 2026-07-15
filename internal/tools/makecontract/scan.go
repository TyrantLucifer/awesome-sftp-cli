package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

type finding struct {
	Path    string
	Line    int
	Message string
}

type recipe struct {
	line   int
	target string
	text   string
}

type makeSyntaxLine struct {
	line int
	text string
}

type shellTokenKind int

const (
	shellWord shellTokenKind = iota
	shellOperator
)

type shellToken struct {
	kind         shellTokenKind
	text         string
	makeRefs     []string
	dynamic      bool
	redirection  bool
	nestedIssues []string
}

var assignmentPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
var makeAssignmentPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+[ \t]*(?:[?:+!])?=`)
var simpleMakeReferencePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var makeNamedAssignmentPattern = regexp.MustCompile(`^(?:(?:override|export|private|unexport)[ \t]+)*([A-Za-z0-9_.-]+)[ \t]*(?:::=|::=|:=|\+=|\?=|!=|=)`)

func scanSource(path, source string) []finding {
	findings := scanMakeSyntax(path, source)
	recipes, recipeFindings := extractRecipes(path, source)
	findings = append(findings, recipeFindings...)
	for _, recipe := range recipes {
		findings = append(findings, scanShell(path, recipe.line, recipe.text)...)
	}
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Line != findings[j].Line {
			return findings[i].Line < findings[j].Line
		}
		return findings[i].Message < findings[j].Message
	})
	return findings
}

func scanMakeSyntax(path, source string) []finding {
	var findings []finding
	for index, line := range strings.Split(source, "\n") {
		if strings.ContainsRune(line, '\r') {
			findings = append(findings, finding{Path: path, Line: index + 1, Message: "canonical Makefile must use LF line endings"})
		}
	}
	for _, line := range makeSyntaxLines(source) {
		trimmed := strings.TrimSpace(line.text)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if control := recipeExecutionControl(trimmed); control != "" {
			findings = append(findings, finding{Path: path, Line: line.line, Message: fmt.Sprintf("recipe execution control %s is forbidden in the canonical Makefile", control)})
		}
		if hasDirectivePrefix(trimmed, "include") || hasDirectivePrefix(trimmed, "-include") || hasDirectivePrefix(trimmed, "sinclude") {
			findings = append(findings, finding{Path: path, Line: line.line, Message: "included Make sources are outside the canonical Makefile contract"})
		}
		if hasDirectivePrefix(trimmed, "define") || hasDirectivePrefix(trimmed, "endef") {
			findings = append(findings, finding{Path: path, Line: line.line, Message: "define directives are unsupported in the canonical Makefile"})
		}
		if containsMakeExecution(trimmed) {
			findings = append(findings, finding{Path: path, Line: line.line, Message: "parse-time shell/eval execution is unsupported in the canonical Makefile"})
		}
	}
	return findings
}

func makeSyntaxLines(source string) []makeSyntaxLine {
	physicalLines := strings.Split(source, "\n")
	lines := make([]makeSyntaxLine, 0, len(physicalLines))
	var logical strings.Builder
	logicalStart := 0
	continuingMakeSyntax := false
	continuingRecipe := false

	emitLogical := func() {
		lines = append(lines, makeSyntaxLine{line: logicalStart, text: logical.String()})
		logical.Reset()
		logicalStart = 0
		continuingMakeSyntax = false
	}

	for index, physical := range physicalLines {
		lineNumber := index + 1
		if continuingRecipe {
			lines = append(lines, makeSyntaxLine{line: lineNumber, text: physical})
			continuingRecipe = hasOddTrailingBackslash(physical)
			continue
		}
		if continuingMakeSyntax {
			logical.WriteByte(' ')
			if hasOddTrailingBackslash(physical) {
				logical.WriteString(physical[:len(physical)-1])
				continue
			}
			logical.WriteString(physical)
			emitLogical()
			continue
		}
		if isRecipeSyntaxLine(physical) {
			lines = append(lines, makeSyntaxLine{line: lineNumber, text: physical})
			continuingRecipe = hasOddTrailingBackslash(physical)
			continue
		}
		if hasOddTrailingBackslash(physical) {
			logicalStart = lineNumber
			logical.WriteString(physical[:len(physical)-1])
			continuingMakeSyntax = true
			continue
		}
		lines = append(lines, makeSyntaxLine{line: lineNumber, text: physical})
	}
	if continuingMakeSyntax {
		emitLogical()
	}
	return lines
}

func isRecipeSyntaxLine(line string) bool {
	if strings.HasPrefix(line, "\t") {
		return true
	}
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") || makeAssignmentPattern.MatchString(trimmed) || isMakeDirective(trimmed) {
		return false
	}
	colon := topLevelMakeCharacter(line, ':')
	if colon < 0 {
		return false
	}
	right := strings.TrimSpace(line[colon+1:])
	if strings.HasPrefix(right, ":") {
		right = strings.TrimSpace(right[1:])
	}
	if makeAssignmentName(right) != "" {
		return false
	}
	return topLevelMakeCharacter(right, ';') >= 0
}

func recipeExecutionControl(line string) string {
	if name := makeAssignmentName(line); isRecipeExecutionVariable(name) {
		return name
	}

	colon := topLevelMakeCharacter(line, ':')
	if colon < 0 {
		return ""
	}
	left := strings.TrimSpace(line[:colon])
	for _, target := range strings.Fields(left) {
		switch target {
		case ".IGNORE", ".ONESHELL", ".POSIX":
			return target
		}
	}
	right := strings.TrimSpace(line[colon+1:])
	if strings.HasPrefix(right, ":") {
		right = strings.TrimSpace(right[1:])
	}
	if name := makeAssignmentName(right); isRecipeExecutionVariable(name) {
		return name
	}
	return ""
}

func makeAssignmentName(line string) string {
	match := makeNamedAssignmentPattern.FindStringSubmatch(line)
	if len(match) != 2 {
		return ""
	}
	return match[1]
}

func isRecipeExecutionVariable(name string) bool {
	switch name {
	case "SHELL", ".SHELLFLAGS", ".RECIPEPREFIX", "MAKEFLAGS", "MFLAGS", "GNUMAKEFLAGS", "MAKEFILES":
		return true
	default:
		return false
	}
}

func hasDirectivePrefix(line, directive string) bool {
	if !strings.HasPrefix(line, directive) {
		return false
	}
	return len(line) == len(directive) || unicode.IsSpace(rune(line[len(directive)]))
}

func containsMakeExecution(line string) bool {
	for _, marker := range []string{"$(shell", "${shell", "$(eval", "${eval"} {
		if strings.Contains(line, marker) {
			return true
		}
	}
	return false
}

func extractRecipes(path, source string) ([]recipe, []finding) {
	lines := strings.Split(source, "\n")
	var recipes []recipe
	var findings []finding
	var targets []string
	var buffered strings.Builder
	bufferedLine := 0
	bufferedTarget := ""
	continuing := false

	emit := func() {
		text := stripRecipeModifiers(buffered.String())
		if strings.TrimSpace(text) != "" {
			recipes = append(recipes, recipe{line: bufferedLine, target: bufferedTarget, text: text})
		}
		buffered.Reset()
		bufferedLine = 0
		bufferedTarget = ""
		continuing = false
	}

	for index, physical := range lines {
		lineNumber := index + 1
		if continuing {
			piece := physical
			piece = strings.TrimPrefix(piece, "\t")
			if hasOddTrailingBackslash(piece) {
				buffered.WriteString(piece[:len(piece)-1])
				continue
			}
			buffered.WriteString(piece)
			emit()
			continue
		}

		if strings.HasPrefix(physical, "\t") {
			if len(targets) == 0 {
				findings = append(findings, finding{Path: path, Line: lineNumber, Message: "recipe appears outside a static rule"})
				continue
			}
			piece := physical[1:]
			bufferedLine = lineNumber
			bufferedTarget = strings.Join(targets, " ")
			if hasOddTrailingBackslash(piece) {
				buffered.WriteString(piece[:len(piece)-1])
				continuing = true
				continue
			}
			buffered.WriteString(piece)
			emit()
			continue
		}

		trimmed := strings.TrimSpace(physical)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if makeAssignmentPattern.MatchString(trimmed) || isMakeDirective(trimmed) {
			targets = nil
			continue
		}

		colon := topLevelMakeCharacter(physical, ':')
		if colon < 0 {
			targets = nil
			continue
		}
		left := strings.TrimSpace(physical[:colon])
		if left == "" {
			targets = nil
			continue
		}
		if strings.Contains(left, "$(") || strings.Contains(left, "${") {
			findings = append(findings, finding{Path: path, Line: lineNumber, Message: "dynamic rule targets are unsupported in the canonical Makefile"})
			targets = nil
			continue
		}
		targets = strings.Fields(left)
		right := physical[colon+1:]
		semicolon := topLevelMakeCharacter(right, ';')
		if semicolon < 0 {
			continue
		}
		piece := right[semicolon+1:]
		bufferedLine = lineNumber
		bufferedTarget = strings.Join(targets, " ")
		if hasOddTrailingBackslash(piece) {
			buffered.WriteString(piece[:len(piece)-1])
			continuing = true
			continue
		}
		buffered.WriteString(piece)
		emit()
	}

	if continuing {
		findings = append(findings, finding{Path: path, Line: bufferedLine, Message: "unterminated recipe continuation"})
	}
	return recipes, findings
}

func isMakeDirective(line string) bool {
	for _, directive := range []string{"ifeq", "ifneq", "ifdef", "ifndef", "else", "endif", "include", "-include", "sinclude", "define", "endef", "override", "export", "unexport", "private", "vpath"} {
		if hasDirectivePrefix(line, directive) {
			return true
		}
	}
	return false
}

func topLevelMakeCharacter(line string, wanted byte) int {
	depth := 0
	closers := make([]byte, 0, 4)
	escaped := false
	for index := 0; index < len(line); index++ {
		character := line[index]
		if escaped {
			escaped = false
			continue
		}
		if character == '\\' {
			escaped = true
			continue
		}
		if character == '$' && index+1 < len(line) && (line[index+1] == '(' || line[index+1] == '{') {
			closer := byte(')')
			if line[index+1] == '{' {
				closer = '}'
			}
			closers = append(closers, closer)
			depth++
			index++
			continue
		}
		if depth > 0 {
			if character == closers[len(closers)-1] {
				closers = closers[:len(closers)-1]
				depth--
			}
			continue
		}
		if character == wanted {
			return index
		}
	}
	return -1
}

func hasOddTrailingBackslash(line string) bool {
	count := 0
	for index := len(line) - 1; index >= 0 && line[index] == '\\'; index-- {
		count++
	}
	return count%2 == 1
}

func stripRecipeModifiers(command string) string {
	for len(command) > 0 && strings.ContainsRune("@+-", rune(command[0])) {
		command = command[1:]
	}
	return command
}

func scanShell(path string, line int, command string) []finding {
	tokens, tokenIssues := lexShell(command)
	findings := make([]finding, 0, len(tokenIssues))
	for _, issue := range tokenIssues {
		findings = append(findings, finding{Path: path, Line: line, Message: issue})
	}

	commandStart := true
	redirectionTarget := false
	for _, token := range tokens {
		for _, issue := range token.nestedIssues {
			findings = append(findings, finding{Path: path, Line: line, Message: issue})
		}
		unsafeExpansion := token.dynamic || hasUnapprovedMakeReference(token.makeRefs)
		if token.kind == shellWord && unsafeExpansion {
			findings = append(findings, finding{Path: path, Line: line, Message: "dynamic or indirect Make/shell expansion is forbidden in canonical recipes"})
		}
		if token.kind == shellOperator {
			if token.redirection {
				redirectionTarget = true
				continue
			}
			redirectionTarget = false
			commandStart = true
			continue
		}
		if redirectionTarget {
			redirectionTarget = false
			continue
		}
		if !commandStart {
			continue
		}
		if assignmentPattern.MatchString(token.text) {
			continue
		}
		if isShellControlWord(token.text) {
			continue
		}
		commandStart = false

		if unsafeExpansion || len(token.makeRefs) > 0 && (len(token.makeRefs) != 1 || token.text != "") {
			findings = append(findings, finding{Path: path, Line: line, Message: "dynamic or indirect executable is forbidden in canonical recipes"})
			continue
		}
		if len(token.makeRefs) == 1 {
			if token.makeRefs[0] != "GO" {
				findings = append(findings, finding{Path: path, Line: line, Message: "dynamic or indirect executable is forbidden in canonical recipes"})
			}
			continue
		}
		if isHardCodedGo(token.text) {
			findings = append(findings, finding{Path: path, Line: line, Message: fmt.Sprintf("hard-coded Go executable %q bypasses $(GO)", token.text)})
			continue
		}
		if isIndirectExecutor(token.text) {
			findings = append(findings, finding{Path: path, Line: line, Message: fmt.Sprintf("dynamic or indirect executable %q is forbidden in canonical recipes", token.text)})
			continue
		}
		if !isAllowedLiteralCommand(token.text) {
			findings = append(findings, finding{Path: path, Line: line, Message: fmt.Sprintf("dynamic or indirect executable %q is not on the canonical recipe allowlist", token.text)})
		}
	}
	return findings
}

func hasUnapprovedMakeReference(references []string) bool {
	for _, reference := range references {
		switch reference {
		case "GO", "BUILD_DIR", "COVERAGE_DIR", "TOOL_MOD", "MAKE_CONTRACT_RECIPE_GUARD":
			continue
		default:
			return true
		}
	}
	return false
}

func isShellControlWord(word string) bool {
	switch word {
	case "!", "{", "}", "if", "then", "elif", "else", "while", "until", "do", "fi", "done", "esac":
		return true
	default:
		return false
	}
}

func isHardCodedGo(word string) bool {
	return word == "go" || filepath.Base(word) == "go" && strings.ContainsRune(word, '/')
}

func isIndirectExecutor(word string) bool {
	base := filepath.Base(word)
	switch base {
	case "env", "command", "exec", "nice", "nohup", "timeout", "sudo", "doas", "time",
		"sh", "bash", "dash", "zsh", "ksh", "eval", "xargs", "find":
		return true
	default:
		return false
	}
}

func isAllowedLiteralCommand(word string) bool {
	switch filepath.Base(word) {
	case ":", "echo", "false", "mkdir", "printf", "test", "true", "[":
		return true
	default:
		return false
	}
}

func lexShell(command string) ([]shellToken, []string) {
	var tokens []shellToken
	var issues []string
	for index := 0; index < len(command); {
		if isShellSpace(command[index]) {
			index++
			continue
		}
		if command[index] == '#' {
			break
		}
		if token, next, ok := lexShellOperator(command, index); ok {
			tokens = append(tokens, token)
			index = next
			continue
		}

		word, next, wordIssues := lexShellWord(command, index)
		issues = append(issues, wordIssues...)
		if next == index {
			issues = append(issues, fmt.Sprintf("unsupported shell syntax near %q", command[index:]))
			break
		}
		tokens = append(tokens, word)
		index = next
	}
	return tokens, issues
}

func lexShellOperator(command string, start int) (shellToken, int, bool) {
	index := start
	for index < len(command) && command[index] >= '0' && command[index] <= '9' {
		index++
	}
	if index < len(command) && (command[index] == '<' || command[index] == '>') {
		end := index + 1
		for end < len(command) && strings.ContainsRune("<>&|", rune(command[end])) {
			end++
		}
		return shellToken{kind: shellOperator, text: command[start:end], redirection: true}, end, true
	}

	for _, operator := range []string{"&&", "||", ";;", "|&", ";", "|", "&", "(", ")", "{", "}"} {
		if strings.HasPrefix(command[start:], operator) {
			return shellToken{kind: shellOperator, text: operator}, start + len(operator), true
		}
	}
	return shellToken{}, start, false
}

func lexShellWord(command string, start int) (shellToken, int, []string) {
	token := shellToken{kind: shellWord}
	var literal strings.Builder
	var issues []string
	quote := byte(0)
	index := start
	for index < len(command) {
		character := command[index]
		if quote == 0 {
			if isShellSpace(character) || character == '#' && literal.Len() == 0 && len(token.makeRefs) == 0 || startsShellOperator(command, index) {
				break
			}
			if character == '\'' || character == '"' {
				quote = character
				index++
				continue
			}
			if character == '\\' {
				if index+1 >= len(command) {
					issues = append(issues, "unterminated shell escape in recipe")
					index++
					break
				}
				literal.WriteByte(command[index+1])
				index += 2
				continue
			}
		}

		if character == '`' && quote != '\'' {
			token.dynamic = true
			issues = append(issues, "dynamic or indirect executable via backticks is forbidden in canonical recipes")
			index++
			continue
		}
		if character == '$' {
			if quote != '\'' && strings.HasPrefix(command[index:], "$$(") {
				body, next, ok := readBalancedShellSubstitution(command, index+3)
				if !ok {
					issues = append(issues, "unterminated shell command substitution")
					return token, len(command), issues
				}
				nestedFindings := scanShell("", 0, body)
				for _, nested := range nestedFindings {
					token.nestedIssues = append(token.nestedIssues, nested.Message)
				}
				token.dynamic = true
				index = next
				continue
			}
			if index+1 < len(command) && command[index+1] == '$' {
				if quote == '\'' {
					literal.WriteByte('$')
					index += 2
					continue
				}
				token.dynamic = true
				index += 2
				if index < len(command) && command[index] == '{' {
					if end := strings.IndexByte(command[index+1:], '}'); end >= 0 {
						index += end + 2
					}
				} else {
					for index < len(command) && (command[index] == '_' || unicode.IsLetter(rune(command[index])) || unicode.IsDigit(rune(command[index]))) {
						index++
					}
				}
				continue
			}
			if index+1 < len(command) && (command[index+1] == '(' || command[index+1] == '{') {
				name, next, ok := readMakeReference(command, index)
				if !ok {
					issues = append(issues, "unterminated Make variable reference in recipe")
					return token, len(command), issues
				}
				if !simpleMakeReferencePattern.MatchString(name) {
					token.dynamic = true
				}
				token.makeRefs = append(token.makeRefs, name)
				index = next
				continue
			}
			if index+1 < len(command) {
				token.dynamic = true
				index += 2
				continue
			}
		}

		if quote != 0 && character == quote {
			quote = 0
			index++
			continue
		}
		if character == '\\' && quote == '"' {
			if index+1 >= len(command) {
				issues = append(issues, "unterminated shell escape in double quote")
				index++
				break
			}
			literal.WriteByte(command[index+1])
			index += 2
			continue
		}
		literal.WriteByte(character)
		index++
	}
	if quote != 0 {
		issues = append(issues, "unterminated shell quote in recipe")
	}
	token.text = literal.String()
	return token, index, issues
}

func startsShellOperator(command string, index int) bool {
	_, _, ok := lexShellOperator(command, index)
	return ok
}

func readMakeReference(command string, start int) (string, int, bool) {
	opener := command[start+1]
	closer := byte(')')
	if opener == '{' {
		closer = '}'
	}
	depth := 1
	for index := start + 2; index < len(command); index++ {
		if command[index] == opener {
			depth++
		}
		if command[index] == closer {
			depth--
			if depth == 0 {
				return command[start+2 : index], index + 1, true
			}
		}
	}
	return "", len(command), false
}

func readBalancedShellSubstitution(command string, start int) (string, int, bool) {
	depth := 1
	quote := byte(0)
	escaped := false
	for index := start; index < len(command); index++ {
		character := command[index]
		if escaped {
			escaped = false
			continue
		}
		if quote != '\'' && character == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if character == quote {
				quote = 0
			}
			continue
		}
		if character == '\'' || character == '"' {
			quote = character
			continue
		}
		if character == '(' {
			depth++
			continue
		}
		if character == ')' {
			depth--
			if depth == 0 {
				return command[start:index], index + 1, true
			}
		}
	}
	return "", len(command), false
}

func isShellSpace(character byte) bool {
	return character == ' ' || character == '\t' || character == '\n'
}

func formatFindings(findings []finding) string {
	var output strings.Builder
	for _, finding := range findings {
		fmt.Fprintf(&output, "%s:%d: %s\n", finding.Path, finding.Line, finding.Message)
	}
	return output.String()
}
