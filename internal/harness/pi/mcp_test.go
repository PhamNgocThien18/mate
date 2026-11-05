package pi

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
)

// The replay supplies pi's runtime seam without requiring a global npm install.
// Real factory loading and isolation are separately checked in the Herdr proof.
func stubPiRuntime(t *testing.T, cwd string) {
	t.Helper()
	dir := filepath.Join(cwd, "node_modules", "@earendil-works", "pi-coding-agent")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"package.json": `{"type":"module","exports":"./index.mjs"}`,
		"index.mjs": `export function createMcpExtension(options) {
  const config = options.loadConfig({});
  if (config.servers.length || config.errors.length) throw Error('global config leaked');
  return pi => { pi.nativeMCPOptions = options; };
}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPiMCPConfigIgnoresGlobal(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(AgentDirEnv, dir)
	servers := []harness.MCPServer{{Name: "work-probe", Type: "stdio", Command: []string{"./server", "quoted \"arg"}, Cwd: "/package dir"}, {Name: "remote", Type: "http", URL: "https://example.com/mcp"}}
	doc, err := MCPConfig(servers)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Servers map[string]struct {
			Command, Cwd, URL, Exposure string
			Args                        []string
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(doc, &decoded); err != nil {
		t.Fatal(err)
	}
	got := decoded.Servers["work-probe"]
	if got.Command != "./server" || got.Cwd != "/package dir" || got.Exposure != "direct" || !slices.Equal(got.Args, servers[0].Command[1:]) {
		t.Fatalf("%+v", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "mcp.json"), []byte(`{"mcpServers":{"work_probe":{"url":"https://other.com"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if again, err := MCPConfig(servers); err != nil || string(again) != string(doc) {
		t.Fatalf("global config changed renderer: %s %v", again, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mcp.json"), []byte(`broken global JSON`), 0600); err != nil {
		t.Fatal(err)
	}
	if again, err := MCPConfig(servers); err != nil || string(again) != string(doc) {
		t.Fatal("renderer read malformed global config")
	}
	if _, err := MCPConfig(append(servers, harness.MCPServer{Name: "work_probe", Type: "http", URL: "https://example.com"})); err == nil {
		t.Fatal("normalized collision accepted")
	}
	if _, err := MCPConfig([]harness.MCPServer{{Name: "has.dot", Type: "http", URL: "https://example.com"}}); err == nil {
		t.Fatal("invalid native pi name accepted")
	}
}

// requireNode skips a test that runs the extension under node when node is not
// on PATH. expected-skips.txt allows that skip on linux only, until the image
// make check-linux uses has node (CON-219). A Mac without node still fails.
func requireNode(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not on PATH; this test runs the pi extension under node")
	}
}

func TestPiMateExtensionEvents(t *testing.T) {
	requireNode(t)
	req := crewRequest(t)
	req.Role = harness.RoleMate
	logPath := filepath.Join(req.Cwd, "payloads.jsonl")
	req.Binary = filepath.Join(req.Cwd, "hook-test")
	shell := "#!/bin/sh\ncat >> " + harness.ShellQuote(logPath) + "\nprintf '\\n' >> " + harness.ShellQuote(logPath) + "\nprintf DIGEST\n"
	if err := os.WriteFile(req.Binary, []byte(shell), 0755); err != nil {
		t.Fatal(err)
	}
	prep, err := (Pi{}).Prepare(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range prep.Files {
		if err := os.MkdirAll(filepath.Dir(f.Path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f.Path, f.Data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	stubPiRuntime(t, req.Cwd)
	// Replay real event shapes through the embedded module. The fake binary
	// records payloads, which catches duplicate receipts and source mapping.
	script := `
import {writeFileSync,readFileSync} from 'node:fs';
const configPath=process.argv[1];const extension=process.argv[2];
const events={};const servers=[];
const pi={registerMcpServer:(n,s)=>servers.push([n,s]),on:(n,f)=>{events[n]=f}};
const {default:install}=await import(extension);install(pi);
if(!pi.nativeMCPOptions)throw Error('native MCP not installed');
const ctx={cwd:process.cwd(),hasUI:false,sessionManager:{getSessionId:()=>"test-session",getSessionFile:()=>"test.jsonl"}};
await events.session_start({reason:'startup'},ctx);
await events.input({text:'⟦mate⟧ hello',source:'interactive'},ctx);
const first=await events.before_agent_start({},ctx);
if(first?.message?.content!=="DIGEST"||first.message.customType!=='mate-recall')throw Error('no recall');
if(await events.before_agent_start({},ctx))throw Error('duplicate recall');
await events.session_compact({},ctx);
if(!(await events.before_agent_start({},ctx))?.message)throw Error('no compact recall');
await events.agent_end({messages:[{role:'assistant',stopReason:'stop',content:[{type:'text',text:'answer'}]}]},ctx);
await events.session_start({reason:'resume'},ctx);
if(!(await events.before_agent_start({},ctx))?.message)throw Error('no resume recall');
`
	cmd := exec.Command("node", "--input-type=module", "-e", script, launchConfigPath(req.Cwd), extensionPath(req.Cwd))
	cmd.Dir = req.Cwd
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("embedded extension events: %v %s", err, out)
	}
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	var payloads []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var p map[string]any
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			t.Fatal(err)
		}
		payloads = append(payloads, p)
	}
	if len(payloads) != 5 || payloads[0]["prompt"] != "⟦mate⟧ hello" || payloads[1]["source"] != "startup" || payloads[2]["source"] != "compact" || payloads[3]["last_assistant_message"] != "answer" || payloads[4]["source"] != "resume" {
		t.Fatalf("payloads: %+v", payloads)
	}

}

func TestPiHookErrorsAreVisibleAndBounded(t *testing.T) {
	requireNode(t)
	req := crewRequest(t)
	req.Role = harness.RoleMate
	req.Binary = filepath.Join(req.Cwd, "hook-test")
	if err := os.WriteFile(req.Binary, []byte("#!/bin/sh\ncat >/dev/null\necho 'handler failed' >&2\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	files, err := mateFiles(req)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if err := os.MkdirAll(filepath.Dir(f.Path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f.Path, f.Data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	stubPiRuntime(t, req.Cwd)
	script := `
import {writeFileSync} from 'node:fs';
const {default:install,runHook}=await import(process.argv[1]);
const events={};const warnings=[];
install({registerMcpServer:()=>{},on:(n,f)=>{events[n]=f},appendEntry:()=>{}});
const ctx={cwd:process.cwd(),hasUI:true,ui:{notify:(m)=>warnings.push(m)}};
await events.input({text:'hello',source:'interactive'},ctx);
if(warnings.length!==1||!warnings[0].includes('handler failed'))throw Error('stderr silently lost');
const hung=process.argv[2];
writeFileSync(hung,'#!/usr/bin/env node\nsetInterval(()=>{},1000);\n',{mode:0o755});
const started=Date.now();
try {await runHook(hung,[],{},process.cwd(),50);throw Error('hung hook accepted');}
catch(err) {if(!err.message.includes('timed out'))throw err;}
if(Date.now()-started>2000)throw Error('timeout not bounded');
`
	cmd := exec.Command("node", "--input-type=module", "-e", script, extensionPath(req.Cwd), filepath.Join(req.Cwd, "hung-hook"))
	cmd.Dir = req.Cwd
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hook failures: %v %s", err, out)
	}
}

// Recall reads files and git metadata, so it gets Codex's SessionStart bound;
// a receipt blocks the composer, so it keeps the short one. Both run at once
// against a binary slower than the receipt bound.
func TestPiRecallOutlastsTheReceiptTimeout(t *testing.T) {
	requireNode(t)
	req := crewRequest(t)
	req.Role = harness.RoleMate
	req.Binary = filepath.Join(req.Cwd, "hook-test")
	if err := os.WriteFile(req.Binary, []byte("#!/bin/sh\ncat >/dev/null\nsleep 6\n[ \"$2\" = mate-session ] && printf DIGEST\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	files, err := mateFiles(req)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if err := os.MkdirAll(filepath.Dir(f.Path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f.Path, f.Data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	stubPiRuntime(t, req.Cwd)
	script := `
const {default:install}=await import(process.argv[1]);
const events={};const warnings=[];
install({registerMcpServer:()=>{},on:(n,f)=>{events[n]=f}});
const ctx={cwd:process.cwd(),hasUI:true,ui:{notify:(m)=>warnings.push(m)},sessionManager:{getSessionId:()=>"s",getSessionFile:()=>"s.jsonl"}};
const started=Date.now();
const receipt=events.input({text:'hello',source:'interactive'},ctx).then(()=>Date.now()-started);
const [recall,receiptMs]=await Promise.all([events.before_agent_start({},ctx),receipt]);
if(recall?.message?.content!=='DIGEST')throw Error('recall cut short: '+JSON.stringify(recall)+' '+warnings);
if(receiptMs>5500||!warnings.some((w)=>w.includes('timed out')))throw Error('receipt not bounded: '+receiptMs+'ms '+warnings);
`
	cmd := exec.Command("node", "--input-type=module", "-e", script, extensionPath(req.Cwd))
	cmd.Dir = req.Cwd
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hook timeouts: %v %s", err, out)
	}
}

func TestPiMateFilesProtectOwnershipAndLaunch(t *testing.T) {
	req := crewRequest(t)
	req.Role = harness.RoleMate
	req.Binary = "/bin/mate"
	files, err := mateFiles(req)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if err := os.MkdirAll(filepath.Dir(f.Path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f.Path, f.Data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := checkMateFiles(req.Cwd, nil, false); err != nil {
		t.Fatal(err)
	}
	if err := checkMateFiles(req.Cwd, nil, true); err == nil {
		t.Fatal("accepted config for a different launch intent")
	}
	if err := os.WriteFile(extensionPath(req.Cwd), []byte("user extension"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := mateFiles(req); err == nil {
		t.Fatal("overwrote an unowned extension")
	}
	if err := checkMateFiles(req.Cwd, nil, false); err == nil {
		t.Fatal("accepted a changed extension")
	}
	if err := os.WriteFile(launchConfigPath(req.Cwd), []byte(`{"binary":"/missing"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if changed, err := (piHooks{}).Repoint("/new/mate", req.Cwd, func(string) bool { return false }); changed || err == nil {
		t.Fatal("repointed an unowned config")
	}
}

func TestPiRepointPreservesLaunchConfig(t *testing.T) {
	cwd := t.TempDir()
	path := launchConfigPath(cwd)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"mateOwned":1,"binary":"/old/mate","resume":true,"mcpServers":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := (piHooks{}).Repoint("/new/mate", cwd, func(string) bool { return false })
	if err != nil || !changed {
		t.Fatalf("repoint: %v %v", changed, err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["binary"] != "/new/mate" || doc["resume"] != true || doc["mateOwned"] != float64(1) {
		t.Fatalf("repoint lost launch fields: %s", b)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatalf("repoint left temporary files: %v %v", entries, err)
	}
}

func TestPiCrewRefusesPluginServers(t *testing.T) {
	req := crewRequest(t)
	req.MCPServers = []harness.MCPServer{{Name: "remote", Type: "http", URL: "https://example.com/mcp"}}
	if _, err := (Pi{}).Prepare(context.Background(), req); err == nil {
		t.Fatal("Crew Prepare accepted plugin servers")
	}
	if _, err := (Pi{}).Build(context.Background(), harness.AgentSpec{Role: harness.RoleCrew, Cwd: req.Cwd, ContextPath: req.ContextPath, MCPServers: req.MCPServers}); err == nil {
		t.Fatal("Crew Build accepted plugin servers")
	}
}
