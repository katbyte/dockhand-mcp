package tools

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// What a 1.0.51 server answers to a connection that is tried: one that is reached, through an agent and without, and one that is not.
const (
	reachedAgent  = `{"success":true,"info":{"serverVersion":"29.9.0","containers":3,"images":2,"name":"docker-gamma","memoryLimitSupported":true,"showMemoryWarning":false},"hawser":{"agentId":"an-agent-id","agentName":"agent-gamma","capabilities":["exec","metrics","events","compose"],"dockerVersion":"29.9.0","hawserVersion":"0.2.50","mode":"standard","uptime":1432337}}`
	reachedPlain  = `{"success":true,"info":{"serverVersion":"29.9.0","containers":1,"images":1,"name":"docker-delta"},"hawser":null}`
	wrongToken    = `{"success":false,"error":"Invalid token - check that the Hawser token matches"}`
	gammaAsSaved  = `{"id":3,"name":"gamma","connectionType":"hawser-standard","host":"agent-gamma","port":2376,"protocol":"http","socketPath":"/var/run/docker.sock","labels":"[\"lab\"]","hasHawserToken":true,"hasTlsKey":false,"createdAt":"2026-10-10 08:00:00","updatedAt":"2026-10-10 08:00:00"}`
	gammaReadBack = `{"id":3,"name":"gamma","connectionType":"hawser-standard","host":"agent-gamma","port":2376,"protocol":"http","socketPath":"/var/run/docker.sock","labels":["lab"],"hasHawserToken":true,"hasTlsKey":false}`
)

// sent is the JSON a call was sent, as its fields.
func sent(t *testing.T, r dockhandtest.Request) map[string]any {
	t.Helper()

	var body map[string]any
	if err := json.Unmarshal([]byte(r.Body), &body); err != nil {
		t.Fatalf("%s %s was sent %q, which is not JSON: %v", r.Method, r.Path, r.Body, err)
	}

	return body
}

func TestEnvTest(t *testing.T) {
	t.Parallel()

	dock := twoHosts(t)
	dock.Answer("POST /api/environments/test", reachedAgent)
	dock.Answer("POST /api/environments/2/test", reachedAgent)
	suite := session(t, dock, Options{})

	// a connection that is not saved: what the kind needs is sent, with the port an agent listens on unless another is given
	out := suite.Call(t, "env_test", map[string]any{"connection": "hawser-standard", "host": "agent-gamma", "agent_token": "a-token"})
	body := sent(t, dock.Last("POST /api/environments/test"))
	if body["connectionType"] != "hawser-standard" || body["host"] != "agent-gamma" || body["port"] != float64(2376) || body["protocol"] != "http" || body["hawserToken"] != "a-token" {
		t.Errorf("sent %v", body)
	}
	if !acctest.IsBool(out["reachable"], true) || out["docker_version"] != "29.9.0" || out["docker_host"] != "docker-gamma" || acctest.Num(t, out["containers"], "containers") != 3 || out["agent"] != "agent-gamma" || out["agent_version"] != "0.2.50" || strings.Join(acctest.Strs(t, out["agent_can"], "agent_can"), " ") != "exec metrics events compose" || out["env"] != nil || !strings.Contains(acctest.Str(out["note"]), "not saved") {
		t.Errorf("an agent that is reached = %v", out)
	}
	if strings.Contains(flat(out), "a-token") {
		t.Errorf("the answer repeats the agent's token: %v", out)
	}

	// a socket that is not there, with the ones Dockhand does find on its machine; one it does not find is not named
	dock.Answer("POST /api/environments/test", `{"success":false,"error":"Docker socket not found at /var/run/docker.sock"}`)
	dock.Answer("GET /api/environments/detect-socket", `{"sockets":[{"path":"/run/user/1000/docker.sock","name":"Docker (rootless)","exists":true},{"path":"/run/podman/podman.sock","name":"Podman","exists":false}],"homedir":"/home/dockhand"}`)
	out = suite.Call(t, "env_test", map[string]any{"connection": "socket"})
	if !acctest.IsBool(out["reachable"], false) || !strings.Contains(acctest.Str(out["error"]), "socket not found") || strings.Join(acctest.Strs(t, out["sockets_found"], "sockets_found"), " ") != "/run/user/1000/docker.sock" || sent(t, dock.Last("POST /api/environments/test"))["socketPath"] != "/var/run/docker.sock" {
		t.Errorf("a socket that is not there = %v", out)
	}
	dock.Fail("GET /api/environments/detect-socket", http.StatusForbidden, "Permission denied")
	if out = suite.Call(t, "env_test", map[string]any{"connection": "socket", "socket_path": "/nowhere.sock"}); !acctest.IsBool(out["reachable"], false) || out["sockets_found"] != nil {
		t.Errorf("a socket that is not there, on a Dockhand that will not say what it has = %v", out)
	}

	// one that is not reached is an answer, with why
	dock.Answer("POST /api/environments/test", wrongToken)
	out = suite.Call(t, "env_test", map[string]any{"connection": "hawser-standard", "host": "agent-gamma", "agent_token": "another"})
	if !acctest.IsBool(out["reachable"], false) || !strings.Contains(acctest.Str(out["error"]), "Invalid token") || out["agent"] != nil {
		t.Errorf("an agent that turns Dockhand away = %v", out)
	}

	// a socket and an address, each with what it takes unless given
	dock.Answer("POST /api/environments/test", reachedPlain)
	suite.Call(t, "env_test", map[string]any{"connection": "socket"})
	if body = sent(t, dock.Last("POST /api/environments/test")); body["connectionType"] != "socket" || body["socketPath"] != "/var/run/docker.sock" || body["host"] != nil {
		t.Errorf("a socket sent %v", body)
	}
	suite.Call(t, "env_test", map[string]any{"connection": "direct", "host": "docker-delta", "protocol": "https", "port": 2443})
	if body = sent(t, dock.Last("POST /api/environments/test")); body["connectionType"] != "direct" || body["port"] != float64(2443) || body["protocol"] != "https" || body["hawserToken"] != nil {
		t.Errorf("an address sent %v", body)
	}

	// an environment that is saved, by its name
	out = suite.Call(t, "env_test", map[string]any{"env": "beta"})
	if out["env"] != "beta" || !acctest.IsBool(out["reachable"], true) || out["agent"] != "agent-gamma" || out["note"] != nil {
		t.Errorf("a saved environment tried = %v", out)
	}
}

