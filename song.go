package onairlogsync

import "time"

// Song is a canonical airplay subject identified by the normalized
// (title, artist) pair. Multiple Plays reference the same Song.
type Song struct {
	Title         string     `firestore:"title" json:"title"`
	Artist        string     `firestore:"artist" json:"artist"`
	NormalizedKey string     `firestore:"normalizedKey" json:"-"`
	FirstAired    *time.Time `firestore:"firstAired" json:"firstAired"`
	LastAired     *time.Time `firestore:"lastAired" json:"lastAired"`
	PlayCount     int        `firestore:"playCount" json:"playCount"`
}

// Play is a single airplay event and references a Song by ID.
type Play struct {
	SongID    string     `firestore:"songId" json:"songId"`
	Time      *time.Time `firestore:"time" json:"time"`
	RawTitle  string     `firestore:"rawTitle" json:"rawTitle"`
	RawArtist string     `firestore:"rawArtist" json:"rawArtist"`
}
