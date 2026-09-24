package main

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// captureError runs fn, which must fail(), and returns the exit code and the
// JSON error body it wrote.
func captureError(t *testing.T, fn func()) (int, map[string]any) {
	t.Helper()
	t.Setenv("RCMD_ERROR_FORMAT", "json")
	var buf bytes.Buffer
	oldOut, oldExit := errorOut, osExit
	errorOut = &buf
	osExit = func(code int) { panic(exitCodePanic(code)) }
	defer func() { errorOut, osExit = oldOut, oldExit }()

	code := -1
	func() {
		defer func() {
			if r := recover(); r != nil {
				c, ok := r.(exitCodePanic)
				if !ok {
					panic(r)
				}
				code = int(c)
			}
		}()
		fn()
	}()
	var body map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &body); err != nil {
		t.Fatalf("error body is not JSON: %q (%v)", buf.String(), err)
	}
	return code, body
}

// ---- typed errors (cli-output-spec §3) ----

func TestUnknownCommandTypedError(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"remotecmd-cli", "__no_such_command__"}

	code, body := captureError(t, main)
	if code != ExitConfigError {
		t.Errorf("exit = %d, want %d", code, ExitConfigError)
	}
	e, _ := body["error"].(map[string]any)
	if body["ok"] != false || e == nil {
		t.Fatalf("body = %v, want ok:false with error", body)
	}
	if int(e["code"].(float64)) != code {
		t.Errorf("error.code = %v, want it to equal the exit status %d", e["code"], code)
	}
	if e["type"] != "unknown_command" || e["recoverable"] != false {
		t.Errorf("error = %v, want type unknown_command, recoverable false", e)
	}
	if s, _ := e["suggestions"].([]any); len(s) == 0 {
		t.Errorf("expected suggestions, got %v", e["suggestions"])
	}
}

func TestExecMissingTargetTypedError(t *testing.T) {
	cleanup := setupTestHome(t)
	defer cleanup()
	code, body := captureError(t, func() { handleExecSubcommand([]string{"--target", "ghost", "--cmd", "true"}) })
	e := body["error"].(map[string]any)
	if code != ExitConfigError || e["type"] != "unknown_target" {
		t.Errorf("exit %d, error %v; want %d unknown_target", code, e, ExitConfigError)
	}
}

func TestRelayErrorsAreRecoverable(t *testing.T) {
	code, body := captureError(t, func() {
		failErr(errString("cannot connect to relay: dial tcp: i/o timeout"))
	})
	e := body["error"].(map[string]any)
	if code != ExitRelayError || e["type"] != "relay_unreachable" || e["recoverable"] != true {
		t.Errorf("exit %d, error %v; want %d relay_unreachable recoverable", code, e, ExitRelayError)
	}
}

func TestErrorTextFormatForHumans(t *testing.T) {
	t.Setenv("RCMD_ERROR_FORMAT", "text")
	var buf bytes.Buffer
	writeError(&buf, cliError{Code: 3, Type: "missing_argument", Message: "--cmd is required", Suggestions: []string{"remotecmd-cli exec --cmd x"}})
	want := "Error: --cmd is required\n  try: remotecmd-cli exec --cmd x\n"
	if buf.String() != want {
		t.Errorf("text error = %q, want %q", buf.String(), want)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// ---- help-json (cli-output-spec §4) ----

func helpJSONOutput(t *testing.T) map[string]any {
	t.Helper()
	var cat map[string]any
	out := captureStdout(t, handleHelpJSON)
	if err := json.Unmarshal([]byte(out), &cat); err != nil {
		t.Fatalf("help-json is not JSON: %v", err)
	}
	return cat
}

func TestHelpJSONShape(t *testing.T) {
	cat := helpJSONOutput(t)
	for _, k := range []string{"version", "commands", "exit_codes", "env"} {
		if cat[k] == nil {
			t.Errorf("help-json missing %q", k)
		}
	}
	for name, v := range cat["commands"].(map[string]any) {
		c := v.(map[string]any)
		if _, ok := c["args"].([]any); !ok {
			t.Errorf("command %q has no args array", name)
		}
		if _, ok := c["auth"].(bool); !ok {
			t.Errorf("command %q has no auth flag", name)
		}
	}
}

// Every top-level command in main()'s dispatcher must be in the catalog.
func TestHelpJSONCoversDispatcher(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var cmds []string
	ast.Inspect(f, func(n ast.Node) bool {
		if cc, ok := n.(*ast.CaseClause); ok {
			for _, e := range cc.List {
				if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					s, _ := strconv.Unquote(lit.Value)
					cmds = append(cmds, s)
				}
			}
		}
		return true
	})
	catalog := helpCatalog()["commands"].(map[string]catalogCommand)
	for _, cmd := range cmds {
		if cmd == "--help" || cmd == "-h" {
			continue
		}
		found := false
		for key := range catalog {
			if key == cmd || strings.HasPrefix(key, cmd+" ") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("dispatcher command %q has no help-json entry", cmd)
		}
	}
}

// Every environment variable the CLI reads must be advertised.
func TestHelpJSONListsEveryEnvVar(t *testing.T) {
	skip := map[string]bool{"SHELL": true, "USER": true, "RCMD_TEST_CONFIG_DIR": true}
	advertised := map[string]bool{}
	for _, v := range helpCatalog()["env"].([]string) {
		advertised[v] = true
	}
	re := regexp.MustCompile(`Getenv\("([A-Z_]+)"\)`)
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, _ := os.ReadFile(f)
		for _, m := range re.FindAllStringSubmatch(string(src), -1) {
			if !skip[m[1]] && !advertised[m[1]] {
				t.Errorf("%s reads %s but help-json does not list it", f, m[1])
			}
		}
	}
}

// ---- guide (cli-guide-spec) ----

func TestGuideJSON(t *testing.T) {
	out := captureStdout(t, func() { handleGuide(nil) })
	var env struct {
		OK      bool           `json:"ok"`
		Version string         `json:"version"`
		Guide   map[string]any `json:"guide"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("guide is not JSON: %v", err)
	}
	if !env.OK || env.Version != Version {
		t.Errorf("envelope ok=%v version=%q, want true %q", env.OK, env.Version, Version)
	}
	for _, k := range []string{"remotecmd-cli", "one_liner", "model", "loop", "concepts", "commands", "examples", "gotchas"} {
		v, ok := env.Guide[k]
		if !ok || v == nil || v == "" {
			t.Errorf("guide missing required field %q", k)
		}
	}
	if env.Guide["version"] != Version {
		t.Errorf("guide.version = %v, want %q", env.Guide["version"], Version)
	}
}

func TestGuideHuman(t *testing.T) {
	out := captureStdout(t, func() { handleGuide([]string{"--human"}) })
	for _, want := range []string{"# remotecmd-cli guide", "## Loop", "## Gotchas", "## Examples"} {
		if !strings.Contains(out, want) {
			t.Errorf("--human output missing %q", want)
		}
	}
	if json.Valid([]byte(out)) {
		t.Error("--human should be markdown, not JSON")
	}
}

func TestGuideRejectsUnknownFlag(t *testing.T) {
	code, body := captureError(t, func() { handleGuide([]string{"--bogus"}) })
	if code != ExitConfigError || body["error"].(map[string]any)["type"] != "invalid_arguments" {
		t.Errorf("exit %d body %v", code, body)
	}
}
