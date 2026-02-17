package auth

// Config is the configuration for authentication and authorization.
type Config struct {
	// Disabled indicates if authentication is disabled.
	//
	// When true, all requests are treated as anonymous with full permissions.
	Disabled bool `yaml:"disabled"`
}

// DefaultConfig returns the default authentication configuration.
func DefaultConfig() Config {
	return Config{
		Disabled: false, // Security by default.
	}
}
