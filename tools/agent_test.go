package tools

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// What a 1.0.51 server answers about agents that call in, with invented names: three environments of which two are called in for, one agent
// connected, and the tokens of both, one of them replaced.
//
//nolint:gosec // G101: the tokens are made up, and the calls about tokens are no tokens
const (
	withCallers  = `[{"id":1,"name":"alpha","connectionType":"socket"},{"id":3,"name":"gamma","connectionType":"hawser-edge"},{"id":4,"name":"delta","connectionType":"hawser-edge"}]`
	gammaCalling = `{"status":"ready","message":"Hawser Edge WebSocket endpoint. Connect via WebSocket.","protocol":"wss://<host>/api/hawser/connect","activeConnections":1,"connections":[{"environmentId":3,"agentId":"an-agent-id","agentName":"agent-gamma","agentVersion":"0.2.50","dockerVersion":"29.9.0","hostname":"host-gamma","capabilities":["exec","metrics","events","compose"],"connectedAt":"2026-10-10T13:36:29.139Z","lastHeartbeat":"2026-10-10T13:36:34.142Z"}]}`
	nobodyCalls  = `{"status":"ready","message":"Hawser Edge WebSocket endpoint. Connect via WebSocket.","protocol":"wss://<host>/api/hawser/connect","activeConnections":0,"connections":[]}`
	gammaOld     = `{"id":1,"tokenPrefix":"_0noM99t","name":"gamma","environmentId":3,"isActive":false,"lastUsed":null,"createdAt":"2026-10-10 13:36:28","expiresAt":null}`
	gammaNow     = `{"id":2,"tokenPrefix":"Zq4k1x8u","name":"gamma again","environmentId":3,"isActive":true,"lastUsed":"2026-10-10T13:36:29.138Z","createdAt":"2026-10-10 13:40:00","expiresAt":"2030-01-01T00:00:00Z"}`
	deltaNow     = `{"id":5,"tokenPrefix":"m3Jw0aPd","name":"delta","environmentId":4,"isActive":true,"lastUsed":null,"createdAt":"2026-10-10 14:00:00","expiresAt":null}`
	gammaEnded   = `{"id":2,"tokenPrefix":"Zq4k1x8u","name":"gamma again","environmentId":3,"isActive":false,"lastUsed":"2026-10-10T13:36:29.138Z","createdAt":"2026-10-10 13:40:00","expiresAt":"2030-01-01T00:00:00Z"}`
	tokenMadeNow = `{"token":"made-by-dockhand-0123456789abcdefghijklmnopq","tokenId":6,"message":"Token generated successfully. Save this token - it will not be shown again."}`
	// ownToken is a token of a caller's own choosing
	ownToken = "a-token-of-my-own-choosing"

	callers = "GET /api/hawser/connect"
	tokens  = "GET /api/hawser/tokens"
	mint    = "POST /api/hawser/tokens"
	revoke  = "DELETE /api/hawser/tokens"
)

// calledIn is a Dockhand with two environments that agents call in for, one of them connected.
func calledIn(t *testing.T) *dockhandtest.Server {
	t.Helper()

	dock := dockhandtest.New(t)
	dock.Answer("GET /api/environments", withCallers)
	dock.Answer("GET /api/environments/1", `{"id":1,"name":"alpha","connectionType":"socket"}`)
	dock.Answer("GET /api/environments/3", `{"id":3,"name":"gamma","connectionType":"hawser-edge"}`)
	dock.Answer("GET /api/environments/4", `{"id":4,"name":"delta","connectionType":"hawser-edge"}`)
	dock.Answer(callers, gammaCalling)
	dock.Answer(tokens, `[`+gammaOld+`,`+gammaNow+`,`+deltaNow+`]`)

	return dock
}

