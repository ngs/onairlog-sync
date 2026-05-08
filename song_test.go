package onairlogsync

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSongJSONIncludesEnrichmentURLs(t *testing.T) {
	// Regression: Slack notifications were missing artwork because the
	// pre-computed URLs were not flowing through the Pub/Sub payload.
	// They must round-trip via JSON so Notify can read them.
	now := time.Now().UTC()
	in := Song{
		Title:           "YOU MIGHT THINK",
		Artist:          "CARS",
		PlayCount:       5,
		FirstAired:      &now,
		LastAired:       &now,
		EnrichedAt:      &now,
		ITunesTrackID:   1088528668,
		CanonicalTitle:  "You Might Think",
		CanonicalArtist: "The Cars",
		CanonicalKey:    "itunes:1088528668",
		ArtworkURL:      "https://example.com/artwork.jpg",
		ITunesURL:       "https://music.apple.com/jp/song/1088528668",
		ITunesResponse: map[string]interface{}{
			"resultCount": 1,
			"results":     []interface{}{},
		},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(b)

	want := []string{`"artworkUrl":"https://example.com/artwork.jpg"`,
		`"itunesUrl":"https://music.apple.com/jp/song/1088528668"`,
		`"canonicalTitle":"You Might Think"`,
		`"canonicalArtist":"The Cars"`,
		`"itunesTrackId":1088528668`}
	for _, w := range want {
		if !strings.Contains(js, w) {
			t.Errorf("expected JSON to contain %s, got: %s", w, js)
		}
	}

	// ITunesResponse / LLMResponse are intentionally excluded from JSON.
	for _, w := range []string{`itunesResponse`, `llmResponse`} {
		if strings.Contains(js, w) {
			t.Errorf("expected JSON to omit %s, got: %s", w, js)
		}
	}

	var out Song
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ArtworkURL != in.ArtworkURL {
		t.Errorf("ArtworkURL not preserved: %q vs %q", out.ArtworkURL, in.ArtworkURL)
	}
	if out.ITunesURL != in.ITunesURL {
		t.Errorf("ITunesURL not preserved: %q vs %q", out.ITunesURL, in.ITunesURL)
	}
	if out.CanonicalTitle != in.CanonicalTitle || out.CanonicalArtist != in.CanonicalArtist {
		t.Errorf("canonical not preserved: %+v", out)
	}
	if out.ITunesTrackID != in.ITunesTrackID {
		t.Errorf("trackId not preserved: %d vs %d", out.ITunesTrackID, in.ITunesTrackID)
	}
}

func TestPublishedPlayRoundTrip(t *testing.T) {
	// The full Pub/Sub payload Sync emits and Notify consumes.
	now := time.Now().UTC()
	in := []PublishedPlay{{
		Play: Play{
			SongID:    "abc",
			Time:      &now,
			RawTitle:  "YOU MIGHT THINK",
			RawArtist: "CARS",
		},
		Song: Song{
			Title:           "YOU MIGHT THINK",
			Artist:          "CARS",
			CanonicalTitle:  "You Might Think",
			CanonicalArtist: "The Cars",
			ArtworkURL:      "https://example.com/art.jpg",
			ITunesURL:       "https://music.apple.com/jp/song/1",
			ITunesTrackID:   1,
		},
	}}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out []PublishedPlay
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len = %d", len(out))
	}
	got := out[0]
	if got.Song.ArtworkURL != "https://example.com/art.jpg" {
		t.Errorf("ArtworkURL lost in round-trip: %q", got.Song.ArtworkURL)
	}
	if got.Song.ITunesURL != "https://music.apple.com/jp/song/1" {
		t.Errorf("ITunesURL lost in round-trip: %q", got.Song.ITunesURL)
	}
	if got.Play.RawTitle != "YOU MIGHT THINK" {
		t.Errorf("RawTitle lost: %q", got.Play.RawTitle)
	}
}

func TestDisplayTitleArtist(t *testing.T) {
	cases := []struct {
		s              Song
		wantTitle      string
		wantArtist     string
		descr          string
	}{
		{Song{Title: "RAW TITLE", Artist: "RAW ARTIST"}, "RAW TITLE", "RAW ARTIST", "no canonical"},
		{Song{Title: "RAW", Artist: "RAW", CanonicalTitle: "Canonical T", CanonicalArtist: "Canonical A"}, "Canonical T", "Canonical A", "both canonical"},
		{Song{Title: "RAW", Artist: "RAW", CanonicalTitle: "Canonical T"}, "Canonical T", "RAW", "title canonical only"},
		{Song{Title: "RAW", Artist: "RAW", CanonicalArtist: "Canonical A"}, "RAW", "Canonical A", "artist canonical only"},
	}
	for _, c := range cases {
		if got := c.s.DisplayTitle(); got != c.wantTitle {
			t.Errorf("[%s] DisplayTitle = %q, want %q", c.descr, got, c.wantTitle)
		}
		if got := c.s.DisplayArtist(); got != c.wantArtist {
			t.Errorf("[%s] DisplayArtist = %q, want %q", c.descr, got, c.wantArtist)
		}
	}
}

func TestBuildITunesURL(t *testing.T) {
	if got := BuildITunesURL(0); got != "" {
		t.Errorf("expected empty for trackID=0, got %q", got)
	}
	if got := BuildITunesURL(12345); got != "https://music.apple.com/jp/song/12345" {
		t.Errorf("unexpected: %q", got)
	}
}

func TestExtractArtworkURL(t *testing.T) {
	cases := []struct {
		in   map[string]interface{}
		want string
	}{
		{nil, ""},
		{map[string]interface{}{}, ""},
		{map[string]interface{}{"results": []interface{}{}}, ""},
		{
			map[string]interface{}{"results": []interface{}{
				map[string]interface{}{"artworkUrl100": "https://x/100x100bb.jpg"},
			}},
			"https://x/600x600bb.jpg",
		},
		{
			map[string]interface{}{"results": []interface{}{
				map[string]interface{}{}, // no artworkUrl100
			}},
			"",
		},
	}
	for _, c := range cases {
		if got := ExtractArtworkURL(c.in); got != c.want {
			t.Errorf("ExtractArtworkURL(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
