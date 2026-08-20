package obex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	transferInterface = "org.bluez.obex.Transfer1"
	propertiesGet     = "org.freedesktop.DBus.Properties.Get"
)

func transferResult(body []any) (dbus.ObjectPath, string, error) {
	if len(body) == 0 {
		return "", "", errors.New("OBEX transfer returned an invalid response")
	}
	path, ok := body[0].(dbus.ObjectPath)
	if !ok || !path.IsValid() {
		return "", "", errors.New("OBEX transfer returned an invalid path")
	}
	status := "queued"
	if len(body) > 1 {
		if properties, ok := body[1].(map[string]dbus.Variant); ok {
			status = strings.ToLower(variantString(properties["Status"]))
		}
	}
	return path, status, nil
}

func waitTransfer(
	ctx context.Context,
	transport Transport,
	path dbus.ObjectPath,
	initialStatus string,
	outputPath string,
	maximum int64,
) error {
	status := strings.ToLower(initialStatus)
	if status == "" {
		status = "queued"
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := enforceFileSize(outputPath, maximum); err != nil {
			return err
		}
		switch status {
		case "complete":
			return nil
		case "error":
			return errors.New("OBEX transfer reported an error")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		body, err := transport.Call(
			ctx,
			obexDestination,
			path,
			propertiesGet,
			transferInterface,
			"Status",
		)
		if err != nil {
			if disappearedError(err) {
				return nil
			}
			return fmt.Errorf("read OBEX transfer status: %w", err)
		}
		if len(body) != 1 {
			return errors.New("OBEX transfer status response is invalid")
		}
		variant, ok := body[0].(dbus.Variant)
		if !ok {
			return errors.New("OBEX transfer status has an invalid type")
		}
		status, _ = variant.Value().(string)
		status = strings.ToLower(status)
	}
}

func waitForFile(ctx context.Context, path string, maximum int64) error {
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		info, err := os.Lstat(path)
		if err == nil {
			if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return errors.New("OBEX output is not a regular file")
			}
			if info.Size() > maximum {
				return errors.New("OBEX output exceeds the byte limit")
			}
			if info.Size() > 0 {
				return nil
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect OBEX output: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("OBEX transfer produced no output")
		case <-ticker.C:
		}
	}
}

func enforceFileSize(path string, maximum int64) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Size() > maximum {
		return errors.New("OBEX output exceeds the byte limit")
	}
	return nil
}

func PrepareRuntimeDir(runtimeDir string) error {
	if runtimeDir == "" || !filepath.IsAbs(runtimeDir) || filepath.Clean(runtimeDir) == string(filepath.Separator) {
		return errors.New("runtime directory is unsafe")
	}
	info, err := os.Lstat(runtimeDir)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
			return fmt.Errorf("create runtime directory: %w", err)
		}
		if err := os.Chmod(runtimeDir, 0o700); err != nil {
			return fmt.Errorf("protect runtime directory: %w", err)
		}
		info, err = os.Lstat(runtimeDir)
	}
	if err != nil {
		return fmt.Errorf("inspect runtime directory: %w", err)
	}
	stat, owned := info.Sys().(*syscall.Stat_t)
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm() != 0o700 ||
		!owned || stat.Uid != uint32(os.Getuid()) {
		return errors.New("runtime directory has unsafe ownership or permissions")
	}
	return nil
}

func privateTemp(runtimeDir, pattern string) (string, error) {
	if err := PrepareRuntimeDir(runtimeDir); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(runtimeDir, pattern)
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

func disappearedError(err error) bool {
	var dbusError dbus.Error
	if errors.As(err, &dbusError) {
		return dbusError.Name == "org.freedesktop.DBus.Error.UnknownObject" ||
			dbusError.Name == "org.bluez.obex.Error.NotFound"
	}
	text := err.Error()
	return strings.Contains(text, "UnknownObject") || strings.Contains(text, "NotFound")
}
