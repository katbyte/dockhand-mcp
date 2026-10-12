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
	"github.com/katbyte/pandorest/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// tagColours is every colour a 1.0.51 Dockhand takes for a tag. One made with any other is made slate, and one changed to any other keeps the colour
// it had, neither with a word said: so a colour is checked here first.
var tagColours = []string{"slate", "red", "orange", "amber", "lime", "green", "emerald", "teal", "cyan", "sky", "blue", "indigo", "violet", "purple", "fuchsia", "pink"}

// tagColour is the colour a tag is made when none is asked for.
const tagColour = "slate"

// tagsByName is what every tool about a container's or a stack's tags says of how Dockhand keeps them
const tagsByName = "Dockhand keeps a container's and a stack's tags under its name on one environment, and not with the thing itself: a container that is renamed leaves its tags behind under the name it had, a container or a stack that is removed leaves them too, and whatever has that name afterwards has them"

// errNotSaid is a change Dockhand answered without the answer saying that it was made.
var errNotSaid = errors.New("the answer did not say that it was")

// tagArg names one tag the Dockhand keeps.
type tagArg struct {
	Tag any `json:"tag,omitempty" jsonschema:"the tag, by its name or its number as tag_list gives them"`
}

// tagOn is what one environment has given a tag to.
type tagOn struct {
	Env        string   `json:"env"`
	Containers []string `json:"containers,omitempty" jsonschema:"the names of the containers that have the tag, in order"`
	Stacks     []string `json:"stacks,omitempty"     jsonschema:"the names of the compose stacks that have the tag, in order"`
}

// tagRow is one tag the Dockhand keeps.
type tagRow struct {
	ID    int     `json:"id"`
	Name  string  `json:"name"`
	Color string  `json:"color"          jsonschema:"one of the colours Dockhand has for a tag"`
	Icon  string  `json:"icon,omitempty" jsonschema:"the name of the icon shown with it; absent for a tag given none"`
	On    []tagOn `json:"on,omitempty"   jsonschema:"where the tag is given, for each environment asked that has it on anything; absent for a tag on nothing there"`
}

func tagRowOf(t *dockhand.GetTagsAnswerTags) tagRow {
	return tagRow{ID: int(t.Id), Name: t.Name, Color: t.Color, Icon: t.Icon}
}

// tags is every tag the Dockhand keeps.
func (r *registry) tags(ctx context.Context) ([]dockhand.GetTagsAnswerTags, error) {
	listed, err := r.dock.GetTags(ctx)
	if err != nil {
		return nil, fmt.Errorf("the tags could not be listed: %w", refusal(err))
	}

	return listed.Answer.Tags, nil
}

// tagIn is the tag of a list a call names, by its number or by its name whatever its case: Dockhand keeps no two tags whose names differ only in
// that.
func tagIn(listed []dockhand.GetTagsAnswerTags, given any) (*dockhand.GetTagsAnswerTags, error) {
	wanted, err := named(given, "tag")
	if err != nil {
		return nil, err
	}
	if wanted == "" {
		return nil, errors.New("tag is required: the tag's name or number, as tag_list gives them")
	}
	names := make([]string, 0, len(listed))
	byName := -1
	for i := range listed {
		if strconv.Itoa(int(listed[i].Id)) == wanted {
			return &listed[i], nil
		}
		if byName < 0 && strings.EqualFold(listed[i].Name, wanted) {
			byName = i
		}
		names = append(names, listed[i].Name)
	}
	if byName < 0 {
		return nil, fmt.Errorf("no tag is named or numbered %q: the Dockhand keeps %s", wanted, cmp.Or(strings.Join(names, ", "), "none"))
	}

	return &listed[byName], nil
}

// tagColourOf is a colour as Dockhand has it, or why what was given is none of its colours.
func tagColourOf(given string) (string, error) {
	colour := strings.ToLower(strings.TrimSpace(given))
	if colour != "" && !slices.Contains(tagColours, colour) {
		return "", fmt.Errorf("color is %q: a tag is %s or %s. Dockhand takes any other without a word, making a new tag %s and leaving a tag that is changed the colour it was", given, strings.Join(tagColours[:len(tagColours)-1], ", "), tagColours[len(tagColours)-1], tagColour)
	}

	return colour, nil
}