func TestAgentList(t *testing.T) {
	t.Parallel()

	dock := calledIn(t)
	suite := session(t, dock, Options{})

	// who has called in, who is waited for, and the tokens that work, the newest first
	out := suite.Call(t, "agent_list", nil)
	connected := acctest.Rows(t, out["connected"], "connected")
	if len(connected) != 1 || connected[0]["env"] != "gamma" || connected[0]["agent"] != "agent-gamma" || connected[0]["agent_version"] != "0.2.50" || connected[0]["docker_version"] != "29.9.0" || connected[0]["hostname"] != "host-gamma" || connected[0]["since"] != "2026-10-10T13:36:29.139Z" || connected[0]["last_heard"] != "2026-10-10T13:36:34.142Z" || len(acctest.Strs(t, connected[0]["can"], "can")) != 4 {
		t.Errorf("the agents connected = %v", connected)
	}
	if waiting := acctest.Strs(t, out["waiting"], "waiting"); len(waiting) != 1 || waiting[0] != "delta" {
		t.Errorf("the environments waited for = %v", waiting)
	}
	listed := acctest.Rows(t, out["tokens"], "tokens")
	if len(listed) != 2 || acctest.Num(t, out["revoked"], "revoked") != 1 || out["note"] != nil {
		t.Fatalf("the tokens = %v", out)
	}
	if first, second := listed[0], listed[1]; acctest.Num(t, first["id"], "id") != 5 || first["env"] != "delta" || first["starts"] != "m3Jw0aPd" || !acctest.IsBool(first["active"], true) || first["last_used"] != nil || first["expires"] != nil || second["name"] != "gamma again" || second["env"] != "gamma" || second["expires"] != "2030-01-01T00:00:00Z" || second["last_used"] != "2026-10-10T13:36:29.138Z" || second["made"] != "2026-10-10 13:40:00" {
		t.Errorf("the tokens that work = %v", listed)
	}

	// one environment's, with the tokens that no longer work
	out = suite.Call(t, "agent_list", map[string]any{"env": "gamma", "revoked": true})
	if listed = acctest.Rows(t, out["tokens"], "tokens"); len(listed) != 2 || acctest.Num(t, listed[1]["id"], "id") != 1 || !acctest.IsBool(listed[1]["active"], false) || out["revoked"] != nil || len(acctest.Rows(t, out["connected"], "connected")) != 1 || len(acctest.Strs(t, out["waiting"], "waiting")) != 0 {
		t.Errorf("one environment's agent and tokens = %v", out)
	}
	if out = suite.Call(t, "agent_list", map[string]any{"env": 4}); len(acctest.Rows(t, out["connected"], "connected")) != 0 || len(acctest.Strs(t, out["waiting"], "waiting")) != 1 || len(acctest.Rows(t, out["tokens"], "tokens")) != 1 {
		t.Errorf("one that is waited for = %v", out)
	}

	// a token whose environment is not listed is given by its number, and a Dockhand no agent calls says so
	dock.Answer(tokens, `[{"id":9,"tokenPrefix":"abcdefgh","name":"left over","environmentId":77,"isActive":true,"createdAt":"2026-10-10 14:00:00"}]`)
	if out = suite.Call(t, "agent_list", nil); acctest.Rows(t, out["tokens"], "tokens")[0]["env"] != "#77" {
		t.Errorf("a token of an environment that is not listed = %v", out)
	}
	dock.Answer("GET /api/environments", twoEnvironments)
	dock.Answer(callers, nobodyCalls)
	dock.Answer(tokens, `[]`)
	if out = suite.Call(t, "agent_list", nil); !strings.Contains(acctest.Str(out["note"]), "no environment here is one an agent calls in for") {
		t.Errorf("a Dockhand no agent calls = %v", out)
	}
}

func TestAgentListRefused(t *testing.T) {
	t.Parallel()

	dock := calledIn(t)
	suite := session(t, dock, Options{})

	if msg := suite.CallErr(t, "agent_list", map[string]any{"env": "omega"}); !strings.Contains(msg, `no environment is named or numbered "omega"`) {
		t.Errorf("an environment that is none = %q", msg)
	}
	dock.Fail(tokens, http.StatusForbidden, "Admin access required")
	if msg := suite.CallErr(t, "agent_list", nil); !strings.Contains(msg, "the agent tokens could not be listed") || !strings.Contains(msg, "Admin access required") {
		t.Errorf("tokens the account may not list = %q", msg)
	}
	dock.Fail(callers, http.StatusInternalServerError, "Failed")
	if msg := suite.CallErr(t, "agent_list", nil); !strings.Contains(msg, "the agents that have called in could not be listed") {
		t.Errorf("connections that could not be listed = %q", msg)
	}
	dock.Fail("GET /api/environments", http.StatusInternalServerError, "Failed")
	if msg := suite.CallErr(t, "agent_list", nil); !strings.Contains(msg, "the environments could not be listed") {
		t.Errorf("environments that could not be listed = %q", msg)
	}
}

