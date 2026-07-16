package sshconfig

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transport/openssh"
)

const (
	maximumIncludeDepth = 16
	maximumConfigFiles  = 256
	maximumConfigBytes  = 1024 * 1024
)

type discovery struct {
	sshDir  string
	visited map[string]struct{}
	aliases map[string]struct{}
	files   int
}

func DiscoverAliases(configPath, userSSHDirectory string) ([]string, error) {
	if err := validateAbsolutePath(configPath); err != nil {
		return nil, fmt.Errorf("discover SSH hosts: config path: %w", err)
	}
	if err := validateAbsolutePath(userSSHDirectory); err != nil {
		return nil, fmt.Errorf("discover SSH hosts: user SSH directory: %w", err)
	}
	state := &discovery{
		sshDir:  userSSHDirectory,
		visited: make(map[string]struct{}),
		aliases: make(map[string]struct{}),
	}
	if err := state.read(configPath, 0, true); err != nil {
		return nil, err
	}
	result := make([]string, 0, len(state.aliases))
	for alias := range state.aliases {
		result = append(result, alias)
	}
	sort.Strings(result)
	return result, nil
}

func (d *discovery) read(filePath string, depth int, optional bool) error {
	cleanPath, err := filepath.Abs(filePath)
	if err != nil {
		return fmt.Errorf("discover SSH hosts: resolve %q: %w", filePath, err)
	}
	cleanPath = filepath.Clean(cleanPath)
	if _, seen := d.visited[cleanPath]; seen {
		return nil
	}
	if depth > maximumIncludeDepth {
		return fmt.Errorf("discover SSH hosts: include depth exceeds %d at %q", maximumIncludeDepth, cleanPath)
	}
	file, err := os.Open(cleanPath)
	if errors.Is(err, os.ErrNotExist) && optional {
		return nil
	}
	if err != nil {
		return fmt.Errorf("discover SSH hosts: open %q: %w", cleanPath, err)
	}
	defer file.Close()
	d.files++
	if d.files > maximumConfigFiles {
		return fmt.Errorf("discover SSH hosts: config file count exceeds %d", maximumConfigFiles)
	}
	d.visited[cleanPath] = struct{}{}
	content, err := io.ReadAll(io.LimitReader(file, maximumConfigBytes+1))
	if err != nil {
		return fmt.Errorf("discover SSH hosts: read %q: %w", cleanPath, err)
	}
	if len(content) > maximumConfigBytes {
		return fmt.Errorf("discover SSH hosts: %q exceeds %d bytes", cleanPath, maximumConfigBytes)
	}
	for lineNumber, line := range strings.Split(string(content), "\n") {
		keyword, arguments, err := parseDirective(line)
		if err != nil {
			return fmt.Errorf("discover SSH hosts: %s:%d: %w", cleanPath, lineNumber+1, err)
		}
		switch strings.ToLower(keyword) {
		case "host":
			if len(arguments) == 0 {
				return fmt.Errorf("discover SSH hosts: %s:%d: Host requires a pattern", cleanPath, lineNumber+1)
			}
			for _, alias := range arguments {
				if !selectableAlias(alias) {
					continue
				}
				d.aliases[alias] = struct{}{}
			}
		case "include":
			if len(arguments) == 0 {
				return fmt.Errorf("discover SSH hosts: %s:%d: Include requires a path", cleanPath, lineNumber+1)
			}
			for _, pattern := range arguments {
				matches, err := d.expand(pattern)
				if err != nil {
					return fmt.Errorf("discover SSH hosts: %s:%d: %w", cleanPath, lineNumber+1, err)
				}
				for _, match := range matches {
					if err := d.read(match, depth+1, false); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func (d *discovery) expand(pattern string) ([]string, error) {
	if pattern == "" || strings.IndexByte(pattern, 0) >= 0 {
		return nil, errors.New("SSH Include path is invalid")
	}
	switch {
	case pattern == "~":
		pattern = filepath.Dir(d.sshDir)
	case strings.HasPrefix(pattern, "~/"):
		pattern = filepath.Join(filepath.Dir(d.sshDir), pattern[2:])
	case !filepath.IsAbs(pattern):
		pattern = filepath.Join(d.sshDir, pattern)
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("expand SSH Include %q: %w", pattern, err)
	}
	sort.Strings(matches)
	return matches, nil
}

func parseDirective(line string) (string, []string, error) {
	tokens, err := splitConfigLine(line)
	if err != nil || len(tokens) == 0 {
		return "", nil, err
	}
	keyword := tokens[0]
	arguments := tokens[1:]
	if equals := strings.IndexByte(keyword, '='); equals >= 0 {
		value := keyword[equals+1:]
		keyword = keyword[:equals]
		if value != "" {
			arguments = append([]string{value}, arguments...)
		}
	} else if len(arguments) != 0 && arguments[0] == "=" {
		arguments = arguments[1:]
	}
	if keyword == "" {
		return "", nil, errors.New("empty SSH config keyword")
	}
	return keyword, arguments, nil
}

func splitConfigLine(line string) ([]string, error) {
	var tokens []string
	for index := 0; index < len(line); {
		for index < len(line) && unicode.IsSpace(rune(line[index])) {
			index++
		}
		if index == len(line) || line[index] == '#' {
			break
		}
		var token strings.Builder
		var quote byte
		for index < len(line) {
			value := line[index]
			if quote == 0 && unicode.IsSpace(rune(value)) {
				break
			}
			index++
			switch {
			case value == '\\':
				if index == len(line) {
					return nil, errors.New("dangling SSH config escape")
				}
				token.WriteByte(line[index])
				index++
			case quote == 0 && (value == '\'' || value == '"'):
				quote = value
			case quote != 0 && value == quote:
				quote = 0
			default:
				token.WriteByte(value)
			}
		}
		if quote != 0 {
			return nil, errors.New("unterminated SSH config quote")
		}
		tokens = append(tokens, token.String())
	}
	return tokens, nil
}

func selectableAlias(alias string) bool {
	if alias == "" || strings.ContainsAny(alias, "*?!") {
		return false
	}
	_, err := openssh.Arguments(alias)
	return err == nil
}

func validateAbsolutePath(value string) error {
	if value == "" || strings.IndexByte(value, 0) >= 0 || !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return errors.New("path must be canonical absolute")
	}
	return nil
}
