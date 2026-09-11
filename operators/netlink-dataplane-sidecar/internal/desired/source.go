package desired

// Source loads interface configuration once before runtime resources open.
type Source interface {
	Load() (State, error)
}
