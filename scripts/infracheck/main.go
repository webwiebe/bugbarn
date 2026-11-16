// Command infracheck renders every kustomize overlay and asserts on the
// rendered output.
//
// deploy/k8s/{testing,staging,production} were never built by CI. A broken
// overlay, a container with no resource limits, an image pinned to a floating
// tag or a secretKeyRef naming a Secret nobody applies all reach the cluster
// before anything notices. Rendering is the cheap floor that catches the whole
// class at review time.
//
// Rendering is mandatory: if kustomize (or `kubectl kustomize`) is missing, or
// an overlay fails to build, this exits 2. "Did not run" must never read as
// "clean".
//
// Findings are counted against a committed baseline in scripts/infra-baseline.txt.
// A new finding fails, and a baselined finding that is fixed fails as STALE until
// the baseline is refreshed with -update. -report-only prints without blocking.
package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// overlays are rendered in this order. Every one of them is deployed by a
// pipeline in .woodpecker.
var overlays = []string{"testing", "staging", "production"}

// externalSecrets are applied outside kustomize by the deploy workflows
// (SOPS-decrypted app secret and SMTP secret). A reference to one of these is
// not a finding.
var externalSecrets = map[string]bool{
	"bugbarn-secrets": true,
	"smtp-secret":     true,
}

type finding struct {
	Overlay string
	Check   string
	Object  string
}

func (f finding) line() string {
	return f.Overlay + "\t" + f.Check + "\t" + f.Object
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("infracheck", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "repository root")
	baselinePath := flags.String("baseline", "", "baseline file (default <root>/scripts/infra-baseline.txt)")
	update := flags.Bool("update", false, "rewrite the baseline from the current render")
	reportOnly := flags.Bool("report-only", false, "print findings but always exit 0")
	renderDir := flags.String("rendered", "", "read pre-rendered <overlay>.yaml from this directory instead of running kustomize (tests)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *baselinePath == "" {
		*baselinePath = filepath.Join(*root, "scripts", "infra-baseline.txt")
	}

	found, err := renderAndAssert(*root, *renderDir)
	if err != nil {
		say(stderr, "infracheck: %v\n", err)
		return 2
	}
	if *update {
		if err := writeBaseline(*baselinePath, found); err != nil {
			say(stderr, "infracheck: %v\n", err)
			return 2
		}
		say(stdout, "infracheck: baseline written with %d finding(s) -> %s\n", len(found), *baselinePath)
		return 0
	}
	baseline, err := readBaseline(*baselinePath)
	if err != nil {
		say(stderr, "infracheck: %v\ncreate one with: go run ./scripts/infracheck -update\n", err)
		return 2
	}
	if report(stdout, found, baseline) || *reportOnly {
		return 0
	}
	return 1
}

// renderAndAssert renders every overlay and returns the sorted, deduplicated
// findings. An overlay that does not render, parses to nothing or is not YAML is
// an error: "did not run" must never read as "clean".
func renderAndAssert(root, renderDir string) ([]finding, error) {
	var found []finding
	for _, overlay := range overlays {
		var rendered []byte
		var err error
		if renderDir != "" {
			rendered, err = os.ReadFile(filepath.Join(renderDir, overlay+".yaml"))
		} else {
			rendered, err = render(filepath.Join(root, "deploy", "k8s", overlay))
		}
		if err != nil {
			return nil, fmt.Errorf("overlay %s did not render: %w", overlay, err)
		}
		docs, err := parse(rendered)
		if err != nil {
			return nil, fmt.Errorf("overlay %s rendered unparseable YAML: %w", overlay, err)
		}
		if len(docs) == 0 {
			return nil, fmt.Errorf("overlay %s rendered nothing", overlay)
		}
		found = append(found, assert(overlay, docs)...)
	}
	sort.Slice(found, func(i, j int) bool { return found[i].line() < found[j].line() })
	return dedupe(found), nil
}

