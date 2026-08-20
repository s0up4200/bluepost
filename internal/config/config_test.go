package config

import (
	"path/filepath"
	"testing"
)

func TestLoadRejectsUntrustedPhoneSyntax(t *testing.T) {
	t.Parallel()

	_, err := Load(
		"AA:BB;rm -rf",
		testEnv(t.TempDir(), t.TempDir(), ""),
	)
	if err == nil {
		t.Fatal("expected invalid phone error")
	}
}

func TestLoadUsesFlagBeforeEnvironment(t *testing.T) {
	t.Parallel()

	stateRoot := t.TempDir()
	runtimeRoot := t.TempDir()
	got, err := Load(
		"aa:bb:cc:dd:ee:ff",
		testEnv(stateRoot, runtimeRoot, "11:22:33:44:55:66"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.Phone != "AA:BB:CC:DD:EE:FF" {
		t.Fatalf("phone %q", got.Phone)
	}
	if got.StateDir != filepath.Join(stateRoot, "bluepost") {
		t.Fatalf("state directory %q", got.StateDir)
	}
	if got.RuntimeDir != filepath.Join(runtimeRoot, "bluepost") {
		t.Fatalf("runtime directory %q", got.RuntimeDir)
	}
}

func TestLoadRequiresRuntimeDirectory(t *testing.T) {
	t.Parallel()

	_, err := Load("", testEnv(t.TempDir(), "", "AA:BB:CC:DD:EE:FF"))
	if err == nil {
		t.Fatal("expected missing XDG_RUNTIME_DIR error")
	}
}

func testEnv(stateRoot, runtimeRoot, phone string) func(string) string {
	values := map[string]string{
		"BLUEPOST_PHONE":  phone,
		"XDG_STATE_HOME":  stateRoot,
		"XDG_RUNTIME_DIR": runtimeRoot,
	}
	return func(key string) string { return values[key] }
}
