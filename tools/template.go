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

const (
	// defaultTemplates is how many templates template_list gives unless told, and maxTemplates the most: the sources Dockhand comes with hold about a
	// thousand between the two that are switched on
	defaultTemplates = 25
	maxTemplates     = 200
	// aboutTemplate is how much of a template's description a list gives
	aboutTemplate = 200
	// templateContainer and templateStack are the two kinds of template: one image and how to run it, and a compose file kept in a repository
	templateContainer = "container"
	templateStack     = "stack"
	// noPlace is what Dockhand writes in a compose file for a volume the template names no place on the host for, which is no volume at all
	noPlace = "undefined:"
)

// templateRow is one app template as a list gives it.
type templateRow struct {
	ID          string   `json:"id"                    jsonschema:"what template_get takes"`
	Title       string   `json:"title"`
	Type        string   `json:"type"                  jsonschema:"container for one image and how to run it, stack for a compose file kept in a repository"`
	Description string   `json:"description,omitempty" jsonschema:"what it is, cut short"`
	Categories  []string `json:"categories,omitempty"`
	Image       string   `json:"image,omitempty"       jsonschema:"for a container template, the image it runs"`
	Source      string   `json:"source"                jsonschema:"the template source it comes from, by the name the source had when it was read"`
}

// templates is every app template Dockhand has from the sources that are switched on.
func (r *registry) templates(ctx context.Context) ([]dockhand.GetTemplatesResponse, error) {
	listed, err := r.dock.GetTemplates(ctx)
	if err != nil {
		return nil, fmt.Errorf("the templates could not be listed: %w", refusal(err))
	}

	return listed.Model, nil
}

// templateSources is every source of templates Dockhand keeps.
func (r *registry) templateSources(ctx context.Context) ([]dockhand.GetTemplatesSourcesResponse, error) {
	listed, err := r.dock.GetTemplatesSources(ctx)
	if err != nil {
		return nil, fmt.Errorf("the template sources could not be listed: %w", refusal(err))
	}

	return listed.Model, nil
}

// sourceArg names one source of templates.
type sourceArg struct {
	Source any `json:"source,omitempty" jsonschema:"the template source, by its name or its number as template_source_list gives them"`
}

// sourceIn is the source of a list a call names, by its name whatever its case or by its number. Dockhand lets two share a name, and a name two share
// is refused with their numbers.
func sourceIn(listed []dockhand.GetTemplatesSourcesResponse, a sourceArg) (*dockhand.GetTemplatesSourcesResponse, error) {
	wanted, err := named(a.Source, "source")
	if err != nil {
		return nil, err
	}
	if wanted == "" {
		return nil, errors.New("source is required: the template source's name or number, as template_source_list gives them")
	}
	var found []*dockhand.GetTemplatesSourcesResponse
	names := make([]string, 0, len(listed))
	for i := range listed {
		if strconv.Itoa(listed[i].Id) == wanted {
			return &listed[i], nil
		}
		if strings.EqualFold(listed[i].Name, wanted) {
			found = append(found, &listed[i])
		}
		names = append(names, listed[i].Name)
	}
	switch len(found) {
	case 0:
		return nil, fmt.Errorf("no template source is named or numbered %q: Dockhand keeps %s", wanted, cmp.Or(strings.Join(names, ", "), "none"))
	case 1:
		return found[0], nil
	default:
		numbers := make([]string, 0, len(found))
		for _, s := range found {
			numbers = append(numbers, strconv.Itoa(s.Id))
		}

		return nil, fmt.Errorf("%d template sources are named %q: give the number of one, %s", len(found), wanted, strings.Join(numbers, " or "))
	}
}

