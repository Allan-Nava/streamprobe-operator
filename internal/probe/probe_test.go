// SPDX-License-Identifier: MIT

package probe

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func server(t *testing.T, routes map[string]func(w http.ResponseWriter)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h, ok := routes[r.URL.Path]; ok {
			h(w)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func body(s string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) { _, _ = w.Write([]byte(s)) }
}

const media = "#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXT-X-MEDIA-SEQUENCE:7\n#EXTINF:6,\nseg7.ts\n#EXTINF:6,\nseg8.ts\n"

func TestCheckFollowsTheFirstVariantAndAsksForTheLiveEdge(t *testing.T) {
	srv := server(t, map[string]func(http.ResponseWriter){
		"/live/master.m3u8":    body("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nlow/index.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=2\nhigh/index.m3u8\n"),
		"/live/low/index.m3u8": body(media),
		"/live/low/seg8.ts":    body("segment"),
	})
	r, err := (&HTTPProber{}).Check(context.Background(), srv.URL+"/live/master.m3u8", true)
	if err != nil {
		t.Fatal(err)
	}
	if r.PlaylistURL != srv.URL+"/live/low/index.m3u8" {
		t.Errorf("PlaylistURL = %s", r.PlaylistURL)
	}
	if r.MediaSequence != 7 || r.TargetDuration != 6 || r.Segments != 2 {
		t.Errorf("got sequence %d target %d segments %d", r.MediaSequence, r.TargetDuration, r.Segments)
	}
	if r.SegmentURL != srv.URL+"/live/low/seg8.ts" || r.SegmentStatus != http.StatusOK {
		t.Errorf("segment %s status %d", r.SegmentURL, r.SegmentStatus)
	}
}

func TestCheckReportsAMissingSegmentWithoutFailing(t *testing.T) {
	srv := server(t, map[string]func(http.ResponseWriter){"/index.m3u8": body(media)})
	r, err := (&HTTPProber{}).Check(context.Background(), srv.URL+"/index.m3u8", true)
	if err != nil {
		t.Fatal(err)
	}
	if r.SegmentStatus != http.StatusNotFound {
		t.Errorf("SegmentStatus = %d, want 404", r.SegmentStatus)
	}
}

func TestCheckWithoutTheSegment(t *testing.T) {
	srv := server(t, map[string]func(http.ResponseWriter){"/index.m3u8": body(media)})
	r, err := (&HTTPProber{}).Check(context.Background(), srv.URL+"/index.m3u8", false)
	if err != nil {
		t.Fatal(err)
	}
	if r.SegmentURL != "" || r.SegmentStatus != 0 {
		t.Errorf("the segment was checked: %s %d", r.SegmentURL, r.SegmentStatus)
	}
}

func TestCheckFailures(t *testing.T) {
	srv := server(t, map[string]func(http.ResponseWriter){
		"/html.m3u8":  body("<html>"),
		"/empty.m3u8": body("#EXTM3U\n#EXT-X-TARGETDURATION:6\n"),
		"/boom.m3u8":  func(w http.ResponseWriter) { w.WriteHeader(http.StatusBadGateway) },
	})
	for _, path := range []string{"/html.m3u8", "/empty.m3u8", "/boom.m3u8", "/missing.m3u8"} {
		t.Run(path, func(t *testing.T) {
			_, err := (&HTTPProber{}).Check(context.Background(), srv.URL+path, true)
			if _, ok := errors.AsType[*FetchError](err); !ok {
				t.Fatalf("want a FetchError, got %v", err)
			}
		})
	}
}
