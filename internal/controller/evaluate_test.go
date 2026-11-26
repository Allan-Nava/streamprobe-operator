// SPDX-License-Identifier: MIT

package controller

import (
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	streamingv1alpha1 "github.com/Allan-Nava/streamprobe-operator/api/v1alpha1"
	"github.com/Allan-Nava/streamprobe-operator/internal/hls"
	"github.com/Allan-Nava/streamprobe-operator/internal/probe"
)

var t0 = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

// start is t0 as the time of the last advance.
func start() *metav1.Time { t := metav1.NewTime(t0); return &t }

func live(seq int64, segments int) *probe.Result {
	return &probe.Result{PlaylistURL: "https://o/x.m3u8", TargetDuration: 6, MediaSequence: seq, Segments: segments,
		SegmentURL: "https://o/s.ts", SegmentStatus: 200, PlaylistLatency: 40 * time.Millisecond}
}

func TestEvaluate(t *testing.T) {
	ended := live(10, 3)
	ended.Ended = true
	missing := live(10, 3)
	missing.SegmentStatus = 404
	noAnswer := live(10, 3)
	noAnswer.SegmentStatus = 0

	for _, c := range []struct {
		name       string
		prev       streamingv1alpha1.StreamProbeStatus
		staleAfter *metav1.Duration
		res        *probe.Result
		err        error
		now        time.Duration
		reason     string
		advanced   bool
	}{
		{name: "first check is live", res: live(10, 3), reason: streamingv1alpha1.ReasonLive, advanced: true},
		{name: "a stream that just started: edge and media sequence 0 are kept, not dropped",
			res: live(0, 1), reason: streamingv1alpha1.ReasonLive, advanced: true},
		{name: "the edge moved", prev: streamingv1alpha1.StreamProbeStatus{LiveEdge: ptr.To[int64](12), LastAdvanceTime: start()},
			res: live(11, 3), now: 30 * time.Second, reason: streamingv1alpha1.ReasonLive, advanced: true},
		{name: "an EVENT playlist grows with the same media sequence",
			prev: streamingv1alpha1.StreamProbeStatus{LiveEdge: ptr.To[int64](12), LastAdvanceTime: start()},
			res:  live(10, 4), now: 30 * time.Second, reason: streamingv1alpha1.ReasonLive, advanced: true},
		{name: "still within three target durations",
			prev: streamingv1alpha1.StreamProbeStatus{LiveEdge: ptr.To[int64](12), LastAdvanceTime: start()},
			res:  live(10, 3), now: 17 * time.Second, reason: streamingv1alpha1.ReasonLive},
		{name: "stuck past three target durations",
			prev: streamingv1alpha1.StreamProbeStatus{LiveEdge: ptr.To[int64](12), LastAdvanceTime: start()},
			res:  live(10, 3), now: 19 * time.Second, reason: streamingv1alpha1.ReasonStale},
		{name: "staleAfter from the spec wins",
			prev:       streamingv1alpha1.StreamProbeStatus{LiveEdge: ptr.To[int64](12), LastAdvanceTime: start()},
			staleAfter: &metav1.Duration{Duration: time.Minute},
			res:        live(10, 3), now: 50 * time.Second, reason: streamingv1alpha1.ReasonLive},
		{name: "an encoder restart is an advance",
			prev: streamingv1alpha1.StreamProbeStatus{LiveEdge: ptr.To[int64](900), LastAdvanceTime: start()},
			res:  live(0, 3), now: time.Hour, reason: streamingv1alpha1.ReasonLive, advanced: true},
		{name: "end list", res: ended, reason: streamingv1alpha1.ReasonEnded, advanced: true},
		{name: "segment 404", res: missing, reason: streamingv1alpha1.ReasonSegmentFailed, advanced: true},
		{name: "segment no answer", res: noAnswer, reason: streamingv1alpha1.ReasonSegmentFailed, advanced: true},
		{name: "playlist fetch fails", prev: streamingv1alpha1.StreamProbeStatus{ConsecutiveFailures: 2},
			err: &probe.FetchError{URL: "u", Err: probe.ErrNoSegments}, reason: streamingv1alpha1.ReasonFetchFailed},
	} {
		t.Run(c.name, func(t *testing.T) {
			sp := &streamingv1alpha1.StreamProbe{
				ObjectMeta: metav1.ObjectMeta{Generation: 3},
				Spec:       streamingv1alpha1.StreamProbeSpec{StaleAfter: c.staleAfter},
				Status:     c.prev,
			}
			st, v := evaluate(sp, c.res, c.err, t0.Add(c.now))
			if v.reason != c.reason {
				t.Fatalf("reason %s (%s), want %s", v.reason, v.message, c.reason)
			}
			if (v.status == metav1.ConditionTrue) != (c.reason == streamingv1alpha1.ReasonLive) {
				t.Errorf("status %s for reason %s", v.status, v.reason)
			}
			if st.ObservedGeneration != 3 || st.LastCheckTime == nil || !st.LastCheckTime.Time.Equal(t0.Add(c.now)) {
				t.Errorf("generation %d, last check %v", st.ObservedGeneration, st.LastCheckTime)
			}
			if c.err != nil {
				if st.ConsecutiveFailures != c.prev.ConsecutiveFailures+1 || st.LastError == "" {
					t.Errorf("failures %d, error %q", st.ConsecutiveFailures, st.LastError)
				}
				return
			}
			if st.ConsecutiveFailures != 0 || st.LastError != "" {
				t.Errorf("a good check keeps failures %d, error %q", st.ConsecutiveFailures, st.LastError)
			}
			if want := c.res.MediaSequence + int64(c.res.Segments) - 1; st.LiveEdge == nil || *st.LiveEdge != want {
				t.Errorf("live edge %v, want %d", st.LiveEdge, want)
			}
			if st.MediaSequence == nil || *st.MediaSequence != c.res.MediaSequence {
				t.Errorf("media sequence %v, want %d", st.MediaSequence, c.res.MediaSequence)
			}
			moved := st.LastAdvanceTime != nil && st.LastAdvanceTime.Time.Equal(t0.Add(c.now))
			if moved != c.advanced {
				t.Errorf("advanced %v, want %v", moved, c.advanced)
			}
		})
	}
}

