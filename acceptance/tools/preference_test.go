//go:build acceptance

package tools_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// What the web interface remembers is arranged with the tools alone, read back, changed a part at a time and put back as it was: the orders of the
// environments and of a tag, the sidebar, the columns of a grid, the dashboard and its tiles, and the two pages; what Dockhand would save and should
// not is refused on the way.
func TestJourneyTheWebInterfaceArrangedAndPutBack(t *testing.T) {
	const tag = "acc-journey-preference-tag"
	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	column := func(id string, visible bool) map[string]any { return map[string]any{"id": id, "visible": visible} }
	// written is an answer as the JSON it came in, its names in order, to hold whole against what is wanted
	written := func(answer map[string]any) string {
		raw, err := json.Marshal(answer)
		if err != nil {
			t.Fatal(err)
		}

		return string(raw)
	}
	asItComes := map[string]any{
		"environment_order": []any{}, "tag_order": []any{}, "sidebar_order": []string{}, "sidebar_hidden": []string{}, "grid_reset": "all", "hide_system_jobs": false,
		"dashboard_locked": false, "dashboard_view": "grid", "dashboard_tiles": []any{}, "landing_page": "", "env_click_page": "",
	}
	clean := func() {
		suite.Call(t, "preferences_set", asItComes)
		for _, row := range acctest.Rows(t, call(t, "tag_list", nil)["tags"], "tags") {
			if row["name"] == tag {
				suite.Call(t, "tag_delete", map[string]any{"tag": tag, "confirm": true})
			}
		}
	}
	clean()
	t.Cleanup(clean)
	before := written(call(t, "preferences_get", nil))
	if before != `{"dashboard_locked":false,"dashboard_tiles":[],"dashboard_view":"grid","env_click_page":"containers","environment_order":[],"hide_system_jobs":false,"landing_page":"dashboard","sidebar_hidden":[],"sidebar_order":[],"tag_order":[]}` {
		t.Fatalf("a web interface with nothing arranged = %s", before)
	}
	call(t, "tag_create", map[string]any{"name": tag})

	// refused before anything is sent, each of which Dockhand would save or would refuse less plainly
	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"an environment there is none of": {map[string]any{"environment_order": []any{testenv.Beta, "acc-nowhere"}}, `environment_order has "acc-nowhere", which is no environment there is`},
		"an environment twice":            {map[string]any{"environment_order": []any{testenv.Beta, testenv.Beta}}, "twice"},
		"a tag there is none of":          {map[string]any{"tag_order": []any{"acc-no-such-tag"}}, "which is no tag there is"},
		"a layout there is none of":       {map[string]any{"dashboard_view": "heap"}, "it is grid or list"},
		"a tile of no size":               {map[string]any{"dashboard_tiles": []any{map[string]any{"env": testenv.Alpha, "x": 0, "y": 0, "w": 0, "h": 0}}}, "a size 1 or more"},
		"a page there is none of":         {map[string]any{"landing_page": "nowhere"}, "or empty for Dockhand's own"},
		"a grid with no columns":          {map[string]any{"grid": "containers"}, "grid and grid_columns go together"},
		"nothing at all":                  {map[string]any{}, "nothing to change was given"},
	} {
		if msg := refused(t, "preferences_set", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}

	// said first, which changes nothing; then done, and read back by the tool that set it and by the one that reads
	arranged := map[string]any{
		"environment_order": []any{testenv.Beta, testenv.Alpha}, "tag_order": []any{tag}, "sidebar_order": []string{"stacks", "containers"}, "sidebar_hidden": []string{"terminal"},
		"grid": "containers", "grid_columns": []any{column("name", true), column("image", false)}, "hide_system_jobs": true, "dashboard_locked": true, "dashboard_view": "list",
		"dashboard_tiles": []any{map[string]any{"env": testenv.Beta, "x": 0, "y": 0, "w": 2, "h": 2}, map[string]any{"env": testenv.Alpha, "x": 2, "y": 0, "w": 1, "h": 2}}, "landing_page": "stacks", "env_click_page": "logs",
	}
	if dry := call(t, "preferences_set", over(arranged, map[string]any{"dry_run": true})); len(acctest.Strs(t, dry["changes"], "changes")) != 7 || !acctest.IsBool(dry["dry_run"], true) || written(call(t, "preferences_get", nil)) != before {
		t.Fatalf("a dry run of arranging everything = %v", dry)
	}
	set := call(t, "preferences_set", arranged)
	if changes := acctest.Strs(t, set["changes"], "changes"); len(changes) != 7 || changes[0] != "listed the environments as beta, alpha" || changes[1] != "listed the tags as "+tag {
		t.Errorf("what arranging everything did = %v", changes)
	}
	want := `{"dashboard_locked":true,"dashboard_tiles":[{"env":"beta","h":2,"w":2,"x":0,"y":0},{"env":"alpha","h":2,"w":1,"x":2,"y":0}],"dashboard_view":"list","env_click_page":"logs","env_click_page_set":"logs","environment_order":["beta","alpha"],"grids":{"containers":[{"id":"name","visible":true},{"id":"image","visible":false}]},"hide_system_jobs":true,"landing_page":"stacks","landing_page_set":"stacks","sidebar_hidden":["terminal"],"sidebar_order":["stacks","containers"],"tag_order":["` + tag + `"]}`
	if got := written(call(t, "preferences_get", nil)); got != want {
		t.Errorf("everything arranged, read = %s, want %s", got, want)
	}
	delete(set, "changes")
	if got := written(set); got != want {
		t.Errorf("everything arranged, as the tool that did it read it back = %s, want %s", got, want)
	}
	if again := call(t, "preferences_set", arranged); len(acctest.Strs(t, again["changes"], "changes")) != 0 || !strings.Contains(acctest.Str(again["note"]), "that way already") {
		t.Errorf("the same arrangement again = %v", again)
	}

	// a part at a time, the rest kept: one grid shown as it comes, one page forgotten and the other left, nothing hidden in the sidebar
	part := call(t, "preferences_set", map[string]any{"grid_reset": "containers", "landing_page": "", "sidebar_hidden": []string{}, "dashboard_locked": false})
	if part["grids"] != nil || part["landing_page"] != "dashboard" || part["landing_page_set"] != nil || part["env_click_page_set"] != "logs" || len(acctest.Strs(t, part["sidebar_hidden"], "sidebar_hidden")) != 0 || strings.Join(acctest.Strs(t, part["sidebar_order"], "sidebar_order"), " ") != "stacks containers" || !acctest.IsBool(part["dashboard_locked"], false) || part["dashboard_view"] != "list" || len(acctest.Rows(t, part["dashboard_tiles"], "dashboard_tiles")) != 2 || len(acctest.Strs(t, part["changes"], "changes")) != 4 {
		t.Errorf("a part at a time = %v", part)
	}

	// a tag that is removed stays in the order under its number, which is why the order is put back before the tag goes
	back := call(t, "preferences_set", asItComes)
	if changes := acctest.Strs(t, back["changes"], "changes"); len(changes) != 6 {
		t.Errorf("what putting everything back did = %v", changes)
	}
	call(t, "tag_delete", map[string]any{"tag": tag, "confirm": true})
	if got := written(call(t, "preferences_get", nil)); got != before {
		t.Errorf("everything put back = %s, want what there was: %s", got, before)
	}
}

