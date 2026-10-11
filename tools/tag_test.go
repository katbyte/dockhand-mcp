package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// What a 1.0.51 server answers about its tags, with invented names: three tags, one with an icon; on alpha a tag on two containers and a stack, one
// on a container and on a name no container has any more, and one on a stack that is gone; on beta one container tagged.
const (
	threeTags      = `{"tags":[{"id":3,"name":"web","color":"teal","icon":"globe"},{"id":1,"name":"Critical","color":"red","icon":null},{"id":7,"name":"spare","color":"slate","icon":null}]}`
	alphaTagged    = `{"web-front-1":[3,1],"loose":[1],"gone":[3]}`
	alphaStackTags = `{"web":[3],"old":[1]}`
	betaTagged     = `{"solo":[1]}`
	done           = `{"success":true}`
	notDone        = `{"success":false}`

	tagsCall      = "GET /api/tags"
	tagMake       = "POST /api/tags"
	tagChange     = "PUT /api/tags/3"
	tagRemove     = "DELETE /api/tags/3"
	containerTags = "GET /api/container-tags"
	stackTags     = "GET /api/stack-tags"
	looseTags     = "GET /api/container-tags/loose"
	looseSet      = "PUT /api/container-tags/loose"
	goneTags      = "GET /api/container-tags/gone"
	goneSet       = "PUT /api/container-tags/gone"
	webTags       = "GET /api/stacks/web/tags"
	webSet        = "PUT /api/stacks/web/tags"
)

// withTags is a Dockhand managing alpha and beta that keeps three tags, with what each host has given them to.
func withTags(t *testing.T) *dockhandtest.Server {
	t.Helper()

	dock := twoHosts(t)
	dock.Answer(tagsCall, threeTags)
	dock.AnswerWhen(containerTags, "env", "1", alphaTagged)
	dock.AnswerWhen(containerTags, "env", "2", betaTagged)
	dock.AnswerWhen(stackTags, "env", "1", alphaStackTags)
	dock.AnswerWhen(stackTags, "env", "2", `{}`)
	dock.Answer(looseTags, `{"tagIds":[1]}`)
	dock.Answer(goneTags, `{"tagIds":[3]}`)
	dock.Answer(webTags, `{"tagIds":[3]}`)

	return dock
}

func TestTagList(t *testing.T) {
	t.Parallel()

	dock := withTags(t)
	suite := session(t, dock, Options{})

	// by name whatever its case, each with where it is on
	out := suite.Call(t, "tag_list", nil)
	rows := acctest.Rows(t, out["tags"], "tags")
	if acctest.Num(t, out["total"], "total") != 3 || out["note"] != nil || out["failed"] != nil || rows[0]["name"] != "Critical" || rows[1]["name"] != "spare" || rows[2]["name"] != "web" {
		t.Fatalf("the tags, by name = %v", out)
	}
	critical := acctest.Rows(t, rows[0]["on"], "on")
	if acctest.Num(t, rows[0]["id"], "id") != 1 || rows[0]["color"] != "red" || rows[0]["icon"] != nil || len(critical) != 2 || critical[0]["env"] != "alpha" || strings.Join(acctest.Strs(t, critical[0]["containers"], "containers"), " ") != "loose web-front-1" || strings.Join(acctest.Strs(t, critical[0]["stacks"], "stacks"), " ") != "old" || critical[1]["env"] != "beta" || strings.Join(acctest.Strs(t, critical[1]["containers"], "containers"), " ") != "solo" || critical[1]["stacks"] != nil {
		t.Errorf("a tag on two hosts = %v", rows[0])
	}
	// a name is listed whether or not a container has it still
	web := acctest.Rows(t, rows[2]["on"], "on")
	if rows[2]["icon"] != "globe" || rows[2]["color"] != "teal" || len(web) != 1 || strings.Join(acctest.Strs(t, web[0]["containers"], "containers"), " ") != "gone web-front-1" || strings.Join(acctest.Strs(t, web[0]["stacks"], "stacks"), " ") != "web" || rows[1]["on"] != nil {
		t.Errorf("a tag with an icon, and one on nothing = %v, %v", rows[2], rows[1])
	}

	// one host asked
	out = suite.Call(t, "tag_list", map[string]any{"env": "beta"})
	rows = acctest.Rows(t, out["tags"], "tags")
	if on := acctest.Rows(t, rows[0]["on"], "on"); len(rows) != 3 || len(on) != 1 || on[0]["env"] != "beta" || rows[2]["on"] != nil {
		t.Errorf("the tags as one host has them = %v", out)
	}
	for env, want := range map[any]string{"nowhere": "nowhere", 1.5: "a whole one"} {
		if msg := suite.CallErr(t, "tag_list", map[string]any{"env": env}); !strings.Contains(msg, want) {
			t.Errorf("env %v = %q, want %q", env, msg, want)
		}
	}

	// a host that cannot be asked is said, and the others still answer
	dock.FailWhen(stackTags, "env", "2", http.StatusInternalServerError, "Failed to connect to the agent")
	dock.FailWhen(containerTags, "env", "1", http.StatusInternalServerError, "Failed")
	out = suite.Call(t, "tag_list", nil)
	failed := rowsNamed(acctest.Rows(t, out["failed"], "failed"), "env")
	if rows = acctest.Rows(t, out["tags"], "tags"); len(failed) != 2 || !strings.Contains(acctest.Str(failed["beta"]["error"]), "Failed to connect to the agent") || failed["alpha"] == nil || len(rows) != 3 || rows[0]["on"] != nil {
		t.Errorf("hosts that could not be asked = %v", out)
	}

	// a Dockhand that manages no host still keeps tags
	dock.Answer("GET /api/environments", `[]`)
	if out = suite.Call(t, "tag_list", nil); acctest.Num(t, out["total"], "total") != 3 || out["failed"] != nil {
		t.Errorf("the tags of a Dockhand with no host = %v", out)
	}
	dock.Fail("GET /api/environments", http.StatusInternalServerError, "Failed")
	if msg := suite.CallErr(t, "tag_list", nil); !strings.Contains(msg, "the environments could not be listed") {
		t.Errorf("environments that could not be listed = %q", msg)
	}

	dock.Answer(tagsCall, `{"tags":[]}`)
	dock.Answer("GET /api/environments", `[]`)
	if out = suite.Call(t, "tag_list", nil); acctest.Num(t, out["total"], "total") != 0 || !strings.Contains(acctest.Str(out["note"]), "keeps no tag") {
		t.Errorf("a Dockhand with no tag = %v", out)
	}
	dock.Fail(tagsCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "tag_list", nil); !strings.Contains(msg, "the tags could not be listed") {
		t.Errorf("tags the account may not see = %q", msg)
	}
}

