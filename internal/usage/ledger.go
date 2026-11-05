package usage

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/timeline"
	"github.com/nguyenngocanh94/mate/internal/timeline/read"
)

// ledgerRow is one line of the ledger table: the Mate's own row, built by
// mateLedgerRow, or one task's row of `v_task_ledger`.
type ledgerRow struct {
	ID         string
	State      string
	Turns      int64
	In         int64
	Cached     int64
	CacheRead  int64
	CacheWrite int64
	Context    sql.NullInt64
	// CtxAvg and CtxMax are the mean and the largest context over the
	// row's calls that recorded one; invalid when none did.
	CtxAvg sql.NullInt64
	CtxMax sql.NullInt64
	// CtxSum and CtxCalls are the sum of those contexts and how many calls
	// recorded one, so a group of rows can weigh its mean by those calls.
	CtxSum   int64
	CtxCalls int64
	// Model and Effort are the row's last call's, falling back to the
	// launch profile in `.meta` when no call recorded them (CON-75).
	Model    string
	Effort   string
	Harness  string
	Out      int64
	Total    int64
	Cost     sql.NullFloat64
	CtxPct   sql.NullFloat64
	Asked    int64
	WaitedMs int64
	Span     string
	// Reviews are the reviewers `mate review` started for this task, and
	// ReviewOf the task this row reviewed (CON-75). TaskTotal is Total plus
	// every reviewer's Total for a task; zero for a reviewer row, whose
	// tokens are in its task's.
	Reviews   []string
	ReviewOf  string
	TaskTotal int64
	// Budget is the task's token ceiling (CON-94): the crew's `budget=`,
	// else the project's crew_tokens, zero when neither. TaskTotal is the
	// spend measured against it, the same total the observer checks.
	Budget int64
}

// writeLedger is the no-crew form: the Mate's row first (mvp.md M5 task
// 27), then every task the project has ever recorded, oldest spawn first.
// A positive top keeps the Mate's row and the top largest tasks by TOTAL,
// largest first; the footer still counts every row, and a last line says
// how many tasks were left out.
func writeLedger(ctx context.Context, out io.Writer, w *store.Workspace, handle *db.DB, harnesses harness.Registry, project string, top int) error {
	mate, err := mateLedgerRow(ctx, handle, w, project)
	if err != nil {
		return err
	}
	rows := []ledgerRow{mate}

	summaries, err := spawn.ListCrews(w, project)
	if err != nil {
		return err
	}
	stateByID := make(map[string]string, len(summaries))
	harnessByID := make(map[string]string, len(summaries))
	profileByID := make(map[string]spawn.CrewSummary, len(summaries))
	for _, s := range summaries {
		stateByID[s.Crew] = s.State
		harnessByID[s.Crew] = s.Harness
		profileByID[s.Crew] = s
	}

	ledger, err := read.UsageTasks(ctx, handle.SQL(), project)
	if err != nil {
		return err
	}
	for _, t := range ledger {
		crew, model, effort := t.Crew, t.Model, t.Effort
		cacheRead, cacheWrite := t.CacheRead, t.CacheWrite
		if model == "" {
			model = profileByID[crew].Model
		}
		if effort == "" {
			effort = profileByID[crew].Effort
		}
		rows = append(rows, ledgerRow{
			ID: crew, State: stateByID[crew], Harness: harnessByID[crew], Model: model, Effort: effort, Turns: t.Turns,
			CtxAvg: t.ContextAvg, CtxMax: t.ContextMax, CtxSum: t.ContextSum, CtxCalls: t.ContextCalls,
			In: t.Input, Cached: cacheRead + cacheWrite, Out: t.Output, Total: t.Input + cacheRead + cacheWrite + t.Output,
			Cost: t.Cost, CtxPct: t.ContextPct, Context: t.ContextTokens, CacheRead: cacheRead, CacheWrite: cacheWrite, Asked: t.Questions, WaitedMs: t.WaitedMs,
			Span: spanWord(t.SpawnedAt, t.ClosedAt),
		})
	}
	reviewersOf, reviewOf, err := w.ReviewLinks(project)
	if err != nil {
		return err
	}
	rollUpReviews(rows[1:], reviewersOf, reviewOf)
	if err := fillBudgets(w, project, rows[1:]); err != nil {
		return err
	}

	shown := rows
	tasks := 0
	for _, r := range rows[1:] {
		if r.ReviewOf == "" {
			tasks++
		}
	}
	if top > 0 && tasks > top {
		shown = append([]ledgerRow{mate}, topTasks(rows[1:], top)...)
	}
	printLedgerRows(out, shown, rows)
	if len(shown) < len(rows) {
		fmt.Fprintf(out, "showing the mate and the %d largest of %d task(s) by TASK-TOTAL, each with its reviewers; the total line counts all of them\n", top, tasks)
	}
	printByModel(out, rows[1:])
	printCompactWarning(out, harnesses, rows[1:], w.CrewAutocompact())
	return nil
}

