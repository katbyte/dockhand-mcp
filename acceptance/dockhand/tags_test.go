//go:build acceptance

package dockhand_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/pandorest/client"
)

// tagColours is every colour a 1.0.51 server takes for a tag, which is what the tools check one against before they send it.
var tagColours = []string{"slate", "red", "orange", "amber", "lime", "green", "emerald", "teal", "cyan", "sky", "blue", "indigo", "violet", "purple", "fuchsia", "pink"}

// sameTags is whether two lists name the same tags, in whatever order: a server gives them back in an order of its own.
func sameTags(got []int, want ...int) bool {
	got = slices.Clone(got)
	slices.Sort(got)
	slices.Sort(want)

	return slices.Equal(slices.Compact(got), want)
}

// Tags are made, changed and removed, and given to a container and to a stack by name: the colours a server takes and what it does with another, what
// it answers of a tag there is none of, that a name keeps its tags when nothing has it any more, and that a change it refuses takes every tag a name
// had.
func TestTagsMadeChangedGivenAndRemoved(t *testing.T) {
	const first, second, nobody = "it-tag-first", "it-tag-second", 999999
	c := dock(t)
	ctx := t.Context()
	env, other := world.Env(testenv.Alpha), world.Env(testenv.Beta)

	tags := func() map[string]dockhand.GetTagsAnswerTags {
		listed, err := c.GetTags(ctx)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]dockhand.GetTagsAnswerTags{}
		for _, tag := range listed.Answer.Tags {
			out[tag.Name] = tag
		}

		return out
	}
	clean := func() {
		listed, err := c.GetTags(noCancel(t))
		if err != nil {
			return
		}
		for _, tag := range listed.Answer.Tags {
			if strings.HasPrefix(strings.ToLower(tag.Name), "it-tag-") {
				_, _ = c.DeleteTagsById(noCancel(t), tag.Id)
			}
		}
	}
	clean()
	t.Cleanup(clean)
	before := len(tags())

	// one with no name is refused, and one is made with the colour and the icon given
	if _, err := c.PostTags(ctx, dockhand.PostTagsRequest{Color: "red"}); client.StatusCode(err) != http.StatusBadRequest {
		t.Errorf("a tag with no name = %v, want a 400", err)
	}
	made, err := c.PostTags(ctx, dockhand.PostTagsRequest{Name: first, Color: "teal", Icon: "globe"})
	if err != nil || made.Answer.Id == 0 || made.Answer.Name != first || made.Answer.Color != "teal" || made.Answer.Icon != "globe" {
		t.Fatalf("a tag made = %+v, %v", made.Answer, err)
	}
	id := made.Answer.Id

	// made again under the same name in another case, it is the one there is that is answered, unchanged
	if again, err := c.PostTags(ctx, dockhand.PostTagsRequest{Name: "  " + strings.ToUpper(first) + " ", Color: "red"}); err != nil || again.Answer.Id != id || again.Answer.Name != first || again.Answer.Color != "teal" || len(tags()) != before+1 {
		t.Errorf("a tag made a second time = %+v, %v, with %d tags: Dockhand answers with the one it has", again.Answer, err, len(tags()))
	}
	// a colour that is none of its own is made slate, and so is none given
	odd, err := c.PostTags(ctx, dockhand.PostTagsRequest{Name: second, Color: "yellow"})
	if err != nil || odd.Answer.Color != "slate" || odd.Answer.Icon != "" {
		t.Fatalf("a tag made in a colour Dockhand has not = %+v, %v, want it slate", odd.Answer, err)
	}
	for _, colour := range tagColours {
		one, err := c.PostTags(ctx, dockhand.PostTagsRequest{Name: "it-tag-colour-" + colour, Color: colour})
		if err != nil || one.Answer.Color != colour {
			t.Errorf("a tag made %s = %+v, %v", colour, one.Answer, err)

			continue
		}
		if gone, err := c.DeleteTagsById(ctx, one.Answer.Id); err != nil || !gone.Answer.Success {
			t.Errorf("removing the %s tag = %+v, %v", colour, gone.Answer, err)
		}
	}

	// changed: what is sent is set and the rest kept, the name with the spaces round it taken off
	if changed, err := c.PutTagsById(ctx, id, dockhand.PutTagsByIdRequest{Name: " " + first + "-renamed ", Color: "pink"}); err != nil || !changed.Answer.Success {
		t.Fatalf("a tag changed = %+v, %v", changed.Answer, err)
	}
	renamed := first + "-renamed"
	if now := tags()[renamed]; now.Id != id || now.Color != "pink" || now.Icon != "globe" {
		t.Errorf("the tag once changed = %+v", now)
	}
	// a colour that is none of its own is answered as done and the colour kept
	if kept, err := c.PutTagsById(ctx, id, dockhand.PutTagsByIdRequest{Color: "yellow"}); err != nil || !kept.Answer.Success || tags()[renamed].Color != "pink" {
		t.Errorf("a tag changed to a colour Dockhand has not = %+v, %v, and it is %s: Dockhand answers that it was and keeps the colour", kept.Answer, err, tags()[renamed].Color)
	}
	// another tag's name is refused whatever its case, and a tag there is none of is answered as changed
	if _, err := c.PutTagsById(ctx, id, dockhand.PutTagsByIdRequest{Name: strings.ToUpper(second)}); client.StatusCode(err) != http.StatusConflict {
		t.Errorf("a tag renamed to another's name = %v, want a 409", err)
	}
	if ghost, err := c.PutTagsById(ctx, nobody, dockhand.PutTagsByIdRequest{Name: "it-tag-nobody"}); err != nil || !ghost.Answer.Success || len(tags()) != before+2 {
		t.Errorf("a tag there is none of, changed = %+v, %v, with %d tags: Dockhand answers that it was", ghost.Answer, err, len(tags()))
	}
	// its icon is forgotten when it is sent empty, which the generated call leaves out
	if forgot, err := c.ForgetTagIcon(ctx, int(id)); err != nil || !forgot.Answer.Success || tags()[renamed].Icon != "" || tags()[renamed].Color != "pink" {
		t.Errorf("the icon forgotten = %+v, %v, leaving %+v", forgot.Answer, err, tags()[renamed])
	}

	// given to a container by its name, on one environment: read for the one name and for every name
	name := started(t, testenv.Alpha, "tagged")
	here := dockhand.PutContainerTagsByNameOptions{Env: env}
	held := func(of string, on int) []int {
		one, err := c.GetContainerTagsByName(ctx, of, dockhand.GetContainerTagsByNameOptions{Env: dockhand.EnvironmentID(on)})
		if err != nil {
			t.Fatal(err)
		}

		return client.IDs[int](one.Answer.TagIds)
	}
	every := func() map[string][]int {
		all, err := c.GetContainerTags(ctx, dockhand.GetContainerTagsOptions{Env: env})
		if err != nil {
			t.Fatal(err)
		}

		return all.Answer
	}
	if set, err := c.PutContainerTagsByName(ctx, name, dockhand.PutContainerTagsByNameRequest{TagIds: []dockhand.TagID{odd.Answer.Id, id, id}}, here); err != nil || !sameTags(client.IDs[int](set.Answer.TagIds), int(id), int(odd.Answer.Id)) || !sameTags(held(name, int(env)), int(id), int(odd.Answer.Id)) || !sameTags(every()[name], int(id), int(odd.Answer.Id)) {
		t.Fatalf("a container's tags set = %+v, %v; read back %v, and among every name's %v", set.Answer, err, held(name, int(env)), every()[name])
	}
	if elsewhere := held(name, int(other)); len(elsewhere) != 0 {
		t.Errorf("the tags the name has on another environment = %v, want none", elsewhere)
	}

	// a change that names a tag there is none of is refused, and the name is left with no tag at all
	if _, err := c.PutContainerTagsByName(ctx, name, dockhand.PutContainerTagsByNameRequest{TagIds: []dockhand.TagID{id, nobody}}, here); client.StatusCode(err) != http.StatusInternalServerError {
		t.Errorf("a container given a tag there is none of = %v, want a 500", err)
	}
	if left := held(name, int(env)); len(left) != 0 {
		t.Errorf("a refused change left the container with %v: it used to leave none, which is why the tools put back what a name had", left)
	}
	if _, err := c.PutContainerTagsByName(ctx, name, dockhand.PutContainerTagsByNameRequest{TagIds: []dockhand.TagID{id}}, here); err != nil {
		t.Fatal(err)
	}

	// the tags are the name's and not the container's: renamed, it leaves them behind, and removed, the name holds them still
	if _, err := world.Docker(ctx, testenv.Alpha, "rename", name, name+"-moved"); err != nil {
		t.Fatal(err)
	}
	if !sameTags(held(name, int(env)), int(id)) || len(held(name+"-moved", int(env))) != 0 {
		t.Errorf("once renamed, the name it had holds %v and the one it has %v: the tags used to stay behind", held(name, int(env)), held(name+"-moved", int(env)))
	}
	if _, err := world.Docker(ctx, testenv.Alpha, "rm", "-f", name+"-moved"); err != nil {
		t.Fatal(err)
	}
	if !sameTags(every()[name], int(id)) {
		t.Errorf("once the container is removed the name holds %v: it used to keep its tags", every()[name])
	}
	// a name nothing has is given tags all the same, and an empty list takes them away
	if ghost, err := c.PutContainerTagsByName(ctx, name, dockhand.PutContainerTagsByNameRequest{TagIds: []dockhand.TagID{id, odd.Answer.Id}}, here); err != nil || !sameTags(client.IDs[int](ghost.Answer.TagIds), int(id), int(odd.Answer.Id)) {
		t.Errorf("a name no container has, given tags = %+v, %v: Dockhand saves them", ghost.Answer, err)
	}
	if none, err := c.PutContainerTagsByName(ctx, name, dockhand.PutContainerTagsByNameRequest{TagIds: []dockhand.TagID{}}, here); err != nil || len(none.Answer.TagIds) != 0 || every()[name] != nil {
		t.Errorf("every tag taken from the name = %+v, %v, leaving %v", none.Answer, err, every()[name])
	}

	// a stack's are kept the same way, under a name no stack need have
	stack := named(t, "no-such-stack")
	there := dockhand.PutStacksByNameTagsOptions{Env: env}
	stackHeld := func() []int {
		one, err := c.GetStacksByNameTags(ctx, stack, dockhand.GetStacksByNameTagsOptions{Env: env})
		if err != nil {
			t.Fatal(err)
		}

		return client.IDs[int](one.Answer.TagIds)
	}
	everyStack := func() map[string][]int {
		all, err := c.GetStackTags(ctx, dockhand.GetStackTagsOptions{Env: env})
		if err != nil {
			t.Fatal(err)
		}

		return all.Answer
	}
	if set, err := c.PutStacksByNameTags(ctx, stack, dockhand.PutStacksByNameTagsRequest{TagIds: []dockhand.TagID{id, odd.Answer.Id}}, there); err != nil || !sameTags(client.IDs[int](set.Answer.TagIds), int(id), int(odd.Answer.Id)) || !sameTags(stackHeld(), int(id), int(odd.Answer.Id)) || !sameTags(everyStack()[stack], int(id), int(odd.Answer.Id)) {
		t.Fatalf("a stack's tags set = %+v, %v; read back %v, and among every name's %v", set.Answer, err, stackHeld(), everyStack()[stack])
	}
	if _, err := c.PutStacksByNameTags(ctx, stack, dockhand.PutStacksByNameTagsRequest{TagIds: []dockhand.TagID{nobody}}, there); client.StatusCode(err) != http.StatusInternalServerError || len(stackHeld()) != 0 {
		t.Errorf("a stack given a tag there is none of = %v, leaving it %v: want a 500 and no tag left", err, stackHeld())
	}
	if _, err := c.PutStacksByNameTags(ctx, stack, dockhand.PutStacksByNameTagsRequest{TagIds: []dockhand.TagID{id, odd.Answer.Id}}, there); err != nil {
		t.Fatal(err)
	}

	// a tag removed is taken from everything that had it, and one there is none of is answered as removed
	if gone, err := c.DeleteTagsById(ctx, id); err != nil || !gone.Answer.Success || !sameTags(stackHeld(), int(odd.Answer.Id)) || tags()[renamed].Id != 0 {
		t.Errorf("a tag removed = %+v, %v, leaving the stack %v", gone.Answer, err, stackHeld())
	}
	if ghost, err := c.DeleteTagsById(ctx, nobody); err != nil || !ghost.Answer.Success {
		t.Errorf("a tag there is none of, removed = %+v, %v: Dockhand answers that it was", ghost.Answer, err)
	}
	if gone, err := c.DeleteTagsById(ctx, odd.Answer.Id); err != nil || !gone.Answer.Success || everyStack()[stack] != nil || len(tags()) != before {
		t.Errorf("the other tag removed = %+v, %v, leaving the stack %v and %d tags, want none and the %d there were", gone.Answer, err, everyStack()[stack], len(tags()), before)
	}
}
