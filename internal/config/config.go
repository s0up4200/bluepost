package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
)

var phonePattern = regexp.MustCompile(`^(?:[0-9A-F]{2}:){5}[0-9A-F]{2}$`)

type Config struct {
	Phone      string
	StateDir   string
	RuntimeDir string
}

func Load(args []string, getenv func(string) string) (Config, error) {
	flags := flag.NewFlagSet("bluepostd", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	phoneFlag := flags.String("phone", "", "paired and trusted iPhone MAC address")
	if err := flags.Parse(args); err != nil {
		return Config{}, err
	}
	if flags.NArg() != 0 {
		return Config{}, errors.New("bluepostd does not accept positional arguments")
	}

	phone := strings.ToUpper(strings.TrimSpace(*phoneFlag))
	if phone == "" {
		phone = strings.ToUpper(strings.TrimSpace(getenv("BLUEPOST_PHONE")))
	}
	if !phonePattern.MatchString(phone) {
		return Config{}, errors.New("phone must use the XX:XX:XX:XX:XX:XX format")
	}

	stateRoot := strings.TrimSpace(getenv("XDG_STATE_HOME"))
	if stateRoot == "" {
		home := strings.TrimSpace(getenv("HOME"))
		if home == "" {
			return Config{}, errors.New("XDG_STATE_HOME and HOME are not set")
		}
		stateRoot = filepath.Join(home, ".local", "state")
	}
	runtimeRoot := strings.TrimSpace(getenv("XDG_RUNTIME_DIR"))
	if runtimeRoot == "" {
		return Config{}, errors.New("XDG_RUNTIME_DIR is not set")
	}
	if err := validateRoot("state", stateRoot); err != nil {
		return Config{}, err
	}
	if err := validateRoot("runtime", runtimeRoot); err != nil {
		return Config{}, err
	}

	return Config{
		Phone:      phone,
		StateDir:   filepath.Join(filepath.Clean(stateRoot), "bluepost"),
		RuntimeDir: filepath.Join(filepath.Clean(runtimeRoot), "bluepost"),
	}, nil
}

func validateRoot(name, path string) error {
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) || clean == string(filepath.Separator) {
		return fmt.Errorf("%s directory root must be a safe absolute path", name)
	}
	return nil
}
