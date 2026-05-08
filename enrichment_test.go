package onairlogsync

import "testing"

// itunesResultsFixture is the structural shape of `iTunesResponse`
// stored on the Song doc — `results` is a slice of opaque maps.
func itunesResultsFixture(rs ...map[string]interface{}) map[string]interface{} {
	out := []interface{}{}
	for _, r := range rs {
		out = append(out, r)
	}
	return map[string]interface{}{"results": out}
}

func TestTopITunesHit(t *testing.T) {
	itunes := itunesResultsFixture(
		map[string]interface{}{
			"trackId":       float64(6763663512),
			"artistName":    "マルーン5",
			"artworkUrl100": "https://example.com/100x100bb.jpg",
		},
		map[string]interface{}{
			"trackId":    float64(2),
			"artistName": "Other",
		},
	)
	id, artist, artwork := topITunesHit(itunes)
	if id != 6763663512 {
		t.Errorf("id = %d, want 6763663512", id)
	}
	if artist != "マルーン5" {
		t.Errorf("artist = %q", artist)
	}
	if artwork != "https://example.com/600x600bb.jpg" {
		t.Errorf("artwork = %q", artwork)
	}
}

func TestTopITunesHit_emptyResults(t *testing.T) {
	id, artist, artwork := topITunesHit(map[string]interface{}{"results": []interface{}{}})
	if id != 0 || artist != "" || artwork != "" {
		t.Errorf("expected zero values, got %d %q %q", id, artist, artwork)
	}
}

func TestArtworkForTrack_matchByID(t *testing.T) {
	itunes := itunesResultsFixture(
		map[string]interface{}{
			"trackId":       float64(1),
			"artworkUrl100": "https://wrong/100x100bb.jpg",
		},
		map[string]interface{}{
			"trackId":       float64(42),
			"artworkUrl100": "https://right/100x100bb.jpg",
		},
	)
	got := artworkForTrack(itunes, 42)
	if got != "https://right/600x600bb.jpg" {
		t.Errorf("got %q, want match for trackId=42", got)
	}
}

func TestArtworkForTrack_unknownIDFallsBackToTop(t *testing.T) {
	itunes := itunesResultsFixture(
		map[string]interface{}{
			"trackId":       float64(1),
			"artworkUrl100": "https://top/100x100bb.jpg",
		},
	)
	got := artworkForTrack(itunes, 999)
	if got != "https://top/600x600bb.jpg" {
		t.Errorf("got %q, want top-result fallback", got)
	}
}

// applyVerdictForTest re-implements the post-LLM logic of Enrich
// (without making network calls) so we can verify the hybrid
// fallback behavior against synthetic iTunes + verdict data.
func applyVerdictForTest(itunesRaw map[string]interface{}, verdict map[string]interface{}) *EnrichmentResult {
	res := &EnrichmentResult{
		ITunesResponse: itunesRaw,
	}
	if v, ok := verdict["trackId"].(float64); ok {
		res.ITunesTrackID = int64(v)
	}
	if s, _ := verdict["canonicalTitle"].(string); s != "" {
		res.CanonicalTitle = s
	}
	if s, _ := verdict["canonicalArtist"].(string); s != "" {
		res.CanonicalArtist = s
	}
	// Hybrid fallback (mirrors Enrich)
	if res.ITunesTrackID == 0 {
		if topID, topArtist, topArtwork := topITunesHit(itunesRaw); topID > 0 &&
			res.CanonicalArtist != "" && Normalize(topArtist) == Normalize(res.CanonicalArtist) {
			res.ITunesTrackID = topID
			res.ArtworkURL = topArtwork
		}
	}
	if res.ArtworkURL == "" && res.ITunesTrackID > 0 {
		res.ArtworkURL = artworkForTrack(itunesRaw, res.ITunesTrackID)
	}
	res.ITunesURL = BuildITunesURL(res.ITunesTrackID)
	return res
}

// TestEnrichHybrid_LLMNullButArtistMatchesTop is the production
// "HEROIN / MAROON 5" scenario: iTunes Search returned the correct
// track as its top hit, but Gemini was uncertain (single-letter
// difference between "HEROIN" and "Heroine") and returned trackId=null.
//
// The hybrid fallback should adopt the top hit because its artist —
// after normalization — matches the LLM-derived canonicalArtist.
func TestEnrichHybrid_LLMNullButArtistMatchesTop(t *testing.T) {
	itunesRaw := itunesResultsFixture(
		map[string]interface{}{
			"trackId":       float64(6763663512),
			"trackName":     "Heroine",
			"artistName":    "Maroon 5",
			"artworkUrl100": "https://example.com/v/100x100bb.jpg",
		},
	)
	verdict := map[string]interface{}{
		"trackId":         nil,
		"canonicalTitle":  "Heroine",
		"canonicalArtist": "Maroon 5",
	}
	res := applyVerdictForTest(itunesRaw, verdict)
	if res.ITunesTrackID != 6763663512 {
		t.Errorf("expected hybrid fallback to set TrackID=6763663512, got %d", res.ITunesTrackID)
	}
	if res.ITunesURL != "https://music.apple.com/jp/song/6763663512" {
		t.Errorf("ITunesURL = %q", res.ITunesURL)
	}
	if res.ArtworkURL != "https://example.com/v/600x600bb.jpg" {
		t.Errorf("ArtworkURL = %q", res.ArtworkURL)
	}
}

