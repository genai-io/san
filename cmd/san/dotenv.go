package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/joho/godotenv"
)

// A working-directory .env may come from a cloned repository, so it may supply
// credentials but nothing else: not where they are sent, not San's own
// switches (SAN_TRUST_WORKSPACE), and not variables that make a shell or git
// run code (BASH_ENV, GIT_CONFIG_*, PATH). Your shell still can set any of them.

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

// credentialSuffixes name the shapes of provider credentials and the cloud
// project settings that travel with them.
var credentialSuffixes = []string{
	"_API_KEY", "_KEY", "_TOKEN", "_SECRET", "_ACCESS_KEY_ID",
	"_PROJECT", "_PROJECT_ID", "_REGION", "_LOCATION",
}

// decidesWhereCredentialsGo reports whether name would change the destination
// of an authenticated request. Endpoints match by shape, covering new providers.
func decidesWhereCredentialsGo(name string) bool {
	upper := strings.ToUpper(name)
	return networkControlEnv[upper] || strings.HasSuffix(upper, "_BASE_URL")
}

// isCredentialName reports whether a working-directory .env may set name.
func isCredentialName(name string) bool {
	upper := strings.ToUpper(name)
	if strings.HasPrefix(upper, "SAN_") || strings.HasPrefix(upper, "CLAUDE_") {
		return false
	}
	for _, suffix := range credentialSuffixes {
		if strings.HasSuffix(upper, suffix) {
			return true
		}
	}
	return false
}

// loadWorkingDirEnv applies the credentials in ./.env without overriding the
// environment. It returns (sorted) the refused names that decide where
// credentials go; other non-credential names are skipped silently, since an
// application's .env routinely carries settings that are not San's.
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
		if !isCredentialName(name) {
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
