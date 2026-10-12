package tools

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// What a 1.0.51 server answers about its config sets, with invented names: one that holds everything, a token and an address with a password among
// its variables; one that holds nothing; and one whose name differs from that one's only by case, which holds a variable and a label twice and whose
// lists and modes a server may send as nothing at all.
//
//nolint:gosec // G101: the token and the password are made up, and there to be seen hidden
const (
	fullSet = `{"id":4,"name":"web","description":"a made-up web server","envVars":[{"key":"MODE","value":"demo"},{"key":"API_TOKEN","value":"not-a-real-token"},{"key":"DB","value":"postgres://app:not-a-real-word@db/app"}],"labels":[{"key":"team","value":"ops"}],"ports":[{"hostPort":"8080","containerPort":"80","protocol":"tcp"},{"hostPort":"","containerPort":"53","protocol":"udp"}],"volumes":[{"hostPath":"data","containerPath":"/var/lib/app","mode":"rw"}],"networkMode":"bridge","restartPolicy":"unless-stopped","createdAt":"2026-10-10 20:01:30","updatedAt":"2026-10-10 20:01:30"}`
	bareSet = `{"id":5,"name":"Bare","description":null,"envVars":[],"labels":[],"ports":[],"volumes":[],"networkMode":"bridge","restartPolicy":"no","createdAt":"2026-10-10 20:01:30","updatedAt":"2026-10-10 20:01:30"}`
	twinSet = `{"id":6,"name":"bare","description":null,"envVars":[{"key":"A","value":"1"},{"key":"A","value":"2"}],"labels":[{"key":"l","value":"1"},{"key":"l","value":"2"}],"ports":null,"volumes":null,"networkMode":"","restartPolicy":"","createdAt":"2026-10-10 20:01:30","updatedAt":"2026-10-10T20:01:31.160Z"}`
	newSet  = `{"id":7,"name":"new","description":"another","envVars":[{"key":"API_TOKEN","value":"not-a-real-token"},{"key":"MODE","value":"demo"}],"labels":[{"key":"team","value":"ops"}],"ports":[{"hostPort":"8080","containerPort":"80","protocol":"tcp"},{"hostPort":"","containerPort":"53","protocol":"udp"}],"volumes":[{"hostPath":"/srv/conf","containerPath":"/etc/app","mode":"ro"},{"hostPath":"data","containerPath":"/var/lib/app","mode":"rw"}],"networkMode":"host","restartPolicy":"always","createdAt":"2026-10-10 21:00:00","updatedAt":"2026-10-10 21:00:00"}`
	aToken  = "not-a-real-token"
	// withWord is an address with a made-up password in it, and hiddenWord the same as it is shown
	withWord   = "postgres://app:not-a-real-word@db/app"
	hiddenWord = "postgres://app:***@db/app"

	setsCall  = "GET /api/config-sets"
	setMake   = "POST /api/config-sets"
	setChange = "PUT /api/config-sets/4"
	setRemove = "DELETE /api/config-sets/4"
)

// withConfigSets is a Dockhand that keeps three config sets.
func withConfigSets(t *testing.T) *dockhandtest.Server {
	t.Helper()

	dock := dockhandtest.New(t)
	dock.Answer(setsCall, `[`+fullSet+`,`+twinSet+`,`+bareSet+`]`)

	return dock
}

