# Security

Report vulnerabilities through [GitHub's private vulnerability reporting](https://github.com/GillMacedo/golimits/security/advisories/new), not in a public issue.

`golimits` reads files only. It never connects to a cluster and never runs anything from its input. It doesn't read Secrets: variables that come from a Secret are listed as not checked, with the Secret's name and never a value.

A missed or wrong finding is a normal bug. If `golimits` prints a Secret value, or reports a clean result for input it couldn't read, report it privately. `golimits` is a diagnostic aid and doesn't enforce anything.
