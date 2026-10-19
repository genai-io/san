package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/joho/godotenv"
)

// A working-directory .env may come from a cloned repository, so it may supply
// credentials but never decide where they are sent; your shell still can.

// networkControlEnv holds the variables the Go runtime reads to pick a
// request's route and trusted certificates.
var networkControlEnv = map[string]bool{
	"HTTP_PROXY":    true,
	"HTTPS_PROXY":   true,
	"ALL_PROXY":     true,
	"NO_PROXY":      true,
	"SSL_CERT_FILE": true,
	"SSL_CERT_DIR":  true,
}

// decidesWhereCredentialsGo reports whether name would change the destination
// of an authenticated request. Endpoints match by shape, covering new providers.
func decidesWhereCredentialsGo(name string) bool {
	upper := strings.ToUpper(name)
	return networkControlEnv[upper] || strings.HasSuffix(upper, "_BASE_URL")
}

// loadWorkingDirEnv applies ./.env without overriding the environment, skipping
// and returning (sorted) the names that decide where credentials go.
func loadWorkingDirEnv() (refused []string) {
	vars, err := godotenv.Read()
	if err != nil {
		return nil // no .env, or unreadable — same as before
	}
	for name, value := range vars {
		if decidesWhereCredentialsGo(name) {
			refused = append(refused, name)
			continue
		}
		if _, ok := os.LookupEnv(name); ok {
			continue
		}
		_ = os.Setenv(name, value)
	}
	sort.Strings(refused)
	return refused
}

// warnRefusedEnv reports skipped .env entries on stderr, where a deliberate
// base URL's owner will see it.
func warnRefusedEnv(refused []string) {
	if len(refused) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr,
		"san: ignoring %s from ./.env — a working-directory file may supply credentials but not the endpoint they are sent to.\n"+
			"     Export the variable in your shell, or set the base URL from the provider menu, if this was your own file.\n",
		strings.Join(refused, ", "))
}
