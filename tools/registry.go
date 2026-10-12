package tools

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/pandorest/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// dockerHub is what a search with no registry named is made of
	dockerHub = "Docker Hub"
	// defaultFound is how many images or tags a registry tool gives unless told, and maxFound the most
	defaultFound = 50
	maxFound     = 200
	// sizedTags is how many tags registry_tags asks the size of, each being a call of its own
	sizedTags = 25
)

// registryArg names one registry the Dockhand keeps.
type registryArg struct {
	Registry any `json:"registry,omitempty" jsonschema:"the registry, by its name or its number as registry_list gives them"`
}

// named is the registry an argument names, as text: a name as it came, a number as its digits, and nothing for an argument left out.
func (a registryArg) named() (string, error) {
	switch given := a.Registry.(type) {
	case nil:
		return "", nil
	case string:
		return strings.TrimSpace(given), nil
	case float64:
		if given != float64(int(given)) {
			return "", fmt.Errorf("registry is %v: a registry's number is a whole one", given)
		}

		return strconv.Itoa(int(given)), nil
	default:
		return "", fmt.Errorf("registry is %v: it is a registry's name or its number", given)
	}
}

// registryRow is one registry the Dockhand keeps.
type registryRow struct {
	ID             int    `json:"id"`
	Name           string `json:"name"`
	URL            string `json:"url"                jsonschema:"the registry's address, with its scheme"`
	Username       string `json:"username,omitempty" jsonschema:"the user name Dockhand signs in to it with; absent for one with no credentials"`
	HasCredentials bool   `json:"has_credentials"    jsonschema:"Dockhand keeps a password for it, which it never gives back"`
	Default        bool   `json:"default,omitempty"  jsonschema:"the registry Dockhand offers first, where one is chosen"`
}

func registryRowOf(k *dockhand.GetRegistriesAnswer) registryRow {
	return registryRow{ID: int(k.Id), Name: k.Name, URL: k.Url, Username: k.Username, HasCredentials: k.HasCredentials, Default: k.IsDefault}
}

// kept is every registry the Dockhand keeps.
func (r *registry) kept(ctx context.Context) ([]dockhand.GetRegistriesAnswer, error) {
	listed, err := r.dock.GetRegistries(ctx)
	if err != nil {
		return nil, fmt.Errorf("the registries could not be listed: %w", refusal(err))
	}

	return listed.Answer, nil
}

// oneRegistry is the single registry a call names, by its name whatever its case or by its number. A name that is no registry's is refused with the
// ones there are.
func (r *registry) oneRegistry(ctx context.Context, a registryArg) (*dockhand.GetRegistriesAnswer, error) {
	named, err := a.named()
	if err != nil {
		return nil, err
	}
	if named == "" {
		return nil, errors.New("registry is required: the registry's name or number, as registry_list gives them")
	}
	listed, err := r.kept(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(listed))
	for i := range listed {
		if strings.EqualFold(listed[i].Name, named) || strconv.Itoa(int(listed[i].Id)) == named {
			return &listed[i], nil
		}
		names = append(names, listed[i].Name)
	}

	return nil, fmt.Errorf("no registry is named or numbered %q: Dockhand keeps %s", named, cmp.Or(strings.Join(names, ", "), "none"))
}

// isHub reports whether a registry's address is Docker Hub's, which has no catalog to list, no tag to delete, and no name before an image's.
func isHub(url string) bool {
	return strings.Contains(url, "docker.io") || strings.Contains(url, "hub.docker.com")
}

// hostOf is a registry's address as it leads the name of an image kept there: without its scheme or a slash after it, and nothing for Docker Hub.
func hostOf(url string) string {
	if isHub(url) {
		return ""
	}
	_, host, cut := strings.Cut(url, "://")
	if !cut {
		host = url
	}

	return strings.TrimRight(host, "/") + "/"
}

// withoutHost is the name of an image without the registry it leads with: a first part that holds a dot or a colon, or is localhost, is a registry.
func withoutHost(name string) string {
	first, rest, cut := strings.Cut(name, "/")
	if cut && (strings.ContainsAny(first, ".:") || first == "localhost") {
		return rest
	}

	return name
}

// registryRefusal is why a call that Dockhand passes on to a registry failed. Dockhand hands the registry's own 401 back as its answer, which read as
// any other 401 says Dockhand's login refused the caller: it is the registry that refused Dockhand, and that is what is said.
func registryRefusal(err error, registry string) error {
	if client.StatusCode(err) == http.StatusUnauthorized {
		return fmt.Errorf("%s turned Dockhand away: the user name and password kept for it are missing or wrong, which registry_test tells, or may not do this", registry)
	}

	return refusal(err)
}

// registryTried is what trying a registry found.
type registryTried struct {
	Reachable     bool   `json:"reachable"               jsonschema:"whether Dockhand reached a registry there"`
	Authenticated *bool  `json:"authenticated,omitempty" jsonschema:"whether the registry took the user name and password; absent when none were tried"`
	Message       string `json:"message,omitempty"       jsonschema:"what Dockhand says of it: who it signed in as, or why it could not"`
}

// tryRegistry has Dockhand try a registry, saved or not.
func (r *registry) tryRegistry(ctx context.Context, request dockhand.PostRegistriesTestRequest) (registryTried, bool, error) {
	answer, err := r.dock.PostRegistriesTest(ctx, request)
	if err != nil {
		return registryTried{}, false, refusal(err)
	}

	return registryTried{Reachable: answer.Answer.Connectivity, Authenticated: answer.Answer.Authenticated, Message: answer.Answer.Message}, answer.Answer.Success, nil
}

