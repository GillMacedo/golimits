// Package tests runs golimits as a binary, the way the skill does.
package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var bin, probe string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "golimits-tests")
	if err != nil {
		panic(err)
	}
	bin = filepath.Join(dir, "golimits")
	probe = filepath.Join(dir, "probe")
	for _, b := range [][2]string{{bin, "../plugin/scripts"}, {probe, "./testdata/probe"}} {
		cmd := exec.Command("go", "build", "-o", b[0], b[1])
		if out, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "building %s: %v\n%s", b[1], err, out)
			os.Exit(1)
		}
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type result struct {
	code           int
	stdout, stderr string
}

func runTool(t *testing.T, stdin []byte, args ...string) result {
	t.Helper()
	cmd := exec.Command(bin, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return result{code, out.String(), errb.String()}
}

type jsonFinding struct {
	Severity, Code, Kind, Name, Container, Variable, Value, Message string
}

type jsonReport struct {
	Checked, Containers, Workloads int
	Findings                       []jsonFinding
}

func runJSON(t *testing.T, args ...string) (jsonReport, result) {
	t.Helper()
	r := runTool(t, nil, append([]string{"--format", "json", "--fail-on", "never"}, args...)...)
	var rep jsonReport
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	if err := json.Unmarshal([]byte(r.stdout), &rep); err != nil {
		t.Fatalf("%v: %s", err, r.stdout)
	}
	return rep, r
}

func codes(rep jsonReport) []string {
	var out []string
	for _, f := range rep.Findings {
		out = append(out, f.Severity+" "+f.Code+" "+f.Name+"/"+f.Container)
	}
	return out
}

func mustJSON(t *testing.T, s string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(s), v); err != nil {
		t.Fatalf("%v: %s", err, s)
	}
}
