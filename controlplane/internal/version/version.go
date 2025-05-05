package version

// version is the version of the controlplane.
//
// This value is expected to be set via build-time injection.
var version string

// Version returns the version of the controlplane.
func Version() string {
	return version
}
