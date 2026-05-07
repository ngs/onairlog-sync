package onairlogsync

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

var (
	parenRE = regexp.MustCompile(`[\(（\[［][^\)）\]］]*[\)）\]］]`)
	featRE  = regexp.MustCompile(`(?i)\b(?:featuring|feat|ft)(?:\.|\b)`)
	wsRE    = regexp.MustCompile(`\s+`)
)

// DisplayClean strips bracketed annotations like "(2009 Remaster)" or
// "(生演奏)" while preserving the original casing for display purposes.
func DisplayClean(s string) string {
	s = parenRE.ReplaceAllString(s, " ")
	s = wsRE.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// Normalize lowercases, drops bracketed annotations, unifies the
// feat./ft./featuring spelling, and collapses whitespace. Output is
// suitable for matching but not for display.
func Normalize(s string) string {
	s = parenRE.ReplaceAllString(s, " ")
	s = strings.ToLower(s)
	s = featRE.ReplaceAllString(s, "feat.")
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
