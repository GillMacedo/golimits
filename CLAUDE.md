# golimits

Claude Code skill and Go CLI that checks GOMEMLIMIT, GOGC and GOMAXPROCS in rendered Kubernetes manifests against each container's resource limits, and reports values the Go runtime rejects or ignores and values that don't fit the container.

## Architecture
`plugin/scripts/golimits.go` holds the logic. `load` and `parseFile` read files and refuse input they can't trust; `endPath` records where a file's last object ends so `podSpec` can refuse a file cut inside a container. `check` finds pod specs, `resolveEnv` computes each variable the way the kubelet does (envFrom, env in order, `$(VAR)` expansion, ConfigMap refs, `resourceFieldRef`), and `checkContainer` runs the rules. `plugin/scripts/quantity.go` parses Kubernetes quantities and holds a copy of the Go runtime's GOMEMLIMIT parser. `plugin/scripts/yaml_*.go` is go.yaml.in/yaml/v2 copied in, so the plugin builds with no go.mod. `plugin/skills/checking-go-runtime-limits/SKILL.md` tells Claude how to use it. See `docs/architecture.md`.

## Commands
- `make test`: gofmt, vet and tests, including `tests/`, which check every rule against a real Go program built with the local toolchain
- `make lint`: plugin manifest validation (needs Claude Code)
- `make demo`: run on the examples

## Conventions
- Standard library only, plus the copied YAML parser. Never connect to a cluster.
- Any input it can't fully read or trust exits 2 with nothing on stdout, whatever `--fail-on` says. Add a case to `badInputs` in `tests/inputs_test.go` for each new kind of input.
- An ERROR must mean the Go runtime exits, ignores the value, or gets a value that can't be meant. Each rule that claims runtime behavior needs a case in `tests/runtime_test.go` that runs the probe program with the value.
- Never print Secret values.

## Do and don't
- Do run `make test` on Go 1.22 and on the current Go release before calling a rule change done: the runtime's parsers differ between versions.
- Don't add `go.mod` requirements: the installed plugin has no module.
- Don't print "No problems" for a run that checked nothing.

## Definition of done
`make test` and `make lint` pass, README output matches a real run, and `ROADMAP.md` and docs are updated when relevant.
