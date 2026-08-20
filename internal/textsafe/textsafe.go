package textsafe

import (
	"strings"
	"unicode"
)

func OneLine(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	var output strings.Builder
	output.Grow(len(value))
	for _, char := range value {
		switch char {
		case '\n', '\r':
			output.WriteString(" ⏎ ")
		case '\t':
			output.WriteByte(' ')
		default:
			if !unicode.IsControl(char) {
				output.WriteRune(char)
			}
		}
	}
	return output.String()
}
