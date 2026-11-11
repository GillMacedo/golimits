# golimits: check GOMEMLIMIT, GOGC and GOMAXPROCS in Kubernetes manifests

[![CI](https://github.com/GillMacedo/golimits/actions/workflows/ci.yml/badge.svg)](https://github.com/GillMacedo/golimits/actions/workflows/ci.yml) [![MIT license](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

**A Claude Code skill and Go CLI for anyone who runs Go services on Kubernetes. It reads rendered Helm, Kustomize or plain manifests and checks the Go runtime variables in each container against its CPU and memory limits: values that crash the program at startup, values Go silently ignores, and limits that leave the garbage collector no room before an OOMKill.**

Kubernetes and Go write sizes differently. A container limit is `512Mi`, and Go wants `512MiB`, so `GOMEMLIMIT: 512Mi` makes every Go program exit at startup with `fatal error: malformed GOMEMLIMIT`, which shows up as a CrashLoopBackOff after the rollout. `GOGC: 50%` and `GOMAXPROCS: "1.5"` are worse: Go ignores them without a word and runs with its defaults. A `resourceFieldRef` with `divisor: 1Mi` hands Go the number 512, which it reads as 512 bytes, so the garbage collector runs back to back. `GOGC: "off"` with no GOMEMLIMIT turns the collector off until the kernel kills the pod. None of this fails `helm lint`, `kubeconform` or a schema check, because to Kubernetes these are just strings. golimits reads the rendered files offline and tells you which container, which value, and what to write instead.

## What you can ask Claude

- "Our Go API is OOMKilled every few hours. Is GOMEMLIMIT set right in the chart?"
- "Check this Helm chart's Go runtime settings against the container limits before I merge."
- "The pod crash-loops with malformed GOMEMLIMIT. What should the value be?"
- "Which of our Go containers have a memory limit and no GOMEMLIMIT?"

## Install

As a Claude Code plugin:

    claude plugin marketplace add GillMacedo/golimits
    claude plugin install golimits@golimits-marketplace

Or run it directly with Go 1.22 or newer. It has no dependencies outside this repository:

    helm template my-release ./chart | go run github.com/GillMacedo/golimits/plugin/scripts@v0.1.0 -

## Example

`examples/broken` is a made-up shop with three workloads and the mistakes above. `--fail-on never` keeps the exit code at 0 for this demo; without it, the run exits 1:

    $ go run ./plugin/scripts --fail-on never examples/broken
    ERROR gomemlimit-malformed: Deployment shop/api, container api
      GOMEMLIMIT="900Mi" makes every Go program exit at startup with "fatal error: malformed GOMEMLIMIT". Go accepts a whole number of bytes with an optional B, KiB, MiB, GiB or TiB suffix, and not Kubernetes suffixes such as Mi or M
      fix: write 900MiB
      in examples/broken/app.yaml

    ERROR gogc-ignored: Deployment shop/api, container api
      GOGC="200%" is not a whole number or "off", so the Go runtime silently ignores it and uses 100. The value comes from ConfigMap go-runtime key GOGC
      fix: write GOGC=200
      in examples/broken/app.yaml

    ERROR gogc-off-no-limit: Deployment shop/worker, container worker
      GOGC=off with no GOMEMLIMIT turns the garbage collector off completely, so the heap grows until the container is OOM-killed
      fix: set GOMEMLIMIT too, about 90% of the limit: 1843MiB
      in examples/broken/app.yaml

    ERROR gomemlimit-tiny: DaemonSet shop/log-agent, container agent
      GOMEMLIMIT=256 is 256 bytes, under 0.01% of the 256 MiB memory limit. Go reads a number with no suffix as bytes, so the garbage collector runs back to back and can take about half the CPU. It comes from resourceFieldRef limits.memory with divisor 1Mi, which divides the limit before Go reads it as bytes
      fix: remove the divisor so the kubelet writes bytes, or set GOMEMLIMIT to 230MiB
      in examples/broken/app.yaml

    WARN gomaxprocs-above-cpu-limit: Deployment shop/worker, container worker
      GOMAXPROCS=8 with a CPU limit of 500m lets more threads run Go code than the CPU quota allows, so the container gets throttled
      fix: set GOMAXPROCS to 2 or less, or remove it on Go 1.25 and newer, which reads the CPU limit itself
      in examples/broken/app.yaml

    4 errors, 1 warning and 0 notes in 3 checked containers (read 4 containers in 3 workloads).

`examples/fixed` is the same app with the values corrected. `--go '*'` says every container runs Go, so a Go container with a memory limit and no GOMEMLIMIT would be reported too. It stays quiet:

    $ go run ./plugin/scripts --go '*' examples/fixed
    No problems in the 4 containers that set Go runtime variables or were named with --go (read 4 containers in 3 workloads).

On real chart output: the Traefik chart sets GOMEMLIMIT and GOMAXPROCS itself, and gets them right. This render of Traefik 42.3.2 adds `GOMEMLIMIT: 512Mi` and `GOGC: 50%` through the chart's own `env` value, the way a user would:

    $ go run ./plugin/scripts --fail-on never tests/testdata/charts/traefik-bad.yaml
    ERROR gomemlimit-malformed: Deployment edge/edge-traefik, container edge-traefik
      GOMEMLIMIT="512Mi" makes every Go program exit at startup with "fatal error: malformed GOMEMLIMIT". Go accepts a whole number of bytes with an optional B, KiB, MiB, GiB or TiB suffix, and not Kubernetes suffixes such as Mi or M
      fix: write 512MiB
      in tests/testdata/charts/traefik-bad.yaml

    ERROR gogc-ignored: Deployment edge/edge-traefik, container edge-traefik
      GOGC="50%" is not a whole number or "off", so the Go runtime silently ignores it and uses 100
      fix: write GOGC=50
      in tests/testdata/charts/traefik-bad.yaml

    2 errors, 0 warnings and 0 notes in 1 checked container (read 1 container in 1 workload).

Add `--format json` for machine-readable output.

## When it refuses

golimits exits 2 and prints only the reason when it can't trust what it read, whatever `--fail-on` says. For example, feeding it the error that `helm template` prints when a chart fails to render:

    printf 'Error: execution error at (app/templates/deployment.yaml:31:20): resources.limits.memory is required\n' | go run ./plugin/scripts -

prints this on stderr. The program exits 2 (under `go run`, Go adds a line `exit status 2` and itself exits 1):

    golimits: can't trust the input, nothing was checked: stdin, document 1: not valid YAML or JSON (yaml: mapping values are not allowed in this context); is it the output of a failed command?

It does the same for empty files, other failed command output (kubectl, kustomize, `go build`, a Go runtime crash), a values file, an API error, invalid or cut-off YAML or JSON, a file with no final line break, a template with `{{ }}` left in, a chart or kustomization directory, a symbolic link inside a directory, a workload without containers, a container without a name or image, a file whose last object ends inside a container, and `--go` names that aren't in the input. A single bad file among good ones fails the whole run.

A file cut on a line break can still parse. If the cut falls inside a container, the env or limits after it are gone, and a broken value could pass as clean. So golimits refuses a file whose last object ends inside a container. A rendered file that really ends there, which is common for a hand-written single Deployment, can be marked complete with a final `---` line, for example `{ helm template ...; echo ---; } | golimits -`. A file cut exactly between two objects is a valid shorter file, and no tool can tell. The summary line says how many workloads and containers were read, so check that number.

## What it reports

| Code | Severity | Meaning |
|---|---|---|
| `gomemlimit-malformed` | ERROR | Go exits at startup: Kubernetes suffixes (`512Mi`, `1G`), decimals (`1.5GiB`), lowercase units, spaces, `OFF`. A leading `+`, which Go 1.22 rejects and newer releases accept |
| `gomemlimit-tiny` | ERROR | GOMEMLIMIT under 1 MiB, or under 1/256 of the memory limit: a number with no unit, or a `resourceFieldRef` with `divisor: 1Mi`, `1Ki` or `1k` |
| `gogc-ignored` | ERROR | GOGC that isn't a whole number or `off` (`50%`, `OFF`, `1e2`), which Go ignores and replaces with 100 |
| `gomaxprocs-ignored` | ERROR | GOMAXPROCS that isn't a positive whole number (`1.5`, `500m`, `0`), which Go ignores |
| `gogc-off-no-limit` | ERROR | GOGC=off (or negative) with no GOMEMLIMIT, or with a GOMEMLIMIT above the memory limit: the collector doesn't run before the OOMKill |
| `env-not-string` | ERROR | A Go variable written as an unquoted number, which the API server rejects |
| `bad-quantity` | ERROR | A CPU or memory limit that isn't a Kubernetes quantity |
| `gomemlimit-above-limit` | WARN | GOMEMLIMIT above the memory limit, so the kernel kills the container first |
| `gomemlimit-no-headroom` | WARN | GOMEMLIMIT within 5% of the memory limit (`--headroom` changes this), including `resourceFieldRef: limits.memory` with no divisor. The [Go GC guide](https://go.dev/doc/gc-guide) suggests 5-10% headroom |
| `gomemlimit-node-memory` | WARN | GOMEMLIMIT from `resourceFieldRef: limits.memory` on a container with no memory limit, which gives the node's allocatable memory |
| `gomaxprocs-above-cpu-limit` | WARN | GOMAXPROCS above the CPU limit rounded up (at least 2, as Go 1.25 does), including `resourceFieldRef: limits.cpu` with `divisor: 1m` |
| `gogc-zero` | WARN | GOGC=0, which makes the collector run continuously |
| `go-without-gomemlimit` | WARN | With `--go`: a Go container with a memory limit and no GOMEMLIMIT |
| `not-checked` | note | Values from Secrets, ConfigMaps not in the input, or fieldRefs |
| `no-memory-limit`, `pod-level-resources` | note | Nothing to compare GOMEMLIMIT with, or pod-level resources, which golimits doesn't use |

Values are resolved the way the kubelet resolves them: `envFrom` first (with `prefix`), then `env` in order, so a later entry wins; `$(VAR)` references to earlier variables are expanded and `$$` is a literal `$`; `configMapKeyRef` and `envFrom` ConfigMaps are read when the ConfigMap is in the input; `resourceFieldRef` gives `ceil(quantity / divisor)`, counted in millicores for CPU, from the same container or the one named in `containerName`. Init containers are checked too.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Nothing at or above `--fail-on`, which is `error` by default. Use `--fail-on warn` to fail on warnings too, or `never`. |
| 1 | Findings at or above `--fail-on`. |
| 2 | The input can't be trusted or the flags are wrong. Nothing was checked, and stdout is empty. |

## Which containers it checks

Any container that sets GOMEMLIMIT, GOGC or GOMAXPROCS, in `env` or through a ConfigMap. Manifests don't say which images are Go programs, so a Go container that sets none of them is only checked when you name it with `--go api,worker` (or `--go '*'` when every container runs Go). The skill tells Claude to work that out from the repository's Dockerfiles and `go.mod`.

## Limits

- **Settings, not usage.** golimits doesn't know how much memory your program needs. A clean result means the values parse and fit the limits, not that the pod won't be OOMKilled.
- **Programs that set limits themselves.** A program that calls `debug.SetMemoryLimit`, or uses a library such as automemlimit or automaxprocs, can override what the manifest says. golimits only sees the manifest.
- **Secrets aren't read.** Values from Secrets are listed as not checked.
- **Pod-level resources** (Kubernetes 1.32 and newer) aren't used as a container's limit.
- **The Go version of the image isn't known.** GOMAXPROCS rules assume Go 1.25's default of following the CPU limit; older Go uses all node CPUs when GOMAXPROCS isn't set, which golimits doesn't report.

## FAQ

**Why does my Go pod crash with "malformed GOMEMLIMIT"?** GOMEMLIMIT takes a whole number of bytes with an optional `B`, `KiB`, `MiB`, `GiB` or `TiB` suffix. Kubernetes writes `Mi` and `Gi`, and many people copy the limit across. Write `512MiB`, not `512Mi`. Decimals such as `1.5GiB` are rejected too ([golang/go#76923](https://github.com/golang/go/issues/76923)).

**What should GOMEMLIMIT be in Kubernetes?** A bit below the container's memory limit. The Go GC guide suggests leaving 5-10% for memory the runtime doesn't manage. For a `1Gi` limit, `921MiB` is 90%.

**Can I set GOMEMLIMIT from the memory limit with the downward API?** Yes, with `resourceFieldRef: {resource: limits.memory}` and no divisor, which gives the limit in bytes. That leaves no headroom, so golimits warns about it. With `divisor: 1Mi` Go gets a number of MiB and reads it as bytes, which golimits reports as an error.

**Does GOGC=50% work?** No. GOGC takes a whole number or `off`. Anything else is ignored and Go uses 100.

**Do I still need GOMAXPROCS on Go 1.25?** Usually not: since Go 1.25 the runtime follows the container's CPU limit on Linux. A GOMAXPROCS you set yourself overrides that, so golimits checks it against the limit.

**Is it an MCP server?** No. It's a skill with one Go program, so `go run` or `go build` is all it needs.

## Tested on

- **The Go runtime itself:** `tests/runtime_test.go` builds a small program and runs it with 27 GOMEMLIMIT, 17 GOGC and 12 GOMAXPROCS values, then checks that golimits reports exactly the values that crash it or that it ignores, and nothing for the values it applies. It also checks that the values golimits calls tiny make the garbage collector run at least five times as often. These tests pass on Go 1.22.12 and Go 1.27.3 on Linux arm64. They found that Go 1.22 rejects a leading `+` that Go 1.27 accepts.
- **Real charts with real Helm:** Helm v3.16.3 renders of Traefik 42.3.2, the OpenTelemetry Collector chart 0.178.3 and cert-manager v1.21.3. The charts' own GOMEMLIMIT and GOMAXPROCS settings stay quiet, and so does cert-manager, which sets none. With mistakes added through each chart's documented `env` or `extraEnvs` values, each is reported. These renders are in `tests/testdata/charts`. I also ran it on the Loki single-binary example and the Tempo and VictoriaMetrics single charts, which stayed quiet; those renders aren't kept.
- **Input it can't trust:** tests pass 15 kinds of bad input (empty, whitespace, comments only, random text, binary, failed `helm template`, `helm install --dry-run` output, failed `kustomize`, `kubectl` and `go build`, a Go runtime crash, a values file, an API error, cut JSON, an unrendered template) alone, on stdin, before and after a real file, and in a directory with a real file, under every `--fail-on`, `--format json` and `--go '*'`, and check for exit 2 with nothing on stdout. Others cut real renders and the examples at every line break and at about 150 byte offsets each: a mid-line cut always exits 2, and a line-break cut either exits 2 or gives the same findings as the whole file (a subset for the multi-workload examples, when the cut falls between objects).
- **An adversarial review:** a separate review session tried to make golimits give a wrong all-clear, and found ten cases in an early version: YAML merge keys that were dropped, a `divisor: 1k` that wasn't called tiny, an optional ConfigMap key that erased an earlier value, GOGC=off with a GOMEMLIMIT above the limit reported only as a warning, cut-off detection missing for `kind: List` and for an indented `---` in a block scalar, a cut custom resource, `$(VAR)` that an envFrom Secret could set, whitespace in a quantity, and symbolic links in a directory. All are fixed, and the inputs are kept in `tests/testdata/review` as tests.
- **The installed plugin:** a test copies `plugin/` outside the repository and builds it with the skill's own command.

Not tested: a real kubelet (the `resourceFieldRef` arithmetic follows the Kubernetes source and has unit tests only); real OOMKills and CPU throttling in a cluster; Windows and macOS; Go versions other than 1.22.12 and 1.27.3; a fresh Claude Code session using the skill. The `go run ...@v0.1.0` line works only once the repository is published.

## Contributing

Contributions are welcome, from people and from people working with AI agents. See [CONTRIBUTING.md](CONTRIBUTING.md). Good first issues are [listed here](https://github.com/GillMacedo/golimits/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22).

### How pull requests work

1. **Open a PR.** Fork the repo, work on a branch, and keep it to one change. Write a title that says what changes for users, because it becomes the changelog line: `feat:` goes under Added and `fix:` under Fixed.
2. **Automatic check.** Code changes need tests, and `make test` must pass. Changes over 30 lines need a linked open issue (`Closes #<number>`), and a PR can have at most 800 changed lines. If something is missing, a comment tells you what to fix.
3. **Review.** Claude reviews each PR automatically. Code PRs also get a security review and are tested in a sandbox. The review then merges the PR or asks for changes, and a small problem may become a follow-up issue instead. A PR with no new commit 14 days after a change request is closed, and you can reopen it. PRs that change CI, agent settings, npm or Python dependencies, vendored code or build files can't be merged this way, and a comment says so. The same goes for anything the security review doesn't clear.
4. **Release.** A merge that changes what users install becomes a new version within about two hours, with a GitHub Release and, if the project has one, its npm package. Docs and CI changes wait for the next release.

## AI disclosure

This project was largely generated with Claude Code and reviewed by its maintainer.

## About me

I'm Giulliano Macedo, and I build developer tools at GoTech Solutions. Follow along or get in touch:

- GitHub: [GillMacedo](https://github.com/GillMacedo)
- LinkedIn: [giulliano](https://www.linkedin.com/in/giulliano/)
- X: [@giumacedo](https://x.com/giumacedo)

If `golimits` saved you some prompts, a star helps others find it.

---

Built by [Giulliano Macedo](https://github.com/GillMacedo), GoTech Solutions · [LinkedIn](https://www.linkedin.com/in/giulliano/) · [X](https://x.com/giumacedo)
