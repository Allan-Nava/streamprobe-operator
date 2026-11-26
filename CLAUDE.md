# CLAUDE.md

Instructions for Claude Code (and other AI agents) working in this repository.

## What this is

streamprobe-operator: a Kubernetes operator, written with kubebuilder v4 and controller-runtime,
that watches live HLS streams declared as `StreamProbe` resources
(`streaming.allan-nava.github.io/v1alpha1`). Go, MIT, public. The README explains the design;
read it before changing the reconcile loop.

It is also the owner's project for learning Kubernetes from the inside: prefer the idiomatic
controller-runtime way, and explain the Kubernetes reason for a choice in a comment.

## Language

Everything in this repository is in English: code, comments, docs, commit messages, issues,
milestones.

## Layout

```
api/v1alpha1/streamprobe_types.go    the types; validation, defaults and printer columns are markers
api/v1alpha1/zz_generated.*          GENERATED (make generate), never edited
internal/hls/                        playlist parser, no dependencies
internal/probe/                      one check over HTTP; knows nothing about Kubernetes
internal/controller/evaluate.go      the rules: previous status + check + time -> status, verdict
internal/controller/streamprobe_controller.go   Reconcile: when to check, write, event, metrics
internal/controller/metrics.go       the Prometheus series
config/crd/bases/, config/rbac/role.yaml   GENERATED (make manifests), never edited
config/samples/                      example resources (edited by hand)
dist/chart/                          the Helm chart, generated from config/ by `kubebuilder edit
                                     --plugins=helm/v2-alpha`; Chart.yaml metadata and the empty
                                     image tag in values.yaml are ours: keep them on a regeneration
test/e2e/                            kind end-to-end suite (make test-e2e); testdata/fake-hls.yaml is
                                     the fake live origin it probes (Live, then Stale after GET /stop)
docs/                                the GitHub Pages site: index (overview), how-it-works (the guide),
                                     reference, raspberry-pi, kubebuilder (the tool from zero: generated
                                     vs. written files, markers, make targets, recipes); plain HTML
                                     with nava-ui from the CDN at a fixed tag
                                     (nava-ui-update.yml proposes each new release); pages.yml deploys it
deploy/argocd/                       Argo CD Applications: the operator from dist/chart, the streams
                                     from deploy/probes/ (one StreamProbe per file, listed in its
                                     kustomization); the Raspberry Pi guide is docs/raspberry-pi.html
BACKLOG.md                           planned work, source of truth; ROADMAP.md generated from it
```

## Rules

- **Never edit generated files.** Change the markers in `api/` or the `+kubebuilder:rbac`
  markers in the controller, then `make manifests generate`, and commit sources and output
  together. A new permission the controller needs is a new rbac marker, not a hand-written
  Role.
- **The decision stays pure.** Every rule about what a check means lives in `evaluate`, with a
  row in its table test. The reconciler only decides when to check and writes the result.
- **Status writes must not wake the controller.** The watch filters on generation
  (`GenerationChangedPredicate`); keep it, or every status update triggers a check.
- **Events on change only**, never one per check.
- **Every metric series is per probe and is forgotten on delete** (`forgetMetrics`). A new
  metric goes in `metrics.go`, in `forgetMetrics`, and in the README's metrics table.
- **Tests**: `make test` runs unit tests and envtest (downloads kube-apiserver and etcd into
  `bin/`; no Docker, no cluster). A change to the reconcile loop needs an envtest case; a new
  rule needs an `evaluate` table row. `make lint` must pass.
- **Keep the docs true.** A changed fact is changed everywhere it is stated: README, this file,
  BACKLOG.md, and the pages in `docs/` (a new spec or status field, reason or metric goes in
  `docs/reference.html`; a change to the reconcile loop in `docs/how-it-works.html`).
- **Alerts live in `dist/chart/files/streamprobe.rules.yaml`**, a plain rules file (no Helm
  templating, so promtool can read it); a changed alert changes `test/prometheus/rules_test.yaml`,
  the README and `docs/reference.html` too. A chart regeneration must keep `files/`, the
  `prometheus/streamprobe-rules.yaml` template and `prometheus.rules` in values.
- **The dashboard is `dist/chart/files/streamprobe-dashboard.json`**, plain JSON: a new metric
  that matters gets a panel, and every query must survive `test/grafana/dashboard_queries.py`
  plus `promtool check rules` (Grafana variables are substituted there; add a new one to its
  table).
- **The chart follows `config/`.** After a change to the CRD, RBAC or manager manifests, run
  `kubebuilder edit --plugins=helm/v2-alpha` again, restore our `Chart.yaml` fields and the
  empty `manager.image.tag` (a released chart must install the image of its own version), and
  `helm lint dist/chart`. `test-chart.yml` installs it on kind and waits for a probe to be Live.
- **Planned work goes in `BACKLOG.md`** (ids `SP-n`, never reused). `ROADMAP.md` is generated by
  `npx backlogsync roadmap` and never edited by hand; GitHub issues and milestones follow the
  backlog on push to `main`, one way. A finished item is ticked and gets `ver=main` (or the
  version) in the same commit as the work.

## Commands

```bash
make test                   # unit + envtest
make lint                   # golangci-lint
make manifests generate     # CRD, RBAC, DeepCopy from the markers
make install run            # CRD into the current cluster, controller on this machine
make test-e2e               # on kind (needs Docker)
make deploy                 # into the current cluster, image ghcr.io/allan-nava/streamprobe-operator:main
npx backlogsync check       # BACKLOG.md valid, ROADMAP.md current
npx backlogsync roadmap     # regenerate ROADMAP.md
```

## Git

- Commits are authored as the repository owner, with no co-author trailer and no AI attribution
  in commits, pull requests or issues.
- Work on `main`; small, complete commits.
