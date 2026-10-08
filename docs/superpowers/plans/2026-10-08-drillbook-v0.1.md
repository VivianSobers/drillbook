# drillbook v0.1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship drillbook v0.1: a Go CLI that runs fire drills (fault, alert fires, runbook steps, alert clears, control run) against a kubeadm drill cluster built by Terraform and Ansible, with five working drills and CI.

**Architecture:** One Go binary (`cmd/drillbook`) with small internal packages behind interfaces (`Prom`, `Alertmanager`, `FaultExecutor`, `BlockRunner`, `Clock`), so the drill engine and the verdict logic are tested against fakes. Host faults and host runbook steps run through generated Ansible playbooks over SSH; Kubernetes faults use client-go. The drill environment is two kind-style systemd containers built by Terraform's Docker provider and turned into a real kubeadm cluster by Ansible.

**Tech Stack:** Go 1.27 (module `github.com/VivianSobers/drillbook`), cobra, client-go, prometheus/client_golang API client, goldmark, sigs.k8s.io/yaml; ansible-core 2.21; Terraform 1.16 with kreuzwerker/docker 4.x and hashicorp/tls; kindest/node v1.36.4; kube-prometheus-stack 92.1.1; GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-10-08-drillbook-design.md`

## Global Constraints

- Laptop only: 14 GB RAM, Docker without sudo, no libvirt, no sudo. The lab GPU workers are not used (no GPU work in this project).
- Drill environment: one control plane plus one worker, kindest/node v1.36.4 pinned by digest, kube-prometheus-stack 92.1.1 with Grafana disabled.
- Prometheus at `http://172.31.250.10:30090`, Alertmanager at `http://172.31.250.10:30093` (NodePorts on the control plane).
- Commits follow `~/Documents/StaleHand/CONTRIBUTING.md`: subject only, `<type>: <4-6 words>`, no trailers, one logical change per commit, tests pass at every commit.
- Never run `docker volume prune` or `docker system prune`; remove only resources named `drillbook-*`.
- Every host fault arms a `systemd-run --on-active` revert on the host before it applies.
- No number goes into docs that did not come from a run.

## Decisions taken without the user (recorded in the spec's "v0.1 decisions" section)

1. Drill nodes are privileged systemd containers (kindest/node plus sshd), not libvirt VMs, because there is no sudo. Ansible still provisions them over SSH and kubeadm still builds the cluster. Nodes share the host kernel and clock, so `skew_clock` is deferred.
2. Results and state are JSON files under `.drillbook/`, not SQLite: no cgo and enough for v0.1.
3. Kubernetes faults (`scale`, `patch` on a Deployment) join Ansible faults, because three of the five drills are bad deploys.
4. Alert routing comes from Alertmanager's own `/api/v2/alerts` response (`receivers`), not from re-implementing the routing tree.
5. Role tests are Ansible test playbooks run against a throwaway node container, not Molecule.
6. Verdict precedence when several apply: `aborted` > `alert-did-not-fire` > `misrouted` > `step-failed` > `not-resolved` > `slower-than-runbook` > `inconclusive` > `pass`. All findings are still recorded.

## Review Focus

- A runbook with zero tagged blocks, or a block with malformed attributes: lint must report the file and line, `run` must abort before injecting.
- The alert is already firing for the target before the drill: preflight must abort without injecting a fault.
- The runner is interrupted (Ctrl-C) mid-drill: cleanup must still revert the fault, delete the silence and record `aborted`.
- `revert_after` shorter than `fire_within + resolve_within + 10m`: lint must reject it, because the host timer would revert the fault and fake a pass or an inconclusive control.
- Alertmanager has the alert but silenced: the routing check must still see receivers (query with `silenced=true`).

---

### Task 1: Scaffold, config and drill loading

**Files:** `go.mod`, `cmd/drillbook/main.go`, `internal/config/config.go`, `internal/drill/drill.go`, tests beside each, `.gitignore`, `Makefile`.

