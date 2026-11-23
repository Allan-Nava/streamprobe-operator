# streamprobe-operator

**A Kubernetes operator that watches live HLS streams.** Declare a stream as a `StreamProbe`;
the operator checks it on an interval and tells you, in the resource's status, in events and in
Prometheus metrics, whether it is live: the playlist answers, its live edge moves, and the
origin serves the newest segment.

[![Tests](https://github.com/Allan-Nava/streamprobe-operator/actions/workflows/test.yml/badge.svg)](https://github.com/Allan-Nava/streamprobe-operator/actions/workflows/test.yml)
[![Lint](https://github.com/Allan-Nava/streamprobe-operator/actions/workflows/lint.yml/badge.svg)](https://github.com/Allan-Nava/streamprobe-operator/actions/workflows/lint.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-10b981)](LICENSE)

```console
$ kubectl get streamprobes
NAME                READY   REASON   EDGE        ADVANCED   AGE
unified-live-demo   True    Live     935119251   2s         3h
channel-2           False   Stale    918233      6m         3h
```

## Why

A live stream fails in ways an HTTP health check does not see. The origin answers 200, the
playlist is valid, and the stream is still dead: the encoder stopped and the playlist stopped
moving, or the playlist moves but the segments it lists return 404. streamprobe checks what a
player depends on, and keeps the answer where the rest of the platform already looks: the
Kubernetes API and Prometheus.

It is also a learning project: an operator written from scratch with
[kubebuilder](https://book.kubebuilder.io/) and controller-runtime, to learn Kubernetes from the
inside, after years of running live video on Nomad.

## How a check works

```
StreamProbe ──► reconcile (every spec.interval)
                  │
                  ├─ GET url ──► multivariant? ──► GET first variant
                  │                 read: target duration, media sequence, segments, end list
                  ├─ GET the live-edge segment (first 1 KB)
                  │
                  └─ evaluate ──► status, Ready condition, event on change, metrics
```

The verdict, the `Ready` condition's reason, is decided in this order:

| Reason | Ready | When |
|---|---|---|
| `FetchFailed` | False | the playlist could not be fetched or read, or lists no segment |
| `Ended` | False | the playlist has `#EXT-X-ENDLIST`: the stream is over, or the URL is VOD |
| `Stale` | False | the live edge has not moved for longer than `staleAfter` (default: three target durations) |
| `SegmentFailed` | False | the live-edge segment did not answer 2xx |
| `Live` | True | otherwise |

"Moving" is measured on the **live edge**, the sequence number of the last segment
(media sequence + segments − 1), not on `#EXT-X-MEDIA-SEQUENCE` alone: an EVENT playlist keeps
its media sequence and only appends segments. An edge that goes back is an encoder restart and
counts as movement.

## The resource

```yaml
apiVersion: streaming.allan-nava.github.io/v1alpha1
kind: StreamProbe
metadata:
  name: unified-live-demo
spec:
  url: https://demo.unified-streaming.com/k8s/live/stable/live.isml/.m3u8
  interval: 30s        # default 30s, never less than 5s
  timeout: 10s         # default 10s, for the whole check
  staleAfter: 1m       # optional; default three target durations
  checkSegment: true   # default true
```

The status keeps `liveEdge`, `mediaSequence`, `targetDurationSeconds`, `lastAdvanceTime`,
`lastCheckTime`, `playlistURL` (the variant actually read), `playlistLatencyMilliseconds`,
`segmentStatus`, `consecutiveFailures` and `lastError`.

## Metrics

On the manager's metrics endpoint, labelled by `namespace` and `name`:

| Metric | Type | |
|---|---|---|
| `streamprobe_ready` | gauge | 1 when Ready=True |
| `streamprobe_live_edge` | gauge | sequence number of the last segment |
| `streamprobe_seconds_since_advance` | gauge | seconds since the live edge last moved |
| `streamprobe_playlist_fetch_seconds` | histogram | time to fetch the media playlist |
| `streamprobe_checks_total` | counter | checks, by reason (also labelled `reason`) |

The series of a deleted probe are dropped.

## Design notes

- **Periodic work in a level-based controller.** A reconcile checks the stream only when the
  interval has passed since `status.lastCheckTime`, or the spec changed (`observedGeneration`),
  and returns `RequeueAfter` for the next check. The watch uses `GenerationChangedPredicate`,
  so the controller's own status writes do not wake it up again.
- **A pure decision.** `evaluate` takes the previous status, the check and the time, and
  returns the next status and the verdict; every rule has a table test without a cluster.
- **One event per change, not per check.** A stream stuck for a day would otherwise fill the
  namespace's events.
- **No finalizer.** The only external state is the metric series, which live in the operator's
  process; the reconcile that finds the object gone drops them.
- **A floor on the interval.** Five seconds: a typo must not turn the operator into a load test
  of someone's origin.

## Run it

You need Go 1.26+, `kubectl` and a cluster (kind, k3s, or any other).

```sh
make install                 # the CRD, into the cluster of the current context
make run                     # the controller, on your machine, against that cluster
kubectl apply -k config/samples
kubectl get sp -w
```

In the cluster, with an image you built and pushed:

```sh
make docker-build docker-push IMG=<registry>/streamprobe-operator:<tag>
make deploy IMG=<registry>/streamprobe-operator:<tag>
```

`make undeploy` and `make uninstall` remove it.

## Development

```sh
make test       # unit tests and envtest (a real kube-apiserver and etcd, no cluster)
make lint       # golangci-lint
make manifests  # the CRD and RBAC, from the markers in api/ and internal/controller
make generate   # DeepCopy methods
make test-e2e   # end to end on a kind cluster
```

Layout:

```
api/v1alpha1/                   the StreamProbe types and their kubebuilder markers
internal/hls/                   the playlist parser
internal/probe/                 one check over HTTP
internal/controller/            the reconcile loop, the evaluate rules, the metrics
config/                         kustomize: CRD, RBAC, manager, samples (CRD and RBAC generated)
test/e2e/                       the kind suite
```

Planned work is in [BACKLOG.md](BACKLOG.md) (the roadmap view is [ROADMAP.md](ROADMAP.md)):
the image and a Helm chart, an end-to-end test on kind, running on k3s with ArgoCD, alerts and
a dashboard, then probes of every variant, DASH, and probe Pods for heavier checks.

## License

[MIT](LICENSE) © Allan Nava
