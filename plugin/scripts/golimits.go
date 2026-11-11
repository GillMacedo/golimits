// Command golimits checks the Go runtime settings (GOMEMLIMIT, GOGC and
// GOMAXPROCS) in rendered Kubernetes manifests against each container's
// resource limits. It reads files only and never connects to a cluster.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const version = "0.1.0"

const (
	sevNote = iota
	sevWarn
	sevError
)

var sevNames = map[int]string{sevNote: "note", sevWarn: "WARN", sevError: "ERROR"}

type finding struct {
	sev       int
	Severity  string `json:"severity"`
	Code      string `json:"code"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	Container string `json:"container"`
	Variable  string `json:"variable,omitempty"`
	Value     string `json:"value,omitempty"`
	Message   string `json:"message"`
	Fix       string `json:"fix,omitempty"`
	File      string `json:"file"`
}

type object struct {
	File      string
	Kind      string
	Namespace string
	Name      string
	raw       map[string]any
	endPath   []string // non-nil on a file's last object unless the file ends with --- or ...
}

// inputError means the input can't be trusted; nothing is reported.
type inputError struct{ msg string }

func (e *inputError) Error() string { return e.msg }

func untrusted(format string, a ...any) error {
	return &inputError{fmt.Sprintf(format, a...)}
}

var goVars = []string{"GOMEMLIMIT", "GOGC", "GOMAXPROCS"}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

type options struct {
	format   string
	failOn   string
	headroom float64
	goNames  map[string]bool
	goAll    bool
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("golimits", flag.ContinueOnError)
	fl.SetOutput(stderr)
	format := fl.String("format", "text", "output format: text or json")
	failOn := fl.String("fail-on", "error", "exit 1 when a finding is at or above this level: error, warn or never")
	headroom := fl.Float64("headroom", 5, "warn when GOMEMLIMIT leaves less than this percent of the memory limit")
	goList := fl.String("go", "", "comma-separated container names that run Go, or * for all; these also get a warning when they have a memory limit and no GOMEMLIMIT")
	showVersion := fl.Bool("version", false, "print the version")
	fl.Usage = func() {
		fmt.Fprintf(stderr, "usage: golimits [flags] FILE|DIR|- ...\n\nChecks GOMEMLIMIT, GOGC and GOMAXPROCS in rendered Kubernetes manifests against container limits.\nExit 0: nothing at or above --fail-on. Exit 1: findings. Exit 2: input can't be trusted or bad usage.\n\n")
		fl.PrintDefaults()
	}
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, "golimits", version)
		return 0
	}
	if *format != "text" && *format != "json" {
		fmt.Fprintf(stderr, "golimits: --format must be text or json\n")
		return 2
	}
	if *failOn != "error" && *failOn != "warn" && *failOn != "never" {
		fmt.Fprintf(stderr, "golimits: --fail-on must be error, warn or never\n")
		return 2
	}
	if *headroom < 0 || *headroom >= 100 {
		fmt.Fprintf(stderr, "golimits: --headroom must be from 0 to below 100\n")
		return 2
	}
	if fl.NArg() == 0 {
		fl.Usage()
		return 2
	}
	opt := options{format: *format, failOn: *failOn, headroom: *headroom, goNames: map[string]bool{}}
	for _, n := range strings.Split(*goList, ",") {
		n = strings.TrimSpace(n)
		if n == "*" {
			opt.goAll = true
		} else if n != "" {
			opt.goNames[n] = true
		}
	}

	objs, err := load(fl.Args(), stdin)
	if err != nil {
		return refuse(stderr, err)
	}
	rep, err := check(objs, opt)
	if err != nil {
		return refuse(stderr, err)
	}
	if opt.format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		enc.Encode(rep)
	} else {
		printText(stdout, rep)
	}
	limit := map[string]int{"error": sevError, "warn": sevWarn, "never": 99}[opt.failOn]
	for _, f := range rep.Findings {
		if f.sev >= limit {
			return 1
		}
	}
	return 0
}

func refuse(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "golimits: can't trust the input, nothing was checked: %v\n", err)
	return 2
}

// ---- loading ----

func load(paths []string, stdin io.Reader) ([]*object, error) {
	var objs []*object
	for _, p := range paths {
		if p == "-" {
			data, err := io.ReadAll(stdin)
			if err != nil {
				return nil, untrusted("reading stdin: %v", err)
			}
			o, err := parseFile("stdin", data)
			if err != nil {
				return nil, err
			}
			objs = append(objs, o...)
			continue
		}
		st, err := os.Stat(p)
		if err != nil {
			return nil, untrusted("%v", err)
		}
		if !st.IsDir() {
			data, err := os.ReadFile(p)
			if err != nil {
				return nil, untrusted("%v", err)
			}
			o, err := parseFile(p, data)
			if err != nil {
				return nil, err
			}
			objs = append(objs, o...)
			continue
		}
		var files []string
		err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			name := d.Name()
			if d.Type()&fs.ModeSymlink != 0 {
				return untrusted("%s is a symbolic link, which golimits doesn't follow; pass its target instead", path)
			}
			if d.IsDir() {
				if path != p && strings.HasPrefix(name, ".") {
					return filepath.SkipDir
				}
				return nil
			}
			switch name {
			case "Chart.yaml":
				return untrusted("%s is a Helm chart; render it first with helm template and pass the output", filepath.Dir(path))
			case "kustomization.yaml", "kustomization.yml", "Kustomization":
				return untrusted("%s is a kustomization; render it first with kustomize build and pass the output", filepath.Dir(path))
			}
			switch strings.ToLower(filepath.Ext(name)) {
			case ".yaml", ".yml", ".json":
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			var ie *inputError
			if errors.As(err, &ie) {
				return nil, err
			}
			return nil, untrusted("%v", err)
		}
		if len(files) == 0 {
			return nil, untrusted("%s has no .yaml, .yml or .json files", p)
		}
		sort.Strings(files)
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				return nil, untrusted("%v", err)
			}
			o, err := parseFile(f, data)
			if err != nil {
				return nil, err
			}
			objs = append(objs, o...)
		}
	}
	return objs, nil
}

func parseFile(name string, data []byte) ([]*object, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, untrusted("%s is empty", name)
	}
	if !bytes.HasSuffix(data, []byte("\n")) {
		return nil, untrusted("%s doesn't end with a line break, so it may be cut off mid-line; helm template and kubectl always end their output with one", name)
	}
	if bytes.Contains(data, []byte("\x00")) {
		return nil, untrusted("%s contains NUL bytes; it isn't a text manifest", name)
	}
	if bytes.Contains(data, []byte("{{")) {
		return nil, untrusted("%s contains {{ template markers; render it first with helm template", name)
	}
	if last := lastLine(data); strings.HasSuffix(last, ":") || last == "-" {
		return nil, untrusted("%s ends with %q, a key with no value, so it looks cut off", name, last)
	}
	// Each document is decoded twice: into plain maps for the values, which
	// keeps YAML merge keys the way kubectl does, and into ordered maps only
	// to find where the document ends.
	dec := NewDecoder(bytes.NewReader(data))
	ord := NewDecoder(bytes.NewReader(data))
	var out []*object
	var lastObjs []*object
	var lastPath []string
	var lastList bool
	for n := 1; ; n++ {
		var doc any
		err := dec.Decode(&doc)
		var odoc MapSlice
		oerr := ord.Decode(&odoc)
		if oerr != nil && strings.Contains(oerr.Error(), "cannot unmarshal") {
			oerr = nil // not a mapping; the plain decode reports that
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, untrusted("%s, document %d: not valid YAML or JSON (%v); is it the output of a failed command?", name, n, err)
		}
		if oerr != nil {
			return nil, untrusted("%s, document %d: not valid YAML or JSON (%v)", name, n, oerr)
		}
		if doc == nil {
			continue
		}
		m, ok := toStringMap(doc).(map[string]any)
		if !ok {
			return nil, untrusted("%s, document %d is not a Kubernetes object; is it the output of a failed command?", name, n)
		}
		objs, err := toObjects(name, n, m)
		if err != nil {
			return nil, err
		}
		out = append(out, objs...)
		_, lastList = m["items"].([]any)
		lastObjs, lastPath = objs, endPath(odoc)
	}
	if len(out) == 0 {
		return nil, untrusted("%s has no Kubernetes objects", name)
	}
	if !explicitEnd(data) && len(lastObjs) > 0 {
		last := lastObjs[len(lastObjs)-1]
		switch {
		case lastList && len(lastPath) >= 2 && lastPath[0] == "items":
			// the List ends inside its last item
			if idx, err := strconv.Atoi(lastPath[1]); err == nil && idx == len(lastObjs)-1 {
				last.endPath = lastPath[2:]
			}
		case !lastList:
			last.endPath = lastPath
		}
		if last.endPath == nil {
			last.endPath = []string{}
		}
	}
	return out, nil
}

func toStringMap(v any) any {
	switch t := v.(type) {
	case map[any]any:
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[fmt.Sprint(k)] = toStringMap(val)
		}
		return m
	case []any:
		for i := range t {
			t[i] = toStringMap(t[i])
		}
		return t
	}
	return v
}

// explicitEnd reports whether the file ends with a --- or ... line, which
// says the last document is complete.
func explicitEnd(data []byte) bool {
	lines := strings.Split(string(data), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		t := strings.TrimRight(lines[i], " \t\r")
		if t == "" {
			continue
		}
		return t == "---" || t == "..."
	}
	return false
}

// endPath is the path of the last value in document order.
func endPath(v any) []string {
	var p []string
	for {
		switch t := v.(type) {
		case MapSlice:
			if len(t) == 0 {
				return p
			}
			last := t[len(t)-1]
			p = append(p, fmt.Sprint(last.Key))
			v = last.Value
		case []any:
			if len(t) == 0 {
				return p
			}
			p = append(p, strconv.Itoa(len(t)-1))
			v = t[len(t)-1]
		default:
			return p
		}
	}
}

func lastLine(data []byte) string {
	lines := strings.Split(string(data), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if t == "" || strings.HasPrefix(t, "#") || t == "---" {
			continue
		}
		return t
	}
	return ""
}

func toObjects(file string, n int, m map[string]any) ([]*object, error) {
	kind, _ := m["kind"].(string)
	apiVersion, _ := m["apiVersion"].(string)
	if kind == "" || apiVersion == "" {
		return nil, untrusted("%s, document %d has no apiVersion or kind, so it isn't a Kubernetes object; is it the output of a failed command?", file, n)
	}
	if items, ok := m["items"].([]any); ok && strings.HasSuffix(kind, "List") {
		var out []*object
		for i, it := range items {
			im, ok := it.(map[string]any)
			if !ok {
				return nil, untrusted("%s, document %d, item %d is not an object", file, n, i+1)
			}
			o, err := toObjects(file, n, im)
			if err != nil {
				return nil, err
			}
			out = append(out, o...)
		}
		return out, nil
	}
	meta, _ := m["metadata"].(map[string]any)
	name, _ := meta["name"].(string)
	if name == "" {
		name, _ = meta["generateName"].(string)
	}
	if name == "" {
		return nil, untrusted("%s, document %d: %s has no metadata.name, so it looks incomplete", file, n, kind)
	}
	ns, _ := meta["namespace"].(string)
	return []*object{{File: file, Kind: kind, Namespace: ns, Name: name, raw: m}}, nil
}

func path(m map[string]any, keys ...string) any {
	var cur any = m
	for _, k := range keys {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[k]
	}
	return cur
}

var workloadKinds = map[string][]string{
	"Pod":                   {"spec"},
	"Deployment":            {"spec", "template", "spec"},
	"StatefulSet":           {"spec", "template", "spec"},
	"DaemonSet":             {"spec", "template", "spec"},
	"ReplicaSet":            {"spec", "template", "spec"},
	"ReplicationController": {"spec", "template", "spec"},
	"Job":                   {"spec", "template", "spec"},
	"CronJob":               {"spec", "jobTemplate", "spec", "template", "spec"},
	"PodTemplate":           {"template", "spec"},
}

// podSpec finds the pod spec of a workload. Kinds it doesn't know, such as
// an Argo Rollout, count when they have spec.template.spec.containers.
func podSpec(o *object) (map[string]any, bool, error) {
	p, known := workloadKinds[o.Kind]
	if !known {
		p = []string{"spec", "template", "spec"}
	}
	spec, _ := path(o.raw, p...).(map[string]any)
	if !known && o.endPath != nil && path(o.raw, "spec", "template") != nil {
		if _, ok := spec["containers"].([]any); !ok {
			return nil, false, untrusted("%s: %s %s is the file's last object and has spec.template but no containers in it, so it looks cut off. If the file is complete, end it with a line that says ---", o.File, o.Kind, o.Name)
		}
	}
	if spec == nil {
		if known {
			return nil, false, untrusted("%s: %s %s has no pod spec at %s, so it looks cut off", o.File, o.Kind, o.Name, strings.Join(p, "."))
		}
		return nil, false, nil
	}
	if _, ok := spec["containers"].([]any); !ok && !known {
		return nil, false, nil
	}
	if e := o.endPath; len(e) > len(p)+1 && strings.Join(e[:len(p)], ".") == strings.Join(p, ".") &&
		(e[len(p)] == "containers" || e[len(p)] == "initContainers") {
		return nil, false, untrusted("%s ends inside a container of %s %s, which is what a file cut on a line break looks like: env or resources after the cut would be missing. If the file is complete, end it with a line that says --- (for example: { helm template ...; echo ---; } | golimits -)", o.File, o.Kind, o.Name)
	}
	return spec, true, nil
}

// ---- checking ----

type report struct {
	Version    string     `json:"version"`
	Objects    int        `json:"objects"`
	Workloads  int        `json:"workloads"`
	Containers int        `json:"containers"`
	Checked    int        `json:"checked"`
	Findings   []*finding `json:"findings"`
}

type container struct {
	obj    *object
	pod    map[string]any
	name   string
	label  string
	raw    map[string]any
	limits map[string]*big.Rat
	reqs   map[string]*big.Rat
	badQty []*finding
}

type envVal struct {
	known bool
	value string
	from  string // how the value was set, for messages
	why   string // why it isn't known
	ref   *resRef
	bad   bool // not a string in the manifest
}

type resRef struct {
	resource string
	divisor  string
	nodeWide bool // limit not set, so the kubelet uses node allocatable
}

func check(objs []*object, opt options) (*report, error) {
	rep := &report{Version: version, Objects: len(objs)}
	configMaps := map[string]map[string]string{}
	for _, o := range objs {
		if o.Kind == "ConfigMap" {
			data := map[string]string{}
			if d, ok := o.raw["data"].(map[string]any); ok {
				for k, v := range d {
					if s, ok := v.(string); ok {
						data[k] = s
					} else {
						data[k] = fmt.Sprint(v)
					}
				}
			}
			configMaps[o.Namespace+"/"+o.Name] = data
		}
	}
	seen := map[string]bool{}
	for _, o := range objs {
		spec, ok, err := podSpec(o)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		rep.Workloads++
		ctrs, err := containersOf(o, spec)
		if err != nil {
			return nil, err
		}
		if spec["resources"] != nil {
			rep.Findings = append(rep.Findings, &finding{sev: sevNote, Code: "pod-level-resources", Kind: o.Kind, Namespace: o.Namespace, Name: o.Name, File: o.File,
				Message: "sets pod-level resources; golimits compares only with each container's own limits"})
		}
		for _, c := range ctrs {
			rep.Containers++
			seen[c.name] = true
			fs, checked := checkContainer(c, ctrs, configMaps, opt)
			if checked {
				rep.Checked++
			}
			rep.Findings = append(rep.Findings, fs...)
		}
	}
	if rep.Workloads == 0 {
		return nil, untrusted("found %d objects and no workloads (Pod, Deployment, StatefulSet, DaemonSet, Job, CronJob or anything with spec.template.spec.containers), so there was nothing to check", len(objs))
	}
	var missing []string
	for n := range opt.goNames {
		if !seen[n] {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, untrusted("--go names containers that aren't in the input: %s", strings.Join(missing, ", "))
	}
	for _, f := range rep.Findings {
		f.Severity = sevNames[f.sev]
	}
	sort.SliceStable(rep.Findings, func(i, j int) bool {
		a, b := rep.Findings[i], rep.Findings[j]
		if a.sev != b.sev {
			return a.sev > b.sev
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return false
	})
	if rep.Findings == nil {
		rep.Findings = []*finding{}
	}
	return rep, nil
}

func containersOf(o *object, spec map[string]any) ([]*container, error) {
	var out []*container
	for _, field := range []string{"initContainers", "containers"} {
		list, _ := spec[field].([]any)
		if field == "containers" && len(list) == 0 {
			return nil, untrusted("%s: %s %s has no containers, so it looks cut off", o.File, o.Kind, o.Name)
		}
		for i, it := range list {
			m, ok := it.(map[string]any)
			if !ok {
				return nil, untrusted("%s: %s %s: %s[%d] is not an object", o.File, o.Kind, o.Name, field, i)
			}
			name, _ := m["name"].(string)
			image, _ := m["image"].(string)
			if name == "" || image == "" {
				return nil, untrusted("%s: %s %s: %s[%d] has no name or no image, so it looks cut off", o.File, o.Kind, o.Name, field, i)
			}
			label := "container " + name
			if field == "initContainers" {
				label = "init container " + name
			}
			out = append(out, &container{obj: o, pod: spec, name: name, label: label, raw: m})
		}
	}
	return out, nil
}

func (c *container) resources() {
	if c.limits != nil {
		return
	}
	c.limits, c.reqs = map[string]*big.Rat{}, map[string]*big.Rat{}
	for _, part := range []string{"limits", "requests"} {
		m, _ := path(c.raw, "resources", part).(map[string]any)
		for k, v := range m {
			s := fmt.Sprint(v)
			q, err := parseQuantity(strings.TrimSpace(s))
			if err != nil || q.Sign() < 0 {
				c.badQty = append(c.badQty, &finding{sev: sevError, Code: "bad-quantity", Value: s,
					Message: fmt.Sprintf("resources.%s.%s is %q, which is not a valid Kubernetes quantity, so the API server rejects this manifest", part, k, s)})
				continue
			}
			if part == "limits" {
				c.limits[k] = q
			} else {
				c.reqs[k] = q
			}
		}
	}
}

func findContainer(all []*container, name string) *container {
	for _, c := range all {
		if c.name == name {
			return c
		}
	}
	return nil
}

// resolveEnv computes each variable's value the way the kubelet does:
// envFrom first, then env in order, with $(VAR) expansion.
func resolveEnv(c *container, all []*container, cms map[string]map[string]string) (map[string]*envVal, []string) {
	vars := map[string]*envVal{}
	var bulk []string
	ns := c.obj.Namespace
	for _, it := range asList(c.raw["envFrom"]) {
		prefix, _ := it["prefix"].(string)
		if ref, ok := it["configMapRef"].(map[string]any); ok {
			name, _ := ref["name"].(string)
			if data, ok := cms[ns+"/"+name]; ok {
				for k, v := range data {
					vars[prefix+k] = &envVal{known: true, value: v, from: "envFrom ConfigMap " + name}
				}
			} else {
				src := "ConfigMap " + name + " (not in the input)"
				bulk = append(bulk, src)
				shadow(vars, src)
			}
		}
		if ref, ok := it["secretRef"].(map[string]any); ok {
			name, _ := ref["name"].(string)
			src := "Secret " + name
			bulk = append(bulk, src)
			shadow(vars, src)
		}
	}
	for _, it := range asList(c.raw["env"]) {
		name, _ := it["name"].(string)
		if name == "" {
			continue
		}
		if raw, has := it["value"]; has && raw != nil {
			s, isStr := raw.(string)
			if !isStr {
				vars[name] = &envVal{known: true, value: fmt.Sprint(raw), from: "env", bad: true}
				continue
			}
			v, ok, why := expand(s, vars, bulk)
			vars[name] = &envVal{known: ok, value: v, from: "env", why: why}
			continue
		}
		vf, _ := it["valueFrom"].(map[string]any)
		switch {
		case vf == nil:
			vars[name] = &envVal{known: true, value: "", from: "env"}
		case vf["configMapKeyRef"] != nil:
			ref, _ := vf["configMapKeyRef"].(map[string]any)
			cm, _ := ref["name"].(string)
			key, _ := ref["key"].(string)
			optional, _ := ref["optional"].(bool)
			data, ok := cms[ns+"/"+cm]
			if !ok {
				vars[name] = &envVal{why: fmt.Sprintf("comes from ConfigMap %s, which is not in the input", cm)}
			} else if v, ok := data[key]; ok {
				vars[name] = &envVal{known: true, value: v, from: fmt.Sprintf("ConfigMap %s key %s", cm, key)}
			} else if optional {
				// the kubelet skips the entry, so an earlier value stays
			} else {
				vars[name] = &envVal{why: fmt.Sprintf("comes from key %s of ConfigMap %s, which doesn't have it", key, cm)}
			}
		case vf["secretKeyRef"] != nil:
			ref, _ := vf["secretKeyRef"].(map[string]any)
			s, _ := ref["name"].(string)
			vars[name] = &envVal{why: "comes from Secret " + s}
		case vf["resourceFieldRef"] != nil:
			ref, _ := vf["resourceFieldRef"].(map[string]any)
			vars[name] = resourceValue(c, all, ref)
		default:
			vars[name] = &envVal{why: "comes from a fieldRef or another source golimits can't read"}
		}
	}
	return vars, bulk
}

// shadow marks values set so far by envFrom as unknown, because a later
// envFrom source that golimits can't read may override them.
func shadow(vars map[string]*envVal, src string) {
	for k, v := range vars {
		if v.known {
			vars[k] = &envVal{why: fmt.Sprintf("comes from %s, and envFrom %s, which comes later, may override it", v.from, src)}
		}
	}
}

func asList(v any) []map[string]any {
	l, _ := v.([]any)
	var out []map[string]any
	for _, it := range l {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// expand does Kubernetes $(VAR) expansion; $$ is a literal $.
func expand(s string, vars map[string]*envVal, unknownSources []string) (string, bool, string) {
	if !strings.Contains(s, "$") {
		return s, true, ""
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '$' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		switch s[i+1] {
		case '$':
			b.WriteByte('$')
			i++
		case '(':
			end := strings.IndexByte(s[i+2:], ')')
			if end < 0 {
				b.WriteString(s[i:])
				return b.String(), true, ""
			}
			ref := s[i+2 : i+2+end]
			if v, ok := vars[ref]; ok {
				if !v.known {
					return "", false, "uses $(" + ref + "), which " + v.why
				}
				b.WriteString(v.value)
			} else if len(unknownSources) > 0 {
				return "", false, "uses $(" + ref + "), which envFrom " + strings.Join(unknownSources, ", ") + " may set"
			} else {
				b.WriteString(s[i : i+3+end])
			}
			i += 2 + end
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String(), true, ""
}

func resourceValue(c *container, all []*container, ref map[string]any) *envVal {
	res, _ := ref["resource"].(string)
	target := c
	if cn, _ := ref["containerName"].(string); cn != "" {
		if t := findContainer(all, cn); t != nil {
			target = t
		} else {
			return &envVal{why: fmt.Sprintf("uses resourceFieldRef on container %s, which isn't in the pod", cn)}
		}
	}
	target.resources()
	divisor := "1"
	if d, ok := ref["divisor"]; ok && d != nil {
		divisor = strings.TrimSpace(fmt.Sprint(d))
	}
	div, err := parseQuantity(divisor)
	if err != nil || div.Sign() <= 0 {
		return &envVal{why: fmt.Sprintf("uses resourceFieldRef with divisor %q, which isn't a valid quantity", divisor)}
	}
	from := "resourceFieldRef " + res
	if divisor != "1" {
		from += " with divisor " + divisor
	}
	rr := &resRef{resource: res, divisor: divisor}
	part, name, ok := strings.Cut(res, ".")
	if !ok || (part != "limits" && part != "requests") {
		return &envVal{why: "uses resourceFieldRef " + res + ", which golimits can't evaluate"}
	}
	isCPU := name == "cpu"
	var q *big.Rat
	if part == "limits" {
		q = target.limits[name]
		if q == nil {
			rr.nodeWide = true
			return &envVal{why: "comes from " + res + ", and the container has no such limit, so the kubelet uses the node's allocatable amount", ref: rr, from: from}
		}
	} else {
		q = target.reqs[name]
		if q == nil {
			q = target.limits[name]
		}
		if q == nil {
			q = new(big.Rat)
		}
	}
	return &envVal{known: true, value: strconv.FormatInt(downwardValue(q, div, isCPU), 10), from: from, ref: rr}
}

func checkContainer(c *container, all []*container, cms map[string]map[string]string, opt options) ([]*finding, bool) {
	var out []*finding
	add := func(f *finding) {
		f.Kind, f.Namespace, f.Name, f.Container, f.File = c.obj.Kind, c.obj.Namespace, c.obj.Name, c.label, c.obj.File
		out = append(out, f)
	}
	c.resources()
	vars, bulk := resolveEnv(c, all, cms)
	isGo := opt.goAll || opt.goNames[c.name]
	has := false
	for _, n := range goVars {
		if vars[n] != nil {
			has = true
		}
	}
	if !has && !isGo {
		return out, false
	}
	for _, f := range c.badQty {
		add(f)
	}

	memLimit := c.limits["memory"]
	var L int64 = -1
	if memLimit != nil {
		L = ceilInt(memLimit)
	}
	unknown := func(name string, v *envVal) {
		add(&finding{sev: sevNote, Code: "not-checked", Variable: name,
			Message: fmt.Sprintf("%s %s, so its value wasn't checked", name, v.why)})
	}
	notString := func(name string, v *envVal) bool {
		if v.bad {
			add(&finding{sev: sevError, Code: "env-not-string", Variable: name, Value: v.value,
				Message: fmt.Sprintf("%s is set to %s without quotes, so it isn't a string and the API server rejects the manifest", name, v.value),
				Fix:     fmt.Sprintf("quote it: value: \"%s\"", v.value)})
			return true
		}
		return false
	}

	// GOMEMLIMIT
	memSet := false // a usable limit is in effect
	var memN int64
	memUnknown := false
	if v := vars["GOMEMLIMIT"]; v != nil {
		switch {
		case notString("GOMEMLIMIT", v):
			memUnknown = true
		case !v.known:
			memUnknown = true
			if v.ref != nil && v.ref.nodeWide && v.ref.resource == "limits.memory" {
				memSet = true
				add(&finding{sev: sevWarn, Code: "gomemlimit-node-memory", Variable: "GOMEMLIMIT", Value: v.from,
					Message: "GOMEMLIMIT comes from resourceFieldRef limits.memory, but the container has no memory limit, so the kubelet sets it to the node's allocatable memory and the Go garbage collector gets no useful limit",
					Fix:     "set resources.limits.memory on the container"})
			} else {
				unknown("GOMEMLIMIT", v)
			}
		case v.value == "" || v.value == "off":
		default:
			n, ok := parseByteCount(v.value)
			if ok && strings.HasPrefix(v.value, "+") {
				add(&finding{sev: sevError, Code: "gomemlimit-malformed", Variable: "GOMEMLIMIT", Value: v.value,
					Message: fmt.Sprintf("GOMEMLIMIT=%q starts with +, which newer Go versions accept and older ones such as Go 1.22 reject with \"fatal error: malformed GOMEMLIMIT\" at startup", v.value),
					Fix:     "write " + strings.TrimPrefix(v.value, "+")})
				memSet = true
				break
			}
			if !ok {
				add(&finding{sev: sevError, Code: "gomemlimit-malformed", Variable: "GOMEMLIMIT", Value: v.value,
					Message: fmt.Sprintf("GOMEMLIMIT=%q makes every Go program exit at startup with \"fatal error: malformed GOMEMLIMIT\". Go accepts a whole number of bytes with an optional B, KiB, MiB, GiB or TiB suffix, and not Kubernetes suffixes such as Mi or M", v.value),
					Fix:     memFix(v.value)})
				memUnknown = true
				break
			}
			memN = n
			memSet = n < math.MaxInt64
			tiny := n < 1<<20
			if L > 0 && n < L/256 {
				tiny = true
			}
			viaRef := v.ref != nil && v.ref.divisor != "1"
			switch {
			case tiny:
				msg := fmt.Sprintf("GOMEMLIMIT=%s is %s", v.value, humanBytes(n))
				if L > 0 {
					msg += fmt.Sprintf(", %s of the %s memory limit", pct(n, L), humanBytes(L))
				}
				msg += ". Go reads a number with no suffix as bytes, so the garbage collector runs back to back and can take about half the CPU"
				fix := "write the unit, for example " + formatBytes(n<<20) + " if MiB was meant"
				if viaRef {
					msg += fmt.Sprintf(". It comes from %s, which divides the limit before Go reads it as bytes", v.from)
					fix = "remove the divisor so the kubelet writes bytes"
					if L > 0 {
						fix = fmt.Sprintf("remove the divisor so the kubelet writes bytes, or set GOMEMLIMIT to %s", formatBytes(target90(L)))
					}
				}
				add(&finding{sev: sevError, Code: "gomemlimit-tiny", Variable: "GOMEMLIMIT", Value: v.value, Message: msg, Fix: fix})
			case L > 0 && n > L:
				add(&finding{sev: sevWarn, Code: "gomemlimit-above-limit", Variable: "GOMEMLIMIT", Value: v.value,
					Message: fmt.Sprintf("GOMEMLIMIT=%s (%s) is above the %s memory limit, so the kernel OOM-kills the container before the garbage collector works to stay under GOMEMLIMIT", v.value, humanBytes(n), humanBytes(L)),
					Fix:     fmt.Sprintf("set GOMEMLIMIT to about 90%% of the limit: %s", formatBytes(target90(L)))})
			case L > 0 && float64(n) > float64(L)*(1-opt.headroom/100):
				msg := fmt.Sprintf("GOMEMLIMIT=%s is %s of the %s memory limit, leaving less than %s%% for memory the Go runtime doesn't track, such as cgo allocations. The Go GC guide suggests 5-10%% headroom", v.value, pct(n, L), humanBytes(L), trimFloat(opt.headroom))
				if v.ref != nil {
					msg = fmt.Sprintf("GOMEMLIMIT comes from %s, which is the whole %s memory limit, leaving no room for memory the Go runtime doesn't track, such as cgo allocations. The Go GC guide suggests 5-10%% headroom", v.from, humanBytes(L))
				}
				add(&finding{sev: sevWarn, Code: "gomemlimit-no-headroom", Variable: "GOMEMLIMIT", Value: v.value, Message: msg,
					Fix: fmt.Sprintf("set GOMEMLIMIT to about 90%% of the limit: %s", formatBytes(target90(L)))})
			case L < 0:
				add(&finding{sev: sevNote, Code: "no-memory-limit", Variable: "GOMEMLIMIT", Value: v.value,
					Message: fmt.Sprintf("GOMEMLIMIT=%s, and the container has no memory limit to compare it with", v.value)})
			}
		}
	} else if len(bulk) > 0 {
		memUnknown = true
	}

	// GOGC
	if v := vars["GOGC"]; v != nil && !notString("GOGC", v) {
		if !v.known {
			unknown("GOGC", v)
		} else if v.value != "" {
			off := v.value == "off"
			if !off {
				n, err := strconv.ParseInt(v.value, 10, 32)
				if err == nil && strings.HasPrefix(v.value, "+") {
					add(&finding{sev: sevError, Code: "gogc-ignored", Variable: "GOGC", Value: v.value,
						Message: fmt.Sprintf("GOGC=%q starts with +, which older Go versions such as Go 1.22 silently ignore, using 100", v.value),
						Fix:     "write GOGC=" + strings.TrimPrefix(v.value, "+")})
				} else if err != nil {
					add(&finding{sev: sevError, Code: "gogc-ignored", Variable: "GOGC", Value: v.value,
						Message: fmt.Sprintf("GOGC=%q is not a whole number or \"off\", so the Go runtime silently ignores it and uses 100", v.value),
						Fix:     gogcFix(v.value)})
				} else if n < 0 {
					off = true
				} else if n == 0 {
					add(&finding{sev: sevWarn, Code: "gogc-zero", Variable: "GOGC", Value: v.value,
						Message: "GOGC=0 makes the garbage collector run continuously, which costs a lot of CPU",
						Fix:     "use a positive number such as 100, or GOGC=off with GOMEMLIMIT"})
				}
			}
			if off {
				switch {
				case memSet && L > 0 && memN > L:
					add(&finding{sev: sevError, Code: "gogc-off-no-limit", Variable: "GOGC", Value: v.value,
						Message: fmt.Sprintf("GOGC=%s turns the garbage collector off until the heap reaches GOMEMLIMIT, and GOMEMLIMIT is above the %s memory limit, so the container is OOM-killed before the collector runs", v.value, humanBytes(L)),
						Fix:     gcOffFix(L)})
				case memSet:
				case memUnknown:
					add(&finding{sev: sevNote, Code: "not-checked", Variable: "GOGC",
						Message: fmt.Sprintf("GOGC=%s turns the garbage collector off unless GOMEMLIMIT is set, and GOMEMLIMIT's value couldn't be read", v.value)})
				default:
					add(&finding{sev: sevError, Code: "gogc-off-no-limit", Variable: "GOGC", Value: v.value,
						Message: fmt.Sprintf("GOGC=%s with no GOMEMLIMIT turns the garbage collector off completely, so the heap grows until the container is OOM-killed", v.value),
						Fix:     gcOffFix(L)})
				}
			}
		}
	}

	// GOMAXPROCS
	if v := vars["GOMAXPROCS"]; v != nil && !notString("GOMAXPROCS", v) {
		if !v.known {
			if !(v.ref != nil && v.ref.nodeWide) {
				unknown("GOMAXPROCS", v)
			}
		} else if v.value != "" {
			n, err := strconv.ParseInt(v.value, 10, 32)
			if err == nil && n > 0 && strings.HasPrefix(v.value, "+") {
				add(&finding{sev: sevError, Code: "gomaxprocs-ignored", Variable: "GOMAXPROCS", Value: v.value,
					Message: fmt.Sprintf("GOMAXPROCS=%q starts with +, which older Go versions such as Go 1.22 silently ignore", v.value),
					Fix:     "write GOMAXPROCS=" + strings.TrimPrefix(v.value, "+")})
			} else if err != nil || n <= 0 {
				add(&finding{sev: sevError, Code: "gomaxprocs-ignored", Variable: "GOMAXPROCS", Value: v.value,
					Message: fmt.Sprintf("GOMAXPROCS=%q is not a positive whole number, so the Go runtime silently ignores it and picks its own value", v.value),
					Fix:     procsFix(v.value)})
			} else if cpu := c.limits["cpu"]; cpu != nil {
				allowed := ceilInt(cpu)
				if allowed < 2 {
					allowed = 2
				}
				if n > allowed {
					msg := fmt.Sprintf("GOMAXPROCS=%d with a CPU limit of %s lets more threads run Go code than the CPU quota allows, so the container gets throttled", n, cpuString(cpu))
					fix := fmt.Sprintf("set GOMAXPROCS to %d or less, or remove it on Go 1.25 and newer, which reads the CPU limit itself", allowed)
					if v.ref != nil && v.ref.divisor != "1" {
						msg += fmt.Sprintf(". It comes from %s, which counts in units smaller than a CPU", v.from)
						fix = "use divisor 1 (or no divisor) so the kubelet writes whole CPUs"
					}
					add(&finding{sev: sevWarn, Code: "gomaxprocs-above-cpu-limit", Variable: "GOMAXPROCS", Value: v.value, Message: msg, Fix: fix})
				}
			}
		}
	}

	for _, f := range out {
		if v := vars[f.Variable]; v != nil && f.sev > sevNote && v.from != "" && v.from != "env" && !strings.Contains(f.Message, v.from) {
			f.Message += ". The value comes from " + v.from
		}
	}

	// Go containers without GOMEMLIMIT
	if isGo && vars["GOMEMLIMIT"] == nil {
		switch {
		case len(bulk) > 0:
			add(&finding{sev: sevNote, Code: "not-checked", Variable: "GOMEMLIMIT",
				Message: fmt.Sprintf("GOMEMLIMIT isn't in env, and envFrom %s may set it", strings.Join(bulk, ", "))})
		case L > 0:
			add(&finding{sev: sevWarn, Code: "go-without-gomemlimit", Variable: "GOMEMLIMIT",
				Message: fmt.Sprintf("has a %s memory limit and no GOMEMLIMIT, so the Go garbage collector doesn't know about the limit, unless the program sets one itself with debug.SetMemoryLimit or a library such as automemlimit", humanBytes(L)),
				Fix:     fmt.Sprintf("add env GOMEMLIMIT=%s (about 90%% of the limit)", formatBytes(target90(L)))})
		}
	}
	return out, true
}

func pct(n, of int64) string {
	if p := float64(n) * 100 / float64(of); p > 0 && p < 0.01 {
		return "under 0.01%"
	}
	return trimFloat(float64(n)*100/float64(of)) + "%"
}

func target90(L int64) int64 {
	mib := L * 9 / 10 >> 20
	if mib < 1 {
		return L * 9 / 10
	}
	return mib << 20
}

func cpuString(q *big.Rat) string {
	f, _ := q.Float64()
	if q.IsInt() {
		return trimFloat(f)
	}
	return strconv.FormatInt(ceilInt(new(big.Rat).Mul(q, big.NewRat(1000, 1))), 10) + "m"
}

func memFix(s string) string {
	try := []string{s}
	if strings.HasSuffix(s, "iB") {
		try = append(try, strings.TrimSuffix(s, "B"))
	} else if strings.HasSuffix(s, "B") || strings.HasSuffix(s, "b") {
		try = append(try, s[:len(s)-1])
	}
	for _, t := range try {
		q, err := parseQuantity(t)
		if err != nil || q.Sign() <= 0 {
			continue
		}
		n := ceilInt(q)
		if n%(1<<20) == 0 || n < 1<<20 {
			return "write " + formatBytes(n)
		}
		return fmt.Sprintf("write %dMiB (%s rounded down to whole MiB) or %d", n>>20, t, n)
	}
	if strings.EqualFold(s, "off") {
		return `write "off" in lowercase`
	}
	return "write a whole number of bytes, or a whole number with B, KiB, MiB, GiB or TiB"
}

func gogcFix(s string) string {
	if strings.EqualFold(s, "off") {
		return `write "off" in lowercase`
	}
	t := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "%"))
	if n, err := strconv.ParseInt(t, 10, 32); err == nil {
		return fmt.Sprintf("write GOGC=%d", n)
	}
	return `use a whole number such as 100, or "off"`
}

func procsFix(s string) string {
	if q, err := parseQuantity(strings.TrimSpace(s)); err == nil && q.Sign() > 0 {
		return fmt.Sprintf("write GOMAXPROCS=%d", ceilInt(q))
	}
	return "use a whole number of at least 1, or remove it on Go 1.25 and newer, which reads the CPU limit itself"
}

func gcOffFix(L int64) string {
	if L > 0 {
		return fmt.Sprintf("set GOMEMLIMIT too, about 90%% of the limit: %s", formatBytes(target90(L)))
	}
	return "set GOMEMLIMIT too, or remove GOGC=off"
}

// ---- output ----

func printText(w io.Writer, rep *report) {
	counts := map[int]int{}
	for _, f := range rep.Findings {
		counts[f.sev]++
		where := f.Kind + " "
		if f.Namespace != "" {
			where += f.Namespace + "/"
		}
		where += f.Name
		if f.Container != "" {
			where += ", " + f.Container
		}
		fmt.Fprintf(w, "%s %s: %s\n", f.Severity, f.Code, where)
		fmt.Fprintf(w, "  %s\n", f.Message)
		if f.Fix != "" {
			fmt.Fprintf(w, "  fix: %s\n", f.Fix)
		}
		fmt.Fprintf(w, "  in %s\n\n", f.File)
	}
	read := fmt.Sprintf("%d %s in %d %s", rep.Containers, plural(rep.Containers, "container", "containers"), rep.Workloads, plural(rep.Workloads, "workload", "workloads"))
	if rep.Checked == 0 {
		fmt.Fprintf(w, "Read %s. None sets GOMEMLIMIT, GOGC or GOMAXPROCS, so nothing was checked. Pass --go NAME for containers that run Go.\n", read)
		return
	}
	if counts[sevError] == 0 && counts[sevWarn] == 0 {
		fmt.Fprintf(w, "No problems in the %d %s that set Go runtime variables or were named with --go (read %s).", rep.Checked, plural(rep.Checked, "container", "containers"), read)
		if counts[sevNote] > 0 {
			fmt.Fprintf(w, " %d %s above.", counts[sevNote], plural(counts[sevNote], "note", "notes"))
		}
		fmt.Fprintln(w)
		return
	}
	fmt.Fprintf(w, "%d %s, %d %s and %d %s in %d checked %s (read %s).\n",
		counts[sevError], plural(counts[sevError], "error", "errors"),
		counts[sevWarn], plural(counts[sevWarn], "warning", "warnings"),
		counts[sevNote], plural(counts[sevNote], "note", "notes"),
		rep.Checked, plural(rep.Checked, "container", "containers"), read)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
