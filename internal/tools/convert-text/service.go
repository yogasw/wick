package converttext

import (
	"strings"
	"unicode"
)

// ConvertType defines available text transformations.
type ConvertType string

const (
	ConvertUppercase      ConvertType = "uppercase"
	ConvertLowercase      ConvertType = "lowercase"
	ConvertTitleCase      ConvertType = "titlecase"
	ConvertSentence       ConvertType = "sentence"
	ConvertAlternating    ConvertType = "alternating"
	ConvertLinesToEscaped ConvertType = "lines-to-escaped"
	ConvertEscapedToLines ConvertType = "escaped-to-lines"
)

// Convert transforms text according to ct. Unknown types return text unchanged.
func Convert(text string, ct ConvertType) string {
	switch ct {
	case ConvertUppercase:
		return strings.ToUpper(text)
	case ConvertLowercase:
		return strings.ToLower(text)
	case ConvertTitleCase:
		return toTitleCase(text)
	case ConvertSentence:
		return toSentenceCase(text)
	case ConvertAlternating:
		return toAlternatingCase(text)
	case ConvertLinesToEscaped:
		return linesToEscaped(text)
	case ConvertEscapedToLines:
		return escapedToLines(text)
	default:
		return text
	}
}

// toTitleCase capitalises the first letter of each word.
func toTitleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		runes := []rune(w)
		if len(runes) > 0 {
			runes[0] = unicode.ToUpper(runes[0])
			for j := 1; j < len(runes); j++ {
				runes[j] = unicode.ToLower(runes[j])
			}
		}
		words[i] = string(runes)
	}
	return strings.Join(words, " ")
}

// toSentenceCase capitalises only the first letter of the whole string.
func toSentenceCase(s string) string {
	lower := strings.ToLower(s)
	runes := []rune(lower)
	if len(runes) == 0 {
		return s
	}
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// linesToEscaped normalises line endings and joins lines with the literal "\n".
func linesToEscaped(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.ReplaceAll(s, "\n", `\n`)
}

// escapedToLines replaces literal "\n" sequences with real newlines.
func escapedToLines(s string) string {
	return strings.ReplaceAll(s, `\n`, "\n")
}

// toAlternatingCase alternates upper/lower on each alphabetic character.
func toAlternatingCase(s string) string {
	runes := []rune(s)
	upper := true
	for i, r := range runes {
		if unicode.IsLetter(r) {
			if upper {
				runes[i] = unicode.ToUpper(r)
			} else {
				runes[i] = unicode.ToLower(r)
			}
			upper = !upper
		}
	}
	return string(runes)
}