func TestTagCreate(t *testing.T) {
	t.Parallel()

	dock := withTags(t)
	suite := session(t, dock, everything)

	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"no name":             {map[string]any{"name": " "}, "name is required"},
		"a colour it has not": {map[string]any{"name": "db", "color": "yellow"}, `color is "yellow": a tag is slate, red`},
		"a name in use":       {map[string]any{"name": " CRITICAL "}, "a tag is already named Critical, numbered 1"},
	} {
		if msg := suite.CallErr(t, "tag_create", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}

	// a dry run says what would be made and makes nothing
	out := suite.Call(t, "tag_create", map[string]any{"name": " db ", "color": " Blue ", "icon": " star ", "dry_run": true})
	if changes := acctest.Strs(t, out["changes"], "changes"); len(changes) != 1 || changes[0] != "would make the tag db, blue, with the icon star" || !acctest.IsBool(out["dry_run"], true) || out["tag"] != nil || len(dock.Requests(tagMake)) != 0 {
		t.Errorf("a dry run = %v, with %d sent", out, len(dock.Requests(tagMake)))
	}

	// made as asked, and what Dockhand answers is what is given back
	dock.Answer(tagMake, `{"id":9,"name":"db","color":"blue","icon":"star"}`)
	out = suite.Call(t, "tag_create", map[string]any{"name": " db ", "color": " Blue ", "icon": " star "})
	made := acctest.Object(t, out["tag"], "tag")
	if body := sent(t, dock.Last(tagMake)); body["name"] != "db" || body["color"] != "blue" || body["icon"] != "star" || acctest.Num(t, made["id"], "id") != 9 || made["name"] != "db" || made["color"] != "blue" || made["icon"] != "star" || out["note"] != nil || acctest.Strs(t, out["changes"], "changes")[0] != "made the tag db, blue, with the icon star" {
		t.Errorf("a tag made = %v, sent %v", out, body)
	}

	// no colour and no icon are sent as neither
	dock.Answer(tagMake, `{"id":10,"name":"plain","color":"slate","icon":null}`)
	out = suite.Call(t, "tag_create", map[string]any{"name": "plain"})
	if body := dock.Last(tagMake).Body; body != `{"name":"plain"}` || out["note"] != nil || acctest.Strs(t, out["changes"], "changes")[0] != "made the tag plain, slate" {
		t.Errorf("a tag made with a name alone = %v, sent %s", out, body)
	}

	// what Dockhand made is said when it is not what was asked
	for answer, want := range map[string]string{
		`{"id":11,"name":"odd","color":"slate","icon":null}`:  "a tag that is slate with no icon, which is not what was asked",
		`{"id":11,"name":"odd","color":"blue","icon":"moon"}`: "a tag that is blue with the icon moon, which is not what was asked",
	} {
		dock.Answer(tagMake, answer)
		if out = suite.Call(t, "tag_create", map[string]any{"name": "odd", "color": "blue", "icon": "star"}); !strings.Contains(acctest.Str(out["note"]), want) {
			t.Errorf("answered %s = %v, want %q", answer, out, want)
		}
	}

	dock.Fail(tagMake, http.StatusBadRequest, "A valid tag name is required")
	if msg := suite.CallErr(t, "tag_create", map[string]any{"name": "db"}); !strings.Contains(msg, "the tag db was not made") || !strings.Contains(msg, "A valid tag name is required") {
		t.Errorf("a tag Dockhand refuses = %q", msg)
	}
	dock.Fail(tagsCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "tag_create", map[string]any{"name": "db"}); !strings.Contains(msg, "the tags could not be listed") {
		t.Errorf("tags the account may not see = %q", msg)
	}
}

