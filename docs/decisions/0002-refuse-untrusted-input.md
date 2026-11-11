# 0002: Refuse input it can't trust, with exit 2

**Decision:** golimits exits 2 and prints nothing on stdout for input it can't fully read: empty files, failed command output, unrendered templates, charts, kustomizations, files with no final line break, and files whose last object ends inside a container. `--fail-on` doesn't change this.

**Why:** a check that passes on the output of a failed `helm template` is worse than no check. A file cut mid-line can still parse, and a cut like `memory: 51` from `memory: 512Mi` changes the answer, so a missing final line break is refused. A file cut on a line break inside a container drops the env or limits after the cut, which turns a finding into a clean result, so that is refused too. A rendered file that really ends inside a container can be marked complete with a final `---` line.

**Cost:** a hand-written single-Deployment file usually ends inside its container and is refused until it gets a final `---`. The message says so.

**Not covered:** a file cut exactly between two objects is a valid shorter file. The summary line says how many workloads and containers were read so a person or agent can notice.
