package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
)

type itemJSON struct {
	ID                 string          `json:"id"`
	Key                string          `json:"key"`
	Project            string          `json:"project"`
	Kind               tracker.Kind    `json:"kind"`
	Title              string          `json:"title"`
	Description        string          `json:"description"`
	State              tracker.State   `json:"state"`
	Stage              string          `json:"stage,omitempty"`
	Type               string          `json:"type,omitempty"`
	AcceptanceCriteria []string        `json:"acceptance_criteria,omitempty"`
	Policy             *tracker.Policy `json:"policy,omitempty"`
	Epic               string          `json:"epic,omitempty"`
	Milestone          string          `json:"milestone,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
	Version            int64           `json:"version"`
}

func toItemJSON(v app.ItemView) itemJSON {
	out := itemJSON{
		ID: v.ID, Key: v.Key, Project: v.ProjectKey, Kind: v.Kind, Title: v.Title, Description: v.Description,
		State: v.State, Stage: v.Stage, Epic: v.EpicKey, Milestone: v.MilestoneKey,
		CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, Version: v.Version,
	}
	if v.Kind == tracker.KindTicket {
		out.Type = string(v.Type)
		out.AcceptanceCriteria = v.AcceptanceCriteria
		if out.AcceptanceCriteria == nil {
			out.AcceptanceCriteria = []string{}
		}
		policy := v.Policy
		out.Policy = &policy
	}
	return out
}

type eventJSON struct {
	Seq        int64           `json:"seq"`
	Type       string          `json:"type"`
	OccurredAt time.Time       `json:"occurred_at"`
	Actor      event.Actor     `json:"actor"`
	Payload    json.RawMessage `json:"payload,omitempty"`
}

func registerTracker(mux *http.ServeMux, t *app.Tracker) {
	mux.HandleFunc("POST /api/v1/projects/{project}/items", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Kind               tracker.Kind       `json:"kind"`
			Title              string             `json:"title"`
			Description        string             `json:"description"`
			Type               tracker.TicketType `json:"type"`
			AcceptanceCriteria []string           `json:"acceptance_criteria"`
			Policy             *tracker.Policy    `json:"policy"`
			Epic               string             `json:"epic"`
			Milestone          string             `json:"milestone"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, err)
			return
		}
		v, err := t.CreateItem(r.Context(), app.CreateItemInput{
			ProjectKey: r.PathValue("project"), Kind: in.Kind, Title: in.Title, Description: in.Description,
			Type: in.Type, AcceptanceCriteria: in.AcceptanceCriteria, Policy: in.Policy,
			EpicKey: in.Epic, MilestoneKey: in.Milestone,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, toItemJSON(v))
	})

	mux.HandleFunc("GET /api/v1/projects/{project}/items", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		items, err := t.ListItems(r.Context(), r.PathValue("project"),
			tracker.Kind(q.Get("kind")), tracker.State(q.Get("state")), q.Get("epic"), q.Get("milestone"))
		if err != nil {
			writeError(w, err)
			return
		}
		out := listJSON[itemJSON]{Items: make([]itemJSON, 0, len(items))}
		for _, v := range items {
			out.Items = append(out.Items, toItemJSON(v))
		}
		writeJSON(w, http.StatusOK, out)
	})

	mux.HandleFunc("GET /api/v1/items/{item}", func(w http.ResponseWriter, r *http.Request) {
		v, err := t.GetItem(r.Context(), r.PathValue("item"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toItemJSON(v))
	})

	mux.HandleFunc("PATCH /api/v1/items/{item}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Version            int64               `json:"version"`
			Title              *string             `json:"title"`
			Description        *string             `json:"description"`
			Type               *tracker.TicketType `json:"type"`
			AcceptanceCriteria *[]string           `json:"acceptance_criteria"`
			Policy             *tracker.Policy     `json:"policy"`
			Epic               *string             `json:"epic"`
			Milestone          *string             `json:"milestone"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, err)
			return
		}
		v, err := t.UpdateItem(r.Context(), app.UpdateItemInput{
			Key: r.PathValue("item"), Version: in.Version, Title: in.Title, Description: in.Description,
			Type: in.Type, AcceptanceCriteria: in.AcceptanceCriteria, Policy: in.Policy,
			EpicKey: in.Epic, MilestoneKey: in.Milestone,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toItemJSON(v))
	})

	mux.HandleFunc("POST /api/v1/items/{item}/transition", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			State   tracker.State `json:"state"`
			Version int64         `json:"version"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, err)
			return
		}
		v, err := t.TransitionItem(r.Context(), r.PathValue("item"), in.State, in.Version)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toItemJSON(v))
	})

	mux.HandleFunc("GET /api/v1/items/{item}/history", func(w http.ResponseWriter, r *http.Request) {
		events, err := t.ItemHistory(r.Context(), r.PathValue("item"))
		if err != nil {
			writeError(w, err)
			return
		}
		out := listJSON[eventJSON]{Items: make([]eventJSON, 0, len(events))}
		for _, e := range events {
			out.Items = append(out.Items, eventJSON{Seq: e.Seq, Type: e.Type, OccurredAt: e.OccurredAt, Actor: e.Actor, Payload: e.Payload})
		}
		writeJSON(w, http.StatusOK, out)
	})
}
