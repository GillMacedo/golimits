package tests

import (
	"os"
	"strings"
	"testing"
)

// testdata/review holds the inputs from an adversarial review that found
// wrong results in an early version: YAML merge keys, a 1k divisor, optional
// ConfigMap keys, GOGC=off with GOMEMLIMIT above the limit, cut Lists, an
// indented --- in a block scalar, a cut custom kind, $(VAR) from an envFrom
// Secret, whitespace in a quantity, a byte order mark and CRLF line endings.
// expected.txt lists each file's finding codes, or "refused" for exit 2.
func TestReviewCases(t *testing.T) {
	data, err := os.ReadFile("testdata/review/expected.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		file, want, _ := strings.Cut(line, " ")
		r := runTool(t, nil, "--format", "json", "--fail-on", "never", "testdata/review/"+file)
		if want == "refused" {
			if r.code != 2 || r.stdout != "" {
				t.Errorf("%s: exit %d, want 2\n%s", file, r.code, r.stdout)
			}
			continue
		}
		if r.code != 0 {
			t.Fatalf("%s: exit %d: %s", file, r.code, r.stderr)
		}
		var rep jsonReport
		mustJSON(t, r.stdout, &rep)
		var got []string
		for _, f := range rep.Findings {
			got = append(got, f.Code)
		}
		if strings.Join(got, " ") != want {
			t.Errorf("%s: got %q, want %q", file, strings.Join(got, " "), want)
		}
	}
}
