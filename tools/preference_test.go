package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// What a 1.0.51 server answers about what its web interface remembers, with invented names: first with nothing saved, then with everything saved, an
// environment and a tag that were since removed still in the two orders.
const (
	noOrder        = `{"order":[]}`
	plainSidebar   = `{"preferences":{"order":[],"hidden":[]}}`
	noGrids        = `{"preferences":{}}`
	jobsShown      = `{"hideSystemJobs":false}`
	plainDashboard = `{"gridLayout":[],"locked":false,"viewMode":"grid"}`
	ownPages       = `{"effective":{"landingPage":"dashboard","envClickPage":"containers"},"global":{"landingPage":null,"envClickPage":null},"user":null}`

	savedEnvOrder  = `{"order":[2,9,1]}`
	savedTagOrder  = `{"order":[1,44,3]}`
	savedSidebar   = `{"preferences":{"order":["stacks","containers"],"hidden":["terminal"]}}`
	savedGrids     = `{"preferences":{"containers":{"columns":[{"id":"name","visible":true},{"id":"image","visible":false}]},"images":{"columns":[{"id":"tag","visible":true}]}}}`
	jobsHidden     = `{"success":true,"hideSystemJobs":true}`
	savedDashboard = `{"gridLayout":[{"id":2,"x":0,"y":0,"w":2,"h":2},{"id":9,"x":2,"y":0,"w":1,"h":2}],"locked":true,"viewMode":"list"}`
	chosenPages    = `{"effective":{"landingPage":"stacks","envClickPage":"logs"},"global":{"landingPage":"stacks","envClickPage":"logs"},"user":null}`
	pagesSet       = `{"success":true,"global":{"landingPage":"stacks","envClickPage":"logs"}}`

	twoFavorites = `{"favorites":["loose",{"name":"front","containers":["web-front-1","web-back-1"]}]}`
	noFavorites  = `{"favorites":[]}`

	envOrderCall  = "GET /api/preferences/environment-order"
	envOrderSave  = "POST /api/preferences/environment-order"
	tagOrderCall  = "GET /api/preferences/tag-order"
	tagOrderSave  = "POST /api/preferences/tag-order"
	sidebarCall   = "GET /api/preferences/sidebar"
	sidebarSave   = "POST /api/preferences/sidebar"
	gridCall      = "GET /api/preferences/grid"
	gridSave      = "POST /api/preferences/grid"
	gridForget    = "DELETE /api/preferences/grid"
	jobsCall      = "GET /api/schedules/settings"
	jobsSave      = "PUT /api/schedules/settings"
	dashboardCall = "GET /api/dashboard/preferences"
	dashboardSave = "POST /api/dashboard/preferences"
	pagesCall     = "GET /api/settings/navigation"
	pagesSave     = "PUT /api/settings/navigation"
	favoritesCall = "GET /api/preferences/favorites"
	favoritesSave = "POST /api/preferences/favorites"
)

// withNothingRemembered is a Dockhand managing alpha and beta, with three tags, whose web interface has nothing saved.
func withNothingRemembered(t *testing.T) *dockhandtest.Server {
	t.Helper()

	dock := twoHosts(t)
	dock.Answer(tagsCall, threeTags)
	dock.Answer(envOrderCall, noOrder)
	dock.Answer(tagOrderCall, noOrder)
	dock.Answer(sidebarCall, plainSidebar)
	dock.Answer(gridCall, noGrids)
	dock.Answer(jobsCall, jobsShown)
	dock.Answer(dashboardCall, plainDashboard)
	dock.Answer(pagesCall, ownPages)

	return dock
}

// remember has a canned Dockhand answer as one whose web interface has everything saved.
func remember(dock *dockhandtest.Server) {
	dock.Answer(envOrderCall, savedEnvOrder)
	dock.Answer(tagOrderCall, savedTagOrder)
	dock.Answer(sidebarCall, savedSidebar)
	dock.Answer(gridCall, savedGrids)
	dock.Answer(jobsCall, jobsHidden)
	dock.Answer(dashboardCall, savedDashboard)
	dock.Answer(pagesCall, chosenPages)
}

// writesOf is the calls a canned Dockhand was sent that change something.
func writesOf(dock *dockhandtest.Server) []string {
	var writes []string
	for _, call := range dock.Calls() {
		if !strings.HasPrefix(call, "GET ") {
			writes = append(writes, call)
		}
	}

	return writes
}

