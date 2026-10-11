package tools

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The pages a 1.0.51 Dockhand takes for where its web interface opens and for what a click on an environment opens, and the two ways its dashboard is
// laid out. It refuses any other page, and saves any text as a layout.
var (
	landingPages   = []string{"dashboard", "containers", "stacks", "logs", "images", "volumes", "networks", "schedules", "activity", "terminal"}
	envClickPages  = []string{"containers", "stacks", "logs", "images", "volumes", "networks", "schedules", "activity", "terminal"}
	dashboardViews = []string{"grid", "list"}
)

// everyGrid is what grid_reset takes for the columns of every grid at once.
const everyGrid = "all"

// preferencesAre is what the preference tools say of whose these are.
const preferencesAre = "These are what Dockhand's web interface remembers. The order of environments and of tags, the sidebar, the columns of a grid and whether Dockhand's own jobs are hidden are each user's own, here the user this server signs in as; how the dashboard is laid out and the two pages are everyone's. Nothing on a Docker host is changed by any of them"

// gridColumn is one column of a grid of the web interface.
type gridColumn struct {
	ID      string `json:"id"      jsonschema:"the column, by the id the web interface has for it"`
	Visible bool   `json:"visible" jsonschema:"whether it is shown"`
}

// dashboardTile is where the dashboard shows one environment's tile, in the cells of its grid.
type dashboardTile struct {
	Env string `json:"env" jsonschema:"the environment the tile is of, by name"`
	X   int    `json:"x"   jsonschema:"the column its left edge is in, the first being 0"`
	Y   int    `json:"y"   jsonschema:"the row its top edge is in, the first being 0"`
	W   int    `json:"w"   jsonschema:"how many columns wide it is"`
	H   int    `json:"h"   jsonschema:"how many rows tall it is"`
}

// preferences is everything the web interface remembers that a token may read, in words.
type preferences struct {
	EnvironmentOrder []string                `json:"environment_order"            jsonschema:"the environments in the order saved, by name; empty for none saved, which lists them by name"`
	TagOrder         []string                `json:"tag_order"                    jsonschema:"the tags in the order saved, by name; empty for none saved"`
	SidebarOrder     []string                `json:"sidebar_order"                jsonschema:"the pages of the sidebar in the order saved, by the id the web interface has for each; empty for its own order"`
	SidebarHidden    []string                `json:"sidebar_hidden"               jsonschema:"the pages left out of the sidebar"`
	Grids            map[string][]gridColumn `json:"grids,omitempty"              jsonschema:"the columns saved for each grid, by the grid's id; a grid not here is shown as it comes"`
	HideSystemJobs   bool                    `json:"hide_system_jobs"             jsonschema:"whether the schedules page leaves out what Dockhand does for itself"`
	DashboardLocked  bool                    `json:"dashboard_locked"             jsonschema:"whether the tiles of the dashboard are kept from being moved"`
	DashboardView    string                  `json:"dashboard_view"               jsonschema:"how the dashboard is laid out: grid or list"`
	DashboardTiles   []dashboardTile         `json:"dashboard_tiles"              jsonschema:"where the tile of each environment is on the dashboard, as moving them in the web interface saves it; empty for the page's own places, one beside the next"`
	LandingPage      string                  `json:"landing_page"                 jsonschema:"the page the web interface opens on for whoever chose none of their own"`
	EnvClickPage     string                  `json:"env_click_page"               jsonschema:"the page a click on an environment opens for whoever chose none of their own"`
	LandingChosen    string                  `json:"landing_page_set,omitempty"   jsonschema:"the opening page that was set for everyone; absent when none was, and landing_page is Dockhand's own"`
	EnvClickChosen   string                  `json:"env_click_page_set,omitempty" jsonschema:"the page for a click on an environment that was set for everyone; absent when none was"`
}

