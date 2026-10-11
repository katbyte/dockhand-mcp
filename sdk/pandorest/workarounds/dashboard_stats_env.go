package workarounds

import (
	"errors"
	"net/http"
	"slices"

	pandorest "github.com/katbyte/pandorest/importer/workarounds"
	"github.com/katbyte/pandorest/openapi"
)

// dashboardStatsEnv takes away the parameter that makes the dashboard's statistics answer another shape than the one they declare.
type dashboardStatsEnv struct{}

func (dashboardStatsEnv) Name() string    { return Service + "-dashboard-stats-env" }
func (dashboardStatsEnv) Service() string { return Service }

func (dashboardStatsEnv) Bug() string {
	return "GET /api/dashboard/stats declares a list, one entry an environment, and takes an env; given one, a 1.0.51 server answers that environment's entry alone, an object where the list was declared, which no one model reads. The client asks for the list and never sends env: one environment's entry is found in it"
}

func (dashboardStatsEnv) Apply(spec *openapi.Spec) error {
	op, err := pandorest.Operation(spec, http.MethodGet, "/api/dashboard/stats")
	if err != nil {
		return err
	}
	if op.Parameter(openapi.InQuery, "env") == nil {
		return errors.New("GET /api/dashboard/stats no longer takes env")
	}
	op.Parameters = slices.DeleteFunc(op.Parameters, func(p *openapi.Parameter) bool { return p.In == openapi.InQuery && p.Name == "env" })

	return nil
}
