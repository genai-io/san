package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/setting/trust"
)

// warnUntrustedWorkspace is the headless form: no prompt, so an untrusted
// folder's configuration stays off and stderr says what was skipped.
func warnUntrustedWorkspace(cwd string) {
	findings := trust.ReviewAndTrustIfClean(cwd)
	if len(findings) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "san: %s is not trusted, so its configuration was not applied:\n  - %s\n"+
		"Run san there once and trust it, or set SAN_TRUST_WORKSPACE=1 for this run.\n",
		setting.TrustKey(cwd), strings.Join(findings, "\n  - "))
}