// The favourites of a host's log view are set with the tools alone: a container and a group in the one list, in the order given and the other way
// round, kept by name once the container is gone, and cleared; a name no container has is refused, which Dockhand would keep.
func TestJourneyLogFavouritesKeptWholeAndCleared(t *testing.T) {
	const group = "acc-journey-favourites"
	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	passing := started(t, testenv.Alpha, "acc-journey-favourite")
	on := func(env string) []map[string]any {
		for _, row := range acctest.Rows(t, call(t, "favorite_list", nil)["favorites"], "favorites") {
			if row["env"] == env {
				return acctest.Rows(t, row["favorites"], "favorites")
			}
		}
		t.Fatalf("favorite_list gave nothing for %s", env)

		return nil
	}
	setOn := func(favorites ...any) map[string]any {
		return map[string]any{"env": testenv.Alpha, "favorites": append([]any{}, favorites...)}
	}
	clean := func() { suite.Call(t, "favorite_set", setOn()) }
	clean()
	t.Cleanup(clean)
	one, both := map[string]any{"container": loose}, map[string]any{"group": group, "containers": []string{loose, passing}}

	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"a name no container has":   {setOn(map[string]any{"container": "acc-no-such-container"}), `no container is named "acc-no-such-container" on alpha`},
		"another host's container":  {setOn(map[string]any{"container": solo}), `no container is named "` + solo + `" on alpha`},
		"a group of nothing":        {setOn(map[string]any{"group": group}), "has no containers"},
		"a container twice":         {setOn(one, one), "twice"},
		"an entry that is both":     {setOn(map[string]any{"container": loose, "group": group}), "is neither one container nor one group"},
		"no host named":             {map[string]any{"favorites": []any{}}, "env is required"},
		"a host there is none of":   {map[string]any{"env": "acc-nowhere", "favorites": []any{}}, "acc-nowhere"},
		"a group member nothing is": {setOn(map[string]any{"group": group, "containers": []string{loose, "acc-no-such-container"}}), `no container is named "acc-no-such-container"`},
	} {
		if msg := refused(t, "favorite_set", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	if dry := call(t, "favorite_set", over(setOn(one, both), map[string]any{"dry_run": true})); !acctest.IsBool(dry["dry_run"], true) || len(on(testenv.Alpha)) != 0 || strings.Join(acctest.Strs(t, dry["changes"], "changes"), "; ") != "would keep 2 favourites for the log view of alpha, in place of no favourites" {
		t.Fatalf("a dry run of keeping two favourites = %v", dry)
	}

	// a container and a group go in as the one list, and are there in that order; the other host keeps none
	set := call(t, "favorite_set", setOn(one, both))
	if rows := acctest.Rows(t, set["favorites"], "favorites"); len(rows) != 2 || rows[0]["container"] != loose || rows[1]["group"] != group || strings.Join(acctest.Strs(t, rows[1]["containers"], "containers"), " ") != loose+" "+passing {
		t.Fatalf("two favourites kept = %v", set)
	}
	if rows := on(testenv.Alpha); len(rows) != 2 || rows[0]["container"] != loose || rows[1]["group"] != group || len(on(testenv.Beta)) != 0 {
		t.Errorf("the favourites as listed = %v on alpha, %v on beta", rows, on(testenv.Beta))
	}
	if again := call(t, "favorite_set", setOn(one, both)); len(acctest.Strs(t, again["changes"], "changes")) != 0 || !strings.Contains(acctest.Str(again["note"]), "nothing to do") {
		t.Errorf("the same favourites again = %v", again)
	}

	// the other way round is a change, and neither kind is lost by it
	if turned := call(t, "favorite_set", setOn(both, one)); len(acctest.Strs(t, turned["changes"], "changes")) != 1 {
		t.Errorf("the favourites the other way round = %v", turned)
	}
	if rows := on(testenv.Alpha); len(rows) != 2 || rows[0]["group"] != group || rows[1]["container"] != loose {
		t.Errorf("the favourites the other way round, as listed = %v", rows)
	}

	// a favourite is kept by name: its container gone, it is listed still, and may be kept on though no container has the name
	if _, err := world.Docker(t.Context(), testenv.Alpha, "rm", "-f", passing); err != nil {
		t.Fatal(err)
	}
	gone := map[string]any{"container": passing}
	if msg := refused(t, "favorite_set", setOn(map[string]any{"container": "acc-journey-never-there"})); !strings.Contains(msg, "no container is named") {
		t.Errorf("a name nothing has, once a container is gone = %q", msg)
	}
	kept := call(t, "favorite_set", setOn(both, gone))
	if rows := acctest.Rows(t, kept["favorites"], "favorites"); len(rows) != 2 || rows[1]["container"] != passing || strings.Join(acctest.Strs(t, rows[0]["containers"], "containers"), " ") != loose+" "+passing {
		t.Errorf("favourites of a container that is gone = %v", kept)
	}

	// cleared, and none are left
	cleared := call(t, "favorite_set", setOn())
	if strings.Join(acctest.Strs(t, cleared["changes"], "changes"), "; ") != "kept no favourites for the log view of alpha, in place of 2 favourites" || len(on(testenv.Alpha)) != 0 {
		t.Errorf("the favourites cleared = %v", cleared)
	}
}
