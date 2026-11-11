# Architecture

golimits is one Go program in `plugin/scripts`, plus a skill that tells Claude when and how to run it.

## Flow

1. **Load** (`load`, `parseFile`). Each argument is a file, a directory or `-`. A file is refused when it is empty, has no final line break, contains NUL bytes or `{{`, ends on a key with no value, or holds a document that isn't a Kubernetes object (no `apiVersion` or `kind`, or no `metadata.name`). Directories are walked for `.yaml`, `.yml` and `.json`, and refused when they hold a `Chart.yaml` or a kustomization. Each document is decoded twice: into plain maps for the values, which keeps YAML merge keys as kubectl does, and into ordered maps so `endPath` can record where the last object of each file ends (inside a `kind: List`, where its last item ends). Symbolic links inside a directory are refused, not followed.
2. **Find workloads** (`podSpec`, `containersOf`). Known kinds have a fixed pod spec path. Other kinds count when they have `spec.template.spec.containers`. A known kind without a pod spec or containers, or a container without a name or image, is refused as cut off. So is a file whose last object ends inside a container, or whose last object is an unknown kind with `spec.template` and no containers, unless the file ends with an unindented `---` or `...` line.
3. **Resolve variables** (`resolveEnv`). As the kubelet does: `envFrom` first (ConfigMaps in the input are read, with prefixes; Secrets and missing ConfigMaps are unknown), then `env` in order with `$(VAR)` expansion, so a later entry wins. `configMapKeyRef` reads ConfigMaps in the input; an optional missing key leaves the earlier value. A value set by `envFrom` becomes unknown when a later `envFrom` source can't be read, and so does a `$(VAR)` that such a source could define. `resourceFieldRef` computes `ceil(quantity / divisor)`, in millicores for CPU, as `k8s.io/kubernetes/pkg/api/v1/resource` does. A `limits.*` reference with no limit becomes the node's allocatable amount, which is unknown.
4. **Check** (`checkContainer`). Only containers that set GOMEMLIMIT, GOGC or GOMAXPROCS, or are named with `--go`, are checked. The rules are listed in the README.
5. **Report** (`printText`, JSON). Findings are sorted by severity. The summary line always says how many workloads and containers were read and how many were checked.

## Why the parsers are copied

`parseByteCount` in `quantity.go` is the Go runtime's GOMEMLIMIT parser. The runtime doesn't export it, and the point is to agree with it exactly. Go 1.22 used its own `atoi64`, which rejects a leading `+`, and newer releases use `strconv.ParseInt`, which accepts it, so golimits reports a leading `+` as an error for all three variables. `tests/runtime_test.go` builds `tests/testdata/probe` with the local toolchain and checks every value in its tables against the real runtime, and CI runs it on Go 1.22 and the current release.

Kubernetes quantities are parsed with `math/big` so a value like `1.5Gi` is exact before rounding.

## Exit codes

0 when nothing reaches `--fail-on`, 1 when something does, 2 when the input can't be trusted or the flags are wrong. Exit 2 prints only to stderr.
