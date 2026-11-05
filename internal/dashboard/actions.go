package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/query"
)

// ActionRequest is one press of an action button on a page: the action's
// wire word (query.ActionAvailability's), the Project, and the Crew when
// the action is a crew's. coreRequest turns it into the core's request,
// the one the console's action sheet sends, so the page and the sheet run
// one function.
type ActionRequest struct {
	Action  string `json:"action"`
	Project string `json:"project"`
	Crew    string `json:"crew,omitempty"`
	// Text is what the captain typed: a message to the Mate (`say`), an
	// answer to a news (`answer`), a question to a news's session (`fork`).
	Text string `json:"text,omitempty"`
	// News is the news an `answer`, a `fork` or a `fork_close` is about,
	// and on `interrupt` the session to stop instead of the Mate's turn.
	News int `json:"news,omitempty"`
	// Work is the row an `okay` ticks on the Mate's work board (CON-158).
	Work int `json:"work,omitempty"`
	// Harness is the agent a new Mate is created with (`onboard`); empty
	// is the workspace's default.
	Harness string `json:"harness,omitempty"`
	// Repo is the repository `add_project` registers, absolute or relative
	// to the workspace root; empty adds a project with no repo yet.
	Repo string `json:"repo,omitempty"`
	// Key names the inbox entry an `assign` hands to the Mate
	// (query.BoxEntry.AssignKey).
	Key string `json:"key,omitempty"`
	// Line is the entry's resolve line, filled in by the server from its
	// own snapshot so a page cannot put words in the Mate's composer.
	Line string `json:"-"`
}

// coreRequest is a page's press in the shape the console's action sheet
// sends the same action (CON-206): the target kinds are the ones its
// entries build (internal/ui/console's actionRequest, restartMateChoice,
// onboardChoice), so cmd/mate's consoleAction branches on a press from
// either the same way. The chat keeps its own actions, with the text in
// Input as a reply's line is and the news in News; the work board's tick
// is the work sheet's okay_work, with the row in Input. Nothing else a
// page typed goes on: an assign hands over Line, the server's own.
//
// A page cannot show a pane Mate, so every press is HeadlessOnly
// (CON-205); the sheet's requests are HeadlessOnly only where the console
// cannot split its terminal.
func coreRequest(req ActionRequest) coreapi.ActionRequest {
	out := coreapi.ActionRequest{Action: coreapi.Action(req.Action), Target: req.Project, HeadlessOnly: true}
	switch {
	case req.Action == actAddProject:
		// The new-project form's request (newProjectChoice).
		out.Action, out.TargetKind, out.Target, out.Input, out.Repo = coreapi.ActionOnboard, "workspace", "", req.Project, req.Repo
	case req.Action == actAssign:
		// The box's [assign] (boxAssignChoice); Line is the server's.
		out.Action, out.TargetKind, out.Crew, out.Input, out.Key = coreapi.ActionResolve, "project", req.Crew, req.Line, req.Key
	case req.Action == actOkay:
		// The work sheet's Space (okayWork).
		out.Action, out.TargetKind, out.Input = coreapi.ActionOkayWork, "project", strconv.Itoa(req.Work)
	case talk[req.Action]:
		// A say has no news, and an interrupt or a fork_close no text.
		out.TargetKind = "mate"
		if req.Action != actInterrupt && req.Action != actForkClose {
			out.Input = req.Text
		}
		if req.Action != actSay {
			out.News = req.News
		}
	case req.Crew != "":
		out.TargetKind, out.Crew = "crew", req.Crew
	case out.Action == coreapi.ActionOnboard:
		out.TargetKind, out.Harness = "project-mate", query.HarnessKind(req.Harness)
	case out.Action == coreapi.ActionRestartMate, out.Action == coreapi.ActionClearComposer, out.Action == coreapi.ActionMode,
		out.Action == coreapi.ActionRemoveProject:
		out.TargetKind = "project"
	default:
		out.TargetKind = "mate"
	}
	return out
}