func TestEnvTestRefused(t *testing.T) {
	t.Parallel()

	dock := twoHosts(t)
	suite := session(t, dock, Options{})

	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"nothing to try":                  {nil, "connection is required: socket, direct, hawser-standard or hawser-edge; or give env"},
		"a kind that is none":             {map[string]any{"connection": "ssh", "host": "h"}, `connection is "ssh"`},
		"an agent that calls in":          {map[string]any{"connection": "hawser-edge"}, "cannot be tried before it is saved"},
		"one that calls in from a host":   {map[string]any{"connection": "hawser-edge", "host": "h"}, "takes no host, port, protocol or socket_path"},
		"an address with no host":         {map[string]any{"connection": "direct"}, "host is required for a direct connection"},
		"an agent with no token":          {map[string]any{"connection": "hawser-standard", "host": "h"}, "agent_token is required"},
		"Docker itself given a token":     {map[string]any{"connection": "direct", "host": "h", "agent_token": "t"}, "takes no agent_token"},
		"a socket given a host":           {map[string]any{"connection": "socket", "host": "h"}, "a socket is a path"},
		"a protocol that is none":         {map[string]any{"connection": "direct", "host": "h", "protocol": "ftp"}, `protocol is "ftp"`},
		"both a saved one and an unsaved": {map[string]any{"env": "beta", "connection": "socket"}, "not both"},
		"a saved one that is none":        {map[string]any{"env": "gamma"}, `no environment is named or numbered "gamma"`},
	} {
		if msg := suite.CallErr(t, "env_test", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	for _, call := range dock.Calls() {
		if strings.HasPrefix(call, "POST ") {
			t.Errorf("%s was made for a connection that was refused before it was tried", call)
		}
	}

	dock.Fail("POST /api/environments/2/test", http.StatusForbidden, "Permission denied")
	if msg := suite.CallErr(t, "env_test", map[string]any{"env": "beta"}); !strings.Contains(msg, "beta could not be tried") || !strings.Contains(msg, "Permission denied") {
		t.Errorf("a test the account may not make = %q", msg)
	}
}

func TestEnvCreate(t *testing.T) {
	t.Parallel()

	dock := twoHosts(t)
	dock.Answer("POST /api/environments/test", reachedAgent)
	dock.Answer("POST /api/environments", gammaAsSaved)
	dock.Answer("GET /api/environments/3", gammaReadBack)
	suite := session(t, dock, everything)

	// a dry run tries the connection and saves nothing
	out := suite.Call(t, "env_create", map[string]any{"name": "gamma", "connection": "hawser-standard", "host": "agent-gamma", "agent_token": "a-token", "dry_run": true})
	if strings.Join(acctest.Strs(t, out["changes"], "changes"), "|") != "would add the environment gamma, reached by hawser-standard" || !acctest.IsBool(out["dry_run"], true) || out["environment"] != nil || len(dock.Requests("POST /api/environments")) != 0 {
		t.Errorf("a dry run = %v, with %d saved", out, len(dock.Requests("POST /api/environments")))
	}
	if found := acctest.Object(t, out["found"], "found"); !acctest.IsBool(found["reachable"], true) || found["agent"] != "agent-gamma" {
		t.Errorf("what a dry run found = %v", found)
	}

	out = suite.Call(t, "env_create", map[string]any{"name": " gamma ", "connection": "hawser-standard", "host": "agent-gamma", "agent_token": "a-token", "labels": []string{"lab"}})
	body := sent(t, dock.Last("POST /api/environments"))
	if body["name"] != "gamma" || body["connectionType"] != "hawser-standard" || body["host"] != "agent-gamma" || body["port"] != float64(2376) || body["hawserToken"] != "a-token" || len(acctest.RowsOfAny(body["labels"])) != 1 {
		t.Errorf("sent %v", body)
	}
	// what is answered is the environment read back, and what trying it found
	made := acctest.Object(t, out["environment"], "environment")
	if made["env"] != "gamma" || acctest.Num(t, made["id"], "id") != 3 || made["connection"] != "hawser-standard" || made["address"] != "agent-gamma:2376" || !acctest.IsBool(made["online"], true) {
		t.Errorf("the environment made = %v", made)
	}
	if strings.Join(acctest.Strs(t, out["changes"], "changes"), "|") != "added the environment gamma, reached by hawser-standard" || out["note"] != nil || strings.Contains(flat(out), "a-token") {
		t.Errorf("what was done = %v", out)
	}
}

func TestEnvCreateRefused(t *testing.T) {
	t.Parallel()

	dock := twoHosts(t)
	dock.Answer("POST /api/environments/test", wrongToken)
	suite := session(t, dock, everything)

	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"a blank name":                 {map[string]any{"name": "  ", "connection": "socket"}, "name is required"},
		"no connection":                {map[string]any{"name": "gamma"}, "connection is required"},
		"a name in use":                {map[string]any{"name": "ALPHA", "connection": "socket"}, `an environment is already named "ALPHA"`},
		"a host Dockhand cannot reach": {map[string]any{"name": "gamma", "connection": "hawser-standard", "host": "agent-gamma", "agent_token": "another"}, "gamma was not saved: Invalid token"},
		"the same as a dry run":        {map[string]any{"name": "gamma", "connection": "hawser-standard", "host": "agent-gamma", "agent_token": "another", "dry_run": true}, "was not saved"},
	} {
		if msg := suite.CallErr(t, "env_create", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	if made := dock.Requests("POST /api/environments"); len(made) != 0 {
		t.Errorf("%d environments were saved by calls that were refused", len(made))
	}

	// asked to, one that cannot be reached is saved, and says it cannot be
	dock.Answer("POST /api/environments", gammaAsSaved)
	dock.Answer("GET /api/environments/3", gammaReadBack)
	out := suite.Call(t, "env_create", map[string]any{"name": "gamma", "connection": "hawser-standard", "host": "agent-gamma", "agent_token": "another", "save_unreachable": true})
	if made := acctest.Object(t, out["environment"], "environment"); !acctest.IsBool(made["online"], false) || !strings.Contains(acctest.Str(out["note"]), "Dockhand cannot reach it yet: Invalid token") {
		t.Errorf("one saved though it cannot be reached = %v", out)
	}

	// refused by Dockhand, and saved but not read back
	dock.Answer("POST /api/environments/test", reachedPlain)
	dock.Fail("POST /api/environments", http.StatusConflict, "An environment with this name already exists")
	if msg := suite.CallErr(t, "env_create", map[string]any{"name": "delta", "connection": "direct", "host": "docker-delta"}); !strings.Contains(msg, "delta was not added") || !strings.Contains(msg, "already exists") {
		t.Errorf("one Dockhand refuses = %q", msg)
	}
	dock.Answer("POST /api/environments", gammaAsSaved)
	dock.Fail("GET /api/environments/3", http.StatusInternalServerError, "Failed to read environment")
	out = suite.Call(t, "env_create", map[string]any{"name": "delta", "connection": "direct", "host": "docker-delta"})
	if out["environment"] != nil || len(acctest.Strs(t, out["changes"], "changes")) != 1 || !strings.Contains(acctest.Str(out["note"]), "could not be read back") {
		t.Errorf("one added and not read back = %v", out)
	}
}

func TestEnvDelete(t *testing.T) {
	t.Parallel()

	dock := twoHosts(t)
	dock.Answer("DELETE /api/environments/2", `{"success":true}`)
	suite := session(t, dock, everything)

	// without confirm nothing goes, and what would is said, with what is left alone
	out := suite.Call(t, "env_delete", map[string]any{"env": "beta"})
	if !acctest.IsBool(out["removed"], false) || len(acctest.Strs(t, out["would_go"], "would_go")) != 1 || acctest.Num(t, out["containers"], "containers") != 1 || !strings.Contains(acctest.Str(out["note"]), "nothing was removed") || len(dock.Requests("DELETE /api/environments/2")) != 0 {
		t.Errorf("a removal not confirmed = %v", out)
	}

	dock.Then("DELETE /api/environments/2", func(dockhandtest.Request) {
		dock.Answer("GET /api/environments", `[{"id":1,"name":"alpha","connectionType":"socket"}]`)
	})
	out = suite.Call(t, "env_delete", map[string]any{"env": "beta", "confirm": true})
	if !acctest.IsBool(out["removed"], true) || out["would_go"] != nil || out["env"] != "beta" || !strings.Contains(acctest.Str(out["note"]), "the 1 containers on the host were not touched") {
		t.Errorf("a removal confirmed = %v", out)
	}
}

func TestEnvDeleteRefused(t *testing.T) {
	t.Parallel()

	dock := twoHosts(t)
	suite := session(t, dock, everything)

	if msg := suite.CallErr(t, "env_delete", map[string]any{"confirm": true}); !strings.Contains(msg, "env is required") {
		t.Errorf("a removal that names nothing = %q", msg)
	}
	if msg := suite.CallErr(t, "env_delete", map[string]any{"env": "gamma", "confirm": true}); !strings.Contains(msg, `no environment is named or numbered "gamma"`) {
		t.Errorf("a removal of what is not there = %q", msg)
	}

	dock.Fail("DELETE /api/environments/2", http.StatusForbidden, "Permission denied")
	if msg := suite.CallErr(t, "env_delete", map[string]any{"env": "beta", "confirm": true}); !strings.Contains(msg, "beta was not removed") || !strings.Contains(msg, "Permission denied") {
		t.Errorf("a removal the account may not make = %q", msg)
	}
	dock.Answer("DELETE /api/environments/2", `{"success":false}`)
	if msg := suite.CallErr(t, "env_delete", map[string]any{"env": "beta", "confirm": true}); !strings.Contains(msg, "without its being said to have succeeded") {
		t.Errorf("a removal answered without success = %q", msg)
	}
	// answered as done and still listed: not said to be removed
	dock.Answer("DELETE /api/environments/2", `{"success":true}`)
	if msg := suite.CallErr(t, "env_delete", map[string]any{"env": "beta", "confirm": true}); !strings.Contains(msg, "it is still listed") {
		t.Errorf("a removal that did not take = %q", msg)
	}

	// a host that cannot be asked for its containers can still be forgotten
	dock.FailWhen("GET /api/containers", "env", "2", http.StatusInternalServerError, "Failed to connect to the agent")
	if out := suite.Call(t, "env_delete", map[string]any{"env": "beta"}); acctest.Num(t, out["containers"], "containers") != 0 || !acctest.IsBool(out["removed"], false) {
		t.Errorf("a preview for a host that is down = %v", out)
	}

	// it is a delete: not there unless deletes are enabled
	for _, name := range register(t, Options{EnableWrite: true}) {
		if name == "env_delete" {
			t.Error("env_delete is registered with deletes not enabled")
		}
	}
}

// flat is an answer as one text, to look for something anywhere in it.
func flat(answer map[string]any) string {
	raw, _ := json.Marshal(answer)

	return string(raw)
}
