package docscheck

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
)

const canonicalDocumentationURL = "https://github.com/TyrantLucifer/awesome-sftp-cli/blob/main/"

func checkTranslationPairs(root string) []Finding {
	var findings []Finding
	for _, pair := range bilingualDocumentationPairs {
		english, englishErr := readLines(root, pair.english)
		chinese, chineseErr := readLines(root, pair.chinese)
		if englishErr != nil || chineseErr != nil {
			continue
		}

		englishTarget := relativeDocumentLink(pair.english, pair.chinese)
		if !hasTranslationLink(english, englishTarget, pair.chinese) {
			findings = append(findings, Finding{
				Path: pair.english, Line: 1, Rule: "docs.translation_link",
				Message: fmt.Sprintf("English page must link to its Chinese translation %q", englishTarget),
			})
		}
		chineseTarget := relativeDocumentLink(pair.chinese, pair.english)
		if !hasTranslationLink(chinese, chineseTarget, pair.english) {
			findings = append(findings, Finding{
				Path: pair.chinese, Line: 1, Rule: "docs.translation_link",
				Message: fmt.Sprintf("Chinese page must link back to its English source %q", chineseTarget),
			})
		}

		englishHeadings := markdownHeadingLevels(english)
		chineseHeadings := markdownHeadingLevels(chinese)
		if !reflect.DeepEqual(chineseHeadings, englishHeadings) {
			findings = append(findings, Finding{
				Path: pair.chinese, Line: 1, Rule: "docs.translation_structure",
				Message: fmt.Sprintf("Chinese heading levels %v must match English source %v", chineseHeadings, englishHeadings),
			})
		}
	}
	return findings
}

func hasTranslationLink(lines []string, relativeTarget, repositoryTarget string) bool {
	return hasMarkdownLinkTarget(lines, relativeTarget) ||
		hasMarkdownLinkTarget(lines, canonicalDocumentationURL+repositoryTarget)
}

func relativeDocumentLink(document, target string) string {
	relative, err := filepath.Rel(filepath.Dir(filepath.FromSlash(document)), filepath.FromSlash(target))
	if err != nil {
		return filepath.ToSlash(target)
	}
	return filepath.ToSlash(relative)
}

func hasMarkdownLinkTarget(lines []string, target string) bool {
	inFence := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		for _, candidate := range markdownLinkTargets(line) {
			if candidate == target {
				return true
			}
		}
	}
	return false
}

func markdownHeadingLevels(lines []string) []int {
	var levels []int
	inFence := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		level := 0
		for level < len(line) && level < 6 && line[level] == '#' {
			level++
		}
		if level > 0 && len(line) > level && line[level] == ' ' {
			levels = append(levels, level)
		}
	}
	return levels
}
