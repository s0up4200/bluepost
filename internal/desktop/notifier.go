package desktop

import (
	"context"
	"fmt"
	"html"
	"io"
	"os/exec"
	"strings"
	"unicode"

	"github.com/s0up4200/bluepost/internal/model"
	"github.com/s0up4200/bluepost/internal/otp"
)

type commandRunner func(context.Context, string, []string, string) (string, error)

type Notifier struct {
	run    commandRunner
	errors io.Writer
}

func NewNotifier(errors io.Writer) *Notifier {
	return &Notifier{run: runCommand, errors: errors}
}

func (notifier *Notifier) Notify(ctx context.Context, message model.Message) {
	go notifier.notify(ctx, message)
}

func (notifier *Notifier) notify(ctx context.Context, message model.Message) {
	title := safeMarkup(message.ContactName)
	if title == "" {
		title = safeMarkup(message.SenderAddress)
	}
	if title == "" {
		title = "New message"
	}
	code, hasCode := otp.Extract(message.Body)
	args := []string{"--app-name=Bluepost", "--icon=mail-unread-symbolic"}
	if hasCode {
		args = append(args, "--action=default=Copy code")
	}
	args = append(args, title, safeMarkup(message.Body))
	action, err := notifier.run(ctx, "notify-send", args, "")
	if err != nil {
		notifier.report(ctx, "Could not show message notification")
		return
	}
	if !hasCode || action != "default" {
		return
	}
	if _, err := notifier.run(ctx, "wl-copy", []string{"--sensitive", "--trim-newline"}, code); err != nil {
		notifier.report(ctx, "Could not copy authentication code")
	}
}

func (notifier *Notifier) report(ctx context.Context, message string) {
	if ctx.Err() == nil && notifier.errors != nil {
		fmt.Fprintln(notifier.errors, message)
	}
}

func runCommand(ctx context.Context, name string, args []string, input string) (string, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Stdin = strings.NewReader(input)
	output, err := command.Output()
	return strings.TrimSpace(string(output)), err
}

func safeMarkup(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	value = strings.ToValidUTF8(value, "�")
	value = strings.Map(func(char rune) rune {
		if char == '\n' || char == '\t' || !unicode.IsControl(char) {
			return char
		}
		return -1
	}, value)
	return html.EscapeString(value)
}
