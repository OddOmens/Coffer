package files

import "testing"

func TestVersionOf(t *testing.T) {
	cases := []struct {
		name string
		key  string
		v    int
		ok   bool
	}{
		{"spot_v11.mov", "spot\x00.mov", 11, true},
		{"spot_v12_final.mov", "spot\x00.mov", 12, true},
		{"Spot V3.aep", "spot\x00.aep", 3, true},
		{"logo-v002.png", "logo\x00.png", 2, true},
		{"brief.docx", "", 0, false},
		{"v2.mov", "", 0, false},
		{"movie.mov", "", 0, false},
		{"dev1.txt", "", 0, false},
		{"review v4 notes.txt", "review\x00.txt", 4, true},
	}
	for _, c := range cases {
		key, v, ok := VersionOf(c.name)
		if key != c.key || v != c.v || ok != c.ok {
			t.Errorf("%s: got %q %d %v", c.name, key, v, ok)
		}
	}
}
