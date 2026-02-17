package auth

// Config is the configuration for authentication and authorization.
type Config struct {
	// Disabled indicates if authentication is disabled.
	//
	// When true, all requests are treated as anonymous with full permissions.
	Disabled bool `yaml:"disabled"`

	// IdentityProviders is a list of identity providers (chain of responsibility).
	// First match wins.
	IdentityProviders []IdentityProviderConfig `yaml:"identity_providers"`

	// BasicAuth is the configuration for Basic Authentication.
	BasicAuth BasicAuthConfig `yaml:"basic_auth"`

	// PermissionsPath is the path to the permissions YAML file.
	PermissionsPath string `yaml:"permissions_path"`
}

// IdentityProviderConfig configures a single identity provider.
type IdentityProviderConfig struct {
	// Type is the provider type: "file", "pam" (future), etc.
	Type string `yaml:"type"`

	// Path is the file path (for file-based providers).
	Path string `yaml:"path"`
}

// BasicAuthConfig configures Basic Authentication.
type BasicAuthConfig struct {
	// CredentialsPath is the path to the basic_auth.yaml file.
	CredentialsPath string `yaml:"credentials_path"`
}

// DefaultConfig returns the default authentication configuration.
func DefaultConfig() Config {
	return Config{
		Disabled: false, // Security by default.
		IdentityProviders: []IdentityProviderConfig{
			{
				Type: "file",
				Path: "/etc/yanet/identities.yaml",
			},
		},
		BasicAuth: BasicAuthConfig{
			CredentialsPath: "/etc/yanet/basic_auth.yaml",
		},
		PermissionsPath: "/etc/yanet/permissions.yaml",
	}
}
