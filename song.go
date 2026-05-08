package onairlogsync

import (
	"fmt"
	"strings"
	"time"
)

// Song is a canonical airplay subject identified by the normalized
// (title, artist) pair. Multiple Plays reference the same Song.
type Song struct {
	Title         string     `firestore:"title" json:"title"`
	Artist        string     `firestore:"artist" json:"artist"`
	NormalizedKey string     `firestore:"normalizedKey" json:"-"`
	FirstAired    *time.Time `firestore:"firstAired" json:"firstAired"`
	LastAired     *time.Time `firestore:"lastAired" json:"lastAired"`
	PlayCount     int        `firestore:"playCount" json:"playCount"`

	// Enrichment fields, populated by the iTunes Search API + Gemini
	// verification pipeline. Optional.
	EnrichedAt      *time.Time             `firestore:"enrichedAt,omitempty" json:"enrichedAt,omitempty"`
	ITunesTrackID   int64                  `firestore:"itunesTrackId,omitempty" json:"itunesTrackId,omitempty"`
	CanonicalTitle  string                 `firestore:"canonicalTitle,omitempty" json:"canonicalTitle,omitempty"`
	CanonicalArtist string                 `firestore:"canonicalArtist,omitempty" json:"canonicalArtist,omitempty"`
	CanonicalKey    string                 `firestore:"canonicalKey,omitempty" json:"canonicalKey,omitempty"`
	// Pre-computed for downstream consumers (Notify, API) so they
	// don't have to dig through ITunesResponse.
	ArtworkURL  string                 `firestore:"artworkUrl,omitempty" json:"artworkUrl,omitempty"`
	ITunesURL   string                 `firestore:"itunesUrl,omitempty" json:"itunesUrl,omitempty"`
	// Raw archives — Firestore-only, intentionally excluded from JSON
	// to keep Pub/Sub payloads small.
	ITunesResponse map[string]interface{} `firestore:"itunesResponse,omitempty" json:"-"`
	LLMResponse    map[string]interface{} `firestore:"llmResponse,omitempty" json:"-"`
}

// Play is a single airplay event and references a Song by ID.
type Play struct {
	SongID    string     `firestore:"songId" json:"songId"`
	Time      *time.Time `firestore:"time" json:"time"`
	RawTitle  string     `firestore:"rawTitle" json:"rawTitle"`
	RawArtist string     `firestore:"rawArtist" json:"rawArtist"`
}

// DisplayTitle prefers the canonical (enriched) title, falling back to
// the raw display string captured at airplay time.
func (s *Song) DisplayTitle() string {
	if s == nil {
		return ""
	}
	if s.CanonicalTitle != "" {
		return s.CanonicalTitle
	}
	return s.Title
}

// DisplayArtist prefers the canonical (enriched) artist.
func (s *Song) DisplayArtist() string {
	if s == nil {
		return ""
	}
	if s.CanonicalArtist != "" {
		return s.CanonicalArtist
	}
	return s.Artist
}

// BuildITunesURL is the music.apple.com URL for a verified iTunes
// track id, or an empty string when there is no match.
func BuildITunesURL(trackID int64) string {
	if trackID == 0 {
		return ""
	}
	return fmt.Sprintf("https://music.apple.com/jp/song/%d", trackID)
}

// ExtractArtworkURL pulls the 600x600 cover art URL out of an iTunes
// search result map (the top hit). Returns "" when unavailable.
func ExtractArtworkURL(itunes map[string]interface{}) string {
	if itunes == nil {
		return ""
	}
	results, _ := itunes["results"].([]interface{})
	if len(results) == 0 {
		return ""
	}
	r0, _ := results[0].(map[string]interface{})
	u, _ := r0["artworkUrl100"].(string)
	if u == "" {
		return ""
	}
	return strings.ReplaceAll(u, "100x100bb", "600x600bb")
}
