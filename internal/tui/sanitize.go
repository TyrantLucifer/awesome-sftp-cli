package tui

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

func SanitizeTerminalText(input string) string {
	var output strings.Builder
	output.Grow(len(input))
	for len(input) != 0 {
		char, size := utf8.DecodeRuneInString(input)
		if char == utf8.RuneError && size == 1 {
			output.WriteRune(utf8.RuneError)
			input = input[1:]
			continue
		}
		if unicode.IsControl(char) {
			output.WriteRune(utf8.RuneError)
		} else {
			output.WriteRune(char)
		}
		input = input[size:]
	}
	return output.String()
}
