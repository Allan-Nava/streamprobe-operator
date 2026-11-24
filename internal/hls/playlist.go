// SPDX-License-Identifier: MIT

// Package hls reads the few things of an HLS playlist that tell whether a live stream is
// moving: the target duration, the media sequence, the segments and the end tag. It is not a
// full parser: tags it does not need are skipped, and a multivariant (master) playlist only
// yields its variant URIs, so the prober can follow one of them.
package hls

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
)

// Playlist is what a probe needs from one fetch of a playlist.
type Playlist struct {
	// Multivariant is true for a master playlist: it lists variants, not segments.
	Multivariant bool
	// Variants are the variants of a multivariant playlist, in the order listed.
	Variants []Variant

	// TargetDuration is #EXT-X-TARGETDURATION, in seconds: the longest a segment may be.
	TargetDuration int
	// MediaSequence is #EXT-X-MEDIA-SEQUENCE: the number of the first segment listed. On a
	// live stream it grows as old segments leave the window.
	MediaSequence int64
	// Segments are the segment URIs, in order; the last one is the live edge.
	Segments []string
	// Ended is true when the playlist has #EXT-X-ENDLIST: the stream is over or is VOD.
	Ended bool
}

// Variant is one #EXT-X-STREAM-INF entry: the URI of its media playlist and the attributes a
// person uses to tell variants apart.
type Variant struct {
	URI        string
	Bandwidth  int64  // BANDWIDTH, bits per second; 0 when missing
	Resolution string // RESOLUTION, e.g. 1280x720; empty when missing
}

// ErrNotPlaylist is returned when the body does not start with #EXTM3U.
var ErrNotPlaylist = errors.New("not an HLS playlist: missing #EXTM3U")

// Parse reads a playlist. Lines are trimmed; blank lines and unknown tags are skipped.
func Parse(r io.Reader) (*Playlist, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)

	p := &Playlist{}
	first := true
	var pending *Variant
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if first {
			// A byte-order mark may precede the first tag.
			line = strings.TrimPrefix(line, "\ufeff")
			if line != "#EXTM3U" {
				return nil, ErrNotPlaylist
			}
			first = false
			continue
		}
		switch {
		case line == "":
		case strings.HasPrefix(line, "#EXT-X-STREAM-INF:"):
			p.Multivariant = true
			pending = streamInf(strings.TrimPrefix(line, "#EXT-X-STREAM-INF:"))
		case strings.HasPrefix(line, "#EXT-X-TARGETDURATION:"):
			v, err := strconv.Atoi(strings.TrimPrefix(line, "#EXT-X-TARGETDURATION:"))
			if err != nil {
				return nil, fmt.Errorf("bad #EXT-X-TARGETDURATION: %w", err)
			}
			p.TargetDuration = v
		case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
			v, err := strconv.ParseInt(strings.TrimPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("bad #EXT-X-MEDIA-SEQUENCE: %w", err)
			}
			p.MediaSequence = v
		case line == "#EXT-X-ENDLIST":
			p.Ended = true
		case strings.HasPrefix(line, "#"):
			// Another tag or a comment.
		default:
			if pending != nil {
				pending.URI = line
				p.Variants = append(p.Variants, *pending)
				pending = nil
			} else if !p.Multivariant {
				p.Segments = append(p.Segments, line)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if first {
		return nil, ErrNotPlaylist
	}
	return p, nil
}

// streamInf reads BANDWIDTH and RESOLUTION from an #EXT-X-STREAM-INF attribute list. Values in
// quotes may hold commas (CODECS="avc1.4d401f,mp4a.40.2"), so the list is split by hand.
func streamInf(attrs string) *Variant {
	v := &Variant{}
	for _, kv := range splitAttributes(attrs) {
		key, value, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		switch key {
		case "BANDWIDTH":
			v.Bandwidth, _ = strconv.ParseInt(value, 10, 64)
		case "RESOLUTION":
			v.Resolution = value
		}
	}
	return v
}

func splitAttributes(s string) []string {
	var out []string
	start, quoted := 0, false
	for i, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
		case r == ',' && !quoted:
			out = append(out, strings.TrimSpace(s[start:i]))
			start = i + 1
		}
	}
	return append(out, strings.TrimSpace(s[start:]))
}

// Resolve turns a URI found in a playlist into an absolute URL, relative to the playlist's.
func Resolve(base, ref string) (string, error) {
	b, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	r, err := url.Parse(ref)
	if err != nil {
		return "", err
	}
	return b.ResolveReference(r).String(), nil
}
