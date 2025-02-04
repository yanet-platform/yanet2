package structures

// LPMConfig describes the Longest Prefix Match (LPM) structure configuration.
type LPMConfig struct {
	// KeySize is the size of the key in bytes.
	KeySize uint16 `json:"key_size" yaml:"key_size"`
	// ValueSize is the size of the value in bytes.
	ValueSize uint64 `json:"value_size" yaml:"value_size"`
	// MemoryLimit is the maximum amount of memory that can be allocated for
	// the LPM structure.
	MemoryLimit uint64 `json:"memory_limit" yaml:"memory_limit"`
}

// LPM is a Longest Prefix Match (LPM) structure.
type LPM struct {
	// TODO: somehow identify this structure, using pointer?
}
