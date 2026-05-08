package version

// version is the version of the bird-adapter.
//
// This value is expected to be set via build-time injection.
var version string

// Version returns the version of the bird-adapter.
func Version() string {
	return version
}
