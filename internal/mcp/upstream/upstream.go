// Package upstream describes the MCP servers a session gets: what the config
// layer gives the proxy, the credential store and the name function. It
// depends on no config format, so the format can change without them.
package upstream

// Server is one MCP server bound to a session.
type Server struct {
	// ID is the server's stable identity: the key of its credentials.
	ID string
	// Name is the prefix of its tools in sessions.
	Name string
	// URL is a remote server's address; "" for a local server.
	URL string
	// Command is a local server's program and arguments; nil for a remote
	// server.
	Command []string
	// Env are a local server's variables; EnvPass names variables whose
	// values come from the proxy's environment; SecretEnv names variables
	// whose values are in the credential store.
	Env       map[string]string
	EnvPass   []string
	SecretEnv []string
	// Headers are a remote server's HTTP headers; SecretHeaders names
	// headers whose values are in the credential store.
	Headers       map[string]string
	SecretHeaders []string
	// ClientID is a pre-registered OAuth client; Scopes are those to ask
	// for at sign-in.
	ClientID string
	Scopes   []string
	// Notes are the user's own words about the server, such as which account
	// to use. ctxtra doesn't interpret them.
	Notes string
}

// Local reports whether s is started as a command.
func (s Server) Local() bool { return len(s.Command) > 0 }
