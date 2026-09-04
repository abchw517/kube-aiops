package sanitizer

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	credentialMarker    = "[REDACTED:credential]"
	privateKeyMarker    = "[REDACTED:private-key]"
	headerMarker        = "[REDACTED:header]"
	activeContentMarker = "[REDACTED:active-content]"
)

var (
	privateKeyPattern          = regexp.MustCompile(`(?is)-----BEGIN (?:RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----.*?-----END (?:RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----`)
	authorizationHeaderPattern = regexp.MustCompile(`(?i)(authorization\s*:\s*bearer\s+)[^\s,;]+`)
	cookieHeaderPattern        = regexp.MustCompile(`(?im)\b(set-cookie|cookie)\s*:\s*[^\r\n]*`)
	assignmentPattern          = regexp.MustCompile(`(?i)\b(password|passwd|secret|token|api[_-]?key|client[_-]?key(?:[_-]?data)?)\s*[:=]\s*(?:"[^"\r\n]*"|'[^'\r\n]*'|[A-Za-z0-9._~+/=-]{6,})`)
	jwtPattern                 = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{6,}\.eyJ[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{16,}\b`)
	bearerPattern              = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{12,}`)
	scriptBlockPattern         = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script\s*>`)
	scriptTagPattern           = regexp.MustCompile(`(?is)</?script\b[^>]*>`)
	eventHandlerPattern        = regexp.MustCompile(`(?i)\bon[a-z]{3,32}\s*=\s*(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s>]+)`)
	javascriptPattern          = regexp.MustCompile(`(?i)\bjavascript\s*:`)
)

func sanitizeDiagnosticText(value string, maxBytes int) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
	value = normalizeControls(value)
	value = privateKeyPattern.ReplaceAllString(value, privateKeyMarker)
	value = authorizationHeaderPattern.ReplaceAllString(value, "${1}"+credentialMarker)
	value = cookieHeaderPattern.ReplaceAllString(value, "${1}: "+headerMarker)
	value = assignmentPattern.ReplaceAllString(value, "${1}="+credentialMarker)
	value = jwtPattern.ReplaceAllString(value, credentialMarker)
	value = bearerPattern.ReplaceAllString(value, "Bearer "+credentialMarker)
	value = scriptBlockPattern.ReplaceAllString(value, activeContentMarker)
	value = scriptTagPattern.ReplaceAllString(value, activeContentMarker)
	value = eventHandlerPattern.ReplaceAllString(value, activeContentMarker)
	value = javascriptPattern.ReplaceAllString(value, activeContentMarker)
	return truncateUTF8(value, maxBytes)
}

func normalizeControls(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range value {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func truncateUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	cut := maxBytes - len(truncationMarker)
	if cut < 0 {
		cut = 0
	}
	for cut > 0 && !utf8.ValidString(value[:cut]) {
		cut--
	}
	return value[:cut] + truncationMarker
}
