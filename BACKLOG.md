# Backlog — streamprobe-operator

Single source of truth for planned work. Items keep a stable `SP-n` id; ROADMAP.md is
generated from this file (`npx backlogsync roadmap`) and the GitHub issues and milestones
follow it on push to main.

```
## v0.2.0 — Title <!-- ms: phase=next -->

- [ ] **SP-99 — Short name**: what it is, why, what it touches. <!-- sp: prio=high size=M labels=controller -->
```

## v0.1.0 — A probe that reconciles <!-- ms: phase=shipped -->

The smallest operator that is useful: one custom resource per stream, checked on an interval
by the controller itself, the result in the status, events and metrics.

- [x] **SP-1 — The StreamProbe API**: `streaming.allan-nava.github.io/v1alpha1`, spec `url`,
  `interval`, `timeout`, `staleAfter`, `checkSegment`, with CRD defaults and validation (http(s)
  only); status with the Ready condition, the live edge, latencies and failures; printer columns
  and the short name `sp`. <!-- sp: prio=high size=M labels=api ver=main -->
- [x] **SP-2 — The HLS check**: a minimal playlist parser (target duration, media sequence,
  segments, end list, variants) and a prober that follows the first variant and asks the origin
  for the live-edge segment, reading only its first bytes. <!-- sp: prio=high size=M labels=probe ver=main -->
- [x] **SP-3 — The reconcile loop**: periodic work in a level-based controller: check when the
  interval has passed or the spec changed, requeue for the next check, ignore its own status
  writes (generation predicate). Ready is Live, Stale, Ended, FetchFailed or SegmentFailed, from a
  pure `evaluate` function; "moving" is measured on the live edge, so EVENT playlists count. One
  event per change of verdict, not per check. <!-- sp: prio=high size=M labels=controller ver=main -->
- [x] **SP-4 — Metrics**: `streamprobe_ready`, `streamprobe_live_edge`,
  `streamprobe_seconds_since_advance`, `streamprobe_playlist_fetch_seconds`,
  `streamprobe_checks_total`, dropped when a probe is deleted. <!-- sp: prio=med size=S labels=observability ver=main -->
- [x] **SP-5 — Tests**: table tests of every rule of `evaluate`, the parser and the prober
  against `httptest`, and an envtest suite (a real kube-apiserver and etcd, no cluster) for the
  CRD defaults and validation and the reconcile loop with a fake prober and clock. <!-- sp: prio=high size=M labels=ci ver=main -->

## v0.2.0 — On a real cluster <!-- ms: phase=next -->

From envtest to a cluster: the image, the install, and a test that runs the operator for real.

- [x] **SP-6 — End-to-end on kind**: the kubebuilder e2e suite green in CI on a kind cluster,
  plus a test that applies a StreamProbe against a stream served inside the cluster and waits
  for Ready=True, then stops the stream and waits for Stale. Done: a fake live origin
  (`test/e2e/testdata/fake-hls.yaml`, stdlib Python in a ConfigMap, 1-second segments, `GET
  /stop` freezes it); the test waits for Live, freezes it, waits for Stale, and checks the two
  events and the probe's series on the manager's metrics endpoint. <!-- sp: prio=high size=M labels=ci ver=main -->
- [x] **SP-7 — Image and releases**: multi-arch image (amd64, arm64) on GHCR from a tag, the
  CRD and the install manifest (`make build-installer`) attached to the GitHub release. Done in
  `image.yml`: `:main` and `:sha-<commit>` on every push to main, `:X.Y.Z`, `:X.Y`, `:latest` and
  a release with `install.yaml` on a tag; the builder cross-compiles instead of running under
  QEMU. <!-- sp: prio=high size=M labels=deploy,ci ver=main -->
- [x] **SP-8 — Helm chart**: a chart for the operator (CRD, RBAC, Deployment, metrics Service),
  linted and installed on kind in CI, published as an OCI artifact on GHCR. Done: `dist/chart/`
  generated from `config/` by kubebuilder's helm plugin; `test-chart.yml` lints it, installs it on
  kind and waits for a StreamProbe on the fake origin to be Live; a tag pushes it to
  `oci://ghcr.io/allan-nava/charts/streamprobe-operator` with the release's version, and the empty
  image tag makes it install the image of that version. <!-- sp: prio=med size=M labels=deploy ver=main -->