**Produces:**
- `config.Load(path string) (*config.Config, error)`; `Config{Prometheus, Alertmanager, Kubeconfig, Inventory, AnsiblePlaybook, CollectionsPath, StateDir string; PollInterval Duration; Allow Allowlist{Hosts, Namespaces []string}; SilenceCreator string}`.
- `drill.Load(path string) (*drill.Drill, error)`, `drill.LoadDir(dir string) ([]*drill.Drill, error)`, `(*Drill).Validate(cfg *config.Config) []error`.
- `Drill{Name, File, Alert, Runbook string; Target Target; Fault Fault; FireWithin, ResolveWithin, ExpectedResolve Duration; Expect Expect; Schedule string}`; `Target{Host string; Labels, Env map[string]string}`; `Fault{Ansible *AnsibleFault; Kube *KubeFault; RevertAfter Duration}`; `AnsibleFault{Role string; Vars map[string]any}`; `KubeFault{Namespace, Deployment string; Scale *int32; Patch *Patch}`; `Patch{Type string; Body any}`; `Expect{Receiver string}`.

Tests (write first): valid file loads with durations parsed; unknown field rejected; exactly one of `fault.ansible`/`fault.kube`; host outside allowlist rejected; namespace outside allowlist rejected; `revert_after < fire_within+resolve_within+10m` rejected; missing runbook file rejected.

- [ ] Write failing tests, run `go test ./internal/...` (fail), implement, run (pass), commit `feat: load and validate drill files`.

### Task 2: Runbook parser

**Files:** `internal/runbook/runbook.go`, `internal/runbook/runbook_test.go`.

**Produces:** `runbook.Parse(path string) (*runbook.Runbook, error)`; `Runbook{Path string; Blocks []Block}`; `Block{Name, Kind /* "check"|"fix" */, Target, Lang, Script string; Line int; Timeout time.Duration}`; `(*Runbook).Checks() []Block`, `Fixes() []Block`.

Uses goldmark to walk fenced code blocks; info string is `<lang> <json-object>`. Untagged blocks are ignored. Tests: tagged and untagged blocks; document order kept; `drill` value other than check/fix is an error with line number; malformed JSON is an error with line number; default timeout 5m; `target` defaults to `runner`; `host:<name>` accepted.

- [ ] Tests first, implement, commit `feat: parse tagged runbook code blocks`.

### Task 3: Verdict logic (test first; fails quietly otherwise)

**Files:** `internal/engine/verdict.go`, `internal/engine/verdict_test.go`.

**Produces:** `type Verdict string` with the eight constants; `type Observations struct{Aborted bool; AbortReason string; Fired bool; FiredAfter time.Duration; Receivers []string; ExpectedReceiver string; FailedStep string; Resolved bool; ResolvedAfter, ExpectedResolve time.Duration; ControlRan, ControlCleared bool}`; `Decide(o Observations) (Verdict, []string /*findings*/)`.

Table test with one row per verdict plus precedence rows (misrouted and step-failed together gives misrouted with both findings; ExpectedResolve zero never yields slower-than-runbook; ControlRan false with all else passing gives pass).

- [ ] Tests first, implement, commit `feat: decide drill verdicts`.

### Task 4: Prometheus and Alertmanager clients

**Files:** `internal/prom/prom.go`, `internal/am/am.go`, tests with `httptest` servers.

**Produces:** `prom.New(url) *prom.Client`; `(*Client).Firing(ctx, alert string, labels map[string]string) (bool, error)` querying `ALERTS{alertname=..., alertstate="firing", ...}`. `am.New(url) *am.Client`; `CreateSilence(ctx, alert string, labels map[string]string, until time.Time, createdBy, comment string) (string, error)`; `DeleteSilence(ctx, id string) error`; `Receivers(ctx, alert string, labels map[string]string) ([]string, bool, error)` via `GET /api/v2/alerts?active=true&silenced=true&inhibited=true&filter=...`.

Tests: label values with quotes are escaped; empty result is not firing; HTTP 500 is an error; silence POST body has `isRegex:false, isEqual:true` matchers; receivers parsed from a recorded AM response.

