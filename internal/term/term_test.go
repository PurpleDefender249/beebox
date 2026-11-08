package term

import "testing"

func TestUnicode(t *testing.T) {
	for _, tc := range []struct {
		term, lcAll, lcCtype, lang string
		want                       bool
	}{
		{"xterm-256color", "", "", "en_US.UTF-8", true},
		{"xterm-256color", "", "", "C.utf8", true},
		{"xterm-256color", "", "", "C", false},
		{"xterm-256color", "", "", "", false},
		{"xterm-256color", "C", "", "en_US.UTF-8", false}, // LC_ALL wins
		{"xterm-256color", "", "en_US.UTF-8", "C", true},  // then LC_CTYPE
		{"linux", "", "", "en_US.UTF-8", false},           // Linux console fonts vary
	} {
		t.Setenv("TERM", tc.term)
		t.Setenv("LC_ALL", tc.lcAll)
		t.Setenv("LC_CTYPE", tc.lcCtype)
		t.Setenv("LANG", tc.lang)
		if got := Unicode(); got != tc.want {
			t.Errorf("TERM=%s LC_ALL=%q LC_CTYPE=%q LANG=%q: Unicode() = %v, want %v",
				tc.term, tc.lcAll, tc.lcCtype, tc.lang, got, tc.want)
		}
	}
}

func TestColorDepth(t *testing.T) {
	for _, tc := range []struct {
		colorterm, term string
		want            Depth
	}{
		{"truecolor", "xterm-256color", DepthTrue},
		{"24bit", "xterm", DepthTrue},
		{"", "xterm-256color", Depth256},
		{"", "tmux-256color", Depth256},
		{"", "xterm-direct", DepthTrue},
		{"", "xterm", Depth16},
		{"", "linux", Depth16},
	} {
		t.Setenv("COLORTERM", tc.colorterm)
		t.Setenv("TERM", tc.term)
		if got := ColorDepth(); got != tc.want {
			t.Errorf("COLORTERM=%q TERM=%q: ColorDepth() = %d, want %d", tc.colorterm, tc.term, got, tc.want)
		}
	}
}
