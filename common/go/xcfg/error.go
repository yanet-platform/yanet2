package xcfg

import "fmt"

// LineError is returned from UnmarshalYAML when a value fails validation.
type LineError struct {
	Line int
	Err  error
}

func (m *LineError) Error() string {
	return fmt.Sprintf("line %d: %s", m.Line, m.Err)
}

func (m *LineError) Unwrap() error {
	return m.Err
}

// PathError is returned from Decode's validate when a field fails validation.
type PathError struct {
	Path string
	Err  error
}

func (m *PathError) Error() string {
	return fmt.Sprintf("%s: %s", m.Path, m.Err)
}

func (m *PathError) Unwrap() error {
	return m.Err
}
