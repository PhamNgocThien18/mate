package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/nguyenngocanh94/mate/internal/coreapi"
)

// The core's client routes (CON-212): what a program of this user's on this
// machine - the console from CON-214, `mate core status` from CON-213 -
// asks the core on its socket, in coreapi's types. They are not on TCP. The
// socket is 0600, so only the user who runs mate reaches it; POST
// coreapi.PathAct takes text the client wrote (a resolve's Input), which
// POST /api/actions never does for a page (it fills an assign's line in
// from the snapshot), so it is not offered where a page could reach it.

// clientRoutes is the socket's site: the three client routes, then every
// route a page has. Each request that names its client (coreapi.ClientHeader)
// is told to Deps.ClientSeen as it starts and as it ends, so a console
// waiting on a long action is still seen.
func (s *Server) clientRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+coreapi.PathCore, s.handleCore)
	mux.HandleFunc("GET "+coreapi.PathSnapshot, s.handleSnapshot)
	mux.HandleFunc("POST "+coreapi.PathAct, s.handleClientAct)
	mux.Handle("/", s.mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if client := r.Header.Get(coreapi.ClientHeader); client != "" && s.deps.ClientSeen != nil {
			s.deps.ClientSeen(client)
			defer s.deps.ClientSeen(client)
		}
		mux.ServeHTTP(w, r)
	})
}

// SocketHandler is the site served on the core's socket: Handler's routes
// with the client routes on top.
func (s *Server) SocketHandler() http.Handler { return s.socket }

// socketOnly answers a client route asked over TCP: there is none there.
func (s *Server) socketOnly(w http.ResponseWriter, r *http.Request) {
	s.writeError(w, r, http.StatusNotFound, r.URL.Path+" is served on the core's socket only", "")
}

// handleCore is GET coreapi.PathCore: the core's own state.
func (s *Server) handleCore(w http.ResponseWriter, _ *http.Request) {
	if s.deps.Core == nil {
		writeClientError(w, http.StatusServiceUnavailable, coreapi.ErrorBody{Error: "this server runs no core"})
		return
	}
	writeClientJSON(w, http.StatusOK, s.deps.Core())
}

// handleSnapshot is GET coreapi.PathSnapshot: the engine's snapshot
// (Deps.Snapshot), without Commands, which are a console's own keys. A
// snapshot that cannot be read - a broken workspace.yaml - is a 503 with
// the read's error.
func (s *Server) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	if s.deps.Snapshot == nil {
		writeClientError(w, http.StatusServiceUnavailable, coreapi.ErrorBody{Error: readOnlyReason})
		return
	}
	snap, err := s.deps.Snapshot(r.Context())
	if err != nil {
		body := coreapi.ErrorBody{Error: err.Error()}
		if errors.Is(err, coreapi.ErrStopping) {
			body = coreapi.ErrorBody{Error: coreapi.ErrStopping.Error(), Code: coreapi.CodeStopping}
		}
		writeClientError(w, http.StatusServiceUnavailable, body)
		return
	}
	// The engine shares its snapshot between readers: only this copy's
	// field is cleared.
	snap.Commands = nil
	writeClientJSON(w, http.StatusOK, snap)
}

// handleClientAct is POST coreapi.PathAct: a coreapi.ActionRequest, run by
// Deps.Act (the engine's Act, under its locks) as the console runs one
// today. Unlike POST /api/actions it does not check the request against a
// fresh snapshot: the client offered it from the snapshot it shows, as the
// console does. The action runs to its end when the client goes away. A
// refusal because the target is busy is a 409 (coreapi.CodeBusy), because
// the core stops a 409 (coreapi.CodeStopping); a Mate with no REPL is a
// 422 (coreapi.CodeNoMateREPL), and any other error of the action's a 422
// with no code.
func (s *Server) handleClientAct(w http.ResponseWriter, r *http.Request) {
	if s.deps.Act == nil {
		writeClientError(w, http.StatusForbidden, coreapi.ErrorBody{Error: readOnlyReason})
		return
	}
	var req coreapi.ActionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxActionBody)).Decode(&req); err != nil {
		writeClientError(w, http.StatusBadRequest, coreapi.ErrorBody{Error: "the action could not be read: " + err.Error()})
		return
	}
	line, err := s.deps.Act(context.WithoutCancel(r.Context()), req)
	if err != nil {
		status, body := clientActError(err)
		writeClientError(w, status, body)
		return
	}
	writeClientJSON(w, http.StatusOK, coreapi.ActResult{Message: line})
}

// clientActError is how POST coreapi.PathAct answers an action's error.
func clientActError(err error) (int, coreapi.ErrorBody) {
	var busy *coreapi.BusyError
	switch {
	case errors.As(err, &busy):
		// The refusal's own line, without whatever wrapped it, as the
		// console shows it (refusedBusy).
		return http.StatusConflict, coreapi.ErrorBody{Error: busy.Error(), Code: coreapi.CodeBusy, Busy: busy}
	case errors.Is(err, coreapi.ErrBusy):
		return http.StatusConflict, coreapi.ErrorBody{Error: err.Error(), Code: coreapi.CodeBusy}
	case errors.Is(err, coreapi.ErrStopping):
		return http.StatusConflict, coreapi.ErrorBody{Error: coreapi.ErrStopping.Error(), Code: coreapi.CodeStopping}
	case errors.Is(err, coreapi.ErrNoMateREPL):
		return http.StatusUnprocessableEntity, coreapi.ErrorBody{Error: err.Error(), Code: coreapi.CodeNoMateREPL}
	}
	return http.StatusUnprocessableEntity, coreapi.ErrorBody{Error: err.Error()}
}

func writeClientJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		writeClientError(w, http.StatusInternalServerError, coreapi.ErrorBody{Error: "the answer could not be encoded: " + err.Error()})
		return
	}
	writeJSON(w, status, body)
}

func writeClientError(w http.ResponseWriter, status int, body coreapi.ErrorBody) {
	b, err := json.Marshal(body)
	if err != nil {
		http.Error(w, body.Error, status)
		return
	}
	writeJSON(w, status, b)
}
