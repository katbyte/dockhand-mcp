//go:build acceptance

package dockhand_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/pandorest/client"
)

// The orders of environments and of tags, the sidebar and the columns of a grid are saved, read back and forgotten: that each is kept as it is given,
// names of things there are none of among them, and what each call refuses.
func TestOrdersSidebarAndGridColumnsSavedAndForgotten(t *testing.T) {
	const nobody = 987654
	c := dock(t)
	ctx := t.Context()
	alpha, beta := world.Env(testenv.Alpha), world.Env(testenv.Beta)

	// whatever was saved before is put back
	envsBefore, err := c.GetPreferencesEnvironmentOrder(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tagsBefore, err := c.GetPreferencesTagOrder(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sidebarBefore, err := c.GetPreferencesSidebar(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gridsBefore, err := c.GetPreferencesGrid(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sidebarBefore.Model.Preferences == nil {
		t.Fatal("the sidebar was answered with no preferences at all")
	}
	t.Cleanup(func() {
		after := noCancel(t)
		if _, err := c.PostPreferencesEnvironmentOrder(after, dockhand.PostPreferencesEnvironmentOrderRequest{Order: append([]int{}, envsBefore.Model.Order...)}); err != nil {
			t.Errorf("putting the order of environments back: %v", err)
		}
		if _, err := c.PostPreferencesTagOrder(after, dockhand.PostPreferencesTagOrderRequest{Order: append([]int{}, tagsBefore.Model.Order...)}); err != nil {
			t.Errorf("putting the order of tags back: %v", err)
		}
		if _, err := c.PostPreferencesSidebar(after, dockhand.PostPreferencesSidebarRequest{Order: append([]string{}, sidebarBefore.Model.Preferences.Order...), Hidden: append([]string{}, sidebarBefore.Model.Preferences.Hidden...)}); err != nil {
			t.Errorf("putting the sidebar back: %v", err)
		}
		if _, err := c.DeletePreferencesGrid(after, dockhand.DeletePreferencesGridOperationOptions{}); err != nil {
			t.Errorf("forgetting the grids: %v", err)
		}
		for id, grid := range gridsBefore.Model.Preferences {
			columns := make([]dockhand.PostPreferencesGridRequestColumns, 0, len(grid.Columns))
			for _, column := range grid.Columns {
				columns = append(columns, dockhand.PostPreferencesGridRequestColumns(column))
			}
			if _, err := c.PostPreferencesGrid(after, dockhand.PostPreferencesGridRequest{GridId: id, Columns: columns}); err != nil {
				t.Errorf("putting the columns of %s back: %v", id, err)
			}
		}
	})

	// an order is kept as given, an environment there is none of with it, answered by the call that saved it and by the one that reads it
	order := []int{beta, nobody, alpha}
	if saved, err := c.PostPreferencesEnvironmentOrder(ctx, dockhand.PostPreferencesEnvironmentOrderRequest{Order: order}); err != nil || !slices.Equal(saved.Model.Order, order) {
		t.Fatalf("an order of environments saved = %+v, %v", saved.Model, err)
	}
	if read, err := c.GetPreferencesEnvironmentOrder(ctx); err != nil || !slices.Equal(read.Model.Order, order) {
		t.Errorf("the order of environments read back = %+v, %v", read.Model, err)
	}
	// one twice is refused and what was saved stays; an empty list is no order, and so is forgetting it
	if _, err := c.PostPreferencesEnvironmentOrder(ctx, dockhand.PostPreferencesEnvironmentOrderRequest{Order: []int{alpha, alpha}}); client.StatusCode(err) != http.StatusBadRequest || !strings.Contains(err.Error(), "must not repeat") {
		t.Errorf("an environment twice in the order = %v, want a 400", err)
	}
	if read, err := c.GetPreferencesEnvironmentOrder(ctx); err != nil || !slices.Equal(read.Model.Order, order) {
		t.Errorf("the order after one that was refused = %+v, %v", read.Model, err)
	}
	if saved, err := c.PostPreferencesEnvironmentOrder(ctx, dockhand.PostPreferencesEnvironmentOrderRequest{Order: []int{}}); err != nil || len(saved.Model.Order) != 0 {
		t.Errorf("an empty order of environments = %+v, %v", saved.Model, err)
	}
	if _, err := c.PostPreferencesEnvironmentOrder(ctx, dockhand.PostPreferencesEnvironmentOrderRequest{Order: order}); err != nil {
		t.Fatal(err)
	}
	if forgotten, err := c.DeletePreferencesEnvironmentOrder(ctx); err != nil || len(forgotten.Model.Order) != 0 {
		t.Errorf("the order of environments forgotten = %+v, %v", forgotten.Model, err)
	}
	if read, err := c.GetPreferencesEnvironmentOrder(ctx); err != nil || len(read.Model.Order) != 0 {
		t.Errorf("the order of environments once forgotten = %+v, %v", read.Model, err)
	}

	// the order of tags is the same, for tags there are none of too
	tags := []int{nobody, nobody + 1}
	if saved, err := c.PostPreferencesTagOrder(ctx, dockhand.PostPreferencesTagOrderRequest{Order: tags}); err != nil || !slices.Equal(saved.Model.Order, tags) {
		t.Fatalf("an order of tags saved = %+v, %v", saved.Model, err)
	}
	if _, err := c.PostPreferencesTagOrder(ctx, dockhand.PostPreferencesTagOrderRequest{Order: []int{nobody, nobody}}); client.StatusCode(err) != http.StatusBadRequest {
		t.Errorf("a tag twice in the order = %v, want a 400", err)
	}
	if read, err := c.GetPreferencesTagOrder(ctx); err != nil || !slices.Equal(read.Model.Order, tags) {
		t.Errorf("the order of tags read back = %+v, %v", read.Model, err)
	}
	if forgotten, err := c.DeletePreferencesTagOrder(ctx); err != nil || len(forgotten.Model.Order) != 0 {
		t.Errorf("the order of tags forgotten = %+v, %v", forgotten.Model, err)
	}

	// the sidebar takes any text for a page, and needs both of its lists: one left out is refused
	pages, hidden := []string{"it-page-b", "it-page-a"}, []string{"it-page-c"}
	saved, err := c.PostPreferencesSidebar(ctx, dockhand.PostPreferencesSidebarRequest{Order: pages, Hidden: hidden})
	if err != nil || saved.Model.Preferences == nil || !slices.Equal(saved.Model.Preferences.Order, pages) || !slices.Equal(saved.Model.Preferences.Hidden, hidden) {
		t.Fatalf("a sidebar saved = %+v, %v", saved.Model.Preferences, err)
	}
	if _, err := c.PostPreferencesSidebar(ctx, dockhand.PostPreferencesSidebarRequest{Order: pages}); client.StatusCode(err) != http.StatusBadRequest || !strings.Contains(err.Error(), "hidden must be an array") {
		t.Errorf("a sidebar with no list of what is hidden = %v, want a 400", err)
	}
	if read, err := c.GetPreferencesSidebar(ctx); err != nil || read.Model.Preferences == nil || !slices.Equal(read.Model.Preferences.Order, pages) || !slices.Equal(read.Model.Preferences.Hidden, hidden) {
		t.Errorf("the sidebar read back = %+v, %v", read.Model.Preferences, err)
	}
	if forgotten, err := c.DeletePreferencesSidebar(ctx); err != nil || forgotten.Model.Preferences == nil || len(forgotten.Model.Preferences.Order) != 0 || len(forgotten.Model.Preferences.Hidden) != 0 {
		t.Errorf("the sidebar forgotten = %+v, %v", forgotten.Model.Preferences, err)
	}

	// the columns of a grid are kept under any id for the grid and for a column, each answer giving every grid; one grid is forgotten by its id and
	// the rest stay, and all of them with no id
	const one, two = "it-grid-one", "it-grid-two"
	columns := []dockhand.PostPreferencesGridRequestColumns{{Id: "it-b", Visible: new(false)}, {Id: "it-a", Visible: new(true)}}
	if _, err := c.PostPreferencesGrid(ctx, dockhand.PostPreferencesGridRequest{GridId: one, Columns: columns}); err != nil {
		t.Fatal(err)
	}
	both, err := c.PostPreferencesGrid(ctx, dockhand.PostPreferencesGridRequest{GridId: two, Columns: columns[:1]})
	if err != nil || len(both.Model.Preferences[one].Columns) != 2 || len(both.Model.Preferences[two].Columns) != 1 {
		t.Fatalf("two grids saved = %+v, %v", both.Model.Preferences, err)
	}
	if _, err := c.PostPreferencesGrid(ctx, dockhand.PostPreferencesGridRequest{GridId: one}); client.StatusCode(err) != http.StatusBadRequest || !strings.Contains(err.Error(), "columns array is required") {
		t.Errorf("a grid with no columns = %v, want a 400", err)
	}
	read, err := c.GetPreferencesGrid(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := read.Model.Preferences[one].Columns; len(got) != 2 || got[0].Id != "it-b" || yes(got[0].Visible) || got[1].Id != "it-a" || !yes(got[1].Visible) {
		t.Errorf("the columns of a grid read back = %+v", got)
	}
	if left, err := c.DeletePreferencesGrid(ctx, dockhand.DeletePreferencesGridOperationOptions{GridId: one}); err != nil || len(left.Model.Preferences[one].Columns) != 0 || len(left.Model.Preferences[two].Columns) != 1 {
		t.Errorf("one grid forgotten = %+v, %v", left.Model.Preferences, err)
	}
	if left, err := c.DeletePreferencesGrid(ctx, dockhand.DeletePreferencesGridOperationOptions{}); err != nil || len(left.Model.Preferences) != 0 {
		t.Errorf("every grid forgotten = %+v, %v", left.Model.Preferences, err)
	}
}

// How the dashboard is laid out and the pages the web interface opens on are set, read back and put back: that the dashboard is changed a field at a
// time and takes any text for a layout and any list for its tiles, that a page is one of those Dockhand has, that a page is forgotten with a null,
// and that a user's own pages are not a token's to set.
func TestDashboardLayoutAndOpeningPagesSetAndForgotten(t *testing.T) {
	c := dock(t)
	ctx := t.Context()
	alpha := world.Env(testenv.Alpha)

	dashboardBefore, err := c.GetDashboardPreferences(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pagesBefore, err := c.GetSettingsNavigation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pagesBefore.Model.Effective == nil || pagesBefore.Model.Global == nil {
		t.Fatalf("the pages were answered without the ones in effect or the ones set for everyone: %+v", pagesBefore.Model)
	}
	t.Cleanup(func() {
		after := noCancel(t)
		if _, err := c.PostDashboardPreferences(after, dockhand.PostDashboardPreferencesRequest{Locked: new(yes(dashboardBefore.Model.Locked)), ViewMode: dashboardBefore.Model.ViewMode, GridLayout: append([]map[string]any{}, dashboardBefore.Model.GridLayout...)}); err != nil {
			t.Errorf("putting the dashboard back: %v", err)
		}
		was := pagesBefore.Model.Global
		if _, err := c.ForgetNavigationPages(after, was.LandingPage == "", was.EnvClickPage == ""); err != nil {
			t.Errorf("forgetting the pages: %v", err)
		}
		if was.LandingPage != "" || was.EnvClickPage != "" {
			if _, err := c.PutSettingsNavigation(after, dockhand.PutSettingsNavigationRequest{LandingPage: was.LandingPage, EnvClickPage: was.EnvClickPage}, dockhand.PutSettingsNavigationOperationOptions{Scope: "global"}); err != nil {
				t.Errorf("putting the pages back: %v", err)
			}
		}
	})

	// one field at a time, the others kept; any text is a layout, and any list of records its tiles
	locked, err := c.PostDashboardPreferences(ctx, dockhand.PostDashboardPreferencesRequest{Locked: new(true), ViewMode: "list"})
	if err != nil || !yes(locked.Model.Locked) || locked.Model.ViewMode != "list" {
		t.Fatalf("a dashboard locked and listed = %+v, %v", locked.Model, err)
	}
	tiles := []map[string]any{{"id": alpha, "x": 1, "y": 0, "w": 1, "h": 2}, {"id": 987654, "x": 0, "y": 0, "w": 2, "h": 2}}
	placed, err := c.PostDashboardPreferences(ctx, dockhand.PostDashboardPreferencesRequest{GridLayout: tiles})
	if err != nil || !yes(placed.Model.Locked) || placed.Model.ViewMode != "list" || len(placed.Model.GridLayout) != 2 {
		t.Fatalf("tiles placed, the rest kept = %+v, %v", placed.Model, err)
	}
	if odd, err := c.PostDashboardPreferences(ctx, dockhand.PostDashboardPreferencesRequest{ViewMode: "it-heap"}); err != nil || odd.Model.ViewMode != "it-heap" || !yes(odd.Model.Locked) {
		t.Errorf("a layout there is none of = %+v, %v, want it saved all the same", odd.Model, err)
	}
	read, err := c.GetDashboardPreferences(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// the tiles come back as they went, the one of an environment there is none of with them
	sent, _ := json.Marshal(tiles)
	got, _ := json.Marshal(read.Model.GridLayout)
	if !bytes.Equal(got, sent) || read.Model.ViewMode != "it-heap" || !yes(read.Model.Locked) {
		t.Errorf("the dashboard read back = %s, %+v, want the tiles %s", got, read.Model, sent)
	}
	if none, err := c.PostDashboardPreferences(ctx, dockhand.PostDashboardPreferencesRequest{Locked: new(false), ViewMode: "grid", GridLayout: []map[string]any{}}); err != nil || yes(none.Model.Locked) || len(none.Model.GridLayout) != 0 {
		t.Errorf("the dashboard as it comes = %+v, %v", none.Model, err)
	}

	// the two pages, for everyone: set together or one at a time, with no scope named meaning everyone's
	everyone := dockhand.PutSettingsNavigationOperationOptions{Scope: "global"}
	set, err := c.PutSettingsNavigation(ctx, dockhand.PutSettingsNavigationRequest{LandingPage: "stacks", EnvClickPage: "logs"}, everyone)
	if err != nil || !yes(set.Model.Success) || set.Model.Global == nil || set.Model.Global.LandingPage != "stacks" || set.Model.Global.EnvClickPage != "logs" {
		t.Fatalf("the two pages set = %+v, %v", set.Model, err)
	}
	if one, err := c.PutSettingsNavigation(ctx, dockhand.PutSettingsNavigationRequest{LandingPage: "images"}, dockhand.PutSettingsNavigationOperationOptions{}); err != nil || one.Model.Global == nil || one.Model.Global.LandingPage != "images" || one.Model.Global.EnvClickPage != "logs" {
		t.Errorf("one page set with no scope named = %+v, %v", one.Model, err)
	}
	now, err := c.GetSettingsNavigation(ctx)
	if err != nil || now.Model.Effective == nil || now.Model.Global == nil {
		t.Fatalf("the pages read back = %+v, %v", now.Model, err)
	}
	if now.Model.Global.LandingPage != "images" || now.Model.Global.EnvClickPage != "logs" || now.Model.Effective.LandingPage != "images" || now.Model.Effective.EnvClickPage != "logs" || now.Model.User != nil {
		t.Errorf("the pages read back = set %+v, in effect %+v, the user's %+v", now.Model.Global, now.Model.Effective, now.Model.User)
	}
	// a page there is none of is refused, and the dashboard is no page a click on an environment opens
	if _, err := c.PutSettingsNavigation(ctx, dockhand.PutSettingsNavigationRequest{LandingPage: "it-nowhere"}, everyone); client.StatusCode(err) != http.StatusBadRequest || !strings.Contains(err.Error(), "Invalid landingPage") {
		t.Errorf("an opening page there is none of = %v, want a 400", err)
	}
	if _, err := c.PutSettingsNavigation(ctx, dockhand.PutSettingsNavigationRequest{EnvClickPage: "dashboard"}, everyone); client.StatusCode(err) != http.StatusBadRequest || !strings.Contains(err.Error(), "Invalid envClickPage") {
		t.Errorf("the dashboard for a click on an environment = %v, want a 400", err)
	}
	// every page Dockhand has is taken for the opening page
	for _, page := range []string{"dashboard", "containers", "stacks", "logs", "images", "volumes", "networks", "schedules", "activity", "terminal"} {
		if _, err := c.PutSettingsNavigation(ctx, dockhand.PutSettingsNavigationRequest{LandingPage: page}, everyone); err != nil {
			t.Errorf("the opening page %s = %v", page, err)
		}
		if page == "dashboard" {
			continue
		}
		if _, err := c.PutSettingsNavigation(ctx, dockhand.PutSettingsNavigationRequest{EnvClickPage: page}, everyone); err != nil {
			t.Errorf("the page %s for a click on an environment = %v", page, err)
		}
	}

	// a user's own pages are theirs to set signed in: with nobody signed in there are none, and a token is not a session
	_, err = c.PutSettingsNavigation(ctx, dockhand.PutSettingsNavigationRequest{LandingPage: "logs"}, dockhand.PutSettingsNavigationOperationOptions{Scope: "user"})
	want := map[testenv.Stage]int{testenv.StageOpen: http.StatusBadRequest, testenv.StageLogin: http.StatusUnauthorized}[world.Stage]
	if client.StatusCode(err) != want {
		t.Errorf("a user's own page set in the %s stage = %v, want a %d", world.Stage, err, want)
	}

	// a page is forgotten with a null, one at a time or both, and Dockhand's own is in effect again
	if forgot, err := c.ForgetNavigationPages(ctx, true, false); err != nil || forgot.Model.Global == nil || forgot.Model.Global.LandingPage != "" || forgot.Model.Global.EnvClickPage != "terminal" {
		t.Errorf("the opening page forgotten = %+v, %v", forgot.Model, err)
	}
	if forgot, err := c.ForgetNavigationPages(ctx, true, true); err != nil || forgot.Model.Global == nil || forgot.Model.Global.LandingPage != "" || forgot.Model.Global.EnvClickPage != "" {
		t.Errorf("both pages forgotten = %+v, %v", forgot.Model, err)
	}
	if own, err := c.GetSettingsNavigation(ctx); err != nil || own.Model.Effective == nil || own.Model.Effective.LandingPage != "dashboard" || own.Model.Effective.EnvClickPage != "containers" {
		t.Errorf("the pages with none set = %+v, %v, want Dockhand's own", own.Model, err)
	}
}

// The favourites of an environment's log view are one list of names and groups: what each of the two calls answers, that a name is kept unchecked,
// what a group needs, that Dockhand's own reordering of either kind drops the other, and that the whole list written at once keeps both.
func TestLogFavouritesAreOneListOfNamesAndGroups(t *testing.T) {
	const first, second, group = "it-favourite-one", "it-favourite-two", "it-favourite-group"
	c := dock(t)
	ctx := t.Context()
	alpha, beta := world.Env(testenv.Alpha), world.Env(testenv.Beta)
	here := dockhand.GetPreferencesFavoritesOperationOptions{Env: &alpha}

	// as shows a list the way it is compared: a name as it is, a group as its name and its containers
	as := func(list []any) string {
		raw, err := json.Marshal(list)
		if err != nil {
			t.Fatal(err)
		}

		return string(raw)
	}
	kept := func() string {
		names, err := c.GetPreferencesFavorites(ctx, here)
		if err != nil {
			t.Fatal(err)
		}
		groups, err := c.GetPreferencesFavoriteGroups(ctx, dockhand.GetPreferencesFavoriteGroupsOperationOptions{Env: &alpha})
		if err != nil {
			t.Fatal(err)
		}
		// the call for names and the call for groups answer the same list, whole
		if as(names.Model.Favorites) != as(groups.Model.Groups) {
			t.Errorf("the favourites = %s and the groups = %s, want the one list from both", as(names.Model.Favorites), as(groups.Model.Groups))
		}

		return as(names.Model.Favorites)
	}
	before, err := c.GetPreferencesFavorites(ctx, here)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := c.SetLogFavorites(noCancel(t), alpha, append([]any{}, before.Model.Favorites...)); err != nil {
			t.Errorf("putting the favourites of alpha back: %v", err)
		}
	})
	if _, err := c.SetLogFavorites(ctx, alpha, []any{}); err != nil {
		t.Fatal(err)
	}

	// a name is kept whether or not a container has it, once however often it is added
	for range 2 {
		if _, err := c.PostPreferencesFavorites(ctx, dockhand.PostPreferencesFavoritesRequest{Action: "add", EnvironmentId: alpha, ContainerName: first}); err != nil {
			t.Fatal(err)
		}
	}
	if got := kept(); got != `["`+first+`"]` {
		t.Fatalf("one name added twice = %s", got)
	}
	// a group goes into the same list, after it; it needs a container, and a name no other group has
	added, err := c.PostPreferencesFavoriteGroups(ctx, dockhand.PostPreferencesFavoriteGroupsRequest{Action: "add", EnvironmentId: alpha, Name: group, Containers: []string{first, second}})
	if err != nil {
		t.Fatal(err)
	}
	mixed := `["` + first + `",{"containers":["` + first + `","` + second + `"],"name":"` + group + `"}]`
	if as(added.Model.Groups) != mixed || kept() != mixed {
		t.Fatalf("a group added = %s, kept %s, want %s", as(added.Model.Groups), kept(), mixed)
	}
	if _, err := c.PostPreferencesFavoriteGroups(ctx, dockhand.PostPreferencesFavoriteGroupsRequest{Action: "add", EnvironmentId: alpha, Name: group, Containers: []string{second}}); client.StatusCode(err) != http.StatusBadRequest {
		t.Errorf("a second group by the same name = %v, want a 400", err)
	}
	if _, err := c.PostPreferencesFavoriteGroups(ctx, dockhand.PostPreferencesFavoriteGroupsRequest{Action: "add", EnvironmentId: alpha, Name: group + "-empty"}); client.StatusCode(err) != http.StatusBadRequest {
		t.Errorf("a group of no containers = %v, want a 400", err)
	}
	if _, err := c.PostPreferencesFavoriteGroups(ctx, dockhand.PostPreferencesFavoriteGroupsRequest{Action: "update", EnvironmentId: alpha, Name: group + "-nope", NewName: "x"}); client.StatusCode(err) != http.StatusNotFound {
		t.Errorf("changing a group there is none of = %v, want a 404", err)
	}
	// it is renamed and given other containers in place, and the name beside it stays
	if _, err := c.PostPreferencesFavoriteGroups(ctx, dockhand.PostPreferencesFavoriteGroupsRequest{Action: "update", EnvironmentId: alpha, Name: group, NewName: group + "-b", Containers: []string{second}}); err != nil {
		t.Fatal(err)
	}
	renamed := `["` + first + `",{"containers":["` + second + `"],"name":"` + group + `-b"}]`
	if got := kept(); got != renamed {
		t.Errorf("a group renamed = %s, want %s", got, renamed)
	}
	// another environment keeps a list of its own
	if other, err := c.GetPreferencesFavorites(ctx, dockhand.GetPreferencesFavoritesOperationOptions{Env: &beta}); err != nil || slices.Contains(other.Model.Favorites, any(first)) {
		t.Errorf("the favourites of another environment = %+v, %v", other.Model, err)
	}

	// Dockhand's own reordering of names replaces the whole list, so the group is gone; and of groups, so the names are
	if _, err := c.PostPreferencesFavorites(ctx, dockhand.PostPreferencesFavoritesRequest{Action: "reorder", EnvironmentId: alpha, Favorites: []string{second, first}}); err != nil {
		t.Fatal(err)
	}
	if got := kept(); got != `["`+second+`","`+first+`"]` {
		t.Errorf("after the names were reordered = %s, want the names alone", got)
	}
	if _, err := c.PostPreferencesFavoriteGroups(ctx, dockhand.PostPreferencesFavoriteGroupsRequest{Action: "reorder", EnvironmentId: alpha, Groups: []dockhand.PostPreferencesFavoriteGroupsRequestGroups{{Name: group, Containers: []string{first}}}}); err != nil {
		t.Fatal(err)
	}
	if got := kept(); got != `[{"containers":["`+first+`"],"name":"`+group+`"}]` {
		t.Errorf("after the groups were reordered = %s, want the group alone", got)
	}

	// the whole list written at once keeps both kinds, in the order given
	whole := []any{map[string]any{"name": group, "containers": []string{first}}, second, first}
	set, err := c.SetLogFavorites(ctx, alpha, whole)
	if want := `[{"containers":["` + first + `"],"name":"` + group + `"},"` + second + `","` + first + `"]`; err != nil || as(set.Model.Favorites) != want || kept() != want {
		t.Fatalf("the whole list set = %s, %v, kept %s, want %s", as(set.Model.Favorites), err, kept(), want)
	}
	// a name and a group are each taken out by name, the rest staying where it was
	if _, err := c.PostPreferencesFavorites(ctx, dockhand.PostPreferencesFavoritesRequest{Action: "remove", EnvironmentId: alpha, ContainerName: second}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PostPreferencesFavoriteGroups(ctx, dockhand.PostPreferencesFavoriteGroupsRequest{Action: "remove", EnvironmentId: alpha, Name: group}); err != nil {
		t.Fatal(err)
	}
	if got := kept(); got != `["`+first+`"]` {
		t.Errorf("after a name and a group were taken out = %s", got)
	}

	// an environment there is none of cannot keep any, and one has to be named to read them
	if _, err := c.SetLogFavorites(ctx, 987654, []any{first}); client.StatusCode(err) != http.StatusInternalServerError {
		t.Errorf("favourites for an environment there is none of = %v, want a 500", err)
	}
	if _, err := c.GetPreferencesFavorites(ctx, dockhand.GetPreferencesFavoritesOperationOptions{}); client.StatusCode(err) != http.StatusBadRequest {
		t.Errorf("favourites of no environment = %v, want a 400", err)
	}
	if cleared, err := c.SetLogFavorites(ctx, alpha, []any{}); err != nil || len(cleared.Model.Favorites) != 0 || kept() != `[]` {
		t.Errorf("no favourites kept = %+v, %v", cleared.Model, err)
	}
}

// Which of what the web interface remembers is one user's own and which is everyone's, seen from a second user signed in: the orders, the sidebar,
// the columns of a grid and the hiding of Dockhand's own jobs are each user's, and the dashboard, the favourites and the pages are shared.
func TestWhichPreferencesAreOneUsersOwn(t *testing.T) {
	const other, password = "it-preferences-user", "it-preferences-password"
	c := dock(t)
	ctx := t.Context()
	if world.Stage != testenv.StageLogin {
		// with the login off there are no users: the one set of preferences is whoever asks's
		if _, err := c.GetProfilePreferences(ctx); client.StatusCode(err) != http.StatusBadRequest || !strings.Contains(err.Error(), "Authentication is not enabled") {
			t.Errorf("a user's own profile with the login off = %v, want a 400", err)
		}

		return
	}
	alpha := world.Env(testenv.Alpha)

	if left := userNamed(t, c, other); left != nil {
		if _, err := c.DeleteUsersById(ctx, left.Id, dockhand.DeleteUsersByIdOperationOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	made, err := c.PostUsers(ctx, dockhand.PostUsersRequest{Username: other, Password: password})
	if err != nil {
		t.Fatal(err)
	}
	dashboardBefore, err := c.GetDashboardPreferences(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		after := noCancel(t)
		if _, err := c.DeleteUsersById(after, made.Model.Id, dockhand.DeleteUsersByIdOperationOptions{}); err != nil {
			t.Errorf("removing the second user: %v", err)
		}
		_, _ = c.DeletePreferencesEnvironmentOrder(after)
		_, _ = c.DeletePreferencesSidebar(after)
		_, _ = c.DeletePreferencesGrid(after, dockhand.DeletePreferencesGridOperationOptions{GridId: "it-own-grid"})
		_, _ = c.PutSchedulesSettings(after, dockhand.PutSchedulesSettingsRequest{HideSystemJobs: new(false)})
		_, _ = c.PostDashboardPreferences(after, dockhand.PostDashboardPreferencesRequest{Locked: new(yes(dashboardBefore.Model.Locked))})
		_, _ = c.SetLogFavorites(after, alpha, []any{})
		_, _ = c.ForgetNavigationPages(after, true, true)
	})
	second, err := signedIn(t, other, password)
	if err != nil {
		t.Fatal(err)
	}

	// what the token's user saves for itself, the second user does not see
	if _, err := c.PostPreferencesEnvironmentOrder(ctx, dockhand.PostPreferencesEnvironmentOrderRequest{Order: []int{alpha}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PostPreferencesTagOrder(ctx, dockhand.PostPreferencesTagOrderRequest{Order: []int{987654}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PostPreferencesSidebar(ctx, dockhand.PostPreferencesSidebarRequest{Order: []string{"it-own-page"}, Hidden: []string{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PostPreferencesGrid(ctx, dockhand.PostPreferencesGridRequest{GridId: "it-own-grid", Columns: []dockhand.PostPreferencesGridRequestColumns{{Id: "it-a", Visible: new(true)}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutSchedulesSettings(ctx, dockhand.PutSchedulesSettingsRequest{HideSystemJobs: new(true)}); err != nil {
		t.Fatal(err)
	}
	if got, err := second.GetPreferencesEnvironmentOrder(ctx); err != nil || len(got.Model.Order) != 0 {
		t.Errorf("another user's order of environments = %+v, %v, want none", got.Model, err)
	}
	if got, err := second.GetPreferencesTagOrder(ctx); err != nil || len(got.Model.Order) != 0 {
		t.Errorf("another user's order of tags = %+v, %v, want none", got.Model, err)
	}
	if got, err := second.GetPreferencesSidebar(ctx); err != nil || got.Model.Preferences == nil || len(got.Model.Preferences.Order) != 0 {
		t.Errorf("another user's sidebar = %+v, %v, want none", got.Model.Preferences, err)
	}
	if got, err := second.GetPreferencesGrid(ctx); err != nil || len(got.Model.Preferences) != 0 {
		t.Errorf("another user's grids = %+v, %v, want none", got.Model.Preferences, err)
	}
	if got, err := second.GetSchedulesSettings(ctx); err != nil || yes(got.Model.HideSystemJobs) {
		t.Errorf("whether another user hides Dockhand's own jobs = %+v, %v, want not", got.Model, err)
	}
	if _, err := c.DeletePreferencesTagOrder(ctx); err != nil {
		t.Error(err)
	}

	// the dashboard, the favourites and the pages set for everyone are the same whoever asks
	if _, err := c.PostDashboardPreferences(ctx, dockhand.PostDashboardPreferencesRequest{Locked: new(!yes(dashboardBefore.Model.Locked))}); err != nil {
		t.Fatal(err)
	}
	if got, err := second.GetDashboardPreferences(ctx); err != nil || yes(got.Model.Locked) == yes(dashboardBefore.Model.Locked) {
		t.Errorf("the dashboard as another user sees it = %+v, %v, want what the first user set", got.Model, err)
	}
	if _, err := c.SetLogFavorites(ctx, alpha, []any{"it-shared-favourite"}); err != nil {
		t.Fatal(err)
	}
	if got, err := second.GetPreferencesFavorites(ctx, dockhand.GetPreferencesFavoritesOperationOptions{Env: &alpha}); err != nil || len(got.Model.Favorites) != 1 || got.Model.Favorites[0] != "it-shared-favourite" {
		t.Errorf("the favourites as another user sees them = %+v, %v, want what the first user set", got.Model, err)
	}
	if _, err := c.PutSettingsNavigation(ctx, dockhand.PutSettingsNavigationRequest{LandingPage: "stacks"}, dockhand.PutSettingsNavigationOperationOptions{Scope: "global"}); err != nil {
		t.Fatal(err)
	}
	if got, err := second.GetSettingsNavigation(ctx); err != nil || got.Model.Effective == nil || got.Model.Effective.LandingPage != "stacks" {
		t.Errorf("the opening page as another user gets it = %+v, %v, want the one set for everyone", got.Model, err)
	}

	// signed in, a user sets pages of their own, which are in effect for them alone; and reads a profile a token cannot
	own, err := second.PutSettingsNavigation(ctx, dockhand.PutSettingsNavigationRequest{LandingPage: "logs"}, dockhand.PutSettingsNavigationOperationOptions{Scope: "user"})
	if err != nil || own.Model.User == nil || own.Model.User.LandingPage != "logs" {
		t.Fatalf("a user's own opening page = %+v, %v", own.Model, err)
	}
	if got, err := second.GetSettingsNavigation(ctx); err != nil || got.Model.Effective == nil || got.Model.Effective.LandingPage != "logs" || got.Model.User == nil || got.Model.User.LandingPage != "logs" {
		t.Errorf("the pages of the user who chose one = %+v, %v", got.Model, err)
	}
	if got, err := c.GetSettingsNavigation(ctx); err != nil || got.Model.Effective == nil || got.Model.Effective.LandingPage != "stacks" {
		t.Errorf("the pages of a user who chose none = %+v, %v, want the one set for everyone", got.Model, err)
	}
	if _, err := second.GetProfilePreferences(ctx); err != nil {
		t.Errorf("a user's own profile preferences, signed in = %v", err)
	}
	if _, err := c.GetProfilePreferences(ctx); client.StatusCode(err) != http.StatusUnauthorized {
		t.Errorf("a profile asked for with a token = %v, want a 401", err)
	}
}
