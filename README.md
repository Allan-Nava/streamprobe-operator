# streamprobe-operator

**A Kubernetes operator that watches live HLS streams.** Declare a stream as a `StreamProbe`;
the operator checks it on an interval and tells you, in the resource's status, in events and in
Prometheus metrics, whether it is live: the playlist answers, its live edge moves, and the
origin serves the newest segment.

**Documentation: <https://allan-nava.github.io/streamprobe-operator/>**, with a guide to how the
operator works, one Kubernetes idea at a time, and how each maps to Nomad, and
[Kubebuilder from zero](https://allan-nava.github.io/streamprobe-operator/kubebuilder.html): what
kubebuilder generated here, which files are yours and which are generated, and the everyday loop.

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

With the Prometheus Operator in the cluster, `--set prometheus.enabled=true` on the chart adds a
ServiceMonitor and a PrometheusRule with three alerts (rules in
`dist/chart/files/streamprobe.rules.yaml`, unit-tested with `promtool test rules`):

| Alert | When |
|---|---|
| `StreamNotLive` | a stream not Ready for 5 minutes |
| `StreamPlaylistSlow` | 95% of a stream's playlist fetches over 1 s, for 10 minutes |
| `StreamprobeDown` | Prometheus cannot scrape the operator for 5 minutes |

A Grafana dashboard comes with the chart (`dist/chart/files/streamprobe-dashboard.json`):
live and not live streams, a live/not-live timeline per stream, seconds since the live edge moved,
playlist fetch p50 and p95, segments per minute, checks by verdict, filtered by namespace and
stream. `--set grafana.dashboard.enabled=true` puts it in a ConfigMap labelled
`grafana_dashboard: "1"`, which Grafana's dashboard sidecar (kube-prometheus-stack) loads; or
import the JSON by hand. Its queries are parsed by promtool in CI.

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

In the cluster, with the published image (`ghcr.io/allan-nava/streamprobe-operator`, for
linux/amd64 and linux/arm64: `:main` follows the main branch, `:X.Y.Z` and `:latest` the
releases):

```sh
make deploy                  # IMG defaults to ghcr.io/allan-nava/streamprobe-operator:main
```

A release also attaches `install.yaml` (CRD, RBAC and Deployment, pinned to that release's
image), so a cluster needs no clone:

```sh
kubectl apply -f https://github.com/Allan-Nava/streamprobe-operator/releases/latest/download/install.yaml
```

With Helm, from the chart published with each release (an OCI artifact on GHCR, same version
as the image):

```sh
helm install streamprobe-operator oci://ghcr.io/allan-nava/charts/streamprobe-operator \
  --version <X.Y.Z> --namespace streamprobe-system --create-namespace
```

The chart is in `dist/chart/` (`make helm-deploy` installs it from the clone). With your own
build: `make docker-build docker-push deploy IMG=<registry>/streamprobe-operator:<tag>`.

`make undeploy` and `make uninstall` remove it.

## Development

```sh
make test       # unit tests and envtest (a real kube-apiserver and etcd, no cluster)
make lint       # golangci-lint
make manifests  # the CRD and RBAC, from the markers in api/ and internal/controller
make generate   # DeepCopy methods
make test-e2e   # end to end on a kind cluster: the operator against a fake live origin
```

Layout:

```
api/v1alpha1/                   the StreamProbe types and their kubebuilder markers
internal/hls/                   the playlist parser
internal/probe/                 one check over HTTP
internal/controller/            the reconcile loop, the evaluate rules, the metrics
config/                         kustomize: CRD, RBAC, manager, samples (CRD and RBAC generated)
dist/chart/                     the Helm chart, generated from config/ by kubebuilder's helm plugin
deploy/argocd/, deploy/probes/  Argo CD Applications for a k3s cluster, and the streams it watches
test/e2e/                       the kind suite; testdata/fake-hls.yaml is a fake live origin
docs/                           the GitHub Pages site (plain HTML, nava-ui from the CDN)
```

On a Raspberry Pi, with k3s and Argo CD syncing the operator and a list of streams from this
repository (`deploy/argocd/`, `deploy/probes/`): see the
[guide](https://allan-nava.github.io/streamprobe-operator/raspberry-pi.html).

Planned work is in [BACKLOG.md](BACKLOG.md) (the roadmap view is [ROADMAP.md](ROADMAP.md)): running on k3s with ArgoCD, alerts and
a dashboard, then probes of every variant, DASH, and probe Pods for heavier checks.

## License

[MIT](LICENSE) © Allan Nava
