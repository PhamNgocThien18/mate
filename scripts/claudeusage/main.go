// Command claudeusage sums the token usage of one or more Claude Code sessions
// the way mate's ledger does (CON-159 benchmark, arm A): one model call is one
// message.id, the last record of a message.id carries its completed usage, and
// TOTAL = input + cache_read + cache_write + output with thinking reported
// apart. Subagent transcripts under <session>/subagents/*.jsonl are included.
//
//	go run ./scripts/claudeusage [--since RFC3339] [--until RFC3339] [--json] <session.jsonl|dir>...
//
// A directory argument means every *.jsonl directly inside it (one per
// session) plus their subagents. --since/--until keep only records whose
// timestamp falls in the window, which is how teammate sessions of an agent
// team are limited to one run.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"text/tabwriter"
	"time"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "claudeusage:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("claudeusage", flag.ContinueOnError)
	since := fs.String("since", "", "keep records at or after this RFC3339 time")
	until := fs.String("until", "", "keep records before this RFC3339 time")
	asJSON := fs.Bool("json", false, "print JSON instead of a table")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("want at least one session.jsonl or directory")
	}
	var w window
	var err error
	if w.since, err = parseTime(*since); err != nil {
		return fmt.Errorf("--since: %w", err)
	}
	if w.until, err = parseTime(*until); err != nil {
		return fmt.Errorf("--until: %w", err)
	}
	files, err := expand(fs.Args())
	if err != nil {
		return err
	}
	report, err := sum(files, w)
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	print(out, report)
	return nil
}

func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, s)
}

func print(out io.Writer, r Report) {
	tw := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "FILE\tCALLS\tINPUT\tCACHE_READ\tCACHE_WRITE\tOUTPUT\tTHINKING\tTOTAL")
	for _, f := range r.Files {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\n", f.Path, f.Calls, f.Input, f.CacheRead, f.CacheWrite, f.Output, f.Thinking, f.Total())
	}
	t := r.Totals
	fmt.Fprintf(tw, "TOTAL\t%d\t%d\t%d\t%d\t%d\t%d\t%d\n", t.Calls, t.Input, t.CacheRead, t.CacheWrite, t.Output, t.Thinking, t.Total())
	tw.Flush()
	models := make([]string, 0, len(r.Models))
	for m := range r.Models {
		models = append(models, m)
	}
	sort.Strings(models)
	for _, m := range models {
		fmt.Fprintf(out, "model %s: %d calls\n", m, r.Models[m])
	}
	if r.Skipped > 0 {
		fmt.Fprintf(out, "records outside the time window: %d\n", r.Skipped)
	}
}