func TestConfigSetList(t *testing.T) {
	t.Parallel()

	dock := withConfigSets(t)
	suite := session(t, dock, Options{})

	// by name, the two that differ by case in the order of their numbers, with every secret hidden
	out := suite.Call(t, "config_set_list", nil)
	rows := acctest.Rows(t, out["sets"], "sets")
	if acctest.Num(t, out["total"], "total") != 3 || acctest.Num(t, out["hidden"], "hidden") != 2 || out["note"] != nil || rows[0]["name"] != "Bare" || rows[1]["name"] != "bare" || rows[2]["name"] != "web" {
		t.Fatalf("the config sets, by name = %v", out)
	}
	web := rows[2]
	vars := acctest.Object(t, web["env_vars"], "env_vars")
	if vars["MODE"] != "demo" || vars["API_TOKEN"] != "***" || vars["DB"] != hiddenWord || acctest.Num(t, web["hidden"], "hidden") != 2 || strings.Contains(flat(out), aToken) || strings.Contains(flat(out), "not-a-real-word") {
		t.Errorf("the variables of a set, its secrets hidden = %v", web)
	}
	if web["description"] != "a made-up web server" || acctest.Object(t, web["labels"], "labels")["team"] != "ops" || strings.Join(acctest.Strs(t, web["ports"], "ports"), " ") != "8080:80/tcp 53/udp" || strings.Join(acctest.Strs(t, web["volumes"], "volumes"), " ") != "data:/var/lib/app:rw" || web["network_mode"] != "bridge" || web["restart_policy"] != "unless-stopped" || web["updated"] != "2026-10-10T20:01:30Z" || web["note"] != nil {
		t.Errorf("a set in full = %v", web)
	}
	// one that holds nothing, and one that holds a name twice: the last value is the one given, and it is said
	if bare, twin := rows[0], rows[1]; bare["env_vars"] != nil || bare["labels"] != nil || bare["ports"] != nil || bare["volumes"] != nil || bare["hidden"] != nil || acctest.Object(t, twin["env_vars"], "env_vars")["A"] != "2" || acctest.Object(t, twin["labels"], "labels")["l"] != "2" || !strings.Contains(acctest.Str(twin["note"]), "the set holds 2 names more than once") || twin["network_mode"] != nil {
		t.Errorf("a set of nothing = %v, and one with a name twice = %v", bare, twin)
	}

	// asked for, the secrets are shown
	out = suite.Call(t, "config_set_list", map[string]any{"name": " WEB ", "reveal_secrets": true})
	rows = acctest.Rows(t, out["sets"], "sets")
	if vars = acctest.Object(t, rows[0]["env_vars"], "env_vars"); len(rows) != 1 || vars["API_TOKEN"] != aToken || vars["DB"] != withWord || out["hidden"] != nil || rows[0]["hidden"] != nil {
		t.Errorf("a set with its secrets asked for = %v", out)
	}

	if out = suite.Call(t, "config_set_list", map[string]any{"name": "zzz"}); acctest.Num(t, out["total"], "total") != 0 || out["note"] != `no config set has "zzz" in its name, of the 3 the Dockhand keeps` {
		t.Errorf("a name no set has = %v", out)
	}
	dock.Answer(setsCall, `[]`)
	if out = suite.Call(t, "config_set_list", nil); acctest.Num(t, out["total"], "total") != 0 || !strings.Contains(acctest.Str(out["note"]), "keeps no config set") {
		t.Errorf("a Dockhand with no config set = %v", out)
	}
	dock.Fail(setsCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "config_set_list", nil); !strings.Contains(msg, "the config sets could not be listed") {
		t.Errorf("config sets the account may not see = %q", msg)
	}
}

// refusedSets is what neither saving nor changing a config set takes, each with why: Dockhand would save every one of them.
var refusedSets = map[string]struct {
	args map[string]any
	want string
}{
	"a variable that starts with a digit": {map[string]any{"env_vars": map[string]any{"1BAD": "x"}}, `env_vars names "1BAD"`},
	"a label with a space in its name":    {map[string]any{"labels": map[string]any{"a b": "x"}}, `labels names "a b"`},
	"a label with no name":                {map[string]any{"labels": map[string]any{"": "x"}}, `labels names ""`},
	"a port of the host that is none":     {map[string]any{"ports": []any{"web:80"}}, `"web" is no port of the host`},
	"a port of the container too high":    {map[string]any{"ports": []any{"8080:99999"}}, `"99999" is no port of the container`},
	"a protocol that is none":             {map[string]any{"ports": []any{"80/icmp"}}, `whose protocol is "icmp"`},
	"an address to publish on":            {map[string]any{"ports": []any{"127.0.0.1:8080:80"}}, "a config set has no place for an address to publish on"},
	"a mount with no target":              {map[string]any{"volumes": []any{"data"}}, `volumes has "data"`},
	"a restart policy that is none":       {map[string]any{"restart_policy": "sometimes"}, `restart_policy is "sometimes"`},
	"a count of tries":                    {map[string]any{"restart_policy": "on-failure:3"}, "a config set keeps the policy alone"},
}