func TestTagEdit(t *testing.T) {
	t.Parallel()

	t.Run("refused before anything is sent", func(t *testing.T) {
		t.Parallel()

		dock := withTags(t)
		suite := session(t, dock, everything)
		for what, tc := range map[string]struct {
			args map[string]any
			want string
		}{
			"a colour it has not":     {map[string]any{"tag": "web", "color": "brown"}, `color is "brown"`},
			"no tag":                  {map[string]any{"tag": " ", "name": "x"}, "tag is required"},
			"a tag that is no name":   {map[string]any{"tag": true, "name": "x"}, "it is a name or a number"},
			"a tag there is none of":  {map[string]any{"tag": 99, "name": "x"}, `no tag is named or numbered "99": the Dockhand keeps web, Critical, spare`},
			"another tag's name":      {map[string]any{"tag": "web", "name": "critical"}, "another tag is named Critical, numbered 1, so web is not renamed to it"},
			"nothing to change given": {map[string]any{"tag": "web"}, "nothing to change was given"},
		} {
			if msg := suite.CallErr(t, "tag_edit", tc.args); !strings.Contains(msg, tc.want) {
				t.Errorf("%s = %q, want %q", what, msg, tc.want)
			}
		}

		// what it is already is nothing to do, and a dry run sends nothing
		out := suite.Call(t, "tag_edit", map[string]any{"tag": "WEB", "name": "web", "color": "TEAL", "icon": " globe "})
		if !strings.Contains(acctest.Str(out["note"]), "nothing to do") || len(acctest.Strs(t, out["changes"], "changes")) != 0 {
			t.Errorf("a tag that is that way already = %v", out)
		}
		out = suite.Call(t, "tag_edit", map[string]any{"tag": 3, "name": "front", "color": "pink", "icon": "", "dry_run": true})
		if changes := strings.Join(acctest.Strs(t, out["changes"], "changes"), "; "); changes != "would rename it from web to front; would change its colour from teal to pink; would take away its icon, which was globe" || acctest.Object(t, out["tag"], "tag")["name"] != "web" {
			t.Errorf("a dry run = %v", out)
		}
		if out = suite.Call(t, "tag_edit", map[string]any{"tag": 3, "icon": "star", "dry_run": true}); acctest.Strs(t, out["changes"], "changes")[0] != "would change its icon to star" || len(dock.Requests(tagChange)) != 0 {
			t.Errorf("a dry run of another icon = %v, with %d sent", out, len(dock.Requests(tagChange)))
		}

		dock.Fail(tagsCall, http.StatusForbidden, notAllowed)
		if msg := suite.CallErr(t, "tag_edit", map[string]any{"tag": "web", "name": "x"}); !strings.Contains(msg, "the tags could not be listed") {
			t.Errorf("tags the account may not see = %q", msg)
		}
	})

	t.Run("changed and read back", func(t *testing.T) {
		t.Parallel()

		dock := withTags(t)
		suite := session(t, dock, everything)
		dock.Answer(tagChange, done)
		dock.Then(tagChange, func(dockhandtest.Request) {
			dock.Answer(tagsCall, strings.Replace(threeTags, `{"id":3,"name":"web","color":"teal","icon":"globe"}`, `{"id":3,"name":"front","color":"pink","icon":"star"}`, 1))
		})
		out := suite.Call(t, "tag_edit", map[string]any{"tag": "web", "name": " front ", "color": "Pink", "icon": "star"})
		row := acctest.Object(t, out["tag"], "tag")
		if body := dock.Last(tagChange).Body; body != `{"color":"pink","icon":"star","name":"front"}` || len(dock.Requests(tagChange)) != 1 || row["name"] != "front" || row["color"] != "pink" || row["icon"] != "star" || out["note"] != nil || strings.Join(acctest.Strs(t, out["changes"], "changes"), "; ") != "renamed it from web to front; changed its colour from teal to pink; changed its icon to star" {
			t.Errorf("a tag changed = %v, sent %s", out, body)
		}
	})

	t.Run("its icon taken away", func(t *testing.T) {
		t.Parallel()

		dock := withTags(t)
		suite := session(t, dock, everything)
		dock.Answer(tagChange, done)
		dock.Then(tagChange, func(r dockhandtest.Request) {
			if r.Body == `{"icon":""}` {
				dock.Answer(tagsCall, strings.Replace(threeTags, `{"id":3,"name":"web","color":"teal","icon":"globe"}`, `{"id":3,"name":"web","color":"pink","icon":null}`, 1))
			}
		})

		// the colour goes by the call that leaves an empty text out, and the icon by one that sends it
		out := suite.Call(t, "tag_edit", map[string]any{"tag": "web", "color": "pink", "icon": " "})
		calls := dock.Requests(tagChange)
		if row := acctest.Object(t, out["tag"], "tag"); len(calls) != 2 || calls[0].Body != `{"color":"pink"}` || calls[1].Body != `{"icon":""}` || row["icon"] != nil || row["color"] != "pink" || out["note"] != nil || strings.Join(acctest.Strs(t, out["changes"], "changes"), "; ") != "changed its colour from teal to pink; took away its icon, which was globe" {
			t.Errorf("a colour changed and an icon taken away = %v, sent %v", out, calls)
		}
	})

	t.Run("what Dockhand did not take", func(t *testing.T) {
		t.Parallel()

		dock := withTags(t)
		suite := session(t, dock, everything)
		dock.Answer(tagChange, done)
		dock.Answer("PUT /api/tags/1", done)

		// answered as done and read back as it was
		out := suite.Call(t, "tag_edit", map[string]any{"tag": "web", "color": "pink"})
		if !strings.Contains(acctest.Str(out["note"]), "what it has is not all that was asked: the tag is named web, is teal and has the icon globe") || acctest.Object(t, out["tag"], "tag")["color"] != "teal" {
			t.Errorf("a colour Dockhand kept = %v", out)
		}
		if out = suite.Call(t, "tag_edit", map[string]any{"tag": 1, "icon": "bolt"}); !strings.Contains(acctest.Str(out["note"]), "the tag is named Critical, is red and has no icon") {
			t.Errorf("an icon Dockhand did not take = %v", out)
		}
		// the icon alone goes by the one call
		if out = suite.Call(t, "tag_edit", map[string]any{"tag": "web", "icon": ""}); !strings.Contains(acctest.Str(out["note"]), "has the icon globe") || dock.Last(tagChange).Body != `{"icon":""}` {
			t.Errorf("an icon Dockhand kept = %v", out)
		}

		// answered without its being said to be done
		dock.Answer(tagChange, notDone)
		if msg := suite.CallErr(t, "tag_edit", map[string]any{"tag": "web", "color": "pink"}); !strings.Contains(msg, "the tag web was not changed: the answer did not say that it was") {
			t.Errorf("a change not said to be done = %q", msg)
		}
		if msg := suite.CallErr(t, "tag_edit", map[string]any{"tag": "web", "icon": ""}); !strings.Contains(msg, "the icon of the tag web was not taken away, and anything else asked for was done: the answer did not say that it was") {
			t.Errorf("an icon not said to be taken away = %q", msg)
		}
		dock.Fail(tagChange, http.StatusConflict, `A tag named "front" already exists`)
		if msg := suite.CallErr(t, "tag_edit", map[string]any{"tag": "web", "name": "front"}); !strings.Contains(msg, "the tag web was not changed") || !strings.Contains(msg, "already exists") {
			t.Errorf("a change Dockhand refuses = %q", msg)
		}
		if msg := suite.CallErr(t, "tag_edit", map[string]any{"tag": "web", "icon": ""}); !strings.Contains(msg, "the icon of the tag web was not taken away") {
			t.Errorf("an icon Dockhand refuses to take away = %q", msg)
		}
	})

	t.Run("not read back", func(t *testing.T) {
		t.Parallel()

		dock := withTags(t)
		suite := session(t, dock, everything)
		dock.Answer(tagChange, done)
		dock.Then(tagChange, func(dockhandtest.Request) { dock.Answer(tagsCall, `{"tags":[]}`) })
		if msg := suite.CallErr(t, "tag_edit", map[string]any{"tag": "web", "color": "pink"}); !strings.Contains(msg, "was answered as done, and the Dockhand keeps no tag numbered 3 any more") {
			t.Errorf("a tag gone once changed = %q", msg)
		}

		dock.Answer(tagsCall, threeTags)
		dock.Then(tagChange, func(dockhandtest.Request) { dock.Fail(tagsCall, http.StatusInternalServerError, "Failed") })
		out := suite.Call(t, "tag_edit", map[string]any{"tag": "web", "color": "pink"})
		if !strings.Contains(acctest.Str(out["note"]), "it was changed, and could not be read back") || out["tag"] != nil || len(acctest.Strs(t, out["changes"], "changes")) != 1 {
			t.Errorf("a change that could not be read back = %v", out)
		}
	})
}

