# 0003: Check rules against the real Go runtime

**Decision:** every rule that says what the Go runtime does with a value (exits, ignores it, applies it) is tested by running a real Go program with that value in its environment, built with the toolchain running the tests.

**Why:** the parsers changed between Go versions (`atoi64` in Go 1.22, `strconv.ParseInt` later), and the docs don't list every accepted form. CI runs the tests on Go 1.22 and on the current release, so a change in a future release fails CI instead of producing a wrong report.

**Limit:** the kubelet isn't tested the same way. The `resourceFieldRef` arithmetic follows `k8s.io/kubernetes/pkg/api/v1/resource/helpers.go` and is covered by unit tests only.
