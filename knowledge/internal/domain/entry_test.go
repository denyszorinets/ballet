package domain_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/denyszorinets/ballet/knowledge/internal/domain"
)

func TestEntry_Validate(t *testing.T) {
	ok := domain.Entry{Kind: domain.KindDecision, Title: "Use SQLite", Projects: []string{"WEB"}, Items: []string{"WEB-42"}}
	assert.NoError(t, ok.Validate())

	for name, mutate := range map[string]func(*domain.Entry){
		"unknown kind":   func(e *domain.Entry) { e.Kind = "wiki" },
		"empty title":    func(e *domain.Entry) { e.Title = " " },
		"long title":     func(e *domain.Entry) { e.Title = strings.Repeat("x", 301) },
		"bad project":    func(e *domain.Entry) { e.Projects = []string{"web"} },
		"bad item":       func(e *domain.Entry) { e.Items = []string{"WEB-0"} },
		"item w/o num":   func(e *domain.Entry) { e.Items = []string{"WEB"} },
		"body too large": func(e *domain.Entry) { e.Body = strings.Repeat("x", 200_001) },
	} {
		e := ok
		mutate(&e)
		assert.Error(t, e.Validate(), name)
	}
}
