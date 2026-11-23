// SPDX-License-Identifier: MIT

package controller

import (
	"context"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	streamingv1alpha1 "github.com/Allan-Nava/streamprobe-operator/api/v1alpha1"
	"github.com/Allan-Nava/streamprobe-operator/internal/probe"
)

// EventRecorder is the part of the manager's event recorder the reconciler uses (the
// events.k8s.io API: regarding, related, type, reason, action, note).
type EventRecorder interface {
	Eventf(regarding runtime.Object, related runtime.Object, eventtype, reason, action, note string, args ...any)
}

// StreamProbeReconciler checks each StreamProbe's stream on its interval and writes what it
// found to the status, the Ready condition, events and metrics.
//
// It is level-based like any controller, with one twist: the work is periodic. A reconcile
// checks the stream only when the interval has passed since the last check (or the spec
// changed), and always asks to be called again when the next check is due. Status writes do
// not wake it up: the watch filters on generation, so only a spec change does.
type StreamProbeReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Prober   probe.Prober
	Recorder EventRecorder
	// Now is the clock; tests set it. Nil means time.Now.
	Now func() time.Time
}

// +kubebuilder:rbac:groups=streaming.allan-nava.github.io,resources=streamprobes,verbs=get;list;watch
// +kubebuilder:rbac:groups=streaming.allan-nava.github.io,resources=streamprobes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// Reconcile checks one stream if its check is due, and schedules the next one.
func (r *StreamProbeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var sp streamingv1alpha1.StreamProbe
	if err := r.Get(ctx, req.NamespacedName, &sp); err != nil {
		if apierrors.IsNotFound(err) {
			forgetMetrics(req.Namespace, req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	now := r.now()
	every := interval(&sp)
	if last := sp.Status.LastCheckTime; last != nil && sp.Status.ObservedGeneration == sp.Generation {
		if wait := last.Add(every).Sub(now); wait > 0 {
			return ctrl.Result{RequeueAfter: wait}, nil
		}
	}

	checkCtx, cancel := context.WithTimeout(ctx, timeout(&sp))
	res, checkErr := r.Prober.Check(checkCtx, sp.Spec.URL, checkSegment(&sp))
	cancel()
	if res == nil && checkErr == nil {
		checkErr = probe.ErrNoSegments
	}
	var good *probe.Result
	if checkErr == nil {
		good = res
	}

	prev := meta.FindStatusCondition(sp.Status.Conditions, streamingv1alpha1.ConditionReady)
	status, v := evaluate(&sp, good, checkErr, now)
	sp.Status = status
	meta.SetStatusCondition(&sp.Status.Conditions, metav1.Condition{
		Type:               streamingv1alpha1.ConditionReady,
		Status:             v.status,
		Reason:             v.reason,
		Message:            v.message,
		ObservedGeneration: sp.Generation,
	})
	if err := r.Status().Update(ctx, &sp); err != nil {
		// A conflict means someone changed the object since the Get: the next reconcile
		// reads it again. The check is not lost, only its write.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// An event only when the verdict changes, not on every check: a stream stuck for a day
	// would otherwise fill the namespace's events.
	if prev == nil || prev.Reason != v.reason {
		kind := "Normal"
		if v.status != metav1.ConditionTrue {
			kind = "Warning"
		}
		if r.Recorder != nil {
			r.Recorder.Eventf(&sp, nil, kind, v.reason, "Check", "%s", v.message)
		}
		log.Info("stream state changed", "reason", v.reason, "message", v.message)
	}

	r.record(&sp, good, v, now)
	return ctrl.Result{RequeueAfter: every}, nil
}

func (r *StreamProbeReconciler) record(sp *streamingv1alpha1.StreamProbe, res *probe.Result, v verdict, now time.Time) {
	ns, name := sp.Namespace, sp.Name
	ready := 0.0
	if v.status == metav1.ConditionTrue {
		ready = 1
	}
	readyGauge.WithLabelValues(ns, name).Set(ready)
	checksTotal.WithLabelValues(ns, name, v.reason).Inc()
	if res == nil {
		return
	}
	liveEdgeGauge.WithLabelValues(ns, name).Set(float64(sp.Status.LiveEdge))
	if sp.Status.LastAdvanceTime != nil {
		sinceAdvanceGauge.WithLabelValues(ns, name).Set(now.Sub(sp.Status.LastAdvanceTime.Time).Seconds())
	}
	playlistSeconds.WithLabelValues(ns, name).Observe(res.PlaylistLatency.Seconds())
}

func (r *StreamProbeReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// SetupWithManager sets up the controller with the Manager. The generation predicate keeps the
// controller's own status writes from waking it up again.
func (r *StreamProbeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&streamingv1alpha1.StreamProbe{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("streamprobe").
		Complete(r)
}