// report prints the comparison with the baseline and reports whether it matches
// exactly (no new finding, no stale baseline entry).
func report(stdout io.Writer, found []finding, baseline map[string]bool) bool {
	current := map[string]bool{}
	for _, f := range found {
		current[f.line()] = true
	}
	var added []finding
	for _, f := range found {
		if !baseline[f.line()] {
			added = append(added, f)
		}
	}
	var stale []string
	for k := range baseline {
		if !current[k] {
			stale = append(stale, k)
		}
	}
	sort.Strings(stale)

	say(stdout, "infracheck: %d overlay(s) rendered, %d finding(s) (baseline %d)\n",
		len(overlays), len(found), len(baseline))
	for _, f := range added {
		say(stdout, "  NEW   [%s] %s: %s\n", f.Overlay, f.Check, f.Object)
	}
	for _, k := range stale {
		say(stdout, "  STALE %s — fixed, remove it from the baseline\n", strings.ReplaceAll(k, "\t", " "))
	}
	if len(added) == 0 && len(stale) == 0 {
		say(stdout, "  matches the baseline exactly\n")
		return true
	}
	say(stdout, "  refresh with: go run ./scripts/infracheck -update\n")
	return false
}

// say writes a progress line. A failed write to stdout or stderr has nowhere
// better to go, so it is dropped.
func say(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

// dedupe drops repeated findings: a container with nine env vars from one missing
// Secret is one finding.
func dedupe(sorted []finding) []finding {
	var out []finding
	for i, f := range sorted {
		if i > 0 && f == sorted[i-1] {
			continue
		}
		out = append(out, f)
	}
	return out
}

// render builds one overlay. kustomize is preferred; `kubectl kustomize` is the
// fallback. Neither being present is a hard error.
func render(dir string) ([]byte, error) {
	var cmd *exec.Cmd
	switch {
	case hasBinary("kustomize"):
		cmd = exec.Command("kustomize", "build", dir)
	case hasBinary("kubectl"):
		cmd = exec.Command("kubectl", "kustomize", dir)
	default:
		return nil, errors.New("neither kustomize nor kubectl is on PATH")
	}
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %v: %s", cmd.Path, err, strings.TrimSpace(errBuf.String()))
	}
	return out.Bytes(), nil
}

