package point

import "errors"

var ErrPersistentEnvironmentUnavailable = errors.New("persistent user environment is unavailable on this platform")

type UserEnvironment interface {
	Lookup(name string) (value string, exists bool, err error)
	Set(name, value string) error
	Unset(name string) error
}

type MemoryEnvironment struct{ Values map[string]string }

func NewMemoryEnvironment() *MemoryEnvironment {
	return &MemoryEnvironment{Values: map[string]string{}}
}
func (e *MemoryEnvironment) Lookup(name string) (string, bool, error) {
	v, ok := e.Values[name]
	return v, ok, nil
}
func (e *MemoryEnvironment) Set(name, value string) error { e.Values[name] = value; return nil }
func (e *MemoryEnvironment) Unset(name string) error      { delete(e.Values, name); return nil }