// tagged is what one environment has given tags to: the numbers of the tags each container and each stack has, by its name.
type tagged struct {
	Containers map[string][]int
	Stacks     map[string][]int
}

// taggedOn is what an environment has given tags to.
func (r *registry) taggedOn(ctx context.Context, env environment) (tagged, error) {
	containers, err := r.dock.GetContainerTags(ctx, dockhand.GetContainerTagsOptions{Env: env.ID})
	if err != nil {
		return tagged{}, refusal(err)
	}
	stacks, err := r.dock.GetStackTags(ctx, dockhand.GetStackTagsOptions{Env: env.ID})
	if err != nil {
		return tagged{}, refusal(err)
	}

	return tagged{Containers: containers.Answer, Stacks: stacks.Answer}, nil
}

// holding is the names of a list that hold a tag, in order.
func holding(given map[string][]int, tag int) []string {
	var names []string
	for name, ids := range given {
		if slices.Contains(ids, tag) {
			names = append(names, name)
		}
	}
	slices.Sort(names)

	return names
}

// tagsAcross is where each tag is given on the environments a call is about, by the tag's number, with the environments that could not be asked. With
// no environment named it is every one, and none at all is an answer too: a Dockhand that manages no Docker host still keeps tags.
func (r *registry) tagsAcross(ctx context.Context, in envArg, listed []dockhand.GetTagsAnswerTags) (map[int][]tagOn, []envFailure, error) {
	which, err := in.named()
	if err != nil {
		return nil, nil, err
	}
	var envs []environment
	if which == "" {
		envs, err = r.environments(ctx)
	} else {
		envs, err = r.pick(ctx, in)
	}
	if err != nil {
		return nil, nil, err
	}

	on := map[int][]tagOn{}
	var failed []envFailure
	for _, answer := range ask(ctx, envs, r.taggedOn) {
		if answer.Err != nil {
			failed = append(failed, envFailure{Env: answer.Env.Name, Error: answer.Err.Error()})

			continue
		}
		for i := range listed {
			here := tagOn{Env: answer.Env.Name, Containers: holding(answer.Value.Containers, int(listed[i].Id)), Stacks: holding(answer.Value.Stacks, int(listed[i].Id))}
			if len(here.Containers)+len(here.Stacks) > 0 {
				on[int(listed[i].Id)] = append(on[int(listed[i].Id)], here)
			}
		}
	}

	return on, failed, nil
}

// takenFirst is what Dockhand was seen to do with a change of tags it refuses
const takenFirst = "Dockhand takes the tags a name has from it before it gives it the ones asked for, and a change it refuses leaves the name with none"

// sameTags is whether two lists name the same tags, in whatever order and however often each is named: Dockhand gives them back in an order of its
// own.
func sameTags(a, b []int) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)

	return slices.Equal(slices.Compact(a), slices.Compact(b))
}

// several counts things in words: 1 container, 3 stacks.
func several(n int, thing string) string {
	if n == 1 {
		return "1 " + thing
	}

	return strconv.Itoa(n) + " " + thing + "s"
}

// tagChangeOut is what making or changing a tag answers.
type tagChangeOut struct {
	Tag     *tagRow  `json:"tag,omitempty"     jsonschema:"the tag as Dockhand has it now, read back; absent for a dry run of making one"`
	Changes []string `json:"changes"           jsonschema:"what was done, in words; empty when nothing needed doing"`
	DryRun  bool     `json:"dry_run,omitempty" jsonschema:"nothing was done: changes says what would be"`
	Note    string   `json:"note,omitempty"`
}

