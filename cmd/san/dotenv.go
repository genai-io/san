package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/joho/godotenv"
)

// San reads a .env from whatever directory it is launched in. That file is a
// convenience for supplying your own credentials — but the working directory is
// not necessarily yours. Clone a repository and a .env committed inside it is
// read before you have typed anything.
//
// Supplying a credential that way is harmless enough: the worst case is that
// San talks to the model on someone else's account. Deciding the *endpoint* is
// not, because every provider resolves its API key from the user's own secret
// store and then sends it wherever the base URL points. One line in a
// repository's .env is otherwise enough to redirect the request and take the
// key with it.
//
// So: a working-directory .env may say what to send, never where to send it.
// Names carrying that decision are refused and reported. Exporting the same
// variable in your shell still works — that is your own environment, not the
// repository's — as does setting a base URL from the provider UI.

// networkControlEnv holds the variables the Go runtime itself reads to decide
// where a request goes and which certificates to trust. None of them is
// referenced by San's own code; the runtime picks them up regardless.
var networkControlEnv = map[string]bool{
	"HTTP_PROXY":    true,
	"HTTPS_PROXY":   true,
	"ALL_PROXY":     true,
	"NO_PROXY":      true,
	"SSL_CERT_FILE": true,
	"SSL_CERT_DIR":  true,
}

// decidesWhereCredentialsGo reports whether setting name would change the
// destination of an authenticated request. Provider endpoints are matched by
// shape rather than by a list, so a provider added later is covered without
// anyone remembering to come back here.
func decidesWhereCredentialsGo(name string) bool {
	upper := strings.ToUpper(name)
	return networkControlEnv[upper] || strings.HasSuffix(upper, "_BASE_URL")
}

// loadWorkingDirEnv applies ./.env to the process environment, skipping the
// names that decide where credentials go and returning those names sorted. A
// missing .env is not an error. Like godotenv.Load, a variable already present
// in the environment is left alone — your shell outranks the file.
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

// warnRefusedEnv tells the user which .env entries were skipped and why. It
// goes to stderr rather than the log: someone who put a base URL in .env
// deliberately needs to see that it did not take effect.
func warnRefusedEnv(refused []string) {
	if len(refused) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr,
		"san: ignoring %s from ./.env — a working-directory file may supply credentials but not the endpoint they are sent to.\n"+
			"     Export the variable in your shell, or set the base URL from the provider menu, if this was your own file.\n",
		strings.Join(refused, ", "))
}
