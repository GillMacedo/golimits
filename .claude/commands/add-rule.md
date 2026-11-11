Add a rule to golimits for: $ARGUMENTS

1. Find the Go runtime or Kubernetes source that defines the behavior, and link it in the PR.
2. Add the check to `checkContainer` in `plugin/scripts/golimits.go`, with a code in kebab case, a message that says what happens at run time, and a fix line.
3. Add cases to `TestRules` in `plugin/scripts/golimits_test.go`: what it flags and what must stay quiet.
4. If the rule says what the Go runtime does with a value, add the value to `tests/runtime_test.go` so the probe confirms it.
5. Add the code to the table in `README.md` and to the skill's list in `plugin/skills/checking-go-runtime-limits/SKILL.md`.
6. Run `make test`.
