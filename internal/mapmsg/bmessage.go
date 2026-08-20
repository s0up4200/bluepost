package mapmsg

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/s0up4200/bluepost/internal/protocol"
)

type Message struct {
	Sender     string
	SenderName string
	Body       string
	Status     string
	Type       string
	Folder     string
}

func Parse(reader io.Reader, maximum int64) (Message, error) {
	if maximum <= 0 {
		return Message{}, errors.New("bMessage byte limit must be positive")
	}
	blob, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil {
		return Message{}, fmt.Errorf("read bMessage: %w", err)
	}
	if int64(len(blob)) > maximum {
		return Message{}, errors.New("bMessage exceeds the byte limit")
	}

	lines := splitLines(string(blob))
	if !hasMarker(lines, "BEGIN:BMSG") || !hasMarker(lines, "END:BMSG") ||
		!hasMarker(lines, "BEGIN:BENV") || !hasMarker(lines, "BEGIN:BBODY") {
		return Message{}, errors.New("bMessage structure is incomplete")
	}

	headerEnd := markerIndex(lines, "BEGIN:BENV")
	message := parseHeader(lines[:headerEnd])
	body, ok := messageBody(lines)
	if !ok {
		return Message{}, errors.New("bMessage has no complete MSG block")
	}
	message.Body = body
	return message, nil
}

func splitLines(blob string) []string {
	blob = strings.ReplaceAll(blob, "\r\n", "\n")
	blob = strings.ReplaceAll(blob, "\r", "\n")
	return strings.Split(blob, "\n")
}

func markerIndex(lines []string, wanted string) int {
	for index, line := range lines {
		if strings.EqualFold(strings.TrimSpace(line), wanted) {
			return index
		}
	}
	return -1
}

func hasMarker(lines []string, wanted string) bool {
	return markerIndex(lines, wanted) >= 0
}

func parseHeader(lines []string) Message {
	var message Message
	var senderPhone string
	var senderEmail string
	inOriginator := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		upper := strings.ToUpper(trimmed)
		switch upper {
		case "BEGIN:VCARD":
			if !inOriginator {
				inOriginator = true
			}
			continue
		case "END:VCARD":
			inOriginator = false
			continue
		}
		left, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		property := strings.ToUpper(strings.SplitN(left, ";", 2)[0])
		if !inOriginator {
			switch property {
			case "STATUS":
				message.Status = bounded(value, 512)
			case "TYPE":
				message.Type = bounded(value, 512)
			case "FOLDER":
				message.Folder = bounded(value, 512)
			}
			continue
		}
		switch property {
		case "FN":
			message.SenderName = bounded(value, protocol.MaxContactNameChars)
		case "TEL":
			if senderPhone == "" {
				senderPhone = bounded(value, protocol.MaxContactAddressChars)
			}
		case "EMAIL":
			if senderEmail == "" {
				senderEmail = bounded(value, protocol.MaxContactAddressChars)
			}
		}
	}
	message.Sender = senderPhone
	if message.Sender == "" {
		message.Sender = senderEmail
	}
	return message
}

func messageBody(lines []string) (string, bool) {
	insideBody := false
	start := -1
	indent := ""
	for index, line := range lines {
		content := strings.TrimLeft(line, " \t")
		lineIndent := line[:len(line)-len(content)]
		marker := strings.ToUpper(strings.TrimSpace(content))
		switch marker {
		case "BEGIN:BBODY":
			insideBody = true
		case "END:BBODY":
			insideBody = false
		case "BEGIN:MSG":
			if insideBody && start < 0 {
				start = index + 1
				indent = lineIndent
			}
		case "END:MSG":
			if insideBody && start >= 0 && lineIndent == indent {
				bodyLines := append([]string(nil), lines[start:index]...)
				for bodyIndex, bodyLine := range bodyLines {
					if strings.HasPrefix(bodyLine, " ") {
						unstuffed := bodyLine[1:]
						upper := strings.ToUpper(unstuffed)
						if strings.HasPrefix(upper, "BEGIN:") || strings.HasPrefix(upper, "END:") {
							bodyLines[bodyIndex] = unstuffed
						}
					}
				}
				return strings.Trim(strings.Join(bodyLines, "\n"), "\n "), true
			}
		}
	}
	return "", false
}

func bounded(value string, maximum int) string {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) <= maximum {
		return value
	}
	return string([]rune(value)[:maximum])
}