func TestPreferencesGet(t *testing.T) {
	t.Parallel()

	dock := withNothingRemembered(t)
	suite := session(t, dock, Options{})

	// nothing saved is said as nothing, and the two pages are Dockhand's own with none set
	out := suite.Call(t, "preferences_get", nil)
	if got := flat(out); got != `{"dashboard_locked":false,"dashboard_tiles":[],"dashboard_view":"grid","env_click_page":"containers","environment_order":[],"hide_system_jobs":false,"landing_page":"dashboard","sidebar_hidden":[],"sidebar_order":[],"tag_order":[]}` {
		t.Errorf("a web interface with nothing saved = %s", got)
	}

	// everything saved, the two orders by name, and what was since removed by its number
	remember(dock)
	out = suite.Call(t, "preferences_get", nil)
	if got := flat(out); got != `{"dashboard_locked":true,"dashboard_tiles":[{"env":"beta","h":2,"w":2,"x":0,"y":0},{"env":"#9","h":2,"w":1,"x":2,"y":0}],"dashboard_view":"list","env_click_page":"logs","env_click_page_set":"logs","environment_order":["beta","#9","alpha"],"grids":{"containers":[{"id":"name","visible":true},{"id":"image","visible":false}],"images":[{"id":"tag","visible":true}]},"hide_system_jobs":true,"landing_page":"stacks","landing_page_set":"stacks","sidebar_hidden":["terminal"],"sidebar_order":["stacks","containers"],"tag_order":["Critical","#44","web"]}` {
		t.Errorf("a web interface with everything saved = %s", got)
	}

	// an answer that leaves the sidebar and the pages out is nothing saved, not a failure
	dock.Answer(sidebarCall, `{}`)
	dock.Answer(pagesCall, `{}`)
	out = suite.Call(t, "preferences_get", nil)
	if len(acctest.Strs(t, out["sidebar_order"], "sidebar_order")) != 0 || out["landing_page"] != "" || out["landing_page_set"] != nil {
		t.Errorf("answers with nothing in them = %v", out)
	}

	// any one of them that cannot be read is said by name
	for call, want := range map[string]string{
		envsCall:      "the environments could not be listed",
		tagsCall:      "the tags could not be listed",
		envOrderCall:  "the order of environments could not be read",
		tagOrderCall:  "the order of tags could not be read",
		sidebarCall:   "the sidebar could not be read",
		gridCall:      "the columns of the grids could not be read",
		jobsCall:      "whether Dockhand's own jobs are hidden could not be read",
		dashboardCall: "the dashboard could not be read",
		pagesCall:     "the pages could not be read",
	} {
		refusing := withNothingRemembered(t)
		refusing.Fail(call, http.StatusForbidden, notAllowed)
		if msg := session(t, refusing, Options{}).CallErr(t, "preferences_get", nil); !strings.Contains(msg, want) {
			t.Errorf("%s refused = %q, want %q", call, msg, want)
		}
	}
}

