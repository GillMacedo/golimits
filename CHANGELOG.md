# Changelog

All notable changes are documented here, in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) format. This project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.1.0] - 2026-11-11

### Added
- Checks GOMEMLIMIT, GOGC and GOMAXPROCS in rendered Kubernetes manifests against each container's limits: values the Go runtime rejects at startup or silently ignores, tiny GOMEMLIMIT values from a missing unit or a `resourceFieldRef` divisor, GOGC=off with no GOMEMLIMIT, GOMEMLIMIT above or too close to the memory limit, GOMAXPROCS above the CPU limit, and, with `--go`, Go containers with a memory limit and no GOMEMLIMIT.
- Resolves values the way the kubelet does: envFrom with prefixes, later entries winning, `$(VAR)` expansion, ConfigMap references and `resourceFieldRef` with divisors.
- Exit 2 with no result for input it can't trust: empty files, failed command output, unrendered templates, charts, kustomizations, files without a final line break and files cut inside a container.
- Text and JSON output, `--fail-on error|warn|never`, `--headroom` and `--go`.
- `checking-go-runtime-limits` skill and plugin manifest.
