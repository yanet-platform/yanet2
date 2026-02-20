package basic

// Config configures Basic Authentication.
type Config struct {
	// CredentialsPath is the path to the basic_auth.yaml file.
	CredentialsPath string `yaml:"credentials_path"`
}
