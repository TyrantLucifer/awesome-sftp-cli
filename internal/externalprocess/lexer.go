package externalprocess

import (
	"errors"
	"fmt"
	"unicode/utf8"
)

const (
	MaxArguments     = 128
	MaxArgumentBytes = 4096
	MaxCommandBytes  = 32768
)

// Command is the structured, pre-resolution form of an external command. Args
// never includes the executable or the managed file argument.
type Command struct {
	Executable string
	Args       []string
}

// ParseEnvironmentCommand performs grouping only. It deliberately implements
// no expansion, substitution, globbing, redirection, operators, or shell
// evaluation.
func ParseEnvironmentCommand(input string) (Command, error) {
	if !utf8.ValidString(input) {
		return Command{}, errors.New("parse external command: input is not valid UTF-8")
	}
	if len(input) > MaxCommandBytes {
		return Command{}, fmt.Errorf("parse external command: input exceeds %d bytes", MaxCommandBytes)
	}
	if err := validateLexicalInput(input); err != nil {
		return Command{}, err
	}

	const (
		unquoted = iota
		singleQuoted
		doubleQuoted
	)
	state := unquoted
	escaped := false
	started := false
	word := make([]byte, 0, 32)
	words := make([]string, 0, 4)
	finish := func() error {
		if !started {
			return nil
		}
		if len(word) > MaxArgumentBytes {
			return fmt.Errorf("parse external command: item exceeds %d bytes", MaxArgumentBytes)
		}
		words = append(words, string(word))
		if len(words) > MaxArguments+1 {
			return fmt.Errorf("parse external command: argument count exceeds %d", MaxArguments)
		}
		word = word[:0]
		started = false
		return nil
	}

	for i := 0; i < len(input); i++ {
		ch := input[i]
		if escaped {
			word = append(word, ch)
			started = true
			escaped = false
			continue
		}
		switch state {
		case singleQuoted:
			if ch == '\'' {
				state = unquoted
			} else {
				word = append(word, ch)
			}
			started = true
		case doubleQuoted:
			switch ch {
			case '"':
				state = unquoted
			case '\\':
				escaped = true
			default:
				word = append(word, ch)
			}
			started = true
		default:
			switch ch {
			case ' ', '\t':
				if err := finish(); err != nil {
					return Command{}, err
				}
			case '\'':
				state = singleQuoted
				started = true
			case '"':
				state = doubleQuoted
				started = true
			case '\\':
				escaped = true
				started = true
			default:
				word = append(word, ch)
				started = true
			}
		}
	}
	if escaped {
		return Command{}, errors.New("parse external command: unclosed escape")
	}
	if state != unquoted {
		return Command{}, errors.New("parse external command: unclosed quote")
	}
	if err := finish(); err != nil {
		return Command{}, err
	}
	if len(words) == 0 || words[0] == "" {
		return Command{}, errors.New("parse external command: executable is empty")
	}

	command := Command{Executable: words[0], Args: append([]string(nil), words[1:]...)}
	if err := validateCommand(command); err != nil {
		return Command{}, err
	}
	return command, nil
}

func validateLexicalInput(input string) error {
	for i := 0; i < len(input); i++ {
		ch := input[i]
		if ch < 0x20 && ch != '\t' || ch == 0x7f {
			return fmt.Errorf("parse external command: ASCII control byte 0x%02x is forbidden", ch)
		}
		switch ch {
		case '$', '`', '*', '?', '[', ']', '|', '&', ';', '<', '>':
			return fmt.Errorf("parse external command: shell metacharacter %q is forbidden", ch)
		}
	}
	return nil
}

func validateCommand(command Command) error {
	if command.Executable == "" {
		return errors.New("validate external command: executable is empty")
	}
	if len(command.Args) > MaxArguments {
		return fmt.Errorf("validate external command: argument count exceeds %d", MaxArguments)
	}
	total := 0
	items := make([]string, 0, len(command.Args)+1)
	items = append(items, command.Executable)
	items = append(items, command.Args...)
	for _, item := range items {
		if !utf8.ValidString(item) {
			return errors.New("validate external command: item is not valid UTF-8")
		}
		if len(item) > MaxArgumentBytes {
			return fmt.Errorf("validate external command: item exceeds %d bytes", MaxArgumentBytes)
		}
		for i := 0; i < len(item); i++ {
			if item[i] < 0x20 || item[i] == 0x7f {
				return errors.New("validate external command: ASCII control bytes are forbidden")
			}
		}
		total += len(item)
	}
	if total > MaxCommandBytes {
		return fmt.Errorf("validate external command: executable and arguments exceed %d bytes", MaxCommandBytes)
	}
	return nil
}
