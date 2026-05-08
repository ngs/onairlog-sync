package onairlogsync

import (
	"testing"
)

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefInt(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func TestBuildSlackAttachment_nilTime(t *testing.T) {
	pp := PublishedPlay{
		Play: Play{Time: nil, RawTitle: "x", RawArtist: "y"},
		Song: Song{},
	}
	_, ok := BuildSlackAttachment(pp, jstLocation())
	if ok {
		t.Errorf("expected ok=false for nil Play.Time")
	}
}

func TestBuildSlackAttachment_enrichedSong(t *testing.T) {
	at := ts("2026-05-08T01:45:56Z") // = JST 10:45:56
	pp := PublishedPlay{
		Play: Play{
			Time:      at,
			SongID:    "abc",
			RawTitle:  "YOU MIGHT THINK",
			RawArtist: "CARS",
		},
		Song: Song{
			Title:           "YOU MIGHT THINK",
			Artist:          "CARS",
			CanonicalTitle:  "You Might Think",
			CanonicalArtist: "The Cars",
			ITunesTrackID:   1088528668,
			ITunesURL:       "https://music.apple.com/jp/song/1088528668",
			ArtworkURL:      "https://example.com/art.jpg",
		},
	}
	a, ok := BuildSlackAttachment(pp, jstLocation())
	if !ok {
		t.Fatal("ok=false unexpected")
	}
	if got := deref(a.Title); got != "You Might Think" {
		t.Errorf("Title = %q, want canonical 'You Might Think'", got)
	}
	if got := deref(a.AuthorName); got != "The Cars" {
		t.Errorf("AuthorName = %q, want canonical 'The Cars'", got)
	}
	if got := deref(a.TitleLink); got != "https://music.apple.com/jp/song/1088528668" {
		t.Errorf("TitleLink = %q", got)
	}
	if got := deref(a.ImageUrl); got != "https://example.com/art.jpg" {
		t.Errorf("ImageUrl = %q", got)
	}
	if got := deref(a.Footer); got != "2026/05/08 10:45" {
		t.Errorf("Footer = %q, want JST-formatted '2026/05/08 10:45'", got)
	}
	if got := derefInt(a.Timestamp); got != at.Unix() {
		t.Errorf("Timestamp = %d, want %d", got, at.Unix())
	}
	if got := deref(a.Fallback); got != "2026/05/08 10:45 You Might Think / The Cars" {
		t.Errorf("Fallback = %q", got)
	}
}

func TestBuildSlackAttachment_unenrichedSong(t *testing.T) {
	// No canonical fields, no iTunes data. Should fall back to raw
	// display values from the Song struct, no link, no artwork.
	at := ts("2026-05-08T01:45:56Z")
	pp := PublishedPlay{
		Play: Play{
			Time:      at,
			RawTitle:  "RAW TITLE",
			RawArtist: "RAW ARTIST",
		},
		Song: Song{
			Title:  "RAW TITLE",
			Artist: "RAW ARTIST",
		},
	}
	a, ok := BuildSlackAttachment(pp, jstLocation())
	if !ok {
		t.Fatal("ok=false unexpected")
	}
	if got := deref(a.Title); got != "RAW TITLE" {
		t.Errorf("Title = %q, want raw 'RAW TITLE'", got)
	}
	if got := deref(a.AuthorName); got != "RAW ARTIST" {
		t.Errorf("AuthorName = %q", got)
	}
	if a.TitleLink != nil {
		t.Errorf("TitleLink = %q, want nil", deref(a.TitleLink))
	}
	if a.ImageUrl != nil {
		t.Errorf("ImageUrl = %q, want nil", deref(a.ImageUrl))
	}
}

func TestBuildSlackAttachment_emptySongFallsBackToRawPlay(t *testing.T) {
	// Song has no fields populated (e.g. lookup failure). Slack
	// attachment should still render using the Play's raw fields.
	at := ts("2026-05-08T01:45:56Z")
	pp := PublishedPlay{
		Play: Play{
			Time:      at,
			RawTitle:  "FALLBACK TITLE",
			RawArtist: "FALLBACK ARTIST",
		},
		Song: Song{}, // empty
	}
	a, ok := BuildSlackAttachment(pp, jstLocation())
	if !ok {
		t.Fatal("ok=false unexpected")
	}
	if got := deref(a.Title); got != "FALLBACK TITLE" {
		t.Errorf("Title = %q", got)
	}
	if got := deref(a.AuthorName); got != "FALLBACK ARTIST" {
		t.Errorf("AuthorName = %q", got)
	}
}

// TestBuildSlackAttachment_footerUsesPlayTime ensures the rendered
// timestamp comes from Play.Time, never from Song.LastAired (the
// latter can be older than the airplay during enrichment edge cases).
func TestBuildSlackAttachment_footerUsesPlayTime(t *testing.T) {
	playAt := ts("2026-05-08T02:11:54Z")     // today
	songLast := ts("2026-05-03T05:28:17Z")   // older
	pp := PublishedPlay{
		Play: Play{Time: playAt, RawTitle: "T", RawArtist: "A"},
		Song: Song{Title: "T", Artist: "A", LastAired: songLast},
	}
	a, ok := BuildSlackAttachment(pp, jstLocation())
	if !ok {
		t.Fatal("ok=false")
	}
	wantFooter := "2026/05/08 11:11"
	if got := deref(a.Footer); got != wantFooter {
		t.Errorf("footer = %q, want %q (must reflect Play.Time, not Song.LastAired)", got, wantFooter)
	}
	if got := derefInt(a.Timestamp); got != playAt.Unix() {
		t.Errorf("ts = %d, want %d (Play.Time)", got, playAt.Unix())
	}
}

// TestBuildSlackAttachment_artworkButNoLink mirrors the production
// case where iTunes Search returned a result (so artwork was
// extracted) but Gemini said no match (so no trackId / URL). The
// attachment should reflect that asymmetry — it is still rendered,
// just without a clickable title.
func TestBuildSlackAttachment_artworkButNoLink(t *testing.T) {
	at := ts("2026-05-08T02:52:00Z")
	pp := PublishedPlay{
		Play: Play{Time: at, RawTitle: "HEROIN", RawArtist: "MAROON 5"},
		Song: Song{
			Title:           "HEROIN",
			Artist:          "MAROON 5",
			CanonicalTitle:  "Heroine",
			CanonicalArtist: "Maroon 5",
			ArtworkURL:      "https://example.com/heroine.jpg",
			ITunesURL:       "", // no link
		},
	}
	a, ok := BuildSlackAttachment(pp, jstLocation())
	if !ok {
		t.Fatal("ok=false")
	}
	if a.ImageUrl == nil {
		t.Errorf("expected ImageUrl set when ArtworkURL is non-empty")
	}
	if a.TitleLink != nil {
		t.Errorf("TitleLink = %q, expected nil when ITunesURL is empty", deref(a.TitleLink))
	}
}

func TestBuildSlackAttachment_jstFormatting(t *testing.T) {
	// JST midnight from a UTC of 15:00 the previous day.
	at := ts("2026-05-07T15:00:00Z") // = JST 2026-05-08 00:00
	pp := PublishedPlay{
		Play: Play{Time: at, RawTitle: "x", RawArtist: "y"},
		Song: Song{Title: "x", Artist: "y"},
	}
	a, ok := BuildSlackAttachment(pp, jstLocation())
	if !ok {
		t.Fatal("ok=false")
	}
	if got := deref(a.Footer); got != "2026/05/08 00:00" {
		t.Errorf("footer = %q, want '2026/05/08 00:00'", got)
	}
}
