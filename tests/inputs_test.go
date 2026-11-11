package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Bad inputs, as real tools print them. Each is passed alone, next to a real
// file in either order, inside a directory with a real file, and on stdin.
var badInputs = map[string]string{
	"empty":              "",
	"whitespace":         " \n\t\n",
	"comments only":      "# Source: app/templates/deployment.yaml\n---\n",
	"random text":        "lorem ipsum dolor sit amet\n",
	"binary":             "\x00\x01\x02\x03garbage\xff\xfe\n",
	"helm template fail": "Error: execution error at (app/templates/deployment.yaml:31:20): resources.limits.memory is required\n",
	"helm dry-run":       "NAME: shop\nLAST DEPLOYED: Mon Nov  9 10:00:00 2026\nNAMESPACE: default\nSTATUS: pending-install\nREVISION: 1\n",
	"kustomize fail":     "Error: accumulating resources: accumulation err='accumulating resources from 'base': evalsymlink failure on 'base' : lstat base: no such file or directory'\n",
	"kubectl fail":       "error: the server doesn't have a resource type \"deploy\"\n",
	"go build fail":      "# example.com/shop/cmd/api\ncmd/api/main.go:12:2: undefined: serve\n",
	"go runtime crash":   "GOMEMLIMIT=512Mi\nfatal error: malformed GOMEMLIMIT; see `go doc runtime/debug.SetMemoryLimit`\n\nruntime stack:\nruntime.throw({0xb8efa?, 0xa?})\n",
	"values file":        "replicaCount: 2\nresources:\n  limits:\n    memory: 512Mi\n",
	"api error":          "apiVersion: v1\nkind: Status\nmetadata: {}\nstatus: Failure\nmessage: deployments.apps is forbidden\nreason: Forbidden\ncode: 403\n",
	"cut json":           "{\"apiVersion\": \"apps/v1\", \"kind\": \"Deployment\", \"metadata\": {\"name\": \"api\"}, \"spec\": {\"template\":\n",
	"unrendered":         "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: {{ include \"app.fullname\" . }}\n",
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func expectRefused(t *testing.T, label string, stdin []byte, args ...string) {
	t.Helper()
	for _, extra := range [][]string{nil, {"--fail-on", "warn"}, {"--fail-on", "never"}, {"--format", "json"}, {"--go", "*"}} {
		r := runTool(t, stdin, append(append([]string{}, extra...), args...)...)
		if r.code != 2 || r.stdout != "" || !strings.Contains(r.stderr, "can't trust the input") {
			t.Errorf("%s %v: exit %d\nstdout: %s\nstderr: %s", label, extra, r.code, r.stdout, r.stderr)
		}
		if strings.Contains(r.stdout+r.stderr, "No problems") {
			t.Errorf("%s %v: printed a clean result", label, extra)
		}
	}
}

func TestBadInputs(t *testing.T) {
	good, _ := filepath.Abs("testdata/charts/traefik-healthy.yaml")
	broken, _ := filepath.Abs("../examples/broken/app.yaml")
	for name, content := range badInputs {
		dir := t.TempDir()
		bad := writeFile(t, dir, "bad.yaml", content)
		expectRefused(t, name+" alone", nil, bad)
		expectRefused(t, name+" on stdin", []byte(content), "-")
		for _, other := range []string{good, broken} {
			expectRefused(t, name+" after a real file", nil, other, bad)
			expectRefused(t, name+" before a real file", nil, bad, other)
			expectRefused(t, name+" on stdin with a real file", []byte(content), other, "-")
		}
		mixed := t.TempDir()
		data, _ := os.ReadFile(broken)
		writeFile(t, mixed, "a.yaml", string(data))
		writeFile(t, mixed, "b.yaml", content)
		expectRefused(t, name+" in a directory with a real file", nil, mixed)
	}
}

func TestBadUsage(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"--format", "xml", "../examples/broken"},
		{"--fail-on", "sometimes", "../examples/broken"},
		{"--headroom", "100", "../examples/broken"},
		{"--go", "nosuchcontainer", "../examples/broken"},
		{"does-not-exist.yaml"},
		{"testdata/charts/values"},
	} {
		r := runTool(t, nil, args...)
		if r.code != 2 || strings.Contains(r.stdout, "No problems") {
			t.Errorf("%v: exit %d\n%s%s", args, r.code, r.stdout, r.stderr)
		}
	}
}

func TestExamples(t *testing.T) {
	r := runTool(t, nil, "../examples/broken")
	if r.code != 1 || !strings.Contains(r.stdout, "4 errors, 1 warning and 0 notes in 3 checked containers") {
		t.Errorf("broken: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	r = runTool(t, nil, "--go", "*", "--fail-on", "warn", "../examples/fixed")
	if r.code != 0 || !strings.HasPrefix(r.stdout, "No problems in the 4 containers") {
		t.Errorf("fixed: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
}
