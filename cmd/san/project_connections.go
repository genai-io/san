package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Both the directory-to-name selection and the binding belong to the user,
// not the checkout. Credential references resolve before reading project .env.
type projectConnections struct {
	Projects    map[string]string `json:"projects"`
	Connections map[string]struct {
		EndpointEnv string            `json:"endpointEnv"`
		URL         string            `json:"url"`
		Credentials map[string]string `json:"credentials"`
	} `json:"connections"`
}

var connectionEnvName = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

func applyProjectConnection(home, cwd string) error {
	data, err := os.ReadFile(filepath.Join(home, ".san", "connections.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var cfg projectConnections
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("invalid connections.json: %w", err)
	}
	dir, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return err
	}
	name, ok := cfg.Projects[dir]
	if !ok {
		return nil
	}
	c, ok := cfg.Connections[name]
	if !ok {
		return fmt.Errorf("unknown project connection %q", name)
	}
	u, err := url.Parse(c.URL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("connection %q requires an HTTPS endpoint without credentials, query or fragment", name)
	}
	if !connectionEnvName.MatchString(c.EndpointEnv) || !strings.HasSuffix(c.EndpointEnv, "_BASE_URL") || len(c.Credentials) == 0 {
		return fmt.Errorf("connection %q requires an endpointEnv ending in _BASE_URL and credential references", name)
	}
	values := map[string]string{c.EndpointEnv: c.URL}
	for dest, src := range c.Credentials {
		if !connectionEnvName.MatchString(dest) || !connectionEnvName.MatchString(src) || decidesWhereCredentialsGo(dest) {
			return fmt.Errorf("invalid credential binding in connection %q", name)
		}
		value := os.Getenv(src)
		if value == "" {
			return fmt.Errorf("connection %q requires %s in the shell environment", name, src)
		}
		values[dest] = value
	}
	for k, v := range values {
		if err := os.Setenv(k, v); err != nil {
			return err
		}
	}
	return nil
}