func TestPreferencesSet(t *testing.T) {
	t.Parallel()

	column := func(id string, visible bool) map[string]any { return map[string]any{"id": id, "visible": visible} }
	tile := func(env any, x, y, w, h int) map[string]any {
		return map[string]any{"env": env, "x": x, "y": y, "w": w, "h": h}
	}
	everythingSet := map[string]any{
		"environment_order": []any{"beta", 1}, "tag_order": []any{"critical", "web", 7}, "sidebar_order": []string{"stacks", "containers"}, "sidebar_hidden": []string{"terminal"},
		"grid": " containers ", "grid_columns": []any{column(" name ", true), column("image", false)}, "hide_system_jobs": true,
		"dashboard_locked": true, "dashboard_view": " List ", "dashboard_tiles": []any{tile("BETA", 0, 0, 2, 2), tile(1, 2, 0, 1, 2)}, "landing_page": " Stacks ", "env_click_page": "logs",
	}
	nothingSet := map[string]any{
		"environment_order": []any{}, "tag_order": []any{}, "sidebar_order": []string{}, "sidebar_hidden": []string{}, "grid_reset": "all", "hide_system_jobs": false,
		"dashboard_locked": false, "dashboard_view": "grid", "dashboard_tiles": []any{}, "landing_page": "", "env_click_page": " ",
	}

	t.Run("refused before anything is sent", func(t *testing.T) {
		t.Parallel()

		dock := withNothingRemembered(t)
		suite := session(t, dock, everything)
		for what, tc := range map[string]struct {
			args map[string]any
			want string
		}{
			"a grid with no columns given":     {map[string]any{"grid": "containers"}, "grid and grid_columns go together"},
			"columns of no grid":               {map[string]any{"grid_columns": []any{column("name", true)}}, "grid and grid_columns go together"},
			"an empty list of columns":         {map[string]any{"grid": "containers", "grid_columns": []any{}}, "grid_columns is empty: a grid is shown as it comes again with grid_reset"},
			"a column with no id":              {map[string]any{"grid": "containers", "grid_columns": []any{column(" ", true)}}, "grid_columns has a column with no id"},
			"a grid both set and reset":        {map[string]any{"grid": "containers", "grid_columns": []any{column("name", true)}, "grid_reset": "containers"}, "the grid containers is in both grid and grid_reset"},
			"a layout that is not one":         {map[string]any{"dashboard_view": "heap"}, `dashboard_view is "heap": it is grid or list. Dockhand would save it all the same`},
			"an opening page there is none of": {map[string]any{"landing_page": "nowhere"}, `landing_page is "nowhere": it is dashboard, containers, stacks, logs, images, volumes, networks, schedules, activity or terminal, or empty for Dockhand's own`},
			"a page a click cannot open":       {map[string]any{"env_click_page": "dashboard"}, `env_click_page is "dashboard": it is containers, stacks,`},
			"a page of the sidebar twice":      {map[string]any{"sidebar_order": []string{"stacks", "stacks"}}, `sidebar_order has "stacks", which is empty or is there twice`},
			"an empty page left out":           {map[string]any{"sidebar_hidden": []string{" "}}, `sidebar_hidden has " ", which is empty or is there twice`},
			"an environment that is no name":   {map[string]any{"environment_order": []any{map[string]any{"id": 1}}}, "environment is map[id:1]: it is a name or a number"},
			"an environment there is none of":  {map[string]any{"environment_order": []any{"beta", "nope"}}, `environment_order has "nope", which is no environment there is: Dockhand would save the order all the same`},
			"an environment twice":             {map[string]any{"environment_order": []any{"beta", 2}}, "environment_order has beta twice: each is in the order once"},
			"a tag there is none of":           {map[string]any{"tag_order": []any{"web", 44}}, `tag_order has "44", which is no tag there is`},
			"a tag twice":                      {map[string]any{"tag_order": []any{"web", 3}}, "tag_order has web twice"},
			"a tile of no environment":         {map[string]any{"dashboard_tiles": []any{tile("nope", 0, 0, 1, 2)}}, `dashboard_tiles has a tile of "nope", which is no environment there is: Dockhand would save it all the same`},
			"a tile of what is no name":        {map[string]any{"dashboard_tiles": []any{tile(1.5, 0, 0, 1, 2)}}, "the env of a tile is 1.5: its number is a whole one"},
			"an environment's tile twice":      {map[string]any{"dashboard_tiles": []any{tile("alpha", 0, 0, 1, 2), tile(1, 1, 0, 1, 2)}}, "dashboard_tiles has the tile of alpha twice: an environment has one tile"},
			"a tile of no size":                {map[string]any{"dashboard_tiles": []any{tile("alpha", 0, 0, 0, 2)}}, "the tile of alpha is at x 0, y 0 and is 0 wide and 2 tall: a place is 0 or more, and a size 1 or more"},
			"a tile off the grid":              {map[string]any{"dashboard_tiles": []any{tile("alpha", -1, 0, 1, 2)}}, "the tile of alpha is at x -1, y 0"},
			"nothing to change":                {map[string]any{}, "nothing to change was given: environment_order, tag_order,"},
			"nothing but a dry run":            {map[string]any{"dry_run": true}, "nothing to change was given"},
		} {
			if msg := suite.CallErr(t, "preferences_set", tc.args); !strings.Contains(msg, tc.want) {
				t.Errorf("%s = %q, want %q", what, msg, tc.want)
			}
		}

		// what is that way already is not sent again: nothing saved set to nothing, and a grid nothing is saved for shown as it comes
		for what, args := range map[string]map[string]any{"nothing saved, set to nothing": nothingSet, "a grid nothing is saved for": {"grid_reset": "images"}} {
			if out := suite.Call(t, "preferences_set", args); !strings.Contains(acctest.Str(out["note"]), "nothing to do: it is that way already") || len(acctest.Strs(t, out["changes"], "changes")) != 0 {
				t.Errorf("%s = %v", what, out)
			}
		}

		// a dry run says each thing that would be done, and gives what is remembered now
		out := suite.Call(t, "preferences_set", over(everythingSet, map[string]any{"dry_run": true}))
		if got := strings.Join(acctest.Strs(t, out["changes"], "changes"), "; "); got != "would list the environments as beta, alpha; would list the tags as Critical, web, spare; would set the sidebar in the order stacks, containers, with terminal left out; would set the 2 columns of the grid containers; would have the schedules page leave out Dockhand's own jobs; would have the dashboard locked and laid out as a list and with 2 tiles placed; would make the opening page stacks and the page a click on an environment opens logs, for everyone who chose none" {
			t.Errorf("a dry run of everything = %q", got)
		}
		if !acctest.IsBool(out["dry_run"], true) || out["landing_page"] != "dashboard" || out["grids"] != nil {
			t.Errorf("a dry run gives what is remembered now: %v", out)
		}

		if writes := writesOf(dock); len(writes) != 0 {
			t.Errorf("what was refused, was that way already or was a dry run sent %v", writes)
		}

		dock.Fail(sidebarCall, http.StatusForbidden, notAllowed)
		if msg := suite.CallErr(t, "preferences_set", map[string]any{"hide_system_jobs": true}); !strings.Contains(msg, "the sidebar could not be read") {
			t.Errorf("preferences that cannot be read = %q", msg)
		}
	})

	t.Run("everything set", func(t *testing.T) {
		t.Parallel()

		dock := withNothingRemembered(t)
		suite := session(t, dock, everything)
		dock.Answer(envOrderSave, `{"order":[2,1]}`)
		dock.Answer(tagOrderSave, `{"order":[1,3,7]}`)
		dock.Answer(sidebarSave, savedSidebar)
		dock.Answer(gridSave, savedGrids)
		dock.Answer(jobsSave, jobsHidden)
		dock.Answer(dashboardSave, savedDashboard)
		dock.Answer(pagesSave, pagesSet)
		dock.Then(pagesSave, func(dockhandtest.Request) {
			remember(dock)
			dock.Answer(envOrderCall, `{"order":[2,1]}`)
			dock.Answer(tagOrderCall, `{"order":[1,3,7]}`)
			dock.Answer(dashboardCall, `{"gridLayout":[{"id":2,"x":0,"y":0,"w":2,"h":2},{"id":1,"x":2,"y":0,"w":1,"h":2}],"locked":true,"viewMode":"list"}`)
		})

		// names are looked up whatever their case, a number is taken for a name, and what is given is trimmed
		out := suite.Call(t, "preferences_set", everythingSet)
		for call, want := range map[string]string{
			envOrderSave:  `{"order":[2,1]}`,
			tagOrderSave:  `{"order":[1,3,7]}`,
			sidebarSave:   `{"hidden":["terminal"],"order":["stacks","containers"]}`,
			gridSave:      `{"columns":[{"id":"name","visible":true},{"id":"image","visible":false}],"gridId":"containers"}`,
			jobsSave:      `{"hideSystemJobs":true}`,
			dashboardSave: `{"gridLayout":[{"h":2,"id":2,"w":2,"x":0,"y":0},{"h":2,"id":1,"w":1,"x":2,"y":0}],"locked":true,"viewMode":"list"}`,
			pagesSave:     `{"envClickPage":"logs","landingPage":"stacks"}`,
		} {
			if calls := dock.Requests(call); len(calls) != 1 || flat(sent(t, calls[0])) != want {
				t.Errorf("%s was sent %v, want once with %s", call, calls, want)
			}
		}
		// the pages are everyone's: a user's own is not a token's to set
		if scope := dock.Last(pagesSave).Query.Get("scope"); scope != "global" {
			t.Errorf("the pages were set for the scope %q", scope)
		}
		if got := strings.Join(acctest.Strs(t, out["changes"], "changes"), "; "); !strings.HasPrefix(got, "listed the environments as beta, alpha; listed the tags as Critical, web, spare; set the sidebar in the order stacks, containers, with terminal left out; set the 2 columns") || !strings.HasSuffix(got, "made the opening page stacks and the page a click on an environment opens logs, for everyone who chose none") {
			t.Errorf("what was done = %q", got)
		}
		// read back afterwards
		if out["dry_run"] != nil || out["note"] != nil || out["landing_page_set"] != "stacks" || !acctest.IsBool(out["hide_system_jobs"], true) || strings.Join(acctest.Strs(t, out["environment_order"], "environment_order"), " ") != "beta alpha" || len(acctest.Rows(t, out["dashboard_tiles"], "dashboard_tiles")) != 2 {
			t.Errorf("everything set, read back = %v", out)
		}

		// given again it is that way already
		before := len(writesOf(dock))
		if out = suite.Call(t, "preferences_set", everythingSet); !strings.Contains(acctest.Str(out["note"]), "nothing to do") || len(writesOf(dock)) != before {
			t.Errorf("everything set a second time = %v", out)
		}
	})

	t.Run("put back to nothing", func(t *testing.T) {
		t.Parallel()

		dock := withNothingRemembered(t)
		remember(dock)
		suite := session(t, dock, everything)
		dock.Answer(envOrderSave, noOrder)
		dock.Answer(tagOrderSave, noOrder)
		dock.Answer(sidebarSave, plainSidebar)
		dock.Answer(gridForget, noGrids)
		dock.Answer(jobsSave, `{"success":true,"hideSystemJobs":false}`)
		dock.Answer(dashboardSave, plainDashboard)
		dock.Answer(pagesSave, `{"success":true,"global":{"landingPage":null,"envClickPage":null}}`)

		// one grid is shown as it comes by its id, and the others keep their columns
		dock.Then(gridForget, func(dockhandtest.Request) {
			dock.Answer(gridCall, `{"preferences":{"containers":{"columns":[{"id":"name","visible":true},{"id":"image","visible":false}]}}}`)
		})
		out := suite.Call(t, "preferences_set", map[string]any{"grid_reset": "images"})
		grids := acctest.Object(t, out["grids"], "grids")
		if got := dock.Last(gridForget).Query.Get("gridId"); got != "images" || grids["images"] != nil || grids["containers"] == nil || strings.Join(acctest.Strs(t, out["changes"], "changes"), "; ") != "had the grid images shown as it comes again" {
			t.Errorf("one grid reset = %v, for the grid %q", out, got)
		}
		dock.Then(gridForget, nil)

		// one part of the sidebar is set and the other is sent as it was; one of the two pages is set and the other forgotten, which is a null
		out = suite.Call(t, "preferences_set", map[string]any{"sidebar_order": []string{"containers"}, "dashboard_locked": false, "landing_page": "logs", "env_click_page": ""})
		pages := dock.Requests(pagesSave)
		if got := flat(sent(t, dock.Last(sidebarSave))); got != `{"hidden":["terminal"],"order":["containers"]}` || dock.Last(dashboardSave).Body != `{"locked":false}` || len(pages) != 2 || pages[0].Body != `{"landingPage":"logs"}` || pages[1].Body != `{"envClickPage":null}` || pages[1].Query.Get("scope") != "global" {
			t.Errorf("a part of each = sidebar %s, dashboard %s, pages %v", got, dock.Last(dashboardSave).Body, pages)
		}
		if got := strings.Join(acctest.Strs(t, out["changes"], "changes"), "; "); got != "set the sidebar in the order containers, with terminal left out; had the dashboard unlocked; made the opening page logs and the page a click on an environment opens Dockhand's own, for everyone who chose none" {
			t.Errorf("a part of each, in words = %q", got)
		}
		if out = suite.Call(t, "preferences_set", map[string]any{"sidebar_hidden": []string{}, "dashboard_view": "grid"}); flat(sent(t, dock.Last(sidebarSave))) != `{"hidden":[],"order":["stacks","containers"]}` || dock.Last(dashboardSave).Body != `{"viewMode":"grid"}` {
			t.Errorf("the other part of each = %v", out)
		}

		// everything back to nothing: each order as an empty list, every grid at once, and the two pages as nulls in the one call
		dock.Then(pagesSave, func(dockhandtest.Request) {
			dock.Answer(envOrderCall, noOrder)
			dock.Answer(tagOrderCall, noOrder)
			dock.Answer(sidebarCall, plainSidebar)
			dock.Answer(gridCall, noGrids)
			dock.Answer(jobsCall, jobsShown)
			dock.Answer(dashboardCall, plainDashboard)
			dock.Answer(pagesCall, ownPages)
		})
		before := len(dock.Requests(pagesSave))
		out = suite.Call(t, "preferences_set", nothingSet)
		pages = dock.Requests(pagesSave)[before:]
		if dock.Last(envOrderSave).Body != noOrder || dock.Last(tagOrderSave).Body != noOrder || flat(sent(t, dock.Last(sidebarSave))) != `{"hidden":[],"order":[]}` || len(dock.Last(gridForget).Query) != 0 || dock.Last(jobsSave).Body != `{"hideSystemJobs":false}` || flat(sent(t, dock.Last(dashboardSave))) != `{"gridLayout":[],"locked":false,"viewMode":"grid"}` || len(pages) != 1 || flat(sent(t, pages[0])) != `{"envClickPage":null,"landingPage":null}` {
			t.Errorf("everything put back was sent orders %s %s, sidebar %s, grids %v, jobs %s, dashboard %s, pages %v", dock.Last(envOrderSave).Body, dock.Last(tagOrderSave).Body, dock.Last(sidebarSave).Body, dock.Last(gridForget).Query, dock.Last(jobsSave).Body, dock.Last(dashboardSave).Body, pages)
		}
		if got := strings.Join(acctest.Strs(t, out["changes"], "changes"), "; "); got != "listed the environments as their names order them; listed the tags as their names order them; set the sidebar in the order it comes in, with nothing left out; had every grid shown as it comes again; had the schedules page show Dockhand's own jobs; had the dashboard unlocked and laid out as a grid and with its tiles where the page puts them; made the opening page Dockhand's own and the page a click on an environment opens Dockhand's own, for everyone who chose none" {
			t.Errorf("everything put back, in words = %q", got)
		}
		if out["grids"] != nil || out["landing_page_set"] != nil || out["landing_page"] != "dashboard" {
			t.Errorf("everything put back, read back = %v", out)
		}
	})

	t.Run("refused by Dockhand", func(t *testing.T) {
		t.Parallel()

		// what was done before the one that failed is said, and nothing after it is tried
		dock := withNothingRemembered(t)
		suite := session(t, dock, everything)
		dock.Answer(envOrderSave, `{"order":[2,1]}`)
		dock.Fail(jobsSave, http.StatusBadRequest, "hideSystemJobs must be a boolean")
		msg := suite.CallErr(t, "preferences_set", map[string]any{"environment_order": []any{"beta", "alpha"}, "hide_system_jobs": true, "dashboard_locked": true})
		if !strings.Contains(msg, "this was not done, and what came before it was: had the schedules page leave out Dockhand's own jobs") || !strings.Contains(msg, "hideSystemJobs must be a boolean") || len(dock.Requests(envOrderSave)) != 1 || len(dock.Requests(dashboardSave)) != 0 {
			t.Errorf("a change refused part way = %q, after %v", msg, writesOf(dock))
		}

		// either half of setting the pages may be the one refused: the page set, or the one forgotten after it
		dock.Answer(pagesCall, chosenPages)
		dock.Fail(pagesSave, http.StatusBadRequest, "Invalid landingPage")
		if msg = suite.CallErr(t, "preferences_set", map[string]any{"landing_page": "logs", "env_click_page": ""}); !strings.Contains(msg, "this was not done") || len(dock.Requests(pagesSave)) != 1 {
			t.Errorf("a page refused = %q", msg)
		}
		dock.Answer(pagesSave, pagesSet)
		dock.Then(pagesSave, func(r dockhandtest.Request) {
			if strings.Contains(r.Body, "null") {
				dock.Fail(pagesSave, http.StatusInternalServerError, "Failed to update navigation settings")
			}
		})
		if msg = suite.CallErr(t, "preferences_set", map[string]any{"landing_page": "logs", "env_click_page": ""}); !strings.Contains(msg, "Failed to update navigation settings") || len(dock.Requests(pagesSave)) != 3 {
			t.Errorf("a page not forgotten = %q", msg)
		}
		dock.Then(pagesSave, nil)
		for call, args := range map[string]map[string]any{
			tagOrderSave:  {"tag_order": []any{"web"}},
			sidebarSave:   {"sidebar_hidden": []string{"terminal"}},
			gridSave:      {"grid": "containers", "grid_columns": []any{column("name", true)}},
			dashboardSave: {"dashboard_view": "list"},
			envOrderSave:  {"environment_order": []any{"alpha"}},
		} {
			dock.Fail(call, http.StatusInternalServerError, "Failed to save")
			if msg = suite.CallErr(t, "preferences_set", args); !strings.Contains(msg, "this was not done") || !strings.Contains(msg, "Failed to save") {
				t.Errorf("%s refused = %q", call, msg)
			}
		}

		// a grid's columns that cannot be dropped
		saved := withNothingRemembered(t)
		remember(saved)
		saved.Fail(gridForget, http.StatusInternalServerError, "Failed to reset grid preferences")
		if msg = session(t, saved, everything).CallErr(t, "preferences_set", map[string]any{"grid_reset": "all"}); !strings.Contains(msg, "this was not done, and what came before it was: had every grid shown as it comes again") {
			t.Errorf("grids not reset = %q", msg)
		}
	})

	t.Run("done and not read back", func(t *testing.T) {
		t.Parallel()

		dock := withNothingRemembered(t)
		suite := session(t, dock, everything)
		dock.Answer(jobsSave, jobsHidden)
		dock.Then(jobsSave, func(dockhandtest.Request) { dock.Fail(dashboardCall, http.StatusForbidden, notAllowed) })

		// it went through, so it is no error: the note says that what is given is from before
		out := suite.Call(t, "preferences_set", map[string]any{"hide_system_jobs": true})
		if note := acctest.Str(out["note"]); !strings.Contains(note, "it was done, and could not be read back") || !strings.Contains(note, "the dashboard could not be read") || !acctest.IsBool(out["hide_system_jobs"], false) || len(acctest.Strs(t, out["changes"], "changes")) != 1 {
			t.Errorf("a change that could not be read back = %v", out)
		}
	})
}

