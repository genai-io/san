// Package llmerr classifies LLM provider/stream errors so the agent loop can
// decide whether a failure is worth retrying. It maps each provider SDK's error
// type onto a small, provider-agnostic taxonomy and exposes a single Wrap entry
// point that tags retryable errors with core.RetryableError.
package llmerr

import (
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go/v3"
	"google.golang.org/genai"

	"github.com/genai-io/san/internal/core"
)

// class is the provider-agnostic failure category.
type class int

const (
	fatal       class = iota // never retry: bad request, auth, not-found, content policy, context window
	retryable                // transient: 408/409, all 5xx (incl. 529), connection/network errors
	rateLimited              // 429 — retry, honoring Retry-After when present
)

// Wrap classifies err and tags it for the turn loop: retryable failures
// satisfy core.RetryableError (carrying any Retry-After hint), a prompt that
// overflowed the context window satisfies core.ContextExceededError. Anything
// else — and nil — is returned unchanged, so it satisfies neither and the turn
// loop surfaces it immediately. The original error always stays in the chain.
func Wrap(err error) error {
	if err == nil {
		return nil
	}
	// Checked before classify: an overflowed prompt arrives as a 400/422,
	// which classify buckets as fatal, and it needs its own tag so the loop
	// compacts instead of giving up.
	if isContextExceeded(err) {
		return contextErr{err: err}
	}
	switch c, after := classify(err); c {
	case retryable:
		return retryErr{err: err}
	case rateLimited:
		return retryErr{err: err, after: after}
	default:
		return err
	}
}

// contextExceededSignatures are the ways providers say "this prompt exceeds
// the context window". Matching is on the message text because no provider
// distinguishes it from other 400s with a machine-readable code.
//
// This is the whole safety net for a model whose window San could not size in
// advance: proactive compaction cannot fire without a known limit, so a
// phrasing missing here means the turn fails and keeps failing rather than
// compacting and retrying. Add a provider's wording when adding the provider.
var contextExceededSignatures = []string{
	"prompt is too long",                // Anthropic
	"prompt_too_long",                   // Anthropic (error type)
	"maximum context length",            // OpenAI and OpenAI-compatible
	"context_length_exceeded",           // OpenAI (error code)
	"reduce the length of the messages", // OpenAI (remediation text)
	"input token count",                 // Google Gemini
	"exceeds the maximum number of tokens",
	"context length exceeded",
	"too many tokens",
}

func isContextExceeded(err error) bool {
	msg := strings.ToLower(err.Error())
	for _, sig := range contextExceededSignatures {
		if strings.Contains(msg, sig) {
			return true
		}
	}
	return false
}

// contextErr satisfies core.ContextExceededError while preserving the original.
type contextErr struct{ err error }

func (e contextErr) Error() string    { return e.err.Error() }
func (e contextErr) Unwrap() error    { return e.err }
func (e contextErr) ContextExceeded() {}

var _ core.ContextExceededError = contextErr{}

// retryErr satisfies core.RetryableError while preserving the original error.
type retryErr struct {
	err   error
	after time.Duration
}

func (e retryErr) Error() string             { return e.err.Error() }
func (e retryErr) Unwrap() error             { return e.err }
func (e retryErr) RetryAfter() time.Duration { return e.after }

var _ core.RetryableError = retryErr{}

// classify maps err onto the taxonomy, returning a Retry-After hint when the
// provider supplied one (429 responses); 0 otherwise.
func classify(err error) (class, time.Duration) {
	// Provider SDK typed errors carry an HTTP status — the most reliable
	// signal. (openai.Error.Code is a string, so use .StatusCode.)
	var anthErr *anthropic.Error
	if errors.As(err, &anthErr) {
		return fromStatus(anthErr.StatusCode, anthErr.Response)
	}
	var oaiErr *openai.Error
	if errors.As(err, &oaiErr) {
		return fromStatus(oaiErr.StatusCode, oaiErr.Response)
	}
	var gErr genai.APIError
	if errors.As(err, &gErr) {
		// genai.APIError exposes no response headers, so there is no
		// Retry-After to honor — fall back to plain backoff.
		c, _ := fromStatus(gErr.Code, nil)
		return c, 0
	}

	// Transport-level failures with no HTTP status: connection dropped,
	// refused, reset, or a timeout. All worth a retry.
	if isNetworkError(err) {
		return retryable, 0
	}

	return fatal, 0
}

// fromStatus classifies an HTTP status code and extracts Retry-After for 429.
func fromStatus(code int, resp *http.Response) (class, time.Duration) {
	switch {
	case code == http.StatusTooManyRequests: // 429
		return rateLimited, retryAfter(resp)
	case code == http.StatusRequestTimeout, // 408
		code == http.StatusConflict, // 409
		code >= 500:                 // all 5xx, incl. Anthropic 529 overloaded
		return retryable, 0
	default:
		// 400/401/403/404/422, content policy, model-not-found, and
		// context-window-exceeded all land here: retrying cannot help.
		return fatal, 0
	}
}

// isNetworkError reports whether err is a transport failure worth retrying. A
// dropped/refused/reset connection surfaces as a net.Error (e.g. *net.OpError);
// a mid-stream cutoff surfaces as io.EOF / io.ErrUnexpectedEOF.
//
// net.Error intentionally also matches a per-request timeout
// (context.DeadlineExceeded satisfies net.Error): a request that timed out is
// worth retrying. A user interrupt (context.Canceled) is NOT a net.Error and
// stays fatal — and the turn loop checks the caller ctx before classifying, so
// a cancel never reaches here.
func isNetworkError(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

// retryAfter parses a Retry-After header (delta-seconds or HTTP-date). Returns
// 0 when absent or unparseable.
func retryAfter(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}
	v := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}