//nolint:gocyclo // five tools registered in one function, each a handler of its own: the count is theirs added up
func registerTagTools(r *registry) {
	type tagListOut struct {
		Tags   []tagRow     `json:"tags"             jsonschema:"the tags the Dockhand keeps, by name"`
		Total  int          `json:"total"`
		Failed []envFailure `json:"failed,omitempty" jsonschema:"the environments that could not be asked, and why; what the others have tagged is still given"`
		Note   string       `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "tag_list",
		Description: "List the tags the Dockhand keeps: the names, each with a colour and an icon, that its web interface sorts and filters containers and compose stacks by. Each is given with where it is on: for every environment (Docker host), or the one named, the containers and the stacks that have it. A tag is only Dockhand's own note: Docker knows nothing of it, and it is no label of a container. " + tagsByName + ", so a name listed here need be no container's or stack's now.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in envArg) (*mcp.CallToolResult, tagListOut, error) {
		listed, err := r.tags(ctx)
		if err != nil {
			return nil, tagListOut{}, err
		}
		on, failed, err := r.tagsAcross(ctx, in, listed)
		if err != nil {
			return nil, tagListOut{}, err
		}

		out := tagListOut{Tags: make([]tagRow, 0, len(listed)), Total: len(listed), Failed: failed}
		for i := range listed {
			row := tagRowOf(&listed[i])
			row.On = on[row.ID]
			out.Tags = append(out.Tags, row)
		}
		slices.SortStableFunc(out.Tags, func(a, b tagRow) int {
			return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.ID, b.ID))
		})
		if out.Total == 0 {
			out.Note = "the Dockhand keeps no tag: tag_create makes one"
		}

		return nil, out, nil
	})

	type tagCreateIn struct {
		dryRunArg

		Name  string `json:"name"            jsonschema:"what to call the tag"`
		Color string `json:"color,omitempty" jsonschema:"its colour: slate, red, orange, amber, lime, green, emerald, teal, cyan, sky, blue, indigo, violet, purple, fuchsia or pink; slate unless given"`
		Icon  string `json:"icon,omitempty"  jsonschema:"the name of an icon to show with it; none unless given. Dockhand saves whatever text it is given"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "tag_create",
		Description: "Make a tag on the Dockhand: a name with a colour, and an icon if one is given, that tag_assign then gives to containers and compose stacks so that its web interface can sort and filter by it. A tag is only Dockhand's own note: nothing on a Docker host is changed by one. A name in use, whatever its case, is refused: Dockhand would answer with the tag it has, unchanged, as though it had made one. A colour that is none of Dockhand's is refused too, which Dockhand would make slate without a word.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in tagCreateIn) (*mcp.CallToolResult, tagChangeOut, error) {
		name, icon := strings.TrimSpace(in.Name), strings.TrimSpace(in.Icon)
		if name == "" {
			return nil, tagChangeOut{}, errors.New("name is required: what to call the tag")
		}
		colour, err := tagColourOf(in.Color)
		if err != nil {
			return nil, tagChangeOut{}, err
		}
		listed, err := r.tags(ctx)
		if err != nil {
			return nil, tagChangeOut{}, err
		}
		if i := slices.IndexFunc(listed, func(t dockhand.GetTagsAnswerTags) bool { return strings.EqualFold(t.Name, name) }); i >= 0 {
			return nil, tagChangeOut{}, fmt.Errorf("a tag is already named %s, numbered %d: Dockhand would answer with that one, unchanged. tag_edit changes it, and another name is needed for another", listed[i].Name, listed[i].Id)
		}

		out := tagChangeOut{Changes: []string{}, DryRun: in.DryRun}
		what := fmt.Sprintf("the tag %s, %s", name, cmp.Or(colour, tagColour))
		if icon != "" {
			what += ", with the icon " + icon
		}
		if in.DryRun {
			out.Changes = append(out.Changes, "would make "+what)

			return nil, out, nil
		}

		made, err := r.dock.PostTags(ctx, dockhand.PostTagsRequest{Name: name, Color: colour, Icon: icon})
		if err != nil {
			return nil, out, fmt.Errorf("the tag %s was not made: %w", name, refusal(err))
		}
		row := tagRow{ID: int(made.Answer.Id), Name: made.Answer.Name, Color: made.Answer.Color, Icon: made.Answer.Icon}
		out.Tag = &row
		out.Changes = append(out.Changes, "made "+what)
		if row.Color != cmp.Or(colour, tagColour) || row.Icon != icon {
			out.Note = fmt.Sprintf("Dockhand answered with a tag that is %s%s, which is not what was asked: tag_edit changes it", row.Color, map[bool]string{true: " with the icon " + row.Icon, false: " with no icon"}[row.Icon != ""])
		}

		return nil, out, nil
	})

	type tagEditIn struct {
		tagArg
		dryRunArg

		Name  string  `json:"name,omitempty"  jsonschema:"another name for the tag"`
		Color string  `json:"color,omitempty" jsonschema:"another colour: slate, red, orange, amber, lime, green, emerald, teal, cyan, sky, blue, indigo, violet, purple, fuchsia or pink"`
		Icon  *string `json:"icon,omitempty"  jsonschema:"the name of another icon to show with it; empty to have it shown with none"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "tag_edit",
		Description: "Change a tag the Dockhand keeps: its name, its colour and its icon. What is not given is kept, and everything that has the tag has it still, under its new name. A name another tag has, whatever its case, is refused, and so is a colour that is none of Dockhand's, which Dockhand would answer as done while keeping the colour it had. A tag there is none of is refused too: Dockhand answers that one as done as well. The tag is read back afterwards.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in tagEditIn) (*mcp.CallToolResult, tagChangeOut, error) {
		colour, err := tagColourOf(in.Color)
		if err != nil {
			return nil, tagChangeOut{}, err
		}
		listed, err := r.tags(ctx)
		if err != nil {
			return nil, tagChangeOut{}, err
		}
		saved, err := tagIn(listed, in.Tag)
		if err != nil {
			return nil, tagChangeOut{}, err
		}
		row := tagRowOf(saved)
		out := tagChangeOut{Tag: &row, Changes: []string{}, DryRun: in.DryRun}

		done := func(did, would string) string {
			if in.DryRun {
				return would
			}

			return did
		}
		request := dockhand.PutTagsByIdRequest{}
		wanted := row
		name := strings.TrimSpace(in.Name)
		if name != "" && name != saved.Name {
			if i := slices.IndexFunc(listed, func(t dockhand.GetTagsAnswerTags) bool { return t.Id != saved.Id && strings.EqualFold(t.Name, name) }); i >= 0 {
				return nil, out, fmt.Errorf("another tag is named %s, numbered %d, so %s is not renamed to it", listed[i].Name, listed[i].Id, saved.Name)
			}
			request.Name, wanted.Name = name, name
			out.Changes = append(out.Changes, fmt.Sprintf("%s it from %s to %s", done("renamed", "would rename"), saved.Name, name))
		}
		if colour != "" && colour != saved.Color {
			request.Color, wanted.Color = colour, colour
			out.Changes = append(out.Changes, fmt.Sprintf("%s its colour from %s to %s", done("changed", "would change"), saved.Color, colour))
		}
		sends := len(out.Changes) > 0
		forget := false
		if in.Icon != nil {
			icon := strings.TrimSpace(*in.Icon)
			if icon == "" && saved.Icon != "" {
				forget, wanted.Icon = true, ""
				out.Changes = append(out.Changes, fmt.Sprintf("%s its icon, which was %s", done("took away", "would take away"), saved.Icon))
			}
			if icon != "" && icon != saved.Icon {
				sends, request.Icon, wanted.Icon = true, icon, icon
				out.Changes = append(out.Changes, fmt.Sprintf("%s its icon to %s", done("changed", "would change"), icon))
			}
		}
		if len(out.Changes) == 0 {
			if name == "" && colour == "" && in.Icon == nil {
				return nil, out, errors.New("nothing to change was given: name, color or icon")
			}
			out.Note = "nothing to do: it is that way already"

			return nil, out, nil
		}
		if in.DryRun {
			return nil, out, nil
		}

		if sends {
			changed, failed := r.dock.PutTagsById(ctx, saved.Id, request)
			if failed != nil || !changed.Answer.Success {
				out.Changes = []string{}

				return nil, out, fmt.Errorf("the tag %s was not changed: %w", saved.Name, cmp.Or(refusal(failed), errNotSaid))
			}
		}
		if forget {
			forgot, failed := r.dock.ForgetTagIcon(ctx, int(saved.Id))
			if failed != nil || !forgot.Answer.Success {
				return nil, out, fmt.Errorf("the icon of the tag %s was not taken away, and anything else asked for was done: %w", saved.Name, cmp.Or(refusal(failed), errNotSaid))
			}
		}
		after, err := r.tags(ctx)
		if err != nil {
			out.Tag = nil
			out.Note = "it was changed, and could not be read back: " + err.Error()

			return nil, out, nil //nolint:nilerr // the change went through: that it could not be read back is said in the answer
		}
		i := slices.IndexFunc(after, func(t dockhand.GetTagsAnswerTags) bool { return t.Id == saved.Id })
		if i < 0 {
			return nil, out, fmt.Errorf("the change to the tag %s was answered as done, and the Dockhand keeps no tag numbered %d any more", saved.Name, saved.Id)
		}
		row = tagRowOf(&after[i])
		if row.Name != wanted.Name || row.Color != wanted.Color || row.Icon != wanted.Icon {
			out.Note = fmt.Sprintf("Dockhand answered that it was done, and what it has is not all that was asked: the tag is named %s, is %s and has %s", row.Name, row.Color, map[bool]string{true: "the icon " + row.Icon, false: "no icon"}[row.Icon != ""])
		}

		return nil, out, nil
	})

	type tagDeleteIn struct {
		tagArg

		Confirm bool `json:"confirm,omitempty" jsonschema:"remove it: without this the answer says what would go and nothing is removed"`
	}
	type tagDeleteOut struct {
		Tag     string   `json:"tag"`
		Removed bool     `json:"removed"            jsonschema:"the tag was removed, and is read back as gone"`
		WouldGo []string `json:"would_go,omitempty" jsonschema:"what removing it takes away, said before anything is removed"`
		Note    string   `json:"note,omitempty"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "tag_delete",
		Description: "Remove a tag from the Dockhand: it is taken from every container and every compose stack that has it, on every environment, and is gone from the list. No container or stack is otherwise touched. To take a tag from one thing and keep it, use tag_assign. Without confirm the answer says what has the tag and removes nothing.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in tagDeleteIn) (*mcp.CallToolResult, tagDeleteOut, error) {
		listed, err := r.tags(ctx)
		if err != nil {
			return nil, tagDeleteOut{}, err
		}
		saved, err := tagIn(listed, in.Tag)
		if err != nil {
			return nil, tagDeleteOut{}, err
		}
		out := tagDeleteOut{Tag: saved.Name}

		// what has it, which is said before it goes and counted once it has
		var unread string
		containers, stacks := 0, 0
		on, failed, err := r.tagsAcross(ctx, envArg{}, []dockhand.GetTagsAnswerTags{*saved})
		switch {
		case err != nil:
			unread = "what has the tag could not be read: " + err.Error()
		case len(failed) > 0:
			names := make([]string, 0, len(failed))
			for _, f := range failed {
				names = append(names, f.Env)
			}
			unread = "what has the tag on " + strings.Join(names, " and ") + " could not be read"
		}
		for _, here := range on[int(saved.Id)] {
			containers, stacks = containers+len(here.Containers), stacks+len(here.Stacks)
		}
		if !in.Confirm {
			out.WouldGo = []string{"the tag " + saved.Name}
			for _, here := range on[int(saved.Id)] {
				var from []string
				if len(here.Containers) > 0 {
					from = append(from, several(len(here.Containers), "container")+", "+strings.Join(here.Containers, ", "))
				}
				if len(here.Stacks) > 0 {
					from = append(from, several(len(here.Stacks), "stack")+", "+strings.Join(here.Stacks, ", "))
				}
				out.WouldGo = append(out.WouldGo, fmt.Sprintf("on %s, its place on %s", here.Env, strings.Join(from, "; and on ")))
			}
			out.Note = "nothing was removed: call again with confirm to remove it. No container or stack is otherwise touched"
			if unread != "" {
				out.Note = note(out.Note, unread)
			}

			return nil, out, nil
		}

		gone, err := r.dock.DeleteTagsById(ctx, saved.Id)
		if err != nil {
			return nil, out, fmt.Errorf("the tag %s was not removed: %w", saved.Name, refusal(err))
		}
		if !gone.Answer.Success {
			return nil, out, fmt.Errorf("the removal of the tag %s was answered without its being said to have succeeded: tag_list says whether it is still there", saved.Name)
		}
		left, err := r.tags(ctx)
		if err != nil {
			out.Note = "it was removed, and the tags could not be listed afterwards: " + err.Error()

			return nil, out, nil //nolint:nilerr // the removal went through: that it could not be read back is said in the answer
		}
		if slices.ContainsFunc(left, func(t dockhand.GetTagsAnswerTags) bool { return t.Id == saved.Id }) {
			return nil, out, fmt.Errorf("the removal of the tag %s was answered as done, and it is still listed", saved.Name)
		}
		out.Removed = true
		if containers+stacks > 0 {
			out.Note = fmt.Sprintf("it was on %s and %s, and is on none now", several(containers, "container"), several(stacks, "stack"))
		}
		if unread != "" {
			out.Note = note(out.Note, unread)
		}

		return nil, out, nil
	})

	type tagAssignIn struct {
		envArg
		dryRunArg

		Container string `json:"container,omitempty" jsonschema:"the container whose tags to set, by its name as container_list gives it; this or stack"`
		Stack     string `json:"stack,omitempty"     jsonschema:"the compose stack whose tags to set, by its name as stack_list gives it; this or container"`
		Tags      []any  `json:"tags,omitempty"      jsonschema:"every tag it is to have, each by name or number, in place of the ones it has; an empty list takes every tag from it"`
		Add       []any  `json:"add,omitempty"       jsonschema:"tags to give it beside the ones it has, each by name or number"`
		Remove    []any  `json:"remove,omitempty"    jsonschema:"tags to take from it, each by name or number"`
	}
	type tagAssignOut struct {
		Env       string   `json:"env"`
		Container string   `json:"container,omitempty"`
		Stack     string   `json:"stack,omitempty"`
		Tags      []string `json:"tags"                jsonschema:"the tags it has now, by name, read back; for a dry run, the ones it has before anything is done"`
		Changes   []string `json:"changes"             jsonschema:"what was done, in words; empty when nothing needed doing"`
		DryRun    bool     `json:"dry_run,omitempty"   jsonschema:"nothing was done: changes says what would be"`
		Note      string   `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "tag_assign",
		Description: "Set the tags of one container or one compose stack on an environment (Docker host): give tags for every tag it is to have, or add and remove to give and take some and keep the rest. The tags are the ones tag_list gives, made with tag_create. Nothing on the Docker host is changed: a tag is only Dockhand's own note. " + tagsByName + ". So a name that is no container's or stack's there is refused, which Dockhand would save tags under all the same; a name that holds tags from when something had it can have them taken away. A tag there is none of is refused too: Dockhand answers that with an error of its own, having first taken away every tag the name had. What it has is read back afterwards, and put back if Dockhand refused the change and left it with none.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in tagAssignIn) (*mcp.CallToolResult, tagAssignOut, error) {
		container, stack := strings.TrimSpace(in.Container), strings.TrimSpace(in.Stack)
		if (container == "") == (stack == "") {
			return nil, tagAssignOut{}, errors.New("give container or stack, one of them: the name of what the tags are of")
		}
		if in.Tags == nil && len(in.Add)+len(in.Remove) == 0 {
			return nil, tagAssignOut{}, errors.New("nothing to set was given: tags for every tag it is to have, or add and remove")
		}
		if in.Tags != nil && len(in.Add)+len(in.Remove) > 0 {
			return nil, tagAssignOut{}, errors.New("tags is every tag it is to have, so it is not given with add or remove: give one or the other")
		}
		env, err := r.one(ctx, in.envArg)
		if err != nil {
			return nil, tagAssignOut{}, err
		}
		listed, err := r.tags(ctx)
		if err != nil {
			return nil, tagAssignOut{}, err
		}
		numbers := func(given []any) ([]int, error) {
			ids := make([]int, 0, len(given))
			for _, one := range given {
				tag, unknown := tagIn(listed, one)
				if unknown != nil {
					return nil, unknown
				}
				if !slices.Contains(ids, int(tag.Id)) {
					ids = append(ids, int(tag.Id))
				}
			}

			return ids, nil
		}
		whole, err := numbers(in.Tags)
		if err != nil {
			return nil, tagAssignOut{}, err
		}
		adds, err := numbers(in.Add)
		if err != nil {
			return nil, tagAssignOut{}, err
		}
		removes, err := numbers(in.Remove)
		if err != nil {
			return nil, tagAssignOut{}, err
		}
		if i := slices.IndexFunc(adds, func(id int) bool { return slices.Contains(removes, id) }); i >= 0 {
			return nil, tagAssignOut{}, fmt.Errorf("the tag numbered %d is in both add and remove: it is given or taken, not both", adds[i])
		}

		// what it is, whether it is there, and how its tags are read and set
		out := tagAssignOut{Env: env.Name, Container: container, Stack: stack, Tags: []string{}, Changes: []string{}, DryRun: in.DryRun}
		what, name, lister := "container", container, "container_list"
		isThere := func() (bool, error) {
			found, failed := r.containerNamed(ctx, env, container)

			return found != nil, failed
		}
		read := func() ([]int, error) {
			now, failed := r.dock.GetContainerTagsByName(ctx, container, dockhand.GetContainerTagsByNameOptions{Env: env.ID})
			if failed != nil {
				return nil, refusal(failed)
			}

			return client.IDs[int](now.Answer.TagIds), nil
		}
		write := func(ids []int) ([]int, error) {
			set, failed := r.dock.PutContainerTagsByName(ctx, container, dockhand.PutContainerTagsByNameRequest{TagIds: client.IDs[dockhand.TagID](ids)}, dockhand.PutContainerTagsByNameOptions{Env: env.ID})
			if failed != nil {
				return nil, refusal(failed)
			}

			return client.IDs[int](set.Answer.TagIds), nil
		}
		if stack != "" {
			what, name, lister = "stack", stack, "stack_list"
			isThere = func() (bool, error) {
				stacks, failed := r.stacksOf(ctx, env)

				return slices.ContainsFunc(stacks, func(s dockhand.GetStacksAnswer) bool { return s.Name == stack }), failed
			}
			read = func() ([]int, error) {
				now, failed := r.dock.GetStacksByNameTags(ctx, stack, dockhand.GetStacksByNameTagsOptions{Env: env.ID})
				if failed != nil {
					return nil, refusal(failed)
				}

				return client.IDs[int](now.Answer.TagIds), nil
			}
			write = func(ids []int) ([]int, error) {
				set, failed := r.dock.PutStacksByNameTags(ctx, stack, dockhand.PutStacksByNameTagsRequest{TagIds: client.IDs[dockhand.TagID](ids)}, dockhand.PutStacksByNameTagsOptions{Env: env.ID})
				if failed != nil {
					return nil, refusal(failed)
				}

				return client.IDs[int](set.Answer.TagIds), nil
			}
		}
		there, err := isThere()
		if err != nil {
			return nil, out, fmt.Errorf("the %ss of %s could not be listed: %w", what, env.Name, err)
		}
		has, err := read()
		if err != nil {
			return nil, out, fmt.Errorf("the tags of %s on %s could not be read: %w", name, env.Name, err)
		}
		called := func(ids []int) []string {
			names := make([]string, 0, len(ids))
			for _, id := range ids {
				if i := slices.IndexFunc(listed, func(t dockhand.GetTagsAnswerTags) bool { return int(t.Id) == id }); i >= 0 {
					names = append(names, listed[i].Name)
				} else {
					names = append(names, strconv.Itoa(id))
				}
			}
			slices.SortFunc(names, func(a, b string) int { return cmp.Compare(strings.ToLower(a), strings.ToLower(b)) })

			return names
		}
		out.Tags = called(has)

		wanted := whole
		if in.Tags == nil {
			wanted = slices.DeleteFunc(slices.Clone(has), func(id int) bool { return slices.Contains(removes, id) })
			for _, id := range adds {
				if !slices.Contains(wanted, id) {
					wanted = append(wanted, id)
				}
			}
		}
		given := slices.DeleteFunc(slices.Clone(wanted), func(id int) bool { return slices.Contains(has, id) })
		taken := slices.DeleteFunc(slices.Clone(has), func(id int) bool { return slices.Contains(wanted, id) })
		if !there {
			if len(has) == 0 {
				return nil, out, fmt.Errorf("no %s is named %q on %s: %s gives the ones there are. Dockhand would save tags under the name all the same", what, name, env.Name, lister)
			}
			if len(given) > 0 {
				return nil, out, fmt.Errorf("no %s is named %q on %s now, and the name holds the tags %s from when one was: they can be taken from it, and none is given to it", what, name, env.Name, strings.Join(out.Tags, ", "))
			}
		}

		done := func(did, would string) string {
			if in.DryRun {
				return would
			}

			return did
		}
		if len(given) > 0 {
			out.Changes = append(out.Changes, fmt.Sprintf("%s it %s", done("gave", "would give"), strings.Join(called(given), ", ")))
		}
		if len(taken) > 0 {
			out.Changes = append(out.Changes, fmt.Sprintf("%s %s from it", done("took", "would take"), strings.Join(called(taken), ", ")))
		}
		if !there {
			out.Note = fmt.Sprintf("no %s is named %s on %s now: the name holds tags from when one was", what, name, env.Name)
		}
		if len(out.Changes) == 0 {
			out.Note = note(out.Note, "nothing to do: it has exactly those already")

			return nil, out, nil
		}
		if in.DryRun {
			return nil, out, nil
		}

		// sent as a list even when it is empty: Dockhand takes a list that is left out as none too, and an empty one says what is meant
		slices.Sort(wanted)
		back, failed := write(append([]int{}, wanted...))
		if failed != nil {
			out.Changes = []string{}
			left, unread := read()
			if unread != nil {
				return nil, out, fmt.Errorf("the tags of %s on %s were not set: %w. %s, and what it has now could not be read", name, env.Name, failed, takenFirst)
			}
			out.Tags = called(left)
			if sameTags(left, has) {
				return nil, out, fmt.Errorf("the tags of %s on %s were not set, and it has the ones it had: %w", name, env.Name, failed)
			}
			if _, again := write(append([]int{}, has...)); again != nil {
				return nil, out, fmt.Errorf("the tags of %s on %s were not set: %w. %s: it had %s and has %s now, and they could not be put back: %s", name, env.Name, failed, takenFirst, cmp.Or(strings.Join(called(has), ", "), "none"), cmp.Or(strings.Join(out.Tags, ", "), "none"), again.Error())
			}
			out.Tags = called(has)

			return nil, out, fmt.Errorf("the tags of %s on %s were not set: %w. %s, so the ones it had were put back: it has %s again", name, env.Name, failed, takenFirst, strings.Join(out.Tags, ", "))
		}
		if now, unread := read(); unread == nil {
			back = now
		} else {
			out.Note = note(out.Note, "they were set, and could not be read back: tags is what Dockhand answered the change with")
		}
		out.Tags = called(back)
		if !sameTags(back, wanted) {
			return nil, out, fmt.Errorf("the tags of %s on %s were answered as set, and what it has is not what was asked: it has %s", name, env.Name, cmp.Or(strings.Join(out.Tags, ", "), "none"))
		}

		return nil, out, nil
	})
}
