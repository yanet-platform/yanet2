package auth

import (
	"gopkg.in/yaml.v3"
)

// Config is the configuration for authentication and authorization.
type Config struct {
	// Disabled indicates if authentication is disabled.
	//
	// When true, all requests are treated as anonymous with full permissions.
	Disabled bool `yaml:"disabled"`
	// IdentityProviders is a list of identity providers (chain of responsibility).
	// First match wins.
	IdentityProviders []IdentityProviderConfig `yaml:"identity_providers"`
	// Authenticators is a list of authenticator configurations.
	//
	// Each entry specifies a type and its type-specific config.
	Authenticators []AuthenticatorConfig `yaml:"authenticators"`
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

// AuthenticatorConfig is a generic authenticator configuration entry.
//
// The Type field selects the authenticator factory, and Config holds the
// type-specific YAML configuration decoded by the factory.
type AuthenticatorConfig struct {
	// Type is the authenticator type name.
	Type string `yaml:"type"`
	// Config is the raw YAML node for type-specific configuration.
	Config yaml.Node `yaml:"config"`
}

// DefaultConfig returns the default authentication configuration.
func DefaultConfig() Config {
	return Config{
		Disabled: true, // TODO: Security by default. For now transition phase.
	}
}
