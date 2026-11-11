---
name: checking-go-runtime-limits
description: Checks GOMEMLIMIT, GOGC and GOMAXPROCS in rendered Kubernetes manifests against each container's CPU and memory limits, and reports values the Go runtime rejects at startup ("fatal error: malformed GOMEMLIMIT", for example GOMEMLIMIT=512Mi), values it silently ignores (GOGC=50%, GOMAXPROCS=1.5), GOMEMLIMIT at or above the memory limit, resourceFieldRef divisors that turn the limit into a tiny byte count, GOGC=off with no memory limit, and GOMAXPROCS above the CPU limit. Use when a Go service in Kubernetes is OOMKilled, CPU-throttled or crash-looping at startup, when reviewing a Helm chart, Kustomize overlay or manifest change that sets Go runtime variables, or when adding GOMEMLIMIT to a deployment.
---

# Checking Go runtime limits in Kubernetes manifests

The tool reads files. It never talks to a cluster. It needs Go 1.22 or newer.

## Get rendered manifests

Pass what will actually be applied, never a chart or kustomization directory (the tool refuses those):

- Helm: `helm template RELEASE CHART -n NS -f values.yaml > rendered.yaml`, or `helm get manifest RELEASE -n NS` for what is deployed now.
- Kustomize: `kustomize build DIR > rendered.yaml` (or `kubectl kustomize DIR`).
- Plain manifests: the files or the directory.
- A live cluster, read-only: `kubectl get deploy,sts,ds,cronjob,job,configmap -n NS -o yaml > live.yaml`. Only do this if the user asked you to look at the cluster.

Check that the render command exited 0 before you go on. If ConfigMaps feed the Go variables through `configMapKeyRef` or `envFrom`, include them in the input so their values get checked.

## Find out which containers run Go

The tool always checks containers that set GOMEMLIMIT, GOGC or GOMAXPROCS. To also warn about Go containers with a memory limit and no GOMEMLIMIT, pass their names with `--go name1,name2`. Decide which containers run Go from the repository: a Dockerfile that builds with `go build`, a `go.mod` next to the chart, or an image you know is written in Go. Don't guess; leave `--go` out if you don't know.

## Run it

Build it once, then run it:

    go build -o "${TMPDIR:-/tmp}/golimits" $(ls "${CLAUDE_PLUGIN_ROOT}"/scripts/*.go | grep -v _test.go)
    "${TMPDIR:-/tmp}/golimits" rendered.yaml

Inputs are files, directories of `.yaml`, `.yml` and `.json` files, or `-` for standard input. Add `--format json` for machine-readable output and `--fail-on warn` to fail on warnings too.

## Read the exit code first

- **0:** nothing at or above `--fail-on` (default `error`). Warnings and notes may still be printed; read them.
- **1:** findings at or above `--fail-on`.
- **2:** the input can't be trusted, so nothing was checked. The message says why: an empty file, the output of a failed command, a file without a final line break, a file whose last object ends inside a container (what a file cut on a line break looks like), an unrendered template, or `--go` naming a container that isn't there. Fix the input and run again. Never describe exit 2 as a clean result. If you rendered the file yourself, the command succeeded, and the tool says the last object ends inside a container, append a line `---` to the file and run again.

Read the summary line too: it says how many workloads and containers were read. If that is fewer than the user expects, the input is incomplete.

## Check each finding

- **ERROR** means the Go runtime exits at startup, silently ignores the value, or gets a value that can't be what was meant: `gomemlimit-malformed`, `gogc-ignored`, `gomaxprocs-ignored`, `gomemlimit-tiny`, `gogc-off-no-limit`, `env-not-string`, `bad-quantity`.
- **WARN** means the value is valid but doesn't fit the container: `gomemlimit-above-limit`, `gomemlimit-no-headroom` (less than 5% headroom by default, change with `--headroom`), `gomemlimit-node-memory`, `gomaxprocs-above-cpu-limit`, `gogc-zero`, `go-without-gomemlimit`.
- **note** lists values that couldn't be read (Secrets, ConfigMaps not in the input, fieldRefs) and containers with no memory limit to compare with.

## Report

1. Errors first: the workload, the container, the value, and the fix line the tool printed. Point at the values file or template line that produces the value.
2. Then warnings, and whether each matters for this service.
3. Say what wasn't checked: the notes, and containers that run Go but weren't named with `--go`.

## Don't

Don't change anything in the cluster. Don't say a service is safe from OOM kills because golimits found nothing: it checks settings, not how much memory the program uses. Don't add GOMEMLIMIT to containers that aren't Go programs.