func TestTagDelete(t *testing.T) {
	t.Parallel()

	t.Run("said before it goes", func(t *testing.T) {
		t.Parallel()

		dock := withTags(t)
		suite := session(t, dock, everything)

		// without confirm, what has it and nothing removed
		out := suite.Call(t, "tag_delete", map[string]any{"tag": "critical"})
		if would := strings.Join(acctest.Strs(t, out["would_go"], "would_go"), " | "); would != "the tag Critical | on alpha, its place on 2 containers, loose, web-front-1; and on 1 stack, old | on beta, its place on 1 container, solo" || !acctest.IsBool(out["removed"], false) || out["tag"] != "Critical" || !strings.Contains(acctest.Str(out["note"]), "nothing was removed") || strings.Contains(acctest.Str(out["note"]), "could not be read") {
			t.Errorf("what would go = %v", out)
		}
		// one on a stack alone
		dock.AnswerWhen(containerTags, "env", "1", `{}`)
		if out = suite.Call(t, "tag_delete", map[string]any{"tag": 3}); strings.Join(acctest.Strs(t, out["would_go"], "would_go"), " | ") != "the tag web | on alpha, its place on 1 stack, web" {
			t.Errorf("what would go of a tag on a stack = %v", out)
		}

		// a host that cannot be asked is said
		dock.FailWhen(containerTags, "env", "2", http.StatusInternalServerError, "Failed to connect to the agent")
		if out = suite.Call(t, "tag_delete", map[string]any{"tag": "critical"}); !strings.Contains(acctest.Str(out["note"]), "what has the tag on beta could not be read") || len(acctest.Strs(t, out["would_go"], "would_go")) != 2 {
			t.Errorf("what would go with a host that cannot be asked = %v", out)
		}
		dock.Fail("GET /api/environments", http.StatusInternalServerError, "Failed")
		if out = suite.Call(t, "tag_delete", map[string]any{"tag": "critical"}); !strings.Contains(acctest.Str(out["note"]), "what has the tag could not be read: the environments could not be listed") || len(acctest.Strs(t, out["would_go"], "would_go")) != 1 {
			t.Errorf("what would go with no host to ask = %v", out)
		}
		if n := len(dock.Requests("DELETE /api/tags/1")) + len(dock.Requests(tagRemove)); n != 0 {
			t.Errorf("%d removals were sent with no confirm", n)
		}

		for tag, want := range map[any]string{"nope": `no tag is named or numbered "nope"`, " ": "tag is required"} {
			if msg := suite.CallErr(t, "tag_delete", map[string]any{"tag": tag, "confirm": true}); !strings.Contains(msg, want) {
				t.Errorf("tag %v = %q, want %q", tag, msg, want)
			}
		}
		dock.Fail(tagsCall, http.StatusForbidden, notAllowed)
		if msg := suite.CallErr(t, "tag_delete", map[string]any{"tag": "web", "confirm": true}); !strings.Contains(msg, "the tags could not be listed") {
			t.Errorf("tags the account may not see = %q", msg)
		}
	})

	t.Run("removed and read back as gone", func(t *testing.T) {
		t.Parallel()

		dock := withTags(t)
		suite := session(t, dock, everything)
		dock.Answer(tagRemove, done)
		dock.Then(tagRemove, func(dockhandtest.Request) {
			dock.Answer(tagsCall, strings.Replace(threeTags, `{"id":3,"name":"web","color":"teal","icon":"globe"},`, ``, 1))
		})
		out := suite.Call(t, "tag_delete", map[string]any{"tag": "web", "confirm": true})
		if !acctest.IsBool(out["removed"], true) || out["would_go"] != nil || out["note"] != "it was on 2 containers and 1 stack, and is on none now" {
			t.Errorf("a tag removed = %v", out)
		}

		// one that was on nothing, with a host that could not be asked
		dock.Answer("DELETE /api/tags/7", done)
		dock.Then("DELETE /api/tags/7", func(dockhandtest.Request) { dock.Answer(tagsCall, `{"tags":[]}`) })
		dock.FailWhen(stackTags, "env", "1", http.StatusInternalServerError, "Failed")
		if out = suite.Call(t, "tag_delete", map[string]any{"tag": "spare", "confirm": true}); !acctest.IsBool(out["removed"], true) || out["note"] != "what has the tag on alpha could not be read" {
			t.Errorf("a tag on nothing removed = %v", out)
		}
	})

	t.Run("not removed", func(t *testing.T) {
		t.Parallel()

		dock := withTags(t)
		suite := session(t, dock, everything)

		// answered as done and still listed
		dock.Answer(tagRemove, done)
		if msg := suite.CallErr(t, "tag_delete", map[string]any{"tag": "web", "confirm": true}); !strings.Contains(msg, "was answered as done, and it is still listed") {
			t.Errorf("a tag still listed = %q", msg)
		}
		dock.Answer(tagRemove, notDone)
		if msg := suite.CallErr(t, "tag_delete", map[string]any{"tag": "web", "confirm": true}); !strings.Contains(msg, "without its being said to have succeeded") {
			t.Errorf("a removal not said to be done = %q", msg)
		}
		dock.Fail(tagRemove, http.StatusForbidden, notAllowed)
		if msg := suite.CallErr(t, "tag_delete", map[string]any{"tag": "web", "confirm": true}); !strings.Contains(msg, "the tag web was not removed") {
			t.Errorf("a removal Dockhand refuses = %q", msg)
		}

		// removed, and the list not read afterwards: it is not called gone
		dock.Answer(tagRemove, done)
		dock.Then(tagRemove, func(dockhandtest.Request) { dock.Fail(tagsCall, http.StatusInternalServerError, "Failed") })
		out := suite.Call(t, "tag_delete", map[string]any{"tag": "web", "confirm": true})
		if !acctest.IsBool(out["removed"], false) || !strings.Contains(acctest.Str(out["note"]), "it was removed, and the tags could not be listed afterwards") {
			t.Errorf("a removal that could not be read back = %v", out)
		}
	})
}

