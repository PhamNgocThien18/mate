package spawn_test

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/codex"
	"github.com/nguyenngocanh94/mate/internal/harness/pi"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/runtime/herdr"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// TestLivePluginMCPStartResume exercises real StartMate -> Herdr -> MCP
// calls and results, not a print-mode substitute. Each transport starts
// and resumes the same session. pi also proves prompt/answer hooks and recall.
func TestLivePluginMCPStartResume(t *testing.T) {
	for _, kind := range []harness.Kind{codex.KindCodex, pi.KindPi} {
		for _, transport := range []string{"stdio", "http", "oauth"} {
			t.Run(string(kind)+"/"+transport, func(t *testing.T) {
				lab := newLiveLab(t)
				capture := &mcpCaptureRuntime{Herdr: lab.rt}
				lab.deps.Runtime = capture
				if kind == codex.KindCodex {
					lab.deps.Harnesses = codexSessionsIn(t, filepath.Join(os.Getenv(codex.CodexHomeEnv), "sessions"))
				}
				if kind == pi.KindPi {
					piLabAuth(t)
				}
				pkg := filepath.Join(lab.w.Root(), "plugins", "probe")
				if err := os.MkdirAll(pkg, 0755); err != nil {
					t.Fatal(err)
				}
				fixture := filepath.Join(pkg, "mcpfixture")
				if out, err := exec.Command("go", "build", "-o", fixture, "github.com/nguyenngocanh94/mate/scripts/mcpfixture").CombinedOutput(); err != nil {
					t.Fatalf("fixture: %v %s", err, out)
				}
				logPath := filepath.Join(lab.w.Root(), "fixture.jsonl")
				pidPath := filepath.Join(lab.w.Root(), "fixture.pid")
				// The fixture reads its log path from argv-independent cwd state.
				manifest := "apiVersion: mate.plugin/v1\nname: Probe\nmcp:\n"
				if transport == "stdio" {
					// Wrapper passes only this lab's log, never credentials; relative
					// executable verifies the server cwd differs from the Mate cwd.
					wrapper := filepath.Join(pkg, "server")
					data := "#!/bin/sh\nexport MCP_FIXTURE_LOG=" + harness.ShellQuote(logPath) + "\nexec ./mcpfixture --pid-file " + harness.ShellQuote(pidPath) + "\n"
					if err := os.WriteFile(wrapper, []byte(data), 0755); err != nil {
						t.Fatal(err)
					}
					manifest += "  type: stdio\n  command: [./server]\n"
				} else {
					addrFile := filepath.Join(pkg, "address")
					argv := []string{"--http", "127.0.0.1:0", "--address-file", addrFile}
					if transport == "oauth" {
						argv = append(argv, "--oauth")
					}
					cmd := exec.Command(fixture, argv...)
					cmd.Env = append(os.Environ(), "MCP_FIXTURE_LOG="+logPath)
					if err := cmd.Start(); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
					url := waitFileContaining(t, addrFile, "http://", 15*time.Second)
					manifest += "  type: http\n  url: " + strings.TrimSpace(url) + "\n"
					if transport == "oauth" {
						loginMCPFixture(t, kind, strings.TrimSpace(url))
					}
				}
				globalMarker := filepath.Join(lab.w.Root(), "global-mcp-leaked")
				if kind == pi.KindPi {
					// Both an exact collision and a foreign server must stay out.
					// Either global subprocess writes a marker if native config leaks.
					config, err := json.Marshal(map[string]any{"mcpServers": map[string]any{
						"probe-inst":  map[string]any{"command": "/usr/bin/touch", "args": []string{globalMarker}},
						"global-leak": map[string]any{"command": "/usr/bin/touch", "args": []string{globalMarker}},
					}})
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(os.Getenv(pi.AgentDirEnv), "mcp.json"), config, 0600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(filepath.Join(pkg, "mate-plugin.yaml"), []byte(manifest), 0644); err != nil {
					t.Fatal(err)
				}
				writePluginsYAML(t, lab.w, "apiVersion: mate.plugins/v1\ninstances:\n  probe-inst:\n    path: plugins/probe\n    projects:\n      shop: {}\n")
				seedCanary(t, lab.w.MemoryFile("shop"), "MCP-RECALL-CANARY")
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
				defer cancel()
				request := spawn.StartRequest{Project: "shop", Harness: kind, Resume: true}
				if kind == pi.KindPi {
					cfg, err := lab.w.LoadProject("shop")
					if err != nil {
						t.Fatal(err)
					}
					cfg.Mate = store.MateConfig{Model: "openai-codex/gpt-6.1-sol", Effort: "low"}
					if err := lab.w.SaveProject("shop", cfg); err != nil {
						t.Fatal(err)
					}
				}
				var session string
				for _, turn := range []string{"fresh", "resume"} {
					started, err := spawn.StartMate(ctx, lab.w, lab.deps, request)
					if err != nil {
						screen := capture.last
						t.Fatalf("%s start: %v\nFull pane:\n%s", turn, err, screen)
					}
					id := string(kind) + "-" + transport + "-" + turn
					prompt := "Call MCP tool mcp__probe_inst__read_probe with id " + id + ". Return its exact text. Do not use bash or read files."
					if kind == pi.KindPi {
						prompt = "⟦mate⟧ " + prompt + " Also state the deploy canary word from the mate recall digest in your context."
					}
					if err := lab.rt.PromptAgent(ctx, lab.handle(started), prompt); err != nil {
						t.Fatal(err)
					}
					waitFileContaining(t, logPath, `"id":"`+id+`"`, 150*time.Second)
					var meta map[string]string
					for deadline := time.Now().Add(150 * time.Second); ; {
						meta = readMeta(t, lab.w, "shop")
						transcript := meta[spawn.MetaTranscript]
						if b, err := os.ReadFile(transcript); err == nil && strings.Contains(string(b), "probe:"+id+":ok") {
							break
						}
						if time.Now().After(deadline) {
							t.Fatalf("%s: no tool result in transcript", turn)
						}
						time.Sleep(time.Second)
					}
					if session != "" && session != meta[spawn.MetaSessionID] {
						t.Fatalf("resume changed session %s -> %s", session, meta[spawn.MetaSessionID])
					}
					session = meta[spawn.MetaSessionID]
					if session == "" {
						t.Fatal("no session recorded")
					}
					if kind == pi.KindPi {
						if _, err := os.Stat(globalMarker); !os.IsNotExist(err) {
							t.Fatal("pi loaded a global MCP server")
						}
						waitSentAnswer(t, lab.w, id)
						text, _ := os.ReadFile(meta[spawn.MetaTranscript])
						if !strings.Contains(string(text), "MCP-RECALL-CANARY") {
							t.Fatal("recall not injected")
						}
					}
					if turn == "fresh" {
						if err := lab.rt.PromptAgent(ctx, lab.handle(started), "Call MCP tool mcp__probe_inst__read_probe with id error exactly once. Report the error text verbatim. Do not retry or use any other tool."); err != nil {
							t.Fatal(err)
						}
						waitFileContaining(t, meta[spawn.MetaTranscript], "probe:error:failed", 150*time.Second)
					}
					t.Logf("%s %s %s: real tool call/result, session %s", kind, transport, turn, session)
					if _, err := spawn.StopMate(ctx, lab.w, lab.deps, "shop"); err != nil {
						t.Fatal(err)
					}
					if transport == "stdio" {
						b, err := os.ReadFile(pidPath)
						if err != nil {
							t.Fatal(err)
						}
						pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
						if err != nil {
							t.Fatal(err)
						}
						for deadline := time.Now().Add(10 * time.Second); ; {
							err := syscall.Kill(pid, 0)
							if err == syscall.ESRCH {
								break
							}
							if time.Now().After(deadline) {
								t.Fatalf("stdio fixture PID %d still lives after StopMate", pid)
							}
							time.Sleep(100 * time.Millisecond)
						}
					}

				}
			})
		}
	}
}

// pi intentionally leaves no session file until a conversation starts. A stop
// before the first prompt must therefore restart fresh with a clear note, and
// management commands must not manufacture prompt/answer receipts.
func TestLivePiMateBeforeFirstPrompt(t *testing.T) {
	lab := newLiveLab(t)
	piLabAuth(t)
	globalMarker := filepath.Join(lab.w.Root(), "global-mcp-leaked")
	global, err := json.Marshal(map[string]any{"mcpServers": map[string]any{
		"global-leak": map[string]any{"command": "/usr/bin/touch", "args": []string{globalMarker}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(os.Getenv(pi.AgentDirEnv), "mcp.json"), global, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	request := spawn.StartRequest{Project: "shop", Harness: pi.KindPi, Resume: true}
	first, err := spawn.StartMate(ctx, lab.w, lab.deps, request)
	if err != nil {
		t.Fatal(err)
	}
	if first.SessionID == "" {
		t.Fatal("startup has no prepared session id")
	}
	// /session is a builtin UI command. /mcp is a native extension command.
	for _, command := range []string{"/session", "/mcp", "/compact"} {
		if err := lab.rt.PromptAgent(ctx, lab.handle(first), command); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
	}
	if _, err := spawn.StopMate(ctx, lab.w, lab.deps, "shop"); err != nil {
		t.Fatal(err)
	}
	second, err := spawn.StartMate(ctx, lab.w, lab.deps, request)
	if err != nil {
		t.Fatal(err)
	}
	if second.SessionID == first.SessionID || second.Resumed || !strings.Contains(second.ResumeNote, "no session file") {
		t.Fatalf("empty session restart not explained: first=%s second=%s resumed=%v note=%q", first.SessionID, second.SessionID, second.Resumed, second.ResumeNote)
	}
	if _, err := spawn.StopMate(ctx, lab.w, lab.deps, "shop"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(globalMarker); !os.IsNotExist(err) {
		t.Fatal("pi loaded global MCP in a workspace without plugins")
	}
	rows, _, err := lab.w.ReadSent("shop", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Source == store.SourceUser || row.Source == store.SourceApp || row.Source == store.SourceMate {
			t.Fatalf("slash command created a prompt/answer receipt: %v", row)
		}
	}
	t.Logf("pi stop before first prompt restarts fresh with explicit missing-session note; management slash commands made no receipts and global MCP stayed excluded")
}

func waitFileContaining(t *testing.T, path, needle string, timeout time.Duration) string {
	t.Helper()
	for deadline := time.Now().Add(timeout); ; {
		if b, err := os.ReadFile(path); err == nil && strings.Contains(string(b), needle) {
			return string(b)
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %q in %s after %s", needle, path, timeout)
		}
		time.Sleep(time.Second)
	}
}
func waitSentAnswer(t *testing.T, w *store.Workspace, id string) {
	t.Helper()
	for deadline := time.Now().Add(60 * time.Second); ; {
		rows, _, err := w.ReadSent("shop", 0)
		if err != nil {
			t.Fatal(err)
		}
		prompts, answers := 0, 0
		for _, e := range rows {
			if !strings.Contains(e.Text, id) {
				continue
			}
			if e.Source == store.SourceApp {
				prompts++
			}
			if e.Source == store.SourceMate {
				answers++
			}
		}
		if prompts == 1 && answers == 1 {
			return
		}
		if prompts > 1 || answers > 1 {
			t.Fatalf("duplicate hooks: %d prompts, %d answers", prompts, answers)
		}
		if time.Now().After(deadline) {
			t.Fatalf("hooks: %d prompts, %d answers", prompts, answers)
		}
		time.Sleep(time.Second)
	}
}

// piLabAuth creates an isolated pi home. Prefer a copy of a pi login; when
// absent reuse the operator's Codex subscription credentials only in the lab.
// Credentials are never logged and the real homes are never modified.
func piLabAuth(t *testing.T) {
	t.Helper()
	agent := os.Getenv(pi.AgentDirEnv)
	if agent == "" {
		agent = filepath.Join(os.Getenv("HOME"), ".pi", "agent")
	}
	auth, _ := os.ReadFile(filepath.Join(agent, "auth.json"))
	var entries map[string]json.RawMessage
	_ = json.Unmarshal(auth, &entries)
	if len(entries["openai-codex"]) == 0 {
		home, _ := codex.EffectiveCodexHome("")
		// newLiveLab already selected a copied codexlab home.
		b, err := os.ReadFile(filepath.Join(home, "auth.json"))
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Tokens struct {
				Access  string `json:"access_token"`
				Refresh string `json:"refresh_token"`
				Account string `json:"account_id"`
			} `json:"tokens"`
		}
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatal(err)
		}
		parts := strings.Split(doc.Tokens.Access, ".")
		var claims struct {
			Exp int64 `json:"exp"`
		}
		if len(parts) > 1 {
			raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
			_ = json.Unmarshal(raw, &claims)
		}
		auth, _ = json.Marshal(map[string]any{"openai-codex": map[string]any{"type": "oauth", "access": doc.Tokens.Access, "refresh": doc.Tokens.Refresh, "expires": claims.Exp * 1000, "accountId": doc.Tokens.Account}})
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), auth, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(pi.AgentDirEnv, dir)
}

type mcpCaptureRuntime struct {
	*herdr.Herdr
	last string
}

func (c *mcpCaptureRuntime) ReadAgent(ctx context.Context, h runtime.AgentHandle, s harness.ReadSource, n int) (string, error) {
	text, err := c.Herdr.ReadAgent(ctx, h, s, n)
	if err == nil {
		c.last = text
	}
	return text, err
}

// loginMCPFixture completes native OAuth against a local mock authorization
// server. This proves native credential persistence without contacting Linear
// or opening the operator's browser. No real provider credential is printed.
func loginMCPFixture(t *testing.T, kind harness.Kind, url string) {
	t.Helper()
	servers := []harness.MCPServer{{Name: "probe-inst", Type: "http", URL: url}}
	var bin string
	var args []string
	if kind == codex.KindCodex {
		path := filepath.Join(os.Getenv(codex.CodexHomeEnv), "config.toml")
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		// Store mock MCP credentials in the lab home, never the OS keychain.
		b = append([]byte("mcp_oauth_credentials_store = \"file\"\n"), b...)
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
		args, err = codex.MCPArgs(servers)
		if err != nil {
			t.Fatal(err)
		}
		args = append(args, "mcp", "login", "probe-inst", "--no-browser", "--oauth-client-registration", "dcr")
		bin = "codex"
	} else {
		config, err := pi.MCPConfig(servers)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(os.Getenv(pi.AgentDirEnv), "mcp.json")
		if err := os.WriteFile(path, config, 0600); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(path) // Native cache survives for registered-server launch.
		args = []string{"mcp", "login", "probe-inst", "--timeout", "60"}
		bin = "pi"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	// pi's native browser opener is best-effort. Stub only this test process's
	// opener, then follow the local authorization URL through the Go client.
	shim := t.TempDir()
	for _, name := range []string{"open", "xdg-open"} {
		if err := os.WriteFile(filepath.Join(shim, name), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	cmd.Env = append(os.Environ(), "PATH="+shim+string(os.PathListSeparator)+os.Getenv("PATH"))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	reader, writer := io.Pipe()
	cmd.Stdout, cmd.Stderr = writer, writer
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { err := cmd.Wait(); _ = writer.Close(); done <- err }()
	auth := make(chan string, 1)
	go func() {
		defer reader.Close()
		scanner := bufio.NewScanner(reader)
		pattern := regexp.MustCompile(`http://127\.0\.0\.1:[0-9]+/authorize\?[^\s]+`)
		for scanner.Scan() {
			if u := pattern.FindString(scanner.Text()); u != "" {
				select {
				case auth <- u:
				default:
				}
			}
		}
	}()
	var authorizationURL string
	select {
	case authorizationURL = <-auth:
	case err := <-done:
		t.Fatalf("%s native OAuth exited before authorization URL: %v", kind, err)
	case <-ctx.Done():
		t.Fatal("native OAuth did not produce an authorization URL")
	}
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	callback := response.Header.Get("Location")
	if callback == "" {
		t.Fatal("mock issuer did not produce callback")
	}
	callbackClient := &http.Client{Timeout: 15 * time.Second}
	callbackResponse, err := callbackClient.Get(callback)
	if err == nil {
		callbackResponse.Body.Close()
	} else {
		_, _ = io.WriteString(stdin, callback+"\n")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("%s native OAuth login failed: %v", kind, err)
		}
	case <-ctx.Done():
		t.Fatal("native OAuth login did not finish")
	}
	t.Logf("%s native OAuth login succeeded against mock issuer; no browser or operator cache used", kind)
}
