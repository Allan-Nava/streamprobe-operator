/*
Copyright 2026 Allan Nava.

SPDX-License-Identifier: MIT
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Condition types and reasons of a StreamProbe.
const (
	// ConditionReady is True while the stream is live: the playlist answers, its media sequence
	// moves, and the live-edge segment is served.
	ConditionReady = "Ready"

	ReasonLive          = "Live"
	ReasonStale         = "Stale"
	ReasonEnded         = "Ended"
	ReasonFetchFailed   = "FetchFailed"
	ReasonSegmentFailed = "SegmentFailed"
	ReasonPending       = "Pending"
)

// StreamProbeSpec is the stream to watch and how often.
type StreamProbeSpec struct {
	// url is the HLS playlist of the stream: a media playlist, or a multivariant (master) one,
	// in which case the first variant is followed.
	// +kubebuilder:validation:Pattern=`^https?://`
	// +kubebuilder:validation:MaxLength=2048
	// +required
	URL string `json:"url"`

	// interval is how often the stream is checked. At least 5s.
	// +kubebuilder:default="30s"
	// +optional
	Interval metav1.Duration `json:"interval,omitzero"`

	// timeout bounds one check: the playlist fetch, the variant's, and the segment's.
	// +kubebuilder:default="10s"
	// +optional
	Timeout metav1.Duration `json:"timeout,omitzero"`

	// staleAfter is how long the live edge may stand still before the stream is Stale.
	// Unset, it is three target durations, read from the playlist.
	// +optional
	StaleAfter *metav1.Duration `json:"staleAfter,omitempty"`

	// checkSegment asks the origin for the live-edge segment on every check. A playlist can
	// keep moving while the segments it lists return errors.
	// +kubebuilder:default=true
	// +optional
	CheckSegment *bool `json:"checkSegment,omitempty"`
}

// StreamProbeStatus is what the last checks found.
type StreamProbeStatus struct {
	// observedGeneration is the spec generation the status was written for.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions: Ready, with reason Live, Stale, Ended, FetchFailed, SegmentFailed or Pending.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// lastCheckTime is when the stream was last checked.
	// +optional
	LastCheckTime *metav1.Time `json:"lastCheckTime,omitempty"`

	// lastAdvanceTime is when the live edge was last seen moving.
	// +optional
	LastAdvanceTime *metav1.Time `json:"lastAdvanceTime,omitempty"`

	// mediaSequence is #EXT-X-MEDIA-SEQUENCE at the last successful check.
	// +optional
	MediaSequence int64 `json:"mediaSequence,omitempty"`

	// liveEdge is the sequence number of the last segment listed: mediaSequence plus the
	// segments minus one. It is what "the stream moves" is measured on, because an EVENT
	// playlist keeps its media sequence and only appends segments.
	// +optional
	LiveEdge int64 `json:"liveEdge,omitempty"`

	// targetDurationSeconds is #EXT-X-TARGETDURATION at the last successful check.
	// +optional
	TargetDurationSeconds int32 `json:"targetDurationSeconds,omitempty"`

	// playlistURL is the media playlist read: the url, or its first variant.
	// +optional
	PlaylistURL string `json:"playlistURL,omitempty"`

	// playlistLatencyMilliseconds is the time to fetch the media playlist.
	// +optional
	PlaylistLatencyMilliseconds int64 `json:"playlistLatencyMilliseconds,omitempty"`

	// segmentStatus is the HTTP status of the live-edge segment; 0 when it was not checked or
	// the request failed.
	// +optional
	SegmentStatus int32 `json:"segmentStatus,omitempty"`

	// consecutiveFailures counts the checks in a row that could not read the playlist.
	// +optional
	ConsecutiveFailures int32 `json:"consecutiveFailures,omitempty"`

	// lastError is the error of the last failed check, empty after a good one.
	// +optional
	LastError string `json:"lastError,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=sp
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=`.spec.url`,priority=1
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`
// +kubebuilder:printcolumn:name="Edge",type=integer,JSONPath=`.status.liveEdge`
// +kubebuilder:printcolumn:name="Advanced",type=date,JSONPath=`.status.lastAdvanceTime`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// StreamProbe watches one live HLS stream: is its playlist moving, and does the origin serve
// its live edge.
type StreamProbe struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of StreamProbe
	// +required
	Spec StreamProbeSpec `json:"spec"`

	// status defines the observed state of StreamProbe
	// +optional
	Status StreamProbeStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// StreamProbeList contains a list of StreamProbe
type StreamProbeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []StreamProbe `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &StreamProbe{}, &StreamProbeList{})
		return nil
	})
}
