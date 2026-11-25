package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/sthefanocordeiro/kloxus/internal/pipeline"
)

// apiRun is the run resource snapshot.
type apiRun struct {
	ID            string         `json:"id"`
	Kind          string         `json:"kind"`
	Status        string         `json:"status"` // running | done | failed | blocked
	Step          string         `json:"step"`
	IssueID       int64          `json:"issue_id,omitempty"`
	PublicationID int64          `json:"publication_id,omitempty"`
	NoNewItems    bool           `json:"no_new_items"`
	Error         string         `json:"error,omitempty"`
	Blocked       bool           `json:"blocked,omitempty"`
	BlockedReason string         `json:"blocked_reason,omitempty"`
	Sample        bool           `json:"sample,omitempty"`
	Events        []runEventView `json:"events"`
}

type runEventView struct {
	Seq    int            `json:"seq"`
	Step   string         `json:"step"`
	Counts map[string]int `json:"counts,omitempty"`
}

func runView(st *runProgress) apiRun {
	st.mu.Lock()
	defer st.mu.Unlock()
	v := apiRun{
		ID: st.st.RunID, Kind: st.st.Kind, Step: st.st.Step,
		IssueID: st.st.IssueID, PublicationID: st.st.PubID,
		NoNewItems: st.st.NoNewItems, Error: st.st.Err,
		Blocked: st.st.Blocked, BlockedReason: st.st.BlockedReason, Sample: st.st.Sample,
		Status: "running",
	}
	if st.st.Done {
		v.Status = "done"
		if st.st.Err != "" {
			v.Status = "failed"
		}
		if st.st.Blocked {
			v.Status = "blocked"
		}
	}
	for i, ev := range st.events {
		v.Events = append(v.Events, runEventView{Seq: i + 1, Step: ev.Step, Counts: ev.Counts})
	}
	if v.Events == nil {
		v.Events = []runEventView{}
	}
	return v
}

func (s *Server) apiRunStart(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	st := s.startRun(reqWS(r), pub)
	apiJSON(w, 202, map[string]any{"run": runView(st)})
}

func (s *Server) apiRunLatest(w http.ResponseWriter, r *http.Request) {
	pub, err := s.apiPub(r)
	if apiDBErr(w, r, err, "publication") {
		return
	}
	v, ok := reqWS(r).Runs.Load(pub.ID)
	if !ok {
		apiErr(w, 404, "no_runs", "no runs yet for this publication")
		return
	}
	apiJSON(w, 200, map[string]any{"run": runView(v.(*runProgress))})
}

func (s *Server) apiRun(w http.ResponseWriter, r *http.Request) {
	st, ok := findRun(reqWS(r), r.PathValue("id"))
	if !ok {
		apiErr(w, 404, "not_found", "run not found")
		return
	}
	apiJSON(w, 200, map[string]any{"run": runView(st)})
}

// apiRunEvents streams one SSE event per finished step with its real
// counts, then a final "done" event with the run snapshot.
func (s *Server) apiRunEvents(w http.ResponseWriter, r *http.Request) {
	st, ok := findRun(reqWS(r), r.PathValue("id"))
	if !ok {
		apiErr(w, 404, "not_found", "run not found")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		apiErr(w, 500, "internal", "streaming not supported")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	send := func(event string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		flusher.Flush()
	}
	sent := 0
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	// The server's 60s WriteTimeout would kill a stream held open by a
	// long run — renew the deadline every tick so a live handler keeps
	// its connection while a wedged one still dies.
	rc := http.NewResponseController(w)
	for {
		_ = rc.SetWriteDeadline(time.Now().Add(30 * time.Second))
		st.mu.Lock()
		evs := append([]pipeline.StepEvent{}, st.events...)
		done := st.st.Done
		st.mu.Unlock()
		snap := runView(st)
		for ; sent < len(evs); sent++ {
			send("step", runEventView{Seq: sent + 1, Step: evs[sent].Step, Counts: evs[sent].Counts})
		}
		if done {
			send("done", snap)
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}
	}
}