func TestFavoriteList(t *testing.T) {
	t.Parallel()

	dock := twoHosts(t)
	suite := session(t, dock, Options{})

	// a name and a group come in the one list, in order; what is neither, and a member that is no name, is passed over
	dock.AnswerWhen(favoritesCall, "env", "1", `{"favorites":["loose",{"name":"front","containers":["web-front-1",7,"web-back-1"]},5,{"name":"bare"}]}`)
	dock.FailWhen(favoritesCall, "env", "2", http.StatusInternalServerError, "Failed to get favorites")
	out := suite.Call(t, "favorite_list", nil)
	if got := flat(out); got != `{"failed":[{"env":"beta","error":"GET /api/preferences/favorites: HTTP 500 (expected 200): Failed to get favorites"}],"favorites":[{"env":"alpha","favorites":[{"container":"loose"},{"containers":["web-front-1","web-back-1"],"group":"front"},{"group":"bare"}]}]}` {
		t.Errorf("the favourites of two hosts, one of which cannot be asked = %s", got)
	}

	dock.AnswerWhen(favoritesCall, "env", "2", noFavorites)
	out = suite.Call(t, "favorite_list", map[string]any{"env": "beta"})
	if got := flat(out); got != `{"favorites":[{"env":"beta","favorites":[]}]}` {
		t.Errorf("a host with no favourites = %s", got)
	}
	if msg := suite.CallErr(t, "favorite_list", map[string]any{"env": "nope"}); !strings.Contains(msg, "nope") {
		t.Errorf("a host there is none of = %q", msg)
	}
}