func TestIntervalHasAFloor(t *testing.T) {
	sp := &streamingv1alpha1.StreamProbe{Spec: streamingv1alpha1.StreamProbeSpec{Interval: metav1.Duration{Duration: time.Second}}}
	if got := interval(sp); got != minInterval {
		t.Fatalf("interval %s, want %s", got, minInterval)
	}
}

func TestEvaluateVariants(t *testing.T) {
	ok := func(uri string, seq int64) probe.VariantResult {
		return probe.VariantResult{Variant: hls.Variant{URI: uri, Bandwidth: 1000000},
			Result: &probe.Result{TargetDuration: 6, MediaSequence: seq, Segments: 3, SegmentURL: "s", SegmentStatus: 200}}
	}
	failed := probe.VariantResult{Variant: hls.Variant{URI: "gone.m3u8"}, Err: &probe.FetchError{URL: "gone.m3u8", Err: probe.ErrNoSegments}}
	ended := ok("ended.m3u8", 10)
	ended.Result.Ended = true

	for _, c := range []struct {
		name    string
		prev    []streamingv1alpha1.VariantStatus
		results []probe.VariantResult
		now     time.Duration
		reason  string
		prefix  string
	}{
		{name: "all live", results: []probe.VariantResult{ok("a", 10), ok("b", 10)},
			reason: streamingv1alpha1.ReasonLive, prefix: "2 of 2 variants live"},
		{name: "one stale among moving ones",
			prev:    []streamingv1alpha1.VariantStatus{{URI: "a", LiveEdge: ptr.To[int64](12), LastAdvanceTime: start()}, {URI: "b", LiveEdge: ptr.To[int64](12), LastAdvanceTime: start()}},
			results: []probe.VariantResult{ok("a", 20), ok("b", 10)}, now: time.Minute,
			reason: streamingv1alpha1.ReasonStale, prefix: "variant b (1000 kb/s): the live edge"},
		{name: "a failing variant beats a stale one",
			prev:    []streamingv1alpha1.VariantStatus{{URI: "a", LiveEdge: ptr.To[int64](12), LastAdvanceTime: start()}},
			results: []probe.VariantResult{ok("a", 10), failed}, now: time.Minute,
			reason: streamingv1alpha1.ReasonFetchFailed, prefix: "variant gone.m3u8: playlist gone.m3u8"},
		{name: "ended", results: []probe.VariantResult{ok("a", 10), ended},
			reason: streamingv1alpha1.ReasonEnded, prefix: "variant ended.m3u8 (1000 kb/s): the playlist has #EXT-X-ENDLIST"},
		{name: "no variant", results: nil,
			reason: streamingv1alpha1.ReasonFetchFailed, prefix: "the multivariant playlist lists no variant"},
	} {
		t.Run(c.name, func(t *testing.T) {
			sp := &streamingv1alpha1.StreamProbe{Status: streamingv1alpha1.StreamProbeStatus{Variants: c.prev}}
			lines, v := evaluateVariants(sp, c.results, t0.Add(c.now))
			if v.reason != c.reason || !strings.HasPrefix(v.message, c.prefix) {
				t.Fatalf("got %s %q, want %s %q…", v.reason, v.message, c.reason, c.prefix)
			}
			if len(lines) != len(c.results) {
				t.Fatalf("%d lines for %d variants", len(lines), len(c.results))
			}
		})
	}
}
