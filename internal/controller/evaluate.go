// SPDX-License-Identifier: MIT

package controller

import (
	"errors"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
	st.MediaSequence = res.MediaSequence
	edge := res.MediaSequence + int64(res.Segments) - 1

	// The first good check counts as an advance: there is nothing to compare with yet. So does
	// an edge that went back, which is an encoder restart, not a stall.
	if prev.LastAdvanceTime == nil || edge != prev.LiveEdge {
		st.LastAdvanceTime = &t
	}
	st.LiveEdge = edge

	if res.Ended {
		return st, verdict{metav1.ConditionFalse, streamingv1alpha1.ReasonEnded,
			"the playlist has #EXT-X-ENDLIST: the stream is over, or the URL is VOD"}
	}

	stuck := now.Sub(st.LastAdvanceTime.Time)
	if limit := staleAfter(sp, res.TargetDuration); stuck > limit {
		return st, verdict{metav1.ConditionFalse, streamingv1alpha1.ReasonStale,
			fmt.Sprintf("the live edge has been at segment %d for %s (limit %s)", edge, stuck.Round(time.Second), limit)}
	}

	if res.SegmentURL != "" && (res.SegmentStatus < 200 || res.SegmentStatus > 299) {
		got := fmt.Sprintf("HTTP %d", res.SegmentStatus)
		if res.SegmentStatus == 0 {
			got = "no answer"
		}
		return st, verdict{metav1.ConditionFalse, streamingv1alpha1.ReasonSegmentFailed,
			fmt.Sprintf("the live-edge segment %s: %s", res.SegmentURL, got)}
	}

	return st, verdict{metav1.ConditionTrue, streamingv1alpha1.ReasonLive,
		fmt.Sprintf("live edge at segment %d, playlist in %dms", edge, st.PlaylistLatencyMilliseconds)}
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
