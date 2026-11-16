package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A workload that satisfies every assertion.
const cleanDeployment = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: bugbarn
spec:
  template:
    spec:
      containers:
      - name: bugbarn
        image: ghcr.io/wiebe-xyz/bugbarn/service:abc123
        readinessProbe:
          httpGet: {path: /healthz, port: 8080}
        livenessProbe:
          httpGet: {path: /healthz, port: 8080}
        resources:
          requests: {cpu: 100m, memory: 128Mi}
          limits: {cpu: "1", memory: 512Mi}
        env:
        - name: BUGBARN_ADMIN_PASSWORD
          valueFrom:
            secretKeyRef:
              name: bugbarn-secrets
              key: admin-password
`

// Same workload with the liveness probe removed.
var missingProbe = strings.Replace(cleanDeployment, `        livenessProbe:
          httpGet: {path: /healthz, port: 8080}
`, "", 1)

// renderDir writes the same manifest as every overlay, since the assertions run
// per overlay and the test only cares about the ratchet semantics.
func renderDir(t *testing.T, manifest string) string {
	t.Helper()
	dir := t.TempDir()
	for _, overlay := range overlays {
		path := filepath.Join(dir, overlay+".yaml")
		if err := os.WriteFile(path, []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func baselineFile(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "infra-baseline.txt")
	body := "# test baseline\n" + strings.Join(lines, "\n")
	if len(lines) > 0 {
		body += "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func runCheck(t *testing.T, rendered, baseline string, extra ...string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	args := append([]string{"-rendered", rendered, "-baseline", baseline}, extra...)
	return run(args, &out, &out), out.String()
}

// allOverlays expands one finding line into the per-overlay lines the check
// emits, because every overlay renders the same fixture.
func allOverlays(check, object string) []string {
	var out []string
	for _, overlay := range overlays {
		out = append(out, fmt.Sprintf("%s\t%s\t%s", overlay, check, object))
	}
	return out
}

func TestViolationPresentFails(t *testing.T) {
	code, out := runCheck(t, renderDir(t, missingProbe), baselineFile(t))
	if code != 1 {
		t.Fatalf("want exit 1, got %d\n%s", code, out)
	}
	if !strings.Contains(out, "NEW   [testing] no-liveness-probe") {
		t.Fatalf("want the finding named, got:\n%s", out)
	}
}

func TestViolationBaselinedPasses(t *testing.T) {
	bl := baselineFile(t, allOverlays("no-liveness-probe", "Deployment/bugbarn:bugbarn")...)
	code, out := runCheck(t, renderDir(t, missingProbe), bl)
	if code != 0 {
		t.Fatalf("want exit 0, got %d\n%s", code, out)
	}
}

func TestBaselineBeatableFails(t *testing.T) {
	// The probe was added but the baseline still excuses its absence.
	bl := baselineFile(t, allOverlays("no-liveness-probe", "Deployment/bugbarn:bugbarn")...)
	code, out := runCheck(t, renderDir(t, cleanDeployment), bl)
	if code != 1 {
		t.Fatalf("want exit 1 for a stale baseline, got %d\n%s", code, out)
	}
	if !strings.Contains(out, "STALE") || !strings.Contains(out, "-update") {
		t.Fatalf("want STALE and the refresh command, got:\n%s", out)
	}
}

func TestCleanTreePasses(t *testing.T) {
	code, out := runCheck(t, renderDir(t, cleanDeployment), baselineFile(t))
	if code != 0 {
		t.Fatalf("want exit 0, got %d\n%s", code, out)
	}
}

func TestReportOnlyNeverBlocks(t *testing.T) {
	code, out := runCheck(t, renderDir(t, missingProbe), baselineFile(t), "-report-only")
	if code != 0 {
		t.Fatalf("report-only mode must not block, got %d\n%s", code, out)
	}
	if !strings.Contains(out, "NEW") {
		t.Fatalf("report-only mode must still report, got:\n%s", out)
	}
}

func TestUnrenderableOverlayIsAHardError(t *testing.T) {
	// "Did not run" must never be indistinguishable from "clean".
	dir := t.TempDir()
	code, out := runCheck(t, dir, baselineFile(t))
	if code != 2 {
		t.Fatalf("a missing render must exit 2, got %d\n%s", code, out)
	}
}

func TestEmptyRenderIsAHardError(t *testing.T) {
	dir := t.TempDir()
	for _, overlay := range overlays {
		if err := os.WriteFile(filepath.Join(dir, overlay+".yaml"), []byte("---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	code, out := runCheck(t, dir, baselineFile(t))
	if code != 2 {
		t.Fatalf("an overlay that renders nothing must exit 2, got %d\n%s", code, out)
	}
}

func TestAssertions(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
		want     string
	}{
		{"floating tag", strings.Replace(cleanDeployment,
			"image: ghcr.io/wiebe-xyz/bugbarn/service:abc123",
			"image: ghcr.io/wiebe-xyz/bugbarn/service:latest", 1), "floating-image-tag"},
		{"untagged image", strings.Replace(cleanDeployment,
			"image: ghcr.io/wiebe-xyz/bugbarn/service:abc123",
			"image: ghcr.io/wiebe-xyz/bugbarn/service", 1), "floating-image-tag"},
		{"no requests", strings.Replace(cleanDeployment,
			"          requests: {cpu: 100m, memory: 128Mi}\n", "", 1), "no-resource-requests"},
		{"no limits", strings.Replace(cleanDeployment,
			`          limits: {cpu: "1", memory: 512Mi}`+"\n", "", 1), "no-resource-limits"},
		{"no readiness", strings.Replace(cleanDeployment,
			"        readinessProbe:\n          httpGet: {path: /healthz, port: 8080}\n", "", 1), "no-readiness-probe"},
		{"undeclared secret", strings.Replace(cleanDeployment,
			"              name: bugbarn-secrets", "              name: nobody-applies-this", 1), "undeclared-secret-ref"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, out := runCheck(t, renderDir(t, tc.manifest), baselineFile(t))
			if code != 1 {
				t.Fatalf("want exit 1, got %d\n%s", code, out)
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("want %q in the output, got:\n%s", tc.want, out)
			}
		})
	}
}

func TestDigestPinnedImageIsNotFloating(t *testing.T) {
	m := strings.Replace(cleanDeployment,
		"image: ghcr.io/wiebe-xyz/bugbarn/service:abc123",
		"image: ghcr.io/wiebe-xyz/bugbarn/service@sha256:deadbeef", 1)
	if code, out := runCheck(t, renderDir(t, m), baselineFile(t)); code != 0 {
		t.Fatalf("a digest-pinned image is not floating; got %d\n%s", code, out)
	}
}

func TestSecretDeclaredInTheRenderIsNotAFinding(t *testing.T) {
	m := strings.Replace(cleanDeployment, "              name: bugbarn-secrets",
		"              name: in-tree-secret", 1) + `---
