package plannertools

import (
	"context"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/feature"
)

// featureSummary is a feature as tools show it.
type featureSummary struct {
	Key      string   `json:"key"`
	Title    string   `json:"title"`
	Status   string   `json:"status"`
	Projects []string `json:"projects"`
}

func featureOf(v app.FeatureView) featureSummary {
	return featureSummary{Key: v.Key, Title: v.Title, Status: string(v.Status), Projects: v.ProjectKeys}
}

func listFeatures(d Deps) app.PlannerTool {
	type in struct {
		AllProjects bool           `json:"all_projects"`
		Status      feature.Status `json:"status"`
	}
	return newTool("list_features",
		"List the features of the organization's feature map: what the product does, feature by feature. By default "+
			"only features of this project; all_projects lists the whole organization (features may span projects).",
		`{"type":"object","properties":{
			"all_projects":{"type":"boolean"},
			"status":{"type":"string","enum":["planned","in_progress","live","changing","deprecated","removed"]}}}`,
		func(ctx context.Context, env app.ToolEnv, in in) (any, error) {
			f := app.FeatureFilter{Status: in.Status}
			if !in.AllProjects {
				f.ProjectKey = env.ProjectKey
			}
			views, err := d.Features.List(ctx, env.OrganizationKey, f)
			if err != nil {
				return nil, err
			}
			out := make([]featureSummary, len(views))
			for i, v := range views {
				out[i] = featureOf(v)
			}
			return out, nil
		})
}

func getFeature(d Deps) app.PlannerTool {
	type in struct {
		Key string `json:"key"`
	}
	type link struct {
		From string `json:"from"`
		Type string `json:"type"`
		To   string `json:"to"`
	}
	type ticket struct {
		Key   string `json:"key"`
		Title string `json:"title"`
		State string `json:"state"`
	}
	type revision struct {
		Number int64  `json:"number"`
		At     string `json:"at"`
		Author string `json:"author"`
		Reason string `json:"reason,omitempty"`
		Status string `json:"status"`
		Title  string `json:"title"`
	}
	type out struct {
		featureSummary
		Description string     `json:"description"`
		Version     int64      `json:"version"`
		Links       []link     `json:"links"`
		Tickets     []ticket   `json:"tickets"`
		History     []revision `json:"history"`
	}
	return newTool("get_feature",
		"Get a feature: what it does now (description), its links to other features, the tickets that changed it and "+
			"its history (latest 10 revisions, newest first, with reasons).",
		`{"type":"object","required":["key"],"properties":{"key":{"type":"string","description":"feature key, e.g. F-3"}}}`,
		func(ctx context.Context, env app.ToolEnv, in in) (any, error) {
			f, err := d.Features.Get(ctx, env.OrganizationKey, in.Key)
			if err != nil {
				return nil, err
			}
			revs, err := d.Features.Revisions(ctx, env.OrganizationKey, in.Key)
			if err != nil {
				return nil, err
			}
			o := out{featureSummary: featureOf(f.FeatureView), Description: f.Description, Version: f.Version,
				Links: []link{}, Tickets: []ticket{}, History: []revision{}}
			for _, l := range f.Links {
				o.Links = append(o.Links, link{From: l.FromKey, Type: string(l.Type), To: l.ToKey})
			}
			for _, t := range f.Tickets {
				o.Tickets = append(o.Tickets, ticket{Key: t.Key, Title: t.Title, State: string(t.State)})
			}
			for i, r := range revs {
				if i == 10 {
					break
				}
				author := r.Author.Subject
				if r.Author.ActingFor != "" {
					author += " for " + r.Author.ActingFor
				}
				o.History = append(o.History, revision{Number: r.Number, At: r.CreatedAt.Format("2006-01-02 15:04"),
					Author: author, Reason: r.Reason, Status: string(r.Status), Title: r.Title})
			}
			return o, nil
		})
}