// Button is one action button: what it does in the console's words,
// whether it can run now and, when it cannot, why. Confirm is the question a page asks
// before it runs a destructive action, empty for one that runs at once.
type Button struct {
	Action    string `json:"action"`
	Label     string `json:"label"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	Confirm   string `json:"confirm,omitempty"`
	// Key is the inbox entry an `assign` button hands to the Mate.
	Key string `json:"key,omitempty"`
}

// ActionsResponse is GET /api/projects/{project}/actions (CON-141): the
// Mate's buttons and each open crew's, from the console's snapshot.
// ReadOnly is set, with the reason, when this dashboard cannot act at all;
// Mate and Crews are then empty.
type ActionsResponse struct {
	envelope
	Project  string              `json:"project"`
	ReadOnly string              `json:"read_only,omitempty"`
	Mate     []Button            `json:"mate"`
	Crews    map[string][]Button `json:"crews"`
	// The City's buttons beyond the console sheet (CON-142); see city.go.
	MateMore  []Button            `json:"mate_more"`
	CrewMore  map[string][]Button `json:"crew_more"`
	Harnesses []HarnessChoice     `json:"harnesses"`
	Daemon    *Daemon             `json:"daemon,omitempty"`
}

// ActionResult is POST /api/actions answered: the console's one line.
type ActionResult struct {
	envelope
	Line string `json:"line"`
}

// readOnlyReason is what a dashboard without the console's actions says.
const readOnlyReason = "this dashboard is read-only: actions run in the dashboard `mate console` serves"

// handleActions is GET /api/projects/{project}/actions. Availability is the
// query layer's, the one answer the console's sheet reads too; the labels
// and the questions are the sheet's.
func (s *Server) handleActions(ctx context.Context, r *http.Request, env envelope) (any, error) {
	project, err := s.project(r)
	if err != nil {
		return nil, err
	}
	out := ActionsResponse{envelope: env, Project: project, Mate: []Button{}, Crews: map[string][]Button{},
		MateMore: []Button{}, CrewMore: map[string][]Button{}, Harnesses: []HarnessChoice{}}
	if s.deps.Snapshot == nil || s.deps.Act == nil {
		out.ReadOnly = readOnlyReason
		return out, nil
	}
	snap, err := s.deps.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	node, err := nodeOf(snap, project)
	if err != nil {
		return nil, err
	}
	s.fillActions(&out, snap, node)
	return out, nil
}

// fillActions puts one project's buttons from snap into out.
func (s *Server) fillActions(out *ActionsResponse, snap query.Snapshot, node query.ProjectNode) {
	out.Mate = mateButtons(node)
	out.MateMore = s.mateMore(node)
	for _, c := range node.Crews {
		out.Crews[c.CrewID] = crewButtons(node.ProjectID, c)
		out.CrewMore[c.CrewID] = s.crewMore(node, c)
	}
	out.Harnesses = harnessChoices(snap)
	out.Daemon = daemonOf(node)
}

// AllActionsResponse is GET /api/actions: every project's buttons from one
// read of the console's snapshot, which is what the City polls - one
// snapshot per answer however many projects there are.
type AllActionsResponse struct {
	envelope
	ReadOnly string                     `json:"read_only,omitempty"`
	Projects map[string]ActionsResponse `json:"projects"`
}

func (s *Server) handleAllActions(ctx context.Context, _ *http.Request, env envelope) (any, error) {
	out := AllActionsResponse{envelope: env, Projects: map[string]ActionsResponse{}}
	if s.deps.Snapshot == nil || s.deps.Act == nil {
		out.ReadOnly = readOnlyReason
		return out, nil
	}
	snap, err := s.deps.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	for _, ref := range s.ws.Projects() {
		node, err := nodeOf(snap, ref.Name)
		if err != nil {
			continue
		}
		one := ActionsResponse{Project: ref.Name, Mate: []Button{}, Crews: map[string][]Button{},
			MateMore: []Button{}, CrewMore: map[string][]Button{}, Harnesses: []HarnessChoice{}}
		s.fillActions(&one, snap, node)
		out.Projects[ref.Name] = one
	}
	return out, nil
}

// projectNode is one Project of the console's snapshot.
func (s *Server) projectNode(ctx context.Context, project string) (query.ProjectNode, error) {
	snap, err := s.deps.Snapshot(ctx)
	if err != nil {
		return query.ProjectNode{}, err
	}
	return nodeOf(snap, project)
}

func nodeOf(snap query.Snapshot, project string) (query.ProjectNode, error) {
	for _, p := range snap.Projects {
		if p.ProjectID == project || p.Name == project {
			return p, nil
		}
	}
	return query.ProjectNode{}, notFound("project %q is not in the console's snapshot", project)
}

// mateButtons is the Mate row's sheet, in its order: start (or resume, or
// create), stop, restart, mode, clear composer.
func mateButtons(p query.ProjectNode) []Button {
	acts := p.Mate.Actions
	start := button(acts, "start", "Start mate", "")
	switch {
	case lookup(acts, "onboard").Available:
		start = button(acts, "onboard", "Create mate", "")
	case lookup(acts, "resume").Available:
		start = button(acts, "resume", "Resume mate", "")
	}
	to := "auto"
	if p.Mode == query.ModeAuto {
		to = "manual"
	}
	return []Button{
		start,
		button(acts, "stop", "Stop mate…",
			"Stop the Mate of "+p.ProjectID+"? This Mate only; its crews keep running. The agent stops and the binding is released once the service confirms it."),
		button(acts, "restart_mate", "Restart mate…",
			"Restart the Mate of "+p.ProjectID+"? This Mate only; its crews keep running. The Mate is stopped and started again."),
		{Action: "mode", Label: "Mode → " + to, Available: true},
		button(acts, "clear_composer", "Clear composer", ""),
	}
}

// crewButtons is a Crew row's sheet, in its order: stop, restart, merge,
// diff. Repair binding is left out: the console offers it, but repairing a
// crew is not wired behind it yet and every press fails.
func crewButtons(project string, c query.CrewNode) []Button {
	acts := c.Actions
	branch, into := "this crew's branch", "the default branch"
	if c.Worktree.IsKnown() && c.Worktree.Value.Branch != "" {
		branch = c.Worktree.Value.Branch
	}
	if c.Repo.IsKnown() && c.Repo.Value.DefaultBranch != "" {
		into = c.Repo.Value.DefaultBranch
	}
	name := project + "/" + c.CrewID
	return []Button{
		button(acts, "stop", "Stop crew…",
			"Close "+name+"? Its agent, tab, worktree and branch go; its brief, report and status file stay. Refused, changing nothing, if the branch is not landed or the worktree is dirty."),
		button(acts, "restart_crew", "Restart crew…",
			"Restart "+name+"? A live agent is stopped first; then the harness starts again in the same worktree, and its branch and status file stay."),
		button(acts, "merge", "Merge…",
			"Merge "+branch+" into "+into+" and finish crew "+c.CrewID+"? Fast-forward only; anything else refuses and changes nothing."),
		button(acts, "diff", "Diff", ""),
	}
}

func button(acts []query.ActionAvailability, name, label, confirm string) Button {
	a := lookup(acts, name)
	b := Button{Action: name, Label: label, Available: a.Available, Confirm: confirm}
	if !a.Available {
		b.Reason = a.Reason
		if b.Reason == "" {
			b.Reason = "not offered for this row"
		}
	}
	return b
}

func lookup(acts []query.ActionAvailability, name string) query.ActionAvailability {
	for _, a := range acts {
		if a.Action == name {
			return a
		}
	}
	return query.ActionAvailability{Action: name}
}

// actionLocks is which targets have an action running, so a second press -
// a double click, or a phone and a laptop at once - is refused rather than
// run twice.
type actionLocks struct {
	mu      sync.Mutex
	running map[string]string
}

func (l *actionLocks) take(target, action string) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.running == nil {
		l.running = map[string]string{}
	}
	if busy, ok := l.running[target]; ok {
		return busy, false
	}
	l.running[target] = action
	return "", true
}

func (l *actionLocks) release(target string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.running, target)
}

// handleAct is POST /api/actions. It is the one handler that changes
// anything, so it asks more of a request than any GET does:
//
//   - the body is JSON, which a cross-site form cannot send without a
//     preflight this server never answers;
//   - an Origin, when the browser sends one, is this server's own;
//   - the action is re-checked against a fresh snapshot, so a button drawn
//     a minute ago cannot run what is no longer allowed.
//
// It is not authentication: anyone who can open the page can press it
// (CON-141 leaves that to the LAN). The action runs to the end even when
// the page goes away, because a start or a merge stopped halfway is worse
// than one nobody watched finish.
func (s *Server) handleAct(w http.ResponseWriter, r *http.Request) {
	if s.deps.Snapshot == nil || s.deps.Act == nil {
		s.writeError(w, r, http.StatusForbidden, readOnlyReason, "")
		return
	}
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		s.writeError(w, r, http.StatusUnsupportedMediaType, "an action is a JSON body (Content-Type: application/json)", "")
		return
	}
	if !sameOrigin(r) {
		s.writeError(w, r, http.StatusForbidden, "an action is only taken from this dashboard's own pages", "Origin "+r.Header.Get("Origin"))
		return
	}
	var req ActionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxActionBody)).Decode(&req); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "the action could not be read", err.Error())
		return
	}
	req.Action, req.Project, req.Crew = strings.TrimSpace(req.Action), strings.TrimSpace(req.Project), strings.TrimSpace(req.Crew)
	if req.Action == actAddProject {
		s.addProject(w, r, req)
		return
	}
	if _, ok := s.ws.Project(req.Project); !ok {
		s.writeError(w, r, http.StatusNotFound, unknownProject(s.ws, req.Project), "")
		return
	}
	ctx := context.WithoutCancel(r.Context())
	node, err := s.projectNode(ctx, req.Project)
	if err != nil {
		var missing errNotFound
		if errors.As(err, &missing) {
			s.writeError(w, r, http.StatusNotFound, missing.reason, "")
			return
		}
		s.writeError(w, r, http.StatusInternalServerError, "the workspace could not be read", err.Error())
		return
	}
	buttons := append(mateButtons(node), s.mateMore(node)...)
	target := req.Project
	if req.Crew != "" {
		buttons, target = nil, req.Project+"/"+req.Crew
		for _, c := range node.Crews {
			if c.CrewID == req.Crew {
				buttons = append(crewButtons(node.ProjectID, c), s.crewMore(node, c)...)
			}
		}
		if buttons == nil {
			s.writeError(w, r, http.StatusNotFound, "crew "+req.Crew+" is not open in "+req.Project, "")
			return
		}
	}
	var pressed *Button
	want := req.Action
	if talk[want] {
		// An answer, a session and an interrupt are the chat's, and can
		// run when a message can be sent.
		want = actSay
	}
	for i := range buttons {
		if buttons[i].Action == want && (want != actAssign || buttons[i].Key == req.Key) {
			pressed = &buttons[i]
		}
	}
	switch {
	case pressed == nil:
		s.writeError(w, r, http.StatusBadRequest, req.Action+" is not an action this dashboard offers here", "")
		return
	case !pressed.Available:
		s.writeError(w, r, http.StatusConflict, "Action refused: "+pressed.Reason+" · nothing started", "")
		return
	}
	if pressed.Action == actAssign {
		req.Line = s.resolveLine(node, req.Crew, req.Key)
	}
	if talk[req.Action] || req.Action == actPeek || req.Action == actOkay {
		// Talking, reading a screen and ticking the board do not wait
		// for, or hold up, a stop or a merge on the same Mate or crew.
		target += "#" + req.Action
	}
	if req.Action == actOkay && req.Work <= 0 {
		s.writeError(w, r, http.StatusBadRequest, "okay wants the work row it ticks", "")
		return
	}
	if busy, ok := s.acting.take(target, req.Action); !ok {
		s.writeError(w, r, http.StatusConflict, "Action refused: "+busy+" is still running on "+target+" · nothing started", "")
		return
	}
	defer s.acting.release(target)
	s.run(w, r, ctx, req)
}

// run hands a checked request to the console, as the core's request, and
// answers with its one line, or with the service's error verbatim as a
// 422: it ran and failed, which a page tells apart from a 409, refused and
// nothing changed.
func (s *Server) run(w http.ResponseWriter, r *http.Request, ctx context.Context, req ActionRequest) {
	line, err := s.deps.Act(ctx, coreRequest(req))
	if err != nil {
		s.writeError(w, r, http.StatusUnprocessableEntity, err.Error(), "")
		return
	}
	id, _ := s.lastEventID(ctx)
	body, err := json.Marshal(ActionResult{envelope: envelope{GeneratedAt: db.FormatTime(s.deps.now()), LastEventID: id}, Line: line})
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "the answer could not be encoded", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, body)
}

// sameOrigin is true when the request carries no Origin (a same-origin GET
// in some browsers, or curl) or an Origin whose host is the one the request
// was sent to.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}
