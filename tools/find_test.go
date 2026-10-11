package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/katbyte/go-kt/mcp/acctest"
)

// A container is named by its name or the start of its id, and is looked for on every host unless one is given.
func TestFind(t *testing.T) {
	t.Parallel()

	dock := twoHosts(t)
	dock.AnswerWhen("GET /api/containers/aaaaaaaaaaaa1111111111111111111111111111111111111111111111111111/logs", "env", "1", `{"logs":""}`)
	dock.AnswerWhen("GET /api/containers/1111111111118888888888888888888888888888888888888888888888888888/logs", "env", "2", `{"logs":""}`)
	suite := session(t, dock, Options{})

	for what, tc := range map[string]struct {
		args      map[string]any
		env, name string
	}{
		"by its name":               {map[string]any{"container": "web-front-1"}, "alpha", "web-front-1"},
		"by its name as Docker has": {map[string]any{"container": " /web-front-1 "}, "alpha", "web-front-1"},
		"by the start of its id":    {map[string]any{"container": "aaaaaaaaaaaa"}, "alpha", "web-front-1"},
		"by four characters of it":  {map[string]any{"container": "1111"}, "beta", "solo"},
		"on a host that is named":   {map[string]any{"container": "solo", "env": "beta"}, "beta", "solo"},
	} {
		out := suite.Call(t, "container_logs", tc.args)
		if out["env"] != tc.env || out["container"] != tc.name {
			t.Errorf("%s = %v on %v, want %s on %s", what, out["container"], out["env"], tc.name, tc.env)
		}
	}

	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"no name at all":            {map[string]any{"container": "  "}, "container is required"},
		"a name nothing has":        {map[string]any{"container": "nothing-by-this-name"}, `no container is named "nothing-by-this-name", or has an id that starts with it, on alpha, beta`},
		"too little of an id":       {map[string]any{"container": "aaa"}, `no container is named "aaa"`},
		"a name on another host":    {map[string]any{"container": "solo", "env": "alpha"}, `no container is named "solo", or has an id that starts with it, on alpha:`},
		"a host that is none":       {map[string]any{"container": "solo", "env": "gamma"}, `no environment is named or numbered "gamma"`},
		"part of a name is no name": {map[string]any{"container": "web-front"}, `no container is named "web-front"`},
	} {
		if msg := suite.CallErr(t, "container_logs", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
}

// A name two hosts both have a container under is not guessed at, and a host that could not be asked is said when the container was not found.
func TestFindOnSeveralHosts(t *testing.T) {
	t.Parallel()

	dock := twoHosts(t)
	dock.AnswerWhen("GET /api/containers", "env", "2", strings.ReplaceAll(betaContainers, `"name":"solo"`, `"name":"loose"`))
	dock.AnswerWhen("GET /api/containers/1111111111118888888888888888888888888888888888888888888888888888/logs", "env", "2", `{"logs":""}`)
	dock.AnswerWhen("GET /api/containers/aaaaaaaaaaaa1111111111111111111111111111111111111111111111111111/logs", "env", "1", `{"logs":""}`)
	suite := session(t, dock, Options{})

	if msg := suite.CallErr(t, "container_logs", map[string]any{"container": "loose"}); !strings.Contains(msg, `"loose" is more than one container (loose on alpha, loose on beta)`) || !strings.Contains(msg, "say which with env") {
		t.Errorf("a name on two hosts = %q", msg)
	}
	if out := suite.Call(t, "container_logs", map[string]any{"container": "loose", "env": "beta"}); out["env"] != "beta" {
		t.Errorf("the same name, with its host = %v", out)
	}

	dock.FailWhen("GET /api/containers", "env", "2", http.StatusInternalServerError, "Failed to connect to the agent")
	if msg := suite.CallErr(t, "container_logs", map[string]any{"container": "solo"}); !strings.Contains(msg, "not every environment could be asked") || !strings.Contains(msg, "beta: ") || !strings.Contains(msg, "Failed to connect to the agent") {
		t.Errorf("a container on a host that is down = %q", msg)
	}
	// one that is found is found, whatever became of the other host
	if out := suite.Call(t, "container_logs", map[string]any{"container": "aaaaaaaaaaaa"}); out["env"] != "alpha" || acctest.Num(t, out["lines"], "lines") != 0 {
		t.Errorf("a container on the host that is up = %v", out)
	}
}
