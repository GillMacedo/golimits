package tests

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

var cutFiles = []string{
	"testdata/charts/traefik-healthy.yaml",
	"testdata/charts/traefik-bad.yaml",
	"testdata/charts/otel-healthy.yaml",
	"testdata/charts/otel-divisor.yaml",
	"../examples/broken/app.yaml",
	"../examples/fixed/app.yaml",
}

// A file cut mid-line is always refused.
func TestCutMidLine(t *testing.T) {
	for _, f := range cutFiles {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		step := len(data)/150 + 1
		for n := 1; n < len(data); n += step {
			if data[n-1] == '\n' {
				continue
			}
			for _, fail := range []string{"error", "never"} {
				r := runTool(t, data[:n], "--fail-on", fail, "-")
				if r.code != 2 || r.stdout != "" {
					t.Fatalf("%s cut at byte %d, --fail-on %s: exit %d\n%s", f, n, fail, r.code, r.stdout)
				}
			}
		}
	}
}

// A file cut on a line break is refused, or gives the same findings as the
// whole file. The second happens only when the cut drops nothing golimits
// reads: pod fields after the last container, or whole later objects. In the
// examples, which hold several workloads, a cut between two objects drops the
// later workloads, so the findings are a subset. That case can't be told
// apart from a shorter file, and the README says so.
func TestCutOnLineBreak(t *testing.T) {
	for _, f := range cutFiles {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		full, _ := runJSON(t, f)
		want := codes(full)
		accepted := 0
		for n := 1; n < len(data); n++ {
			if data[n-1] != '\n' {
				continue
			}
			r := runTool(t, data[:n], "--format", "json", "--fail-on", "never", "-")
			if r.code == 2 {
				continue
			}
			accepted++
			var rep jsonReport
			mustJSON(t, r.stdout, &rep)
			got := codes(rep)
			multi := strings.Contains(f, "examples/")
			if (!multi && !reflect.DeepEqual(got, want)) || (multi && !subset(got, want)) {
				t.Errorf("%s cut after byte %d (%q): findings %q, whole file %q", f, n, lastLineOf(data[:n]), got, want)
			}
		}
		t.Logf("%s: %d line-break cuts accepted", f, accepted)
	}
}

func lastLineOf(b []byte) string {
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	return lines[len(lines)-1]
}

func subset(a, b []string) bool {
	in := map[string]bool{}
	for _, s := range b {
		in[s] = true
	}
	for _, s := range a {
		if !in[s] {
			return false
		}
	}
	return true
}
