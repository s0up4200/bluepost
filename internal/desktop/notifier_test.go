package desktop

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/s0up4200/bluepost/internal/model"
)

type commandCall struct {
	name  string
	args  []string
	input string
}

func TestNotifyCopiesDetectedCodeAfterDefaultAction(t *testing.T) {
	t.Parallel()

	var calls []commandCall
	notifier := &Notifier{run: func(_ context.Context, name string, args []string, input string) (string, error) {
		calls = append(calls, commandCall{name: name, args: append([]string(nil), args...), input: input})
		if name == "notify-send" {
			return "default", nil
		}
		return "", nil
	}}

	notifier.notify(context.Background(), model.Message{
		ContactName:   "Jane <Admin>",
		SenderAddress: "+4712345678",
		Body:          "<b>hello</b> Your Stripe verification code is 482731",
	})

	wantNotify := commandCall{
		name: "notify-send",
		args: []string{
			"--app-name=Bluepost",
			"--icon=mail-unread-symbolic",
			"--action=default=Copy code",
			"Jane &lt;Admin&gt;",
			"&lt;b&gt;hello&lt;/b&gt; Your Stripe verification code is 482731",
		},
	}
	wantCopy := commandCall{name: "wl-copy", args: []string{"--sensitive", "--trim-newline"}, input: "482731"}
	if !reflect.DeepEqual(calls, []commandCall{wantNotify, wantCopy}) {
		t.Fatalf("calls = %#v", calls)
	}
	for _, arg := range calls[1].args {
		if arg == "--paste-once" || strings.Contains(arg, "482731") {
			t.Fatalf("unsafe wl-copy argument %q", arg)
		}
	}
}

func TestNotifyDoesNotAddActionForOrdinarySMS(t *testing.T) {
	t.Parallel()

	var calls []commandCall
	notifier := &Notifier{run: func(_ context.Context, name string, args []string, input string) (string, error) {
		calls = append(calls, commandCall{name: name, args: append([]string(nil), args...), input: input})
		return "default", nil
	}}

	notifier.notify(context.Background(), model.Message{SenderAddress: "+4712345678", Body: "Dinner at 19:00?"})

	want := []string{"--app-name=Bluepost", "--icon=mail-unread-symbolic", "+4712345678", "Dinner at 19:00?"}
	if len(calls) != 1 || calls[0].name != "notify-send" || !reflect.DeepEqual(calls[0].args, want) {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestNotifyDoesNotCopyAfterDismissal(t *testing.T) {
	t.Parallel()

	var calls []commandCall
	notifier := &Notifier{run: func(_ context.Context, name string, args []string, input string) (string, error) {
		calls = append(calls, commandCall{name: name, args: append([]string(nil), args...), input: input})
		return "", nil
	}}

	notifier.notify(context.Background(), model.Message{Body: "Your verification code is 482731"})

	if len(calls) != 1 || calls[0].name != "notify-send" {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestNotifyEscapesUntrustedMarkup(t *testing.T) {
	t.Parallel()

	var got commandCall
	notifier := &Notifier{run: func(_ context.Context, name string, args []string, input string) (string, error) {
		got = commandCall{name: name, args: append([]string(nil), args...), input: input}
		return "", nil
	}}

	notifier.notify(context.Background(), model.Message{Body: "<b>hello</b> & goodbye"})

	if got.args[len(got.args)-1] != "&lt;b&gt;hello&lt;/b&gt; &amp; goodbye" {
		t.Fatalf("body argument = %q", got.args[len(got.args)-1])
	}
}

func TestNotifySanitizesInvalidTextAndControls(t *testing.T) {
	t.Parallel()

	var got commandCall
	notifier := &Notifier{run: func(_ context.Context, name string, args []string, input string) (string, error) {
		got = commandCall{name: name, args: append([]string(nil), args...), input: input}
		return "", nil
	}}

	notifier.notify(context.Background(), model.Message{Body: "hello\x00\x1bworld\xff\nnext\tfield"})

	if got.args[len(got.args)-1] != "helloworld�\nnext\tfield" {
		t.Fatalf("body argument = %q", got.args[len(got.args)-1])
	}
}

func TestNotifyReportsGenericCommandErrors(t *testing.T) {
	t.Parallel()

	message := model.Message{
		SenderAddress: "+4712345678",
		Body:          "Your verification code is 482731",
	}
	tests := []struct {
		name       string
		run        commandRunner
		wantErrors string
	}{
		{
			name: "notification",
			run: func(context.Context, string, []string, string) (string, error) {
				return "", errors.New("private process error")
			},
			wantErrors: "Could not show message notification\n",
		},
		{
			name: "clipboard",
			run: func(_ context.Context, name string, _ []string, _ string) (string, error) {
				if name == "notify-send" {
					return "default", nil
				}
				return "", errors.New("private process error")
			},
			wantErrors: "Could not copy authentication code\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			notifier := &Notifier{run: test.run, errors: &output}
			notifier.notify(context.Background(), message)
			if output.String() != test.wantErrors {
				t.Fatalf("error output = %q", output.String())
			}
			for _, secret := range []string{message.SenderAddress, message.Body, "482731"} {
				if strings.Contains(output.String(), secret) {
					t.Fatalf("error output contains message data: %q", output.String())
				}
			}
		})
	}
}

func TestNotifyReturnsBeforeCommandFinishes(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	notifier := &Notifier{run: func(context.Context, string, []string, string) (string, error) {
		close(started)
		<-release
		return "", nil
	}}
	returned := make(chan struct{})
	go func() {
		notifier.Notify(context.Background(), model.Message{Body: "hello"})
		close(returned)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("notification command did not start")
	}
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("Notify waited for the notification command")
	}
}
