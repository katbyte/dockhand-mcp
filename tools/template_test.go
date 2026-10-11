package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// What a 1.0.51 server answers about app templates, with invented ones: a container template with a volume it names no place for and a variable it
// leaves to be filled in, a stack template kept in a repository, and a second template with the first one's title from another source.
const (
	webTemplate   = `{"id":"aaaaaa","type":"container","title":"Web","description":"A small web server.","logo":"","categories":["Web","Tools"],"source":"own","note":"Fill in the key.","projectUrl":"https://example.com/web","image":"registry.example/web:1","ports":["8080:80/tcp"],"volumes":[{"container":"/data"},{"container":"/etc/web","bind":"/srv/web"}],"env":[{"name":"MODE","label":"Mode","default":"demo"},{"name":"API_KEY","label":"Key","default":""}],"restartPolicy":"unless-stopped","network":"host"}`
	shopTemplate  = `{"id":"bbbbbb","type":"stack","title":"Shop","description":"A shop, its database and its cache, kept as one compose file.","logo":"","categories":["Web"],"source":"catalogue","projectUrl":"https://example.com/shop","detailsUrl":"https://example.com/shop/details","repository":{"url":"https://example.com/shop","stackfile":"deploy/compose.yaml"}}`
	twinTemplate  = `{"id":"cccccc","type":"container","title":"web","description":"Another web server.","logo":"","categories":["Other"],"source":"catalogue","image":"registry.example/other:2","ports":[],"volumes":[],"env":[],"restartPolicy":"always"}`
	webCompose    = "services:\n  web:\n    image: registry.example/web:1\n    volumes:\n      - undefined:/data\n      - /srv/web:/etc/web\n"
	ownSource     = `{"id":10,"sourceId":"custom-1","name":"own","url":"http://catalogue.example/templates.json","enabled":true,"builtin":false,"sortOrder":9}`
	builtinSource = `{"id":1,"sourceId":"catalogue","name":"catalogue","url":"https://example.com/templates.json","enabled":true,"builtin":true,"sortOrder":0}`
	spareSource   = `{"id":2,"sourceId":"spare","name":"spare","url":"https://example.com/spare.json","enabled":false,"builtin":true,"sortOrder":1}`
	newSource     = `{"id":11,"sourceId":"custom-2","name":"another","url":"https://example.com/another.json","enabled":true,"builtin":false,"sortOrder":10}`

	templatesCall = "GET /api/templates"
	composeCall   = "POST /api/templates/compose"
	sourcesCall   = "GET /api/templates/sources"
	sourceMake    = "POST /api/templates/sources"
	sourceChange  = "PUT /api/templates/sources"
	sourceRemove  = "DELETE /api/templates/sources"
)

// withTemplates is a Dockhand with three templates from two sources, and a third source switched off.
func withTemplates(t *testing.T) *dockhandtest.Server {
	t.Helper()

	dock := dockhandtest.New(t)
	dock.Answer(templatesCall, `[`+shopTemplate+`,`+webTemplate+`,`+twinTemplate+`]`)
	dock.Answer(sourcesCall, `[`+builtinSource+`,`+spareSource+`,`+ownSource+`]`)

	return dock
}