// addressed is a registry's address as a caller gives one, which says how it is spoken to: Dockhand takes one with no scheme for one that speaks TLS
// and fails to reach it when it does not, with no word of why.
func addressed(given string) (string, error) {
	url := strings.TrimRight(strings.TrimSpace(given), "/")
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return "", fmt.Errorf("url is %q: it is the registry's address with how it is spoken to before it, as https://ghcr.io or http://registry.example:5000", given)
	}

	return url, nil
}

// registryChangeOut is what saving or changing a registry answers.
type registryChangeOut struct {
	Registry *registryRow   `json:"registry,omitempty" jsonschema:"the registry as Dockhand has it now, read back; absent for a dry run of saving one"`
	Tried    *registryTried `json:"tried,omitempty"    jsonschema:"what trying it found: before it was saved, or after how it is reached was changed"`
	Changes  []string       `json:"changes"            jsonschema:"what was done, in words; empty when nothing needed doing"`
	DryRun   bool           `json:"dry_run,omitempty"  jsonschema:"nothing was done: changes says what would be"`
	Note     string         `json:"note,omitempty"`
}

//nolint:gocyclo,maintidx // nine tools registered in one function, each a handler of its own: the count is theirs added up
func registerRegistryTools(r *registry) {
	type registryListOut struct {
		Registries []registryRow `json:"registries" jsonschema:"the registries Dockhand keeps, the default first"`
		Total      int           `json:"total"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "registry_list",
		Description: "List the image registries the Dockhand keeps: each one's name, address, the user name it signs in with and whether it has a password for it, and which is the default. They are where Dockhand's hosts pull private images from and push to: a host pulling an image whose name leads with a kept registry's address is signed in with what is kept for it. A password is never given back.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, registryListOut, error) {
		listed, err := r.kept(ctx)
		if err != nil {
			return nil, registryListOut{}, err
		}
		out := registryListOut{Registries: make([]registryRow, 0, len(listed)), Total: len(listed)}
		for i := range listed {
			out.Registries = append(out.Registries, registryRowOf(&listed[i]))
		}
		slices.SortStableFunc(out.Registries, func(a, b registryRow) int {
			return cmp.Or(cmp.Compare(b2i(!a.Default), b2i(!b.Default)), cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)))
		})

		return nil, out, nil
	})

	type registryTestIn struct {
		registryArg

		URL      string `json:"url,omitempty"      jsonschema:"to try a registry that is not saved: its address with its scheme, as https://ghcr.io or http://registry.example:5000"`
		Username string `json:"username,omitempty" jsonschema:"with url: the user name to sign in with"`
		Password string `json:"password,omitempty" jsonschema:"with url: the password or token to sign in with. It is sent to Dockhand to try, and is not kept"`
	}
	type registryTestOut struct {
		Registry string `json:"registry,omitempty" jsonschema:"the registry that was tried; absent for one that is not saved"`

		registryTried

		Note string `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "registry_test",
		Description: "Have the Dockhand try an image registry and say what it found: whether it reached one there, and whether the user name and password were taken. Give registry to try one that is saved, with the credentials kept for it, or url, with a user name and password or without, to try one before it is saved, which changes nothing. A registry that cannot be reached, or turns the credentials away, is an answer and not an error. A registry that asks for a login and is given none is said to be reachable and no more.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in registryTestIn) (*mcp.CallToolResult, registryTestOut, error) {
		named, err := in.named()
		if err != nil {
			return nil, registryTestOut{}, err
		}
		var out registryTestOut
		request := dockhand.PostRegistriesTestRequest{}
		switch {
		case named != "" && (in.URL != "" || in.Username != "" || in.Password != ""):
			return nil, registryTestOut{}, errors.New("give registry, to try one that is saved, or url, to try one that is not: not both")
		case named != "":
			saved, failed := r.oneRegistry(ctx, in.registryArg)
			if failed != nil {
				return nil, registryTestOut{}, failed
			}
			out.Registry, request.RegistryId = saved.Name, saved.Id
		default:
			if strings.TrimSpace(in.URL) == "" {
				return nil, registryTestOut{}, errors.New("give registry, to try one that is saved, or url, to try one that is not")
			}
			if request.Url, err = addressed(in.URL); err != nil {
				return nil, registryTestOut{}, err
			}
			request.Username, request.Password = strings.TrimSpace(in.Username), in.Password
			out.Note = "this registry is not saved: nothing on the Dockhand was changed by trying it"
		}

		if out.registryTried, _, err = r.tryRegistry(ctx, request); err != nil {
			return nil, out, fmt.Errorf("the registry could not be tried: %w", err)
		}

		return nil, out, nil
	})

	type registryCreateIn struct {
		dryRunArg

		Name            string `json:"name"                       jsonschema:"what to call the registry: the name the registry tools then take"`
		URL             string `json:"url"                        jsonschema:"its address with its scheme, as https://ghcr.io or http://registry.example:5000"`
		Username        string `json:"username,omitempty"         jsonschema:"the user name to sign in to it with; left out for a registry that asks for none"`
		Password        string `json:"password,omitempty"         jsonschema:"the password or token to sign in with. It is sent to Dockhand and kept there, and is never given back"`
		Default         bool   `json:"default,omitempty"          jsonschema:"make it the default registry, in place of the one that is"`
		SaveUnreachable bool   `json:"save_unreachable,omitempty" jsonschema:"save it though Dockhand cannot reach it now, or it turns the credentials away. Without this such a registry is not saved"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "registry_create",
		Description: "Save an image registry on the Dockhand: its name, its address, and the user name and password to sign in with when it asks for them. From then on a host pulling or pushing an image whose name leads with that address is signed in with them. The registry is tried first, and one that cannot be reached, or turns the credentials away, is not saved unless save_unreachable says to. A name already in use is refused. The password is kept by Dockhand and never given back.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in registryCreateIn) (*mcp.CallToolResult, registryChangeOut, error) {
		name, username := strings.TrimSpace(in.Name), strings.TrimSpace(in.Username)
		if name == "" {
			return nil, registryChangeOut{}, errors.New("name is required: what to call the registry")
		}
		url, err := addressed(in.URL)
		if err != nil {
			return nil, registryChangeOut{}, err
		}
		if (username == "") != (in.Password == "") {
			return nil, registryChangeOut{}, errors.New("a user name and a password go together: give both, or neither for a registry that asks for none")
		}
		listed, err := r.kept(ctx)
		if err != nil {
			return nil, registryChangeOut{}, err
		}
		if slices.ContainsFunc(listed, func(k dockhand.GetRegistriesAnswer) bool { return strings.EqualFold(k.Name, name) }) {
			return nil, registryChangeOut{}, fmt.Errorf("a registry is already named %q: registry_list gives it, and another name is needed for another", name)
		}

		tried, ok, err := r.tryRegistry(ctx, dockhand.PostRegistriesTestRequest{Url: url, Username: username, Password: in.Password})
		if err != nil {
			return nil, registryChangeOut{}, fmt.Errorf("the registry could not be tried: %w", err)
		}
		out := registryChangeOut{Tried: &tried, Changes: []string{}, DryRun: in.DryRun}
		if !ok && !in.SaveUnreachable {
			return nil, out, fmt.Errorf("%s was not saved: %s (save_unreachable saves it all the same)", name, cmp.Or(tried.Message, "Dockhand could not use the registry and gives no reason"))
		}

		what := fmt.Sprintf("the registry %s at %s", name, url)
		if username != "" {
			what += ", signed in to as " + username
		}
		changes := []string{what}
		if in.Default {
			changes = append(changes, "it as the default registry")
		}
		if in.DryRun {
			for _, change := range changes {
				out.Changes = append(out.Changes, "would save "+change)
			}

			return nil, out, nil
		}

		request := dockhand.PostRegistriesRequest{Name: name, Url: url, Username: username, Password: in.Password}
		if in.Default {
			request.IsDefault = new(true)
		}
		made, err := r.dock.PostRegistries(ctx, request)
		if err != nil {
			return nil, out, fmt.Errorf("%s was not saved: %w", name, refusal(err))
		}
		out.Changes = append(out.Changes, "saved "+what)
		if in.Default {
			out.Changes = append(out.Changes, "made it the default registry")
		}
		if back, err := r.dock.GetRegistriesById(ctx, made.Answer.Id); err != nil {
			out.Note = "it was saved, and could not be read back: " + err.Error()
		} else {
			row := registryRowOf(new(dockhand.GetRegistriesAnswer(*back.Answer)))
			out.Registry = &row
		}
		if !ok {
			out.Note = note(out.Note, "Dockhand cannot use it as it is: "+cmp.Or(tried.Message, "it gives no reason"))
		}

		return nil, out, nil
	})

	type registryEditIn struct {
		registryArg
		dryRunArg

		Name              string `json:"name,omitempty"               jsonschema:"another name for the registry"`
		URL               string `json:"url,omitempty"                jsonschema:"another address for it, with its scheme"`
		Username          string `json:"username,omitempty"           jsonschema:"another user name to sign in with"`
		Password          string `json:"password,omitempty"           jsonschema:"another password or token to sign in with. It is sent to Dockhand and kept there, and is never given back"`
		ForgetCredentials bool   `json:"forget_credentials,omitempty" jsonschema:"have Dockhand forget the user name and password it keeps for it"`
		Default           bool   `json:"default,omitempty"            jsonschema:"make it the default registry, in place of the one that is"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "registry_edit",
		Description: "Change an image registry the Dockhand keeps: its name, its address, the user name and password it signs in with, or have those forgotten, and whether it is the default. What is not given is kept, the password included. After a change to how it is reached or signed in to, the registry is tried and the answer says whether Dockhand can still use it: the change is saved either way. Making one the default takes that from the one that was.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in registryEditIn) (*mcp.CallToolResult, registryChangeOut, error) {
		name, username := strings.TrimSpace(in.Name), strings.TrimSpace(in.Username)
		if in.ForgetCredentials && (username != "" || in.Password != "") {
			return nil, registryChangeOut{}, errors.New("forget_credentials forgets the user name and password, so it is not given with one to keep")
		}
		url := ""
		if strings.TrimSpace(in.URL) != "" {
			var err error
			if url, err = addressed(in.URL); err != nil {
				return nil, registryChangeOut{}, err
			}
		}
		saved, err := r.oneRegistry(ctx, in.registryArg)
		if err != nil {
			return nil, registryChangeOut{}, err
		}
		row := registryRowOf(saved)
		out := registryChangeOut{Registry: &row, Changes: []string{}, DryRun: in.DryRun}

		// what is asked for that is not so already: a change must carry the name and the address whether or not they change, since Dockhand fails one
		// that leaves the address out
		done := func(did, would string) string {
			if in.DryRun {
				return would
			}

			return did
		}
		request := dockhand.PutRegistriesByIdRequest{Name: saved.Name, Url: saved.Url}
		reached := false
		if name != "" && name != saved.Name {
			listed, failed := r.kept(ctx)
			if failed != nil {
				return nil, out, failed
			}
			if slices.ContainsFunc(listed, func(k dockhand.GetRegistriesAnswer) bool {
				return k.Id != saved.Id && strings.EqualFold(k.Name, name)
			}) {
				return nil, out, fmt.Errorf("another registry is named %q, so %s is not renamed to it", name, saved.Name)
			}
			request.Name = name
			out.Changes = append(out.Changes, fmt.Sprintf("%s it from %s to %s", done("renamed", "would rename"), saved.Name, name))
		}
		if url != "" && url != strings.TrimRight(saved.Url, "/") {
			request.Url, reached = url, true
			out.Changes = append(out.Changes, fmt.Sprintf("%s its address from %s to %s", done("changed", "would change"), saved.Url, url))
		}
		if username != "" && username != saved.Username {
			request.Username, reached = username, true
			out.Changes = append(out.Changes, fmt.Sprintf("%s the user name it signs in with to %s", done("changed", "would change"), username))
		}
		if in.Password != "" {
			request.Password, reached = in.Password, true
			out.Changes = append(out.Changes, done("changed", "would change")+" the password it signs in with to the one given")
		}
		forget := in.ForgetCredentials && (saved.HasCredentials || saved.Username != "")
		if forget {
			reached = true
			out.Changes = append(out.Changes, done("forgot", "would forget")+" the user name and password kept for it")
		}
		if in.Default && !saved.IsDefault {
			request.IsDefault = new(true)
			out.Changes = append(out.Changes, done("made", "would make")+" it the default registry")
		}
		if len(out.Changes) == 0 {
			if name == "" && url == "" && username == "" && in.Password == "" && !in.ForgetCredentials && !in.Default {
				return nil, out, errors.New("nothing to change was given: name, url, username, password, forget_credentials or default")
			}
			out.Note = "nothing to do: it is that way already"

			return nil, out, nil
		}
		if in.DryRun {
			return nil, out, nil
		}

		// forgetting is a call of its own, since the one that changes a registry leaves out what is empty; it carries the new name and address with
		// it
		var back *dockhand.PutRegistriesByIdAnswer
		if forget {
			forgot, failed := r.dock.ForgetRegistryCredentials(ctx, int(saved.Id), request.Name, request.Url)
			if failed != nil {
				out.Changes = []string{}

				return nil, out, fmt.Errorf("%s was not changed: %w", saved.Name, refusal(failed))
			}
			back = forgot.Answer
		}
		if !forget || request.IsDefault != nil {
			changed, failed := r.dock.PutRegistriesById(ctx, saved.Id, request)
			if failed != nil {
				if !forget {
					out.Changes = []string{}
				}

				return nil, out, fmt.Errorf("%s was not changed: %w", saved.Name, refusal(failed))
			}
			back = changed.Answer
		}
		row = registryRowOf(new(dockhand.GetRegistriesAnswer(*back)))
		out.Registry = &row

		if reached {
			tried, ok, failed := r.tryRegistry(ctx, dockhand.PostRegistriesTestRequest{RegistryId: saved.Id})
			switch {
			case failed != nil:
				out.Note = "it was changed, and could not be tried afterwards: " + failed.Error()
			case !ok:
				out.Tried = &tried
				out.Note = "the change is saved, and Dockhand cannot use the registry as it now is: " + cmp.Or(tried.Message, "it gives no reason")
			default:
				out.Tried = &tried
			}
		}

		return nil, out, nil
	})

	type registryDeleteIn struct {
		registryArg

		Confirm bool `json:"confirm,omitempty" jsonschema:"remove it: without this the answer says what would go and nothing is removed"`
	}
	type registryDeleteOut struct {
		Registry string   `json:"registry"`
		Removed  bool     `json:"removed"            jsonschema:"the registry was removed, and is read back as gone"`
		WouldGo  []string `json:"would_go,omitempty" jsonschema:"what removing it takes away, said before anything is removed"`
		Note     string   `json:"note,omitempty"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "registry_delete",
		Description: "Remove an image registry from the Dockhand: it forgets the registry, its address and the user name and password kept for it. Nothing in the registry is touched and no image on any host is removed, but a host can no longer pull a private image from it or push to it through Dockhand. Dockhand removes any registry it is told to, the default one and its own record of Docker Hub included, and then has no default until another is made it. Without confirm the answer says what would go and removes nothing.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in registryDeleteIn) (*mcp.CallToolResult, registryDeleteOut, error) {
		saved, err := r.oneRegistry(ctx, in.registryArg)
		if err != nil {
			return nil, registryDeleteOut{}, err
		}
		out := registryDeleteOut{Registry: saved.Name}
		if !in.Confirm {
			what := fmt.Sprintf("Dockhand's record of %s at %s", saved.Name, saved.Url)
			if saved.HasCredentials {
				what += ", and the user name and password kept for it"
			}
			out.WouldGo = []string{what}
			out.Note = "nothing was removed: call again with confirm to remove it. Nothing in the registry itself is touched either way"
			if saved.IsDefault {
				out.Note = note(out.Note, "it is the default registry, and Dockhand has none once it is removed, until registry_edit makes another the default")
			}

			return nil, out, nil
		}

		gone, err := r.dock.DeleteRegistriesById(ctx, saved.Id)
		if err != nil {
			return nil, out, fmt.Errorf("%s was not removed: %w", saved.Name, refusal(err))
		}
		if !gone.Answer.Success {
			return nil, out, fmt.Errorf("the removal of %s was answered without its being said to have succeeded: registry_list says whether it is still there", saved.Name)
		}
		left, err := r.kept(ctx)
		if err != nil {
			out.Note = "it was removed, and the registries could not be listed afterwards: " + err.Error()

			return nil, out, nil //nolint:nilerr // the removal went through: that it could not be read back is said in the answer
		}
		if slices.ContainsFunc(left, func(k dockhand.GetRegistriesAnswer) bool { return k.Id == saved.Id }) {
			return nil, out, fmt.Errorf("the removal of %s was answered as done, and it is still listed", saved.Name)
		}
		out.Removed = true
		if saved.IsDefault {
			out.Note = "it was the default registry, and Dockhand has none now: registry_edit makes another the default"
		}

		return nil, out, nil
	})

	type registryImage struct {
		Name        string `json:"name"                  jsonschema:"the image's name in the registry, without the registry's address or a tag"`
		Description string `json:"description,omitempty"`
		Stars       int    `json:"stars,omitempty"       jsonschema:"on Docker Hub: how many have starred it"`
		Official    bool   `json:"official,omitempty"    jsonschema:"on Docker Hub: one of its official images"`
	}
	type registryImagesIn struct {
		registryArg

		Search string `json:"search,omitempty" jsonschema:"only images with this in their name; needed for Docker Hub, which lists nothing without it"`
		Limit  int    `json:"limit,omitempty"  jsonschema:"the most images to give, 50 unless given and 200 at most"`
		After  string `json:"after,omitempty"  jsonschema:"to read on through a registry's list: the next value of the answer before"`
	}
	type registryImagesOut struct {
		Registry string          `json:"registry"       jsonschema:"the registry that was asked: Docker Hub when none was named"`
		Images   []registryImage `json:"images"`
		Total    int             `json:"total"          jsonschema:"how many images are given"`
		Next     string          `json:"next,omitempty" jsonschema:"there are more: give this as after to read on"`
		Note     string          `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "registry_images",
		Description: "List the images an image registry holds, or search one by name. With registry, a registry the Dockhand keeps is asked for everything it holds, or with search for what has that in its name, signed in with what is kept for it. With no registry it is Docker Hub that is searched, which needs search and gives each image's description, stars and whether it is official. It names images, not their tags: registry_tags gives the tags of one. A registry that keeps no list of what it holds says so.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in registryImagesIn) (*mcp.CallToolResult, registryImagesOut, error) {
		if in.Limit < 0 || in.Limit > maxFound {
			return nil, registryImagesOut{}, fmt.Errorf("limit is %d: it is how many images to give at most, from 1 to %d, or left out for %d", in.Limit, maxFound, defaultFound)
		}
		limit, search := cmp.Or(in.Limit, defaultFound), strings.TrimSpace(in.Search)
		named, err := in.named()
		if err != nil {
			return nil, registryImagesOut{}, err
		}
		out := registryImagesOut{Registry: dockerHub, Images: []registryImage{}}
		var saved *dockhand.GetRegistriesAnswer
		if named != "" {
			if saved, err = r.oneRegistry(ctx, in.registryArg); err != nil {
				return nil, registryImagesOut{}, err
			}
			out.Registry = saved.Name
		}
		if search == "" && (saved == nil || isHub(saved.Url)) {
			return nil, out, errors.New("Docker Hub lists nothing without a search: give search, or registry for a registry of your own") //nolint:staticcheck // ST1005: the sentence begins with a name
		}

		// a search, of Docker Hub or of a registry kept
		if search != "" {
			options := dockhand.GetRegistrySearchOptions{Term: search, Limit: &limit}
			if saved != nil {
				options.Registry = saved.Id
			}
			found, failed := r.dock.GetRegistrySearch(ctx, options)
			if failed != nil {
				return nil, out, fmt.Errorf("%s could not be searched: %w", out.Registry, registryRefusal(failed, out.Registry))
			}
			for i := range found.Answer {
				if len(out.Images) == limit {
					break
				}
				out.Images = append(out.Images, registryImage{Name: found.Answer[i].Name, Description: found.Answer[i].Description, Stars: found.Answer[i].StarCount, Official: found.Answer[i].IsOfficial})
			}
			if out.Total = len(out.Images); out.Total == 0 {
				out.Note = fmt.Sprintf("nothing in %s has %q in its name", out.Registry, search)
			}

			return nil, out, nil
		}

		// everything a registry kept holds, a page of it at a time
		listed, err := r.dock.GetRegistryCatalog(ctx, dockhand.GetRegistryCatalogOptions{Registry: saved.Id, Last: strings.TrimSpace(in.After)})
		if client.StatusCode(err) == http.StatusUnauthorized {
			return nil, out, fmt.Errorf("what %s holds could not be listed: %w", out.Registry, registryRefusal(err, out.Registry))
		}
		if err != nil {
			return nil, out, fmt.Errorf("what %s holds could not be listed, which a registry that keeps no list of it answers too: %w", out.Registry, refusal(err))
		}
		for i := range listed.Answer.Repositories {
			if len(out.Images) == limit {
				out.Next = out.Images[limit-1].Name

				break
			}
			out.Images = append(out.Images, registryImage{Name: listed.Answer.Repositories[i].Name, Description: listed.Answer.Repositories[i].Description})
		}
		if out.Next == "" && listed.Answer.Pagination.HasMore {
			out.Next = listed.Answer.Pagination.NextLast
		}
		switch out.Total = len(out.Images); {
		case out.Next != "":
			out.Note = "there are more: give next as after to read on"
		case out.Total == 0:
			out.Note = out.Registry + " holds no images, or none this login may see"
		default:
		}

		return nil, out, nil
	})

	type registryTag struct {
		Tag     string  `json:"tag"`
		SizeMB  float64 `json:"size_mb,omitempty" jsonschema:"what the image under the tag takes to pull, compressed, in megabytes"`
		Updated string  `json:"updated,omitempty" jsonschema:"when the tag last changed, where the registry says"`
		Digest  string  `json:"digest,omitempty"  jsonschema:"what the tag points at, where the registry says"`
	}
	type registryTagsIn struct {
		registryArg

		Image string `json:"image"           jsonschema:"the image's name in the registry, as registry_images gives it: org/app, or library/nginx on Docker Hub"`
		Limit int    `json:"limit,omitempty" jsonschema:"the most tags to give, 50 unless given and 200 at most; total counts every one"`
		Page  int    `json:"page,omitempty"  jsonschema:"on Docker Hub, which gives its tags a page at a time, the newest first: which page of limit tags, 1 unless given"`
		Sizes bool   `json:"sizes,omitempty" jsonschema:"for a registry of your own, which gives the names alone: ask the size of each tag as well, the first 25 of them"`
	}
	type registryTagsOut struct {
		Registry string        `json:"registry"       jsonschema:"the registry that was asked: Docker Hub when none was named"`
		Image    string        `json:"image"`
		Tags     []registryTag `json:"tags"`
		Total    int           `json:"total"          jsonschema:"how many tags the image has there"`
		More     bool          `json:"more,omitempty" jsonschema:"on Docker Hub: there is a page after this one"`
		Note     string        `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "registry_tags",
		Description: "List the tags an image has in a registry: in one the Dockhand keeps, signed in with what is kept for it, or on Docker Hub when no registry is named. It is how to learn which versions of an image there are to pull, and what was pushed. Docker Hub gives each tag's size, when it changed and what it points at, a page at a time and the newest first; a registry of your own gives the names alone, and with sizes each tag's size as well.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in registryTagsIn) (*mcp.CallToolResult, registryTagsOut, error) {
		image := strings.Trim(strings.TrimSpace(in.Image), "/")
		if image == "" || strings.ContainsAny(image, " \t\n:") {
			return nil, registryTagsOut{}, fmt.Errorf("image is %q: it is the image's name in the registry, without the registry's address or a tag, as org/app", in.Image)
		}
		if in.Limit < 0 || in.Limit > maxFound || in.Page < 0 {
			return nil, registryTagsOut{}, fmt.Errorf("limit is %d and page %d: limit is how many tags to give at most, from 1 to %d or left out for %d, and page which page of them, from 1", in.Limit, in.Page, maxFound, defaultFound)
		}
		limit := cmp.Or(in.Limit, defaultFound)
		named, err := in.named()
		if err != nil {
			return nil, registryTagsOut{}, err
		}
		out := registryTagsOut{Registry: dockerHub, Image: image, Tags: []registryTag{}}
		options := dockhand.GetRegistryTagsOptions{Image: image, PageSize: &limit}
		if in.Page > 1 {
			options.Page = &in.Page
		}
		var saved *dockhand.GetRegistriesAnswer
		if named != "" {
			if saved, err = r.oneRegistry(ctx, in.registryArg); err != nil {
				return nil, registryTagsOut{}, err
			}
			out.Registry, options.Registry = saved.Name, saved.Id
		}

		listed, err := r.dock.GetRegistryTags(ctx, options)
		if err != nil && strings.Contains(err.Error(), "not found") {
			return nil, out, fmt.Errorf("%s has no image named %s: registry_images lists what it has", out.Registry, image)
		}
		if err != nil {
			return nil, out, fmt.Errorf("the tags of %s in %s could not be listed: %w", image, out.Registry, registryRefusal(err, out.Registry))
		}
		out.Total, out.More = listed.Answer.Total, listed.Answer.HasNext
		for i := range listed.Answer.Tags {
			if len(out.Tags) == limit {
				break
			}
			t := &listed.Answer.Tags[i]
			out.Tags = append(out.Tags, registryTag{Tag: t.Name, SizeMB: megabytes(t.Size), Updated: when(t.LastUpdated), Digest: t.Digest})
		}
		switch {
		case out.Total == 0:
			out.Note = fmt.Sprintf("%s has no tags in %s: an image whose every tag was deleted is still named there, with none", image, out.Registry)
		case out.More:
			out.Note = fmt.Sprintf("the image has %d tags and these are page %d of them: page %d has the next", out.Total, max(in.Page, 1), max(in.Page, 1)+1)
		case out.Total > len(out.Tags):
			out.Note = fmt.Sprintf("the image has %d tags and the first %d are given: raise limit for more", out.Total, len(out.Tags))
		default:
		}

		// a registry of one's own gives names alone: the size of each is a call of its own
		if in.Sizes && saved != nil && !isHub(saved.Url) {
			for i := range out.Tags {
				if i == sizedTags {
					out.Note = note(out.Note, fmt.Sprintf("the size is given for the first %d tags", sizedTags))

					break
				}
				info, failed := r.dock.GetRegistryTagInfo(ctx, dockhand.GetRegistryTagInfoOptions{Registry: saved.Id, Image: image, Tag: out.Tags[i].Tag})
				if failed != nil {
					out.Note = note(out.Note, "the sizes could not all be read: "+refusal(failed).Error())

					break
				}
				out.Tags[i].SizeMB, out.Tags[i].Updated = math.Round(info.Answer.Size/1e5)/10, cmp.Or(out.Tags[i].Updated, when(info.Answer.LastUpdated))
			}
		}

		return nil, out, nil
	})

	type registryImageDeleteIn struct {
		registryArg

		Image   string `json:"image"             jsonschema:"the image's name in the registry, as registry_images gives it"`
		Tag     string `json:"tag"               jsonschema:"the tag to delete, as registry_tags gives it"`
		Confirm bool   `json:"confirm,omitempty" jsonschema:"delete it: without this the answer says what would go and nothing is deleted"`
	}
	type registryImageDeleteOut struct {
		Registry string   `json:"registry"`
		Image    string   `json:"image"`
		Tag      string   `json:"tag"`
		Deleted  bool     `json:"deleted"             jsonschema:"the tag was deleted, and is read back as gone from the registry"`
		AlsoGone []string `json:"also_gone,omitempty" jsonschema:"the other tags of the image that went with it, having named the very same contents"`
		WouldGo  []string `json:"would_go,omitempty"  jsonschema:"what deleting it takes away, said before anything is deleted"`
		Note     string   `json:"note,omitempty"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "registry_image_delete",
		Description: "Delete one tag of an image from a registry the Dockhand keeps, so that it can no longer be pulled from there. It cannot be got back except by pushing the image again. A registry deletes what the tag points at, not the tag alone, so every other tag of that image holding the very same contents goes with it: the answer names the ones that did. Copies already on hosts are untouched, and the image stays named in the registry even with no tag left. Docker Hub lets nothing be deleted this way, and a registry of your own only when it is set to allow it. Without confirm the answer says what would go and deletes nothing.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in registryImageDeleteIn) (*mcp.CallToolResult, registryImageDeleteOut, error) {
		image, tag := strings.Trim(strings.TrimSpace(in.Image), "/"), strings.TrimSpace(in.Tag)
		if image == "" || tag == "" {
			return nil, registryImageDeleteOut{}, errors.New("image and tag are required: the image's name in the registry and the tag to delete, as registry_images and registry_tags give them")
		}
		saved, err := r.oneRegistry(ctx, in.registryArg)
		if err != nil {
			return nil, registryImageDeleteOut{}, err
		}
		out := registryImageDeleteOut{Registry: saved.Name, Image: image, Tag: tag}
		if isHub(saved.Url) {
			return nil, out, errors.New("Docker Hub lets no tag be deleted through its registry: that is done on its website") //nolint:staticcheck // ST1005: the sentence begins with a name
		}

		// the tags there are, before and after: which is how the others that go with it are told
		tags := func() ([]string, error) {
			listed, failed := r.dock.GetRegistryTags(ctx, dockhand.GetRegistryTagsOptions{Registry: saved.Id, Image: image})
			if failed != nil {
				return nil, registryRefusal(failed, saved.Name)
			}
			names := make([]string, 0, len(listed.Answer.Tags))
			for i := range listed.Answer.Tags {
				names = append(names, listed.Answer.Tags[i].Name)
			}

			return names, nil
		}
		before, err := tags()
		if err != nil {
			return nil, out, fmt.Errorf("the tags of %s in %s could not be listed, which is what an image the registry has none of answers, so nothing was deleted: %w", image, saved.Name, err)
		}
		if !slices.Contains(before, tag) {
			return nil, out, fmt.Errorf("%s has no tag %s in %s: it has %s", image, tag, saved.Name, cmp.Or(strings.Join(before, ", "), "none"))
		}
		if !in.Confirm {
			out.WouldGo = []string{fmt.Sprintf("%s:%s in %s: it could no longer be pulled from there", image, tag, saved.Name)}
			out.Note = "nothing was deleted: call again with confirm to delete it"
			if len(before) > 1 {
				out.Note = note(out.Note, "any of its other tags that holds the very same contents goes with it, which the registry does not tell beforehand")
			}

			return nil, out, nil
		}

		if _, err := r.dock.DeleteRegistryImage(ctx, dockhand.DeleteRegistryImageOptions{Registry: saved.Id, Image: image, Tag: tag}); err != nil {
			if client.StatusCode(err) == http.StatusMethodNotAllowed {
				return nil, out, fmt.Errorf("%s does not allow deleting: a registry has to be set to (REGISTRY_STORAGE_DELETE_ENABLED), and this one is not", saved.Name)
			}

			return nil, out, fmt.Errorf("%s:%s was not deleted from %s: %w", image, tag, saved.Name, registryRefusal(err, saved.Name))
		}
		after, err := tags()
		switch {
		case err != nil:
			out.Note = "it was deleted, and the tags could not be listed afterwards: " + err.Error()
		case slices.Contains(after, tag):
			return nil, out, fmt.Errorf("the deleting of %s:%s was answered as done, and %s still lists the tag", image, tag, saved.Name)
		default:
			out.Deleted = true
			for _, other := range before {
				if other != tag && !slices.Contains(after, other) {
					out.AlsoGone = append(out.AlsoGone, other)
				}
			}
			if len(out.AlsoGone) > 0 {
				out.Note = "the tags named in also_gone held the very same contents, and went with it"
			}
		}

		return nil, out, nil
	})

	type imagePushIn struct {
		envArg
		registryArg
		waitArg
		dryRunArg

		Image string `json:"image"        jsonschema:"the image on the host, by a name and tag it goes by there (nginx:1.27, ghcr.io/org/app:v2), by its name alone or the end of it when that is one image, or by the start of its id"`
		As    string `json:"as,omitempty" jsonschema:"the name and tag to push it under in the registry, without the registry's address: org/app:1.2. Left out, the image's own name and tag, less the registry it came from"`
	}
	type imagePushOut struct {
		jobState

		Env      string `json:"env"               jsonschema:"the environment (Docker host) the image was pushed from"`
		Image    string `json:"image"             jsonschema:"the image that was pushed, by a name it goes by on the host or the start of its id"`
		Registry string `json:"registry"`
		PushedAs string `json:"pushed_as"         jsonschema:"the whole name it is pushed under, the registry's address first: what a host pulls it by"`
		Pushed   bool   `json:"pushed"            jsonschema:"the push ended well, and the registry is read back as holding the tag"`
		DryRun   bool   `json:"dry_run,omitempty" jsonschema:"nothing was pushed: pushed_as says what it would be pushed under"`
		Note     string `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "image_push",
		Description: "Push an image that is on a Docker host (environment) to a registry the Dockhand keeps, signed in with what is kept for it, under its own name or another: how an image built or pulled on one host is made available to the rest. Anything already in the registry under that name and tag is replaced. To push it, Dockhand gives the image the registry's name on the host as well, and leaves that name there afterwards. It waits for the push, 50 seconds unless wait says otherwise, and then reads the registry back to see that the tag is there; a push still going by then is answered as running, with its job.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in imagePushIn) (*mcp.CallToolResult, imagePushOut, error) {
		patience, err := in.patience()
		if err != nil {
			return nil, imagePushOut{}, err
		}
		under := strings.Trim(strings.TrimSpace(in.As), "/")
		if strings.ContainsAny(under, " \t\n@") {
			return nil, imagePushOut{}, fmt.Errorf("as is %q: it is a name and tag to push under, as org/app:1.2", in.As)
		}
		saved, err := r.oneRegistry(ctx, in.registryArg)
		if err != nil {
			return nil, imagePushOut{}, err
		}
		at, err := r.findImage(ctx, imageArg{envArg: in.envArg, Image: in.Image})
		if err != nil {
			return nil, imagePushOut{}, err
		}
		tags := tagsOf(&at.Image)
		out := imagePushOut{Env: at.Env.Name, Image: cmp.Or(cmp.Or(tags...), shortImageID(at.Image.Id)), Registry: saved.Name, DryRun: in.DryRun}

		// the name it goes under is said here rather than left to Dockhand, which takes the first name the image has
		if under == "" {
			own := slices.IndexFunc(tags, func(tag string) bool { return tag == strings.TrimSpace(in.Image) })
			switch {
			case own >= 0:
				under = withoutHost(tags[own])
			case len(tags) > 0:
				under = withoutHost(tags[0])
			default:
				return nil, out, errors.New("the image has no name on the host to push it under: give as, the name and tag it is to have in the registry")
			}
		}
		if under == withoutHost(under) && strings.HasPrefix(under, strings.TrimSuffix(hostOf(saved.Url), "/")+"/") && hostOf(saved.Url) != "" {
			under = strings.TrimPrefix(under, hostOf(saved.Url))
		}
		if repository(under) == under {
			under += ":latest"
		}
		out.PushedAs = hostOf(saved.Url) + under
		if in.DryRun {
			out.Note = "nothing was pushed: without dry_run the image is pushed under that name, replacing what the registry has under it"

			return nil, out, nil
		}

		started, err := r.dock.PostImagesPush(ctx, dockhand.PostImagesPushRequest{ImageId: at.Image.Id, RegistryId: saved.Id, NewTag: under}, dockhand.PostImagesPushOptions{Accept: dockhand.AcceptJob, Env: at.Env.ID})
		if err != nil {
			return nil, out, fmt.Errorf("%s was not pushed to %s: %w", out.Image, saved.Name, refusal(err))
		}
		outcome, state, err := settle(ctx, r, started.Answer.JobId, started.Answer, patience)
		out.jobState = state
		switch {
		case err != nil:
			return nil, out, fmt.Errorf("%s was not pushed to %s: %w", out.Image, saved.Name, err)
		case state.Running:
			out.Note = fmt.Sprintf("the push was still running after %s and goes on without this: job_get with job %s says how it ends", seconds(patience), state.Job)

			return nil, out, nil
		case outcome.Status == jobFailed || outcome.Error != "":
			return nil, out, fmt.Errorf("%s was not pushed to %s: %s", out.Image, saved.Name, cmp.Or(plainly(outcome.Error), "Dockhand gave no reason"))
		default:
		}
		out.PushedAs = cmp.Or(outcome.TargetTag, out.PushedAs)
		out.Note = fmt.Sprintf("the image on %s goes by %s as well now: Dockhand names it so to push it, and leaves the name", at.Env.Name, out.PushedAs)

		// read back: the registry holds the tag
		if isHub(saved.Url) {
			out.Pushed = true

			return nil, out, nil
		}
		listed, err := r.dock.GetRegistryTags(ctx, dockhand.GetRegistryTagsOptions{Registry: saved.Id, Image: repository(under)})
		switch {
		case err != nil:
			out.Note = note("it was pushed, and the registry could not be read back: "+refusal(err).Error(), out.Note)
		case !slices.ContainsFunc(listed.Answer.Tags, func(t dockhand.GetRegistryTagsAnswerTags) bool { return repository(under)+":"+t.Name == under }):
			return nil, out, fmt.Errorf("the push of %s was answered as done, and %s does not list the tag: registry_tags says what it has", out.PushedAs, saved.Name)
		default:
			out.Pushed = true
		}

		return nil, out, nil
	})
}
