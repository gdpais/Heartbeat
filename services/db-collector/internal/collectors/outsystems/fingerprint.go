package outsystems

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// fingerprintStackLines is how many leading non-empty lines of Stack a
// fingerprint covers: enough to tell errors with the same message apart by
// where they were raised, few enough that deeper frames (callers, retries)
// do not split one error into many.
const fingerprintStackLines = 3

// fingerprintLength is the number of hex digits kept: 64 bits, so distinct
// errors practically never collide.
const fingerprintLength = 16

var (
	// guidPattern matches GUIDs, such as request keys and session IDs.
	guidPattern = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	// quotedPattern matches quoted values: names, user input, SQL literals.
	quotedPattern = regexp.MustCompile(`'[^']*'|"[^"]*"`)
	// hexPattern matches 0x-prefixed values and runs of 8 or more hex digits
	// (hashes, object IDs); hexID keeps the runs without a digit, which are
	// words such as "deadbeef" or "accepted".
	hexPattern = regexp.MustCompile(`(?i)\b0x[0-9a-f]+\b|\b[0-9a-f]{8,}\b`)
	// numberPattern matches numbers: IDs, counts, durations, line numbers.
	numberPattern = regexp.MustCompile(`\d+`)
	spacePattern  = regexp.MustCompile(`\s+`)
)

// Fingerprint returns a short, stable identifier of an error: the first 16
// hex digits of the SHA-256 of its normalized message and the normalized
// first lines of its stack.  Errors that differ only in IDs, numbers, GUIDs
// or quoted values share a fingerprint.
func Fingerprint(message, stack string) string {
	var b strings.Builder
	b.WriteString(normalize(message))
	lines := 0
	for line := range strings.Lines(stack) {
		if lines == fingerprintStackLines {
			break
		}
		if line = normalize(line); line != "" {
			b.WriteByte('\n')
			b.WriteString(line)
			lines++
		}
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])[:fingerprintLength]
}

// normalize replaces the variable parts of s with placeholders, collapses
// whitespace and trims it.  The order matters: a GUID or quoted value is
// replaced whole before its digits could be.
func normalize(s string) string {
	s = guidPattern.ReplaceAllString(s, "<guid>")
	s = quotedPattern.ReplaceAllString(s, "<str>")
	s = hexPattern.ReplaceAllStringFunc(s, hexID)
	s = numberPattern.ReplaceAllString(s, "<num>")
	s = spacePattern.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// hexID replaces a hexPattern match that contains a digit.
func hexID(match string) string {
	if strings.ContainsAny(match, "0123456789") {
		return "<hex>"
	}
	return match
}
