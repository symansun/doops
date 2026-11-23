package server

// INT-07 proved the whole path end to end for one Skill; INT-10 generalizes it to
// every builtin Skill package that actually ships scripts/inspect.sh. The run API
// below drives the real package, the real entrypoint resolved from SKILL.md
// frontmatter (never a hardcoded package list) and the real doops_shell tool
// handler. The script itself executes inside python:3.12-slim, so the host
// toolchain cannot satisfy it accidentally.
//
// The production dispatch shells out through dispatcher.ShellCommand, which resolves
// "bash" from PATH (backend/agent/internal/dispatcher/dispatcher.go:198); this test
// therefore prepends a generated bash shim that forwards the dispatched command into
// docker. Three modes are load-bearing negative controls and can be replayed by hand:
//
//	DOOPS_INT07_FORCE_SHELL_MODE=fail    go test ./internal/server/ -run 'BuiltinSkillE2E' -count=1
//	DOOPS_INT07_FORCE_SHELL_MODE=garbage go test ./internal/server/ -run 'BuiltinSkillE2E' -count=1
//	DOOPS_INT07_FORCE_SHELL_MODE=missing go test ./internal/server/ -run 'BuiltinSkillE2E' -count=1
//
// All three must turn the script-backed tests red; if they do not, the assertions
// below no longer prove that the script ran and its payload reached the store.
//
// python:3.12-slim ships no ps/systemctl/kubectl, so several packages degrade
// honestly to status=warning with an explicit degradation check. That is expected
// and tolerated; a skipped exec_mode, a misspelled command or a PATH miss is not.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	builtinSkillE2ESlug    = "inspect-vm-host"
	builtinSkillE2EImage   = "python:3.12-slim"
	builtinSkillE2EModeEnv = "DOOPS_INT07_FORCE_SHELL_MODE"
	builtinSkillE2ELogEnv  = "DOOPS_INT07_EXEC_LOG"
)

func builtinSkillE2EShellMode() string {
	if mode := strings.TrimSpace(os.Getenv(builtinSkillE2EModeEnv)); mode != "" {
		return mode
	}
	return "docker"
}

func builtinSkillE2ESpec(slug string) (defaultSystemSkillSpec, bool) {
	for i := range builtinSystemSkills {
		if builtinSystemSkills[i].Slug == slug {
			return builtinSystemSkills[i], true
		}
	}
	return defaultSystemSkillSpec{}, false
}