apiVersion: v1
kind: Secret
metadata:
  name: in-tree-secret
`
	if code, out := runCheck(t, renderDir(t, m), baselineFile(t)); code != 0 {
		t.Fatalf("a Secret in the render satisfies the reference; got %d\n%s", code, out)
	}
}

func TestDedupeDropsRepeats(t *testing.T) {
	a := finding{Overlay: "testing", Check: "undeclared-secret-ref", Object: "Deployment/x:c->s"}
	b := finding{Overlay: "testing", Check: "no-resource-limits", Object: "Deployment/x:c"}
	got := dedupe([]finding{a, a, a, b})
	if len(got) != 2 || got[0] != a || got[1] != b {
		t.Fatalf("dedupe = %v", got)
	}
}

func TestUpdateWritesTheBaseline(t *testing.T) {
	bl := filepath.Join(t.TempDir(), "baseline.txt")
	var out bytes.Buffer
	code := run([]string{"-rendered", renderDir(t, missingProbe), "-baseline", bl, "-update"}, &out, &out)
	if code != 0 {
		t.Fatalf("want exit 0, got %d\n%s", code, out.String())
	}
	body, err := os.ReadFile(bl)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "testing\tno-liveness-probe\tDeployment/bugbarn:bugbarn") {
		t.Fatalf("baseline lacks the finding:\n%s", body)
	}
	// The file it wrote is a baseline the check accepts.
	if code, out := runCheck(t, renderDir(t, missingProbe), bl); code != 0 {
		t.Fatalf("a fresh baseline must pass, got %d\n%s", code, out)
	}
}

func TestMissingBaselineIsAHardError(t *testing.T) {
	code, out := runCheck(t, renderDir(t, cleanDeployment), filepath.Join(t.TempDir(), "none.txt"))
	if code != 2 || !strings.Contains(out, "-update") {
		t.Fatalf("want exit 2 naming -update, got %d\n%s", code, out)
	}
}

func TestMalformedBaselineIsAHardError(t *testing.T) {
	code, out := runCheck(t, renderDir(t, cleanDeployment), baselineFile(t, "not three columns"))
	if code != 2 {
		t.Fatalf("want exit 2, got %d\n%s", code, out)
	}
}

// A stub kustomize on PATH proves render() runs the binary per overlay and passes
// its output through, and that a failing build is a hard error.
func stubKustomize(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "kustomize"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestRendersWithKustomize(t *testing.T) {
	manifest := filepath.Join(t.TempDir(), "m.yaml")
	if err := os.WriteFile(manifest, []byte(cleanDeployment), 0o644); err != nil {
		t.Fatal(err)
	}
	stubKustomize(t, "/bin/cat "+manifest)
	var out bytes.Buffer
	if code := run([]string{"-baseline", baselineFile(t)}, &out, &out); code != 0 {
		t.Fatalf("want exit 0, got %d\n%s", code, out.String())
	}
}

func TestFailingKustomizeIsAHardError(t *testing.T) {
	stubKustomize(t, "echo broken overlay >&2; exit 1")
	var out bytes.Buffer
	if code := run([]string{"-baseline", baselineFile(t)}, &out, &out); code != 2 {
		t.Fatalf("want exit 2, got %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "broken overlay") {
		t.Fatalf("the build error must be shown, got:\n%s", out.String())
	}
}

func TestNoRendererIsAHardError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	var out bytes.Buffer
	if code := run([]string{"-baseline", baselineFile(t)}, &out, &out); code != 2 {
		t.Fatalf("want exit 2, got %d\n%s", code, out.String())
	}
}

func TestBadFlagIsAnError(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"-nope"}, &out, &out); code != 2 {
		t.Fatalf("want exit 2, got %d", code)
	}
}

const cronJobWithEnvFrom = `apiVersion: batch/v1
kind: CronJob
metadata:
  name: snapshot
