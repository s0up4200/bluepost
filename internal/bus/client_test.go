package bus

import (
	"context"
	"errors"
	"testing"
)

func TestClientDecodesTypedReadResponses(t *testing.T) {
	t.Parallel()

	caller := func(_ context.Context, method string, _ ...any) ([]any, error) {
		switch method {
		case methodGetStatus:
			return []any{`{"state":"ready","map":true}`}, nil
		case methodListEvents:
			return []any{`[{"handle":"one","body":"hello"}]`}, nil
		default:
			return nil, errors.New("unexpected method")
		}
	}
	client := newClient(caller)
	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "ready" || !status.MAP {
		t.Fatalf("status %#v", status)
	}
	messages, err := client.Events(context.Background(), []string{"sms_received"}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Body != "hello" {
		t.Fatalf("messages %#v", messages)
	}
}

func TestClientRejectsMalformedAndOversizedJSON(t *testing.T) {
	t.Parallel()

	for _, response := range []string{"not-json", string(make([]byte, 9<<20))} {
		client := newClient(func(context.Context, string, ...any) ([]any, error) {
			return []any{response}, nil
		})
		if _, err := client.Status(context.Background()); err == nil {
			t.Fatalf("accepted response with %d bytes", len(response))
		}
	}
}

var _ API = (*Client)(nil)