func TestAgentTokenCreate(t *testing.T) {
	t.Parallel()

	dock := calledIn(t)
	dock.Answer(mint, tokenMadeNow)
	suite := session(t, dock, everything)

	// a dry run says what making one ends, and makes none
	out := suite.Call(t, "agent_token_create", map[string]any{"env": "gamma", "dry_run": true})
	changes := acctest.Strs(t, out["changes"], "changes")
	if len(changes) != 3 || changes[0] != "would make the token gamma for gamma" || !strings.Contains(changes[1], "would end the token gamma again (it begins Zq4k1x8u)") || !strings.Contains(changes[2], "would cut off the agent agent-gamma") || !strings.Contains(acctest.Str(out["note"]), "turns away every call from that agent's address for five minutes") || !acctest.IsBool(out["dry_run"], true) || out["token"] != nil || len(dock.Requests(mint)) != 0 {
		t.Errorf("a dry run = %v, with %d made", out, len(dock.Requests(mint)))
	}

	// made by Dockhand: given once, with how an agent is started with it
	out = suite.Call(t, "agent_token_create", map[string]any{"env": "gamma", "name": " rotated ", "expires": "90d"})
	body := sent(t, dock.Last(mint))
	expires, err := time.Parse(time.RFC3339, acctest.Str(body["expiresAt"]))
	if err != nil || time.Until(expires) < 89*24*time.Hour || time.Until(expires) > 91*24*time.Hour || body["name"] != "rotated" || body["environmentId"] != float64(3) || body["rawToken"] != nil {
		t.Errorf("sent %v, %v", body, err)
	}
	if out["token"] != "made-by-dockhand-0123456789abcdefghijklmnopq" || acctest.Num(t, out["token_id"], "token_id") != 6 || out["name"] != "rotated" || out["env"] != "gamma" || out["expires"] != body["expiresAt"] || !strings.Contains(acctest.Str(out["note"]), "this is the only time the token is shown") || !strings.Contains(acctest.Str(out["note"]), "DOCKHAND_SERVER_URL") || !strings.Contains(acctest.Str(out["note"]), "stop the agent first") {
		t.Errorf("a token made = %v", out)
	}
	if changes = acctest.Strs(t, out["changes"], "changes"); len(changes) != 3 || !strings.HasPrefix(changes[0], "made the token rotated for gamma, which stops working at ") || !strings.HasPrefix(changes[1], "ended the token gamma again") || !strings.HasPrefix(changes[2], "cut off the agent agent-gamma") {
		t.Errorf("what making it did = %v", changes)
	}

	// one of the caller's own is sent and not repeated; an environment with no token and no agent has nothing ended
	dock.Answer(tokens, `[`+gammaOld+`]`)
	dock.Answer(callers, nobodyCalls)
	dock.Answer(mint, `{"token":"`+ownToken+`","tokenId":7,"message":"Token generated successfully."}`)
	out = suite.Call(t, "agent_token_create", map[string]any{"env": 3, "agent_token": ownToken, "expires": "2099-01-31"})
	if body = sent(t, dock.Last(mint)); body["rawToken"] != ownToken || body["expiresAt"] != "2099-01-31T00:00:00Z" || body["name"] != "gamma" {
		t.Errorf("sent %v", body)
	}
	if out["token"] != nil || strings.Contains(flat(out), ownToken) || acctest.Num(t, out["token_id"], "token_id") != 7 || len(acctest.Strs(t, out["changes"], "changes")) != 1 || strings.Contains(acctest.Str(out["note"]), "only time") || strings.Contains(acctest.Str(out["note"]), "five minutes") {
		t.Errorf("a token of the caller's own = %v", out)
	}
}