// rollUpReviews fills Reviews, ReviewOf and TaskTotal (CON-75): a task's
// TaskTotal is its own Total plus its reviewers', a reviewer's is zero. A
// reviewer whose task has no row of its own stands as a task.
func rollUpReviews(rows []ledgerRow, reviewersOf map[string][]string, reviewOf map[string]string) {
	index := make(map[string]int, len(rows))
	for i, r := range rows {
		index[r.ID] = i
	}
	for i := range rows {
		r := &rows[i]
		if task, ok := reviewOf[r.ID]; ok {
			if _, known := index[task]; known {
				r.ReviewOf = task
				continue
			}
		}
		r.Reviews = reviewersOf[r.ID]
		r.TaskTotal = r.Total
		for _, reviewer := range r.Reviews {
			if j, ok := index[reviewer]; ok {
				r.TaskTotal += rows[j].Total
			}
		}
	}
}

// topTasks is the n largest tasks by TaskTotal, largest first, each
// followed by its reviewers in the order the ledger has them.
func topTasks(rows []ledgerRow, n int) []ledgerRow {
	ranked := make([]ledgerRow, 0, len(rows))
	for _, r := range rows {
		if r.ReviewOf == "" {
			ranked = append(ranked, r)
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].TaskTotal > ranked[j].TaskTotal })
	if len(ranked) > n {
		ranked = ranked[:n]
	}
	var out []ledgerRow
	for _, task := range ranked {
		out = append(out, task)
		for _, r := range rows {
			if r.ReviewOf == task.ID {
				out = append(out, r)
			}
		}
	}
	return out
}

// printByModel is one line per model and effort the project's tasks last
// ran on: how many tasks, model calls per task and context per call, over
// every task whatever --top shows. They are the numbers CON-68 measures a
// change to the Crew launch against (hellovietnam 2026-10-14: Claude Crews
// 39 calls per task, 162k context per call), and with effort in the key
// (CON-75) Opus high and Sonnet medium are two lines, not one.
func printByModel(out io.Writer, tasks []ledgerRow) {
	type group struct {
		tasks, calls, ctxCalls, ctxSum int64
	}
	groups := map[string]*group{}
	var order []string
	for _, r := range tasks {
		// A task that never made a call (a spawn that failed) has no model
		// and would only dilute calls/task.
		if r.Turns == 0 {
			continue
		}
		key := dashIfEmpty(r.Model)
		if r.Effort != "" {
			key += " " + r.Effort
		}
		g := groups[key]
		if g == nil {
			g = &group{}
			groups[key] = g
			order = append(order, key)
		}
		g.tasks++
		g.calls += r.Turns
		// The group's mean is over the calls that recorded a context, the
		// same calls each task's CTX-AVG is over.
		g.ctxCalls += r.CtxCalls
		g.ctxSum += r.CtxSum
	}
	if len(order) == 0 {
		return
	}
	sort.Strings(order)
	fmt.Fprintln(out, "by model and effort (each task's last call):")
	for _, key := range order {
		g := groups[key]
		ctx := "?"
		if g.ctxCalls > 0 {
			ctx = query.HumanizeTokens(g.ctxSum / g.ctxCalls)
		}
		fmt.Fprintf(out, "  %s: %d task(s), %.1f calls/task, %s context/call\n",
			key, g.tasks, float64(g.calls)/float64(g.tasks), ctx)
	}
}

// takesAutocompact reports whether a Crew on harness kind is launched with
// the workspace's crew autocompact threshold.
func takesAutocompact(harnesses harness.Registry, kind string) bool {
	p, err := harnesses.Lookup(harness.Kind(kind))
	return err == nil && p.Info().Autocompact
}