func hasBinary(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// doc is one rendered manifest. It is deliberately an alias rather than a
// named map type: yaml.v3 propagates a NAMED target type to nested mappings, so
// decoding into `type doc map[string]any` makes every nested map a `doc` too
// and every `.(map[string]any)` assertion below silently fails.
type doc = map[string]any

func parse(b []byte) ([]doc, error) {
	var out []doc
	dec := yaml.NewDecoder(bytes.NewReader(b))
	for {
		var d map[string]any
		err := dec.Decode(&d)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(d) == 0 {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

var workloadKinds = map[string]bool{"Deployment": true, "StatefulSet": true, "DaemonSet": true, "CronJob": true, "Job": true}

func assert(overlay string, docs []doc) []finding {
	var out []finding
	add := func(check, object string) {
		out = append(out, finding{Overlay: overlay, Check: check, Object: object})
	}

	declared := map[string]bool{}
	for _, d := range docs {
		if str(d, "kind") == "Secret" {
			declared[name(d)] = true
		}
	}

	for _, d := range docs {
		kind := str(d, "kind")
		if !workloadKinds[kind] {
			continue
		}
		obj := kind + "/" + name(d)
		spec := podSpec(d)
		if spec == nil {
			add("no-pod-spec", obj)
			continue
		}
		for _, c := range containers(spec) {
			assertContainer(kind, obj+":"+str(c, "name"), c, declared, add)
		}
	}
	return out
}

// assertContainer runs the per-container checks.
func assertContainer(kind, ref string, c map[string]any, declared map[string]bool, add func(check, object string)) {
	if tag := imageTag(str(c, "image")); tag == "" || tag == "latest" {
		add("floating-image-tag", ref)
	}
	res, _ := c["resources"].(map[string]any)
	if !hasKey(res, "requests") {
		add("no-resource-requests", ref)
	}
	if !hasKey(res, "limits") {
		add("no-resource-limits", ref)
	}
	if kind == "Deployment" {
		if !hasKey(c, "readinessProbe") {
			add("no-readiness-probe", ref)
		}
		if !hasKey(c, "livenessProbe") {
			add("no-liveness-probe", ref)
		}
	}
	for _, s := range secretRefs(c) {
		if !declared[s] && !externalSecrets[s] {
			add("undeclared-secret-ref", ref+"->"+s)
		}
	}
}

// --- rendered-YAML accessors ------------------------------------------------

func str(d map[string]any, key string) string {
	s, _ := d[key].(string)
	return s
}

func name(d map[string]any) string {
	meta, _ := d["metadata"].(map[string]any)
	if meta == nil {
		return "<unnamed>"
	}
	return str(meta, "name")
}

func hasKey(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	v, ok := m[key]
	return ok && v != nil
}

// podSpec digs out the PodSpec for the workload kinds we assert on.
func podSpec(d doc) map[string]any {
	spec, _ := d["spec"].(map[string]any)
	if spec == nil {
		return nil
	}
	if jt, ok := spec["jobTemplate"].(map[string]any); ok { // CronJob
		spec, _ = jt["spec"].(map[string]any)
		if spec == nil {
			return nil
		}
	}
	tpl, ok := spec["template"].(map[string]any)
	if !ok {
		return nil
	}
	ps, _ := tpl["spec"].(map[string]any)
	return ps
}

func containers(podSpec map[string]any) []map[string]any {
	var out []map[string]any
	for _, key := range []string{"initContainers", "containers"} {
		list, _ := podSpec[key].([]any)
		for _, item := range list {
			if c, ok := item.(map[string]any); ok {
				out = append(out, c)
			}
		}
	}
	return out
}

func imageTag(image string) string {
	if image == "" {
		return ""
	}
	if i := strings.LastIndex(image, "@"); i >= 0 {
		return image[i+1:] // digest-pinned
	}
	i := strings.LastIndex(image, ":")
	if i < 0 || strings.Contains(image[i+1:], "/") {
		return ""
	}
	return image[i+1:]
}

func secretRefs(container map[string]any) []string {
	var out []string
	envs, _ := container["env"].([]any)
	for _, item := range envs {
		e, ok := item.(map[string]any)
		if !ok {
			continue
		}
		vf, _ := e["valueFrom"].(map[string]any)
		if vf == nil {
			continue
		}
		if skr, ok := vf["secretKeyRef"].(map[string]any); ok {
			out = append(out, str(skr, "name"))
		}
	}
	froms, _ := container["envFrom"].([]any)
	for _, item := range froms {
		f, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if sr, ok := f["secretRef"].(map[string]any); ok {
			out = append(out, str(sr, "name"))
		}
	}
	return out
}

// --- baseline ---------------------------------------------------------------

func writeBaseline(path string, fs []finding) error {
	var b strings.Builder
	b.WriteString("# infracheck baseline — assertions the rendered kustomize overlays do not yet meet.\n")
	b.WriteString("# Generated by: go run ./scripts/infracheck -update\n")
	b.WriteString("# Columns: overlay <TAB> check <TAB> object.\n")
	b.WriteString("# Entries may only be removed. A finding listed here that is fixed makes the\n")
	b.WriteString("# check fail as STALE until the baseline is refreshed.\n")
	for _, f := range fs {
		b.WriteString(f.line() + "\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func readBaseline(path string) (map[string]bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("baseline not readable: %w", err)
	}
	defer func() { _ = f.Close() }()
	out := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Count(line, "\t") != 2 {
			return nil, fmt.Errorf("malformed baseline line: %q", line)
		}
		out[line] = true
	}
	return out, sc.Err()
}
