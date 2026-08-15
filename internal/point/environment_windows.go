//go:build windows

package point

import (
	"errors"
	"os"

	"golang.org/x/sys/windows/registry"
)

type systemEnvironment struct{}

func SystemEnvironment() UserEnvironment { return systemEnvironment{} }

func (systemEnvironment) Lookup(name string) (string, bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE)
	if err != nil {
		return "", false, err
	}
	defer k.Close()
	value, _, err := k.GetStringValue(name)
	if errors.Is(err, registry.ErrNotExist) {
		return "", false, nil
	}
	return value, err == nil, err
}

func (systemEnvironment) Set(name, value string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, `Environment`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.SetStringValue(name, value); err != nil {
		return err
	}
	return os.Setenv(name, value)
}

func (systemEnvironment) Unset(name string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, `Environment`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.DeleteValue(name); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return os.Unsetenv(name)
}