// printCompactWarning names the Crews whose context passed the
// workspace's crew autocompact threshold: a Crew launched with the flag
// compacts before it, so one above it either predates the flag or did not
// compact. Only a harness whose Info says Autocompact takes the threshold
// (today Claude Code); a threshold of 0 (off) warns about nothing.
func printCompactWarning(out io.Writer, harnesses harness.Registry, tasks []ledgerRow, threshold int) {
	if threshold <= 0 {
		return
	}
	var over []ledgerRow
	for _, r := range tasks {
		if takesAutocompact(harnesses, r.Harness) && r.CtxMax.Valid && r.CtxMax.Int64 > int64(threshold) {
			over = append(over, r)
		}
	}
	if len(over) == 0 {
		return
	}
	sort.SliceStable(over, func(i, j int) bool { return over[i].CtxMax.Int64 > over[j].CtxMax.Int64 })
	const named = 5
	var parts []string
	for _, r := range over[:min(named, len(over))] {
		parts = append(parts, r.ID+" "+query.HumanizeTokens(r.CtxMax.Int64))
	}
	more := ""
	if len(over) > named {
		more = fmt.Sprintf(" and %d more", len(over)-named)
	}
	fmt.Fprintf(out, "warning: %d crew(s) passed the %s crew autocompact threshold (crew.autocompact in .mate/workspace.yaml): %s%s\n",
		len(over), query.HumanizeTokens(int64(threshold)), strings.Join(parts, ", "), more)
}

// mateLedgerRow computes the Mate's own row the same way `v_task_ledger`
// computes a crew's: cost only counts a turn whose model has a real price,
// and context_pct is the most recent turn's. There is no task row for a
// Mate - its turns belong to the project rather than to any one task
// (docs/timeline.md) - so State and Span are dashes and Asked/WaitedMs are
// zero: those columns are a task's shape, not a Mate's.
func mateLedgerRow(ctx context.Context, handle *db.DB, w *store.Workspace, project string) (ledgerRow, error) {
	row := ledgerRow{ID: "mate", State: "-", Span: "-"}
	actorID := timeline.MateActorID(project)

	model, effort, err := read.LastModel(ctx, handle.SQL(), actorID)
	if err != nil {
		return row, err
	}
	row.Model, row.Effort = model, effort

	totals, err := read.ActorTotals(ctx, handle.SQL(), actorID)
	if err != nil {
		return row, err
	}
	row.Turns, row.In, row.Out = totals.Turns, totals.Input, totals.Output
	row.Cached = totals.CacheRead + totals.CacheWrite
	row.CacheRead, row.CacheWrite = totals.CacheRead, totals.CacheWrite
	row.Total = row.In + row.Cached + row.Out

	if row.Cost, err = read.ActorCost(ctx, handle.SQL(), actorID); err != nil {
		return row, err
	}

	stats, err := read.ActorContext(ctx, handle.SQL(), actorID)
	if err != nil {
		return row, err
	}
	row.CtxAvg, row.CtxMax = stats.Avg, stats.Max

	if usage, ok, err := handle.LatestContext(ctx, actorID, ""); err != nil {
		return row, err
	} else if ok {
		row.Context = sql.NullInt64{Int64: usage.Tokens, Valid: true}
		if usage.Pct != nil {
			row.CtxPct = sql.NullFloat64{Float64: *usage.Pct, Valid: true}
		}
	}
	if row.Model == "" || row.Effort == "" {
		if meta, err := w.ReadMateMeta(project); err == nil {
			if row.Model == "" {
				row.Model = meta[spawn.MetaModel]
			}
			if row.Effort == "" {
				row.Effort = meta[spawn.MetaEffort]
			}
		}
	}

	return row, nil
}

// spanWord is the SPAWN→CLOSE column: local times, "→ open" for a task
// still in flight, and a dash for a row (the Mate's) that has neither.
func spanWord(spawnedAt, closedAt sql.NullString) string {
	if !spawnedAt.Valid || spawnedAt.String == "" {
		return "-"
	}
	start := db.ParseTime(spawnedAt.String).Local().Format("15:04:05")
	if closedAt.Valid && closedAt.String != "" {
		return start + " → " + db.ParseTime(closedAt.String).Local().Format("15:04:05")
	}
	return start + " → open"
}

// printLedgerTable renders the columns of docs/mvp.md M5 task 27, humanised
// (query.HumanizeTokens, query.HumanizeCost), with a totals footer.
func printLedgerTable(out io.Writer, rows []ledgerRow) {
	printLedgerRows(out, rows, rows)
}

