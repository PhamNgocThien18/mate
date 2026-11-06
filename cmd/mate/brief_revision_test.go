package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/brief"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/store"
)

func TestBriefRevisionSavesBeforeNotification(t *testing.T) {
	t.Parallel()
	for _, captainWords := range []string{"", "Only show reachable states."} {
		t.Run(captainWords, func(t *testing.T) {
			w := liveCrewWorkspace(t, "shop")
			rt := runtime.NewFake()
			deps := fakeSpawnDeps(t, rt)
			spawned := spawnFakeCrew(t, w, deps, "shop", "k3")
			handle := runtime.AgentHandle{Session: runtime.SessionHandle{Name: spawned.Session}, Name: spawned.Agent}
			rt.SetReadOutput(handle, codexEmptyScreen)
			before, _ := os.ReadFile(w.CrewBrief("shop", "k3"))
			task, _ := brief.ExtractTask(string(before))
			revision := strings.Replace(task, "## Build\n", "## Build\n- Only reachable states.\n", 1)
			calls := 0
			rt.OnSendText = func(h runtime.AgentHandle, text string) {
				calls++
				data, _ := os.ReadFile(w.CrewBrief("shop", "k3"))
				if !strings.Contains(string(data), "- Only reachable states.") {
					t.Fatal("notification preceded the revision")
				}
				words, _ := brief.SectionText(string(data), brief.CaptainsWords)
				if captainWords != "" && !strings.Contains(words, captainWords) {
					t.Fatal("notification preceded the captain's answer")
				}
				rt.SetReadOutput(h, codexPendingScreen(text))
			}
			rt.OnSendKeys = func(h runtime.AgentHandle, keys []string) { rt.SetReadOutput(h, codexBusyScreen) }
			got, err := briefUpdate(context.Background(), w, deps, "shop", "k3", captainWords, revision, store.SourceMate, time.Now(), false)
			if err != nil || !got.Revised || got.Appended != (captainWords != "") || !got.Report.Delivered() || calls != 1 {
				t.Fatalf("result = %+v, %v; notifications = %d", got, err, calls)
			}
			data, _ := os.ReadFile(w.CrewBrief("shop", "k3"))
			if captainWords == "" {
				beforeWords, _ := brief.SectionText(string(before), brief.CaptainsWords)
				afterWords, _ := brief.SectionText(string(data), brief.CaptainsWords)
				if beforeWords != afterWords {
					t.Fatal("Mate decision changed captain words")
				}
			}
			if !strings.Contains(got.Line, "whole # Task") || len(brief.CheckFile(string(data), brief.Ship)) != 0 {
				t.Fatalf("bad revised brief or notification: %s\n%s", got.Line, data)
			}
		})
	}
}

func TestBriefRevisionRefusalAndRecovery(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"stopped", "malformed", "stale", "busy"} {
		t.Run(mode, func(t *testing.T) {
			w := liveCrewWorkspace(t, "shop")
			rt := runtime.NewFake()
			deps := fakeSpawnDeps(t, rt)
			spawned := spawnFakeCrew(t, w, deps, "shop", "k3")
			handle := runtime.AgentHandle{Session: runtime.SessionHandle{Name: spawned.Session}, Name: spawned.Agent}
			rt.SetReadOutput(handle, codexBusyScreen)
			before, _ := os.ReadFile(w.CrewBrief("shop", "k3"))
			revision, _ := brief.ExtractTask(string(before))
			switch mode {
			case "stopped":
				if _, err := stopFakeCrew(t, w, deps, "shop", "k3"); err != nil {
					t.Fatal(err)
				}
			case "malformed":
				revision = "## Build\nDo more."
			case "stale":
				revision = strings.Replace(revision, "do the thing", "changed captain words", 1)
			}
			got, err := briefUpdate(context.Background(), w, deps, "shop", "k3", "Keep retry manual.", revision, store.SourceMate, time.Now(), false)
			if err == nil {
				t.Fatal("expected refusal")
			}
			after, _ := os.ReadFile(w.CrewBrief("shop", "k3"))
			if mode != "busy" {
				if got.Appended || got.Revised || !bytes.Equal(before, after) {
					t.Fatal("failed validation changed the brief")
				}
				return
			}
			if !got.Appended || !got.Revised || !strings.Contains(string(after), "Keep retry manual.") {
				t.Fatal("send refusal lost the saved change")
			}
			var out, errw bytes.Buffer
			reportBriefUpdate(&out, &errw, "shop", "k3", store.SourceMate, got, err)
			if !strings.Contains(errw.String(), "mate send shop k3") || !strings.Contains(errw.String(), got.BriefPath) {
				t.Fatalf("missing recovery pointer: %s", errw.String())
			}
		})
	}
}

func TestBriefRevisionRestingCrewReadsRevisedContractInFreshSession(t *testing.T) {
	t.Parallel()
	w := liveCrewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeSpawnDeps(t, rt)
	restingShip(t, w, deps, "wait-mate: ready")
	rec := &promptRecorder{Fake: rt}
	deps.Runtime = rec
	data, _ := os.ReadFile(w.CrewBrief("shop", "k3"))
	task, _ := brief.ExtractTask(string(data))
	revision := strings.Replace(task, "## Build\n", "## Build\n- Keep retry manual.\n", 1)
	got, err := briefUpdate(context.Background(), w, deps, "shop", "k3", "", revision, store.SourceMate, time.Now(), false)
	if err != nil || got.Fresh == nil {
		t.Fatalf("fresh revision: %+v, %v", got, err)
	}
	input, err := os.ReadFile(got.Fresh.InputPath)
	if err != nil || !strings.Contains(string(input), "- Keep retry manual.") || !strings.Contains(string(input), got.Line) {
		t.Fatalf("fresh session did not receive the revision: %s, %v", input, err)
	}
}

func TestBriefRevisionCLIRejectsEmptyFile(t *testing.T) {
	t.Parallel()
	file := writeTemp(t, "\n")
	for _, args := range [][]string{
		{"brief", "revise", "shop", "k3", file},
		{"brief", "append", "shop", "k3", "--revision", file},
	} {
		var out, errw bytes.Buffer
		err := cmdBrief(args[1:], strings.NewReader("new words"), &out, &errw)
		if err == nil || !strings.Contains(err.Error(), "file is empty") {
			t.Fatalf("%v: %v", args, err)
		}
	}
}
