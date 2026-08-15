package autostart

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

func parseSystemdUnit(content string) (string, []string, error) {
	for _, line := range strings.Split(content, "\n") {
		value, ok := strings.CutPrefix(strings.TrimSpace(line), "ExecStart=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			break
		}
		if value[0] == '"' {
			quoted, err := strconv.QuotedPrefix(value)
			if err != nil {
				return "", nil, fmt.Errorf("parse user systemd ExecStart: %w", err)
			}
			executable, err := strconv.Unquote(quoted)
			if err != nil {
				return "", nil, fmt.Errorf("parse user systemd executable: %w", err)
			}
			return executable, strings.Fields(strings.TrimSpace(value[len(quoted):])), nil
		}
		fields := strings.Fields(value)
		if len(fields) > 0 {
			return fields[0], fields[1:], nil
		}
	}
	return "", nil, fmt.Errorf("user systemd unit has no ExecStart")
}

func parseLaunchdArguments(data []byte) ([]string, error) {
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	foundKey := false
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse user launchd plist: %w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local == "key" {
			var key string
			if err := decoder.DecodeElement(&key, &start); err != nil {
				return nil, fmt.Errorf("parse user launchd key: %w", err)
			}
			foundKey = key == "ProgramArguments"
			continue
		}
		if foundKey && start.Name.Local == "array" {
			var values struct {
				Strings []string `xml:"string"`
			}
			if err := decoder.DecodeElement(&values, &start); err != nil {
				return nil, fmt.Errorf("parse user launchd ProgramArguments: %w", err)
			}
			if len(values.Strings) == 0 {
				return nil, fmt.Errorf("user launchd ProgramArguments is empty")
			}
			return values.Strings, nil
		}
	}
	return nil, fmt.Errorf("user launchd plist has no ProgramArguments")
}
