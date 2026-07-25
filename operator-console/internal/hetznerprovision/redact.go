package hetznerprovision

import (
	"regexp"
	"strconv"
	"strings"
)

// Placeholder is what a redacted value is replaced with. It is deliberately
// uniform: a redaction that revealed the length or shape of what it hid would
// still be a disclosure.
const Placeholder = "[redacted]"

// Sensitive patterns that must never survive into a checkpoint, an activity
// event, or an error message shown to the browser. OpenTofu marks *declared*
// sensitive values itself, but it still echoes a token supplied in the
// environment when the provider rejects it, and a state or plan file quoted
// into a diagnostic can carry key material verbatim.
var sensitivePatterns = []*regexp.Regexp{
	// Hetzner Cloud API tokens: 64 characters of base62, bounded so an ordinary
	// long identifier is not mangled.
	regexp.MustCompile(`\b[A-Za-z0-9]{64}\b`),
	// PEM blocks of any kind — private keys, certificates with embedded keys.
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
	// Authorization headers and token environment variables OpenTofu prints when
	// it traces a request. The credential follows an optional scheme word, so the
	// scheme alone is never mistaken for the value being hidden.
	regexp.MustCompile(`(?i)\b(authorization|hcloud_token|tf_var_hcloud_token)\s*[:=]\s*(bearer\s+|token\s+)?\S+`),
	regexp.MustCompile(`(?i)\bbearer\s+\S+`),
	// Kubeconfig and k3s node tokens carried in cloud-init diagnostics.
	regexp.MustCompile(`(?i)\bK10[A-Za-z0-9:]{40,}`),
}

// Redact removes credential material from OpenTofu and bootstrap output before
// it is stored or shown.
//
// Known secret *values* are removed first and exactly — that is the reliable
// half, since the launcher holds the values it supplied. The pattern sweep is
// the second half: it catches material the launcher never held, such as a node
// token the cluster generated, at the cost of occasionally redacting an
// innocuous 64-character identifier. That trade is deliberate — an
// over-redacted diagnostic is inconvenient, a leaked project token is not.
func Redact(output string, secrets ...string) string {
	redacted := output
	for _, secret := range secrets {
		if len(strings.TrimSpace(secret)) < 8 {
			// Refusing to substitute a short value keeps a stray empty or
			// one-character secret from replacing every character in the output.
			continue
		}
		redacted = strings.ReplaceAll(redacted, secret, Placeholder)
	}
	for _, pattern := range sensitivePatterns {
		redacted = pattern.ReplaceAllString(redacted, Placeholder)
	}
	return redacted
}

// RedactedError applies Redact to an error's message, so an error surfaced from
// a provider call can be reported without laundering a token into a log through
// the error path.
func RedactedError(err error, secrets ...string) string {
	if err == nil {
		return ""
	}
	return Redact(err.Error(), secrets...)
}

// TailLines returns at most the last count lines of output, so a long apply log
// can be checkpointed without storing megabytes. It redacts nothing itself —
// callers pass the result of Redact — but keeps the truncation marker explicit
// so a reader knows the record is partial.
func TailLines(output string, count int) string {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if count <= 0 || len(lines) <= count {
		return strings.Join(lines, "\n")
	}
	return "… (" + strconv.Itoa(len(lines)-count) + " earlier lines omitted)\n" + strings.Join(lines[len(lines)-count:], "\n")
}
