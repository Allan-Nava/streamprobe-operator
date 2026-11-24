// SPDX-License-Identifier: MIT

package controller

import (
	streamingv1alpha1 "github.com/Allan-Nava/streamprobe-operator/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// The probe's metrics, on the manager's /metrics endpoint next to controller-runtime's own.
// One series per StreamProbe (namespace, name); they are dropped when the probe is deleted, so
// a removed stream does not stay on a dashboard as a flat line. No finalizer is needed for
// that: the series live in this process, and the reconcile that finds the object gone forgets
// them.
const (
	labelNamespace = "namespace"
	labelName      = "name"
)

var (
	probeLabels = []string{labelNamespace, labelName}

	readyGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "streamprobe_ready",
		Help: "1 when the stream is live (condition Ready=True), 0 otherwise.",
	}, probeLabels)

	liveEdgeGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "streamprobe_live_edge",
		Help: "Sequence number of the last segment in the playlist.",
	}, probeLabels)

	sinceAdvanceGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "streamprobe_seconds_since_advance",
		Help: "Seconds since the live edge last moved, at the last check.",
	}, probeLabels)

	playlistSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "streamprobe_playlist_fetch_seconds",
		Help:    "Time to fetch the media playlist.",
		Buckets: []float64{.025, .05, .1, .25, .5, 1, 2.5, 5, 10},
	}, probeLabels)

	variantReadyGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "streamprobe_variant_ready",
		Help: "1 when the variant is live, 0 otherwise; only for StreamProbes with spec.variants: All.",
	}, []string{labelNamespace, labelName, "variant"})

	checksTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "streamprobe_checks_total",
		Help: "Checks made, by the reason of the Ready condition they led to.",
	}, []string{labelNamespace, labelName, "reason"})
)

func init() {
	metrics.Registry.MustRegister(readyGauge, liveEdgeGauge, sinceAdvanceGauge, playlistSeconds, checksTotal, variantReadyGauge)
}

func forgetMetrics(namespace, name string) {
	l := prometheus.Labels{labelNamespace: namespace, labelName: name}
	readyGauge.Delete(l)
	liveEdgeGauge.Delete(l)
	sinceAdvanceGauge.Delete(l)
	playlistSeconds.Delete(l)
	checksTotal.DeletePartialMatch(l)
	variantReadyGauge.DeletePartialMatch(l)
}

// recordVariants rewrites the probe's variant series from status.variants: a variant that left
// the playlist, or a probe switched back to First, leaves no series behind.
func recordVariants(sp *streamingv1alpha1.StreamProbe) {
	variantReadyGauge.DeletePartialMatch(prometheus.Labels{labelNamespace: sp.Namespace, labelName: sp.Name})
	for _, v := range sp.Status.Variants {
		ready := 0.0
		if v.Reason == streamingv1alpha1.ReasonLive {
			ready = 1
		}
		variantReadyGauge.WithLabelValues(sp.Namespace, sp.Name, v.URI).Set(ready)
	}
}