func TestFavoriteSet(t *testing.T) {
	t.Parallel()

	loose, front := map[string]any{"container": "loose"}, map[string]any{"group": "front", "containers": []string{"web-front-1", "web-back-1"}}
	onAlpha := func(favorites ...any) map[string]any {
		return map[string]any{"env": "alpha", "favorites": append([]any{}, favorites...)}
	}

	t.Run("refused before anything is sent", func(t *testing.T) {
		t.Parallel()

		dock := twoHosts(t)
		suite := session(t, dock, everything)
		// a name no container has any more is kept already, alone and in a group
		dock.AnswerWhen(favoritesCall, "env", "1", `{"favorites":["gone",{"name":"front","containers":["web-front-1","older"]}]}`)
		for what, tc := range map[string]struct {
			args map[string]any
			want string
		}{
			"no list":                        {map[string]any{"env": "alpha", "favorites": nil}, "favorites is required: everything to keep, in order, or an empty list to keep nothing"},
			"no environment":                 {map[string]any{"favorites": []any{}}, "env is required"},
			"an entry that is nothing":       {onAlpha(loose, map[string]any{}), "entry 2 of favorites is neither one container nor one group"},
			"an entry that is both":          {onAlpha(map[string]any{"container": "loose", "group": "front"}), "entry 1 of favorites is neither one container nor one group"},
			"a container with containers":    {onAlpha(map[string]any{"container": "loose", "containers": []string{"steady"}}), "favorites has loose with containers of its own"},
			"a group of nothing":             {onAlpha(map[string]any{"group": "front", "containers": []string{" "}}), "the group front has no containers: a group is at least one"},
			"a container twice":              {onAlpha(loose, front, map[string]any{"container": " loose "}), "favorites has the container loose twice"},
			"two groups of one name":         {onAlpha(front, map[string]any{"group": "front", "containers": []string{"loose"}}), "favorites has two groups named front"},
			"a name no container has":        {onAlpha(map[string]any{"container": "nope"}), `no container is named "nope" on alpha: container_list gives the ones there are. Dockhand would keep the name all the same`},
			"a group with such a name":       {onAlpha(map[string]any{"group": "front", "containers": []string{"loose", "nope"}}), `no container is named "nope" on alpha`},
			"another host's container":       {onAlpha(map[string]any{"container": "solo"}), `no container is named "solo" on alpha`},
			"a host there is none of":        {map[string]any{"env": "nope", "favorites": []any{}}, "nope"},
			"a name in another case":         {onAlpha(map[string]any{"container": "Loose"}), `no container is named "Loose" on alpha`},
			"a group, given a second member": {onAlpha(map[string]any{"group": "front", "containers": []string{"web-front-1", "nope"}}), `no container is named "nope"`},
		} {
			if msg := suite.CallErr(t, "favorite_set", tc.args); !strings.Contains(msg, tc.want) {
				t.Errorf("%s = %q, want %q", what, msg, tc.want)
			}
		}

		// what is kept already is not sent again, a name nothing has among it
		if out := suite.Call(t, "favorite_set", onAlpha(map[string]any{"container": "gone"}, map[string]any{"group": " front ", "containers": []string{" web-front-1", "older", "older", ""}})); !strings.Contains(acctest.Str(out["note"]), "nothing to do: that is what is kept, in that order") || len(acctest.Strs(t, out["changes"], "changes")) != 0 || len(acctest.Rows(t, out["favorites"], "favorites")) != 2 {
			t.Errorf("the favourites there are already = %v", out)
		}

		// a dry run says how many there would be, and gives what is kept now
		out := suite.Call(t, "favorite_set", over(onAlpha(), map[string]any{"dry_run": true}))
		if got := strings.Join(acctest.Strs(t, out["changes"], "changes"), "; "); got != "would keep no favourites for the log view of alpha, in place of 2 favourites" || !acctest.IsBool(out["dry_run"], true) || len(acctest.Rows(t, out["favorites"], "favorites")) != 2 {
			t.Errorf("a dry run = %v", out)
		}

		if writes := writesOf(dock); len(writes) != 0 {
			t.Errorf("what was refused, was kept already or was a dry run sent %v", writes)
		}

		dock.FailWhen("GET /api/containers", "env", "1", http.StatusBadGateway, "the host did not answer")
		if msg := suite.CallErr(t, "favorite_set", onAlpha(loose)); !strings.Contains(msg, "the containers of alpha could not be listed, so nothing was changed") {
			t.Errorf("a host that cannot be asked for its containers = %q", msg)
		}
		dock.FailWhen(favoritesCall, "env", "1", http.StatusInternalServerError, "Failed to get favorites")
		if msg := suite.CallErr(t, "favorite_set", onAlpha(loose)); !strings.Contains(msg, "the favourites of alpha could not be read, so nothing was changed") {
			t.Errorf("favourites that cannot be read = %q", msg)
		}
	})

	t.Run("set whole", func(t *testing.T) {
		t.Parallel()

		dock := twoHosts(t)
		suite := session(t, dock, everything)
		dock.AnswerWhen(favoritesCall, "env", "1", noFavorites)
		dock.Answer(favoritesSave, twoFavorites)
		dock.Then(favoritesSave, func(dockhandtest.Request) { dock.AnswerWhen(favoritesCall, "env", "1", twoFavorites) })

		// names and groups go as the one list, which is what keeps Dockhand from dropping either kind
		out := suite.Call(t, "favorite_set", onAlpha(loose, front))
		if body := dock.Last(favoritesSave).Body; body != `{"action":"reorder","environmentId":1,"favorites":["loose",{"containers":["web-front-1","web-back-1"],"name":"front"}]}` {
			t.Errorf("two favourites were sent as %s", body)
		}
		if got := flat(out); got != `{"changes":["kept 2 favourites for the log view of alpha, in place of no favourites"],"env":"alpha","favorites":[{"container":"loose"},{"containers":["web-front-1","web-back-1"],"group":"front"}]}` {
			t.Errorf("two favourites set = %s", got)
		}

		// the other way round is a change, and none at all is an empty list
		reordered := `{"favorites":[{"name":"front","containers":["web-front-1","web-back-1"]},"loose"]}`
		dock.Answer(favoritesSave, reordered)
		dock.Then(favoritesSave, func(dockhandtest.Request) { dock.AnswerWhen(favoritesCall, "env", "1", reordered) })
		out = suite.Call(t, "favorite_set", onAlpha(front, loose))
		if rows := acctest.Rows(t, out["favorites"], "favorites"); len(rows) != 2 || rows[0]["group"] != "front" || rows[1]["container"] != "loose" || !strings.HasPrefix(dock.Last(favoritesSave).Body, `{"action":"reorder","environmentId":1,"favorites":[{"containers"`) {
			t.Errorf("the favourites the other way round = %v", out)
		}
		dock.Answer(favoritesSave, noFavorites)
		dock.Then(favoritesSave, func(dockhandtest.Request) { dock.AnswerWhen(favoritesCall, "env", "1", noFavorites) })
		out = suite.Call(t, "favorite_set", onAlpha())
		if got := flat(out); got != `{"changes":["kept no favourites for the log view of alpha, in place of 2 favourites"],"env":"alpha","favorites":[]}` || dock.Last(favoritesSave).Body != `{"action":"reorder","environmentId":1,"favorites":[]}` {
			t.Errorf("no favourites kept = %s, sent %s", got, dock.Last(favoritesSave).Body)
		}
	})

	t.Run("refused, not read back, or not as asked", func(t *testing.T) {
		t.Parallel()

		dock := twoHosts(t)
		suite := session(t, dock, everything)
		dock.AnswerWhen(favoritesCall, "env", "1", noFavorites)

		dock.Fail(favoritesSave, http.StatusInternalServerError, "Failed to update favorites")
		if msg := suite.CallErr(t, "favorite_set", onAlpha(loose)); !strings.Contains(msg, "the favourites of alpha were not set") || !strings.Contains(msg, "Failed to update favorites") {
			t.Errorf("favourites Dockhand would not set = %q", msg)
		}

		// answered as set and nothing kept: an error, not a success
		dock.Answer(favoritesSave, noFavorites)
		if msg := suite.CallErr(t, "favorite_set", onAlpha(loose)); !strings.Contains(msg, "the favourites of alpha were answered as set, and what is kept is not what was asked: favorite_list gives it") {
			t.Errorf("favourites answered as set and not kept = %q", msg)
		}

		// set, and not to be read afterwards: what was sent is given, with a note
		unreadable := func(dockhandtest.Request) {
			dock.FailWhen(favoritesCall, "env", "1", http.StatusInternalServerError, "Failed to get favorites")
		}
		dock.Then(favoritesSave, unreadable)
		out := suite.Call(t, "favorite_set", onAlpha(loose, front))
		if rows := acctest.Rows(t, out["favorites"], "favorites"); !strings.Contains(acctest.Str(out["note"]), "they were set, and could not be read back: favorites is what was sent") || len(rows) != 2 || rows[0]["container"] != "loose" || rows[0]["containers"] != nil || rows[1]["group"] != "front" {
			t.Errorf("favourites set and not read back = %v", out)
		}
	})
}
