package main

import (
	"bytes"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseQuantity(t *testing.T) {
	cases := map[string]int64{
		"512Mi": 512 << 20, "1Gi": 1 << 30, "1G": 1e9, "500M": 500e6, "1": 1,
		"1.5Gi": 3 << 29, "100k": 100e3, "1e3": 1000, "1E3": 1000, "0.5": 1, "128974848": 128974848,
		"500m": 1, ".5Ki": 512,
	}
	for in, want := range cases {
		q, err := parseQuantity(in)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if got := ceilInt(q); got != want {
			t.Errorf("%s: got %d, want %d", in, got, want)
		}
	}
	for _, bad := range []string{"", "Mi", "512MB", "512 Mi", "1.5GiB", "abc", "1e", "--1", "512mi"} {
		if _, err := parseQuantity(bad); err == nil {
			t.Errorf("%q parsed, want an error", bad)
		}
	}
}

func TestDownwardValue(t *testing.T) {
	q := func(s string) *big.Rat {
		r, err := parseQuantity(s)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	cases := []struct {
		value, divisor string
		cpu            bool
		want           int64
	}{
		{"512Mi", "1", false, 512 << 20},
		{"512Mi", "1Mi", false, 512},
		{"1Gi", "1M", false, 1074},
		{"1500m", "1", true, 2},
		{"1500m", "1m", true, 1500},
		{"250m", "1", true, 1},
		{"2", "1", true, 2},
	}
	for _, c := range cases {
		if got := downwardValue(q(c.value), q(c.divisor), c.cpu); got != c.want {
			t.Errorf("%s / %s: got %d, want %d", c.value, c.divisor, got, c.want)
		}
	}
}

func TestParseByteCount(t *testing.T) {
	ok := map[string]int64{"0": 0, "1024": 1024, "1024B": 1024, "1KiB": 1024, "512MiB": 512 << 20, "2GiB": 2 << 30, "1TiB": 1 << 40}
	for in, want := range ok {
		if got, good := parseByteCount(in); !good || got != want {
			t.Errorf("%s: got %d %v, want %d", in, got, good, want)
		}
	}
	for _, bad := range []string{"", "512Mi", "512M", "512MB", "1.5GiB", "512mib", "-1", "B", "iB", "MiB", "1PiB", "OFF", " 512MiB"} {
		if _, good := parseByteCount(bad); good {
			t.Errorf("%q accepted, want rejected", bad)
		}
	}
}

func TestExpand(t *testing.T) {
	vars := map[string]*envVal{
		"LIMIT":  {known: true, value: "460MiB"},
		"SECRET": {why: "comes from Secret s"},
	}
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"$(LIMIT)", "460MiB", true},
		{"$$(LIMIT)", "$(LIMIT)", true},
		{"$(MISSING)", "$(MISSING)", true},
		{"$(SECRET)", "", false},
		{"plain", "plain", true},
		{"$(LIMIT", "$(LIMIT", true},
	}
	for _, c := range cases {
		got, ok, _ := expand(c.in, vars, nil)
		if got != c.want || ok != c.ok {
			t.Errorf("%q: got %q %v, want %q %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestFormatAndFix(t *testing.T) {
	if got := formatBytes(512 << 20); got != "512MiB" {
		t.Errorf("formatBytes: %s", got)
	}
	if got := target90(512 << 20); got != 460<<20 {
		t.Errorf("target90: %d", got)
	}
	fixes := map[string]string{
		"512Mi":  "write 512MiB",
		"1.5GiB": "write 1536MiB",
		"512M":   "write 488MiB (512M rounded down to whole MiB) or 512000000",
		"OFF":    `write "off" in lowercase`,
	}
	for in, want := range fixes {
		if got := memFix(in); got != want {
			t.Errorf("memFix(%q) = %q, want %q", in, got, want)
		}
	}
}

const pod = `apiVersion: v1
kind: Pod
metadata:
  name: api
spec:
  containers:
  - name: api
    image: example/api:1
    env:
ENV
    resources:
      limits:
        cpu: 500m
        memory: 512Mi
  restartPolicy: Always
`

func runOn(t *testing.T, content string, args ...string) (int, string, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "in.yaml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := run(append(args, p), strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}

func codesFor(t *testing.T, env string, args ...string) []string {
	t.Helper()
	code, out, errs := runOn(t, strings.Replace(pod, "ENV", env, 1), append([]string{"--format", "json"}, args...)...)
	if code == 2 {
		t.Fatalf("exit 2: %s", errs)
	}
	var rep report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	var codes []string
	for _, f := range rep.Findings {
		codes = append(codes, f.Severity+" "+f.Code)
	}
	return codes
}

func TestRules(t *testing.T) {
	cases := []struct {
		name, env string
		want      string
	}{
		{"healthy", "    - name: GOMEMLIMIT\n      value: 460MiB\n    - name: GOGC\n      value: \"50\"", ""},
		{"k8s unit", "    - name: GOMEMLIMIT\n      value: 512Mi", "ERROR gomemlimit-malformed"},
		{"decimal", "    - name: GOMEMLIMIT\n      value: 1.5GiB", "ERROR gomemlimit-malformed"},
		{"plus", "    - name: GOMEMLIMIT\n      value: +460MiB", "ERROR gomemlimit-malformed"},
		{"no unit", "    - name: GOMEMLIMIT\n      value: \"460\"", "ERROR gomemlimit-tiny"},
		{"above", "    - name: GOMEMLIMIT\n      value: 1GiB", "WARN gomemlimit-above-limit"},
		{"equal", "    - name: GOMEMLIMIT\n      value: 512MiB", "WARN gomemlimit-no-headroom"},
		{"downward exact", "    - name: GOMEMLIMIT\n      valueFrom:\n        resourceFieldRef:\n          resource: limits.memory", "WARN gomemlimit-no-headroom"},
		{"downward Mi", "    - name: GOMEMLIMIT\n      valueFrom:\n        resourceFieldRef:\n          resource: limits.memory\n          divisor: 1Mi", "ERROR gomemlimit-tiny"},
		{"gogc percent", "    - name: GOGC\n      value: 50%", "ERROR gogc-ignored"},
		{"gogc off alone", "    - name: GOGC\n      value: \"off\"", "ERROR gogc-off-no-limit"},
		{"gogc negative alone", "    - name: GOGC\n      value: \"-1\"", "ERROR gogc-off-no-limit"},
		{"gogc off with limit", "    - name: GOGC\n      value: \"off\"\n    - name: GOMEMLIMIT\n      value: 460MiB", ""},
		{"gogc zero", "    - name: GOGC\n      value: \"0\"", "WARN gogc-zero"},
		{"gogc off above limit", "    - name: GOGC\n      value: \"off\"\n    - name: GOMEMLIMIT\n      value: 1GiB", "ERROR gogc-off-no-limit, WARN gomemlimit-above-limit"},
		{"gogc OFF", "    - name: GOGC\n      value: \"OFF\"", "ERROR gogc-ignored"},
		{"maxprocs decimal", "    - name: GOMAXPROCS\n      value: \"1.5\"", "ERROR gomaxprocs-ignored"},
		{"maxprocs zero", "    - name: GOMAXPROCS\n      value: \"0\"", "ERROR gomaxprocs-ignored"},
		{"maxprocs high", "    - name: GOMAXPROCS\n      value: \"8\"", "WARN gomaxprocs-above-cpu-limit"},
		{"maxprocs two", "    - name: GOMAXPROCS\n      value: \"2\"", ""},
		{"maxprocs millis", "    - name: GOMAXPROCS\n      valueFrom:\n        resourceFieldRef:\n          resource: limits.cpu\n          divisor: 1m", "WARN gomaxprocs-above-cpu-limit"},
		{"maxprocs downward", "    - name: GOMAXPROCS\n      valueFrom:\n        resourceFieldRef:\n          resource: limits.cpu", ""},
		{"unquoted number", "    - name: GOGC\n      value: 50", "ERROR env-not-string"},
		{"secret", "    - name: GOMEMLIMIT\n      valueFrom:\n        secretKeyRef:\n          name: s\n          key: k", "note not-checked"},
		{"expanded", "    - name: LIMIT\n      value: 512Mi\n    - name: GOMEMLIMIT\n      value: $(LIMIT)", "ERROR gomemlimit-malformed"},
		{"later wins", "    - name: GOMEMLIMIT\n      value: 512Mi\n    - name: GOMEMLIMIT\n      value: 460MiB", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := strings.Join(codesFor(t, c.env), ", ")
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestGoFlag(t *testing.T) {
	env := "    - name: TZ\n      value: UTC"
	if got := codesFor(t, env); len(got) != 0 {
		t.Errorf("without --go: %v", got)
	}
	if got := strings.Join(codesFor(t, env, "--go", "api"), ","); got != "WARN go-without-gomemlimit" {
		t.Errorf("with --go: %q", got)
	}
	code, _, errs := runOn(t, strings.Replace(pod, "ENV", env, 1), "--go", "nope")
	if code != 2 || !strings.Contains(errs, "nope") {
		t.Errorf("--go with a missing container: exit %d, %s", code, errs)
	}
}

func TestConfigMapRefs(t *testing.T) {
	cm := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: rt\ndata:\n  GOMEMLIMIT: 512Mi\n---\n"
	env := "    - name: GOMEMLIMIT\n      valueFrom:\n        configMapKeyRef:\n          name: rt\n          key: GOMEMLIMIT"
	code, out, _ := runOn(t, cm+strings.Replace(pod, "ENV", env, 1))
	if code != 1 || !strings.Contains(out, "gomemlimit-malformed") {
		t.Errorf("configMapKeyRef: exit %d\n%s", code, out)
	}
	envFrom := strings.Replace(strings.Replace(pod, "ENV", "    - name: TZ\n      value: UTC", 1), "    env:\n", "    envFrom:\n    - configMapRef:\n        name: rt\n    env:\n", 1)
	code, out, _ = runOn(t, cm+envFrom)
	if code != 1 || !strings.Contains(out, "gomemlimit-malformed") {
		t.Errorf("envFrom: exit %d\n%s", code, out)
	}
}

func TestFailOn(t *testing.T) {
	warn := strings.Replace(pod, "ENV", "    - name: GOMEMLIMIT\n      value: 1GiB", 1)
	for flag, want := range map[string]int{"error": 0, "warn": 1, "never": 0} {
		if code, _, _ := runOn(t, warn, "--fail-on", flag); code != want {
			t.Errorf("--fail-on %s: exit %d, want %d", flag, code, want)
		}
	}
	bad := strings.Replace(pod, "ENV", "    - name: GOMEMLIMIT\n      value: 512Mi", 1)
	for flag, want := range map[string]int{"error": 1, "warn": 1, "never": 0} {
		if code, _, _ := runOn(t, bad, "--fail-on", flag); code != want {
			t.Errorf("--fail-on %s: exit %d, want %d", flag, code, want)
		}
	}
}

// Every input golimits can't trust exits 2 and prints nothing on stdout,
// whatever --fail-on says.
func TestUntrusted(t *testing.T) {
	good := strings.Replace(pod, "ENV", "    - name: GOMEMLIMIT\n      value: 460MiB", 1)
	cut := good[:strings.Index(good, "    resources:")]
	cases := map[string]string{
		"empty":            "",
		"blank":            "\n\n  \n",
		"garbage":          "\x7fELF\x02\x01\x01 not yaml at all {{{\n",
		"binary":           "abc\x00def\n",
		"plain text":       "hello world\n",
		"list":             "- a\n- b\n",
		"helm error":       "Error: INSTALLATION FAILED: template: app/templates/deployment.yaml:12:20: executing \"app/templates/deployment.yaml\" at <.Values.x>: nil pointer evaluating interface {}.y\n",
		"go build failure": "# example.com/app\n./main.go:5:2: undefined: foo\n",
		"kubectl error":    "error: error parsing deploy.yaml: error converting YAML to JSON: yaml: line 7: did not find expected key\n",
		"no newline":       strings.TrimSuffix(good, "\n"),
		"cut mid-line":     good[:len(good)-20],
		"cut in container": cut,
		"cut at key":       good[:strings.Index(good, "      limits:")+len("      limits:\n")],
		"no containers":    "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: x\nspec:\n  template:\n    spec:\n      restartPolicy: Always\n",
		"no pod spec":      "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: x\nspec:\n  replicas: 1\n",
		"no image":         "apiVersion: v1\nkind: Pod\nmetadata:\n  name: x\nspec:\n  containers:\n  - name: a\n  restartPolicy: Always\n",
		"no name":          "apiVersion: v1\nkind: Pod\nspec:\n  containers:\n  - name: a\n    image: b\n",
		"only a service":   "apiVersion: v1\nkind: Service\nmetadata:\n  name: x\nspec:\n  ports:\n  - port: 80\n",
		"helm template":    "apiVersion: v1\nkind: Pod\nmetadata:\n  name: {{ .Release.Name }}\n",
		"truncated json":   "{\"apiVersion\": \"v1\", \"kind\": \"Pod\", \"metadata\": {\"name\": \"x\"},\n",
		"bad yaml":         "apiVersion: v1\nkind: Pod\n  metadata: x\n bad: [\n",
	}
	for name, content := range cases {
		for _, fl := range [][]string{nil, {"--fail-on", "warn"}, {"--fail-on", "never"}, {"--format", "json"}} {
			code, out, errs := runOn(t, content, fl...)
			if code != 2 || out != "" || !strings.Contains(errs, "can't trust the input") {
				t.Errorf("%s %v: exit %d, stdout %q, stderr %q", name, fl, code, out, errs)
			}
		}
	}
	if code, out, _ := runOn(t, cut+"---\n"); code != 0 || !strings.Contains(out, "No problems") {
		t.Errorf("a file ending with --- should be trusted: exit %d\n%s", code, out)
	}
}

func TestDirectories(t *testing.T) {
	dir := t.TempDir()
	var out, errb bytes.Buffer
	if code := run([]string{dir}, nil, &out, &errb); code != 2 {
		t.Errorf("empty dir: exit %d", code)
	}
	os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte("name: x\n"), 0o644)
	if code := run([]string{dir}, nil, &out, &errb); code != 2 || !strings.Contains(errb.String(), "helm template") {
		t.Errorf("chart dir: exit %d, %s", code, errb.String())
	}
}
