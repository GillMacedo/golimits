# golimits plugin

This plugin adds the `checking-go-runtime-limits` skill to Claude Code. The skill runs `scripts/golimits.go` on rendered Kubernetes manifests and checks GOMEMLIMIT, GOGC and GOMAXPROCS against each container's resource limits. It needs Go 1.22 or newer. It reads files only and never connects to a cluster.

See the repository README for examples, exit codes and limits: https://github.com/GillMacedo/golimits

## Install

    claude plugin marketplace add GillMacedo/golimits
    claude plugin install golimits@golimits-marketplace
