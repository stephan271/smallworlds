package consoleworkflow

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Redaction patterns scrub secrets by type before any detail is stored in a
// record or shipped to Loki. Redacting at creation time — rather than filtering
// logs after the fact — keeps credential values out of durable state entirely.
var (
	bearerPattern   = regexp.MustCompile(`(?i)\b(bearer|token)\s+[A-Za-z0-9._~+/=-]{8,}`)
	keyValuePattern = regexp.MustCompile(`(?i)\b(password|passwd|secret|token|apikey|api[_-]?key|client[_-]?secret|authorization)\b\s*[:=]\s*\S+`)
	pemPattern      = regexp.MustCompile(`(?s)-----BEGIN [^-]+-----.*?-----END [^-]+-----`)
)

// RedactDetail removes recognized secrets from a detail string, collapses it to
// a single compact line, and truncates it to max characters. It is applied to
// evidence summaries and to any event text before it reaches Loki.
func RedactDetail(detail string, max int) string {
	redacted := pemPattern.ReplaceAllString(detail, "[redacted-key]")
	redacted = bearerPattern.ReplaceAllString(redacted, "$1 [redacted]")
	redacted = keyValuePattern.ReplaceAllString(redacted, "$1=[redacted]")
	redacted = strings.Join(strings.Fields(redacted), " ")
	const ellipsis = "…"
	if max > 0 && len(redacted) > max {
		limit := max - len(ellipsis)
		if limit < 0 {
			limit = 0
		}
		for limit > 0 && !utf8.RuneStart(redacted[limit]) {
			limit-- // do not split a multi-byte rune
		}
		redacted = strings.TrimRight(redacted[:limit], " ") + ellipsis
	}
	return redacted
}
