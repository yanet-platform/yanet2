package debugserver

// Config configures the debug server that serves runtime profiles.
//
// The server has no authentication, and its profiles expose the program's
// internals and can load the process, so a TCP endpoint should stay on
// loopback and a unix socket in a directory only root can reach.
type Config struct {
	// Endpoint is "host:port" for TCP or an absolute path for a unix
	// socket.
	//
	// Empty disables the server. The socket's directory must exist; a stale
	// socket left at the path is replaced.
	Endpoint string `yaml:"endpoint"`
}
