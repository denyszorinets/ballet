package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/skill"
)

type skillJSON struct {
	ID            string            `json:"id"`
	Scope         string            `json:"scope"`
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	Body          string            `json:"body"`
	Files         map[string]string `json:"files"`
	LatestVersion int64             `json:"latest_version"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
	Version       int64             `json:"version"`
}

func toSkillJSON(s skill.Skill) skillJSON {
	files := s.Draft.Files
	if files == nil {
		files = map[string]string{}
	}
	return skillJSON{ID: s.ID, Scope: s.Scope.String(), Name: s.Name, Description: s.Draft.Description, Body: s.Draft.Body,
		Files: files, LatestVersion: s.LatestVersion, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt, Version: s.Version}
}

type skillVersionJSON struct {
	Number      int64             `json:"number"`
	Description string            `json:"description"`
	Body        string            `json:"body"`
	Files       map[string]string `json:"files"`
	PublishedBy string            `json:"published_by"`
	PublishedAt time.Time         `json:"published_at"`
}

func toSkillVersionJSON(v skill.Version) skillVersionJSON {
	files := v.Content.Files
	if files == nil {
		files = map[string]string{}
	}
	return skillVersionJSON{Number: v.Number, Description: v.Content.Description, Body: v.Content.Body, Files: files,
		PublishedBy: v.PublishedBy, PublishedAt: v.PublishedAt}
}

func registerSkills(mux *router, sk *app.Skills) {
	mux.handle("GET /api/v1/skills", func(w http.ResponseWriter, r *http.Request) {
		scope := r.URL.Query().Get("scope")
		if scope == "" {
			writeError(w, fmt.Errorf("%w: scope is required", app.ErrInvalid))
			return
		}
		list, err := sk.ListSkills(r.Context(), scope)
		if err != nil {
			writeError(w, err)
			return
		}
		out := listJSON[skillJSON]{Items: make([]skillJSON, 0, len(list))}
		for _, s := range list {
			out.Items = append(out.Items, toSkillJSON(s))
		}
		writeJSON(w, http.StatusOK, out)
	})

	mux.handle("POST /api/v1/skills", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Scope       string            `json:"scope"`
			Name        string            `json:"name"`
			Description string            `json:"description"`
			Body        string            `json:"body"`
			Files       map[string]string `json:"files"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, err)
			return
		}
		s, err := sk.CreateSkill(r.Context(), in.Scope, in.Name, skill.Content{Description: in.Description, Body: in.Body, Files: in.Files})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, toSkillJSON(s))
	})

	mux.handle("GET /api/v1/skills/{skill}", func(w http.ResponseWriter, r *http.Request) {
		s, err := sk.GetSkill(r.Context(), r.PathValue("skill"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toSkillJSON(s))
	})

	mux.handle("PATCH /api/v1/skills/{skill}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Version     int64              `json:"version"`
			Description *string            `json:"description"`
			Body        *string            `json:"body"`
			Files       *map[string]string `json:"files"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, err)
			return
		}
		s, err := sk.UpdateDraft(r.Context(), r.PathValue("skill"), app.UpdateDraftInput{
			Version: in.Version, Description: in.Description, Body: in.Body, Files: in.Files,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toSkillJSON(s))
	})

	mux.handle("POST /api/v1/skills/{skill}/publish", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Version int64 `json:"version"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, err)
			return
		}
		v, err := sk.Publish(r.Context(), r.PathValue("skill"), in.Version)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, toSkillVersionJSON(v))
	})

	mux.handle("GET /api/v1/skills/{skill}/versions", func(w http.ResponseWriter, r *http.Request) {
		vs, err := sk.Versions(r.Context(), r.PathValue("skill"))
		if err != nil {
			writeError(w, err)
			return
		}
		out := listJSON[skillVersionJSON]{Items: make([]skillVersionJSON, 0, len(vs))}
		for _, v := range vs {
			out.Items = append(out.Items, toSkillVersionJSON(v))
		}
		writeJSON(w, http.StatusOK, out)
	})

	mux.handle("GET /api/v1/skills/{skill}/versions/{number}", func(w http.ResponseWriter, r *http.Request) {
		n, err := strconv.ParseInt(r.PathValue("number"), 10, 64)
		if err != nil {
			writeError(w, fmt.Errorf("%w: version number must be an integer", app.ErrInvalid))
			return
		}
		v, err := sk.Version(r.Context(), r.PathValue("skill"), n)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toSkillVersionJSON(v))
	})
}
