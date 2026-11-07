package main

import "net/url"

// matrixServerDomain returns the Matrix server name used for user IDs in this
// bridge: the explicitly configured server domain when set, otherwise the host
// of the Matrix server URL. The two differ whenever the homeserver is reached
// through a different hostname than its server_name (for example
// https://matrix.example.com serving the example.com server name).
func (p *Plugin) matrixServerDomain() string {
	config := p.getConfiguration()
	if config == nil {
		return ""
	}
	if config.MatrixServerDomain != "" {
		return config.MatrixServerDomain
	}
	if config.MatrixServerURL == "" {
		return ""
	}
	parsed, err := url.Parse(config.MatrixServerURL)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}
