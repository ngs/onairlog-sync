package onairlogsync

import "testing"

func TestNormalize(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Yesterday", "yesterday"},
		{"YESTERDAY", "yesterday"},
		{"  Yesterday  ", "yesterday"},
		{"Yesterday (2009 Remaster)", "yesterday"},
		{"曲名(生演奏)", "曲名"},
		{"曲名(生演奏)(Live)", "曲名"},
		{"曲名（フルバージョン）", "曲名"},
		{"曲名[Live at Tokyo]", "曲名"},
		{"曲名［ライブ］", "曲名"},
		{"DAOKO ft. 米津玄師", "daoko feat. 米津玄師"},
		{"DAOKO Ft 米津玄師", "daoko feat. 米津玄師"},
		{"DAOKO featuring 米津玄師", "daoko feat. 米津玄師"},
		{"a   b\tc", "a b c"},
	}
	for _, c := range cases {
		got := Normalize(c.in)
		if got != c.want {
			t.Errorf("Normalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDisplayClean(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"YESTERDAY", "YESTERDAY"},
		{"Yesterday (2009 Remaster)", "Yesterday"},
		{"曲名(生演奏)", "曲名"},
		{"  曲名  ", "曲名"},
	}
	for _, c := range cases {
		got := DisplayClean(c.in)
		if got != c.want {
			t.Errorf("DisplayClean(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSongIDStability(t *testing.T) {
	// Different surface forms of the same song should hash to the same
	// canonical SongID.
	a := SongID("Yesterday", "The Beatles")
	b := SongID("YESTERDAY (2009 Remaster)", "the beatles")
	c := SongID("yesterday  ", " The Beatles ")
	if a != b || b != c {
		t.Errorf("SongID variants do not match: %s %s %s", a, b, c)
	}
}