// remembered reads everything the web interface remembers, with the environments and the tags there are for the names of the two orders.
func (r *registry) remembered(ctx context.Context) (*preferences, []environment, []dockhand.GetTagsResponseTags, error) {
	fail := func(what string, err error) (*preferences, []environment, []dockhand.GetTagsResponseTags, error) {
		return nil, nil, nil, fmt.Errorf("%s could not be read: %w", what, refusal(err))
	}
	envs, err := r.environments(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	tags, err := r.tags(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	out := &preferences{EnvironmentOrder: []string{}, TagOrder: []string{}, SidebarOrder: []string{}, SidebarHidden: []string{}, DashboardTiles: []dashboardTile{}}

	envOrder, err := r.dock.GetPreferencesEnvironmentOrder(ctx)
	if err != nil {
		return fail("the order of environments", err)
	}
	for _, id := range envOrder.Model.Order {
		i := slices.IndexFunc(envs, func(e environment) bool { return e.ID == id })
		out.EnvironmentOrder = append(out.EnvironmentOrder, nameOr(i, id, func(i int) string { return envs[i].Name }))
	}
	tagOrder, err := r.dock.GetPreferencesTagOrder(ctx)
	if err != nil {
		return fail("the order of tags", err)
	}
	for _, id := range tagOrder.Model.Order {
		i := slices.IndexFunc(tags, func(t dockhand.GetTagsResponseTags) bool { return t.Id == id })
		out.TagOrder = append(out.TagOrder, nameOr(i, id, func(i int) string { return tags[i].Name }))
	}
	sidebar, err := r.dock.GetPreferencesSidebar(ctx)
	if err != nil {
		return fail("the sidebar", err)
	}
	if kept := sidebar.Model.Preferences; kept != nil {
		out.SidebarOrder, out.SidebarHidden = append(out.SidebarOrder, kept.Order...), append(out.SidebarHidden, kept.Hidden...)
	}
	grids, err := r.dock.GetPreferencesGrid(ctx)
	if err != nil {
		return fail("the columns of the grids", err)
	}
	for id, grid := range grids.Model.Preferences {
		columns := make([]gridColumn, 0, len(grid.Columns))
		for _, c := range grid.Columns {
			columns = append(columns, gridColumn{ID: c.Id, Visible: yes(c.Visible)})
		}
		if out.Grids == nil {
			out.Grids = map[string][]gridColumn{}
		}
		out.Grids[id] = columns
	}
	jobs, err := r.dock.GetSchedulesSettings(ctx)
	if err != nil {
		return fail("whether Dockhand's own jobs are hidden", err)
	}
	out.HideSystemJobs = yes(jobs.Model.HideSystemJobs)
	dashboard, err := r.dock.GetDashboardPreferences(ctx)
	if err != nil {
		return fail("the dashboard", err)
	}
	out.DashboardLocked, out.DashboardView = yes(dashboard.Model.Locked), dashboard.Model.ViewMode
	for _, tile := range dashboard.Model.GridLayout {
		id := numberOf(tile["id"])
		i := slices.IndexFunc(envs, func(e environment) bool { return e.ID == id })
		out.DashboardTiles = append(out.DashboardTiles, dashboardTile{Env: nameOr(i, id, func(i int) string { return envs[i].Name }), X: numberOf(tile["x"]), Y: numberOf(tile["y"]), W: numberOf(tile["w"]), H: numberOf(tile["h"])})
	}
	pages, err := r.dock.GetSettingsNavigation(ctx)
	if err != nil {
		return fail("the pages", err)
	}
	if now := pages.Model.Effective; now != nil {
		out.LandingPage, out.EnvClickPage = now.LandingPage, now.EnvClickPage
	}
	if chosen := pages.Model.Global; chosen != nil {
		out.LandingChosen, out.EnvClickChosen = chosen.LandingPage, chosen.EnvClickPage
	}

	return out, envs, tags, nil
}

// nameOr is the name of the thing at a place in a list, or its number written as one when it is in no list any more: Dockhand keeps an order that
// names what was since removed.
func nameOr(i, id int, name func(int) string) string {
	if i < 0 {
		return "#" + strconv.Itoa(id)
	}

	return name(i)
}

// favoriteEntry is one thing kept for an environment's log view: a container by its name, or a group of them under a name.
type favoriteEntry struct {
	Container  string   `json:"container,omitempty"  jsonschema:"a container's name, for an entry that is one container"`
	Group      string   `json:"group,omitempty"      jsonschema:"a group's name, for an entry that is a group of containers"`
	Containers []string `json:"containers,omitempty" jsonschema:"the containers of the group, by name"`
}

// favoritesOf is everything kept for an environment's log view, in order. Dockhand keeps containers and groups in the one list and answers it whole
// whichever is asked for.
func (r *registry) favoritesOf(ctx context.Context, env environment) ([]favoriteEntry, error) {
	kept, err := r.dock.GetPreferencesFavorites(ctx, dockhand.GetPreferencesFavoritesOperationOptions{Env: &env.ID})
	if err != nil {
		return nil, refusal(err)
	}
	entries := make([]favoriteEntry, 0, len(kept.Model.Favorites))
	for _, one := range kept.Model.Favorites {
		switch one := one.(type) {
		case string:
			entries = append(entries, favoriteEntry{Container: one})
		case map[string]any:
			group := favoriteEntry{Containers: []string{}}
			if name, is := one["name"].(string); is {
				group.Group = name
			}
			if members, is := one["containers"].([]any); is {
				for _, member := range members {
					if name, is := member.(string); is {
						group.Containers = append(group.Containers, name)
					}
				}
			}
			entries = append(entries, group)
		default:
		}
	}

	return entries, nil
}

//nolint:gocyclo // four tools registered in one function, each a handler of its own: the count is theirs added up
func registerPreferenceTools(r *registry) {
	add(r, readTool, &mcp.Tool{
		Name:        "preferences_get",
		Description: "Read what Dockhand's web interface remembers: the order it lists environments and tags in, the order of its sidebar and the pages left out of it, the columns saved for each grid, whether the schedules page hides Dockhand's own jobs, how the dashboard is laid out and where each environment's tile is on it, and the pages it opens on and opens for a click on an environment. " + preferencesAre + ". A user's own choice of the two pages, and their theme and fonts, are kept with their profile, which only they see signed in to the web interface.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, preferences, error) {
		out, _, _, err := r.remembered(ctx)
		if err != nil {
			return nil, preferences{}, err
		}

		return nil, *out, nil
	})

	type tilePlace struct {
		Env any `json:"env" jsonschema:"the environment the tile is of, by name or number"`
		X   int `json:"x"   jsonschema:"the column its left edge is in, the first being 0"`
		Y   int `json:"y"   jsonschema:"the row its top edge is in, the first being 0"`
		W   int `json:"w"   jsonschema:"how many columns wide it is, 1 or more"`
		H   int `json:"h"   jsonschema:"how many rows tall it is, 1 or more; the page's own tiles are 1 wide and 2 tall"`
	}
	type preferencesSetIn struct {
		dryRunArg

		EnvironmentOrder []any        `json:"environment_order,omitempty" jsonschema:"every environment in the order to list them, each by name or number, the first first; an empty list for the order of their names"`
		TagOrder         []any        `json:"tag_order,omitempty"         jsonschema:"every tag in the order to list them, each by name or number; an empty list for the order of their names"`
		SidebarOrder     []string     `json:"sidebar_order,omitempty"     jsonschema:"the pages of the sidebar in the order to show them, by the ids preferences_get gives; an empty list for the sidebar's own order"`
		SidebarHidden    []string     `json:"sidebar_hidden,omitempty"    jsonschema:"the pages to leave out of the sidebar; an empty list to show them all"`
		Grid             string       `json:"grid,omitempty"              jsonschema:"the grid whose columns grid_columns sets, by its id: containers, images, stacks"`
		GridColumns      []gridColumn `json:"grid_columns,omitempty"      jsonschema:"every column of that grid in order, each with whether it is shown"`
		GridReset        string       `json:"grid_reset,omitempty"        jsonschema:"have a grid shown as it comes again, by its id, or all for every grid"`
		HideSystemJobs   *bool        `json:"hide_system_jobs,omitempty"  jsonschema:"true has the schedules page leave out what Dockhand does for itself; false shows it"`
		DashboardLocked  *bool        `json:"dashboard_locked,omitempty"  jsonschema:"true keeps the tiles of the dashboard from being moved"`
		DashboardView    string       `json:"dashboard_view,omitempty"    jsonschema:"how the dashboard is laid out: grid or list"`
		DashboardTiles   []tilePlace  `json:"dashboard_tiles,omitempty"   jsonschema:"where the tile of each environment is on the dashboard, every tile that is to have a place of its own; an empty list for the page's own places"`
		LandingPage      *string      `json:"landing_page,omitempty"      jsonschema:"the page the web interface opens on, for everyone who chose none of their own: dashboard, containers, stacks, logs, images, volumes, networks, schedules, activity or terminal; empty for Dockhand's own"`
		EnvClickPage     *string      `json:"env_click_page,omitempty"    jsonschema:"the page a click on an environment opens, for everyone who chose none of their own: containers, stacks, logs, images, volumes, networks, schedules, activity or terminal; empty for Dockhand's own"`
	}
	type preferencesSetOut struct {
		preferences

		Changes []string `json:"changes"           jsonschema:"what was done, in words; empty when nothing needed doing"`
		DryRun  bool     `json:"dry_run,omitempty" jsonschema:"nothing was done: changes says what would be, and the rest is as it is now"`
		Note    string   `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "preferences_set",
		Description: "Change what Dockhand's web interface remembers: the order it lists environments and tags in, its sidebar, the columns of a grid, whether the schedules page hides Dockhand's own jobs, how the dashboard is laid out and where its tiles are, and the pages it opens on. " + preferencesAre + ". Each thing given is set whole and the rest is kept. Dockhand saves an order or a tile that names an environment or a tag there is none of, any text as a page of the sidebar, a column or a layout, so an environment and a tag are looked up first and a layout and the two pages are checked; the ids of pages and columns are the web interface's own, which preferences_get gives once they were saved. Everything is read back afterwards.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in preferencesSetIn) (*mcp.CallToolResult, preferencesSetOut, error) {
		grid, reset, view := strings.TrimSpace(in.Grid), strings.TrimSpace(in.GridReset), strings.ToLower(strings.TrimSpace(in.DashboardView))
		columns := make([]gridColumn, 0, len(in.GridColumns))
		for _, c := range in.GridColumns {
			columns = append(columns, gridColumn{ID: strings.TrimSpace(c.ID), Visible: c.Visible})
		}
		switch {
		case (grid == "") != (in.GridColumns == nil):
			return nil, preferencesSetOut{}, errors.New("grid and grid_columns go together: the grid's id, and every column of it in order")
		case grid != "" && len(columns) == 0:
			return nil, preferencesSetOut{}, errors.New("grid_columns is empty: a grid is shown as it comes again with grid_reset")
		case slices.ContainsFunc(columns, func(c gridColumn) bool { return c.ID == "" }):
			return nil, preferencesSetOut{}, errors.New("grid_columns has a column with no id: each is the id the web interface has for it")
		case grid != "" && grid == reset:
			return nil, preferencesSetOut{}, fmt.Errorf("the grid %s is in both grid and grid_reset: its columns are set or it is shown as it comes, not both", grid)
		case view != "" && !slices.Contains(dashboardViews, view):
			return nil, preferencesSetOut{}, fmt.Errorf("dashboard_view is %q: it is grid or list. Dockhand would save it all the same", in.DashboardView)
		default:
		}
		page := func(given *string, what string, taken []string) (string, error) {
			if given == nil {
				return "", nil
			}
			wanted := strings.ToLower(strings.TrimSpace(*given))
			if wanted != "" && !slices.Contains(taken, wanted) {
				return "", fmt.Errorf("%s is %q: it is %s or %s, or empty for Dockhand's own", what, *given, strings.Join(taken[:len(taken)-1], ", "), taken[len(taken)-1])
			}

			return wanted, nil
		}
		landing, err := page(in.LandingPage, "landing_page", landingPages)
		if err != nil {
			return nil, preferencesSetOut{}, err
		}
		envClick, err := page(in.EnvClickPage, "env_click_page", envClickPages)
		if err != nil {
			return nil, preferencesSetOut{}, err
		}
		for what, pages := range map[string][]string{"sidebar_order": in.SidebarOrder, "sidebar_hidden": in.SidebarHidden} {
			seen := map[string]bool{}
			for _, id := range pages {
				if strings.TrimSpace(id) == "" || seen[id] {
					return nil, preferencesSetOut{}, fmt.Errorf("%s has %q, which is empty or is there twice: each page is named once, by its id", what, id)
				}
				seen[id] = true
			}
		}

		now, envs, tags, err := r.remembered(ctx)
		if err != nil {
			return nil, preferencesSetOut{}, err
		}
		out := preferencesSetOut{preferences: *now, Changes: []string{}, DryRun: in.DryRun}
		done := func(did, would string) string {
			if in.DryRun {
				return would
			}

			return did
		}
		// each thing in an order is looked up, by name or number, and is there once
		ordered := func(given []any, what string, find func(string) (int, string, bool)) (ids []int, names []string, err error) {
			ids, names = []int{}, []string{}
			for _, one := range given {
				wanted, unnamed := named(one, what)
				if unnamed != nil {
					return nil, nil, unnamed
				}
				id, name, is := find(wanted)
				switch {
				case !is:
					return nil, nil, fmt.Errorf("%s has %q, which is no %s there is: Dockhand would save the order all the same", what+"_order", wanted, what)
				case slices.Contains(ids, id):
					return nil, nil, fmt.Errorf("%s has %s twice: each is in the order once", what+"_order", name)
				default:
					ids, names = append(ids, id), append(names, name)
				}
			}

			return ids, names, nil
		}
		envNamed := func(wanted string) (int, string, bool) {
			i := slices.IndexFunc(envs, func(e environment) bool { return strconv.Itoa(e.ID) == wanted || strings.EqualFold(e.Name, wanted) })
			if i < 0 {
				return 0, "", false
			}

			return envs[i].ID, envs[i].Name, true
		}
		envIDs, envNames, err := ordered(in.EnvironmentOrder, "environment", envNamed)
		if err != nil {
			return nil, out, err
		}
		tiles, layout := make([]dashboardTile, 0, len(in.DashboardTiles)), make([]map[string]any, 0, len(in.DashboardTiles))
		for _, tile := range in.DashboardTiles {
			wanted, unnamed := named(tile.Env, "the env of a tile")
			if unnamed != nil {
				return nil, out, unnamed
			}
			id, name, is := envNamed(wanted)
			switch {
			case !is:
				return nil, out, fmt.Errorf("dashboard_tiles has a tile of %q, which is no environment there is: Dockhand would save it all the same", wanted)
			case slices.ContainsFunc(tiles, func(t dashboardTile) bool { return t.Env == name }):
				return nil, out, fmt.Errorf("dashboard_tiles has the tile of %s twice: an environment has one tile", name)
			case tile.X < 0 || tile.Y < 0 || tile.W < 1 || tile.H < 1:
				return nil, out, fmt.Errorf("the tile of %s is at x %d, y %d and is %d wide and %d tall: a place is 0 or more, and a size 1 or more", name, tile.X, tile.Y, tile.W, tile.H)
			default:
				tiles, layout = append(tiles, dashboardTile{Env: name, X: tile.X, Y: tile.Y, W: tile.W, H: tile.H}), append(layout, map[string]any{"id": id, "x": tile.X, "y": tile.Y, "w": tile.W, "h": tile.H})
			}
		}
		tagIDs, tagNames, err := ordered(in.TagOrder, "tag", func(wanted string) (int, string, bool) {
			tag, missing := tagIn(tags, wanted)
			if missing != nil {
				return 0, "", false
			}

			return tag.Id, tag.Name, true
		})
		if err != nil {
			return nil, out, err
		}

		// what is to be done, each thing that is not so already, with the call that does it
		var steps []func() error
		step := func(did, would string, call func() error) {
			out.Changes = append(out.Changes, done(did, would))
			steps = append(steps, call)
		}
		shown := func(names []string, none string) string { return cmp.Or(strings.Join(names, ", "), none) }
		if in.EnvironmentOrder != nil && !slices.Equal(envNames, now.EnvironmentOrder) {
			step("listed the environments as "+shown(envNames, "their names order them"), "would list the environments as "+shown(envNames, "their names order them"), func() error {
				_, failed := r.dock.PostPreferencesEnvironmentOrder(ctx, dockhand.PostPreferencesEnvironmentOrderRequest{Order: envIDs})

				return failed
			})
		}
		if in.TagOrder != nil && !slices.Equal(tagNames, now.TagOrder) {
			step("listed the tags as "+shown(tagNames, "their names order them"), "would list the tags as "+shown(tagNames, "their names order them"), func() error {
				_, failed := r.dock.PostPreferencesTagOrder(ctx, dockhand.PostPreferencesTagOrderRequest{Order: tagIDs})

				return failed
			})
		}
		sidebarOrder, sidebarHidden := now.SidebarOrder, now.SidebarHidden
		if in.SidebarOrder != nil {
			sidebarOrder = append([]string{}, in.SidebarOrder...)
		}
		if in.SidebarHidden != nil {
			sidebarHidden = append([]string{}, in.SidebarHidden...)
		}
		if !slices.Equal(sidebarOrder, now.SidebarOrder) || !slices.Equal(sidebarHidden, now.SidebarHidden) {
			what := fmt.Sprintf("the sidebar in the order %s, with %s left out", shown(sidebarOrder, "it comes in"), shown(sidebarHidden, "nothing"))
			step("set "+what, "would set "+what, func() error {
				_, failed := r.dock.PostPreferencesSidebar(ctx, dockhand.PostPreferencesSidebarRequest{Order: sidebarOrder, Hidden: sidebarHidden})

				return failed
			})
		}
		if grid != "" && !slices.Equal(columns, now.Grids[grid]) {
			sent := make([]dockhand.PostPreferencesGridRequestColumns, 0, len(columns))
			for _, c := range columns {
				sent = append(sent, dockhand.PostPreferencesGridRequestColumns{Id: c.ID, Visible: &c.Visible})
			}
			step(fmt.Sprintf("set the %s of the grid %s", several(len(sent), "column"), grid), fmt.Sprintf("would set the %s of the grid %s", several(len(sent), "column"), grid), func() error {
				_, failed := r.dock.PostPreferencesGrid(ctx, dockhand.PostPreferencesGridRequest{GridId: grid, Columns: sent})

				return failed
			})
		}
		if _, saved := now.Grids[reset]; reset == everyGrid && len(now.Grids) > 0 || saved {
			options, what := dockhand.DeletePreferencesGridOperationOptions{GridId: reset}, "the grid "+reset+" shown as it comes again"
			if reset == everyGrid {
				options, what = dockhand.DeletePreferencesGridOperationOptions{}, "every grid shown as it comes again"
			}
			step("had "+what, "would have "+what, func() error {
				_, failed := r.dock.DeletePreferencesGrid(ctx, options)

				return failed
			})
		}
		if in.HideSystemJobs != nil && *in.HideSystemJobs != now.HideSystemJobs {
			what := map[bool]string{true: "the schedules page leave out Dockhand's own jobs", false: "the schedules page show Dockhand's own jobs"}[*in.HideSystemJobs]
			step("had "+what, "would have "+what, func() error {
				_, failed := r.dock.PutSchedulesSettings(ctx, dockhand.PutSchedulesSettingsRequest{HideSystemJobs: in.HideSystemJobs})

				return failed
			})
		}
		dashboard := dockhand.PostDashboardPreferencesRequest{}
		var laid []string
		if in.DashboardLocked != nil && *in.DashboardLocked != now.DashboardLocked {
			dashboard.Locked = in.DashboardLocked
			laid = append(laid, map[bool]string{true: "locked", false: "unlocked"}[*in.DashboardLocked])
		}
		if view != "" && view != now.DashboardView {
			dashboard.ViewMode = view
			laid = append(laid, "laid out as a "+view)
		}
		if in.DashboardTiles != nil && !slices.Equal(tiles, now.DashboardTiles) {
			dashboard.GridLayout = layout
			placed := "with " + several(len(tiles), "tile") + " placed"
			if len(tiles) == 0 {
				placed = "with its tiles where the page puts them"
			}
			laid = append(laid, placed)
		}
		if len(laid) > 0 {
			step("had the dashboard "+strings.Join(laid, " and "), "would have the dashboard "+strings.Join(laid, " and "), func() error {
				_, failed := r.dock.PostDashboardPreferences(ctx, dashboard)

				return failed
			})
		}
		pages, forgetLanding, forgetClick := dockhand.PutSettingsNavigationRequest{}, false, false
		var opened []string
		if in.LandingPage != nil && landing != now.LandingChosen {
			pages.LandingPage, forgetLanding = landing, landing == ""
			opened = append(opened, "the opening page "+cmp.Or(landing, "Dockhand's own"))
		}
		if in.EnvClickPage != nil && envClick != now.EnvClickChosen {
			pages.EnvClickPage, forgetClick = envClick, envClick == ""
			opened = append(opened, "the page a click on an environment opens "+cmp.Or(envClick, "Dockhand's own"))
		}
		if len(opened) > 0 {
			step("made "+strings.Join(opened, " and ")+", for everyone who chose none", "would make "+strings.Join(opened, " and ")+", for everyone who chose none", func() error {
				if pages.LandingPage != "" || pages.EnvClickPage != "" {
					if _, failed := r.dock.PutSettingsNavigation(ctx, pages, dockhand.PutSettingsNavigationOperationOptions{Scope: "global"}); failed != nil {
						return failed
					}
				}
				if forgetLanding || forgetClick {
					if _, failed := r.dock.ForgetNavigationPages(ctx, forgetLanding, forgetClick); failed != nil {
						return failed
					}
				}

				return nil
			})
		}

		if len(steps) == 0 {
			if in.EnvironmentOrder == nil && in.TagOrder == nil && in.SidebarOrder == nil && in.SidebarHidden == nil && grid == "" && reset == "" && in.HideSystemJobs == nil && in.DashboardLocked == nil && view == "" && in.DashboardTiles == nil && in.LandingPage == nil && in.EnvClickPage == nil {
				return nil, out, errors.New("nothing to change was given: environment_order, tag_order, sidebar_order, sidebar_hidden, grid with grid_columns, grid_reset, hide_system_jobs, dashboard_locked, dashboard_view, dashboard_tiles, landing_page or env_click_page")
			}
			out.Note = "nothing to do: it is that way already"

			return nil, out, nil
		}
		if in.DryRun {
			return nil, out, nil
		}
		for i, call := range steps {
			if failed := call(); failed != nil {
				was := out.Changes[i]
				out.Changes = out.Changes[:i]

				return nil, out, fmt.Errorf("this was not done, and what came before it was: %s: %w", was, refusal(failed))
			}
		}
		after, _, _, err := r.remembered(ctx)
		if err != nil {
			out.Note = "it was done, and could not be read back: what is given is how it stood before: " + err.Error()

			return nil, out, nil //nolint:nilerr // the changes went through: that they could not be read back is said in the answer
		}
		out.preferences = *after

		return nil, out, nil
	})

	type favoritesOn struct {
		Env       string          `json:"env"`
		Favorites []favoriteEntry `json:"favorites" jsonschema:"what is kept for the environment's log view, in the order it is shown: a container, or a group of containers under a name"`
	}
	type favoriteListOut struct {
		Favorites []favoritesOn `json:"favorites"        jsonschema:"what is kept for the log view of each environment asked"`
		Failed    []envFailure  `json:"failed,omitempty" jsonschema:"the environments that could not be asked, and why; the others are still given"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "favorite_list",
		Description: "List the favourites of the log view of Dockhand's web interface, on every environment (Docker host) or on one: the containers whose logs are kept at hand, and the groups of containers whose logs are read together, in the order they are shown. They are everyone's, not one user's. A favourite is kept by a container's name, whether or not a container has it still.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in envArg) (*mcp.CallToolResult, favoriteListOut, error) {
		envs, err := r.pick(ctx, in)
		if err != nil {
			return nil, favoriteListOut{}, err
		}
		out := favoriteListOut{Favorites: make([]favoritesOn, 0, len(envs))}
		for _, answer := range ask(ctx, envs, r.favoritesOf) {
			if answer.Err != nil {
				out.Failed = append(out.Failed, envFailure{Env: answer.Env.Name, Error: answer.Err.Error()})

				continue
			}
			out.Favorites = append(out.Favorites, favoritesOn{Env: answer.Env.Name, Favorites: answer.Value})
		}

		return nil, out, nil
	})

	type favoriteSetIn struct {
		envArg
		dryRunArg

		Favorites []favoriteEntry `json:"favorites" jsonschema:"everything to keep for the environment's log view, in the order to show it, in place of what is kept: each a container by name, or a group with its name and its containers. An empty list keeps nothing"`
	}
	type favoriteSetOut struct {
		Env       string          `json:"env"`
		Favorites []favoriteEntry `json:"favorites"         jsonschema:"what is kept now, read back; for a dry run, what is kept before anything is done"`
		Changes   []string        `json:"changes"           jsonschema:"what was done, in words; empty when nothing needed doing"`
		DryRun    bool            `json:"dry_run,omitempty" jsonschema:"nothing was done: changes says what would be"`
		Note      string          `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "favorite_set",
		Description: "Set the favourites of the log view of Dockhand's web interface for one environment (Docker host): the containers whose logs are kept at hand and the groups whose logs are read together, given whole and in the order to show them. They are everyone's, not one user's, and nothing on a Docker host is changed. Dockhand keeps the containers and the groups in one list, and its own way of reordering either kind drops the other: so the whole list is read first, and what is given is written as the whole of it. A name no container has is refused unless it is kept already, since Dockhand saves any text; a group needs a name of its own and a container.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in favoriteSetIn) (*mcp.CallToolResult, favoriteSetOut, error) {
		if in.Favorites == nil {
			return nil, favoriteSetOut{}, errors.New("favorites is required: everything to keep, in order, or an empty list to keep nothing")
		}
		env, err := r.one(ctx, in.envArg)
		if err != nil {
			return nil, favoriteSetOut{}, err
		}
		kept, err := r.favoritesOf(ctx, env)
		if err != nil {
			return nil, favoriteSetOut{}, fmt.Errorf("the favourites of %s could not be read, so nothing was changed: %w", env.Name, err)
		}
		out := favoriteSetOut{Env: env.Name, Favorites: kept, Changes: []string{}, DryRun: in.DryRun}
		there, err := r.containersOf(ctx, env)
		if err != nil {
			return nil, out, fmt.Errorf("the containers of %s could not be listed, so nothing was changed: %w", env.Name, err)
		}
		known := func(name string) bool {
			return slices.ContainsFunc(there, func(c dockhand.GetContainersResponse) bool { return c.Name == name }) || slices.ContainsFunc(kept, func(e favoriteEntry) bool { return e.Container == name || slices.Contains(e.Containers, name) })
		}

		wanted := make([]favoriteEntry, 0, len(in.Favorites))
		list := make([]any, 0, len(in.Favorites))
		for i, given := range in.Favorites {
			entry := favoriteEntry{Container: strings.TrimSpace(given.Container), Group: strings.TrimSpace(given.Group), Containers: []string{}}
			for _, member := range given.Containers {
				if member = strings.TrimSpace(member); member != "" && !slices.Contains(entry.Containers, member) {
					entry.Containers = append(entry.Containers, member)
				}
			}
			names := entry.Containers
			switch {
			case (entry.Container == "") == (entry.Group == ""):
				return nil, out, fmt.Errorf("entry %d of favorites is neither one container nor one group: give container, or group with its containers", i+1)
			case entry.Container != "" && len(entry.Containers) > 0:
				return nil, out, fmt.Errorf("favorites has %s with containers of its own: a container is given alone, and a group by group", entry.Container)
			case entry.Group != "" && len(entry.Containers) == 0:
				return nil, out, fmt.Errorf("the group %s has no containers: a group is at least one", entry.Group)
			case entry.Container != "" && slices.ContainsFunc(wanted, func(e favoriteEntry) bool { return e.Container == entry.Container }):
				return nil, out, fmt.Errorf("favorites has the container %s twice", entry.Container)
			case entry.Group != "" && slices.ContainsFunc(wanted, func(e favoriteEntry) bool { return e.Group == entry.Group }):
				return nil, out, fmt.Errorf("favorites has two groups named %s", entry.Group)
			case entry.Container != "":
				names = []string{entry.Container}
			default:
			}
			if i := slices.IndexFunc(names, func(name string) bool { return !known(name) }); i >= 0 {
				return nil, out, fmt.Errorf("no container is named %q on %s: container_list gives the ones there are. Dockhand would keep the name all the same", names[i], env.Name)
			}
			wanted = append(wanted, entry)
			if entry.Group != "" {
				list = append(list, map[string]any{"name": entry.Group, "containers": entry.Containers})

				continue
			}
			entry.Containers = nil
			wanted[len(wanted)-1] = entry
			list = append(list, entry.Container)
		}
		alike := func(x, y favoriteEntry) bool {
			return x.Container == y.Container && x.Group == y.Group && slices.Equal(x.Containers, y.Containers)
		}
		same := func(a, b []favoriteEntry) bool { return slices.EqualFunc(a, b, alike) }
		if same(wanted, kept) {
			out.Note = "nothing to do: that is what is kept, in that order"

			return nil, out, nil
		}
		count := func(n int) string {
			if n == 0 {
				return "no favourites"
			}

			return several(n, "favourite")
		}
		what := fmt.Sprintf("%s for the log view of %s, in place of %s", count(len(wanted)), env.Name, count(len(kept)))
		if in.DryRun {
			out.Changes = append(out.Changes, "would keep "+what)

			return nil, out, nil
		}

		if _, failed := r.dock.SetLogFavorites(ctx, env.ID, list); failed != nil {
			return nil, out, fmt.Errorf("the favourites of %s were not set: %w", env.Name, refusal(failed))
		}
		out.Changes = append(out.Changes, "kept "+what)
		back, err := r.favoritesOf(ctx, env)
		if err != nil {
			out.Favorites = wanted
			out.Note = "they were set, and could not be read back: favorites is what was sent"

			return nil, out, nil //nolint:nilerr // the favourites were set: that they could not be read back is said in the answer
		}
		out.Favorites = back
		if !same(back, wanted) {
			return nil, out, fmt.Errorf("the favourites of %s were answered as set, and what is kept is not what was asked: favorite_list gives it", env.Name)
		}

		return nil, out, nil
	})
}
