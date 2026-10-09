package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/feature"
)

type featureJSON struct {
	Key          string         `json:"key"`
	Organization string         `json:"organization"`
	Title        string         `json:"title"`
	Description  string         `json:"description"`
	Status       feature.Status `json:"status"`
	Projects     []string       `json:"projects"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	Version      int64          `json:"version"`
}

func toFeatureJSON(v app.FeatureView) featureJSON {
	return featureJSON{Key: v.Key, Organization: v.OrganizationKey, Title: v.Title, Description: v.Description,
		Status: v.Status, Projects: nonNilStrings(v.ProjectKeys), CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt,
		Version: v.Version}
}

type featureLinkJSON struct {
	ID        string           `json:"id"`
	From      string           `json:"from"`
	To        string           `json:"to"`
	Type      feature.LinkType `json:"type"`
	CreatedBy event.Actor      `json:"created_by"`
	CreatedAt time.Time        `json:"created_at"`
}

func toFeatureLinkJSON(l app.FeatureLinkView) featureLinkJSON {
	return featureLinkJSON{ID: l.ID, From: l.FromKey, To: l.ToKey, Type: l.Type, CreatedBy: l.CreatedBy, CreatedAt: l.CreatedAt}
}

type revisionJSON struct {
	Feature     string         `json:"feature"`
	Number      int64          `json:"number"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Status      feature.Status `json:"status"`
	Projects    []string       `json:"projects"`
	Author      event.Actor    `json:"author"`
	Reason      string         `json:"reason,omitempty"`
	CauseKind   string         `json:"cause_kind,omitempty"`
	CauseRef    string         `json:"cause_ref,omitempty"`
	Review      feature.Review `json:"review,omitempty"`
	ReviewedBy  string         `json:"reviewed_by,omitempty"`
	ReviewedAt  *time.Time     `json:"reviewed_at,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
}

func toRevisionJSON(r app.RevisionView) revisionJSON {
	return revisionJSON{Feature: r.FeatureKey, Number: r.Number, Title: r.Title, Description: r.Description,
		Status: r.Status, Projects: nonNilStrings(r.ProjectKeys), Author: r.Author, Reason: r.Reason,
		CauseKind: r.Cause.Kind, CauseRef: r.Cause.Ref, Review: r.Review, ReviewedBy: r.ReviewedBy,
		ReviewedAt: timePtr(r.ReviewedAt), CreatedAt: r.CreatedAt}
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func registerFeatures(mux *router, fs *app.Features) {
	org := func(r *http.Request) string { return r.PathValue("organization") }
	list := func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		views, err := fs.List(r.Context(), org(r), app.FeatureFilter{ProjectKey: q.Get("project"), Status: feature.Status(q.Get("status"))})
		if err != nil {
			writeError(w, err)
			return
		}
		out := struct {
			Items []featureJSON `json:"items"`
		}{Items: []featureJSON{}}
		for _, v := range views {
			out.Items = append(out.Items, toFeatureJSON(v))
		}
		writeJSON(w, http.StatusOK, out)
	}
	create := func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Title       string         `json:"title"`
			Description string         `json:"description"`
			Status      feature.Status `json:"status"`
			Projects    []string       `json:"projects"`
			Reason      string         `json:"reason"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, err)
			return
		}
		v, err := fs.Create(r.Context(), org(r), app.CreateFeatureInput{Title: in.Title, Description: in.Description,
			Status: in.Status, ProjectKeys: in.Projects, Reason: in.Reason})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, toFeatureJSON(v))
	}
	get := func(w http.ResponseWriter, r *http.Request) {
		d, err := fs.Get(r.Context(), org(r), r.PathValue("feature"))
		if err != nil {
			writeError(w, err)
			return
		}
		out := struct {
			featureJSON
			Links []featureLinkJSON `json:"links"`
		}{featureJSON: toFeatureJSON(d.FeatureView), Links: []featureLinkJSON{}}
		for _, l := range d.Links {
			out.Links = append(out.Links, toFeatureLinkJSON(l))
		}
		writeJSON(w, http.StatusOK, out)
	}
	update := func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Version     int64           `json:"version"`
			Title       *string         `json:"title"`
			Description *string         `json:"description"`
			Status      *feature.Status `json:"status"`
			Projects    *[]string       `json:"projects"`
			Reason      string          `json:"reason"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, err)
			return
		}
		v, err := fs.Update(r.Context(), org(r), r.PathValue("feature"), app.UpdateFeatureInput{Version: in.Version,
			Title: in.Title, Description: in.Description, Status: in.Status, ProjectKeys: in.Projects, Reason: in.Reason})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toFeatureJSON(v))
	}
	revisions := func(w http.ResponseWriter, r *http.Request) {
		revs, err := fs.Revisions(r.Context(), org(r), r.PathValue("feature"))
		if err != nil {
			writeError(w, err)
			return
		}
		out := struct {
			Items []revisionJSON `json:"items"`
		}{Items: []revisionJSON{}}
		for _, rv := range revs {
			out.Items = append(out.Items, toRevisionJSON(rv))
		}
		writeJSON(w, http.StatusOK, out)
	}
	link := func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			From string           `json:"from"`
			To   string           `json:"to"`
			Type feature.LinkType `json:"type"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, err)
			return
		}
		l, err := fs.Link(r.Context(), org(r), app.LinkFeaturesInput{From: in.From, To: in.To, Type: in.Type})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, toFeatureLinkJSON(l))
	}
	unlink := func(w http.ResponseWriter, r *http.Request) {
		if err := fs.Unlink(r.Context(), org(r), r.PathValue("link")); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
	graph := func(w http.ResponseWriter, r *http.Request) {
		q := app.GraphQuery{ProjectKey: r.URL.Query().Get("project")}
		if at := r.URL.Query().Get("at"); at != "" {
			t, err := time.Parse(time.RFC3339, at)
			if err != nil {
				writeError(w, fmt.Errorf("%w: at must be an RFC 3339 time", app.ErrInvalid))
				return
			}
			q.At = t
		}
		g, err := fs.Graph(r.Context(), org(r), q)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toGraphJSON(g))
	}
	mux.handle("GET /api/v1/organizations/{organization}/features", list)
	mux.handle("POST /api/v1/organizations/{organization}/features", create)
	mux.handle("GET /api/v1/organizations/{organization}/features/{feature}", get)
	mux.handle("PATCH /api/v1/organizations/{organization}/features/{feature}", update)
	mux.handle("GET /api/v1/organizations/{organization}/features/{feature}/revisions", revisions)
	mux.handle("POST /api/v1/organizations/{organization}/feature-links", link)
	mux.handle("DELETE /api/v1/organizations/{organization}/feature-links/{link}", unlink)
	mux.handle("GET /api/v1/organizations/{organization}/feature-graph", graph)

	reviews := func(w http.ResponseWriter, r *http.Request) {
		queue, err := fs.Reviews(r.Context(), org(r))
		if err != nil {
			writeError(w, err)
			return
		}
		type reviewJSON struct {
			revisionJSON
			FeatureTitle string        `json:"feature_title"`
			Previous     *revisionJSON `json:"previous,omitempty"`
		}
		out := struct {
			Items []reviewJSON `json:"items"`
		}{Items: []reviewJSON{}}
		for _, rv := range queue {
			item := reviewJSON{revisionJSON: toRevisionJSON(rv.RevisionView), FeatureTitle: rv.FeatureTitle}
			if rv.Previous.Number > 0 {
				prev := toRevisionJSON(rv.Previous)
				item.Previous = &prev
			}
			out.Items = append(out.Items, item)
		}
		writeJSON(w, http.StatusOK, out)
	}
	review := func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Action  string `json:"action"`
			Comment string `json:"comment"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, err)
			return
		}
		n, err := strconv.ParseInt(r.PathValue("revision"), 10, 64)
		if err != nil {
			writeError(w, fmt.Errorf("%w: revision must be a number", app.ErrInvalid))
			return
		}
		rv, err := fs.Review(r.Context(), org(r), r.PathValue("feature"), n, app.ReviewInput{Action: in.Action, Comment: in.Comment})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toRevisionJSON(rv))
	}
	mux.handle("GET /api/v1/organizations/{organization}/feature-reviews", reviews)
	mux.handle("POST /api/v1/organizations/{organization}/features/{feature}/revisions/{revision}/review", review)
}

type graphFeatureJSON struct {
	Key       string         `json:"key"`
	Title     string         `json:"title"`
	Status    feature.Status `json:"status"`
	Projects  []string       `json:"projects"`
	Version   int64          `json:"version"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type graphChangeJSON struct {
	At      time.Time `json:"at"`
	Feature string    `json:"feature"`
	Kind    string    `json:"kind"`
	Summary string    `json:"summary"`
}

type graphJSON struct {
	At       time.Time          `json:"at"`
	Features []graphFeatureJSON `json:"features"`
	Links    []featureLinkJSON  `json:"links"`
	Changes  []graphChangeJSON  `json:"changes"`
}

func toGraphJSON(g app.FeatureGraph) graphJSON {
	out := graphJSON{At: g.At, Features: []graphFeatureJSON{}, Links: []featureLinkJSON{}, Changes: []graphChangeJSON{}}
	for _, f := range g.Features {
		out.Features = append(out.Features, graphFeatureJSON{Key: f.Key, Title: f.Title, Status: f.Status,
			Projects: nonNilStrings(f.ProjectKeys), Version: f.Version, CreatedAt: f.CreatedAt, UpdatedAt: f.UpdatedAt})
	}
	for _, l := range g.Links {
		out.Links = append(out.Links, toFeatureLinkJSON(l))
	}
	for _, c := range g.Changes {
		out.Changes = append(out.Changes, graphChangeJSON{At: c.At, Feature: c.FeatureKey, Kind: c.Kind, Summary: c.Summary})
	}
	return out
}
