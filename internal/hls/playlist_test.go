// SPDX-License-Identifier: MIT

package hls

import (
	"errors"
	"strings"
	"testing"
)

func TestParseMediaPlaylist(t *testing.T) {
	p, err := Parse(strings.NewReader(`#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:6
#EXT-X-MEDIA-SEQUENCE:1042

#EXTINF:6.000,
seg1042.ts
#EXTINF:6.000,
seg1043.ts
`))
	if err != nil {
		t.Fatal(err)
	}
	if p.Multivariant || p.Ended {
		t.Fatalf("got multivariant=%v ended=%v, want both false", p.Multivariant, p.Ended)
	}
	if p.TargetDuration != 6 || p.MediaSequence != 1042 {
		t.Fatalf("got target %d sequence %d", p.TargetDuration, p.MediaSequence)
	}
	if len(p.Segments) != 2 || p.Segments[1] != "seg1043.ts" {
		t.Fatalf("got segments %v", p.Segments)
	}
}

func TestParseMultivariantPlaylist(t *testing.T) {
	p, err := Parse(strings.NewReader("\ufeff#EXTM3U\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=800000,RESOLUTION=640x360\nlow/index.m3u8\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=2400000,RESOLUTION=1280x720\nhigh/index.m3u8\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []Variant{
		{URI: "low/index.m3u8", Bandwidth: 800000, Resolution: "640x360"},
		{URI: "high/index.m3u8", Bandwidth: 2400000, Resolution: "1280x720"},
	}
	if !p.Multivariant || len(p.Variants) != 2 || p.Variants[0] != want[0] || p.Variants[1] != want[1] {
		t.Fatalf("got multivariant=%v variants=%+v", p.Multivariant, p.Variants)
	}
	if len(p.Segments) != 0 {
		t.Fatalf("a multivariant playlist has no segments, got %v", p.Segments)
	}
}

func TestParseStreamInfWithQuotedCommas(t *testing.T) {
	p, err := Parse(strings.NewReader("#EXTM3U\n" +
		"#EXT-X-STREAM-INF:CODECS=\"avc1.4d401f,mp4a.40.2\",BANDWIDTH=1500000,RESOLUTION=960x540\nmid.m3u8\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Variants) != 1 || p.Variants[0] != (Variant{URI: "mid.m3u8", Bandwidth: 1500000, Resolution: "960x540"}) {
		t.Fatalf("got %+v", p.Variants)
	}
}

func TestParseEndList(t *testing.T) {
	p, err := Parse(strings.NewReader("#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXTINF:4,\na.ts\n#EXT-X-ENDLIST\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !p.Ended {
		t.Fatal("want Ended")
	}
}

func TestParseRejects(t *testing.T) {
	for name, body := range map[string]string{
		"empty":        "",
		"html":         "<!doctype html><title>404</title>",
		"bad target":   "#EXTM3U\n#EXT-X-TARGETDURATION:six\n",
		"bad mediaseq": "#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:x\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(body))
			if err == nil {
				t.Fatal("want an error")
			}
			if (name == "empty" || name == "html") && !errors.Is(err, ErrNotPlaylist) {
				t.Fatalf("want ErrNotPlaylist, got %v", err)
			}
		})
	}
}

func TestResolve(t *testing.T) {
	for _, c := range []struct{ base, ref, want string }{
		{"https://cdn.example/live/master.m3u8", "high/index.m3u8", "https://cdn.example/live/high/index.m3u8"},
		{"https://cdn.example/live/high/index.m3u8", "/other/seg.ts", "https://cdn.example/other/seg.ts"},
		{"https://cdn.example/live/index.m3u8", "https://edge.example/seg.ts", "https://edge.example/seg.ts"},
	} {
		got, err := Resolve(c.base, c.ref)
		if err != nil || got != c.want {
			t.Errorf("Resolve(%q, %q) = %q, %v; want %q", c.base, c.ref, got, err, c.want)
		}
	}
}
