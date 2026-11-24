// SPDX-License-Identifier: MIT

// Package probe makes one check of an HLS stream: fetch the playlist (following the first
// variant of a multivariant one), read where the live edge is, and ask the origin for the last
// segment. It knows nothing about Kubernetes: the controller decides what a result means
// (live, stale, ended) by comparing it with the previous one.
package probe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Allan-Nava/streamprobe-operator/internal/hls"
)

// maxPlaylist bounds the body read from a playlist URL: a live media playlist is a few KB, and
// a wrong URL can point at anything.
const maxPlaylist = 2 << 20

// Result is one check.
type Result struct {
	// PlaylistURL is the media playlist that was read: the probe's URL, or its first variant.
	PlaylistURL    string
	TargetDuration int
	MediaSequence  int64
	Segments       int
	Ended          bool
	// PlaylistLatency is the time to fetch the media playlist (the variant's, if followed).
	PlaylistLatency time.Duration

	// SegmentURL is the live-edge segment asked for, empty when the segment was not checked.
	SegmentURL     string
	SegmentStatus  int
	SegmentLatency time.Duration
}

// VariantResult is one variant of a multivariant playlist, checked: its Result, or the Err that
// kept it from being read. For a media playlist there is one, with the probe's URL as URI.
type VariantResult struct {
	Variant hls.Variant
	Result  *Result
	Err     error
}

// Prober checks a stream. The controller holds one; tests replace it.
type Prober interface {
	// Check reads the URL, or the first variant of a multivariant playlist.
	Check(ctx context.Context, url string, checkSegment bool) (*Result, error)
	// CheckAll reads every variant of a multivariant playlist (SP-12), one after the other within
	// the same deadline. The error is for the multivariant playlist itself; a variant that fails
	// carries its own Err.
	CheckAll(ctx context.Context, url string, checkSegment bool) ([]VariantResult, error)
}

// HTTPProber checks over HTTP with the client it is given; the deadline comes from the context.
type HTTPProber struct {
	Client    *http.Client
	UserAgent string
}

// FetchError is a playlist that could not be fetched or read.
type FetchError struct {
	URL string
	Err error
}

func (e *FetchError) Error() string { return fmt.Sprintf("playlist %s: %v", e.URL, e.Err) }
func (e *FetchError) Unwrap() error { return e.Err }

// ErrNoSegments is a media playlist that lists no segment.
var ErrNoSegments = errors.New("the media playlist lists no segment")

// Check fetches the playlist and, when checkSegment is true, the live-edge segment. A playlist
// that cannot be fetched or parsed is an error; a segment that answers with an error status is
// not: it is reported in the result, for the controller to judge.
func (p *HTTPProber) Check(ctx context.Context, url string, checkSegment bool) (*Result, error) {
	variants, err := p.check(ctx, url, checkSegment, false)
	if err != nil {
		return nil, err
	}
	return variants[0].Result, variants[0].Err
}

// CheckAll is Check for every variant.
func (p *HTTPProber) CheckAll(ctx context.Context, url string, checkSegment bool) ([]VariantResult, error) {
	return p.check(ctx, url, checkSegment, true)
}

func (p *HTTPProber) check(ctx context.Context, url string, checkSegment, all bool) ([]VariantResult, error) {
	pl, latency, err := p.playlist(ctx, url)
	if err != nil {
		return nil, &FetchError{URL: url, Err: err}
	}
	if !pl.Multivariant {
		r, err := p.media(ctx, url, pl, latency, checkSegment)
		return []VariantResult{{Variant: hls.Variant{URI: url}, Result: r, Err: err}}, nil
	}
	if len(pl.Variants) == 0 {
		return nil, &FetchError{URL: url, Err: errors.New("multivariant playlist with no variant")}
	}
	variants := pl.Variants
	if !all {
		variants = variants[:1]
	}
	out := make([]VariantResult, 0, len(variants))
	for _, v := range variants {
		vr := VariantResult{Variant: v}
		vr.Result, vr.Err = p.variant(ctx, url, v.URI, checkSegment)
		out = append(out, vr)
	}
	return out, nil
}

// variant reads one variant's media playlist and its live-edge segment.
func (p *HTTPProber) variant(ctx context.Context, base, uri string, checkSegment bool) (*Result, error) {
	playlistURL, err := hls.Resolve(base, uri)
	if err != nil {
		return nil, &FetchError{URL: base, Err: err}
	}
	pl, latency, err := p.playlist(ctx, playlistURL)
	if err != nil {
		return nil, &FetchError{URL: playlistURL, Err: err}
	}
	if pl.Multivariant {
		return nil, &FetchError{URL: playlistURL, Err: errors.New("a variant is itself a multivariant playlist")}
	}
	return p.media(ctx, playlistURL, pl, latency, checkSegment)
}

// media turns a fetched media playlist into a Result, asking for its live-edge segment.
func (p *HTTPProber) media(ctx context.Context, playlistURL string, pl *hls.Playlist, latency time.Duration, checkSegment bool) (*Result, error) {
	r := &Result{
		PlaylistURL:     playlistURL,
		TargetDuration:  pl.TargetDuration,
		MediaSequence:   pl.MediaSequence,
		Segments:        len(pl.Segments),
		Ended:           pl.Ended,
		PlaylistLatency: latency,
	}
	if len(pl.Segments) == 0 {
		return r, &FetchError{URL: playlistURL, Err: ErrNoSegments}
	}
	if !checkSegment {
		return r, nil
	}
	var err error
	if r.SegmentURL, err = hls.Resolve(playlistURL, pl.Segments[len(pl.Segments)-1]); err != nil {
		return r, &FetchError{URL: playlistURL, Err: err}
	}
	r.SegmentStatus, r.SegmentLatency = p.segment(ctx, r.SegmentURL)
	return r, nil
}

func (p *HTTPProber) do(ctx context.Context, method, url string) (*http.Response, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, 0, err
	}
	if p.UserAgent != "" {
		req.Header.Set("User-Agent", p.UserAgent)
	}
	start := time.Now()
	res, err := p.client().Do(req)
	return res, time.Since(start), err
}

func (p *HTTPProber) playlist(ctx context.Context, url string) (*hls.Playlist, time.Duration, error) {
	res, latency, err := p.do(ctx, http.MethodGet, url)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, latency, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	pl, err := hls.Parse(io.LimitReader(res.Body, maxPlaylist))
	return pl, latency, err
}

// segment asks for the segment and reads only its first bytes: the point is whether the origin
// serves it, not to download a whole segment on every check. A transport error is status 0.
func (p *HTTPProber) segment(ctx context.Context, url string) (int, time.Duration) {
	res, latency, err := p.do(ctx, http.MethodGet, url)
	if err != nil {
		return 0, latency
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.CopyN(io.Discard, res.Body, 1024)
	return res.StatusCode, latency
}

func (p *HTTPProber) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return http.DefaultClient
}
