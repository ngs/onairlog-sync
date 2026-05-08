package onairlogsync

import (
	"encoding/json"
	"testing"
	"time"
)

// helper for *time.Time literals.
func ts(s string) *time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return &t
}

func TestBuildPlay(t *testing.T) {
	at := ts("2026-05-08T11:11:54+09:00")
	p := BuildPlay(at, "イデアが溢れて眠れない", "VAUNDY")
	if p.Time != at {
		t.Errorf("Play.Time pointer = %p, want %p", p.Time, at)
	}
	if !p.Time.Equal(*at) {
		t.Errorf("Play.Time = %v, want %v", p.Time, at)
	}
	if p.RawTitle != "イデアが溢れて眠れない" {
		t.Errorf("RawTitle = %q", p.RawTitle)
	}
	if p.RawArtist != "VAUNDY" {
		t.Errorf("RawArtist = %q", p.RawArtist)
	}
	if p.SongID == "" {
		t.Errorf("SongID empty")
	}
}

func TestNewSongFromPlay(t *testing.T) {
	at := ts("2026-05-08T11:11:54+09:00")
	s := NewSongFromPlay(at, "イデアが溢れて眠れない", "VAUNDY")
	if s.PlayCount != 1 {
		t.Errorf("PlayCount = %d", s.PlayCount)
	}
	if !s.FirstAired.Equal(*at) || !s.LastAired.Equal(*at) {
		t.Errorf("aired = (%v, %v), want both %v", s.FirstAired, s.LastAired, at)
	}
}

func TestApplyPlay_newerAirTime(t *testing.T) {
	// Existing lastAired is in the past; airTime is more recent.
	existing := Song{
		Title:      "Foo",
		Artist:     "Bar",
		FirstAired: ts("2020-01-01T00:00:00Z"),
		LastAired:  ts("2026-05-03T05:28:17Z"),
		PlayCount:  3,
	}
	at := ts("2026-05-08T02:11:54Z")
	updated := ApplyPlay(existing, at)

	if !updated.LastAired.Equal(*at) {
		t.Errorf("LastAired = %v, want %v", updated.LastAired, at)
	}
	if !updated.FirstAired.Equal(*existing.FirstAired) {
		t.Errorf("FirstAired changed: %v != %v", updated.FirstAired, existing.FirstAired)
	}
	if updated.PlayCount != 4 {
		t.Errorf("PlayCount = %d, want 4", updated.PlayCount)
	}
	// Defensive: ApplyPlay must not mutate the original.
	if !existing.LastAired.Equal(time.Date(2026, 5, 3, 5, 28, 17, 0, time.UTC)) {
		t.Errorf("ApplyPlay mutated input.LastAired")
	}
	if existing.PlayCount != 3 {
		t.Errorf("ApplyPlay mutated input.PlayCount")
	}
}

func TestApplyPlay_olderAirTime(t *testing.T) {
	// airTime is OLDER than existing.LastAired. lastAired should NOT
	// move backwards. The new play is still counted.
	existing := Song{
		Title:      "Foo",
		Artist:     "Bar",
		FirstAired: ts("2020-01-01T00:00:00Z"),
		LastAired:  ts("2026-05-08T02:11:54Z"),
		PlayCount:  4,
	}
	at := ts("2026-05-03T05:28:17Z")
	updated := ApplyPlay(existing, at)

	if !updated.LastAired.Equal(*existing.LastAired) {
		t.Errorf("LastAired = %v, want unchanged %v", updated.LastAired, existing.LastAired)
	}
	if updated.PlayCount != 5 {
		t.Errorf("PlayCount = %d, want 5", updated.PlayCount)
	}
}

func TestApplyPlay_olderThanFirstAired(t *testing.T) {
	// airTime is older than firstAired (radio digging up older history).
	existing := Song{
		FirstAired: ts("2020-01-01T00:00:00Z"),
		LastAired:  ts("2020-01-01T00:00:00Z"),
		PlayCount:  1,
	}
	at := ts("2010-05-20T00:00:00Z")
	updated := ApplyPlay(existing, at)

	if !updated.FirstAired.Equal(*at) {
		t.Errorf("FirstAired = %v, want %v", updated.FirstAired, at)
	}
	if !updated.LastAired.Equal(*existing.LastAired) {
		t.Errorf("LastAired changed unexpectedly")
	}
	if updated.PlayCount != 2 {
		t.Errorf("PlayCount = %d", updated.PlayCount)
	}
}

