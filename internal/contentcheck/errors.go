package contentcheck

import "fmt"

// Failure separates technical failures from content decisions for callers.
type Failure struct {
	Code  string
	Cause error
}

func (e *Failure) Error() string { return e.Cause.Error() }
func (e *Failure) Unwrap() error { return e.Cause }
func failure(code, format string, args ...any) error {
	return &Failure{Code: code, Cause: fmt.Errorf(format, args...)}
}
