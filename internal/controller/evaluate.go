// SPDX-License-Identifier: MIT

package controller

import (
	"errors"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	streamingv1alpha1 "github.com/Allan-Nava/streamprobe-operator/api/v1alpha1"
	"github.com/Allan-Nava/streamprobe-operator/internal/probe"
)

// verdict is the Ready condition a check leads to.
type verdict struct {
	status  metav1.ConditionStatus
	reason  string
	message string
}

// evaluate turns one check into the next status and the Ready verdict. It is pure: the
// previous status, the check and the time in, the new status out, so every rule is a table
// test without a cluster.
//
// The rules, in order:
//   - the playlist could not be read: FetchFailed;
//   - the playlist has #EXT-X-ENDLIST: Ended (a VOD URL, or a stream that finished);
//   - the live edge has not moved for longer than staleAfter: Stale;
//   - the live-edge segment was asked for and did not answer 2xx: SegmentFailed;
//   - otherwise: Live.
func evaluate(sp *streamingv1alpha1.StreamProbe, res *probe.Result, checkErr error, now time.Time) (streamingv1alpha1.StreamProbeStatus, verdict) {
	prev := sp.Status
	st := *prev.DeepCopy()
	st.ObservedGeneration = sp.Generation
	t := metav1.NewTime(now)
	st.LastCheckTime = &t

	if ce, ok := errors.AsType[*configError](checkErr); ok {
		st.LastError = ce.Error()
		return st, verdict{metav1.ConditionFalse, streamingv1alpha1.ReasonInvalidConfig, ce.Error()}
	}
	if checkErr != nil {
		st.ConsecutiveFailures++
		st.LastError = checkErr.Error()
		msg := checkErr.Error()
		if errors.Is(checkErr, probe.ErrNoSegments) {
			msg = "the media playlist lists no segment"
		}
		return st, verdict{metav1.ConditionFalse, streamingv1alpha1.ReasonFetchFailed, msg}
	}

	st.ConsecutiveFailures = 0
	st.LastError = ""
	st.PlaylistURL = res.PlaylistURL
	st.TargetDurationSeconds = int32(res.TargetDuration)
	st.PlaylistLatencyMilliseconds = res.PlaylistLatency.Milliseconds()
	st.SegmentStatus = int32(res.SegmentStatus)
	st.MediaSequence = new(res.MediaSequence)

	// Deref to 0: a status written before liveEdge was a pointer omitted an edge of 0.
	edge, advance, v := judge(sp, ptr.Deref(prev.LiveEdge, 0), prev.LastAdvanceTime, res, now)
	st.LiveEdge, st.LastAdvanceTime = new(edge), advance
	return st, v
}

// judge is the verdict on one good read of one media playlist, given where its live edge was
// and when it last moved. It returns the new edge and advance time with the verdict. Shared by
// the stream's own status and by each line of status.variants.
func judge(sp *streamingv1alpha1.StreamProbe, prevEdge int64, prevAdvance *metav1.Time, res *probe.Result, now time.Time) (int64, *metav1.Time, verdict) {
	t := metav1.NewTime(now)
	edge := res.MediaSequence + int64(res.Segments) - 1

	// The first good check counts as an advance: there is nothing to compare with yet. So does
	// an edge that went back, which is an encoder restart, not a stall.
	advance := prevAdvance
	if prevAdvance == nil || edge != prevEdge {
		advance = &t
	}

	if res.Ended {
		return edge, advance, verdict{metav1.ConditionFalse, streamingv1alpha1.ReasonEnded,
			"the playlist has #EXT-X-ENDLIST: the stream is over, or the URL is VOD"}
	}

	stuck := now.Sub(advance.Time)
	if limit := staleAfter(sp, res.TargetDuration); stuck > limit {
		return edge, advance, verdict{metav1.ConditionFalse, streamingv1alpha1.ReasonStale,
			fmt.Sprintf("the live edge has been at segment %d for %s (limit %s)", edge, stuck.Round(time.Second), limit)}
	}

	if res.SegmentURL != "" && (res.SegmentStatus < 200 || res.SegmentStatus > 299) {
		got := fmt.Sprintf("HTTP %d", res.SegmentStatus)
		if res.SegmentStatus == 0 {
			got = "no answer"
		}
		return edge, advance, verdict{metav1.ConditionFalse, streamingv1alpha1.ReasonSegmentFailed,
			fmt.Sprintf("the live-edge segment %s: %s", res.SegmentURL, got)}
	}

	return edge, advance, verdict{metav1.ConditionTrue, streamingv1alpha1.ReasonLive,
		fmt.Sprintf("live edge at segment %d, playlist in %dms", edge, res.PlaylistLatency.Milliseconds())}
}

