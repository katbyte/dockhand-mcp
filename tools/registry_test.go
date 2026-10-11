package tools

import (
	"maps"
	"net/http"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// What a 1.0.51 server answers about registries, with invented names: Docker Hub as it comes, the default, and one of the owner's own with a login.
const (
	hubRegistry  = `{"id":1,"name":"Docker Hub","url":"https://registry.hub.docker.com","username":null,"isDefault":true,"createdAt":"2026-10-10 10:00:00","updatedAt":"2026-10-10 10:00:00","hasCredentials":false}`
	ownRegistry  = `{"id":2,"name":"mirror","url":"http://registry.example:5000","username":"pusher","isDefault":false,"createdAt":"2026-10-10 10:00:00","updatedAt":"2026-10-10 10:00:00","hasCredentials":true}`
	newRegistry  = `{"id":3,"name":"spare","url":"https://spare.example","username":"pusher","isDefault":false,"createdAt":"2026-10-10 11:00:00","updatedAt":"2026-10-10 11:00:00","hasCredentials":true}`
	signedIn     = `{"success":true,"connectivity":true,"authenticated":true,"message":"Connected and authenticated as pusher"}`
	turnedDown   = `{"success":false,"connectivity":true,"authenticated":false,"message":"Authentication token rejected (HTTP 401)"}`
	asksForLogin = `{"success":true,"connectivity":true,"authenticated":null,"message":"Registry is reachable (requires authentication)"}`
	ownTags      = `{"tags":[{"name":"1.0"},{"name":"2.0"},{"name":"edge"}],"total":3,"page":1,"pageSize":3,"hasNext":false,"hasPrev":false}`

	registries  = "GET /api/registries"
	tryRegistry = "POST /api/registries/test"
	save        = "POST /api/registries"
	change      = "PUT /api/registries/2"
	tagsOfOne   = "GET /api/registry/tags"
)

// withRegistries is a Dockhand with two hosts that keeps Docker Hub and a registry of the owner's own.
func withRegistries(t *testing.T) *dockhandtest.Server {
	t.Helper()

	dock := twoHosts(t)
	dock.Answer(registries, `[`+ownRegistry+`,`+hubRegistry+`]`)

	return dock
}

func TestRegistryList(t *testing.T) {
	t.Parallel()

	dock := withRegistries(t)
	suite := session(t, dock, Options{})

	out := suite.Call(t, "registry_list", nil)
	rows := acctest.Rows(t, out["registries"], "registries")
	if acctest.Num(t, out["total"], "total") != 2 || rows[0]["name"] != "Docker Hub" || !acctest.IsBool(rows[0]["default"], true) || rows[0]["username"] != nil || !acctest.IsBool(rows[0]["has_credentials"], false) {
		t.Fatalf("the registries, the default first = %v", out)
	}
	if own := rows[1]; acctest.Num(t, own["id"], "id") != 2 || own["name"] != "mirror" || own["url"] != "http://registry.example:5000" || own["username"] != "pusher" || !acctest.IsBool(own["has_credentials"], true) || own["default"] != nil {
		t.Errorf("a registry of one's own = %v", own)
	}
	dock.Fail(registries, http.StatusForbidden, "Permission denied")
	if msg := suite.CallErr(t, "registry_list", nil); !strings.Contains(msg, "the registries could not be listed") || !strings.Contains(msg, "Permission denied") {
		t.Errorf("registries the account may not see = %q", msg)
	}
}

func TestRegistryTest(t *testing.T) {
	t.Parallel()

	dock := withRegistries(t)
	dock.Answer(tryRegistry, signedIn)
	suite := session(t, dock, Options{})

	// one that is saved, by its name whatever its case and by its number, with what is kept for it
	for _, named := range []any{"MIRROR", 2} {
		out := suite.Call(t, "registry_test", map[string]any{"registry": named})
		if body := sent(t, dock.Last(tryRegistry)); body["registryId"] != float64(2) || body["url"] != nil || out["registry"] != "mirror" || !acctest.IsBool(out["reachable"], true) || !acctest.IsBool(out["authenticated"], true) || out["message"] != "Connected and authenticated as pusher" || out["note"] != nil {
			t.Errorf("a saved registry tried as %v = %v, sent %v", named, out, body)
		}
	}
	// one that is not: its address without a slash after it, what was given to sign in with, and nothing kept
	out := suite.Call(t, "registry_test", map[string]any{"url": " https://spare.example/ ", "username": " pusher ", "password": "not-a-real-password"})
	if body := sent(t, dock.Last(tryRegistry)); body["url"] != "https://spare.example" || body["username"] != "pusher" || body["password"] != "not-a-real-password" || body["registryId"] != nil || out["registry"] != nil || !strings.Contains(acctest.Str(out["note"]), "not saved") || strings.Contains(flat(out), "not-a-real-password") {
		t.Errorf("an unsaved registry tried = %v, sent %v", out, body)
	}
	// turned away, and asking for a login it was not given, are answers
	dock.Answer(tryRegistry, turnedDown)
	if out = suite.Call(t, "registry_test", map[string]any{"url": "https://spare.example", "username": "pusher", "password": "wrong"}); !acctest.IsBool(out["reachable"], true) || !acctest.IsBool(out["authenticated"], false) || !strings.Contains(acctest.Str(out["message"]), "401") {
		t.Errorf("a registry that turns the login away = %v", out)
	}
	dock.Answer(tryRegistry, asksForLogin)
	if out = suite.Call(t, "registry_test", map[string]any{"url": "https://spare.example"}); !acctest.IsBool(out["reachable"], true) || out["authenticated"] != nil {
		t.Errorf("a registry that asks for a login and is given none = %v", out)
	}

	tried := len(dock.Requests(tryRegistry))
	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"nothing to try":              {nil, "give registry, to try one that is saved, or url"},
		"both":                        {map[string]any{"registry": "mirror", "url": "https://spare.example"}, "not both"},
		"an address with no scheme":   {map[string]any{"url": "spare.example:5000"}, "with how it is spoken to before it"},
		"a registry there is none of": {map[string]any{"registry": "nowhere"}, `no registry is named or numbered "nowhere": Dockhand keeps mirror, Docker Hub`},
		"a number that is no number":  {map[string]any{"registry": 1.5}, "a registry's number is a whole one"},
	} {
		if msg := suite.CallErr(t, "registry_test", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	if len(dock.Requests(tryRegistry)) != tried {
		t.Error("a registry was tried by a call that was refused")
	}
	dock.Fail(tryRegistry, http.StatusForbidden, "Permission denied")
	if msg := suite.CallErr(t, "registry_test", map[string]any{"registry": "mirror"}); !strings.Contains(msg, "the registry could not be tried") {
		t.Errorf("a test the account may not make = %q", msg)
	}
}

func TestRegistryCreate(t *testing.T) {
	t.Parallel()

	dock := withRegistries(t)
	dock.Answer(tryRegistry, signedIn)
	dock.AnswerMade(save, newRegistry)
	dock.Answer("GET /api/registries/3", newRegistry)
	suite := session(t, dock, everything)
	args := func(more map[string]any) map[string]any {
		with := map[string]any{"name": " spare ", "url": "https://spare.example/", "username": "pusher", "password": "not-a-real-password"}
		maps.Copy(with, more)

		return with
	}

	// a dry run tries it and saves nothing
	out := suite.Call(t, "registry_create", args(map[string]any{"dry_run": true, "default": true}))
	if changes := acctest.Strs(t, out["changes"], "changes"); len(changes) != 2 || changes[0] != "would save the registry spare at https://spare.example, signed in to as pusher" || changes[1] != "would save it as the default registry" || out["registry"] != nil || len(dock.Requests(save)) != 0 || !acctest.IsBool(acctest.Object(t, out["tried"], "tried")["authenticated"], true) {
		t.Errorf("a dry run = %v", out)
	}
	out = suite.Call(t, "registry_create", args(map[string]any{"default": true}))
	if body := sent(t, dock.Last(save)); body["name"] != "spare" || body["url"] != "https://spare.example" || body["username"] != "pusher" || body["password"] != "not-a-real-password" || !acctest.IsBool(body["isDefault"], true) {
		t.Errorf("sent %v", body)
	}
	if made := acctest.Object(t, out["registry"], "registry"); made["name"] != "spare" || acctest.Num(t, made["id"], "id") != 3 || !acctest.IsBool(made["has_credentials"], true) || len(acctest.Strs(t, out["changes"], "changes")) != 2 || out["note"] != nil || strings.Contains(flat(out), "not-a-real-password") {
		t.Errorf("a registry saved = %v", out)
	}
	// one that asks for no login is sent with none, and not as the default unless asked
	suite.Call(t, "registry_create", map[string]any{"name": "open", "url": "http://open.example:5000"})
	if body := sent(t, dock.Last(save)); body["username"] != nil || body["password"] != nil || body["isDefault"] != nil {
		t.Errorf("one with no login sent %v", body)
	}

	// one Dockhand cannot use is not saved, unless asked to, and then says so
	saved := len(dock.Requests(save))
	dock.Answer(tryRegistry, turnedDown)
	if msg := suite.CallErr(t, "registry_create", args(nil)); !strings.Contains(msg, "spare was not saved: Authentication token rejected") || len(dock.Requests(save)) != saved {
		t.Errorf("one that turns the login away = %q", msg)
	}
	if out = suite.Call(t, "registry_create", args(map[string]any{"save_unreachable": true})); !strings.Contains(acctest.Str(out["note"]), "Dockhand cannot use it as it is: Authentication token rejected") || out["registry"] == nil {
		t.Errorf("one saved though it cannot be used = %v", out)
	}
	dock.Answer(tryRegistry, signedIn)
	dock.Fail("GET /api/registries/3", http.StatusInternalServerError, "Failed to read the registry")
	if out = suite.Call(t, "registry_create", args(nil)); out["registry"] != nil || !strings.Contains(acctest.Str(out["note"]), "could not be read back") {
		t.Errorf("one saved and not read back = %v", out)
	}

	saved = len(dock.Requests(save))
	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"a blank name":              {map[string]any{"name": " ", "url": "https://spare.example"}, "name is required"},
		"an address with no scheme": {map[string]any{"name": "spare", "url": "spare.example"}, "with how it is spoken to before it"},
		"a name with no password":   {map[string]any{"name": "spare", "url": "https://spare.example", "username": "pusher"}, "a user name and a password go together"},
		"a password with no name":   {map[string]any{"name": "spare", "url": "https://spare.example", "password": "x"}, "a user name and a password go together"},
		"a name in use":             {map[string]any{"name": "MIRROR", "url": "https://spare.example"}, `a registry is already named "MIRROR"`},
	} {
		if msg := suite.CallErr(t, "registry_create", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	dock.Fail(save, http.StatusBadRequest, "A registry with this name already exists")
	if msg := suite.CallErr(t, "registry_create", args(nil)); !strings.Contains(msg, "spare was not saved") || !strings.Contains(msg, "already exists") {
		t.Errorf("one Dockhand refuses = %q", msg)
	}
	dock.Fail(tryRegistry, http.StatusForbidden, "Permission denied")
	if msg := suite.CallErr(t, "registry_create", args(nil)); !strings.Contains(msg, "the registry could not be tried") || len(dock.Requests(save)) != saved+1 {
		t.Errorf("one that could not be tried = %q", msg)
	}
}

func TestRegistryEdit(t *testing.T) {
	t.Parallel()

	dock := withRegistries(t)
	dock.Answer(change, strings.Replace(ownRegistry, `"name":"mirror"`, `"name":"mirror-two"`, 1))
	dock.Answer(tryRegistry, signedIn)
	suite := session(t, dock, everything)

	// a name alone: said first, then sent with the address a change must carry, and nothing tried since how it is reached did not change
	out := suite.Call(t, "registry_edit", map[string]any{"registry": "mirror", "name": "mirror-two", "dry_run": true})
	if changes := acctest.Strs(t, out["changes"], "changes"); len(changes) != 1 || changes[0] != "would rename it from mirror to mirror-two" || len(dock.Requests(change)) != 0 || acctest.Object(t, out["registry"], "registry")["name"] != "mirror" {
		t.Errorf("a dry run = %v", out)
	}
	out = suite.Call(t, "registry_edit", map[string]any{"registry": "mirror", "name": "mirror-two"})
	if body := sent(t, dock.Last(change)); body["name"] != "mirror-two" || body["url"] != "http://registry.example:5000" || body["username"] != nil || body["password"] != nil || body["isDefault"] != nil {
		t.Errorf("sent %v", body)
	}
	if acctest.Object(t, out["registry"], "registry")["name"] != "mirror-two" || out["tried"] != nil || len(dock.Requests(tryRegistry)) != 0 || acctest.Strs(t, out["changes"], "changes")[0] != "renamed it from mirror to mirror-two" {
		t.Errorf("a registry renamed = %v", out)
	}

	// its address, login and standing as the default: sent together, and tried afterwards
	out = suite.Call(t, "registry_edit", map[string]any{"registry": 2, "url": "https://registry.example/", "username": "other", "password": "not-a-real-password", "default": true})
	if body := sent(t, dock.Last(change)); body["name"] != "mirror" || body["url"] != "https://registry.example" || body["username"] != "other" || body["password"] != "not-a-real-password" || !acctest.IsBool(body["isDefault"], true) {
		t.Errorf("sent %v", body)
	}
	if len(acctest.Strs(t, out["changes"], "changes")) != 4 || !acctest.IsBool(acctest.Object(t, out["tried"], "tried")["authenticated"], true) || out["note"] != nil || sent(t, dock.Last(tryRegistry))["registryId"] != float64(2) || strings.Contains(flat(out), "not-a-real-password") {
		t.Errorf("a registry changed every way = %v", out)
	}
	// changed so that Dockhand can no longer use it: saved all the same, and said
	dock.Answer(tryRegistry, turnedDown)
	if out = suite.Call(t, "registry_edit", map[string]any{"registry": "mirror", "password": "wrong"}); !strings.Contains(acctest.Str(out["note"]), "the change is saved, and Dockhand cannot use the registry as it now is") || !acctest.IsBool(acctest.Object(t, out["tried"], "tried")["authenticated"], false) {
		t.Errorf("a registry given the wrong password = %v", out)
	}
	dock.Fail(tryRegistry, http.StatusInternalServerError, "Failed")
	if out = suite.Call(t, "registry_edit", map[string]any{"registry": "mirror", "password": "x"}); !strings.Contains(acctest.Str(out["note"]), "could not be tried afterwards") {
		t.Errorf("a registry changed and not tried = %v", out)
	}

	// what is so already is nothing to do, and nothing given is refused
	changed := len(dock.Requests(change))
	if out = suite.Call(t, "registry_edit", map[string]any{"registry": "mirror", "name": "mirror", "url": "http://registry.example:5000/", "username": "pusher"}); len(acctest.Strs(t, out["changes"], "changes")) != 0 || !strings.Contains(acctest.Str(out["note"]), "nothing to do") || len(dock.Requests(change)) != changed {
		t.Errorf("a change to what it is = %v", out)
	}
	if out = suite.Call(t, "registry_edit", map[string]any{"registry": "Docker Hub", "default": true, "forget_credentials": true}); !strings.Contains(acctest.Str(out["note"]), "nothing to do") {
		t.Errorf("the default made the default, with no credentials to forget = %v", out)
	}
	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"nothing to change":           {map[string]any{"registry": "mirror"}, "nothing to change was given"},
		"no registry":                 {map[string]any{"name": "x"}, "registry is required"},
		"a registry there is none of": {map[string]any{"registry": "nowhere", "name": "x"}, `no registry is named or numbered "nowhere"`},
		"a name another has":          {map[string]any{"registry": "mirror", "name": "docker hub"}, `another registry is named "docker hub"`},
		"an address with no scheme":   {map[string]any{"registry": "mirror", "url": "registry.example"}, "with how it is spoken to before it"},
		"forgetting and keeping both": {map[string]any{"registry": "mirror", "forget_credentials": true, "password": "x"}, "it is not given with one to keep"},
	} {
		if msg := suite.CallErr(t, "registry_edit", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	dock.Fail(change, http.StatusInternalServerError, "Failed to update registry")
	if msg := suite.CallErr(t, "registry_edit", map[string]any{"registry": "mirror", "name": "other"}); !strings.Contains(msg, "mirror was not changed") || !strings.Contains(msg, "Failed to update registry") {
		t.Errorf("a change Dockhand fails = %q", msg)
	}
}

func TestRegistryEditForgetsCredentials(t *testing.T) {
	t.Parallel()

	const forgotten = `{"id":2,"name":"mirror-two","url":"http://registry.example:5000","username":null,"isDefault":false,"hasCredentials":false}`
	dock := withRegistries(t)
	dock.Answer(change, forgotten)
	dock.Answer(tryRegistry, asksForLogin)
	suite := session(t, dock, everything)

	// forgetting sends the name and password empty, which is a call of its own, with the new name beside it
	out := suite.Call(t, "registry_edit", map[string]any{"registry": "mirror", "forget_credentials": true, "name": "mirror-two"})
	body := sent(t, dock.Last(change))
	if username, there := body["username"]; !there || username != "" || body["password"] != "" || body["name"] != "mirror-two" || body["url"] != "http://registry.example:5000" || len(dock.Requests(change)) != 1 {
		t.Errorf("sent %v, in %d calls", body, len(dock.Requests(change)))
	}
	if kept := acctest.Object(t, out["registry"], "registry"); !acctest.IsBool(kept["has_credentials"], false) || kept["username"] != nil || kept["name"] != "mirror-two" || len(acctest.Strs(t, out["changes"], "changes")) != 2 || acctest.Object(t, out["tried"], "tried")["authenticated"] != nil {
		t.Errorf("a registry with its credentials forgotten = %v", out)
	}
	// made the default in the same go, which is the other call after it
	if out = suite.Call(t, "registry_edit", map[string]any{"registry": "mirror", "forget_credentials": true, "default": true}); len(dock.Requests(change)) != 3 || !acctest.IsBool(sent(t, dock.Last(change))["isDefault"], true) || len(acctest.Strs(t, out["changes"], "changes")) != 2 {
		t.Errorf("forgotten and made the default = %v, in %d calls", out, len(dock.Requests(change)))
	}
	dock.Fail(change, http.StatusInternalServerError, "Failed to update registry")
	if msg := suite.CallErr(t, "registry_edit", map[string]any{"registry": "mirror", "forget_credentials": true}); !strings.Contains(msg, "mirror was not changed") {
		t.Errorf("a forgetting Dockhand fails = %q", msg)
	}
}

func TestRegistryDelete(t *testing.T) {
	t.Parallel()

	const remove, removeHub = "DELETE /api/registries/2", "DELETE /api/registries/1"
	dock := withRegistries(t)
	dock.Answer(remove, `{"success":true}`)
	dock.Answer(removeHub, `{"success":true}`)
	suite := session(t, dock, everything)

	out := suite.Call(t, "registry_delete", map[string]any{"registry": "mirror"})
	if would := acctest.Strs(t, out["would_go"], "would_go"); len(would) != 1 || !strings.Contains(would[0], "Dockhand's record of mirror at http://registry.example:5000, and the user name and password kept for it") || acctest.BoolOf(out["removed"]) || strings.Contains(acctest.Str(out["note"]), "default") || len(dock.Requests(remove)) != 0 {
		t.Errorf("a preview = %v", out)
	}
	if out = suite.Call(t, "registry_delete", map[string]any{"registry": "Docker Hub"}); !strings.Contains(acctest.Str(out["note"]), "it is the default registry, and Dockhand has none once it is removed") || strings.Contains(acctest.Strs(t, out["would_go"], "would_go")[0], "password") {
		t.Errorf("a preview for the default = %v", out)
	}
	dock.Then(remove, func(dockhandtest.Request) { dock.Answer(registries, `[`+hubRegistry+`]`) })
	if out = suite.Call(t, "registry_delete", map[string]any{"registry": "mirror", "confirm": true}); !acctest.BoolOf(out["removed"]) || out["note"] != nil {
		t.Errorf("a registry removed = %v", out)
	}
	dock.Then(removeHub, func(dockhandtest.Request) { dock.Answer(registries, `[]`) })
	if out = suite.Call(t, "registry_delete", map[string]any{"registry": 1, "confirm": true}); !acctest.BoolOf(out["removed"]) || !strings.Contains(acctest.Str(out["note"]), "Dockhand has none now") {
		t.Errorf("the default removed = %v", out)
	}
	if msg := suite.CallErr(t, "registry_delete", map[string]any{"registry": "mirror", "confirm": true}); !strings.Contains(msg, "Dockhand keeps none") {
		t.Errorf("removing it again = %q", msg)
	}

	// answered as removed and still listed, without success, refused, and not read back
	dock.Answer(registries, `[`+ownRegistry+`]`)
	dock.Then(remove, func(dockhandtest.Request) {})
	if msg := suite.CallErr(t, "registry_delete", map[string]any{"registry": "mirror", "confirm": true}); !strings.Contains(msg, "it is still listed") {
		t.Errorf("one removed in word alone = %q", msg)
	}
	dock.Answer(remove, `{"success":false}`)
	if msg := suite.CallErr(t, "registry_delete", map[string]any{"registry": "mirror", "confirm": true}); !strings.Contains(msg, "without its being said to have succeeded") {
		t.Errorf("one answered without success = %q", msg)
	}
	dock.Fail(remove, http.StatusForbidden, "Permission denied")
	if msg := suite.CallErr(t, "registry_delete", map[string]any{"registry": "mirror", "confirm": true}); !strings.Contains(msg, "mirror was not removed") {
		t.Errorf("one Dockhand refuses = %q", msg)
	}
	dock.Answer(remove, `{"success":true}`)
	dock.Then(remove, func(dockhandtest.Request) {
		dock.Fail(registries, http.StatusInternalServerError, "Failed to read registries")
	})
	if out = suite.Call(t, "registry_delete", map[string]any{"registry": "mirror", "confirm": true}); acctest.BoolOf(out["removed"]) || !strings.Contains(acctest.Str(out["note"]), "could not be listed afterwards") {
		t.Errorf("one removed and not read back = %v", out)
	}
}

func TestRegistryImages(t *testing.T) {
	t.Parallel()

	const catalog, search = "GET /api/registry/catalog", "GET /api/registry/search"
	dock := withRegistries(t)
	dock.Answer(catalog, `{"repositories":[{"name":"apps/api","description":"","star_count":0,"is_official":false,"is_automated":false},{"name":"apps/web","description":"","star_count":0,"is_official":false,"is_automated":false},{"name":"tools/box","description":"","star_count":0,"is_official":false,"is_automated":false}],"pagination":{"pageSize":100,"hasMore":true,"nextLast":"tools/box"}}`)
	dock.Answer(search, `[{"name":"busybox","description":"Busybox base image.","star_count":3521,"is_official":true,"is_automated":false},{"name":"someone/busybox","description":"","star_count":2,"is_official":false,"is_automated":false}]`)
	suite := session(t, dock, Options{})

	// everything a registry of one's own holds, a page at a time
	out := suite.Call(t, "registry_images", map[string]any{"registry": "mirror", "after": " apps "})
	images := acctest.Rows(t, out["images"], "images")
	if asked := dock.Last(catalog).Query; asked.Get("registry") != "2" || asked.Get("last") != "apps" || out["registry"] != "mirror" || acctest.Num(t, out["total"], "total") != 3 || images[0]["name"] != "apps/api" || images[0]["stars"] != nil || out["next"] != "tools/box" || !strings.Contains(acctest.Str(out["note"]), "give next as after") {
		t.Errorf("what a registry holds = %v, asked with %v", out, asked)
	}
	// fewer than it gave are given, and the one to read on from is the last given
	if out = suite.Call(t, "registry_images", map[string]any{"registry": "mirror", "limit": 2}); acctest.Num(t, out["total"], "total") != 2 || out["next"] != "apps/web" {
		t.Errorf("two of what it holds = %v", out)
	}
	dock.Answer(catalog, `{"repositories":[],"pagination":{"pageSize":100,"hasMore":false,"nextLast":null}}`)
	if out = suite.Call(t, "registry_images", map[string]any{"registry": "mirror"}); out["next"] != nil || !strings.Contains(acctest.Str(out["note"]), "holds no images") {
		t.Errorf("a registry with nothing in it = %v", out)
	}

	// searched: Docker Hub when no registry is named, with what it says of each image
	out = suite.Call(t, "registry_images", map[string]any{"search": " busybox ", "limit": 1})
	if asked := dock.Last(search).Query; asked.Get("term") != "busybox" || asked.Get("limit") != "1" || asked.Has("registry") || out["registry"] != "Docker Hub" || acctest.Num(t, out["total"], "total") != 1 {
		t.Errorf("Docker Hub searched = %v, asked with %v", out, asked)
	}
	if found := acctest.Rows(t, out["images"], "images")[0]; found["name"] != "busybox" || found["description"] != "Busybox base image." || acctest.Num(t, found["stars"], "stars") != 3521 || !acctest.IsBool(found["official"], true) {
		t.Errorf("an image found on Docker Hub = %v", found)
	}
	suite.Call(t, "registry_images", map[string]any{"registry": "mirror", "search": "box"})
	if dock.Last(search).Query.Get("registry") != "2" {
		t.Errorf("a registry of one's own searched was asked with %v", dock.Last(search).Query)
	}
	dock.Answer(search, `[]`)
	if out = suite.Call(t, "registry_images", map[string]any{"search": "nothing-like-it"}); !strings.Contains(acctest.Str(out["note"]), `nothing in Docker Hub has "nothing-like-it" in its name`) {
		t.Errorf("a search that finds nothing = %v", out)
	}

	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"Docker Hub with no search":     {nil, "Docker Hub lists nothing without a search"},
		"Docker Hub by name, no search": {map[string]any{"registry": "Docker Hub"}, "Docker Hub lists nothing without a search"},
		"more than can be given":        {map[string]any{"search": "x", "limit": 201}, "limit is 201"},
		"a registry there is none of":   {map[string]any{"registry": "nowhere"}, `no registry is named or numbered "nowhere"`},
	} {
		if msg := suite.CallErr(t, "registry_images", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	dock.Fail(catalog, http.StatusNotFound, "Registry does not support V2 catalog API")
	if msg := suite.CallErr(t, "registry_images", map[string]any{"registry": "mirror"}); !strings.Contains(msg, "what mirror holds could not be listed") || !strings.Contains(msg, "V2 catalog") {
		t.Errorf("a registry that keeps no list = %q", msg)
	}
	dock.Fail(search, http.StatusInternalServerError, "Could not connect to registry")
	if msg := suite.CallErr(t, "registry_images", map[string]any{"registry": "mirror", "search": "x"}); !strings.Contains(msg, "mirror could not be searched") {
		t.Errorf("a registry that cannot be reached = %q", msg)
	}
	// the registry's own refusal of what Dockhand signs in with is said as that, and not as Dockhand's refusal of the caller
	dock.Fail(catalog, http.StatusUnauthorized, "Authentication failed. Check the registry credentials.")
	dock.Fail(search, http.StatusUnauthorized, "Authentication failed. Check the registry credentials.")
	for _, args := range []map[string]any{{"registry": "mirror"}, {"registry": "mirror", "search": "x"}} {
		if msg := suite.CallErr(t, "registry_images", args); !strings.Contains(msg, "mirror turned Dockhand away: the user name and password kept for it are missing or wrong") || strings.Contains(msg, "API token") {
			t.Errorf("a registry that turns Dockhand away, asked with %v = %q", args, msg)
		}
	}
}

func TestRegistryTags(t *testing.T) {
	t.Parallel()

	const info = "GET /api/registry/tag-info"
	dock := withRegistries(t)
	dock.Answer(tagsOfOne, ownTags)
	dock.Answer(info, `{"size":1901250,"lastUpdated":null}`)
	suite := session(t, dock, Options{})

	// a registry of one's own gives the names alone, and with sizes what each takes
	out := suite.Call(t, "registry_tags", map[string]any{"registry": "mirror", "image": " /apps/web/ "})
	tags := acctest.Rows(t, out["tags"], "tags")
	if asked := dock.Last(tagsOfOne).Query; asked.Get("registry") != "2" || asked.Get("image") != "apps/web" || asked.Get("pageSize") != "50" || asked.Has("page") || out["registry"] != "mirror" || out["image"] != "apps/web" || acctest.Num(t, out["total"], "total") != 3 || tags[0]["tag"] != "1.0" || tags[0]["size_mb"] != nil || out["note"] != nil || len(dock.Requests(info)) != 0 {
		t.Errorf("the tags of an image = %v, asked with %v", out, asked)
	}
	out = suite.Call(t, "registry_tags", map[string]any{"registry": "mirror", "image": "apps/web", "sizes": true, "limit": 2})
	if tags = acctest.Rows(t, out["tags"], "tags"); len(tags) != 2 || acctest.Decimal(t, tags[1]["size_mb"], "size_mb") != 1.9 || len(dock.Requests(info)) != 2 || dock.Last(info).Query.Get("tag") != "2.0" || !strings.Contains(acctest.Str(out["note"]), "the image has 3 tags and the first 2 are given") {
		t.Errorf("two tags with their sizes = %v", out)
	}
	dock.Fail(info, http.StatusInternalServerError, "Failed")
	if out = suite.Call(t, "registry_tags", map[string]any{"registry": "mirror", "image": "apps/web", "sizes": true}); !strings.Contains(acctest.Str(out["note"]), "the sizes could not all be read") || len(acctest.Rows(t, out["tags"], "tags")) != 3 {
		t.Errorf("tags whose sizes could not be read = %v", out)
	}

	// Docker Hub gives each tag in full, a page at a time
	dock.Answer(tagsOfOne, `{"tags":[{"name":"1.37","size":2200000,"lastUpdated":"2026-09-23T07:58:37Z","digest":"sha256:d474170a9f5c"}],"total":219,"page":2,"pageSize":1,"hasNext":true,"hasPrev":true}`)
	sized := len(dock.Requests(info))
	out = suite.Call(t, "registry_tags", map[string]any{"image": "library/busybox", "limit": 1, "page": 2, "sizes": true})
	if asked := dock.Last(tagsOfOne).Query; asked.Has("registry") || asked.Get("page") != "2" || asked.Get("pageSize") != "1" || out["registry"] != "Docker Hub" || !acctest.BoolOf(out["more"]) || !strings.Contains(acctest.Str(out["note"]), "these are page 2 of them: page 3 has the next") || len(dock.Requests(info)) != sized {
		t.Errorf("a page of Docker Hub's tags = %v, asked with %v", out, asked)
	}
	if tag := acctest.Rows(t, out["tags"], "tags")[0]; tag["tag"] != "1.37" || acctest.Decimal(t, tag["size_mb"], "size_mb") != 2.2 || tag["updated"] != "2026-09-23T07:58:37Z" || tag["digest"] != "sha256:d474170a9f5c" {
		t.Errorf("a tag on Docker Hub = %v", tag)
	}
	dock.Answer(tagsOfOne, `{"tags":[],"total":0,"page":1,"pageSize":0,"hasNext":false,"hasPrev":false}`)
	if out = suite.Call(t, "registry_tags", map[string]any{"registry": "mirror", "image": "apps/web"}); !strings.Contains(acctest.Str(out["note"]), "has no tags in mirror") {
		t.Errorf("an image with no tag left = %v", out)
	}

	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"a blank image":               {map[string]any{"image": " "}, "image is"},
		"an image with its tag":       {map[string]any{"image": "apps/web:1.0"}, "without the registry's address or a tag"},
		"more than can be given":      {map[string]any{"image": "apps/web", "limit": 201}, "limit is 201"},
		"a page before the first":     {map[string]any{"image": "apps/web", "page": -1}, "page -1"},
		"a registry there is none of": {map[string]any{"registry": "nowhere", "image": "apps/web"}, `no registry is named or numbered "nowhere"`},
	} {
		if msg := suite.CallErr(t, "registry_tags", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	dock.Fail(tagsOfOne, http.StatusInternalServerError, "Image not found in registry")
	if msg := suite.CallErr(t, "registry_tags", map[string]any{"registry": "mirror", "image": "apps/none"}); !strings.Contains(msg, "mirror has no image named apps/none") {
		t.Errorf("an image the registry has none of = %q", msg)
	}
	dock.Fail(tagsOfOne, http.StatusInternalServerError, "Could not connect to registry")
	if msg := suite.CallErr(t, "registry_tags", map[string]any{"registry": "mirror", "image": "apps/web"}); !strings.Contains(msg, "the tags of apps/web in mirror could not be listed") {
		t.Errorf("a registry that cannot be reached = %q", msg)
	}
	dock.Fail(tagsOfOne, http.StatusUnauthorized, "Authentication failed. Check the registry credentials.")
	if msg := suite.CallErr(t, "registry_tags", map[string]any{"registry": "mirror", "image": "apps/web"}); !strings.Contains(msg, "mirror turned Dockhand away") || strings.Contains(msg, "API token") {
		t.Errorf("a registry that turns Dockhand away = %q", msg)
	}
}

func TestRegistryImageDelete(t *testing.T) {
	t.Parallel()

	const remove = "DELETE /api/registry/image"
	dock := withRegistries(t)
	dock.Answer(tagsOfOne, ownTags)
	dock.Answer(remove, `{"success":true,"message":"Deleted apps/web:1.0"}`)
	suite := session(t, dock, everything)
	args := func(more map[string]any) map[string]any {
		with := map[string]any{"registry": "mirror", "image": "apps/web", "tag": "1.0"}
		maps.Copy(with, more)

		return with
	}

	out := suite.Call(t, "registry_image_delete", args(nil))
	if would := acctest.Strs(t, out["would_go"], "would_go"); len(would) != 1 || !strings.HasPrefix(would[0], "apps/web:1.0 in mirror") || acctest.BoolOf(out["deleted"]) || !strings.Contains(acctest.Str(out["note"]), "any of its other tags that holds the very same contents goes with it") || len(dock.Requests(remove)) != 0 {
		t.Errorf("a preview = %v", out)
	}
	// deleted, and the tag that held the same contents is named as gone with it
	dock.Then(remove, func(dockhandtest.Request) {
		dock.Answer(tagsOfOne, `{"tags":[{"name":"edge"}],"total":1,"page":1,"pageSize":1,"hasNext":false,"hasPrev":false}`)
	})
	out = suite.Call(t, "registry_image_delete", args(map[string]any{"confirm": true}))
	if asked := dock.Last(remove).Query; asked.Get("registry") != "2" || asked.Get("image") != "apps/web" || asked.Get("tag") != "1.0" || !acctest.BoolOf(out["deleted"]) || strings.Join(acctest.Strs(t, out["also_gone"], "also_gone"), " ") != "2.0" || !strings.Contains(acctest.Str(out["note"]), "went with it") {
		t.Errorf("a tag deleted = %v, asked with %v", out, asked)
	}
	if msg := suite.CallErr(t, "registry_image_delete", args(map[string]any{"confirm": true})); !strings.Contains(msg, "apps/web has no tag 1.0 in mirror: it has edge") {
		t.Errorf("deleting it again = %q", msg)
	}
	// the only tag of its image, which takes nothing with it
	dock.Answer(tagsOfOne, `{"tags":[{"name":"edge"}],"total":1,"page":1,"pageSize":1,"hasNext":false,"hasPrev":false}`)
	if out = suite.Call(t, "registry_image_delete", args(map[string]any{"tag": "edge"})); strings.Contains(acctest.Str(out["note"]), "same contents") {
		t.Errorf("a preview for an image's only tag = %v", out)
	}
	dock.Then(remove, func(dockhandtest.Request) {
		dock.Answer(tagsOfOne, `{"tags":[],"total":0,"page":1,"pageSize":0,"hasNext":false,"hasPrev":false}`)
	})
	if out = suite.Call(t, "registry_image_delete", args(map[string]any{"tag": "edge", "confirm": true})); !acctest.BoolOf(out["deleted"]) || out["also_gone"] != nil || out["note"] != nil {
		t.Errorf("an image's only tag deleted = %v", out)
	}

	dock.Answer(tagsOfOne, ownTags)
	deleted := len(dock.Requests(remove))
	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"no tag":                      {args(map[string]any{"tag": " "}), "image and tag are required"},
		"no registry":                 {map[string]any{"image": "apps/web", "tag": "1.0"}, "registry is required"},
		"Docker Hub":                  {args(map[string]any{"registry": "Docker Hub", "confirm": true}), "Docker Hub lets no tag be deleted"},
		"a tag there is none of":      {args(map[string]any{"tag": "9.9", "confirm": true}), "apps/web has no tag 9.9 in mirror: it has 1.0, 2.0, edge"},
		"a registry there is none of": {args(map[string]any{"registry": "nowhere"}), `no registry is named or numbered "nowhere"`},
	} {
		if msg := suite.CallErr(t, "registry_image_delete", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	if len(dock.Requests(remove)) != deleted {
		t.Error("a tag was deleted by a call that was refused")
	}

	// a registry not set to allow it, answered as done with the tag still there, refused, and not read back
	dock.Then(remove, func(dockhandtest.Request) {})
	dock.Fail(remove, http.StatusMethodNotAllowed, "Registry does not allow deletion")
	if msg := suite.CallErr(t, "registry_image_delete", args(map[string]any{"confirm": true})); !strings.Contains(msg, "mirror does not allow deleting") {
		t.Errorf("a registry not set to allow deleting = %q", msg)
	}
	dock.Fail(remove, http.StatusUnauthorized, "The registry rejected authentication")
	if msg := suite.CallErr(t, "registry_image_delete", args(map[string]any{"confirm": true})); !strings.Contains(msg, "apps/web:1.0 was not deleted from mirror: mirror turned Dockhand away") || strings.Contains(msg, "API token") {
		t.Errorf("a deleting the registry turns away = %q", msg)
	}
	dock.Answer(remove, `{"success":true}`)
	dock.Then(remove, func(dockhandtest.Request) {})
	if msg := suite.CallErr(t, "registry_image_delete", args(map[string]any{"confirm": true})); !strings.Contains(msg, "mirror still lists the tag") {
		t.Errorf("a tag deleted in word alone = %q", msg)
	}
	dock.Then(remove, func(dockhandtest.Request) {
		dock.Fail(tagsOfOne, http.StatusInternalServerError, "Could not connect to registry")
	})
	if out = suite.Call(t, "registry_image_delete", args(map[string]any{"confirm": true})); acctest.BoolOf(out["deleted"]) || !strings.Contains(acctest.Str(out["note"]), "could not be listed afterwards") {
		t.Errorf("a tag deleted and not read back = %v", out)
	}
	if msg := suite.CallErr(t, "registry_image_delete", args(nil)); !strings.Contains(msg, "so nothing was deleted") {
		t.Errorf("tags that could not be listed = %q", msg)
	}
}

func TestImagePush(t *testing.T) {
	t.Parallel()

	const push = "POST /api/images/push"
	dock := withRegistries(t)
	dock.AnswerWhen("GET /api/images", "env", "1", `[{"id":"sha256:aaaaaaaaaaaa1111111111111111111111111111111111111111111111111111","repoTags":["source.example/apps/web:1.2","web:local"],"tags":["source.example/apps/web:1.2","web:local"],"size":500000000,"created":1727386302,"containers":1},{"id":"sha256:bbbbbbbbbbbb2222222222222222222222222222222222222222222222222222","repoTags":[],"tags":[],"size":1000,"created":1727386302,"containers":0}]`)
	dock.AnswerWhen("GET /api/images", "env", "2", `[]`)
	dock.Answer(push, `{"status":"complete","message":"Image pushed to registry.example:5000/apps/web:1.2","targetTag":"registry.example:5000/apps/web:1.2"}`)
	dock.Answer(tagsOfOne, `{"tags":[{"name":"1.2"}],"total":1,"page":1,"pageSize":1,"hasNext":false,"hasPrev":false}`)
	suite := session(t, dock, quick)

	// a dry run says the whole name it would go under, its own less the registry it came from, and pushes nothing
	out := suite.Call(t, "image_push", map[string]any{"env": "alpha", "image": "source.example/apps/web:1.2", "registry": "mirror", "dry_run": true})
	if out["pushed_as"] != "registry.example:5000/apps/web:1.2" || acctest.BoolOf(out["pushed"]) || !acctest.IsBool(out["dry_run"], true) || len(dock.Requests(push)) != 0 || out["image"] != "source.example/apps/web:1.2" {
		t.Errorf("a dry run = %v", out)
	}
	// pushed: by the image's id, under the name worked out here, asked for as a job, and the registry read back
	out = suite.Call(t, "image_push", map[string]any{"env": "alpha", "image": "source.example/apps/web:1.2", "registry": "mirror"})
	pushed := dock.Last(push)
	if body := sent(t, pushed); body["imageId"] != "sha256:aaaaaaaaaaaa1111111111111111111111111111111111111111111111111111" || body["registryId"] != float64(2) || body["newTag"] != "apps/web:1.2" || body["imageName"] != nil || pushed.Query.Get("env") != "1" || pushed.Accept != "*/*" {
		t.Errorf("sent %v with %v, asking for %q", body, pushed.Query, pushed.Accept)
	}
	if !acctest.BoolOf(out["pushed"]) || out["pushed_as"] != "registry.example:5000/apps/web:1.2" || out["registry"] != "mirror" || out["env"] != "alpha" || !strings.Contains(acctest.Str(out["note"]), "goes by registry.example:5000/apps/web:1.2 as well now") || dock.Last(tagsOfOne).Query.Get("image") != "apps/web" {
		t.Errorf("an image pushed = %v", out)
	}

	// under another name, with no tag its latest; by a name it goes by that is not its first; and one with no name at all needs one
	suite.Call(t, "image_push", map[string]any{"env": "alpha", "image": "web:local", "registry": "mirror", "as": " /team/site/ ", "dry_run": true})
	if out = suite.Call(t, "image_push", map[string]any{"env": "alpha", "image": "web:local", "registry": "mirror", "as": "team/site", "dry_run": true}); out["pushed_as"] != "registry.example:5000/team/site:latest" {
		t.Errorf("pushed under another name = %v", out)
	}
	if out = suite.Call(t, "image_push", map[string]any{"env": "alpha", "image": "web:local", "registry": "mirror", "dry_run": true}); out["pushed_as"] != "registry.example:5000/web:local" {
		t.Errorf("pushed by a name that is not its first = %v", out)
	}
	if out = suite.Call(t, "image_push", map[string]any{"env": "alpha", "image": "aaaaaaaaaaaa", "registry": "Docker Hub", "as": "someone/web:1", "dry_run": true}); out["pushed_as"] != "someone/web:1" {
		t.Errorf("pushed to Docker Hub, which leads a name with nothing = %v", out)
	}
	if msg := suite.CallErr(t, "image_push", map[string]any{"env": "alpha", "image": "bbbbbbbbbbbb", "registry": "mirror"}); !strings.Contains(msg, "the image has no name on the host to push it under: give as") {
		t.Errorf("an image with no name = %q", msg)
	}

	// refused before anything is sent
	sentSoFar := len(dock.Requests(push))
	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"no registry":                 {map[string]any{"env": "alpha", "image": "web:local"}, "registry is required"},
		"a registry there is none of": {map[string]any{"env": "alpha", "image": "web:local", "registry": "nowhere"}, `no registry is named or numbered "nowhere"`},
		"an image there is none of":   {map[string]any{"env": "alpha", "image": "nothing:1", "registry": "mirror"}, "nothing:1"},
		"a name that is none":         {map[string]any{"env": "alpha", "image": "web:local", "registry": "mirror", "as": "a b"}, `as is "a b"`},
		"a wait too long":             {map[string]any{"env": "alpha", "image": "web:local", "registry": "mirror", "wait": 301}, "wait is 301"},
	} {
		if msg := suite.CallErr(t, "image_push", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	if len(dock.Requests(push)) != sentSoFar {
		t.Error("an image was pushed by a call that was refused")
	}
}

func TestImagePushAsAJobAndRefused(t *testing.T) {
	t.Parallel()

	const push, job = "POST /api/images/push", "GET /api/jobs/push-1"
	dock := withRegistries(t)
	dock.AnswerWhen("GET /api/images", "env", "1", `[{"id":"sha256:aaaaaaaaaaaa1111111111111111111111111111111111111111111111111111","repoTags":["web:local"],"tags":["web:local"],"size":500000000,"created":1727386302,"containers":1}]`)
	dock.AnswerWhen("GET /api/images", "env", "2", `[]`)
	dock.Answer(push, `{"jobId":"push-1"}`)
	dock.Answer(tagsOfOne, `{"tags":[{"name":"local"}],"total":1,"page":1,"pageSize":1,"hasNext":false,"hasPrev":false}`)
	suite := session(t, dock, quick)
	args := map[string]any{"env": "alpha", "image": "web:local", "registry": "mirror"}

	// answered with a job, which is followed to its end
	dock.Answer(job, `{"id":"push-1","status":"done","lines":[{"data":{"status":"pushing","message":"Pushing to registry..."}}],"result":{"status":"complete","message":"Image pushed","targetTag":"registry.example:5000/web:local"}}`)
	if out := suite.Call(t, "image_push", args); !acctest.BoolOf(out["pushed"]) || acctest.BoolOf(out["running"]) || out["pushed_as"] != "registry.example:5000/web:local" {
		t.Errorf("a push followed as a job = %v", out)
	}
	// still going when the wait is over
	dock.Answer(job, `{"id":"push-1","status":"running","lines":[{"data":{"status":"pushing","message":"Pushing to registry..."}}]}`)
	if out := suite.Call(t, "image_push", map[string]any{"env": "alpha", "image": "web:local", "registry": "mirror", "wait": 0}); !acctest.BoolOf(out["running"]) || out["job"] != "push-1" || acctest.BoolOf(out["pushed"]) || !strings.Contains(acctest.Str(out["note"]), "job_get with job push-1") {
		t.Errorf("a push still going = %v", out)
	}
	// ended in the registry turning it away, said as a job's error and as the answer's own
	dock.Answer(job, `{"id":"push-1","status":"done","lines":[],"result":{"status":"error","error":"push access denied, repository does not exist or may require authorization"}}`)
	if msg := suite.CallErr(t, "image_push", args); !strings.Contains(msg, "web:local was not pushed to mirror") || !strings.Contains(msg, "push access denied") {
		t.Errorf("a push the registry turns away = %q", msg)
	}
	dock.Fail(push, http.StatusInternalServerError, "No such image")
	if msg := suite.CallErr(t, "image_push", args); !strings.Contains(msg, "web:local was not pushed to mirror") || !strings.Contains(msg, "No such image") {
		t.Errorf("a push Dockhand fails = %q", msg)
	}

	// answered as done and the registry does not list it, or cannot be read back
	dock.Answer(push, `{"status":"complete","targetTag":"registry.example:5000/web:local"}`)
	dock.Answer(tagsOfOne, `{"tags":[{"name":"other"}],"total":1,"page":1,"pageSize":1,"hasNext":false,"hasPrev":false}`)
	if msg := suite.CallErr(t, "image_push", args); !strings.Contains(msg, "mirror does not list the tag") {
		t.Errorf("a push in word alone = %q", msg)
	}
	dock.Fail(tagsOfOne, http.StatusInternalServerError, "Could not connect to registry")
	if out := suite.Call(t, "image_push", args); acctest.BoolOf(out["pushed"]) || !strings.Contains(acctest.Str(out["note"]), "it was pushed, and the registry could not be read back") {
		t.Errorf("a push not read back = %v", out)
	}
	// Docker Hub is not read back, having no call for it
	asked := len(dock.Requests(tagsOfOne))
	if out := suite.Call(t, "image_push", map[string]any{"env": "alpha", "image": "web:local", "registry": "Docker Hub", "as": "someone/web:1"}); !acctest.BoolOf(out["pushed"]) || len(dock.Requests(tagsOfOne)) != asked {
		t.Errorf("a push to Docker Hub = %v", out)
	}
}
