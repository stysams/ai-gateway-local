//go:build !windows

package point

type systemEnvironment struct{}

func SystemEnvironment() UserEnvironment { return systemEnvironment{} }
func (systemEnvironment) Lookup(string) (string, bool, error) {
	return "", false, ErrPersistentEnvironmentUnavailable
}
func (systemEnvironment) Set(string, string) error { return ErrPersistentEnvironmentUnavailable }
func (systemEnvironment) Unset(string) error       { return ErrPersistentEnvironmentUnavailable }