// builtinSkillE2EScriptSlugs discovers the script-backed packages from the embedded
// filesystem itself, so a new package that ships scripts/inspect.sh is covered
// without editing a list here (and a package that loses its script stops being
// silently skipped).
func builtinSkillE2EScriptSlugs(t *testing.T) []string {
	t.Helper()
	found := map[string]bool{}
	err := fs.WalkDir(builtinSkillPackagesFS, builtinSkillPackagesRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || d.Name() != "inspect.sh" {
			return nil
		}
		rel := strings.TrimPrefix(path, builtinSkillPackagesRoot+"/")
		parts := strings.Split(rel, "/")
		if len(parts) == 3 && parts[1] == "scripts" {
			found[parts[0]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded %s: %v", builtinSkillPackagesRoot, err)
	}
	slugs := make([]string, 0, len(found))
	for slug := range found {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	if len(slugs) == 0 {
		t.Fatalf("no builtin Skill package under %s ships scripts/inspect.sh; the discovery walk is broken", builtinSkillPackagesRoot)
	}
	return slugs
}

// builtinSkillE2ESkipReason explains, in the test log, why a declared capability
// has no executable script instead of pretending it passed.
func builtinSkillE2ESkipReason(t *testing.T, spec defaultSystemSkillSpec) string {
	t.Helper()
	skillMD, files, err := loadBuiltinSkillPackage(spec)
	if err != nil {
		t.Fatalf("load builtin package %s: %v", spec.Slug, err)
	}
	if mcp := strings.TrimSpace(skillFrontmatterValue(skillMD, "required_mcp")); mcp != "" {
		return fmt.Sprintf("no scripts/inspect.sh; execution is delegated to required MCP %q, not a shell entrypoint (%d package files)", mcp, len(files))
	}
	return fmt.Sprintf("no scripts/inspect.sh and no frontmatter entrypoint; no managed execution channel covers this package (%d package files)", len(files))
}

// builtinSkillE2ERequireDocker skips (with an explicit CI note) when this host cannot
// run containers; CI ubuntu-latest runs the docker branch, so a local skip never
// hides an uncovered path.
func builtinSkillE2ERequireDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not installed on this host; CI ubuntu-latest runs the docker branch of this test, so a local skip does not mean the path is uncovered")
	}
	if out, err := exec.Command("docker", "info", "--format", "{{.ServerVersion}}").CombinedOutput(); err != nil {
		t.Skipf("docker daemon is not reachable on this host; CI ubuntu-latest runs the docker branch of this test, so a local skip does not mean the path is uncovered: %v; %s", err, out)
	}
	if err := exec.Command("docker", "image", "inspect", builtinSkillE2EImage).Run(); err == nil {
		return
	}
	if out, err := exec.Command("docker", "pull", builtinSkillE2EImage).CombinedOutput(); err != nil {
		t.Fatalf("docker image %s is absent and cannot be pulled: %v; %s", builtinSkillE2EImage, err, out)
	}
}

// builtinSkillE2EWriteBashShim installs the PATH "bash" that forwards the production
// command (bash -c <command>) into a throwaway container. fail exits non-zero before
// docker runs; garbage answers with non-JSON text; missing deletes the packaged
// entrypoint before dispatch so the command references an absent file.
func builtinSkillE2EWriteBashShim(t *testing.T, mode, execLog string) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "doops-int10-bin-")
	if err != nil {
		t.Fatalf("create bash shim directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	script := `#!/bin/sh
# Generated by TestBuiltinSkillE2E (INT-07/INT-10): forwards the platform command into docker.
set -u
mode="$DOOPS_INT07_FORCE_SHELL_MODE"
printf 'mode=%s cmd=%s\n' "$mode" "$2" >> "$DOOPS_INT07_EXEC_LOG"
if [ "$mode" = "fail" ]; then
  echo "int07 negative control: the Skill script exits non-zero" >&2
  exit 127
fi
if [ "$mode" = "garbage" ]; then
  printf 'int07 negative control: this is not a JSON artifact\n'
  exit 0
fi
if [ "$mode" = "missing" ]; then
  rm -f "$DOOPS_AGENT_SKILLS_ROOT"/*/scripts/inspect.sh
fi
printf 'docker-run image=python:3.12-slim\n' >> "$DOOPS_INT07_EXEC_LOG"
exec docker run --rm -e DOOPS_AGENT_SKILLS_ROOT="$DOOPS_AGENT_SKILLS_ROOT" -v "$DOOPS_AGENT_SKILLS_ROOT:$DOOPS_AGENT_SKILLS_ROOT:ro" python:3.12-slim sh -c "$2"
`
	path := filepath.Join(dir, "bash")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write bash shim: %v", err)
	}
	return dir
}

// builtinSkillE2ERun drives one inspection run for the requested Skill package
// through the public API and returns the fixture, the operator token, the run, its
// single item and the shell execution log.
func builtinSkillE2ERun(t *testing.T, slug, mode string) (*executionLifecycleFixture, CreatedToken, monitorRunRecord, monitorRunItemRecord, string) {
	t.Helper()
	// /tmp, not t.TempDir(): the skills root is bind-mounted into the container and
	// Docker Desktop shares /tmp deterministically.
	root, err := os.MkdirTemp("/tmp", "doops-int10-skills-")
	if err != nil {
		t.Fatalf("create skills root: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	execLog := filepath.Join(t.TempDir(), "int10-exec.log")
	shimDir := builtinSkillE2EWriteBashShim(t, mode, execLog)

	f := monitorEngineFixture(t)
	t.Setenv("DOOPS_AGENT_SKILLS_ROOT", root)
	t.Setenv("DO_AGENT_URL", "http://127.0.0.1:1")
	t.Setenv(builtinSkillE2EModeEnv, mode)
	t.Setenv(builtinSkillE2ELogEnv, execLog)
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	f.httpTimeout = 180 * time.Second

	operator := f.user(t, "int10-e2e-op", ScopeGrant{Cluster: "dev", Instance: "local"})
	inventoryTestAgent(t, f, "local")

	body, err := json.Marshal(map[string]any{
		"targets": []map[string]string{{"cluster": "dev", "instance": "local"}},
		"skills":  []string{slug},
	})
	if err != nil {
		t.Fatalf("encode run request: %v", err)
	}
	raw := f.http(t, operator, "POST", "/v1/monitor/runs", string(body), 200)
	var resp struct {
		Run   monitorRunRecord       `json:"run"`
		Items []monitorRunItemRecord `json:"items"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode run response: %v; %s", err, raw)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected exactly one item for %s, got %d: %+v", slug, len(resp.Items), resp.Items)
	}
	if resp.Items[0].AssetID != slug {
		t.Fatalf("run item asset_id = %q, want %q", resp.Items[0].AssetID, slug)
	}
	return f, operator, resp.Run, resp.Items[0], execLog
}

// builtinSkillE2EMetrics decodes the artifact payload and returns its self-assessed
// status plus key -> value.
func builtinSkillE2EMetrics(t *testing.T, raw json.RawMessage) (string, map[string]float64) {
	t.Helper()
	if len(raw) == 0 || !json.Valid(raw) {
		t.Fatalf("artifact JSON is empty or invalid: %s", raw)
	}
	var payload struct {
		Status  string `json:"status"`
		Metrics []struct {
			Key   string  `json:"key"`
			Value float64 `json:"value"`
		} `json:"metrics"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode artifact JSON: %v", err)
	}
	if len(payload.Metrics) == 0 {
		t.Fatalf("artifact JSON carries no metrics: %s", raw)
	}
	out := map[string]float64{}
	for _, m := range payload.Metrics {
		out[m.Key] = m.Value
	}
	return payload.Status, out
}

// builtinSkillE2EMetric keeps the raw JSON value so a non-numeric metric value is
// caught by the shape assertion instead of being coerced to 0 by the decoder.
type builtinSkillE2EMetric struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

type builtinSkillE2ECheck struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type builtinSkillE2EPayload struct {
	Status  string                  `json:"status"`
	Metrics []builtinSkillE2EMetric `json:"metrics"`
	Checks  []builtinSkillE2ECheck  `json:"checks"`
}

func builtinSkillE2EDecodeArtifact(t *testing.T, raw json.RawMessage) builtinSkillE2EPayload {
	t.Helper()
	var payload builtinSkillE2EPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode artifact payload: %v; %s", err, raw)
	}
	return payload
}

// builtinSkillE2ENumericMetrics pins the metric-shape half of the structure
// contract: every declared key is pipeline-legal, unique, and its value really is
// a finite JSON number (never a string that the pipeline would silently skip).
func builtinSkillE2ENumericMetrics(t *testing.T, slug string, entries []builtinSkillE2EMetric) map[string]float64 {
	t.Helper()
	out := map[string]float64{}
	for i, m := range entries {
		key := strings.TrimSpace(m.Key)
		if !monitorMetricKeyPattern.MatchString(key) {
			t.Fatalf("%s metric %d key %q does not match the pipeline key contract %s", slug, i, m.Key, monitorMetricKeyPattern)
		}
		if _, dup := out[key]; dup {
			t.Fatalf("%s declares duplicate metric key %q", slug, key)
		}
		var value float64
		if err := json.Unmarshal(m.Value, &value); err != nil {
			t.Fatalf("%s metric %s value %s is not a JSON number: %v", slug, key, m.Value, err)
		}
		if math.IsNaN(value) || math.IsInf(value, 0) {
			t.Fatalf("%s metric %s = %v is not finite", slug, key, value)
		}
		out[key] = value
	}
	return out
}

// builtinSkillE2EStoredMetrics reads the persisted inspection_metric_points rows for
// exactly this run and skill, so the payload can be compared against what the store
// actually kept.
func builtinSkillE2EStoredMetrics(t *testing.T, f *executionLifecycleFixture, runID, slug string) map[string]float64 {
	t.Helper()
	rows, err := f.store.db.Query("SELECT metric_key, value FROM inspection_metric_points WHERE run_id=? AND skill_slug=?", runID, slug)
	if err != nil {
		t.Fatalf("query inspection_metric_points: %v", err)
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var key string
		var value float64
		if err := rows.Scan(&key, &value); err != nil {
			t.Fatalf("scan metric row: %v", err)
		}
		if !monitorMetricKeyPattern.MatchString(key) {
			t.Fatalf("persisted metric key %q violates the pipeline key contract %s", key, monitorMetricKeyPattern)
		}
		out[key] = value
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate metric rows: %v", err)
	}
	return out
}

// builtinSkillE2EVerifyArtifactFiles proves the payload reached disk, not just
// SQLite: artifact.md/artifact.json exist, and the recomputed byte count and
// sha256 match the index the database recorded.
func builtinSkillE2EVerifyArtifactFiles(t *testing.T, f *executionLifecycleFixture, runID, slug string, item monitorRunItemRecord) {
	t.Helper()
	var dir, sha string
	var bytes int64
	err := f.store.db.QueryRow("SELECT artifact_dir, artifact_bytes, artifact_sha256 FROM monitor_run_items WHERE run_id=? AND asset_id=?", runID, slug).Scan(&dir, &bytes, &sha)
	if err != nil {
		t.Fatalf("read persisted artifact index for %s/%s: %v", runID, slug, err)
	}
	if strings.TrimSpace(dir) == "" || bytes <= 0 || strings.TrimSpace(sha) == "" {
		t.Fatalf("%s persisted no artifact files (dir=%q bytes=%d sha256=%q); the payload never reached disk (item.ArtifactDir=%q)", slug, dir, bytes, sha, item.ArtifactDir)
	}
	root := f.store.monitorArtifactRoot()
	if root == "" {
		t.Fatalf("%s artifact storage is disabled for this store, so the file assertions cannot run", slug)
	}
	itemDir := filepath.Join(root, filepath.FromSlash(dir))
	read := func(name string) string {
		body, err := os.ReadFile(filepath.Join(itemDir, name))
		if err != nil {
			t.Fatalf("%s artifact file %s is missing: %v", slug, name, err)
		}
		return string(body)
	}
	markdown := read(monitorArtifactMarkdownName)
	payloadJSON := read(monitorArtifactJSONName)
	rawOutput := read(monitorArtifactRawName)
	if strings.TrimSpace(markdown) == "" {
		t.Fatalf("%s on-disk %s is empty", slug, monitorArtifactMarkdownName)
	}
	if !json.Valid([]byte(payloadJSON)) {
		t.Fatalf("%s on-disk %s is not valid JSON: %s", slug, monitorArtifactJSONName, payloadJSON)
	}
	digest := sha256.New()
	var size int64
	for _, entry := range []struct{ name, body string }{
		{monitorArtifactMarkdownName, markdown},
		{monitorArtifactJSONName, payloadJSON},
		{monitorArtifactRawName, rawOutput},
	} {
		if entry.body == "" {
			continue
		}
		fmt.Fprintf(digest, "%s\x00%d\x00", entry.name, len(entry.body))
		digest.Write([]byte(entry.body))
		size += int64(len(entry.body))
	}
	if size != bytes {
		t.Fatalf("%s artifact_bytes = %d but the files under %s total %d", slug, bytes, itemDir, size)
	}
	if got := hex.EncodeToString(digest.Sum(nil)); got != sha {
		t.Fatalf("%s artifact_sha256 = %s but recomputing over %s gives %s", slug, sha, itemDir, got)
	}
	if len(item.ArtifactJSON) > 0 && !json.Valid(item.ArtifactJSON) {
		t.Fatalf("%s returned artifact JSON is invalid: %s", slug, item.ArtifactJSON)
	}
}

// builtinSkillE2EVerifyMetricSeries checks the /v1/monitor/metrics bridge exposes
// every persisted key for this run with the value the payload declared.
func builtinSkillE2EVerifyMetricSeries(t *testing.T, f *executionLifecycleFixture, operator CreatedToken, slug, runID string, metrics map[string]float64) {
	t.Helper()
	query := url.Values{}
	query.Set("skill", slug)
	query.Set("target", "dev/local")
	query.Set("range", "1h")
	raw := f.http(t, operator, "GET", "/v1/monitor/metrics?"+query.Encode(), "", 200)
	var series struct {
		Series []struct {
			Key    string `json:"key"`
			Points []struct {
				Value float64 `json:"value"`
				RunID string  `json:"run_id"`
			} `json:"points"`
		} `json:"series"`
	}
	if err := json.Unmarshal(raw, &series); err != nil {
		t.Fatalf("decode metric series for %s: %v; %s", slug, err, raw)
	}
	exposed := map[string]bool{}
	for _, s := range series.Series {
		for _, p := range s.Points {
			if p.RunID != runID {
				continue
			}
			exposed[s.Key] = true
			if want, ok := metrics[s.Key]; ok && p.Value != want {
				t.Fatalf("%s metric series %s for run %s = %v, payload says %v", slug, s.Key, runID, p.Value, want)
			}
		}
	}
	for key := range metrics {
		if !exposed[key] {
			t.Fatalf("metric series API did not expose %s for %s run %s: %s", key, slug, runID, raw)
		}
	}
}

// builtinSkillE2EVerifyScriptPackage is the INT-10 structure-contract lock for one
// discovered script-backed package. It never asserts host numbers (cpu percent,
// node counts): it asserts that the script really ran and that the artifact,
// checks, metric typing, persistence and series bridge all hold together.
func builtinSkillE2EVerifyScriptPackage(t *testing.T, slug, mode string) {
	t.Helper()
	spec, ok := builtinSkillE2ESpec(slug)
	if !ok {
		t.Fatalf("builtin Skill package %s ships scripts/inspect.sh but is not declared in builtinSystemSkills; the discovery walk and the registry are out of sync", slug)
	}
	if strings.TrimSpace(spec.Entrypoint) == "" {
		t.Fatalf("builtin capability %s ships scripts/inspect.sh but declares no entrypoint", slug)
	}
	f, operator, run, item, execLog := builtinSkillE2ERun(t, slug, mode)

	if item.AssetID != slug || item.AssetKind != "skill" || item.Strategy != "skill:"+slug {
		t.Fatalf("run item is not bound to the discovered package: asset_id=%q kind=%q strategy=%q", item.AssetID, item.AssetKind, item.Strategy)
	}
	if item.CollectorMethod != "skill_script" {
		t.Fatalf("%s collector method = %q, want skill_script (evidence=%+v)", slug, item.CollectorMethod, item.Evidence)
	}
	if item.Evidence["synced"] != true {
		t.Fatalf("%s package was not materialized on the target Agent: %+v", slug, item.Evidence)
	}
	// Evidence["exec_mode"] is the DECLARED mode from SKILL.md frontmatter; the
	// builtin packages declare none and therefore record "auto". The dispatch
	// decision is what matters: auto with a resolvable entrypoint runs the
	// script (resolveSkillDispatchMode) and only "agent" hands the run to the
	// AI path. The shell execution log below is the proof it actually ran.
	declaredMode, _ := item.Evidence["exec_mode"].(string)
	if declaredMode == skillExecModeAgent {
		t.Fatalf("%s dispatched to the AI path (exec_mode=agent) instead of running %s (evidence=%+v)", slug, spec.Entrypoint, item.Evidence)
	}
	if declaredMode != skillExecModeAuto && declaredMode != skillExecModeScript {
		t.Fatalf("%s evidence exec_mode = %q, want auto or script (evidence=%+v)", slug, declaredMode, item.Evidence)
	}

	skillMD, files, err := loadBuiltinSkillPackage(spec)
	if err != nil {
		t.Fatalf("load builtin package %s: %v", slug, err)
	}
	entrypoint := strings.TrimSpace(skillFrontmatterValue(skillMD, "entrypoint"))
	if entrypoint == "" {
		t.Fatalf("SKILL.md of %s declares no entrypoint (%d package files)", slug, len(files))
	}
	if entrypoint != spec.Entrypoint {
		t.Errorf("entrypoint from SKILL.md frontmatter = %q, but the spec declares %q", entrypoint, spec.Entrypoint)
	}
	if got, _ := item.Evidence["entrypoint"].(string); got != entrypoint {
		t.Fatalf("item evidence entrypoint = %q, want the frontmatter entrypoint %q (evidence=%+v)", got, entrypoint, item.Evidence)
	}

	// The shim logs every dispatched command, so "the script never ran"
	// (mis-resolved exec_mode, misspelled command, PATH miss) fails right here.
	logText, err := os.ReadFile(execLog)
	if err != nil {
		t.Fatalf("read shell execution log for %s: %v", slug, err)
	}
	for _, want := range []string{"mode=" + mode, slug, entrypoint} {
		if !strings.Contains(string(logText), want) {
			t.Fatalf("shell execution log for %s does not contain %q; the script did not run: %s", slug, want, logText)
		}
	}
	if mode == "docker" && !strings.Contains(string(logText), "image="+builtinSkillE2EImage) {
		t.Fatalf("the %s script never ran inside %s; shell execution log: %s", slug, builtinSkillE2EImage, logText)
	}
	t.Logf("%s shell execution log: %s", slug, strings.ReplaceAll(strings.TrimSpace(string(logText)), "\n", " | "))

	switch item.ArtifactFormat {
	case "markdown+json", "json", "text":
	default:
		t.Fatalf("%s artifact format = %q, want markdown+json, json or text (raw output: %s)", slug, item.ArtifactFormat, item.RawOutput)
	}
	if len(item.ArtifactJSON) == 0 || !json.Valid(item.ArtifactJSON) {
		t.Fatalf("%s artifact carries no valid JSON payload (format=%q raw output: %s)", slug, item.ArtifactFormat, item.RawOutput)
	}
	payload := builtinSkillE2EDecodeArtifact(t, item.ArtifactJSON)
	normalized := normalizeArtifactStatus(payload.Status)
	if normalized == "" {
		t.Fatalf("%s payload status %q does not normalize to a verdict", slug, payload.Status)
	}
	if normalized != item.Status {
		t.Fatalf("%s item status = %q but the payload status %q normalizes to %q", slug, item.Status, payload.Status, normalized)
	}
	if item.Status != "pass" && item.Status != "warning" {
		t.Fatalf("%s status = %q, want pass or warning: a tool-less container must degrade honestly (reason=%q checks=%+v)", slug, item.Status, item.Reason, item.Checks)
	}
	if item.Reason != "" {
		t.Fatalf("%s completed successfully but carries reason %q", slug, item.Reason)
	}

	if len(payload.Checks) == 0 {
		t.Fatalf("%s artifact payload carries no checks", slug)
	}
	if len(item.Checks) != len(payload.Checks) {
		t.Fatalf("%s stored %d checks but the payload declares %d: %+v", slug, len(item.Checks), len(payload.Checks), item.Checks)
	}
	for i, c := range item.Checks {
		if strings.TrimSpace(c.ID) == "" {
			t.Errorf("%s check %d has no id: %+v", slug, i, c)
		}
		if strings.TrimSpace(c.Detail) == "" {
			t.Errorf("%s check %d (%s) has no detail", slug, i, c.ID)
		}
		switch c.Status {
		case "pass", "warning", "critical":
		default:
			t.Errorf("%s check %d (%s) status = %q, want pass, warning or critical", slug, i, c.ID, c.Status)
		}
	}

	metrics := builtinSkillE2ENumericMetrics(t, slug, payload.Metrics)
	stored := builtinSkillE2EStoredMetrics(t, f, run.ID, slug)
	if len(stored) != len(metrics) {
		t.Fatalf("%s payload declares %d numeric metrics but %d rows were persisted (payload=%v stored=%v)", slug, len(metrics), len(stored), metrics, stored)
	}
	for key, want := range metrics {
		got, ok := stored[key]
		if !ok {
			t.Fatalf("%s payload metric %s was not persisted (stored=%v)", slug, key, stored)
		}
		if got != want {
			t.Fatalf("%s persisted %s = %v, payload says %v", slug, key, got, want)
		}
	}
	if len(metrics) == 0 {
		// A degraded package may legitimately report metrics: [] (for example no
		// container runtime is installed). That is only acceptable when the payload
		// says so in a warning check; an empty metrics[] with a pass verdict would
		// hide a script that produced nothing.
		degraded := item.Status == "warning"
		for _, c := range item.Checks {
			if c.Status == "warning" && strings.TrimSpace(c.Detail) != "" {
				degraded = true
			}
		}
		if !degraded {
			t.Fatalf("%s persisted no metrics and reports no warning/degradation check: checks=%+v", slug, item.Checks)
		}
	}

	builtinSkillE2EVerifyArtifactFiles(t, f, run.ID, slug, item)
	builtinSkillE2EVerifyMetricSeries(t, f, operator, slug, run.ID, metrics)

	t.Logf("%s: status=%s format=%s metrics=%d checks=%d", slug, item.Status, item.ArtifactFormat, len(stored), len(item.Checks))
}

// TestBuiltinSkillE2E is the INT-07 end-to-end lock: one real Skill package executed
// by the real pipeline in a real container, with its payload landing on disk, in
// inspection_metric_points and on the metric series API.
func TestBuiltinSkillE2E(t *testing.T) {
	mode := builtinSkillE2EShellMode()
	if mode == "docker" {
		builtinSkillE2ERequireDocker(t)
	}
	f, operator, run, item, execLog := builtinSkillE2ERun(t, builtinSkillE2ESlug, mode)

	if item.AssetID != builtinSkillE2ESlug || item.AssetKind != "skill" || item.Strategy != "skill:"+builtinSkillE2ESlug {
		t.Fatalf("run item is not bound to the expected Skill package: asset_id=%q kind=%q strategy=%q", item.AssetID, item.AssetKind, item.Strategy)
	}
	if item.CollectorMethod != "skill_script" {
		t.Fatalf("collector method = %q, want skill_script (evidence=%+v)", item.CollectorMethod, item.Evidence)
	}
	if item.Evidence["synced"] != true {
		t.Fatalf("the Skill package was not materialized on the target Agent: %+v", item.Evidence)
	}
	spec, ok := builtinSkillE2ESpec(builtinSkillE2ESlug)
	if !ok {
		t.Fatalf("builtin capability %q is not declared in builtinSystemSkills", builtinSkillE2ESlug)
	}
	skillMD, _, err := loadBuiltinSkillPackage(spec)
	if err != nil {
		t.Fatalf("load builtin package %s: %v", builtinSkillE2ESlug, err)
	}
	entrypoint := skillFrontmatterValue(skillMD, "entrypoint")
	if entrypoint == "" {
		t.Fatalf("SKILL.md of %s declares no entrypoint", builtinSkillE2ESlug)
	}
	if entrypoint != spec.Entrypoint {
		t.Fatalf("entrypoint from SKILL.md frontmatter = %q, but the spec declares %q", entrypoint, spec.Entrypoint)
	}
	if got, _ := item.Evidence["entrypoint"].(string); got != entrypoint {
		t.Fatalf("item evidence entrypoint = %q, want the frontmatter entrypoint %q (evidence=%+v)", got, entrypoint, item.Evidence)
	}

	logText, err := os.ReadFile(execLog)
	if err != nil {
		t.Fatalf("read shell execution log: %v", err)
	}
	if !strings.Contains(string(logText), "image="+builtinSkillE2EImage) {
		t.Fatalf("the Skill script never ran inside %s; shell execution log: %s", builtinSkillE2EImage, logText)
	}
	if !strings.Contains(string(logText), entrypoint) {
		t.Fatalf("the dispatched command does not mention the frontmatter entrypoint %q; shell execution log: %s", entrypoint, logText)
	}
	t.Logf("shell execution log: %s", strings.ReplaceAll(string(logText), "\n", " | "))

	// inspect.sh prints a Markdown report plus a fenced JSON payload, so the pipeline
	// labels the artifact "markdown+json"; anything that does not carry JSON fails.
	if !strings.Contains(item.ArtifactFormat, "json") {
		t.Fatalf("artifact format = %q, want a JSON-carrying format (raw output: %s)", item.ArtifactFormat, item.RawOutput)
	}
	payloadStatus, metrics := builtinSkillE2EMetrics(t, item.ArtifactJSON)
	if payloadStatus != item.Status {
		t.Fatalf("item status = %q but the Skill payload reported status %q", item.Status, payloadStatus)
	}
	// The verdict mirrors the payload (pass/warning), never critical: the container
	// has no ps and its disk watermark is host-specific, so both are accepted here
	// while a broken script still fails the checks above.
	if payloadStatus != "pass" && payloadStatus != "warning" {
		t.Fatalf("payload status = %q, want pass or warning (checks=%+v)", payloadStatus, item.Checks)
	}
	for _, key := range []string{"disk_total_bytes", "disk_used_bytes", "disk_percent"} {
		value, ok := metrics[key]
		if !ok {
			t.Fatalf("artifact JSON has no %s metric: %s", key, item.ArtifactJSON)
		}
		if value <= 0 {
			t.Fatalf("%s = %v, want > 0 (metrics=%v)", key, value, metrics)
		}
	}

	artifactRoot := filepath.Join(f.store.dbDir, "artifacts")
	matches, err := filepath.Glob(filepath.Join(artifactRoot, "*", run.ID, "item_*", "artifact.json"))
	if err != nil {
		t.Fatalf("glob artifacts under %s: %v", artifactRoot, err)
	}
	if len(matches) == 0 {
		t.Fatalf("no artifact.json under %s for run %s (item.ArtifactDir=%q)", artifactRoot, run.ID, item.ArtifactDir)
	}
	onDisk, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read %s: %v", matches[0], err)
	}
	if !json.Valid(onDisk) || !strings.Contains(string(onDisk), "disk_total_bytes") {
		t.Fatalf("on-disk artifact %s is not the script payload: %s", matches[0], onDisk)
	}

	rows, err := f.store.db.Query("SELECT metric_key, value FROM inspection_metric_points WHERE run_id=?", run.ID)
	if err != nil {
		t.Fatalf("query inspection_metric_points: %v", err)
	}
	defer rows.Close()
	stored := map[string]float64{}
	for rows.Next() {
		var key string
		var value float64
		if err := rows.Scan(&key, &value); err != nil {
			t.Fatalf("scan metric row: %v", err)
		}
		stored[key] = value
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate metric rows: %v", err)
	}
	if len(stored) == 0 {
		t.Fatalf("run %s persisted no inspection_metric_points rows", run.ID)
	}
	if value := stored["disk_total_bytes"]; value <= 0 {
		t.Fatalf("persisted disk_total_bytes = %v, want > 0 (rows=%v)", value, stored)
	}
	if _, ok := stored["disk_used_bytes"]; !ok {
		t.Fatalf("persisted metrics lack disk_used_bytes: %v", stored)
	}
	t.Logf("payload status=%s artifact_format=%s", payloadStatus, item.ArtifactFormat)
	t.Logf("artifact file=%s", matches[0])
	t.Logf("inspection_metric_points rows: %v", stored)

	query := url.Values{}
	query.Set("skill", builtinSkillE2ESlug)
	query.Set("target", "dev/local")
	query.Set("range", "1h")
	seriesRaw := f.http(t, operator, "GET", "/v1/monitor/metrics?"+query.Encode(), "", 200)
	var series struct {
		Series []struct {
			Key    string `json:"key"`
			Points []struct {
				Value float64 `json:"value"`
				RunID string  `json:"run_id"`
			} `json:"points"`
		} `json:"series"`
	}
	if err := json.Unmarshal(seriesRaw, &series); err != nil {
		t.Fatalf("decode metric series: %v; %s", err, seriesRaw)
	}
	exposed := false
	for _, s := range series.Series {
		if s.Key != "disk_total_bytes" {
			continue
		}
		for _, p := range s.Points {
			if p.RunID == run.ID && p.Value > 0 {
				exposed = true
			}
		}
	}
	if !exposed {
		t.Fatalf("metric series API did not expose disk_total_bytes for run %s: %s", run.ID, seriesRaw)
	}
}

// TestBuiltinSkillE2EScriptPackages runs the structure-contract lock over every
// discovered script-backed builtin package, one subtest per slug.
func TestBuiltinSkillE2EScriptPackages(t *testing.T) {
	mode := builtinSkillE2EShellMode()
	if mode == "docker" {
		builtinSkillE2ERequireDocker(t)
	}
	for _, slug := range builtinSkillE2EScriptSlugs(t) {
		slug := slug
		t.Run(slug, func(t *testing.T) {
			builtinSkillE2EVerifyScriptPackage(t, slug, mode)
		})
	}
}

// TestBuiltinSkillE2EScriptlessPackages names the declared capabilities that have no
// executable script and logs why they are not run here instead of faking a pass.
func TestBuiltinSkillE2EScriptlessPackages(t *testing.T) {
	scripted := map[string]bool{}
	for _, slug := range builtinSkillE2EScriptSlugs(t) {
		scripted[slug] = true
	}
	skipped := 0
	for _, spec := range builtinSystemSkills {
		if scripted[spec.Slug] {
			continue
		}
		if strings.TrimSpace(spec.Entrypoint) != "" {
			t.Errorf("builtin capability %s declares entrypoint %q but ships no scripts/inspect.sh; the run pipeline cannot execute it", spec.Slug, spec.Entrypoint)
			continue
		}
		t.Logf("skip %s: %s", spec.Slug, builtinSkillE2ESkipReason(t, spec))
		skipped++
	}
	if skipped == 0 {
		t.Fatalf("no scriptless builtin capability was found; the skip branch is no longer exercised")
	}
}

// TestBuiltinSkillE2ENegativeControls pins the failure boundaries of the same
// pipeline. The registry rejection runs anywhere; the script failure and non-JSON
// subtests need no docker; the absent-entrypoint control needs docker.
func TestBuiltinSkillE2ENegativeControls(t *testing.T) {
	t.Run("script_failure_is_critical", func(t *testing.T) {
		_, _, _, item, _ := builtinSkillE2ERun(t, builtinSkillE2ESlug, "fail")
		if item.Status != "critical" {
			t.Fatalf("status = %q, want critical for a non-zero Skill script", item.Status)
		}
		if item.Reason != "skill_script_failed" {
			t.Fatalf("reason = %q, want skill_script_failed", item.Reason)
		}
		if len(item.Checks) != 1 || item.Checks[0].ID != "skill.script" || item.Checks[0].Status != "critical" {
			t.Fatalf("checks = %+v, want a single critical skill.script check", item.Checks)
		}
		if len(item.ArtifactJSON) != 0 {
			t.Fatalf("a failed script must not produce artifact JSON: %s", item.ArtifactJSON)
		}
	})
	t.Run("non_json_output_never_becomes_metrics", func(t *testing.T) {
		f, _, run, item, _ := builtinSkillE2ERun(t, builtinSkillE2ESlug, "garbage")
		if item.Status == "critical" {
			t.Fatalf("non-JSON output is classified as %q here; update this expectation deliberately instead of widening it", item.Status)
		}
		if item.ArtifactFormat != "text" {
			t.Fatalf("artifact format = %q, want text for non-JSON output", item.ArtifactFormat)
		}
		if len(item.ArtifactJSON) != 0 {
			t.Fatalf("non-JSON output must not become artifact JSON: %s", item.ArtifactJSON)
		}
		var points int
		if err := f.store.db.QueryRow("SELECT COUNT(*) FROM inspection_metric_points WHERE run_id=?", run.ID).Scan(&points); err != nil {
			t.Fatalf("count metric rows: %v", err)
		}
		if points != 0 {
			t.Fatalf("non-JSON artifact produced %d metric rows; unparseable output must never be bridged", points)
		}
	})
	t.Run("entrypoint_not_in_package_is_rejected", func(t *testing.T) {
		f := monitorEngineFixture(t)
		operator := f.user(t, "int10-negative-op", ScopeGrant{Cluster: "dev", Instance: "local"})
		body := `{"action":"upsert","slug":"int10-entrypoint-missing","category":"inspection","skill_md":"---\nname: INT-10 negative control\nexec_mode: script\nentrypoint: scripts/does-not-exist.sh\n---\n\n# negative control\n"}`
		raw := f.http(t, operator, "POST", "/v1/skills", body, 400)
		if !strings.Contains(string(raw), "skill_entrypoint_missing") {
			t.Fatalf("an entrypoint pointing at an absent file was not rejected with skill_entrypoint_missing: %s", raw)
		}
	})
	t.Run("entrypoint_absent_at_run_time_is_critical", func(t *testing.T) {
		builtinSkillE2ERequireDocker(t)
		_, _, _, item, _ := builtinSkillE2ERun(t, builtinSkillE2ESlug, "missing")
		if item.Status == "pass" || item.Status == "warning" {
			t.Fatalf("status = %q, want a non-pass verdict when the packaged entrypoint file is gone (checks=%+v reason=%q)", item.Status, item.Checks, item.Reason)
		}
	})
}
