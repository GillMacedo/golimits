package tests

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// These tests check golimits against the Go runtime that builds them: CI
// runs them on Go 1.22 and on the current release. Each value is put in a
// manifest for golimits and in the environment of a real Go program.

type probeResult struct {
	crashed                      bool
	memlimit, gogc, procs, numGC int64
}

func runProbe(t *testing.T, env ...string) probeResult {
	t.Helper()
	cmd := exec.Command(probe)
	cmd.Env = append(cleanEnv(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if !strings.Contains(string(out), "malformed GOMEMLIMIT") {
			t.Fatalf("probe with %v failed: %v\n%s", env, err, out)
		}
		return probeResult{crashed: true}
	}
	var r probeResult
	var frac float64
	if _, err := fmt.Sscanf(string(out), "memlimit=%d gogc=%d gomaxprocs=%d numgc=%d gccpu=%f", &r.memlimit, &r.gogc, &r.procs, &r.numGC, &frac); err != nil {
		t.Fatalf("probe output %q: %v", out, err)
	}
	return r
}

func cleanEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if k != "GOMEMLIMIT" && k != "GOGC" && k != "GOMAXPROCS" && k != "GODEBUG" {
			out = append(out, kv)
		}
	}
	return out
}

func manifestWith(t *testing.T, name, value string) string {
	t.Helper()
	doc := fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: probe
spec:
  containers:
  - name: probe
    image: example/probe:1
    env:
    - name: %s
      value: %s
    resources:
      limits:
        cpu: "64"
        memory: 64Gi
  restartPolicy: Never
`, name, strconv.Quote(value))
	p := filepath.Join(t.TempDir(), "pod.yaml")
	if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func toolCodes(t *testing.T, name, value string) map[string]bool {
	t.Helper()
	rep, _ := runJSON(t, manifestWith(t, name, value))
	got := map[string]bool{}
	for _, f := range rep.Findings {
		got[f.Code] = true
	}
	return got
}

func TestRuntimeGOMEMLIMIT(t *testing.T) {
	base := runProbe(t)
	values := []string{"512Mi", "512M", "512MB", "512mib", "1.5GiB", "1.5Gi", "512 MiB", " 512MiB", "512MiB ", "OFF", "Off",
		"-1", "1e9", "0x100000", "1_000", "+512MiB", "B", "KiB", "1PiB", "512KB",
		"512MiB", "4GiB", "1073741824", "1073741824B", "1048576KiB", "off", "60GiB"}
	for _, v := range values {
		t.Run(v, func(t *testing.T) {
			got := toolCodes(t, "GOMEMLIMIT", v)
			r := runProbe(t, "GOMEMLIMIT="+v)
			if r.crashed && !got["gomemlimit-malformed"] {
				t.Errorf("the Go runtime exits on GOMEMLIMIT=%q and golimits didn't say so: %v", v, got)
			}
			if !r.crashed && got["gomemlimit-malformed"] && !strings.HasPrefix(v, "+") {
				t.Errorf("golimits says GOMEMLIMIT=%q crashes, and the Go runtime accepts it", v)
			}
			if !r.crashed && len(got) > 0 && !got["gomemlimit-malformed"] {
				t.Errorf("GOMEMLIMIT=%q is fine for a 64Gi limit and golimits reported %v", v, got)
			}
			if !r.crashed && v != "off" && r.memlimit == base.memlimit {
				t.Errorf("the probe didn't apply GOMEMLIMIT=%q", v)
			}
		})
	}
}

func TestRuntimeTinyGOMEMLIMIT(t *testing.T) {
	base := runProbe(t)
	for _, v := range []string{"512", "1024", "65536B"} {
		got := toolCodes(t, "GOMEMLIMIT", v)
		if !got["gomemlimit-tiny"] {
			t.Errorf("GOMEMLIMIT=%s: no gomemlimit-tiny in %v", v, got)
		}
		r := runProbe(t, "GOMEMLIMIT="+v)
		if r.numGC < 5*base.numGC {
			t.Errorf("GOMEMLIMIT=%s: %d collections, against %d with no limit; expected many more", v, r.numGC, base.numGC)
		}
	}
}

func TestRuntimeGOGC(t *testing.T) {
	for _, v := range []string{"50%", "OFF", "Off", "1e2", "50.5", "0x64", " 50", "50 ", "fifty", "+50", "99999999999",
		"50", "200", "0", "off", "-1", "1000"} {
		t.Run(v, func(t *testing.T) {
			got := toolCodes(t, "GOGC", v)
			r := runProbe(t, "GOGC="+v, "GOMEMLIMIT=1GiB")
			n, err := strconv.ParseInt(v, 10, 32)
			intended := int64(100)
			if v == "off" {
				intended = -1
			} else if err == nil {
				intended = n
				if n < 0 {
					intended = -1
				}
			}
			ignored := r.gogc != intended || (err != nil && v != "off")
			if r.gogc == 100 && intended == 100 && v != "100" && err != nil {
				ignored = true
			}
			if ignored && !got["gogc-ignored"] {
				t.Errorf("the Go runtime ignores GOGC=%q (uses %d) and golimits didn't say so: %v", v, r.gogc, got)
			}
			if !ignored && got["gogc-ignored"] && !strings.HasPrefix(v, "+") {
				t.Errorf("golimits says GOGC=%q is ignored, and the runtime uses %d", v, r.gogc)
			}
		})
	}
}

func TestRuntimeGOMAXPROCS(t *testing.T) {
	base := runProbe(t)
	for _, v := range []string{"1.5", "500m", "0", "-2", "two", " 3", "3 ", "+3", "0x3", "1", "3", "7"} {
		t.Run(v, func(t *testing.T) {
			got := toolCodes(t, "GOMAXPROCS", v)
			r := runProbe(t, "GOMAXPROCS="+v)
			n, err := strconv.ParseInt(v, 10, 32)
			applied := err == nil && n > 0 && r.procs == n && (n != base.procs || !strings.HasPrefix(v, "+"))
			if !applied && !got["gomaxprocs-ignored"] && r.procs == base.procs {
				t.Errorf("the Go runtime ignores GOMAXPROCS=%q (uses %d) and golimits didn't say so: %v", v, r.procs, got)
			}
			if applied && got["gomaxprocs-ignored"] && !strings.HasPrefix(v, "+") {
				t.Errorf("golimits says GOMAXPROCS=%q is ignored, and the runtime uses it", v)
			}
		})
	}
}