- [ ] **SP-9 — Running on the Raspberry Pi**: k3s on the Pi 4, the operator installed by
  ArgoCD from this repository, a few public live streams probed around the clock; the steps in
  `docs/`. Ready in the repository, not yet run on the Pi: `deploy/argocd/` (the operator from
  `dist/chart`, the streams from `deploy/probes/`), three test streams, and the guide
  `docs/raspberry-pi.html` (k3s without Traefik next to Docker, Argo CD core, metrics). Done
  when the probes are Live on the Pi. <!-- sp: prio=med size=M labels=deploy,docs -->

## v0.3.0 — Seen from Grafana <!-- ms: phase=later -->

- [x] **SP-10 — ServiceMonitor and alerts**: a ServiceMonitor for the Prometheus Operator and a
  PrometheusRule: a stream not Ready for N minutes, a playlist slower than its target duration.
  Done: the chart's ServiceMonitor (kubebuilder's) plus a PrometheusRule from
  `dist/chart/files/streamprobe.rules.yaml`: StreamNotLive (5m), StreamPlaylistSlow (p95 over
  1 s for 10m; a fixed second, since the target duration is not a metric), StreamprobeDown.
  `promtool test rules` in CI, and the chart installed with them on kind against the Prometheus
  Operator's CRDs. <!-- sp: prio=med size=S labels=observability ver=main -->
- [x] **SP-11 — Dashboard**: a Grafana dashboard as JSON in the repository (and the chart):
  live/not live per stream, the live edge over time, playlist latency percentiles. Done:
  `dist/chart/files/streamprobe-dashboard.json`, nine panels with namespace and stream filters,
  a ConfigMap for Grafana's sidecar behind `grafana.dashboard.enabled`; CI parses every query
  with promtool (`test/grafana/dashboard_queries.py`) and installs the ConfigMap on kind. Not yet
  looked at in a real Grafana. <!-- sp: prio=low size=S labels=observability ver=main -->

## v0.4.0 — Probes that know more <!-- ms: phase=later -->

- [x] **SP-12 — Every variant**: check all the variants of a multivariant playlist, or the ones
  chosen by bandwidth or name, each with its own status line; a variant that stalls while the
  others move is the classic encoder fault. Done for all of them: `spec.variants: All` (default
  `First`), a line per variant in `status.variants` with bandwidth and resolution, Ready as the
  worst variant naming it, `streamprobe_variant_ready`; checked on the Unified Streaming demo
  (two variants, both live). Choosing some variants is SP-18. <!-- sp: prio=med size=M labels=probe,api ver=main -->
- [x] **SP-13 — Headers from a Secret**: `spec.headersFrom` (a Secret's keys as request
  headers) for origins behind a token, never the token in the spec. Done: `headersFrom.secretName`
  in the probe's namespace, read uncached with `mgr.GetAPIReader()` so RBAC is only `get` on
  Secrets (a cached read needs list and watch on all of them) and a rotated token is used at
  the next check; headers go to the URL's host only unless `allHosts`; a missing or invalid
  Secret (bad key, line break in a value) is `InvalidConfig` and the stream is not contacted;
  values never in the status, events or logs. <!-- sp: prio=med size=S labels=api,probe ver=main -->
- [ ] **SP-14 — CEL validation**: `x-kubernetes-validations` on the spec (interval at least 5s,
  timeout shorter than interval, staleAfter longer than interval), so a bad value is refused by
  the API server instead of corrected in code. <!-- sp: prio=low size=S labels=api -->
- [ ] **SP-18 — Choose the variants**: with `variants: All`, a filter by bandwidth range or by URI
  pattern, so a stream with twelve renditions can watch the three that matter. <!-- sp: prio=low size=S labels=probe,api -->
- [ ] **SP-15 — DASH**: the same checks on an MPD: `availabilityStartTime`, the segment
  timeline's last entry as the live edge. <!-- sp: prio=low size=L labels=probe -->

## v0.5.0 — Probes as workloads <!-- ms: phase=later -->

- [ ] **SP-16 — A probe Pod per stream**: for checks that need more than an HTTP GET (low-latency
  HLS with latencyprobe, segment contents with segcheck), the controller owns a Deployment per
  StreamProbe (owner references, garbage collection, rollout on spec change) and reads its
  results back. <!-- sp: prio=low size=L labels=controller -->
- [ ] **SP-17 — Write-up**: a post on allan-nava.github.io: how a periodic check fits a
  level-based reconcile loop, and what Nomad did differently. <!-- sp: prio=low size=S labels=docs -->
