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
	// Variants are the variant playlist URIs of a multivariant playlist, in order.
	Variants []string

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

// ErrNotPlaylist is returned when the body does not start with #EXTM3U.
var ErrNotPlaylist = errors.New("not an HLS playlist: missing #EXTM3U")

// Parse reads a playlist. Lines are trimmed; blank lines and unknown tags are skipped.
func Parse(r io.Reader) (*Playlist, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)

	p := &Playlist{}
	first := true
	expectVariant := false
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
			expectVariant = true
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
			if expectVariant {
				p.Variants = append(p.Variants, line)
				expectVariant = false
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
