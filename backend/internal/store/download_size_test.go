package store

import "testing"

func TestDownloadSize(t *testing.T) {
	for _, check := range []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"}, {1, "1 B"}, {999, "999 B"}, {1000, "1.0 kB"},
		{11560, "11.6 kB"}, {1000000, "1.0 MB"}, {18385963, "18.4 MB"},
	} {
		if got := downloadSize(check.bytes); got != check.want {
			t.Errorf("downloadSize(%d) = %q; want %q", check.bytes, got, check.want)
		}
	}
}
