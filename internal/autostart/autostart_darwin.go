//go:build darwin

package autostart

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const launchdLabel = "local.ai-gateway.gateway"

type darwinRegistrar struct {
	executable string
	plistPath  string
	domain     string
	runner     commandRunner
}

func newPlatform(executable string) Registrar {
	home, err := os.UserHomeDir()
	if err != nil {
		return &unavailableRegistrar{err: fmt.Errorf("locate user home: %w", err)}
	}
	return &darwinRegistrar{
		executable: executable,
		plistPath:  filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist"),
		domain:     "gui/" + strconv.Itoa(os.Getuid()),
		runner:     execRunner{},
	}
}

func (r *darwinRegistrar) Enable() error {
	content := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>` + launchdLabel + `</string>
<key>ProgramArguments</key><array><string>` + xmlEscape(r.executable) + `</string><string>` + ServeArgument + `</string></array>
<key>RunAtLoad</key><true/>
</dict></plist>
`
	if err := writeAtomic(r.plistPath, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write user launchd plist: %w", err)
	}
	if _, err := r.runner.Run("launchctl", "print", r.domain+"/"+launchdLabel); err == nil {
		if output, err := r.runner.Run("launchctl", "bootout", r.domain+"/"+launchdLabel); err != nil {
			return commandErrorPortable("unload prior user launchd job", output, err)
		}
	}
	if output, err := r.runner.Run("launchctl", "bootstrap", r.domain, r.plistPath); err != nil {
		return commandErrorPortable("bootstrap user launchd job", output, err)
	}
	status, err := r.Status()
	if err != nil || !status.Valid {
		if err != nil {
			return fmt.Errorf("verify user launchd job: %w", err)
		}
		return fmt.Errorf("verify user launchd job: %s", status.Issue)
	}
	return nil
}

func (r *darwinRegistrar) Disable() error {
	if _, err := os.Stat(r.plistPath); err == nil {
		if _, err := r.runner.Run("launchctl", "print", r.domain+"/"+launchdLabel); err == nil {
			if output, err := r.runner.Run("launchctl", "bootout", r.domain+"/"+launchdLabel); err != nil {
				return commandErrorPortable("unload user launchd job", output, err)
			}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect user launchd plist: %w", err)
	}
	if err := os.Remove(r.plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove user launchd plist: %w", err)
	}
	return nil
}

func (r *darwinRegistrar) Status() (Registration, error) {
	data, err := os.ReadFile(r.plistPath)
	if os.IsNotExist(err) {
		return Registration{}, nil
	}
	if err != nil {
		return Registration{}, err
	}
	arguments, parseErr := parseLaunchdArguments(data)
	_, printErr := r.runner.Run("launchctl", "print", r.domain+"/"+launchdLabel)
	registration := Registration{Exists: true, Enabled: printErr == nil}
	if parseErr == nil && len(arguments) > 0 {
		registration.Executable = arguments[0]
		registration.Arguments = arguments[1:]
	}
	registration.Valid = registration.Enabled && parseErr == nil && registration.Executable == r.executable && len(registration.Arguments) == 1 && registration.Arguments[0] == ServeArgument
	if !registration.Valid {
		if parseErr != nil {
			registration.Issue = parseErr.Error()
		} else {
			registration.Issue = fmt.Sprintf("user launchd job is unloaded or registered command %q %q does not match %q %q", registration.Executable, strings.Join(registration.Arguments, " "), r.executable, ServeArgument)
		}
	}
	return registration, nil
}

func xmlEscape(value string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(value))
	return b.String()
}

func commandErrorPortable(action string, output []byte, err error) error {
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %w: %s", action, err, detail)
}