// TestPublishedPlay_payloadFreshAirplay simulates Sync inserting today's
// airplay for a song that already exists in Firestore. The resulting
// PublishedPlay must carry today's airTime in BOTH play.time and
// song.lastAired (because today > existing.lastAired).
func TestPublishedPlay_payloadFreshAirplay(t *testing.T) {
	existing := Song{
		Title:      "イデアが溢れて眠れない",
		Artist:     "VAUNDY",
		FirstAired: ts("2026-04-27T14:19:37Z"),
		LastAired:  ts("2026-05-03T05:28:17Z"),
		PlayCount:  3,
	}
	at := ts("2026-05-08T02:11:54Z") // today's airplay UTC
	pp := PublishedPlay{
		Play: BuildPlay(at, "イデアが溢れて眠れない", "VAUNDY"),
		Song: ApplyPlay(existing, at),
	}

	if !pp.Play.Time.Equal(*at) {
		t.Fatalf("play.Time = %v, want %v", pp.Play.Time, at)
	}
	if !pp.Song.LastAired.Equal(*at) {
		t.Fatalf("song.LastAired = %v, want %v", pp.Song.LastAired, at)
	}

	b, err := json.Marshal(pp)
	if err != nil {
		t.Fatal(err)
	}
	var decoded PublishedPlay
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Play.Time.Equal(*at) {
		t.Errorf("decoded play.Time = %v, want %v", decoded.Play.Time, at)
	}
	if !decoded.Song.LastAired.Equal(*at) {
		t.Errorf("decoded song.LastAired = %v, want %v", decoded.Song.LastAired, at)
	}
}

// TestPublishedPlay_payloadStaleAirplay covers the (suspicious in
// production) case where airTime is OLDER than existing.LastAired.
// The ASSERTION is: play.time MUST equal airTime (the value passed in),
// and song.lastAired MUST equal the existing (newer) value, NOT airTime.
//
// This pins the contract that play.time and song.lastAired are
// independent — diverging when the airTime is in the past.
func TestPublishedPlay_payloadStaleAirplay(t *testing.T) {
	existing := Song{
		Title:      "イデアが溢れて眠れない",
		Artist:     "VAUNDY",
		FirstAired: ts("2026-04-27T14:19:37Z"),
		LastAired:  ts("2026-05-08T02:11:54Z"),
		PlayCount:  4,
	}
	at := ts("2026-05-03T05:28:17Z") // older than existing.LastAired
	pp := PublishedPlay{
		Play: BuildPlay(at, "イデアが溢れて眠れない", "VAUNDY"),
		Song: ApplyPlay(existing, at),
	}

	if !pp.Play.Time.Equal(*at) {
		t.Errorf("play.Time = %v, want %v (must equal airTime regardless of song state)",
			pp.Play.Time, at)
	}
	if !pp.Song.LastAired.Equal(*existing.LastAired) {
		t.Errorf("song.LastAired = %v, want %v (must NOT regress to airTime)",
			pp.Song.LastAired, existing.LastAired)
	}
	if pp.Play.Time.Equal(*pp.Song.LastAired) {
		t.Errorf("play.Time and song.LastAired ended up equal (%v) — production bug pattern",
			pp.Play.Time)
	}

	// JSON round-trip — values must survive serialization unchanged.
	b, err := json.Marshal(pp)
	if err != nil {
		t.Fatal(err)
	}
	var decoded PublishedPlay
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Play.Time.Equal(*at) {
		t.Errorf("after JSON round-trip play.Time = %v, want %v", decoded.Play.Time, at)
	}
	if !decoded.Song.LastAired.Equal(*existing.LastAired) {
		t.Errorf("after JSON round-trip song.LastAired = %v, want %v",
			decoded.Song.LastAired, existing.LastAired)
	}
}

// TestPublishedPlay_pointerAliasingIsBenign ensures that ApplyPlay
// re-using the airTime pointer for LastAired (via the conditional
// assignment) does not cause play.Time and song.LastAired to share
// fate even though they reference the same time.Time instance. Both
// point to airTime, both serialize as airTime — no value drift.
func TestPublishedPlay_pointerAliasingIsBenign(t *testing.T) {
	existing := Song{
		LastAired:  ts("2026-05-03T05:28:17Z"),
		FirstAired: ts("2020-01-01T00:00:00Z"),
		PlayCount:  3,
	}
	at := ts("2026-05-08T02:11:54Z")
	pp := PublishedPlay{
		Play: BuildPlay(at, "title", "artist"),
		Song: ApplyPlay(existing, at),
	}
	if pp.Play.Time != pp.Song.LastAired {
		t.Logf("note: pointers differ (Play.Time=%p, Song.LastAired=%p)", pp.Play.Time, pp.Song.LastAired)
	}
	// Both must report airTime regardless of pointer identity.
	if !pp.Play.Time.Equal(*at) || !pp.Song.LastAired.Equal(*at) {
		t.Errorf("expected both = airTime, got play.Time=%v song.LastAired=%v", pp.Play.Time, pp.Song.LastAired)
	}
}