func TestAgentTokenCreateRefused(t *testing.T) {
	t.Parallel()

	dock := calledIn(t)
	suite := session(t, dock, everything)

	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"no environment":                 {nil, "env is required"},
		"an environment that is none":    {map[string]any{"env": "omega"}, `no environment is named or numbered "omega"`},
		"one no agent calls in for":      {map[string]any{"env": "alpha"}, "alpha is reached by socket"},
		"a token too short to be one":    {map[string]any{"env": "gamma", "agent_token": "abc"}, "agent_token is 3 characters"},
		"an expiry that is no time":      {map[string]any{"env": "gamma", "expires": "next week"}, `expires is "next week"`},
		"an expiry of no days":           {map[string]any{"env": "gamma", "expires": "0d"}, "a whole number above 0"},
		"an expiry that is past":         {map[string]any{"env": "gamma", "expires": "2020-01-01"}, "which is past"},
		"a time that is past, in full":   {map[string]any{"env": "gamma", "expires": "2020-01-01T00:00:00Z"}, "which is past"},
		"a dry run of a refused request": {map[string]any{"env": "alpha", "dry_run": true}, "alpha is reached by socket"},
	} {
		if msg := suite.CallErr(t, "agent_token_create", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	if made := dock.Requests(mint); len(made) != 0 {
		t.Errorf("%d tokens were made by calls that were refused", len(made))
	}

	// refused by Dockhand, answered with no token, and what it would end not to be seen
	dock.Fail(mint, http.StatusInternalServerError, "Failed to generate token")
	if msg := suite.CallErr(t, "agent_token_create", map[string]any{"env": "gamma"}); !strings.Contains(msg, "the token for gamma was not made") || !strings.Contains(msg, "Failed to generate token") {
		t.Errorf("one Dockhand refuses = %q", msg)
	}
	dock.Answer(mint, `{"tokenId":8,"message":"Token generated successfully."}`)
	if msg := suite.CallErr(t, "agent_token_create", map[string]any{"env": "gamma"}); !strings.Contains(msg, "answered that it made a token and gave none") {
		t.Errorf("one answered with no token = %q", msg)
	}
	made := len(dock.Requests(mint))
	dock.Fail(callers, http.StatusInternalServerError, "Failed")
	if msg := suite.CallErr(t, "agent_token_create", map[string]any{"env": "gamma"}); !strings.Contains(msg, "the agents that have called in could not be listed") {
		t.Errorf("connections that could not be listed = %q", msg)
	}
	dock.Fail(tokens, http.StatusForbidden, "Admin access required")
	if msg := suite.CallErr(t, "agent_token_create", map[string]any{"env": "gamma"}); !strings.Contains(msg, "the agent tokens could not be listed") {
		t.Errorf("tokens that could not be listed = %q", msg)
	}
	dock.Fail("GET /api/environments/3", http.StatusInternalServerError, "Failed")
	if msg := suite.CallErr(t, "agent_token_create", map[string]any{"env": "gamma"}); !strings.Contains(msg, "gamma could not be read") || len(dock.Requests(mint)) != made {
		t.Errorf("an environment that could not be read = %q, with %d more made", msg, len(dock.Requests(mint))-made)
	}
}

func TestAgentTokenDelete(t *testing.T) {
	t.Parallel()

	dock := calledIn(t)
	dock.Answer(revoke, `{"success":true,"message":"Token revoked"}`)
	suite := session(t, dock, everything)

	// without confirm: what would follow, the agent connected with it among it, and nothing revoked
	out := suite.Call(t, "agent_token_delete", map[string]any{"id": 2})
	would := acctest.Strs(t, out["would_go"], "would_go")
	if len(would) != 2 || !strings.Contains(would[0], "the token gamma again of gamma (it begins Zq4k1x8u)") || !strings.Contains(would[1], "the call of the agent agent-gamma") || acctest.BoolOf(out["revoked"]) || !strings.Contains(acctest.Str(out["note"]), "nothing was revoked") || !strings.Contains(acctest.Str(out["note"]), "stop the agent first") || len(dock.Requests(revoke)) != 0 {
		t.Errorf("a preview = %v, with %d revoked", out, len(dock.Requests(revoke)))
	}
	if out = suite.Call(t, "agent_token_delete", map[string]any{"id": 5}); len(acctest.Strs(t, out["would_go"], "would_go")) != 1 || strings.Contains(acctest.Str(out["note"]), "five minutes") {
		t.Errorf("a preview for a token no agent is connected with = %v", out)
	}

	// with it: revoked, and read back as one that no longer works
	dock.Then(revoke, func(dockhandtest.Request) { dock.Answer(tokens, `[`+gammaOld+`,`+gammaEnded+`,`+deltaNow+`]`) })
	out = suite.Call(t, "agent_token_delete", map[string]any{"id": 2, "confirm": true})
	if dock.Last(revoke).Query.Get("id") != "2" || !acctest.BoolOf(out["revoked"]) || acctest.IsBool(acctest.Object(t, out["token"], "token")["active"], true) || !strings.Contains(acctest.Str(out["note"]), "gamma has no token that works now") || !strings.Contains(acctest.Str(out["note"]), "the agent agent-gamma was cut off") {
		t.Errorf("a token revoked = %v, asked with %v", out, dock.Last(revoke).Query)
	}

	// one that no longer works is nothing to do, with or without confirm
	revoked := len(dock.Requests(revoke))
	if out = suite.Call(t, "agent_token_delete", map[string]any{"id": 1, "confirm": true}); acctest.BoolOf(out["revoked"]) || !strings.Contains(acctest.Str(out["note"]), "nothing to revoke") || len(dock.Requests(revoke)) != revoked {
		t.Errorf("one revoked already = %v", out)
	}
}

func TestAgentTokenDeleteRefused(t *testing.T) {
	t.Parallel()

	dock := calledIn(t)
	suite := session(t, dock, everything)

	if msg := suite.CallErr(t, "agent_token_delete", map[string]any{"id": 0, "confirm": true}); !strings.Contains(msg, "id is required") {
		t.Errorf("no number = %q", msg)
	}
	// Dockhand says it revoked whatever number it is given, so one there is none of is refused here
	dock.Answer(revoke, `{"success":true,"message":"Token revoked"}`)
	if msg := suite.CallErr(t, "agent_token_delete", map[string]any{"id": 99, "confirm": true}); !strings.Contains(msg, "there is no agent token numbered 99") || len(dock.Requests(revoke)) != 0 {
		t.Errorf("a number no token has = %q", msg)
	}
	// answered as revoked and still listed as one that works
	if msg := suite.CallErr(t, "agent_token_delete", map[string]any{"id": 5, "confirm": true}); !strings.Contains(msg, "lists it as one that still works") {
		t.Errorf("one revoked in word alone = %q", msg)
	}
	dock.Fail(revoke, http.StatusForbidden, "Admin access required")
	if msg := suite.CallErr(t, "agent_token_delete", map[string]any{"id": 5, "confirm": true}); !strings.Contains(msg, "the token delta was not revoked") || !strings.Contains(msg, "Admin access required") {
		t.Errorf("one the account may not revoke = %q", msg)
	}
	// revoked, and the list not to be had afterwards
	dock.Answer(revoke, `{"success":true,"message":"Token revoked"}`)
	dock.Then(revoke, func(dockhandtest.Request) {
		dock.Fail(tokens, http.StatusInternalServerError, "Failed to read the tokens")
	})
	if out := suite.Call(t, "agent_token_delete", map[string]any{"id": 5, "confirm": true}); acctest.BoolOf(out["revoked"]) || !strings.Contains(acctest.Str(out["note"]), "the tokens could not be listed afterwards") {
		t.Errorf("one revoked and not read back = %v", out)
	}
	if msg := suite.CallErr(t, "agent_token_delete", map[string]any{"id": 5}); !strings.Contains(msg, "the agent tokens could not be listed") {
		t.Errorf("tokens that could not be listed = %q", msg)
	}
	dock.Fail("GET /api/environments", http.StatusInternalServerError, "Failed")
	if msg := suite.CallErr(t, "agent_token_delete", map[string]any{"id": 5}); !strings.Contains(msg, "the environments could not be listed") {
		t.Errorf("environments that could not be listed = %q", msg)
	}
}

func TestEnvCreateCalledBy(t *testing.T) {
	t.Parallel()

	const made = "POST /api/environments"
	dock := calledIn(t)
	dock.Answer(made, `{"id":6,"name":"epsilon","connectionType":"hawser-edge","socketPath":"/var/run/docker.sock","labels":null,"hasHawserToken":false}`)
	dock.Answer("GET /api/environments/6", `{"id":6,"name":"epsilon","connectionType":"hawser-edge","socketPath":"/var/run/docker.sock","labels":[],"hasHawserToken":false}`)
	dock.Answer(mint, tokenMadeNow)
	suite := session(t, dock, everything)

	// a dry run tries nothing, since there is nothing to try, and saves nothing
	out := suite.Call(t, "env_create", map[string]any{"name": "epsilon", "connection": "hawser-edge", "dry_run": true})
	if changes := acctest.Strs(t, out["changes"], "changes"); len(changes) != 2 || !strings.HasPrefix(changes[0], "would add the environment epsilon, reached by hawser-edge") || !strings.HasPrefix(changes[1], "would make a token for its agent") || out["found"] != nil || out["environment"] != nil || len(dock.Calls()) != 1 {
		t.Errorf("a dry run = %v, after %v", out, dock.Calls())
	}

	// saved with no connection tried, and its token made and given once
	out = suite.Call(t, "env_create", map[string]any{"name": "epsilon", "connection": "hawser-edge", "labels": []string{"far"}})
	if body := sent(t, dock.Last(made)); body["name"] != "epsilon" || body["connectionType"] != "hawser-edge" || body["hawserToken"] != nil || body["host"] != nil || len(acctest.RowsOfAny(body["labels"])) != 1 {
		t.Errorf("sent %v", body)
	}
	if body := sent(t, dock.Last(mint)); body["name"] != "epsilon" || body["environmentId"] != float64(6) || body["rawToken"] != nil || body["expiresAt"] != nil {
		t.Errorf("sent for its token %v", body)
	}
	env := acctest.Object(t, out["environment"], "environment")
	if env["env"] != "epsilon" || acctest.Num(t, env["id"], "id") != 6 || env["connection"] != "hawser-edge" || !acctest.IsBool(env["online"], false) || out["token"] != "made-by-dockhand-0123456789abcdefghijklmnopq" || out["found"] != nil || len(dock.Requests("POST /api/environments/test")) != 0 {
		t.Errorf("an environment an agent calls in for = %v", out)
	}
	if note := acctest.Str(out["note"]); !strings.Contains(note, "no agent has called in yet") || !strings.Contains(note, "DOCKHAND_SERVER_URL") || !strings.Contains(note, "this is the only time the token is shown") || len(acctest.Strs(t, out["changes"], "changes")) != 2 {
		t.Errorf("what was done and said = %v", out)
	}

	// with a token of the caller's own, which is sent to be the agent's and not repeated, nor sent as an agent's that Dockhand calls
	out = suite.Call(t, "env_create", map[string]any{"name": "zeta", "connection": "hawser-edge", "agent_token": ownToken})
	if sent(t, dock.Last(made))["hawserToken"] != nil || sent(t, dock.Last(mint))["rawToken"] != ownToken || out["token"] != nil || strings.Contains(flat(out), ownToken) || strings.Contains(acctest.Str(out["note"]), "only time") {
		t.Errorf("one made with the caller's own token = %v", out)
	}

	// saved and not read back, and saved with no token to be made for it
	dock.Fail("GET /api/environments/6", http.StatusInternalServerError, "Failed to read environment")
	if out = suite.Call(t, "env_create", map[string]any{"name": "eta", "connection": "hawser-edge"}); !strings.Contains(acctest.Str(out["note"]), "could not be read back") || acctest.Object(t, out["environment"], "environment")["env"] != "eta" || out["token"] == nil {
		t.Errorf("one added and not read back = %v", out)
	}
	dock.Fail(mint, http.StatusInternalServerError, "Failed to generate token")
	if msg := suite.CallErr(t, "env_create", map[string]any{"name": "theta", "connection": "hawser-edge"}); !strings.Contains(msg, "theta was added, and the token for its agent was not made") || !strings.Contains(msg, "agent_token_create makes one") {
		t.Errorf("one added with no token = %q", msg)
	}
	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"given an address":        {map[string]any{"name": "iota", "connection": "hawser-edge", "host": "h"}, "takes no host, port, protocol or socket_path"},
		"given a socket":          {map[string]any{"name": "iota", "connection": "hawser-edge", "socket_path": "/var/run/docker.sock"}, "takes no host, port, protocol or socket_path"},
		"given a token too short": {map[string]any{"name": "iota", "connection": "hawser-edge", "agent_token": "short"}, "agent_token is 5 characters"},
	} {
		if msg := suite.CallErr(t, "env_create", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	dock.Fail(made, http.StatusConflict, "An environment with this name already exists")
	if msg := suite.CallErr(t, "env_create", map[string]any{"name": "kappa", "connection": "hawser-edge"}); !strings.Contains(msg, "kappa was not added") {
		t.Errorf("one Dockhand refuses = %q", msg)
	}
}
