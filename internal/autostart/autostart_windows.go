//go:build windows

package autostart

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

const windowsTaskPath = `\ai-gateway`

type windowsRegistrar struct {
	executable string
	runner     commandRunner
}

func newPlatform(executable string) Registrar {
	return &windowsRegistrar{executable: executable, runner: execRunner{}}
}

func (r *windowsRegistrar) Enable() error {
	taskRun := `"` + r.executable + `" ` + ServeArgument
	output, err := r.runner.Run("schtasks.exe",
		"/Create", "/TN", windowsTaskPath, "/TR", taskRun,
		"/SC", "ONLOGON", "/IT", "/RL", "LIMITED", "/F", "/HRESULT")
	if err != nil {
		return commandError("create current-user login task", output, err)
	}
	status, err := r.Status()
	if err != nil {
		return fmt.Errorf("verify current-user login task: %w", err)
	}
	if !status.Enabled || !status.Valid {
		return fmt.Errorf("verify current-user login task: %s", status.Issue)
	}
	return nil
}

func (r *windowsRegistrar) Disable() error {
	output, err := r.runner.Run("schtasks.exe", "/Delete", "/TN", windowsTaskPath, "/F", "/HRESULT")
	if err != nil && !taskNotFound(err) {
		return commandError("delete current-user login task", output, err)
	}
	status, err := r.Status()
	if err != nil {
		return fmt.Errorf("verify current-user login task removal: %w", err)
	}
	if status.Exists {
		return fmt.Errorf("verify current-user login task removal: task still exists")
	}
	return nil
}

func (r *windowsRegistrar) Status() (Registration, error) {
	output, err := r.runner.Run("schtasks.exe", "/Query", "/TN", windowsTaskPath, "/XML", "/HRESULT")
	if err != nil {
		if taskNotFound(err) {
			return Registration{}, nil
		}
		return Registration{}, commandError("query current-user login task", output, err)
	}
	registration, err := parseWindowsTask(output)
	if err != nil {
		return Registration{}, fmt.Errorf("parse current-user login task: %w", err)
	}
	registration.Exists = true
	registration.Valid = registration.Enabled && sameWindowsPath(registration.Executable, r.executable) &&
		len(registration.Arguments) == 1 && registration.Arguments[0] == ServeArgument &&
		registration.Issue == ""
	if !registration.Enabled && registration.Issue == "" {
		registration.Issue = "registration exists but is disabled"
	} else if !registration.Valid && registration.Issue == "" {
		registration.Issue = fmt.Sprintf("registered command %q %q does not match %q %q",
			registration.Executable, strings.Join(registration.Arguments, " "), r.executable, ServeArgument)
	}
	return registration, nil
}

type windowsTaskXML struct {
	Triggers struct {
		Logon []struct{} `xml:"LogonTrigger"`
	} `xml:"Triggers"`
	Actions struct {
		Exec []struct {
			Command   string `xml:"Command"`
			Arguments string `xml:"Arguments"`
		} `xml:"Exec"`
	} `xml:"Actions"`
	Settings struct {
		Enabled *bool `xml:"Enabled"`
	} `xml:"Settings"`
}

func parseWindowsTask(data []byte) (Registration, error) {
	var task windowsTaskXML
	if err := xml.Unmarshal(normalizeWindowsTaskXML(data), &task); err != nil {
		return Registration{}, err
	}
	if len(task.Actions.Exec) != 1 {
		return Registration{Issue: fmt.Sprintf("expected one Exec action, found %d", len(task.Actions.Exec))}, nil
	}
	action := task.Actions.Exec[0]
	executable := strings.Trim(strings.TrimSpace(action.Command), `"`)
	arguments := strings.Fields(action.Arguments)
	if task.Settings.Enabled == nil {
		return Registration{Exists: true, Executable: executable, Arguments: arguments, Issue: "registration does not declare whether it is enabled"}, nil
	}
	if len(task.Triggers.Logon) != 1 {
		return Registration{Exists: true, Enabled: *task.Settings.Enabled, Executable: executable, Arguments: arguments, Issue: "registration does not contain exactly one logon trigger"}, nil
	}
	return Registration{Exists: true, Enabled: *task.Settings.Enabled, Executable: executable, Arguments: arguments}, nil
}

func normalizeWindowsTaskXML(data []byte) []byte {
	text := ""
	switch {
	case len(data) >= 2 && bytes.Equal(data[:2], []byte{0xff, 0xfe}):
		text = decodeUTF16(data[2:], binary.LittleEndian)
	case len(data) >= 2 && bytes.Equal(data[:2], []byte{0xfe, 0xff}):
		text = decodeUTF16(data[2:], binary.BigEndian)
	case len(data) >= 4 && data[1] == 0 && data[3] == 0:
		text = decodeUTF16(data, binary.LittleEndian)
	case len(data) >= 4 && data[0] == 0 && data[2] == 0:
		text = decodeUTF16(data, binary.BigEndian)
	default:
		text = string(data)
	}
	// PowerShell and some test runners transcode stdout to UTF-8 without
	// changing schtasks' XML declaration. The bytes are authoritative here.
	text = strings.Replace(text, `encoding="UTF-16"`, `encoding="UTF-8"`, 1)
	text = strings.Replace(text, `encoding="utf-16"`, `encoding="UTF-8"`, 1)
	return []byte(text)
}

func decodeUTF16(data []byte, order binary.ByteOrder) string {
	units := make([]uint16, 0, len(data)/2)
	for len(data) >= 2 {
		units = append(units, order.Uint16(data[:2]))
		data = data[2:]
	}
	return string(utf16.Decode(units))
}

func sameWindowsPath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(strings.TrimSpace(a)), filepath.Clean(strings.TrimSpace(b)))
}

func taskNotFound(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	// /HRESULT makes ERROR_FILE_NOT_FOUND (0x80070002) locale independent.
	return exitErr.ExitCode() == -2147024894 || exitErr.ExitCode() == 2 ||
		strings.Contains(strings.ToLower(exitErr.Error()), "0x80070002")
}

func commandError(action string, output []byte, err error) error {
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %w: %s", action, err, detail)
}