func TestTemplateList(t *testing.T) {
	t.Parallel()

	dock := withTemplates(t)
	suite := session(t, dock, Options{})

	out := suite.Call(t, "template_list", nil)
	rows := acctest.Rows(t, out["templates"], "templates")
	if acctest.Num(t, out["total"], "total") != 3 || acctest.Num(t, out["of"], "of") != 3 || out["note"] != nil || rows[0]["title"] != "Shop" || rows[0]["type"] != "stack" || rows[0]["image"] != nil || rows[1]["id"] != "cccccc" || rows[2]["id"] != "aaaaaa" || rows[2]["image"] != "registry.example/web:1" || rows[2]["source"] != "own" || len(acctest.Strs(t, rows[2]["categories"], "categories")) != 2 {
		t.Fatalf("the templates, by title = %v", out)
	}

	for what, tc := range map[string]struct {
		args map[string]any
		want []string
	}{
		"every word, whatever its case, in the title, description or categories": {map[string]any{"search": "SMALL web"}, []string{"aaaaaa"}},
		"a word that is a category":      {map[string]any{"search": "tools"}, []string{"aaaaaa"}},
		"a category, whole":              {map[string]any{"category": "WEB"}, []string{"bbbbbb", "aaaaaa"}},
		"one kind":                       {map[string]any{"type": " Container "}, []string{"cccccc", "aaaaaa"}},
		"a part of a source's name":      {map[string]any{"source": "CATA"}, []string{"bbbbbb", "cccccc"}},
		"a kind and a source together":   {map[string]any{"type": "container", "source": "own"}, []string{"aaaaaa"}},
		"the second and no more":         {map[string]any{"limit": 1, "offset": 1}, []string{"cccccc"}},
		"passing over more than matched": {map[string]any{"offset": 9}, []string{}},
	} {
		got := acctest.Rows(t, suite.Call(t, "template_list", tc.args)["templates"], "templates")
		ids := make([]string, 0, len(got))
		for _, row := range got {
			ids = append(ids, acctest.Str(row["id"]))
		}
		if strings.Join(ids, " ") != strings.Join(tc.want, " ") {
			t.Errorf("%s = %v, want %v", what, ids, tc.want)
		}
	}
	// more than are given, and none that match, are each said
	if out = suite.Call(t, "template_list", map[string]any{"limit": 2}); acctest.Num(t, out["total"], "total") != 3 || !strings.Contains(acctest.Str(out["note"]), "3 templates matched and 2 are given: offset 2 has the next") {
		t.Errorf("the first two of three = %v", out)
	}
	if out = suite.Call(t, "template_list", map[string]any{"search": "no-such-thing"}); acctest.Num(t, out["total"], "total") != 0 || !strings.Contains(acctest.Str(out["note"]), "none of the 3 templates matched") {
		t.Errorf("a search that finds nothing = %v", out)
	}
	for what, args := range map[string]map[string]any{"a kind there is none of": {"type": "image"}, "more than the most": {"limit": 201}, "a limit below nothing": {"limit": -1}, "an offset below nothing": {"offset": -1}} {
		if msg := suite.CallErr(t, "template_list", args); !strings.Contains(msg, "it is container or stack") && !strings.Contains(msg, "limit is how many templates to give at most") {
			t.Errorf("%s = %q", what, msg)
		}
	}

	// a long description is cut, and no templates at all is said with why that may be
	dock.Answer(templatesCall, `[`+strings.Replace(webTemplate, "A small web server.", strings.Repeat("é", 300), 1)+`]`)
	if about := acctest.Str(acctest.Rows(t, suite.Call(t, "template_list", nil)["templates"], "templates")[0]["description"]); len([]rune(about)) != 201 || !strings.HasSuffix(about, "…") {
		t.Errorf("a long description is given as %d characters", len([]rune(about)))
	}
	dock.Answer(templatesCall, `[]`)
	if out = suite.Call(t, "template_list", nil); acctest.Num(t, out["of"], "of") != 0 || !strings.Contains(acctest.Str(out["note"]), "Dockhand has no templates") {
		t.Errorf("a Dockhand with no templates = %v", out)
	}
	dock.Fail(templatesCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "template_list", nil); !strings.Contains(msg, "the templates could not be listed") {
		t.Errorf("templates the account may not see = %q", msg)
	}
}