// sourceRow is one source of templates.
type sourceRow struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"url"               jsonschema:"where Dockhand fetches the source's list of templates from"`
	Enabled   bool   `json:"enabled"           jsonschema:"switched on: its templates are among the ones listed"`
	Builtin   bool   `json:"builtin,omitempty" jsonschema:"one of the sources Dockhand comes with"`
	Templates int    `json:"templates"         jsonschema:"how many templates are listed under its name now: none for one that is switched off, none for one whose address answers nothing Dockhand can read, and none for one whose address was fetched within the hour under another name"`
}

func sourceRowOf(s *dockhand.GetTemplatesSourcesResponse, all []dockhand.GetTemplatesResponse) sourceRow {
	row := sourceRow{ID: s.Id, Name: s.Name, URL: s.Url, Enabled: yes(s.Enabled), Builtin: yes(s.Builtin)}
	for i := range all {
		if all[i].Source == s.Name {
			row.Templates++
		}
	}

	return row
}

// sourceAddress is the address of a template source as a caller gives one, which says how it is fetched.
func sourceAddress(given string) (string, error) {
	url := strings.TrimSpace(given)
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return "", fmt.Errorf("url is %q: it is the address a list of templates is fetched from, with its scheme, as https://example.com/templates.json", given)
	}

	return url, nil
}

// sourceChangeOut is what adding or changing a template source answers.
type sourceChangeOut struct {
	Source  *sourceRow `json:"source,omitempty"  jsonschema:"the source as Dockhand has it now, read back, with how many templates are listed under its name; absent for a dry run of adding one"`
	Changes []string   `json:"changes"           jsonschema:"what was done, in words; empty when nothing needed doing"`
	DryRun  bool       `json:"dry_run,omitempty" jsonschema:"nothing was done: changes says what would be"`
	Note    string     `json:"note,omitempty"`
}

//nolint:gocyclo,maintidx // six tools registered in one function, each a handler of its own: the count is theirs added up
func registerTemplateTools(r *registry) {
	type templateListIn struct {
		Search   string `json:"search,omitempty"   jsonschema:"only templates with every one of these words in their title, description or categories, whatever their case"`
		Category string `json:"category,omitempty" jsonschema:"only templates in this category"`
		Type     string `json:"type,omitempty"     jsonschema:"only container templates, or only stack templates"`
		Source   string `json:"source,omitempty"   jsonschema:"only templates from the source with this in its name"`
		Limit    int    `json:"limit,omitempty"    jsonschema:"the most templates to give, 25 unless given and 200 at most; total counts every one that matched"`
		Offset   int    `json:"offset,omitempty"   jsonschema:"how many of the matching templates to pass over first, to read on"`
	}
	type templateListOut struct {
		Templates []templateRow `json:"templates"      jsonschema:"the templates that matched, by title"`
		Total     int           `json:"total"          jsonschema:"how many templates matched"`
		Of        int           `json:"of"             jsonschema:"how many templates there are in all, from every source that is switched on"`
		Note      string        `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "template_list",
		Description: "List the app templates the Dockhand has: ready-made ways to run a well-known application, each either one image with its ports, volumes and variables or a compose file kept in a repository. They come from the template sources that are switched on, each fetched from its address and kept for an hour; the two that are switched on as Dockhand comes hold about a thousand between them, so search by words, category, type or source. template_get gives one in full, with the compose file a stack is made from.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in templateListIn) (*mcp.CallToolResult, templateListOut, error) {
		kind := strings.ToLower(strings.TrimSpace(in.Type))
		if kind != "" && kind != templateContainer && kind != templateStack {
			return nil, templateListOut{}, fmt.Errorf("type is %q: it is %s or %s", in.Type, templateContainer, templateStack)
		}
		if in.Limit < 0 || in.Limit > maxTemplates || in.Offset < 0 {
			return nil, templateListOut{}, fmt.Errorf("limit is %d and offset %d: limit is how many templates to give at most, from 1 to %d or left out for %d, and offset how many to pass over, from 0", in.Limit, in.Offset, maxTemplates, defaultTemplates)
		}
		all, err := r.templates(ctx)
		if err != nil {
			return nil, templateListOut{}, err
		}

		words := strings.Fields(strings.ToLower(in.Search))
		category, source := strings.TrimSpace(in.Category), strings.ToLower(strings.TrimSpace(in.Source))
		matched := make([]*dockhand.GetTemplatesResponse, 0, len(all))
		for i := range all {
			t := &all[i]
			about := strings.ToLower(t.Title + " " + t.Description + " " + strings.Join(t.Categories, " "))
			switch {
			case kind != "" && t.Type != kind:
			case source != "" && !strings.Contains(strings.ToLower(t.Source), source):
			case category != "" && !slices.ContainsFunc(t.Categories, func(c string) bool { return strings.EqualFold(c, category) }):
			case slices.ContainsFunc(words, func(word string) bool { return !strings.Contains(about, word) }):
			default:
				matched = append(matched, t)
			}
		}
		slices.SortStableFunc(matched, func(a, b *dockhand.GetTemplatesResponse) int {
			return cmp.Or(cmp.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title)), cmp.Compare(a.Source, b.Source))
		})

		out := templateListOut{Templates: []templateRow{}, Total: len(matched), Of: len(all)}
		for _, t := range matched[min(in.Offset, len(matched)):] {
			if len(out.Templates) == cmp.Or(in.Limit, defaultTemplates) {
				break
			}
			about := t.Description
			if len([]rune(about)) > aboutTemplate {
				about = string([]rune(about)[:aboutTemplate]) + "…"
			}
			out.Templates = append(out.Templates, templateRow{ID: t.Id, Title: t.Title, Type: t.Type, Description: about, Categories: t.Categories, Image: t.Image, Source: t.Source})
		}
		switch {
		case out.Of == 0:
			out.Note = "Dockhand has no templates: no template source is switched on, or none could be fetched, which Dockhand says nowhere but in its own log"
		case out.Total == 0:
			out.Note = fmt.Sprintf("none of the %d templates matched: fewer words, or another category, find more", out.Of)
		case in.Offset+len(out.Templates) < out.Total:
			out.Note = fmt.Sprintf("%d templates matched and %d are given: offset %d has the next", out.Total, len(out.Templates), in.Offset+len(out.Templates))
		default:
		}

		return nil, out, nil
	})

	type templateVariable struct {
		Name    string `json:"name"`
		Label   string `json:"label,omitempty"   jsonschema:"what the template calls it"`
		Default string `json:"default,omitempty" jsonschema:"what the template sets it to; absent for one it leaves to be filled in"`
	}
	type templateVolume struct {
		Container string `json:"container"      jsonschema:"where it is mounted in the container"`
		Host      string `json:"host,omitempty" jsonschema:"the place on the host, or the volume, the template names; absent when it names none"`
	}
	type templateGetIn struct {
		Template string `json:"template"         jsonschema:"the template, by its id as template_list gives it or by its title"`
		Source   string `json:"source,omitempty" jsonschema:"when two sources have a template of that title: the one from the source with this in its name"`
	}
	type templateGetOut struct {
		templateRow

		Ports         []string           `json:"ports,omitempty"          jsonschema:"for a container template, the ports it publishes, as a compose file writes them"`
		Volumes       []templateVolume   `json:"volumes,omitempty"`
		Variables     []templateVariable `json:"variables,omitempty"      jsonschema:"the environment variables the template sets or leaves to be filled in"`
		RestartPolicy string             `json:"restart_policy,omitempty"`
		Network       string             `json:"network,omitempty"        jsonschema:"the network mode the template asks for, when it asks for one"`
		Repository    string             `json:"repository,omitempty"     jsonschema:"for a stack template, the repository its compose file is kept in"`
		File          string             `json:"file,omitempty"           jsonschema:"for a stack template, the compose file in that repository"`
		ProjectURL    string             `json:"project_url,omitempty"`
		About         string             `json:"about,omitempty"          jsonschema:"what the template's makers say of using it"`
		Compose       string             `json:"compose"                  jsonschema:"the compose file for it, as Dockhand writes it: what a stack is made from"`
		Note          string             `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "template_get",
		Description: "Describe one app template in full and give the compose file for it: for a container template its image, ports, volumes and variables, which Dockhand writes out as a compose file with one service; for a stack template the repository its compose file is kept in, which Dockhand fetches from there, so it fails when the repository cannot be reached. Nothing is started and nothing is saved: the compose file is text to read, change and make a stack from. It is a starting point written by whoever made the template, not by Dockhand: its variables want filling in, and its paths on the host are its makers' own.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in templateGetIn) (*mcp.CallToolResult, templateGetOut, error) {
		wanted, source := strings.TrimSpace(in.Template), strings.ToLower(strings.TrimSpace(in.Source))
		if wanted == "" {
			return nil, templateGetOut{}, errors.New("template is required: its id as template_list gives it, or its title")
		}
		all, err := r.templates(ctx)
		if err != nil {
			return nil, templateGetOut{}, err
		}
		var found []*dockhand.GetTemplatesResponse
		for i := range all {
			if all[i].Id == wanted {
				found = []*dockhand.GetTemplatesResponse{&all[i]}

				break
			}
			if strings.EqualFold(all[i].Title, wanted) && strings.Contains(strings.ToLower(all[i].Source), source) {
				found = append(found, &all[i])
			}
		}
		switch len(found) {
		case 0:
			return nil, templateGetOut{}, fmt.Errorf("no template has the id or the title %q: template_list finds one by words in its title", wanted)
		case 1:
		default:
			ids := make([]string, 0, len(found))
			for _, t := range found {
				ids = append(ids, fmt.Sprintf("%s (%s, from %s)", t.Id, t.Type, t.Source))
			}

			return nil, templateGetOut{}, fmt.Errorf("%d templates are titled %q: give the id of one, %s", len(found), wanted, strings.Join(ids, ", "))
		}
		t := found[0]
		whole := templateRow{ID: t.Id, Title: t.Title, Type: t.Type, Description: t.Description, Categories: t.Categories, Image: t.Image, Source: t.Source}
		out := templateGetOut{templateRow: whole, Ports: t.Ports, RestartPolicy: t.RestartPolicy, Network: t.Network, ProjectURL: t.ProjectUrl, About: t.Note}

		// the template goes back to Dockhand as it gave it, which is what it writes the compose file from
		given := dockhand.PostTemplatesComposeRequestTemplate{Id: t.Id, Type: t.Type, Title: t.Title, Image: t.Image, Ports: t.Ports, RestartPolicy: t.RestartPolicy, Network: t.Network}
		for i := range t.Volumes {
			out.Volumes = append(out.Volumes, templateVolume{Container: t.Volumes[i].Container, Host: t.Volumes[i].Bind})
			given.Volumes = append(given.Volumes, dockhand.PostTemplatesComposeRequestTemplateVolumes{Container: t.Volumes[i].Container, Bind: t.Volumes[i].Bind})
		}
		for i := range t.Env {
			out.Variables = append(out.Variables, templateVariable{Name: t.Env[i].Name, Label: t.Env[i].Label, Default: t.Env[i].Default})
			given.Env = append(given.Env, dockhand.PostTemplatesComposeRequestTemplateEnv{Name: t.Env[i].Name, Label: t.Env[i].Label, Default: t.Env[i].Default})
		}
		if t.Repository != nil {
			out.Repository, out.File = t.Repository.Url, t.Repository.Stackfile
			given.Repository = &dockhand.PostTemplatesComposeRequestTemplateRepository{Url: t.Repository.Url, Stackfile: t.Repository.Stackfile}
		}

		written, err := r.dock.PostTemplatesCompose(ctx, dockhand.PostTemplatesComposeRequest{Template: &given})
		if err != nil && t.Type == templateStack {
			return nil, out, fmt.Errorf("the compose file of %s could not be had: Dockhand fetches a stack template's from its repository, %s, and could not: %w", t.Title, out.Repository, refusal(err))
		}
		if err != nil {
			return nil, out, fmt.Errorf("the compose file of %s could not be had: %w", t.Title, refusal(err))
		}
		out.Compose = written.Model.Compose
		if n := strings.Count(out.Compose, "- "+noPlace); n > 0 {
			out.Note = fmt.Sprintf("Dockhand wrote \"undefined\" as where %d of its volumes are kept on the host, the template naming no place for them, and that is no volume: put a volume's name or a path there before a stack is made from this", n)
		}

		return nil, out, nil
	})

	type sourceListOut struct {
		Sources []sourceRow `json:"sources"        jsonschema:"the template sources, in Dockhand's order"`
		Total   int         `json:"total"`
		Note    string      `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "template_source_list",
		Description: "List the sources the Dockhand's app templates come from: each one's name, the address its list of templates is fetched from, whether it is switched on, whether it is one of the sources Dockhand comes with, and how many templates are listed under its name now. A source whose address answers nothing Dockhand can read gives no templates, and Dockhand says so nowhere but in its own log, so a source that is switched on with none is worth a look. Dockhand keeps what it fetched from an address for an hour, with the name the source had then: one renamed, or added again under another name, has its templates listed under the old name until then.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, sourceListOut, error) {
		listed, err := r.templateSources(ctx)
		if err != nil {
			return nil, sourceListOut{}, err
		}
		out := sourceListOut{Sources: make([]sourceRow, 0, len(listed)), Total: len(listed)}
		all, err := r.templates(ctx)
		if err != nil {
			out.Note = "how many templates each has could not be counted: " + err.Error()
		}
		for i := range listed {
			out.Sources = append(out.Sources, sourceRowOf(&listed[i], all))
		}

		return nil, out, nil
	})

	type sourceCreateIn struct {
		dryRunArg

		Name string `json:"name" jsonschema:"what to call the source: its templates are listed under this name"`
		URL  string `json:"url"  jsonschema:"the address its list of templates is fetched from, with its scheme: a JSON file in the form Portainer's templates take"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "template_source_create",
		Description: "Add a source of app templates to the Dockhand: a name, and the address of a list of templates in the form Portainer's take. It is switched on as it is added, and Dockhand fetches it at once, from wherever the address points, the Dockhand's own network included. Dockhand takes any address that reads as one and says nothing when it answers nothing it can use, so the answer says how many templates came of it, and under what name they are listed when the address was fetched within the hour under another. A name or an address already in use is refused, where Dockhand would keep both.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in sourceCreateIn) (*mcp.CallToolResult, sourceChangeOut, error) {
		name := strings.TrimSpace(in.Name)
		if name == "" {
			return nil, sourceChangeOut{}, errors.New("name is required: what to call the source")
		}
		url, err := sourceAddress(in.URL)
		if err != nil {
			return nil, sourceChangeOut{}, err
		}
		listed, err := r.templateSources(ctx)
		if err != nil {
			return nil, sourceChangeOut{}, err
		}
		for i := range listed {
			if strings.EqualFold(listed[i].Name, name) || listed[i].Url == url {
				return nil, sourceChangeOut{}, fmt.Errorf("the template source %s already has that name or that address: template_source_list gives it, and another is needed for another", listed[i].Name)
			}
		}

		out := sourceChangeOut{Changes: []string{}, DryRun: in.DryRun}
		what := fmt.Sprintf("the template source %s, fetched from %s", name, url)
		if in.DryRun {
			out.Changes = append(out.Changes, "would add "+what)

			return nil, out, nil
		}

		// the templates there are before, to tell afterwards which came of this source: Dockhand lists them under the name the address was last
		// fetched under, which need not be this one
		had := map[string]bool{}
		if before, failed := r.templates(ctx); failed == nil {
			for i := range before {
				had[before[i].Id] = true
			}
		}
		made, err := r.dock.PostTemplatesSources(ctx, dockhand.PostTemplatesSourcesRequest{Name: name, Url: url})
		if err != nil {
			return nil, out, fmt.Errorf("%s was not added: %w", name, refusal(err))
		}
		out.Changes = append(out.Changes, "added "+what)
		all, err := r.templates(ctx)
		if err != nil {
			out.Note = "it was added, and its templates could not be counted: " + err.Error()
		}
		row := sourceRowOf(new(dockhand.GetTemplatesSourcesResponse(*made.Model)), all)
		out.Source = &row
		if err != nil || row.Templates > 0 {
			return nil, out, nil //nolint:nilerr // the source was added: that its templates could not be counted is said in the answer
		}
		under := []string{}
		came := 0
		for i := range all {
			if !had[all[i].Id] {
				came++
				if !slices.Contains(under, all[i].Source) {
					under = append(under, all[i].Source)
				}
			}
		}
		if came > 0 {
			out.Note = fmt.Sprintf("%d templates came of it, and are listed under %s: Dockhand keeps what it fetched from an address for an hour, with the name the source had then, and this address was fetched within the hour under that name", came, strings.Join(under, ", "))
		} else {
			out.Note = "Dockhand took it, and no template comes of it: its address answered nothing Dockhand could read as a list of templates, which Dockhand says only in its own log"
		}

		return nil, out, nil
	})

	type sourceEditIn struct {
		sourceArg
		dryRunArg

		Enabled *bool  `json:"enabled,omitempty" jsonschema:"true switches the source on, so that its templates are listed; false switches it off"`
		Name    string `json:"name,omitempty"    jsonschema:"another name for the source"`
		URL     string `json:"url,omitempty"     jsonschema:"another address for its list of templates, with its scheme"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "template_source_edit",
		Description: "Change a source of the Dockhand's app templates: switch it on or off, or give it another name or address. Switching one off takes its templates out of the list at once, and switching it on has it fetched. What is not given is kept. A new name shows on the templates only once Dockhand reads the source again, which it does within the hour. A source that is not there is refused, where Dockhand answers that it changed it.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in sourceEditIn) (*mcp.CallToolResult, sourceChangeOut, error) {
		name, url := strings.TrimSpace(in.Name), ""
		if strings.TrimSpace(in.URL) != "" {
			var err error
			if url, err = sourceAddress(in.URL); err != nil {
				return nil, sourceChangeOut{}, err
			}
		}
		listed, err := r.templateSources(ctx)
		if err != nil {
			return nil, sourceChangeOut{}, err
		}
		saved, err := sourceIn(listed, in.sourceArg)
		if err != nil {
			return nil, sourceChangeOut{}, err
		}
		out := sourceChangeOut{Changes: []string{}, DryRun: in.DryRun}

		done := func(did, would string) string {
			if in.DryRun {
				return would
			}

			return did
		}
		request := dockhand.PutTemplatesSourcesRequest{Id: saved.Id}
		if name != "" && name != saved.Name {
			if slices.ContainsFunc(listed, func(s dockhand.GetTemplatesSourcesResponse) bool {
				return s.Id != saved.Id && strings.EqualFold(s.Name, name)
			}) {
				return nil, out, fmt.Errorf("another template source is named %q, so %s is not renamed to it", name, saved.Name)
			}
			request.Name = name
			out.Changes = append(out.Changes, fmt.Sprintf("%s it from %s to %s", done("renamed", "would rename"), saved.Name, name))
		}
		if url != "" && url != saved.Url {
			request.Url = url
			out.Changes = append(out.Changes, fmt.Sprintf("%s its address from %s to %s", done("changed", "would change"), saved.Url, url))
		}
		if in.Enabled != nil && *in.Enabled != yes(saved.Enabled) {
			request.Enabled = in.Enabled
			out.Changes = append(out.Changes, map[bool]string{true: done("switched", "would switch") + " it on", false: done("switched", "would switch") + " it off"}[*in.Enabled])
		}
		if len(out.Changes) == 0 {
			if name == "" && url == "" && in.Enabled == nil {
				return nil, out, errors.New("nothing to change was given: enabled, name or url")
			}
			out.Note = "nothing to do: it is that way already"
		}
		if len(out.Changes) > 0 && !in.DryRun {
			changed, failed := r.dock.PutTemplatesSources(ctx, request)
			if failed != nil {
				out.Changes = []string{}

				return nil, out, fmt.Errorf("%s was not changed: %w", saved.Name, refusal(failed))
			}
			if !yes(changed.Model.Ok) {
				out.Changes = []string{}

				return nil, out, fmt.Errorf("the change to %s was answered without its being said to have been made: template_source_list says how it is now", saved.Name)
			}
			if listed, err = r.templateSources(ctx); err != nil {
				out.Note = "it was changed, and could not be read back: " + err.Error()

				return nil, out, nil //nolint:nilerr // the change went through: that it could not be read back is said in the answer
			}
			i := slices.IndexFunc(listed, func(s dockhand.GetTemplatesSourcesResponse) bool { return s.Id == saved.Id })
			if i < 0 {
				return nil, out, fmt.Errorf("%s was changed, and is no longer listed", saved.Name)
			}
			saved = &listed[i]
		}

		all, err := r.templates(ctx)
		if err != nil {
			out.Note = note(out.Note, "its templates could not be counted: "+err.Error())
		}
		row := sourceRowOf(saved, all)
		out.Source = &row
		if !in.DryRun && err == nil && request.Name != "" && row.Enabled && row.Templates == 0 {
			out.Note = note(out.Note, "its templates are listed under its old name until Dockhand reads it again, within the hour")
		}

		return nil, out, nil
	})

	type sourceDeleteIn struct {
		sourceArg

		Confirm bool `json:"confirm,omitempty" jsonschema:"remove it: without this the answer says what would go and nothing is removed"`
	}
	type sourceDeleteOut struct {
		Source  string   `json:"source"`
		Removed bool     `json:"removed"            jsonschema:"the source was removed, and is read back as gone"`
		WouldGo []string `json:"would_go,omitempty" jsonschema:"what removing it takes away, said before anything is removed"`
		Note    string   `json:"note,omitempty"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "template_source_delete",
		Description: "Remove a source of the Dockhand's app templates: its templates go from the list at once. Nothing that was made from one of them is touched. Dockhand removes one of the sources it comes with as readily as one that was added, and nothing brings it back as its own: it can only be added again by its address, as any other. To keep a source and stop listing its templates, template_source_edit switches it off. A source that is not there is refused, where Dockhand answers that it removed it. Without confirm the answer says what would go and removes nothing.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in sourceDeleteIn) (*mcp.CallToolResult, sourceDeleteOut, error) {
		listed, err := r.templateSources(ctx)
		if err != nil {
			return nil, sourceDeleteOut{}, err
		}
		saved, err := sourceIn(listed, in.sourceArg)
		if err != nil {
			return nil, sourceDeleteOut{}, err
		}
		out := sourceDeleteOut{Source: saved.Name}
		own := ""
		if yes(saved.Builtin) {
			own = "it is one of the sources Dockhand comes with, and nothing brings it back as that: it can only be added again by its address, " + saved.Url
		}
		if !in.Confirm {
			out.WouldGo = []string{fmt.Sprintf("the template source %s, fetched from %s, and its templates from the list", saved.Name, saved.Url)}
			out.Note = "nothing was removed: call again with confirm to remove it"
			if own != "" {
				out.Note = note(out.Note, own)
			}

			return nil, out, nil
		}

		gone, err := r.dock.DeleteTemplatesSources(ctx, dockhand.DeleteTemplatesSourcesOperationOptions{Id: &saved.Id})
		if err != nil {
			return nil, out, fmt.Errorf("%s was not removed: %w", saved.Name, refusal(err))
		}
		if !yes(gone.Model.Ok) {
			return nil, out, fmt.Errorf("the removal of %s was answered without its being said to have been done: template_source_list says whether it is still there", saved.Name)
		}
		left, err := r.templateSources(ctx)
		if err != nil {
			out.Note = "it was removed, and the sources could not be listed afterwards: " + err.Error()

			return nil, out, nil //nolint:nilerr // the removal went through: that it could not be read back is said in the answer
		}
		if slices.ContainsFunc(left, func(s dockhand.GetTemplatesSourcesResponse) bool { return s.Id == saved.Id }) {
			return nil, out, fmt.Errorf("the removal of %s was answered as done, and it is still listed", saved.Name)
		}
		out.Removed, out.Note = true, own

		return nil, out, nil
	})
}
