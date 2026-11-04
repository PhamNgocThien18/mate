package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Buckets are the token buckets mate's ledger keeps (docs/dashboard.md).
type Buckets struct {
	Calls      int   `json:"calls"`
	Input      int64 `json:"input"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
	Output     int64 `json:"output"`
	Thinking   int64 `json:"thinking"`
}

// Total is input + cache_read + cache_write + output; thinking is reported
// apart because it is already inside output in the API's accounting.
func (b Buckets) Total() int64 { return b.Input + b.CacheRead + b.CacheWrite + b.Output }

func (b *Buckets) add(o Buckets) {
	b.Calls += o.Calls
	b.Input += o.Input
	b.CacheRead += o.CacheRead
	b.CacheWrite += o.CacheWrite
	b.Output += o.Output
	b.Thinking += o.Thinking
}

type FileReport struct {
	Path string `json:"path"`
	Buckets
}

type Report struct {
	Files   []FileReport   `json:"files"`
	Totals  Buckets        `json:"totals"`
	Models  map[string]int `json:"models"`
	Skipped int            `json:"skipped_outside_window"`
}

type window struct{ since, until time.Time }

func (w window) keeps(ts string) bool {
	if w.since.IsZero() && w.until.IsZero() {
		return true
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return false
	}
	if !w.since.IsZero() && t.Before(w.since) {
		return false
	}
	if !w.until.IsZero() && !t.Before(w.until) {
		return false
	}
	return true
}

// expand turns arguments into the list of transcript files: a session file
// brings its subagents directory, a directory brings every session in it.
func expand(args []string) ([]string, error) {
	var files []string
	seen := map[string]bool{}
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			files = append(files, p)
		}
	}
	addSession := func(session string) {
		add(session)
		dir := strings.TrimSuffix(session, filepath.Ext(session))
		subs, _ := filepath.Glob(filepath.Join(dir, "subagents", "*.jsonl"))
		sort.Strings(subs)
		for _, s := range subs {
			add(s)
		}
	}
	for _, a := range args {
		info, err := os.Stat(a)
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			sessions, _ := filepath.Glob(filepath.Join(a, "*.jsonl"))
			sort.Strings(sessions)
			if len(sessions) == 0 {
				return nil, fmt.Errorf("%s: no *.jsonl inside", a)
			}
			for _, s := range sessions {
				addSession(s)
			}
			continue
		}
		addSession(a)
	}
	return files, nil
}

type record struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
			Input      int64 `json:"input_tokens"`
			CacheWrite int64 `json:"cache_creation_input_tokens"`
			CacheRead  int64 `json:"cache_read_input_tokens"`
			Output     int64 `json:"output_tokens"`
			Details    *struct {
				Thinking int64 `json:"thinking_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
	} `json:"message"`
}

type call struct {
	model string
	b     Buckets
}

// sum reads every file; a message.id seen in several records (one per content
// block) counts once, with the usage of its last record, which is the
// completed cost when a subagent's snapshot grows across records.
func sum(files []string, w window) (Report, error) {
	r := Report{Models: map[string]int{}}
	for _, path := range files {
		calls := map[string]call{}
		var order []string
		f, err := os.Open(path)
		if err != nil {
			return r, err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
		for sc.Scan() {
			line := sc.Bytes()
			var rec record
			if err := json.Unmarshal(line, &rec); err != nil {
				continue // housekeeping records of other shapes
			}
			if rec.Type != "assistant" || rec.Message.ID == "" || rec.Message.Usage == nil {
				continue
			}
			if !w.keeps(rec.Timestamp) {
				r.Skipped++
				continue
			}
			u := rec.Message.Usage
			b := Buckets{Calls: 1, Input: u.Input, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite, Output: u.Output}
			if u.Details != nil {
				b.Thinking = u.Details.Thinking
			}
			if _, ok := calls[rec.Message.ID]; !ok {
				order = append(order, rec.Message.ID)
			}
			calls[rec.Message.ID] = call{model: rec.Message.Model, b: b}
		}
		f.Close()
		if err := sc.Err(); err != nil {
			return r, fmt.Errorf("%s: %w", path, err)
		}
		fr := FileReport{Path: path}
		for _, id := range order {
			c := calls[id]
			fr.add(c.b)
			r.Models[c.model]++
		}
		r.Files = append(r.Files, fr)
		r.Totals.add(fr.Buckets)
	}
	return r, nil
}