spec:
  jobTemplate:
    spec:
      template:
        spec:
          containers:
          - name: snap
            image: ghcr.io/wiebe-xyz/bugbarn/service:abc123
            resources:
              requests: {cpu: 10m}
              limits: {memory: 64Mi}
            envFrom:
            - secretRef: {name: nobody-applies-this}
`

func TestCronJobIsCheckedThroughItsJobTemplate(t *testing.T) {
	code, out := runCheck(t, renderDir(t, cronJobWithEnvFrom), baselineFile(t))
	if code != 1 || !strings.Contains(out, "undeclared-secret-ref: CronJob/snapshot:snap->nobody-applies-this") {
		t.Fatalf("want the envFrom secret flagged, got %d\n%s", code, out)
	}
}

const jobWithoutPodSpec = `apiVersion: batch/v1
kind: Job
metadata:
  name: broken
spec: {}
`

func TestWorkloadWithoutPodSpecIsFlagged(t *testing.T) {
	_, out := runCheck(t, renderDir(t, jobWithoutPodSpec), baselineFile(t))
	if !strings.Contains(out, "no-pod-spec: Job/broken") {
		t.Fatalf("want no-pod-spec, got:\n%s", out)
	}
}

func TestUnnamedObjectAndMissingKeys(t *testing.T) {
	if got := name(map[string]any{}); got != "<unnamed>" {
		t.Fatalf("name = %q", got)
	}
	if hasKey(nil, "x") || hasKey(map[string]any{"x": nil}, "x") {
		t.Fatal("a nil map or nil value has no key")
	}
}