// TestEnrichHybrid_ArtistMismatchKeepsLLMNull confirms the safety
// rail: if iTunes' top result is a completely different artist, the
// fallback is rejected and we end up without an iTunes link.
func TestEnrichHybrid_ArtistMismatchKeepsLLMNull(t *testing.T) {
	itunesRaw := itunesResultsFixture(
		map[string]interface{}{
			"trackId":       float64(123),
			"trackName":     "My Way",
			"artistName":    "Calvin Harris", // unrelated
			"artworkUrl100": "https://example.com/cal/100x100bb.jpg",
		},
	)
	verdict := map[string]interface{}{
		"trackId":         nil,
		"canonicalTitle":  "If I Hadn't Got You",
		"canonicalArtist": "Chris Braide", // does not match "Calvin Harris"
	}
	res := applyVerdictForTest(itunesRaw, verdict)
	if res.ITunesTrackID != 0 {
		t.Errorf("expected no fallback for artist mismatch, got TrackID=%d", res.ITunesTrackID)
	}
	if res.ITunesURL != "" {
		t.Errorf("expected empty ITunesURL, got %q", res.ITunesURL)
	}
	if res.ArtworkURL != "" {
		t.Errorf("expected empty ArtworkURL, got %q (suppress to keep parity with link)", res.ArtworkURL)
	}
}

// TestEnrichHybrid_LLMPickedTrustsLLM ensures we still trust the LLM
// when it does pick a trackId — even if a different result is at the
// top of the iTunes list.
func TestEnrichHybrid_LLMPickedTrustsLLM(t *testing.T) {
	itunesRaw := itunesResultsFixture(
		map[string]interface{}{
			"trackId":       float64(111),
			"trackName":     "Wrong Song",
			"artistName":    "Wrong Artist",
			"artworkUrl100": "https://wrong/100x100bb.jpg",
		},
		map[string]interface{}{
			"trackId":       float64(222),
			"trackName":     "Right Song",
			"artistName":    "Right Artist",
			"artworkUrl100": "https://right/100x100bb.jpg",
		},
	)
	verdict := map[string]interface{}{
		"trackId":         float64(222), // LLM picked second result
		"canonicalTitle":  "Right Song",
		"canonicalArtist": "Right Artist",
	}
	res := applyVerdictForTest(itunesRaw, verdict)
	if res.ITunesTrackID != 222 {
		t.Errorf("expected TrackID=222 (LLM choice), got %d", res.ITunesTrackID)
	}
	// Artwork should follow the LLM-picked track, not the top result.
	if res.ArtworkURL != "https://right/600x600bb.jpg" {
		t.Errorf("ArtworkURL = %q, expected to track the LLM-picked id", res.ArtworkURL)
	}
}

// TestEnrichHybrid_ArtistTransliterationMatches verifies the
// normalization layer collapses "Maroon 5" / "マルーン5" — but only
// after ASCII full-width conversion; pure transliteration is NOT
// covered by Normalize, so this must still rely on the LLM
// canonicalArtist string carrying the same form as iTunes returns.
func TestEnrichHybrid_ArtistTransliterationMatches(t *testing.T) {
	// iTunes returns artistName in fullwidth digits "5" → "5"; the LLM
	// canonical happens to be ASCII. Normalize folds NFKC so they match.
	itunesRaw := itunesResultsFixture(
		map[string]interface{}{
			"trackId":       float64(999),
			"artistName":    "MAROON 5", // from iTunes country=us perhaps
			"artworkUrl100": "https://x/100x100bb.jpg",
		},
	)
	verdict := map[string]interface{}{
		"trackId":         nil,
		"canonicalArtist": "Maroon 5",
	}
	res := applyVerdictForTest(itunesRaw, verdict)
	if res.ITunesTrackID != 999 {
		t.Errorf("expected fallback to recognize 'MAROON 5' == 'Maroon 5', got TrackID=%d", res.ITunesTrackID)
	}
}