// printLedgerRows prints shown as the table and sums the footer over all,
// so a cut table never understates what the project spent.
func printLedgerRows(out io.Writer, shown, all []ledgerRow) {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATE\tMODEL\tEFFORT\tTURNS\tIN\tCACHE-READ\tCACHE-WRITE\tOUT\tTOTAL\tTASK-TOTAL\tBUDGET\t%\tREVIEWS\tCOST\tCTX\tCTX-AVG\tCTX%\tASKED\tWAITED\tSPAWN→CLOSE")

	var totalTurns, totalIn, totalCached, totalOut, totalTotal, totalAsked, totalWaited int64
	var totalCost float64
	haveCost := false
	for _, r := range shown {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			r.ID, dashIfEmpty(r.State), dashIfEmpty(r.Model), dashIfEmpty(r.Effort), r.Turns,
			query.HumanizeTokens(r.In), query.HumanizeTokens(r.CacheRead), query.HumanizeTokens(r.CacheWrite), query.HumanizeTokens(r.Out), query.HumanizeTokens(r.Total),
			taskTotalWord(r), budgetColumn(r), budgetPercent(r), reviewsWord(r),
			costWord(r.Cost), contextWord(r.Context), contextWord(r.CtxAvg), ctxWord(r.CtxPct), r.Asked, waitedWord(r.WaitedMs), r.Span)
	}
	for _, r := range all {
		totalTurns += r.Turns
		totalIn += r.In
		totalCached += r.Cached
		totalOut += r.Out
		totalTotal += r.Total
		totalAsked += r.Asked
		totalWaited += r.WaitedMs
		if r.Cost.Valid {
			totalCost += r.Cost.Float64
			haveCost = true
		}
	}
	_ = tw.Flush()

	cost := "?"
	if haveCost {
		cost = query.HumanizeCost(totalCost)
	}
	fmt.Fprintf(out, "total: %d turn(s), %s in, %s cached, %s out, %s total, %s cost, %d asked, %s waited\n",
		totalTurns, query.HumanizeTokens(totalIn), query.HumanizeTokens(totalCached), query.HumanizeTokens(totalOut),
		query.HumanizeTokens(totalTotal), cost, totalAsked, waitedWord(totalWaited))
}

// taskTotalWord is TASK-TOTAL: the task with its reviewers, a dash for a
// reviewer row (its tokens are in its task's) and for the Mate.
func taskTotalWord(r ledgerRow) string {
	if r.ReviewOf != "" || r.ID == "mate" {
		return "-"
	}
	return query.HumanizeTokens(r.TaskTotal)
}

// fillBudgets sets each task row's Budget: the crew's own `budget=` (the
// dispatch rule's, or an extension), else the project's crew_tokens. A
// reviewer row has none of its own; its tokens count in its task's.
func fillBudgets(w *store.Workspace, project string, rows []ledgerRow) error {
	cfg, err := w.LoadProject(project)
	if err != nil {
		return err
	}
	var projectTokens int64
	if cfg.Budget != nil {
		projectTokens = cfg.Budget.CrewTokens
	}
	for i := range rows {
		r := &rows[i]
		if r.ReviewOf != "" {
			continue
		}
		r.Budget = projectTokens
		meta, err := w.ReadCrewMeta(project, r.ID)
		if err != nil {
			continue
		}
		if n, err := strconv.ParseInt(strings.TrimSpace(meta[store.MetaBudget]), 10, 64); err == nil && n > 0 {
			r.Budget = n
		}
	}
	return nil
}

// budgetColumn is BUDGET, and budgetPercent `%`: TASK-TOTAL as a share of
// it, a dash where there is no budget to hold the task to.
func budgetColumn(r ledgerRow) string {
	if r.Budget <= 0 || r.ReviewOf != "" || r.ID == "mate" {
		return "-"
	}
	return query.HumanizeTokens(r.Budget)
}

func budgetPercent(r ledgerRow) string {
	if r.Budget <= 0 || r.ReviewOf != "" || r.ID == "mate" {
		return "-"
	}
	return fmt.Sprintf("%d%%", r.TaskTotal*100/r.Budget)
}

// reviewsWord is REVIEWS: how many reviewers a task had, or which task a
// reviewer row reviewed.
func reviewsWord(r ledgerRow) string {
	switch {
	case r.ReviewOf != "":
		return "for " + r.ReviewOf
	case len(r.Reviews) > 0:
		return fmt.Sprint(len(r.Reviews))
	}
	return "-"
}

// costWord and ctxWord render a nullable value as "?" - a missing price or
// an unknown context window, never zero (mvp.md M5: "cost is NULL until
// then, because a missing price is not a price of zero").
func costWord(v sql.NullFloat64) string {
	if !v.Valid {
		return "?"
	}
	return query.HumanizeCost(v.Float64)
}

func ctxWord(v sql.NullFloat64) string {
	if !v.Valid {
		return "?"
	}
	return fmt.Sprintf("%.0f%%", v.Float64)
}

// waitedWord renders a millisecond total the way formatAge renders an age:
// the largest whole unit that keeps the number readable.
func waitedWord(ms int64) string {
	if ms <= 0 {
		return "0s"
	}
	return formatAge(time.Duration(ms) * time.Millisecond)
}