func TestTemplateGet(t *testing.T) {
	t.Parallel()

	dock := withTemplates(t)
	dock.Answer(composeCall, `{"compose":"services:\n  web:\n    image: registry.example/web:1\n    volumes:\n      - undefined:/data\n      - /srv/web:/etc/web\n"}`)
	suite := session(t, dock, Options{})

	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"no template":                         {map[string]any{"template": " "}, "template is required"},
		"a template there is none of":         {map[string]any{"template": "nothing"}, `no template has the id or the title "nothing"`},
		"a title two templates have":          {map[string]any{"template": "WEB"}, `2 templates are titled "WEB": give the id of one, aaaaaa (container, from own), cccccc (container, from catalogue)`},
		"a title and a source it is not from": {map[string]any{"template": "Shop", "source": "own"}, "no template has the id or the title"},
	} {
		if msg := suite.CallErr(t, "template_get", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	if len(dock.Requests(composeCall)) != 0 {
		t.Fatal("a compose file was asked for by a call that was refused")
	}

	// a container template, by its id and by its title with the source that tells it from its twin: all of it, and the compose file Dockhand writes
	// from what it is sent back
	for _, args := range []map[string]any{{"template": "aaaaaa"}, {"template": "web", "source": "OWN"}} {
		out := suite.Call(t, "template_get", args)
		given := acctest.Object(t, sent(t, dock.Last(composeCall))["template"], "template")
		if given["id"] != "aaaaaa" || given["type"] != "container" || given["title"] != "Web" || given["image"] != "registry.example/web:1" || given["restartPolicy"] != "unless-stopped" || given["network"] != "host" || len(acctest.Strs(t, given["ports"], "ports")) != 1 || len(acctest.Rows(t, given["volumes"], "volumes")) != 2 || acctest.Rows(t, given["volumes"], "volumes")[1]["bind"] != "/srv/web" || len(acctest.Rows(t, given["env"], "env")) != 2 || given["repository"] != nil {
			t.Errorf("the template sent for its compose file by %v = %v", args, given)
		}
		volumes, variables := acctest.Rows(t, out["volumes"], "volumes"), acctest.Rows(t, out["variables"], "variables")
		if out["id"] != "aaaaaa" || out["source"] != "own" || out["compose"] != webCompose || out["restart_policy"] != "unless-stopped" || out["network"] != "host" || out["about"] != "Fill in the key." || out["project_url"] != "https://example.com/web" || volumes[0]["host"] != nil || volumes[1]["host"] != "/srv/web" || variables[0]["default"] != "demo" || variables[1]["name"] != "API_KEY" || variables[1]["default"] != nil || out["repository"] != nil {
			t.Errorf("a container template by %v = %v", args, out)
		}
		// a volume Dockhand wrote no place for is said, since it is no volume
		if !strings.Contains(acctest.Str(out["note"]), `wrote "undefined" as where 1 of its volumes`) {
			t.Errorf("the note of a compose file with a volume of no place = %q", out["note"])
		}
	}

	// a stack template: where its compose file is kept, which is what Dockhand is sent to fetch it by
	dock.Answer(composeCall, `{"compose":"services:\n  shop:\n    image: registry.example/shop:3\n"}`)
	out := suite.Call(t, "template_get", map[string]any{"template": "shop"})
	given := acctest.Object(t, sent(t, dock.Last(composeCall))["template"], "template")
	if repository := acctest.Object(t, given["repository"], "repository"); repository["url"] != "https://example.com/shop" || repository["stackfile"] != "deploy/compose.yaml" || given["image"] != nil || out["repository"] != "https://example.com/shop" || out["file"] != "deploy/compose.yaml" || out["note"] != nil || !strings.Contains(acctest.Str(out["compose"]), "registry.example/shop:3") || out["ports"] != nil {
		t.Errorf("a stack template = %v, sent %v", out, given)
	}

	// a repository that cannot be reached is said to be why, and a container template's failure is just that
	dock.Fail(composeCall, http.StatusInternalServerError, "fetch failed")
	if msg := suite.CallErr(t, "template_get", map[string]any{"template": "shop"}); !strings.Contains(msg, "Dockhand fetches a stack template's from its repository, https://example.com/shop, and could not") || !strings.Contains(msg, "fetch failed") {
		t.Errorf("a stack template whose repository is not reached = %q", msg)
	}
	if msg := suite.CallErr(t, "template_get", map[string]any{"template": "aaaaaa"}); !strings.Contains(msg, "the compose file of Web could not be had") || strings.Contains(msg, "repository") {
		t.Errorf("a container template whose compose file fails = %q", msg)
	}
	dock.Fail(templatesCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "template_get", map[string]any{"template": "aaaaaa"}); !strings.Contains(msg, "the templates could not be listed") {
		t.Errorf("a template when they cannot be listed = %q", msg)
	}
}

func TestTemplateSourceList(t *testing.T) {
	t.Parallel()

	dock := withTemplates(t)
	suite := session(t, dock, Options{})

	out := suite.Call(t, "template_source_list", nil)
	rows := acctest.Rows(t, out["sources"], "sources")
	if acctest.Num(t, out["total"], "total") != 3 || out["note"] != nil || rows[0]["name"] != "catalogue" || !acctest.IsBool(rows[0]["builtin"], true) || !acctest.IsBool(rows[0]["enabled"], true) || acctest.Num(t, rows[0]["templates"], "templates") != 2 || !acctest.IsBool(rows[1]["enabled"], false) || acctest.Num(t, rows[1]["templates"], "templates") != 0 || rows[2]["name"] != "own" || rows[2]["builtin"] != nil || rows[2]["url"] != "http://catalogue.example/templates.json" || acctest.Num(t, rows[2]["templates"], "templates") != 1 {
		t.Fatalf("the template sources = %v", out)
	}

	dock.Fail(templatesCall, http.StatusInternalServerError, "fetch failed")
	if out = suite.Call(t, "template_source_list", nil); acctest.Num(t, out["total"], "total") != 3 || !strings.Contains(acctest.Str(out["note"]), "how many templates each has could not be counted") {
		t.Errorf("the sources when the templates cannot be listed = %v", out)
	}
	dock.Fail(sourcesCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "template_source_list", nil); !strings.Contains(msg, "the template sources could not be listed") {
		t.Errorf("sources the account may not see = %q", msg)
	}
}

func TestTemplateSourceCreate(t *testing.T) {
	t.Parallel()

	dock := withTemplates(t)
	dock.Answer(sourceMake, newSource)
	suite := session(t, dock, everything)
	another := map[string]any{"name": " another ", "url": " https://example.com/another.json "}

	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"a name of spaces":          {map[string]any{"name": " ", "url": "https://example.com/another.json"}, "name is required"},
		"an address with no scheme": {map[string]any{"name": "another", "url": "example.com/another.json"}, "with its scheme"},
		"a name in use":             {map[string]any{"name": "OWN", "url": "https://example.com/another.json"}, "the template source own already has that name or that address"},
		"an address in use":         {map[string]any{"name": "another", "url": "https://example.com/templates.json"}, "the template source catalogue already has that name or that address"},
	} {
		if msg := suite.CallErr(t, "template_source_create", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	if out := suite.Call(t, "template_source_create", over(another, map[string]any{"dry_run": true})); !acctest.IsBool(out["dry_run"], true) || out["source"] != nil || !strings.Contains(flat(out), "would add the template source another, fetched from https://example.com/another.json") {
		t.Errorf("a dry run of adding a source = %v", out)
	}
	if len(dock.Requests(sourceMake)) != 0 {
		t.Fatal("a source was added by a call that was refused or was a dry run")
	}

	// added, with what came of it counted: none is said, since Dockhand says nothing of a source it could not read
	out := suite.Call(t, "template_source_create", another)
	row := acctest.Object(t, out["source"], "source")
	if body := sent(t, dock.Last(sourceMake)); body["name"] != "another" || body["url"] != "https://example.com/another.json" || len(body) != 2 || acctest.Num(t, row["id"], "id") != 11 || !acctest.IsBool(row["enabled"], true) || acctest.Num(t, row["templates"], "templates") != 0 || !strings.Contains(acctest.Str(out["note"]), "no template comes of it") {
		t.Errorf("a source that gave no templates = %v, sent %v", out, body)
	}
	fresh := strings.Replace(strings.Replace(webTemplate, `"id":"aaaaaa"`, `"id":"dddddd"`, 1), `"source":"own"`, `"source":"another"`, 1)
	dock.Then(sourceMake, func(dockhandtest.Request) {
		dock.Answer(templatesCall, `[`+shopTemplate+`,`+webTemplate+`,`+twinTemplate+`,`+fresh+`]`)
	})
	if out = suite.Call(t, "template_source_create", another); acctest.Num(t, acctest.Object(t, out["source"], "source")["templates"], "templates") != 1 || out["note"] != nil {
		t.Errorf("a source that gave a template = %v", out)
	}
	// an address Dockhand fetched within the hour under another name: its templates come, under that name, which is said rather than that none came
	dock.Answer(templatesCall, `[`+shopTemplate+`,`+webTemplate+`,`+twinTemplate+`]`)
	stale := strings.Replace(fresh, `"source":"another"`, `"source":"as it was called"`, 1)
	dock.Then(sourceMake, func(dockhandtest.Request) {
		dock.Answer(templatesCall, `[`+shopTemplate+`,`+webTemplate+`,`+twinTemplate+`,`+stale+`,`+strings.Replace(stale, "dddddd", "eeeeee", 1)+`]`)
	})
	if out = suite.Call(t, "template_source_create", another); acctest.Num(t, acctest.Object(t, out["source"], "source")["templates"], "templates") != 0 || !strings.Contains(acctest.Str(out["note"]), "2 templates came of it, and are listed under as it was called") {
		t.Errorf("a source whose address was fetched under another name = %v", out)
	}
	dock.Answer(templatesCall, `[`+shopTemplate+`,`+webTemplate+`,`+twinTemplate+`]`)

	// added and not counted, and not added at all
	dock.Then(sourceMake, func(dockhandtest.Request) { dock.Fail(templatesCall, http.StatusInternalServerError, "fetch failed") })
	if out = suite.Call(t, "template_source_create", another); out["source"] == nil || !strings.Contains(acctest.Str(out["note"]), "it was added, and its templates could not be counted") {
		t.Errorf("a source added and not counted = %v", out)
	}
	dock.Then(sourceMake, nil)
	dock.Fail(sourceMake, http.StatusBadRequest, "Invalid source URL: not a valid URL")
	if msg := suite.CallErr(t, "template_source_create", another); !strings.Contains(msg, "another was not added") || !strings.Contains(msg, "Invalid source URL") {
		t.Errorf("a source Dockhand refuses = %q", msg)
	}
	dock.Fail(sourcesCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "template_source_create", another); !strings.Contains(msg, "the template sources could not be listed") {
		t.Errorf("adding a source when they cannot be listed = %q", msg)
	}
}

func TestTemplateSourceEdit(t *testing.T) {
	t.Parallel()

	dock := withTemplates(t)
	dock.Answer(sourceChange, `{"ok":true}`)
	suite := session(t, dock, everything)

	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"nothing to change":         {map[string]any{"source": "own"}, "nothing to change was given"},
		"no source":                 {map[string]any{"enabled": false}, "source is required"},
		"a source there is none of": {map[string]any{"source": "nowhere", "enabled": false}, `no template source is named or numbered "nowhere": Dockhand keeps catalogue, spare, own`},
		"a number that is none":     {map[string]any{"source": 1.5, "enabled": false}, "its number is a whole one"},
		"an address with no scheme": {map[string]any{"source": "own", "url": "catalogue.example"}, "with its scheme"},
		"another's name":            {map[string]any{"source": "own", "name": "SPARE"}, `another template source is named "SPARE"`},
	} {
		if msg := suite.CallErr(t, "template_source_edit", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	// what is so already, and a dry run: nothing is sent
	if out := suite.Call(t, "template_source_edit", map[string]any{"source": 10, "name": "own", "url": "http://catalogue.example/templates.json", "enabled": true}); !strings.Contains(acctest.Str(out["note"]), "that way already") || acctest.Num(t, acctest.Object(t, out["source"], "source")["templates"], "templates") != 1 {
		t.Errorf("a change to what is so already = %v", out)
	}
	if out := suite.Call(t, "template_source_edit", map[string]any{"source": "own", "name": "mine", "url": "https://example.com/mine.json", "enabled": false, "dry_run": true}); !acctest.IsBool(out["dry_run"], true) || len(acctest.Strs(t, out["changes"], "changes")) != 3 || !strings.Contains(flat(out), "would rename it from own to mine") || !strings.Contains(flat(out), "would switch it off") || acctest.Object(t, out["source"], "source")["name"] != "own" {
		t.Errorf("a dry run of changing a source = %v", out)
	}
	if len(dock.Requests(sourceChange)) != 0 {
		t.Fatal("a source was changed by a call that was refused, changed nothing or was a dry run")
	}

	// switched off: only what changes is sent with its number, and it is read back with none of its templates listed
	off := strings.Replace(ownSource, `"enabled":true`, `"enabled":false`, 1)
	dock.Then(sourceChange, func(dockhandtest.Request) {
		dock.Answer(sourcesCall, `[`+builtinSource+`,`+spareSource+`,`+off+`]`)
		dock.Answer(templatesCall, `[`+shopTemplate+`,`+twinTemplate+`]`)
	})
	out := suite.Call(t, "template_source_edit", map[string]any{"source": "OWN", "enabled": false})
	if body := sent(t, dock.Last(sourceChange)); body["id"] != float64(10) || !acctest.IsBool(body["enabled"], false) || len(body) != 2 || !acctest.IsBool(acctest.Object(t, out["source"], "source")["enabled"], false) || acctest.Num(t, acctest.Object(t, out["source"], "source")["templates"], "templates") != 0 || out["note"] != nil {
		t.Errorf("a source switched off = %v, sent %v", out, body)
	}
	// renamed and moved while switched on: its templates stay under the old name until it is read again, which is said
	dock.Answer(sourcesCall, `[`+builtinSource+`,`+spareSource+`,`+ownSource+`]`)
	dock.Answer(templatesCall, `[`+shopTemplate+`,`+webTemplate+`,`+twinTemplate+`]`)
	renamed := strings.Replace(strings.Replace(ownSource, `"name":"own"`, `"name":"mine"`, 1), "http://catalogue.example/templates.json", "https://example.com/mine.json", 1)
	dock.Then(sourceChange, func(dockhandtest.Request) {
		dock.Answer(sourcesCall, `[`+builtinSource+`,`+spareSource+`,`+renamed+`]`)
	})
	out = suite.Call(t, "template_source_edit", map[string]any{"source": "own", "name": " mine ", "url": "https://example.com/mine.json"})
	if body := sent(t, dock.Last(sourceChange)); body["name"] != "mine" || body["url"] != "https://example.com/mine.json" || body["enabled"] != nil || len(body) != 3 || acctest.Object(t, out["source"], "source")["name"] != "mine" || !strings.Contains(acctest.Str(out["note"]), "listed under its old name until Dockhand reads it again") {
		t.Errorf("a source renamed = %v, sent %v", out, body)
	}
}

func TestTemplateSourceEditWhenItGoesWrong(t *testing.T) {
	t.Parallel()

	dock := withTemplates(t)
	dock.Answer(sourceChange, `{"ok":false}`)
	suite := session(t, dock, everything)
	off := map[string]any{"source": "own", "enabled": false}

	// answered without saying it was done; changed and not read back; changed and gone; and refused
	if msg := suite.CallErr(t, "template_source_edit", off); !strings.Contains(msg, "the change to own was answered without its being said to have been made") {
		t.Errorf("a change not said to be made = %q", msg)
	}
	dock.Answer(sourceChange, `{"ok":true}`)
	dock.Then(sourceChange, func(dockhandtest.Request) {
		dock.Fail(sourcesCall, http.StatusInternalServerError, "database is locked")
	})
	if out := suite.Call(t, "template_source_edit", off); out["source"] != nil || !strings.Contains(acctest.Str(out["note"]), "it was changed, and could not be read back") {
		t.Errorf("a change not read back = %v", out)
	}
	dock.Answer(sourcesCall, `[`+builtinSource+`,`+ownSource+`]`)
	dock.Then(sourceChange, func(dockhandtest.Request) { dock.Answer(sourcesCall, `[`+builtinSource+`]`) })
	if msg := suite.CallErr(t, "template_source_edit", off); !strings.Contains(msg, "own was changed, and is no longer listed") {
		t.Errorf("a source gone once changed = %q", msg)
	}
	dock.Answer(sourcesCall, `[`+builtinSource+`,`+ownSource+`]`)
	dock.Then(sourceChange, nil)
	dock.Fail(sourceChange, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "template_source_edit", off); !strings.Contains(msg, "own was not changed") {
		t.Errorf("a change Dockhand refuses = %q", msg)
	}

	// what is so already is still answered when its templates cannot be counted; two by one name are told apart by number
	dock.Fail(templatesCall, http.StatusInternalServerError, "fetch failed")
	if out := suite.Call(t, "template_source_edit", map[string]any{"source": "own", "enabled": true}); !strings.Contains(acctest.Str(out["note"]), "that way already") || !strings.Contains(acctest.Str(out["note"]), "its templates could not be counted") {
		t.Errorf("a source as it is, its templates uncounted = %v", out)
	}
	dock.Answer(sourcesCall, `[`+ownSource+`,`+strings.Replace(newSource, `"name":"another"`, `"name":"Own"`, 1)+`]`)
	if msg := suite.CallErr(t, "template_source_edit", off); !strings.Contains(msg, `2 template sources are named "own": give the number of one, 10 or 11`) {
		t.Errorf("a name two sources share = %q", msg)
	}
	dock.Fail(sourcesCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "template_source_edit", off); !strings.Contains(msg, "the template sources could not be listed") {
		t.Errorf("a change when the sources cannot be listed = %q", msg)
	}
}

func TestTemplateSourceDelete(t *testing.T) {
	t.Parallel()

	dock := withTemplates(t)
	dock.Answer(sourceRemove, `{"ok":true}`)
	suite := session(t, dock, everything)

	// without confirm it says what would go, and of one of Dockhand's own that nothing brings it back as that
	out := suite.Call(t, "template_source_delete", map[string]any{"source": "own"})
	if !acctest.IsBool(out["removed"], false) || out["source"] != "own" || !strings.Contains(flat(out), "the template source own, fetched from http://catalogue.example/templates.json") || out["note"] != "nothing was removed: call again with confirm to remove it" || len(dock.Requests(sourceRemove)) != 0 {
		t.Fatalf("a removal not confirmed = %v", out)
	}
	if out = suite.Call(t, "template_source_delete", map[string]any{"source": 1}); !strings.Contains(acctest.Str(out["note"]), "one of the sources Dockhand comes with, and nothing brings it back as that") || !strings.Contains(acctest.Str(out["note"]), "https://example.com/templates.json") {
		t.Errorf("a removal of one of Dockhand's own, not confirmed = %v", out)
	}
	if msg := suite.CallErr(t, "template_source_delete", map[string]any{"source": 99, "confirm": true}); !strings.Contains(msg, `no template source is named or numbered "99"`) || len(dock.Requests(sourceRemove)) != 0 {
		t.Errorf("removing a source there is none of = %q", msg)
	}

	// answered without saying it was done, and answered as done with the source still listed
	dock.Answer(sourceRemove, `{"ok":false}`)
	if msg := suite.CallErr(t, "template_source_delete", map[string]any{"source": "own", "confirm": true}); !strings.Contains(msg, "without its being said to have been done") {
		t.Errorf("a removal not said to be done = %q", msg)
	}
	dock.Answer(sourceRemove, `{"ok":true}`)
	if msg := suite.CallErr(t, "template_source_delete", map[string]any{"source": "own", "confirm": true}); !strings.Contains(msg, "answered as done, and it is still listed") {
		t.Errorf("a removal that left the source = %q", msg)
	}
	// removed by its number, and read back as gone; one of Dockhand's own says once more what that means
	dock.Then(sourceRemove, func(dockhandtest.Request) { dock.Answer(sourcesCall, `[`+builtinSource+`,`+spareSource+`]`) })
	if out = suite.Call(t, "template_source_delete", map[string]any{"source": "own", "confirm": true}); !acctest.IsBool(out["removed"], true) || out["note"] != nil || dock.Last(sourceRemove).Query.Get("id") != "10" {
		t.Errorf("a source removed = %v, asked %v", out, dock.Last(sourceRemove).Query)
	}
	dock.Then(sourceRemove, func(dockhandtest.Request) { dock.Answer(sourcesCall, `[`+spareSource+`]`) })
	if out = suite.Call(t, "template_source_delete", map[string]any{"source": "catalogue", "confirm": true}); !acctest.IsBool(out["removed"], true) || !strings.Contains(acctest.Str(out["note"]), "nothing brings it back as that") {
		t.Errorf("one of Dockhand's own removed = %v", out)
	}

	// removed and not read back, and not removed at all
	dock.Then(sourceRemove, func(dockhandtest.Request) {
		dock.Fail(sourcesCall, http.StatusInternalServerError, "database is locked")
	})
	if out = suite.Call(t, "template_source_delete", map[string]any{"source": "spare", "confirm": true}); !acctest.IsBool(out["removed"], false) || !strings.Contains(acctest.Str(out["note"]), "it was removed, and the sources could not be listed afterwards") {
		t.Errorf("a removal not read back = %v", out)
	}
	dock.Answer(sourcesCall, `[`+spareSource+`]`)
	dock.Then(sourceRemove, nil)
	dock.Fail(sourceRemove, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "template_source_delete", map[string]any{"source": "spare", "confirm": true}); !strings.Contains(msg, "spare was not removed") {
		t.Errorf("a removal Dockhand refuses = %q", msg)
	}
	dock.Fail(sourcesCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "template_source_delete", map[string]any{"source": "spare"}); !strings.Contains(msg, "the template sources could not be listed") {
		t.Errorf("a removal when the sources cannot be listed = %q", msg)
	}
}