func TestConfigSetCreate(t *testing.T) {
	t.Parallel()

	whole := map[string]any{"name": " new ", "description": " another ", "env_vars": map[string]any{"MODE": "demo", "API_TOKEN": aToken}, "labels": map[string]any{"team": "ops"}, "ports": []any{"8080:80", " 53/UDP "}, "volumes": []any{"/srv/conf:/etc/app:ro", "data:/var/lib/app"}, "network_mode": " host ", "restart_policy": "Always"}

	t.Run("refused before anything is sent", func(t *testing.T) {
		t.Parallel()

		dock := withConfigSets(t)
		suite := session(t, dock, everything)
		for what, tc := range refusedSets {
			if msg := suite.CallErr(t, "config_set_create", over(map[string]any{"name": "new"}, tc.args)); !strings.Contains(msg, tc.want) {
				t.Errorf("%s = %q, want %q", what, msg, tc.want)
			}
		}
		if msg := suite.CallErr(t, "config_set_create", map[string]any{"name": " "}); !strings.Contains(msg, "name is required") {
			t.Errorf("a set with no name = %q", msg)
		}
		if msg := suite.CallErr(t, "config_set_create", map[string]any{"name": " web "}); !strings.Contains(msg, "a config set is already named web, numbered 4") {
			t.Errorf("a name in use = %q", msg)
		}

		// a dry run says what would be saved, by how much of each and with no value in it
		out := suite.Call(t, "config_set_create", over(whole, map[string]any{"dry_run": true}))
		if changes := acctest.Strs(t, out["changes"], "changes"); len(changes) != 1 || changes[0] != "would save the config set new, with 2 variables, 1 label, 2 ports and 2 mounts" || out["set"] != nil || strings.Contains(flat(out), aToken) || len(dock.Requests(setMake)) != 0 {
			t.Errorf("a dry run = %v, with %d sent", out, len(dock.Requests(setMake)))
		}

		dock.Fail(setsCall, http.StatusForbidden, notAllowed)
		if msg := suite.CallErr(t, "config_set_create", map[string]any{"name": "new"}); !strings.Contains(msg, "the config sets could not be listed") {
			t.Errorf("config sets the account may not see = %q", msg)
		}
	})

	t.Run("saved and read back", func(t *testing.T) {
		t.Parallel()

		dock := withConfigSets(t)
		suite := session(t, dock, everything)
		dock.AnswerMade(setMake, newSet)
		dock.Then(setMake, func(dockhandtest.Request) { dock.Answer(setsCall, `[`+fullSet+`,`+newSet+`]`) })

		// each thing as Dockhand keeps it: the variables and labels in order of name, a port and a mount taken apart
		out := suite.Call(t, "config_set_create", whole)
		body := dock.Last(setMake).Body
		for _, want := range []string{`"name":"new"`, `"description":"another"`, `"envVars":[{"key":"API_TOKEN","value":"not-a-real-token"},{"key":"MODE","value":"demo"}]`, `"labels":[{"key":"team","value":"ops"}]`, `"ports":[{"containerPort":"80","hostPort":"8080","protocol":"tcp"},{"containerPort":"53","hostPort":"","protocol":"udp"}]`, `"volumes":[{"containerPath":"/etc/app","hostPath":"/srv/conf","mode":"ro"},{"containerPath":"/var/lib/app","hostPath":"data","mode":"rw"}]`, `"networkMode":"host"`, `"restartPolicy":"always"`} {
			if !strings.Contains(body, want) {
				t.Errorf("what was sent lacks %s: %s", want, body)
			}
		}
		// what is given back has its secrets hidden
		set := acctest.Object(t, out["set"], "set")
		if acctest.Num(t, set["id"], "id") != 7 || acctest.Object(t, set["env_vars"], "env_vars")["API_TOKEN"] != "***" || acctest.Num(t, set["hidden"], "hidden") != 1 || strings.Contains(flat(out), aToken) || out["note"] != nil || acctest.Strs(t, out["changes"], "changes")[0] != "saved the config set new, with 2 variables, 1 label, 2 ports and 2 mounts" {
			t.Errorf("a set saved = %v", out)
		}

		// a name alone is sent as that alone, and one that differs from another's by its case is said to
		dock.AnswerMade(setMake, strings.Replace(bareSet, `"id":5,"name":"Bare"`, `"id":8,"name":"WEB"`, 1))
		dock.Then(setMake, nil)
		out = suite.Call(t, "config_set_create", map[string]any{"name": "WEB"})
		if body = dock.Last(setMake).Body; body != `{"name":"WEB"}` || !strings.Contains(acctest.Str(out["note"]), "WEB is another config set from web, numbered 4: Dockhand tells them apart by their case") || !strings.Contains(acctest.Str(out["note"]), "it was saved, and is not among the config sets read back") || out["set"] != nil {
			t.Errorf("a set saved with a name alone = %v, sent %s", out, body)
		}
	})

	t.Run("not saved, or not read back", func(t *testing.T) {
		t.Parallel()

		dock := withConfigSets(t)
		suite := session(t, dock, everything)
		dock.Fail(setMake, http.StatusBadRequest, "A config set with this name already exists")
		if msg := suite.CallErr(t, "config_set_create", map[string]any{"name": "new"}); !strings.Contains(msg, "the config set new was not saved") || !strings.Contains(msg, "already exists") {
			t.Errorf("a set Dockhand refuses = %q", msg)
		}
		dock.AnswerMade(setMake, newSet)
		dock.Then(setMake, func(dockhandtest.Request) { dock.Fail(setsCall, http.StatusInternalServerError, "Failed") })
		out := suite.Call(t, "config_set_create", map[string]any{"name": "new"})
		if !strings.Contains(acctest.Str(out["note"]), "it was saved, and could not be read back") || out["set"] != nil || len(acctest.Strs(t, out["changes"], "changes")) != 1 {
			t.Errorf("a set saved and not read back = %v", out)
		}
	})
}

