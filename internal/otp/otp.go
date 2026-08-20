package otp

import (
	"regexp"
	"strconv"
	"strings"
)

const (
	candidatePattern = `[A-Za-z0-9]{4,10}`
	strongPhrase     = `(?:verification[ \t]+code|security[ \t]+code|authentication[ \t]+code|one[- ]time[ \t]+(?:code|password)|passcode|otp|2fa(?:[ \t]+code)?|bekreftelseskoden?|sikkerhetskoden?|engangskoden?|innloggingskoden?)`
	connector        = `(?:[ \t]+(?:your|din|ditt|is|er))*[ \t]*[:=]?[ \t]*`
)

var (
	domainBoundLine = regexp.MustCompile(`^@[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])? #(` + candidatePattern + `)(?: [@%][A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?)?$`)
	googleHashLine  = regexp.MustCompile(`^[A-Za-z0-9+/]{11}$`)
	strongBefore    = regexp.MustCompile(`(?i)\b` + strongPhrase + `\b` + connector + `(` + candidatePattern + `)`)
	strongAfter     = regexp.MustCompile(`(?i)\b(` + candidatePattern + `)[ \t]+(?:is|er)[ \t]+(?:your|din|ditt)?[ \t]*` + strongPhrase + `\b`)
	weakBefore      = regexp.MustCompile(`(?i)\b(?:code|kode)\b` + connector + `([0-9]{4,10})`)
	weakAfter       = regexp.MustCompile(`(?i)\b([0-9]{4,10})[ \t]+(?:is|er)[ \t]+(?:your|din|ditt)?[ \t]*(?:code|kode)\b`)
	googleBefore    = regexp.MustCompile(`(?i)\b(?:code|kode)\b` + connector + `(` + candidatePattern + `)`)
	ambiguousPair   = regexp.MustCompile(`(?i)\b(` + candidatePattern + `)[ \t]+(?:or|and|eller|og)[ \t]+(` + candidatePattern + `)\b`)
	urlSpan         = regexp.MustCompile(`(?i)(?:https?://|www\.|(?:[a-z0-9-]+\.)+[a-z]{2,}[/?#])[^\s<>"']+`)
)

func Extract(body string) (string, bool) {
	text := normalize(body)
	if code, ok := domainBound(text); ok {
		return code, true
	}
	hasGoogleHash := googleHashLine.MatchString(lastLine(text))
	if hasGoogleHash {
		text = withoutLastLine(text)
	}
	candidates := contextualCandidates(text, hasGoogleHash)
	if len(candidates) != 1 {
		return "", false
	}
	return candidates[0], true
}

func normalize(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")
	return strings.TrimRight(body, " \t\n")
}

func lastLine(text string) string {
	if index := strings.LastIndexByte(text, '\n'); index >= 0 {
		return text[index+1:]
	}
	return text
}

func withoutLastLine(text string) string {
	if index := strings.LastIndexByte(text, '\n'); index >= 0 {
		return strings.TrimRight(text[:index], " \t\n")
	}
	return ""
}

func domainBound(text string) (string, bool) {
	match := domainBoundLine.FindStringSubmatch(lastLine(text))
	if len(match) != 2 || !validToken(match[1]) {
		return "", false
	}
	return match[1], true
}

func contextualCandidates(text string, hasGoogleHash bool) []string {
	if hasAmbiguousPair(text) {
		return nil
	}
	strong := make(map[string]struct{})
	collect(strong, text, strongBefore)
	collect(strong, text, strongAfter)
	if hasGoogleHash {
		collect(strong, text, googleBefore)
	}
	if len(strong) > 0 {
		return candidateList(strong)
	}
	weak := make(map[string]struct{})
	collect(weak, text, weakBefore)
	collect(weak, text, weakAfter)
	return candidateList(weak)
}

func candidateList(unique map[string]struct{}) []string {
	result := make([]string, 0, len(unique))
	for code := range unique {
		result = append(result, code)
	}
	return result
}

func collect(unique map[string]struct{}, text string, pattern *regexp.Regexp) {
	for _, indexes := range pattern.FindAllStringSubmatchIndex(text, -1) {
		if len(indexes) < 4 {
			continue
		}
		start, end := indexes[2], indexes[3]
		if start < 0 || end < 0 || !validCapture(text, start, end) {
			continue
		}
		unique[text[start:end]] = struct{}{}
	}
}

func hasAmbiguousPair(text string) bool {
	for _, indexes := range ambiguousPair.FindAllStringSubmatchIndex(text, -1) {
		if len(indexes) < 6 {
			continue
		}
		leftStart, leftEnd := indexes[2], indexes[3]
		rightStart, rightEnd := indexes[4], indexes[5]
		if validCapture(text, leftStart, leftEnd) && validCapture(text, rightStart, rightEnd) &&
			text[leftStart:leftEnd] != text[rightStart:rightEnd] {
			return true
		}
	}
	return false
}

func validCapture(text string, start, end int) bool {
	if start < 0 || end > len(text) || start >= end || !validToken(text[start:end]) {
		return false
	}
	if insideURL(text, start, end) {
		return false
	}
	if start > 0 && forbiddenLeftNeighbor(text, start) {
		return false
	}
	if end < len(text) && forbiddenRightNeighbor(text, end) {
		return false
	}
	return !compactDate(text[start:end])
}

func insideURL(text string, start, end int) bool {
	for _, span := range urlSpan.FindAllStringIndex(text, -1) {
		if start >= span[0] && end <= span[1] {
			return true
		}
	}
	return false
}

func validToken(value string) bool {
	if len(value) < 4 || len(value) > 10 {
		return false
	}
	hasDigit := false
	for index := 0; index < len(value); index++ {
		char := value[index]
		if char >= '0' && char <= '9' {
			hasDigit = true
			continue
		}
		if (char < 'A' || char > 'Z') && (char < 'a' || char > 'z') {
			return false
		}
	}
	return hasDigit
}

func forbiddenLeftNeighbor(text string, start int) bool {
	char := text[start-1]
	if asciiAlphaNumeric(char) || strings.ContainsRune("_@/+-", rune(char)) {
		return true
	}
	return char == '.' && start > 1 && asciiAlphaNumeric(text[start-2])
}

func forbiddenRightNeighbor(text string, end int) bool {
	char := text[end]
	if asciiAlphaNumeric(char) || strings.ContainsRune("_@/+-", rune(char)) {
		return true
	}
	return char == '.' && end+1 < len(text) && asciiAlphaNumeric(text[end+1])
}

func asciiAlphaNumeric(char byte) bool {
	return char >= '0' && char <= '9' || char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z'
}

func compactDate(value string) bool {
	if len(value) != 8 {
		return false
	}
	for index := range value {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	year, _ := strconv.Atoi(value[:4])
	month, _ := strconv.Atoi(value[4:6])
	day, _ := strconv.Atoi(value[6:])
	return year >= 1900 && year <= 2100 && month >= 1 && month <= 12 && day >= 1 && day <= 31
}
