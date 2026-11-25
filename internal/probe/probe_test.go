// SPDX-License-Identifier: MIT

package probe

import (
	"context"
	"errors"
	"fmt"
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

const indexPath = "/index.m3u8"

const media = "#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXT-X-MEDIA-SEQUENCE:7\n#EXTINF:6,\nseg7.ts\n#EXTINF:6,\nseg8.ts\n"

func TestCheckFollowsTheFirstVariantAndAsksForTheLiveEdge(t *testing.T) {
	srv := server(t, map[string]func(http.ResponseWriter){
		"/live/master.m3u8":    body("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nlow/index.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=2\nhigh/index.m3u8\n"),
		"/live/low/index.m3u8": body(media),
		"/live/low/seg8.ts":    body("segment"),
	})
	r, err := (&HTTPProber{}).Check(context.Background(), srv.URL+"/live/master.m3u8", Options{CheckSegment: true})
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
	srv := server(t, map[string]func(http.ResponseWriter){indexPath: body(media)})
	r, err := (&HTTPProber{}).Check(context.Background(), srv.URL+indexPath, Options{CheckSegment: true})
	if err != nil {
		t.Fatal(err)
	}
	if r.SegmentStatus != http.StatusNotFound {
		t.Errorf("SegmentStatus = %d, want 404", r.SegmentStatus)
	}
}

func TestCheckWithoutTheSegment(t *testing.T) {
	srv := server(t, map[string]func(http.ResponseWriter){indexPath: body(media)})
	r, err := (&HTTPProber{}).Check(context.Background(), srv.URL+indexPath, Options{})
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
			_, err := (&HTTPProber{}).Check(context.Background(), srv.URL+path, Options{CheckSegment: true})
			if _, ok := errors.AsType[*FetchError](err); !ok {
				t.Fatalf("want a FetchError, got %v", err)
			}
		})
	}
}

func TestCheckAllReadsEveryVariantAndReportsTheBrokenOne(t *testing.T) {
	srv := server(t, map[string]func(http.ResponseWriter){
		"/live/master.m3u8": body("#EXTM3U\n" +
			"#EXT-X-STREAM-INF:BANDWIDTH=800000,RESOLUTION=640x360\nlow/index.m3u8\n" +
			"#EXT-X-STREAM-INF:BANDWIDTH=2400000,RESOLUTION=1280x720\nhigh/index.m3u8\n"),
		"/live/low/index.m3u8": body(media),
		"/live/low/seg8.ts":    body("segment"),
		// high/index.m3u8 is missing: that variant must fail on its own.
	})
	vs, err := (&HTTPProber{}).CheckAll(context.Background(), srv.URL+"/live/master.m3u8", Options{CheckSegment: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 2 {
		t.Fatalf("got %d variants", len(vs))
	}
	if vs[0].Err != nil || vs[0].Result.MediaSequence != 7 || vs[0].Variant.Bandwidth != 800000 {
		t.Errorf("low: %+v %v", vs[0].Variant, vs[0].Err)
	}
	if _, ok := errors.AsType[*FetchError](vs[1].Err); !ok || vs[1].Variant.Resolution != "1280x720" {
		t.Errorf("high: want a FetchError, got %+v %v", vs[1].Variant, vs[1].Err)
	}
}

func TestCheckAllOnAMediaPlaylistIsOneVariant(t *testing.T) {
	srv := server(t, map[string]func(http.ResponseWriter){indexPath: body(media)})
	vs, err := (&HTTPProber{}).CheckAll(context.Background(), srv.URL+indexPath, Options{})
	if err != nil || len(vs) != 1 || vs[0].Variant.URI != srv.URL+indexPath || vs[0].Err != nil {
		t.Fatalf("got %+v, %v", vs, err)
	}
}

func TestHeadersGoToTheURLsHostOnly(t *testing.T) {
	var seen []string
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, "cdn:"+r.Header.Get("Authorization"))
		_, _ = w.Write([]byte("segment"))
	}))
	t.Cleanup(cdn.Close)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, "origin:"+r.Header.Get("Authorization"))
		_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXT-X-MEDIA-SEQUENCE:1\n#EXTINF:6,\n" + cdn.URL + "/seg1.ts\n"))
	}))
	t.Cleanup(origin.Close)
	headers := http.Header{"Authorization": {"Bearer s3cret"}}

	seen = nil
	if _, err := (&HTTPProber{}).Check(context.Background(), origin.URL+"/live.m3u8", Options{CheckSegment: true, Headers: headers}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"origin:Bearer s3cret", "cdn:"}; fmt.Sprint(seen) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v: the token must not reach the CDN", seen, want)
	}

	seen = nil
	if _, err := (&HTTPProber{}).Check(context.Background(), origin.URL+"/live.m3u8", Options{CheckSegment: true, Headers: headers, AllHosts: true}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"origin:Bearer s3cret", "cdn:Bearer s3cret"}; fmt.Sprint(seen) != fmt.Sprint(want) {
		t.Errorf("with AllHosts got %v, want %v", seen, want)
	}
}
