package onairlogsync

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"
)

var (
	parenRE = regexp.MustCompile(`[\(（\[［][^\)）\]］]*[\)）\]］]`)
	featRE  = regexp.MustCompile(`(?i)\b(?:featuring|feat|ft)(?:\.|\b)`)
	sepRE   = regexp.MustCompile(`(?i)[&+,/]|\band\b`)
	punctRE = regexp.MustCompile(`[^\p{L}\p{N}\s]`)
	wsRE    = regexp.MustCompile(`\s+`)
	// Soft hyphen, zero-width chars, BOM, and other format/control
	// characters that survive whitespace trimming and are never
	// legitimate display content.
	invisibleRE = regexp.MustCompile(`[\x00-\x08\x0B\x0C\x0E-\x1F\x7F\x{00AD}\x{200B}-\x{200F}\x{202A}-\x{202E}\x{2060}-\x{206F}\x{FEFF}]`)
)

// DisplayClean strips bracketed annotations like "(2009 Remaster)" or
// "(生演奏)" and unwraps the entire string when it is enclosed in matching
// quotes (e.g. `"HEROES"` -> `HEROES`). Casing is preserved. Zero-width
// characters, BOM, soft hyphens, and other invisible format characters
// are removed so they cannot create silent duplicates.
func DisplayClean(s string) string {
	s = invisibleRE.ReplaceAllString(s, "")
	s = parenRE.ReplaceAllString(s, " ")
	s = wsRE.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	s = stripOuterQuotes(s)
	return strings.TrimSpace(s)
}

var outerQuotes = [][2]string{
	{`"`, `"`},
	{`'`, `'`},
	{"“", "”"}, // “ ”
	{"‘", "’"}, // ‘ ’
}

func stripOuterQuotes(s string) string {
	for _, p := range outerQuotes {
		if len(s) >= len(p[0])+len(p[1]) &&
			strings.HasPrefix(s, p[0]) &&
			strings.HasSuffix(s, p[1]) {
			return s[len(p[0]) : len(s)-len(p[1])]
		}
	}
	return s
}

// Normalize collapses surface-form variation that should not produce
// separate Songs. Steps:
//   1. NFKC: halfwidth katakana → fullwidth, fullwidth ASCII → halfwidth.
//   2. Drop bracketed annotations like "(Live)" / "(2009 Remaster)".
//   3. Lowercase (Latin only; no-op for CJK).
//   4. Drop "feat." / "ft." / "featuring" tokens.
//   5. Treat &, +, /, ",", and the word "and" as artist separators.
//   6. Strip remaining punctuation (quotes, hyphens, "#", "!", ".", ...).
//   7. Collapse whitespace.
func Normalize(s string) string {
	s = invisibleRE.ReplaceAllString(s, "")
	s = norm.NFKC.String(s)
	s = parenRE.ReplaceAllString(s, " ")
	s = strings.ToLower(s)
	s = featRE.ReplaceAllString(s, " ")
	s = sepRE.ReplaceAllString(s, " ")
	s = punctRE.ReplaceAllString(s, " ")
	s = wsRE.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// SongID is the deterministic Firestore document ID for a (title, artist)
// pair, derived from their normalized form so different transcriptions of
// the same song collapse to one canonical document.
func SongID(title, artist string) string {
	return hashKey(Normalize(title) + "\x00" + Normalize(artist))
}

// NormalizedKey returns the matching key stored on the Song document for
// debugging and possible future re-normalization.
func NormalizedKey(title, artist string) string {
	return Normalize(title) + "|" + Normalize(artist)
}

func hashKey(s string) string {
	h := sha1.New()
	fmt.Fprint(h, s)
	return hex.EncodeToString(h.Sum(nil))
}