func TestTagAssign(t *testing.T) {
	t.Parallel()

	alpha := map[string]any{"env": "alpha"}

	t.Run("refused before anything is sent", func(t *testing.T) {
		t.Parallel()

		dock := withTags(t)
		dock.Answer("GET /api/container-tags/nothing-here", `{"tagIds":[]}`)
		dock.Answer("GET /api/stacks/nothing-here/tags", `{"tagIds":[]}`)
		suite := session(t, dock, everything)
		for what, tc := range map[string]struct {
			args map[string]any
			want string
		}{
			"neither a container nor a stack": {map[string]any{"env": "alpha", "tags": []any{"web"}}, "give container or stack, one of them"},
			"both a container and a stack":    {map[string]any{"env": "alpha", "container": "loose", "stack": "web", "tags": []any{"web"}}, "give container or stack, one of them"},
			"nothing to set":                  {map[string]any{"env": "alpha", "container": "loose"}, "nothing to set was given"},
			"the whole set and some of it":    {map[string]any{"env": "alpha", "container": "loose", "tags": []any{}, "add": []any{"web"}}, "it is not given with add or remove"},
			"no environment":                  {map[string]any{"container": "loose", "tags": []any{}}, "env is required"},
			"a tag there is none of":          {map[string]any{"env": "alpha", "container": "loose", "tags": []any{"web", "nope"}}, `no tag is named or numbered "nope"`},
			"one to add there is none of":     {map[string]any{"env": "alpha", "container": "loose", "add": []any{99}}, `no tag is named or numbered "99"`},
			"one to remove that is no name":   {map[string]any{"env": "alpha", "container": "loose", "remove": []any{true}}, "it is a name or a number"},
			"one both given and taken":        {map[string]any{"env": "alpha", "container": "loose", "add": []any{"web"}, "remove": []any{3}}, "the tag numbered 3 is in both add and remove"},
			"a name no container has":         {map[string]any{"env": "alpha", "container": "nothing-here", "add": []any{"web"}}, `no container is named "nothing-here" on alpha: container_list gives the ones there are. Dockhand would save tags under the name all the same`},
			"a name no stack has":             {map[string]any{"env": "alpha", "stack": "nothing-here", "tags": []any{}}, `no stack is named "nothing-here" on alpha: stack_list gives the ones there are`},
			"a tag for a name that is gone":   {map[string]any{"env": "alpha", "container": "gone", "add": []any{"critical"}}, `no container is named "gone" on alpha now, and the name holds the tags web from when one was: they can be taken from it, and none is given to it`},
		} {
			if msg := suite.CallErr(t, "tag_assign", tc.args); !strings.Contains(msg, tc.want) {
				t.Errorf("%s = %q, want %q", what, msg, tc.want)
			}
		}
		if n := len(dock.Requests(looseSet)) + len(dock.Requests(goneSet)) + len(dock.Requests(webSet)); n != 0 {
			t.Errorf("%d changes were sent for what was refused", n)
		}
	})

	t.Run("what cannot be read", func(t *testing.T) {
		t.Parallel()

		dock := withTags(t)
		suite := session(t, dock, everything)
		dock.Fail(looseTags, http.StatusInternalServerError, "Failed")
		if msg := suite.CallErr(t, "tag_assign", over(alpha, map[string]any{"container": "loose", "add": []any{"web"}})); !strings.Contains(msg, "the tags of loose on alpha could not be read") {
			t.Errorf("a container's tags that could not be read = %q", msg)
		}
		dock.Fail(webTags, http.StatusInternalServerError, "Failed")
		if msg := suite.CallErr(t, "tag_assign", over(alpha, map[string]any{"stack": "web", "add": []any{"web"}})); !strings.Contains(msg, "the tags of web on alpha could not be read") {
			t.Errorf("a stack's tags that could not be read = %q", msg)
		}
		dock.FailWhen("GET /api/containers", "env", "1", http.StatusInternalServerError, "Failed to list containers")
		if msg := suite.CallErr(t, "tag_assign", over(alpha, map[string]any{"container": "loose", "add": []any{"web"}})); !strings.Contains(msg, "the containers of alpha could not be listed") {
			t.Errorf("containers that could not be listed = %q", msg)
		}
		dock.FailWhen("GET /api/stacks", "env", "1", http.StatusInternalServerError, "Failed to list stacks")
		if msg := suite.CallErr(t, "tag_assign", over(alpha, map[string]any{"stack": "web", "add": []any{"web"}})); !strings.Contains(msg, "the stacks of alpha could not be listed") {
			t.Errorf("stacks that could not be listed = %q", msg)
		}
		dock.Fail(tagsCall, http.StatusForbidden, notAllowed)
		if msg := suite.CallErr(t, "tag_assign", over(alpha, map[string]any{"container": "loose", "add": []any{"web"}})); !strings.Contains(msg, "the tags could not be listed") {
			t.Errorf("tags the account may not see = %q", msg)
		}
	})

	t.Run("a container's tags set whole", func(t *testing.T) {
		t.Parallel()

		dock := withTags(t)
		suite := session(t, dock, everything)
		dock.Answer(looseSet, `{"tagIds":[3,7]}`)
		dock.Then(looseSet, func(r dockhandtest.Request) { dock.Answer(looseTags, strings.Replace(r.Body, "[3,7]", "[7,3]", 1)) })

		// each named once however often it is given, by name or number
		out := suite.Call(t, "tag_assign", over(alpha, map[string]any{"container": " loose ", "tags": []any{"WEB", 7, 3, "spare"}}))
		last := dock.Last(looseSet)
		if last.Body != `{"tagIds":[3,7]}` || last.Query.Get("env") != "1" || strings.Join(acctest.Strs(t, out["tags"], "tags"), " ") != "spare web" || strings.Join(acctest.Strs(t, out["changes"], "changes"), "; ") != "gave it spare, web; took Critical from it" || out["container"] != "loose" || out["stack"] != nil || out["env"] != "alpha" || out["note"] != nil {
			t.Errorf("a container's tags set = %v, sent %s %v", out, last.Body, last.Query)
		}

		// none at all is sent as an empty list, not left out
		dock.Answer(looseSet, `{"tagIds":[]}`)
		out = suite.Call(t, "tag_assign", over(alpha, map[string]any{"container": "loose", "tags": []any{}}))
		if body := dock.Last(looseSet).Body; body != `{"tagIds":[]}` || len(acctest.Strs(t, out["tags"], "tags")) != 0 || acctest.Strs(t, out["changes"], "changes")[0] != "took spare, web from it" {
			t.Errorf("every tag taken = %v, sent %s", out, body)
		}
		if out = suite.Call(t, "tag_assign", over(alpha, map[string]any{"container": "loose", "tags": []any{}})); !strings.Contains(acctest.Str(out["note"]), "nothing to do") || len(dock.Requests(looseSet)) != 2 {
			t.Errorf("no tags for what has none = %v", out)
		}
	})

	t.Run("some given and some taken", func(t *testing.T) {
		t.Parallel()

		dock := withTags(t)
		suite := session(t, dock, everything)

		// a dry run says what would be and what it has now
		out := suite.Call(t, "tag_assign", over(alpha, map[string]any{"stack": "web", "add": []any{"critical"}, "remove": []any{3, "spare"}, "dry_run": true}))
		if strings.Join(acctest.Strs(t, out["changes"], "changes"), "; ") != "would give it Critical; would take web from it" || strings.Join(acctest.Strs(t, out["tags"], "tags"), " ") != "web" || !acctest.IsBool(out["dry_run"], true) || len(dock.Requests(webSet)) != 0 {
			t.Errorf("a dry run = %v", out)
		}

		dock.Answer(webSet, `{"tagIds":[1]}`)
		dock.Then(webSet, func(r dockhandtest.Request) { dock.Answer(webTags, r.Body) })
		out = suite.Call(t, "tag_assign", over(alpha, map[string]any{"stack": "web", "add": []any{"critical"}, "remove": []any{3, "spare"}}))
		last := dock.Last(webSet)
		if last.Body != `{"tagIds":[1]}` || last.Query.Get("env") != "1" || strings.Join(acctest.Strs(t, out["tags"], "tags"), " ") != "Critical" || out["stack"] != "web" || out["container"] != nil || strings.Join(acctest.Strs(t, out["changes"], "changes"), "; ") != "gave it Critical; took web from it" {
			t.Errorf("a stack's tags changed = %v, sent %s", out, last.Body)
		}

		// what it has already is nothing to do
		if out = suite.Call(t, "tag_assign", over(alpha, map[string]any{"container": "loose", "add": []any{"Critical"}, "remove": []any{"web"}})); !strings.Contains(acctest.Str(out["note"]), "nothing to do: it has exactly those already") || len(dock.Requests(looseSet)) != 0 {
			t.Errorf("a tag it has already = %v", out)
		}
	})

	t.Run("a name nothing has any more", func(t *testing.T) {
		t.Parallel()

		dock := withTags(t)
		suite := session(t, dock, everything)

		// what it holds can be taken from it, and is said to be of nothing
		out := suite.Call(t, "tag_assign", over(alpha, map[string]any{"container": "gone", "remove": []any{"spare"}}))
		if note := acctest.Str(out["note"]); !strings.Contains(note, "no container is named gone on alpha now: the name holds tags from when one was; nothing to do") {
			t.Errorf("nothing to take from a name that is gone = %v", out)
		}
		dock.Answer(goneSet, `{"tagIds":[]}`)
		dock.Then(goneSet, func(r dockhandtest.Request) { dock.Answer(goneTags, r.Body) })
		out = suite.Call(t, "tag_assign", over(alpha, map[string]any{"container": "gone", "tags": []any{}}))
		if dock.Last(goneSet).Body != `{"tagIds":[]}` || acctest.Strs(t, out["changes"], "changes")[0] != "took web from it" || out["note"] != "no container is named gone on alpha now: the name holds tags from when one was" {
			t.Errorf("the tags of a name that is gone taken = %v", out)
		}

		// a tag Dockhand names that it does not list is given by its number
		dock.Answer(looseTags, `{"tagIds":[99,1]}`)
		if out = suite.Call(t, "tag_assign", over(alpha, map[string]any{"container": "loose", "add": []any{"web"}, "dry_run": true})); strings.Join(acctest.Strs(t, out["tags"], "tags"), " ") != "99 Critical" {
			t.Errorf("a tag that is not listed = %v", out)
		}
	})

	t.Run("not set, or not as asked", func(t *testing.T) {
		t.Parallel()

		dock := withTags(t)
		suite := session(t, dock, everything)
		container, stack := over(alpha, map[string]any{"container": "loose", "add": []any{"web"}}), over(alpha, map[string]any{"stack": "web", "add": []any{"spare"}})

		// refused, and what it had is still there
		dock.Fail(looseSet, http.StatusInternalServerError, "Failed")
		if msg := suite.CallErr(t, "tag_assign", container); !strings.Contains(msg, "the tags of loose on alpha were not set, and it has the ones it had") {
			t.Errorf("a container's tags Dockhand refuses = %q", msg)
		}
		dock.Fail(webSet, http.StatusInternalServerError, "Failed")
		if msg := suite.CallErr(t, "tag_assign", stack); !strings.Contains(msg, "the tags of web on alpha were not set, and it has the ones it had") {
			t.Errorf("a stack's tags Dockhand refuses = %q", msg)
		}

		// answered as set, and read back as something else
		dock.Answer(looseSet, `{"tagIds":[1,3]}`)
		if msg := suite.CallErr(t, "tag_assign", container); !strings.Contains(msg, "were answered as set, and what it has is not what was asked: it has Critical") {
			t.Errorf("a container's tags not as asked = %q", msg)
		}
		dock.Answer(webSet, `{"tagIds":[3,7]}`)
		dock.Then(webSet, func(dockhandtest.Request) { dock.Answer(webTags, `{"tagIds":[]}`) })
		if msg := suite.CallErr(t, "tag_assign", stack); !strings.Contains(msg, "what it has is not what was asked: it has none") {
			t.Errorf("a stack's tags not as asked = %q", msg)
		}

		// set, and not read back: what the change was answered with is given
		dock.Then(looseSet, func(dockhandtest.Request) { dock.Fail(looseTags, http.StatusInternalServerError, "Failed") })
		out := suite.Call(t, "tag_assign", container)
		if !strings.Contains(acctest.Str(out["note"]), "they were set, and could not be read back") || strings.Join(acctest.Strs(t, out["tags"], "tags"), " ") != "Critical web" {
			t.Errorf("a container's tags not read back = %v", out)
		}
		dock.Answer(webTags, `{"tagIds":[3]}`)
		dock.Then(webSet, func(dockhandtest.Request) { dock.Fail(webTags, http.StatusInternalServerError, "Failed") })
		if out = suite.Call(t, "tag_assign", stack); !strings.Contains(acctest.Str(out["note"]), "they were set, and could not be read back") || strings.Join(acctest.Strs(t, out["tags"], "tags"), " ") != "spare web" {
			t.Errorf("a stack's tags not read back = %v", out)
		}
	})
}

