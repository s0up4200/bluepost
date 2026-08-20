package textsafe

import "testing"

func TestOneLineRemovesTerminalControls(t *testing.T) {
	t.Parallel()

	got := OneLine("hello\x1b[31m\nworld")
	if got != "hello[31m ⏎ world" {
		t.Fatalf("got %q", got)
	}
}

func TestOneLineCollapsesWindowsLineBreak(t *testing.T) {
	t.Parallel()

	got := OneLine("one\r\ntwo\tthree")
	if got != "one ⏎ two three" {
		t.Fatalf("got %q", got)
	}
}
