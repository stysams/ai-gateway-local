//go:build linux

package autostart

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type linuxRegistrar struct {
	executable string
	unitPath   string
	runner     commandRunner
}

func newPlatform(executable string) Registrar {
	home, err := os.UserHomeDir()
	if err != nil {
		return &unavailableRegistrar{err: fmt.Errorf("locate user home: %w", err)}
	}
	return &linuxRegistrar{
		executable: executable,
		unitPath:   filepath.Join(home, ".config", "systemd", "user", "ai-gateway.service"),
		runner:     execRunner{},
	}
}

func (r *linuxRegistrar) Enable() error {
	content := "[Unit]\nDescription=ai-gateway local AI gateway\n\n" +
		"[Service]\nType=simple\nExecStart=" + strconv.Quote(r.executable) + " " + ServeArgument + "\nRestart=on-failure\n\n" +
		"[Install]\nWantedBy=default.target\n"
	if err := writeAtomic(r.unitPath, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write user systemd unit: %w", err)
	}
	if output, err := r.runner.Run("systemctl", "--user", "daemon-reload"); err != nil {
		return commandErrorPortable("reload user systemd", output, err)
	}
	if output, err := r.runner.Run("systemctl", "--user", "enable", "ai-gateway.service"); err != nil {
		return commandErrorPortable("enable user systemd unit", output, err)
	}
	status, err := r.Status()
	if err != nil || !status.Valid {
		if err != nil {
			return fmt.Errorf("verify user systemd unit: %w", err)
		}
		return fmt.Errorf("verify user systemd unit: %s", status.Issue)
	}
	return nil
}

func (r *linuxRegistrar) Disable() error {
	if _, err := os.Stat(r.unitPath); err == nil {
		if output, err := r.runner.Run("systemctl", "--user", "disable", "ai-gateway.service"); err != nil {
			return commandErrorPortable("disable user systemd unit", output, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect user systemd unit: %w", err)
	}
	if err := os.Remove(r.unitPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove user systemd unit: %w", err)
	}
	if output, err := r.runner.Run("systemctl", "--user", "daemon-reload"); err != nil {
		return commandErrorPortable("reload user systemd", output, err)
	}
	return nil
}

func (r *linuxRegistrar) Status() (Registration, error) {
	data, err := os.ReadFile(r.unitPath)
	if os.IsNotExist(err) {
		return Registration{}, nil
	}
	if err != nil {
		return Registration{}, err
	}
	executable, arguments, parseErr := parseSystemdUnit(string(data))
	validFile := parseErr == nil && executable == r.executable && len(arguments) == 1 && arguments[0] == ServeArgument
	_, enabledErr := r.runner.Run("systemctl", "--user", "is-enabled", "ai-gateway.service")
	registration := Registration{Exists: true, Enabled: enabledErr == nil, Valid: enabledErr == nil && validFile, Executable: executable, Arguments: arguments}
	if !registration.Valid {
		if parseErr != nil {
			registration.Issue = parseErr.Error()
		} else {
			registration.Issue = fmt.Sprintf("user systemd unit is disabled or registered command %q %q does not match %q %q", executable, strings.Join(arguments, " "), r.executable, ServeArgument)
		}
	}
	return registration, nil
}

func commandErrorPortable(action string, output []byte, err error) error {
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %w: %s", action, err, detail)
}