// severity orders the reasons, worst first, to pick the verdict of a stream from its variants.
var severity = map[string]int{
	streamingv1alpha1.ReasonInvalidConfig: 6,
	streamingv1alpha1.ReasonFetchFailed:   5,
	streamingv1alpha1.ReasonEnded:         4,
	streamingv1alpha1.ReasonStale:         3,
	streamingv1alpha1.ReasonSegmentFailed: 2,
	streamingv1alpha1.ReasonLive:          1,
}

// evaluateVariants judges every variant against its own previous line (matched by URI) and
// returns the new lines and the worst verdict, its message naming the variant and counting the
// live ones. A variant missing from the previous lines starts fresh; one that left the
// playlist is dropped.
func evaluateVariants(sp *streamingv1alpha1.StreamProbe, results []probe.VariantResult, now time.Time) ([]streamingv1alpha1.VariantStatus, verdict) {
	prev := map[string]streamingv1alpha1.VariantStatus{}
	for _, vs := range sp.Status.Variants {
		prev[vs.URI] = vs
	}
	lines := make([]streamingv1alpha1.VariantStatus, 0, len(results))
	var worst verdict
	worstAt, live := -1, 0
	for i, r := range results {
		line := streamingv1alpha1.VariantStatus{URI: r.Variant.URI, Bandwidth: r.Variant.Bandwidth, Resolution: r.Variant.Resolution}
		old := prev[r.Variant.URI]
		var v verdict
		if r.Err != nil || r.Result == nil {
			msg := "no result"
			if r.Err != nil {
				msg = r.Err.Error()
			}
			v = verdict{metav1.ConditionFalse, streamingv1alpha1.ReasonFetchFailed, msg}
			line.LiveEdge, line.LastAdvanceTime = old.LiveEdge, old.LastAdvanceTime
		} else {
			var edge int64
			edge, line.LastAdvanceTime, v = judge(sp, ptr.Deref(old.LiveEdge, 0), old.LastAdvanceTime, r.Result, now)
			line.LiveEdge = new(edge)
		}
		line.Reason, line.Message = v.reason, v.message
		lines = append(lines, line)
		if v.status == metav1.ConditionTrue {
			live++
		}
		if worstAt < 0 || severity[v.reason] > severity[worst.reason] {
			worst, worstAt = v, i
		}
	}
	if worstAt < 0 {
		return lines, verdict{metav1.ConditionFalse, streamingv1alpha1.ReasonFetchFailed, "the multivariant playlist lists no variant"}
	}
	count := fmt.Sprintf("%d of %d variants live", live, len(results))
	if worst.status == metav1.ConditionTrue {
		worst.message = count
		return lines, worst
	}
	worst.message = fmt.Sprintf("variant %s: %s; %s", describe(lines[worstAt]), worst.message, count)
	return lines, worst
}

func describe(v streamingv1alpha1.VariantStatus) string {
	switch {
	case v.Resolution != "" && v.Bandwidth > 0:
		return fmt.Sprintf("%s (%s, %d kb/s)", v.URI, v.Resolution, v.Bandwidth/1000)
	case v.Bandwidth > 0:
		return fmt.Sprintf("%s (%d kb/s)", v.URI, v.Bandwidth/1000)
	}
	return v.URI
}

// staleAfter is the spec's value, or three target durations; with no target duration either,
// three intervals.
func staleAfter(sp *streamingv1alpha1.StreamProbe, targetDuration int) time.Duration {
	if sp.Spec.StaleAfter != nil && sp.Spec.StaleAfter.Duration > 0 {
		return sp.Spec.StaleAfter.Duration
	}
	if targetDuration > 0 {
		return 3 * time.Duration(targetDuration) * time.Second
	}
	return 3 * interval(sp)
}

// minInterval keeps a typo from turning the operator into a load test of someone's origin.
const minInterval = 5 * time.Second

func interval(sp *streamingv1alpha1.StreamProbe) time.Duration {
	d := sp.Spec.Interval.Duration
	if d == 0 {
		d = 30 * time.Second
	}
	return max(d, minInterval)
}

func timeout(sp *streamingv1alpha1.StreamProbe) time.Duration {
	if d := sp.Spec.Timeout.Duration; d > 0 {
		return d
	}
	return 10 * time.Second
}

func checkSegment(sp *streamingv1alpha1.StreamProbe) bool {
	return sp.Spec.CheckSegment == nil || *sp.Spec.CheckSegment
}
