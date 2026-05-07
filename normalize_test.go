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
		{"DAOKO ft. 米津玄師", "daoko 米津玄師"},
		{"DAOKO Ft 米津玄師", "daoko 米津玄師"},
		{"DAOKO featuring 米津玄師", "daoko 米津玄師"},
		{"a   b\tc", "a b c"},
		// Halfwidth/fullwidth katakana
		{"ﾐｭｰｼﾞｯｸ", "ミュージック"},
		{"ﾐｭｰｼﾞｯｸ", Normalize("ミュージック")},
		{"ｻｶﾅｸｼｮﾝ", "サカナクション"},
		// Fullwidth ASCII
		{"ＤＡＯＫＯ", "daoko"},
		// Quotes / punctuation
		{`"HEROES"`, "heroes"},
		{`"... AND I KEPT HEARING"`, "i kept hearing"},
		{`"...AND I KEPT HEARING"`, "i kept hearing"},
		{`"MA-MA" FC`, "ma ma fc"},
		{`"MA-MA"FC`, "ma ma fc"},
		// Artist separators: AND / & / / / , / +
		{"BALLAKE SISSOKO AND VINCENT SEGAL", "ballake sissoko vincent segal"},
		{"BALLAKE SISSOKO/VINCENT SEGAL", "ballake sissoko vincent segal"},
		{"BALLAKE SISSOKO & VINCENT SEGAL", "ballake sissoko vincent segal"},
		{"BALLAKE SISSOKO, VINCENT SEGAL", "ballake sissoko vincent segal"},
		// "and" word boundary protection
		{"land", "land"},
		{"candy", "candy"},
		// Curly quotes / dashes
		{"It’s a test", "it s a test"},
		{"foo—bar", "foo bar"},
		// Zero-width / BOM / soft hyphen — silently dropped
		{"\u200Byesterday", "yesterday"},
		{"yesterday\u200B", "yesterday"},
		{"yes\u200Bterday", "yesterday"},
		{"\uFEFFyesterday", "yesterday"},
		{"yes\u00ADterday", "yesterday"},
		{"\u200Dyester\u200Cday\u200D", "yesterday"},
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
		// Outer quotes get unwrapped
		{`"HEROES"`, "HEROES"},
		{`'HEROES'`, "HEROES"},
		{"“HEROES”", "HEROES"},
		{"‘HEROES’", "HEROES"},
		{`"YOU'RE THE BEST PERSON IN THIS WORLD"`, "YOU'RE THE BEST PERSON IN THIS WORLD"},
		// Mismatched quotes — left alone
		{`"HEROES`, `"HEROES`},
		{`HEROES"`, `HEROES"`},
		{`"HEROES'`, `"HEROES'`},
		// Invisible chars at start/end stripped
		{"\u200BYesterday\u200B", "Yesterday"},
		{"\uFEFFYesterday", "Yesterday"},
		{"Yes\u200Bterday", "Yesterday"},
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

	// halfwidth/fullwidth + separator should also collapse
	d := SongID("ミュージック", "サカナクション")
	e := SongID("ﾐｭｰｼﾞｯｸ", "ｻｶﾅｸｼｮﾝ")
	if d != e {
		t.Errorf("kana variants do not match: %s %s", d, e)
	}

	f := SongID(`"MA-MA" FC`, "BALLAKE SISSOKO AND VINCENT SEGAL")
	g := SongID(`"MA-MA"FC`, "BALLAKE SISSOKO/VINCENT SEGAL")
	h := SongID(`"MA-MA" FC`, "BALLAKE SISSOKO & VINCENT SEGAL")
	if f != g || g != h {
		t.Errorf("punct/separator variants do not match: %s %s %s", f, g, h)
	}

	// Invisible chars must not break stability.
	x := SongID("Yesterday", "The Beatles")
	y := SongID("\u200BYesterday\u200B", "\uFEFFThe Beatles")
	if x != y {
		t.Errorf("invisible char variants do not match: %s %s", x, y)
	}
}
