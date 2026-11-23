// SPDX-License-Identifier: MIT

package controller

import (
	"context"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	streamingv1alpha1 "github.com/Allan-Nava/streamprobe-operator/api/v1alpha1"
	"github.com/Allan-Nava/streamprobe-operator/internal/probe"
)

// fakeProber returns the result it holds, and counts the checks.
type fakeProber struct {
	mu     sync.Mutex
	result *probe.Result
	err    error
	calls  int
}

func (f *fakeProber) Check(_ context.Context, _ string, _ bool) (*probe.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.result == nil {
		return nil, f.err
	}
	r := *f.result
	return &r, f.err
}

type event struct{ kind, reason string }

type fakeRecorder struct{ events []event }

func (f *fakeRecorder) Eventf(_ runtime.Object, _ runtime.Object, kind, reason, _, _ string, _ ...any) {
	f.events = append(f.events, event{kind, reason})
}

const (
	testNamespace = "default"
	testURL       = "https://origin.test/live.m3u8"
)

var _ = Describe("StreamProbe controller", func() {
	const name = "live-test"
	key := types.NamespacedName{Name: name, Namespace: testNamespace}

	var (
		prober   *fakeProber
		recorder *fakeRecorder
		clock    time.Time
		r        *StreamProbeReconciler
	)

	reconcile := func() ctrl.Result {
		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		return res
	}
	get := func() *streamingv1alpha1.StreamProbe {
		var sp streamingv1alpha1.StreamProbe
		Expect(k8sClient.Get(ctx, key, &sp)).To(Succeed())
		return &sp
	}
	ready := func() *metav1.Condition {
		return meta.FindStatusCondition(get().Status.Conditions, streamingv1alpha1.ConditionReady)
	}

	BeforeEach(func() {
		prober = &fakeProber{result: &probe.Result{PlaylistURL: testURL, TargetDuration: 6,
			MediaSequence: 100, Segments: 3, SegmentURL: "https://origin.test/seg102.ts", SegmentStatus: 200}}
		recorder = &fakeRecorder{}
		clock = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
		r = &StreamProbeReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Prober: prober, Recorder: recorder,
			Now: func() time.Time { return clock }}

		Expect(k8sClient.Create(ctx, &streamingv1alpha1.StreamProbe{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
			Spec:       streamingv1alpha1.StreamProbeSpec{URL: testURL},
		})).To(Succeed())
	})

	AfterEach(func() {
		sp := &streamingv1alpha1.StreamProbe{}
		if err := k8sClient.Get(ctx, key, sp); err == nil {
			Expect(k8sClient.Delete(ctx, sp)).To(Succeed())
		}
	})

	It("applies the CRD defaults", func() {
		sp := get()
		Expect(sp.Spec.Interval.Duration).To(Equal(30 * time.Second))
		Expect(sp.Spec.Timeout.Duration).To(Equal(10 * time.Second))
		Expect(sp.Spec.CheckSegment).NotTo(BeNil())
		Expect(*sp.Spec.CheckSegment).To(BeTrue())
	})

	It("rejects a URL that is not http(s)", func() {
		err := k8sClient.Create(ctx, &streamingv1alpha1.StreamProbe{
			ObjectMeta: metav1.ObjectMeta{Name: "bad-url", Namespace: testNamespace},
			Spec:       streamingv1alpha1.StreamProbeSpec{URL: "file:///etc/passwd"},
		})
		Expect(errors.IsInvalid(err)).To(BeTrue(), "got %v", err)
	})

	It("marks a moving stream Live, then Stale when it stops, with one event per change", func() {
		res := reconcile()
		Expect(res.RequeueAfter).To(Equal(30 * time.Second))
		Expect(ready().Status).To(Equal(metav1.ConditionTrue))
		Expect(ready().Reason).To(Equal(streamingv1alpha1.ReasonLive))
		Expect(get().Status.LiveEdge).To(BeEquivalentTo(102))

		// Before the interval: no check, a requeue for the rest of it.
		clock = clock.Add(10 * time.Second)
		res = reconcile()
		Expect(prober.calls).To(Equal(1))
		Expect(res.RequeueAfter).To(Equal(20 * time.Second))

		// The interval passed and the edge moved: still Live, no new event.
		clock = clock.Add(20 * time.Second)
		prober.result.MediaSequence = 105
		reconcile()
		Expect(prober.calls).To(Equal(2))
		Expect(ready().Reason).To(Equal(streamingv1alpha1.ReasonLive))

		// The edge stands still for longer than 3 × 6 s: Stale.
		clock = clock.Add(30 * time.Second)
		reconcile()
		Expect(ready().Status).To(Equal(metav1.ConditionFalse))
		Expect(ready().Reason).To(Equal(streamingv1alpha1.ReasonStale))

		Expect(recorder.events).To(Equal([]event{
			{"Normal", streamingv1alpha1.ReasonLive},
			{"Warning", streamingv1alpha1.ReasonStale},
		}))
		Expect(testutil.ToFloat64(readyGauge.WithLabelValues(testNamespace, name))).To(BeEquivalentTo(0))
	})

	It("counts the failures in a row and clears them on a good check", func() {
		prober.result = nil
		prober.err = &probe.FetchError{URL: testURL, Err: probe.ErrNoSegments}
		reconcile()
		clock = clock.Add(30 * time.Second)
		reconcile()
		sp := get()
		Expect(sp.Status.ConsecutiveFailures).To(BeEquivalentTo(2))
		Expect(ready().Reason).To(Equal(streamingv1alpha1.ReasonFetchFailed))

		prober.result = &probe.Result{TargetDuration: 6, MediaSequence: 1, Segments: 2, SegmentURL: "s", SegmentStatus: 200}
		prober.err = nil
		clock = clock.Add(30 * time.Second)
		reconcile()
		Expect(get().Status.ConsecutiveFailures).To(BeZero())
		Expect(ready().Reason).To(Equal(streamingv1alpha1.ReasonLive))
	})

	It("checks again at once when the spec changes", func() {
		reconcile()
		sp := get()
		sp.Spec.URL = "https://origin.test/other.m3u8"
		Expect(k8sClient.Update(ctx, sp)).To(Succeed())
		clock = clock.Add(time.Second)
		reconcile()
		Expect(prober.calls).To(Equal(2))
		Expect(get().Status.ObservedGeneration).To(Equal(get().Generation))
	})

	It("forgets the metrics of a deleted probe", func() {
		reconcile()
		before := testutil.CollectAndCount(readyGauge)
		Expect(before).To(BeNumerically(">=", 1))
		Expect(k8sClient.Delete(ctx, get())).To(Succeed())
		reconcile()
		Expect(testutil.CollectAndCount(readyGauge)).To(Equal(before - 1))
	})
})
