//go:build !windows && !linux && !darwin

package autostart

func newPlatform(string) Registrar {
	return &unavailableRegistrar{err: ErrNotSupported}
}
