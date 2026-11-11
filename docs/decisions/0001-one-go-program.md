# 0001: A skill with one Go program, with the YAML parser copied in

**Decision:** golimits is a Claude Code skill that builds and runs `plugin/scripts`, a single Go `main` package. go.yaml.in/yaml/v2 is copied into it as `yaml_*.go` with the package renamed to `main`.

**Why:** the check is a pure function of files, so an MCP server would add a process and an npm package for nothing. Claude Code installs only `plugin/`, with no `go.mod`, so the program has to build from its `.go` files alone, and Go has no YAML parser in the standard library. The copied parser is the one kubectl uses through `sigs.k8s.io/yaml`. `tests/plugin_test.go` builds a copy of `plugin/` outside the module with the skill's own command.

**Alternatives:** an MCP server (rejected as above); a hand-written YAML subset parser (too easy to get wrong on real chart output); requiring JSON input (users have YAML).