// Dockhand takes the tags a name has before it gives it the ones asked for, so a change it refuses leaves the name with none: what it had is put
// back.
func TestTagAssignPutsBackWhatARefusedChangeTook(t *testing.T) {
	t.Parallel()

	args := map[string]any{"env": "alpha", "container": "loose", "add": []any{"web"}}
	const refusedWith, had = "FOREIGN KEY constraint failed", `{"tagIds":[1]}`

	t.Run("put back", func(t *testing.T) {
		t.Parallel()

		dock := withTags(t)
		suite := session(t, dock, everything)
		dock.Fail(looseSet, http.StatusInternalServerError, refusedWith)
		dock.Then(looseSet, func(r dockhandtest.Request) {
			if r.Body == had {
				dock.Answer(looseSet, had)

				return
			}
			dock.Answer(looseTags, `{"tagIds":[]}`)
		})
		msg := suite.CallErr(t, "tag_assign", args)
		calls := dock.Requests(looseSet)
		if !strings.Contains(msg, "the tags of loose on alpha were not set") || !strings.Contains(msg, refusedWith) || !strings.Contains(msg, "a change it refuses leaves the name with none, so the ones it had were put back: it has Critical again") || len(calls) != 2 || calls[0].Body != `{"tagIds":[1,3]}` || calls[1].Body != had {
			t.Errorf("a refused change that took what it had = %q, sent %v", msg, calls)
		}
	})

	t.Run("not put back", func(t *testing.T) {
		t.Parallel()

		dock := withTags(t)
		suite := session(t, dock, everything)
		dock.Fail(looseSet, http.StatusInternalServerError, refusedWith)
		dock.Then(looseSet, func(dockhandtest.Request) { dock.Answer(looseTags, `{"tagIds":[]}`) })
		if msg := suite.CallErr(t, "tag_assign", args); !strings.Contains(msg, "it had Critical and has none now, and they could not be put back") || len(dock.Requests(looseSet)) != 2 {
			t.Errorf("what it had not put back = %q", msg)
		}
	})

	t.Run("not read", func(t *testing.T) {
		t.Parallel()

		dock := withTags(t)
		suite := session(t, dock, everything)
		dock.Fail(looseSet, http.StatusInternalServerError, refusedWith)
		dock.Then(looseSet, func(dockhandtest.Request) { dock.Fail(looseTags, http.StatusInternalServerError, "Failed") })
		if msg := suite.CallErr(t, "tag_assign", args); !strings.Contains(msg, "a change it refuses leaves the name with none, and what it has now could not be read") || len(dock.Requests(looseSet)) != 1 {
			t.Errorf("what a refused change left, not read = %q", msg)
		}
	})
}

func TestSeveral(t *testing.T) {
	t.Parallel()

	if one, more, none := several(1, "stack"), several(3, "container"), several(0, "stack"); one != "1 stack" || more != "3 containers" || none != "0 stacks" {
		t.Errorf("several = %q, %q, %q", one, more, none)
	}
}