- [ ] Tests first, implement, commit `feat: query prometheus and alertmanager apis`.

### Task 5: Executors

**Files:** `internal/exec/ansible/ansible.go`, `internal/exec/kube/kube.go`, `internal/exec/local/local.go`, tests.

**Produces:**
- `ansible.New(cfg) *ansible.Runner` with `Apply(ctx, f drill.AnsibleFault, host, drillID string, revertAfter time.Duration, log io.Writer) error`, `Revert(...) error`, `RunBlock(ctx, b runbook.Block, host string, env map[string]string, log io.Writer) error`. It writes a temporary playbook (`include_role` with `tasks_from: apply|revert`, or a `shell` task with `environment`) and runs `ansible-playbook -i <inventory>` with `ANSIBLE_COLLECTIONS_PATH`.
- `kube.New(kubeconfig) (*kube.Faults, error)` with `Apply(ctx, f drill.KubeFault, drillID string) error` (saves the Deployment's replicas and pod template to state before changing it) and `Revert(ctx, f drill.KubeFault, drillID string) error` (restores them if they differ).
- `local.RunBlock(ctx, b runbook.Block, env map[string]string, log io.Writer) error` running `bash -euo pipefail -c` with `KUBECONFIG` set.

Unit tests: generated playbook YAML for apply, revert and block (golden strings); kube fault against client-go's fake clientset (scale saves and restores replicas; patch restores template); local block exit codes and timeout.

- [ ] Tests first, implement, commit `feat: add ansible kube and local executors`.

### Task 6: Drill engine and state

**Files:** `internal/engine/engine.go`, `internal/engine/engine_test.go`, `internal/state/state.go`.

**Produces:** `engine.Engine{Prom, AM, Faults FaultExecutor, Blocks BlockRunner, Clock Clock, State *state.Store, Log io.Writer}`; `(*Engine).Run(ctx, d *drill.Drill, rb *runbook.Runbook, opts RunOptions{SkipControl bool}) Result`; `Result{Drill string; Verdict Verdict; Findings []string; Started time.Time; FiredAfter, ResolvedAfter time.Duration; Receivers []string; LogPath string}`. `state.Store` writes `results.jsonl`, `active/<drillID>.json` (removed after cleanup) and `control-cache.json` keyed by a hash of the drill file.

Engine tests with fakes cover: happy path gives pass and calls revert and delete silence; fault never fires gives alert-did-not-fire and still reverts; a check block failing stops before fixes; context cancelled mid-wait still reverts and records aborted; already firing aborts with no Apply call; control run clearing gives inconclusive; cached control result skips the control run.

- [ ] Tests first, implement, commit `feat: orchestrate drills end to end`.

### Task 7: CLI commands

**Files:** `cmd/drillbook/*.go`, `internal/lint/lint.go`, `internal/affected/affected.go`, tests.

Commands: `run <drill...|--all>`, `plan <drill>`, `lint [--rules glob]`, `affected <git-range> [--max-fire-within 20m]`, `abort`, `version`. `run` exits 1 when any verdict is not pass. `affected` maps changed files to drills (drill file, its runbook, or a rules file that defines its alert). Tests for lint and affected use temp dirs and a temp git repo.

- [ ] Tests first, implement, commit `feat: add run plan lint commands` then `feat: add affected and abort commands`.

### Task 8: Ansible fault collection

**Files:** `ansible/collections/ansible_collections/drillbook/faults/{galaxy.yml,README.md}`, `roles/{stop_service,fill_filesystem,stop_container}/{tasks/main.yml,tasks/apply.yml,tasks/revert.yml,defaults/main.yml,meta/argument_specs.yml}`, `tests/roles/*.yml`, `tests/roles/run.sh`.

Each `apply` arms `systemd-run --on-active=<revert_after> --unit=drillbook-revert-<drill_id>` with the revert command, then applies. `fill_filesystem` refuses filesystems larger than `max_fs_bytes` (default 2 GiB) and writes one file of the computed size with `fallocate`. `revert` stops the timer unit and undoes the fault idempotently. Tests run against a throwaway `drillbook-roletest` node container: apply, assert, revert, assert; plus apply-then-wait to prove the timer restores state.

- [ ] Write test playbooks, run (fail), implement roles, run (pass), `ansible-lint`, commit `feat: add ansible fault collection`.

### Task 9: Drill environment

**Files:** `env/node-image/Dockerfile`, `env/terraform/{main.tf,variables.tf,outputs.tf,versions.tf}`, `env/ansible/{site.yml,templates/kubeadm-init.yaml.j2,files/kps-values.yaml}`, `env/up.sh`, `env/down.sh`, `examples/shop/{shop-api.yaml,rules.yaml}`, `drillbook.yaml`.

Terraform builds the image, a `drillbook` network `172.31.250.0/24`, named volumes, two privileged containers (`drillbook-cp` .10, `drillbook-worker` .11; worker gets a 256 MiB tmpfs at `/var/lib/app-data`), an ed25519 key and `env/.generated/{inventory.ini,id_ed25519}`. Ansible runs kubeadm init with the template, applies kindnet and local-path from `/kind/manifests`, joins the worker, fetches the kubeconfig, installs kube-prometheus-stack with Helm and applies the shop example. `env/up.sh` is idempotent.

- [ ] Run `env/up.sh`, verify `kubectl get nodes` Ready, Prometheus ready, `ALERTS{alertname="Watchdog"}` present, commit `feat: build kubeadm drill environment`.

### Task 10: Five drills and runbooks

**Files:** `drills/*.yaml`, `runbooks/kubernetes/{KubeNodeNotReady,KubePodCrashLooping,KubeDeploymentRolloutStuck}.md`, `runbooks/node/NodeFilesystemAlmostOutOfSpace.md`, `examples/shop/runbooks/ShopApiDown.md`.

| Drill | Fault | Alert |
|---|---|---|
| `kubelet-stopped` | `stop_service kubelet` on worker | KubeNodeNotReady |
| `app-data-disk-full` | `fill_filesystem /var/lib/app-data` to 4% free | NodeFilesystemAlmostOutOfSpace (warning) |
| `shop-api-scaled-to-zero` | kube scale 0 | ShopApiDown (`for: 1m`) |
| `shop-api-crashloop` | kube patch with a bad flag | KubePodCrashLooping |
| `shop-api-bad-image` | kube patch with a missing tag | KubeDeploymentRolloutStuck |

- [ ] `drillbook lint` passes, run each drill for real, record verdicts in `docs/v0.1-drill-results.md` from `results.jsonl`, commit per drill `feat: add <drill> drill`.

### Task 10b: Abort and interruption checks on the live env

- [ ] Start `kubelet-stopped`, send SIGINT during the fire wait, confirm kubelet is active, the silence is gone and the result is `aborted`. Commit any fixes as `fix: ...`.

### Task 11: CI

**Files:** `.github/workflows/ci.yml`, `.github/workflows/e2e.yml`, `.golangci.yml`, `.ansible-lint`.

`ci.yml` on push and PR: `go test ./...`, `go vet`, golangci-lint, ansible-lint, `terraform fmt -check` and `validate`, `promtool check rules` and `test rules` for `examples/shop/rules.yaml`, `pint lint`, `drillbook lint`. `e2e.yml` on workflow_dispatch, weekly schedule, and PRs touching `env/`, `drills/`, `runbooks/` or `internal/`: build the environment on the runner and run `drillbook run $(drillbook affected ... --max-fire-within 20m)` falling back to `shop-api-scaled-to-zero`.

- [ ] Push, watch both workflows to green, commit fixes. Commit `ci: add lint test and e2e workflows`.

### Task 12: Docs and release

- [ ] README with install, quickstart against the drill environment, runbook format, drill format, safety; `docs/v0.1-drill-results.md` from real runs; update spec status; tag `v0.1.0` and create a GitHub release with linux/amd64 and darwin/arm64 binaries built by a `release.yml` workflow. Commit `docs: write v0.1 readme and quickstart`.