func TestConfigSetEdit(t *testing.T) {
	t.Parallel()

	t.Run("refused before anything is sent", func(t *testing.T) {
		t.Parallel()

		dock := withConfigSets(t)
		suite := session(t, dock, everything)
		for what, tc := range refusedSets {
			if msg := suite.CallErr(t, "config_set_edit", over(map[string]any{"set": "web"}, tc.args)); !strings.Contains(msg, tc.want) {
				t.Errorf("%s = %q, want %q", what, msg, tc.want)
			}
		}
		for what, tc := range map[string]struct {
			args map[string]any
			want string
		}{
			"a variable both set and taken out": {map[string]any{"set": "web", "env_vars": map[string]any{"MODE": "x"}, "remove_env_vars": []any{"DB", "MODE"}}, "the variable MODE is in both env_vars and remove_env_vars"},
			"a label both set and taken out":    {map[string]any{"set": "web", "labels": map[string]any{"team": "x"}, "remove_labels": []any{"team"}}, "the label team is in both labels and remove_labels"},
			"no set":                            {map[string]any{"set": " ", "name": "x"}, "set is required"},
			"a set that is no name":             {map[string]any{"set": true, "name": "x"}, "it is a name or a number"},
			"a set there is none of":            {map[string]any{"set": "nope", "name": "x"}, `no config set is named or numbered "nope": the Dockhand keeps web, bare, Bare`},
			"a name two have but for its case":  {map[string]any{"set": "BARE", "name": "x"}, `no config set is named "BARE" letter for letter, and 2 are but for their case: give the number of one, bare (6) or Bare (5)`},
			"another set's name":                {map[string]any{"set": "web", "name": "Bare"}, "another config set is named Bare, numbered 5, so web is not renamed to it"},
			"nothing to change given":           {map[string]any{"set": "web"}, "nothing to change was given"},
		} {
			if msg := suite.CallErr(t, "config_set_edit", tc.args); !strings.Contains(msg, tc.want) {
				t.Errorf("%s = %q, want %q", what, msg, tc.want)
			}
		}

		// what it holds already is nothing to do, a secret it holds among it
		same := map[string]any{"set": "WEB", "name": "web", "description": " a made-up web server ", "env_vars": map[string]any{"API_TOKEN": aToken}, "remove_env_vars": []any{"NOT_THERE"}, "labels": map[string]any{"team": "ops"}, "remove_labels": []any{"not-there"}, "ports": []any{"8080:80/tcp", "53/udp"}, "volumes": []any{"data:/var/lib/app:rw"}, "network_mode": "bridge", "restart_policy": "unless-stopped"}
		out := suite.Call(t, "config_set_edit", same)
		if !strings.Contains(acctest.Str(out["note"]), "nothing to do") || len(acctest.Strs(t, out["changes"], "changes")) != 0 || strings.Contains(flat(out), aToken) {
			t.Errorf("a set that is that way already = %v", out)
		}
		if out = suite.Call(t, "config_set_edit", map[string]any{"set": 5, "description": "", "ports": []any{}, "volumes": []any{}}); !strings.Contains(acctest.Str(out["note"]), "nothing to do") {
			t.Errorf("nothing for a set that holds nothing = %v", out)
		}

		// a dry run says what would be, by the names of what is set and never its values
		out = suite.Call(t, "config_set_edit", map[string]any{"set": 4, "name": "site", "description": "", "env_vars": map[string]any{"API_TOKEN": "another-made-up", "EXTRA": "1"}, "remove_env_vars": []any{"DB"}, "labels": map[string]any{"tier": "front"}, "remove_labels": []any{"team"}, "ports": []any{}, "volumes": []any{"/srv/conf:/etc/app:ro"}, "network_mode": "host", "restart_policy": "always", "dry_run": true})
		if got := strings.Join(acctest.Strs(t, out["changes"], "changes"), "; "); got != "would rename it from web to site; would forget its description; would set the variables API_TOKEN, EXTRA; would take out the variable DB; would set the label tier; would take out the label team; would set its ports, which were 8080:80/tcp, 53/udp, to none; would set its mounts, which were data:/var/lib/app:rw, to /srv/conf:/etc/app:ro; would change its network mode from bridge to host; would change its restart policy from unless-stopped to always" || strings.Contains(flat(out), "another-made-up") || acctest.Object(t, out["set"], "set")["name"] != "web" {
			t.Errorf("a dry run = %q", got)
		}
		// one whose modes a server sent as nothing
		if out = suite.Call(t, "config_set_edit", map[string]any{"set": "bare", "network_mode": "none", "restart_policy": "no", "description": "now with one", "dry_run": true}); strings.Join(acctest.Strs(t, out["changes"], "changes"), "; ") != "would change its description; would change its network mode from nothing to none; would change its restart policy from nothing to no" {
			t.Errorf("a dry run for a set with no modes = %v", out)
		}
		if n := len(dock.Requests(setChange)) + len(dock.Requests("PUT /api/config-sets/5")) + len(dock.Requests("PUT /api/config-sets/6")); n != 0 {
			t.Errorf("%d changes were sent for what was refused, was that way already or was a dry run", n)
		}

		dock.Fail(setsCall, http.StatusForbidden, notAllowed)
		if msg := suite.CallErr(t, "config_set_edit", map[string]any{"set": "web", "name": "x"}); !strings.Contains(msg, "the config sets could not be listed") {
			t.Errorf("config sets the account may not see = %q", msg)
		}
	})

	t.Run("changed and read back", func(t *testing.T) {
		t.Parallel()

		dock := withConfigSets(t)
		suite := session(t, dock, everything)
		dock.Answer(setChange, newSet)
		dock.Then(setChange, func(dockhandtest.Request) {
			dock.Answer(setsCall, `[`+strings.Replace(newSet, `"id":7`, `"id":4`, 1)+`]`)
		})

		// the variables it is not told of are sent back as they were, in their order, with the new one after them
		out := suite.Call(t, "config_set_edit", map[string]any{"set": "web", "name": " site ", "description": " changed ", "env_vars": map[string]any{"MODE": "live", "EXTRA": "1"}, "remove_env_vars": []any{"DB"}, "labels": map[string]any{"tier": "front"}, "ports": []any{"9090:90"}, "volumes": []any{}, "network_mode": "host", "restart_policy": "always"})
		calls := dock.Requests(setChange)
		for _, want := range []string{`"name":"site"`, `"description":"changed"`, `"envVars":[{"key":"MODE","value":"live"},{"key":"API_TOKEN","value":"not-a-real-token"},{"key":"EXTRA","value":"1"}]`, `"labels":[{"key":"team","value":"ops"},{"key":"tier","value":"front"}]`, `"ports":[{"containerPort":"90","hostPort":"9090","protocol":"tcp"}]`, `"volumes":[]`, `"networkMode":"host"`, `"restartPolicy":"always"`} {
			if !strings.Contains(calls[0].Body, want) {
				t.Errorf("what was sent lacks %s: %s", want, calls[0].Body)
			}
		}
		if set := acctest.Object(t, out["set"], "set"); len(calls) != 1 || set["name"] != "new" || acctest.Object(t, set["env_vars"], "env_vars")["API_TOKEN"] != "***" || strings.Contains(flat(out), aToken) || out["note"] != nil || len(acctest.Strs(t, out["changes"], "changes")) != 9 {
			t.Errorf("a set changed = %v, in %d calls", out, len(calls))
		}
	})

	t.Run("its description forgotten", func(t *testing.T) {
		t.Parallel()

		dock := withConfigSets(t)
		suite := session(t, dock, everything)
		dock.Answer(setChange, fullSet)

		// alone it is the one call that sends it empty, and with something else it follows the call that leaves it out
		out := suite.Call(t, "config_set_edit", map[string]any{"set": "web", "description": " "})
		if calls := dock.Requests(setChange); len(calls) != 1 || calls[0].Body != `{"description":""}` || acctest.Strs(t, out["changes"], "changes")[0] != "forgot its description" {
			t.Errorf("a description forgotten = %v, sent %v", out, calls)
		}
		out = suite.Call(t, "config_set_edit", map[string]any{"set": "web", "description": "", "restart_policy": "no", "remove_labels": []any{"team"}})
		if calls := dock.Requests(setChange); len(calls) != 3 || calls[1].Body != `{"labels":[],"restartPolicy":"no"}` || calls[2].Body != `{"description":""}` || len(acctest.Strs(t, out["changes"], "changes")) != 3 {
			t.Errorf("a description forgotten with something else changed = %v, sent %v", out, calls)
		}
	})

	t.Run("not changed, or not read back", func(t *testing.T) {
		t.Parallel()

		dock := withConfigSets(t)
		suite := session(t, dock, everything)
		dock.Fail(setChange, http.StatusBadRequest, "A config set with this name already exists")
		if msg := suite.CallErr(t, "config_set_edit", map[string]any{"set": "web", "name": "site"}); !strings.Contains(msg, "the config set web was not changed") || !strings.Contains(msg, "already exists") {
			t.Errorf("a change Dockhand refuses = %q", msg)
		}
		if msg := suite.CallErr(t, "config_set_edit", map[string]any{"set": "web", "description": ""}); !strings.Contains(msg, "the description of the config set web was not forgotten, and anything else asked for was done") {
			t.Errorf("a description Dockhand refuses to forget = %q", msg)
		}
		dock.Answer(setChange, fullSet)
		dock.Then(setChange, func(dockhandtest.Request) { dock.Fail(setsCall, http.StatusInternalServerError, "Failed") })
		out := suite.Call(t, "config_set_edit", map[string]any{"set": "web", "name": "site"})
		if !strings.Contains(acctest.Str(out["note"]), "it was saved, and could not be read back") || out["set"] != nil || len(acctest.Strs(t, out["changes"], "changes")) != 1 {
			t.Errorf("a change that could not be read back = %v", out)
		}
	})
}

