package tests

import (
	"reflect"
	"testing"
)

// The files in testdata/charts are real helm template output from public
// charts; testdata/charts/README.md says which chart, version and values.
func TestCharts(t *testing.T) {
	cases := []struct {
		file string
		args []string
		want []string
	}{
		{"traefik-healthy.yaml", nil, nil},
		{"otel-healthy.yaml", nil, nil},
		{"certmanager-default.yaml", nil, nil},
		{"certmanager-default.yaml", []string{"--go", "*"}, nil},
		{"certmanager-limits.yaml", nil, nil},
		{"certmanager-limits.yaml", []string{"--go", "*"}, []string{"WARN go-without-gomemlimit cm-cert-manager/container cert-manager-controller"}},
		{"traefik-bad.yaml", nil, []string{
			"ERROR gomemlimit-malformed edge-traefik/container edge-traefik",
			"ERROR gogc-ignored edge-traefik/container edge-traefik",
		}},
		{"traefik-cpu-divisor.yaml", nil, []string{"WARN gomaxprocs-above-cpu-limit edge-traefik/container edge-traefik"}},
		{"otel-gogc-off.yaml", nil, []string{"ERROR gogc-off-no-limit col-opentelemetry-collector/container opentelemetry-collector"}},
		{"otel-divisor.yaml", nil, []string{"ERROR gomemlimit-tiny col-opentelemetry-collector-agent/container opentelemetry-collector"}},
	}
	for _, c := range cases {
		rep, _ := runJSON(t, append(c.args, "testdata/charts/"+c.file)...)
		if got := codes(rep); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %v:\n got %q\nwant %q", c.file, c.args, got, c.want)
		}
		if rep.Workloads == 0 {
			t.Errorf("%s: no workloads read", c.file)
		}
	}
}

func TestHealthyChartsExitZero(t *testing.T) {
	for _, f := range []string{"traefik-healthy.yaml", "otel-healthy.yaml"} {
		for _, fail := range []string{"error", "warn"} {
			r := runTool(t, nil, "--fail-on", fail, "testdata/charts/"+f)
			if r.code != 0 || r.stderr != "" {
				t.Errorf("%s --fail-on %s: exit %d, %s", f, fail, r.code, r.stderr)
			}
		}
	}
}