func TestConfigSetDelete(t *testing.T) {
	t.Parallel()

	dock := withConfigSets(t)
	suite := session(t, dock, everything)

	// without confirm, what it holds and nothing removed
	out := suite.Call(t, "config_set_delete", map[string]any{"set": "web"})
	if would := acctest.Strs(t, out["would_go"], "would_go"); len(would) != 1 || would[0] != "the config set web, numbered 4, with its 3 variables, 1 label, 2 ports and 1 mount" || !acctest.IsBool(out["removed"], false) || out["set"] != "web" || !strings.Contains(acctest.Str(out["note"]), "nothing was removed") || len(dock.Requests(setRemove)) != 0 {
		t.Errorf("what would go = %v", out)
	}
	if msg := suite.CallErr(t, "config_set_delete", map[string]any{"set": 99, "confirm": true}); !strings.Contains(msg, `no config set is named or numbered "99"`) {
		t.Errorf("a set there is none of = %q", msg)
	}

	// answered as done and still listed; not said to be done; refused
	dock.Answer(setRemove, done)
	if msg := suite.CallErr(t, "config_set_delete", map[string]any{"set": "web", "confirm": true}); !strings.Contains(msg, "was answered as done, and it is still listed") {
		t.Errorf("a set still listed = %q", msg)
	}
	dock.Answer(setRemove, notDone)
	if msg := suite.CallErr(t, "config_set_delete", map[string]any{"set": "web", "confirm": true}); !strings.Contains(msg, "without its being said to have succeeded") {
		t.Errorf("a removal not said to be done = %q", msg)
	}
	dock.Fail(setRemove, http.StatusNotFound, "Config set not found")
	if msg := suite.CallErr(t, "config_set_delete", map[string]any{"set": "web", "confirm": true}); !strings.Contains(msg, "the config set web was not removed") {
		t.Errorf("a removal Dockhand refuses = %q", msg)
	}

	// removed, and the list not read afterwards: it is not called gone
	dock.Answer(setRemove, done)
	dock.Then(setRemove, func(dockhandtest.Request) { dock.Fail(setsCall, http.StatusInternalServerError, "Failed") })
	if out = suite.Call(t, "config_set_delete", map[string]any{"set": "web", "confirm": true}); !acctest.IsBool(out["removed"], false) || !strings.Contains(acctest.Str(out["note"]), "it was removed, and the config sets could not be listed afterwards") {
		t.Errorf("a removal that could not be read back = %v", out)
	}

	// removed and read back as gone
	dock.Answer(setsCall, `[`+fullSet+`,`+bareSet+`]`)
	dock.Then(setRemove, func(dockhandtest.Request) { dock.Answer(setsCall, `[`+bareSet+`]`) })
	if out = suite.Call(t, "config_set_delete", map[string]any{"set": 4, "confirm": true}); !acctest.IsBool(out["removed"], true) || out["would_go"] != nil || out["note"] != nil {
		t.Errorf("a set removed = %v", out)
	}
	dock.Fail(setsCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "config_set_delete", map[string]any{"set": "web", "confirm": true}); !strings.Contains(msg, "the config sets could not be listed") {
		t.Errorf("config sets the account may not see = %q", msg)
	}
}

// What a set holds twice is set, and taken out, wherever it is held, and is named once.
func TestMerged(t *testing.T) {
	t.Parallel()

	had := [][2]string{{"A", "1"}, {"B", "2"}, {"A", "3"}, {"C", "4"}, {"C", "5"}}
	kept, set, removed := merged(had, map[string]string{"A": "9", "B": "2", "Z": "0", "D": "6"}, []string{"C", "NOPE"})
	if !slices.Equal(kept, [][2]string{{"A", "9"}, {"B", "2"}, {"A", "9"}, {"D", "6"}, {"Z", "0"}}) || !slices.Equal(set, []string{"A", "D", "Z"}) || !slices.Equal(removed, []string{"C"}) {
		t.Errorf("merged = %v, set %v, removed %v", kept, set, removed)
	}
	if had[0][1] != "1" {
		t.Errorf("what it had was changed in place: %v", had)
	}
	if kept, set, removed = merged(nil, nil, nil); len(kept)+len(set)+len(removed) != 0 {
		t.Errorf("nothing merged with nothing = %v, %v, %v", kept, set, removed)
	}
}
