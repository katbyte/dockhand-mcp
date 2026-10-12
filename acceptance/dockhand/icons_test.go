//go:build acceptance

package dockhand_test

import (
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/pandorest/client"
)

// onePixel is a picture of one pixel, as a PNG and as the data: address Dockhand takes a picture as.
const onePixel = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

// pictureOf reads what a call that answers a picture sent: what kind it says it is and how much of it there is.
func pictureOf(t *testing.T, answer *http.Response, err error) (kind string, size int) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = answer.Body.Close() }()
	body, err := io.ReadAll(answer.Body)
	if err != nil {
		t.Fatal(err)
	}
	kind, _, _ = strings.Cut(answer.Header.Get("Content-Type"), ";")

	return kind, len(body)
}

// A container is given an icon by name and as a picture: what Dockhand saves without looking, what it refuses, that the icon is the name's and not
// the container's, and what removing it answers.
func TestContainerIconsGivenFetchedAndRemoved(t *testing.T) {
	c := dock(t)
	ctx := t.Context()
	env := world.Env(testenv.Alpha)
	name := started(t, testenv.Alpha, "pictured")
	ghost := named(t, "no-such-container")
	here := dockhand.PostContainerIconsByNameOperationOptions{Env: &env}
	icons := func() map[string]string {
		given, err := c.GetContainerIcons(ctx, dockhand.GetContainerIconsOperationOptions{Env: &env})
		if err != nil {
			t.Fatal(err)
		}

		return given.Model
	}
	t.Cleanup(func() {
		for _, held := range []string{name, ghost} {
			_, _ = c.DeleteContainerIconsByName(noCancel(t), held, dockhand.DeleteContainerIconsByNameOperationOptions{Env: &env})
		}
	})

	// by name, any name: one of Dockhand's own, one of selfh.st's, and one that is nothing's are saved alike
	for _, icon := range []string{"box", "selfhst:docker", "it-no-such-icon-anywhere", "two words"} {
		if set, err := c.PostContainerIconsByName(ctx, name, dockhand.PostContainerIconsByNameRequest{Icon: icon}, here); err != nil || !yes(set.Model.Success) || set.Model.Icon != icon || icons()[name] != icon {
			t.Errorf("a container given the icon %q = %+v, %v, read back as %q: Dockhand used to save any name", icon, set.Model, err, icons()[name])
		}
	}
	// one given by name has no picture to fetch
	if _, err := c.GetContainerIconsByName(ctx, name, dockhand.GetContainerIconsByNameOperationOptions{Env: &env}); !client.IsNotFound(err) {
		t.Errorf("the picture of an icon that is a name = %v, want a 404", err)
	}
	// neither a name nor a picture is refused, and so is a picture that is none
	for what, request := range map[string]dockhand.PostContainerIconsByNameRequest{"nothing": {}, "text for a picture": {Image: "not a picture"}, "text said to be a picture": {Image: "data:image/png;base64,aGVsbG8="}} {
		if _, err := c.PostContainerIconsByName(ctx, name, request, here); client.StatusCode(err) != http.StatusBadRequest {
			t.Errorf("a container given %s = %v, want a 400", what, err)
		}
	}

	// a picture is kept as Dockhand's own kind whatever it was sent as, and wins over a name sent with it
	if set, err := c.PostContainerIconsByName(ctx, name, dockhand.PostContainerIconsByNameRequest{Icon: "box", Image: onePixel}, here); err != nil || set.Model.Icon != "custom:container" || icons()[name] != "custom:container" {
		t.Fatalf("a container given a picture = %+v, %v", set.Model, err)
	}
	fetched, err := c.GetContainerIconsByName(ctx, name, dockhand.GetContainerIconsByNameOperationOptions{Env: &env})
	if kind, size := pictureOf(t, fetched.HttpResponse, err); kind != "image/webp" || size == 0 {
		t.Errorf("the picture fetched is %d bytes of %s, want some of image/webp", size, kind)
	}

	// the icon is the name's: one no container has is given an icon all the same, and an environment there is none of is not
	if set, err := c.PostContainerIconsByName(ctx, ghost, dockhand.PostContainerIconsByNameRequest{Icon: "box"}, here); err != nil || icons()[ghost] != "box" {
		t.Errorf("a name no container has, given an icon = %+v, %v: Dockhand used to keep it, which is why the tools look first", set.Model, err)
	}
	if _, err := c.PostContainerIconsByName(ctx, name, dockhand.PostContainerIconsByNameRequest{Icon: "box"}, dockhand.PostContainerIconsByNameOperationOptions{Env: new(999999)}); client.StatusCode(err) != http.StatusInternalServerError {
		t.Errorf("an icon on an environment there is none of = %v, want a 500", err)
	}

	// removed, and removed again, are both answered as done
	for range 2 {
		if gone, err := c.DeleteContainerIconsByName(ctx, name, dockhand.DeleteContainerIconsByNameOperationOptions{Env: &env}); err != nil || !yes(gone.Model.Success) {
			t.Errorf("a container's icon removed = %+v, %v", gone.Model, err)
		}
	}
	if gone, err := c.DeleteContainerIconsByName(ctx, ghost, dockhand.DeleteContainerIconsByNameOperationOptions{Env: &env}); err != nil || !yes(gone.Model.Success) || icons()[name] != "" || icons()[ghost] != "" {
		t.Errorf("the other removed = %+v, %v, leaving %v", gone.Model, err, icons())
	}
}

// A stack is given an icon, which makes Dockhand keep a record of a stack it only found: what that record is, that it outlasts the icon, that
// Dockhand's own adoption then makes a second stack of the file, that saving the file where it lies gives the record its file, and that a name no
// stack has is listed as a stack once it is given an icon.
func TestStackIconsAndTheRecordTheyLeave(t *testing.T) {
	c := dock(t)
	const host = testenv.Alpha
	env := world.Env(host)
	stack, ghost := named(t, "s"), named(t, "no-such-stack")
	twin := stack + "-1"
	dir := laidUnder + "/" + stack
	path := dir + "/compose.yaml"
	file := "name: " + stack + "\nservices:\n  app:\n    image: " + world.Image + "\n    command: [\"sleep\", \"100000\"]\n    stop_grace_period: 1s\n"
	ctx := noCancel(t)
	here := dockhand.PostStacksByNameIconOperationOptions{Env: &env}
	for _, made := range []string{stack, twin, ghost} {
		forgotten(t, host, made)
	}
	_ = world.Unlay(ctx, dir)
	t.Cleanup(func() { _ = world.Unlay(ctx, dir) })
	if err := world.Compose(ctx, host, stack, file); err != nil {
		t.Fatal(err)
	}
	if err := world.Lay(ctx, path, file); err != nil {
		t.Fatal(err)
	}
	kept := func(name string) (dockhand.GetStacksSourcesResponse, bool) {
		sources, err := c.GetStacksSources(ctx, dockhand.GetStacksSourcesOperationOptions{Env: &env})
		if err != nil {
			t.Fatal(err)
		}
		source, has := sources.Model[name]

		return source, has
	}
	listed := func(name string) *dockhand.GetStacksResponse {
		stacks, err := c.GetStacks(ctx, dockhand.GetStacksOperationOptions{Env: &env})
		if err != nil {
			t.Fatal(err)
		}
		if i := slices.IndexFunc(stacks.Model, func(s dockhand.GetStacksResponse) bool { return s.Name == name }); i >= 0 {
			return &stacks.Model[i]
		}

		return nil
	}

	// as found it has no record, and given an icon it has one, with no file: external is Dockhand's word for that
	if _, has := kept(stack); has {
		t.Fatal("a stack Dockhand only found has a record before it is given anything")
	}
	if set, err := c.PostStacksByNameIcon(ctx, stack, dockhand.PostStacksByNameIconRequest{Icon: "box"}, here); err != nil || !yes(set.Model.Success) || set.Model.Icon != "box" {
		t.Fatalf("a stack given an icon = %+v, %v", set.Model, err)
	}
	if source, has := kept(stack); !has || source.SourceType != "external" || source.ComposePath != "" || source.Icon != "box" || listed(stack).Icon != "box" {
		t.Errorf("the record of a found stack once given an icon = %+v, %v, and it is listed with the icon %q: Dockhand used to keep one as external with no file", source, has, listed(stack).Icon)
	}
	if _, err := c.PostStacksByNameIcon(ctx, stack, dockhand.PostStacksByNameIconRequest{}, here); client.StatusCode(err) != http.StatusBadRequest {
		t.Errorf("a stack given neither a name nor a picture = %v, want a 400", err)
	}
	// a picture, kept as Dockhand's own kind, and then nothing: the record stays when the icon goes
	if set, err := c.PostStacksByNameIcon(ctx, stack, dockhand.PostStacksByNameIconRequest{Image: onePixel}, here); err != nil || set.Model.Icon != "custom:stack" {
		t.Errorf("a stack given a picture = %+v, %v", set.Model, err)
	}
	fetched, err := c.GetStacksByNameIcon(ctx, stack, dockhand.GetStacksByNameIconOperationOptions{Env: &env})
	if kind, size := pictureOf(t, fetched.HttpResponse, err); kind != "image/webp" || size == 0 {
		t.Errorf("the picture fetched is %d bytes of %s, want some of image/webp", size, kind)
	}
	if gone, err := c.DeleteStacksByNameIcon(ctx, stack, dockhand.DeleteStacksByNameIconOperationOptions{Env: &env}); err != nil || !yes(gone.Model.Success) {
		t.Errorf("a stack's icon removed = %+v, %v", gone.Model, err)
	}
	if source, has := kept(stack); !has || source.SourceType != "external" || source.Icon != "" {
		t.Errorf("the record once the icon is removed = %+v, %v: it used to stay", source, has)
	}

	// Dockhand's own adoption of a stack it has a record of makes a second stack, with no containers, and leaves the first without its file
	adopted, err := c.PostStacksAdopt(ctx, dockhand.PostStacksAdoptRequest{EnvironmentId: env, Stacks: []dockhand.PostStacksAdoptRequestStacks{{Name: stack, ComposePath: path}}})
	if err != nil || !slices.Equal(adopted.Model.Adopted, []string{twin}) {
		t.Errorf("a recorded stack adopted = %+v, %v: Dockhand used to adopt the file as %s, which is why stack_adopt saves the file instead", adopted.Model, err, twin)
	}
	if source, _ := kept(stack); source.SourceType != "external" || listed(twin) == nil || len(listed(twin).Containers) != 0 {
		t.Errorf("after that adoption the stack is kept as %q and its twin is listed as %+v", source.SourceType, listed(twin))
	}
	if gone, err := c.DeleteStacksByName(ctx, twin, dockhand.DeleteStacksByNameOperationOptions{Env: &env, Force: new(true), Files: new(false)}); err != nil || !yes(gone.Model.Success) {
		t.Errorf("removing the twin = %+v, %v", gone.Model, err)
	}
	// saving its file where it lies gives the record its file: it is the same stack, running as it was, and Dockhand has its file
	if saved, err := c.PutStacksByNameCompose(ctx, stack, dockhand.PutStacksByNameComposeRequest{Content: file, ComposePath: path}, dockhand.PutStacksByNameComposeOperationOptions{Env: &env}); err != nil || !yes(saved.Model.Success) {
		t.Fatalf("the file saved for a recorded stack = %+v, %v", saved.Model, err)
	}
	if source, _ := kept(stack); source.SourceType != "internal" || source.ComposePath != path || listed(stack) == nil || listed(stack).Status != "running" || len(listed(stack).Containers) != 1 {
		t.Errorf("once its file is saved the stack is kept as %+v and listed as %+v", source, listed(stack))
	}

	// a name no stack has is given an icon all the same, and is listed as a stack from then on, with nothing in it
	if set, err := c.PostStacksByNameIcon(ctx, ghost, dockhand.PostStacksByNameIconRequest{Icon: "box"}, here); err != nil || set.Model.Icon != "box" {
		t.Errorf("a name no stack has, given an icon = %+v, %v", set.Model, err)
	}
	if phantom := listed(ghost); phantom == nil || len(phantom.Containers) != 0 || phantom.Icon != "box" {
		t.Errorf("a name no stack has, once given an icon, is listed as %+v: Dockhand used to list a stack of that name, which is why the tools look first", phantom)
	}
	if gone, err := c.DeleteStacksByName(ctx, ghost, dockhand.DeleteStacksByNameOperationOptions{Env: &env, Force: new(true)}); err != nil || !yes(gone.Model.Success) || listed(ghost) != nil {
		t.Errorf("removing the stack of nothing = %+v, %v, leaving %+v", gone.Model, err, listed(ghost))
	}
}

// An environment's icon is a name saved with the environment, or a picture sent by a call of its own: that Dockhand takes anything at all as a
// picture for one, and that removing it gives the environment the plain icon whatever it had.
func TestEnvironmentIconsGivenFetchedAndRemoved(t *testing.T) {
	c := dock(t)
	ctx := t.Context()
	env := world.Env(testenv.Beta)
	icon := func() string {
		record, err := c.GetEnvironmentsById(ctx, env)
		if err != nil {
			t.Fatal(err)
		}

		return record.Model.Icon
	}
	was := icon()
	t.Cleanup(func() {
		_, _ = c.DeleteEnvironmentsByIdIcon(noCancel(t), env)
		_, _ = c.PutEnvironmentsById(noCancel(t), env, dockhand.PutEnvironmentsByIdRequest{Icon: was})
	})

	// a name is saved with the environment, whatever name; one given by name has no picture
	for _, name := range []string{"server", "selfhst:docker", "it-no-such-icon-anywhere"} {
		if saved, err := c.PutEnvironmentsById(ctx, env, dockhand.PutEnvironmentsByIdRequest{Icon: name}); err != nil || saved.Model.Icon != name || icon() != name {
			t.Errorf("an environment given the icon %q = %q, %v, read back as %q", name, saved.Model.Icon, err, icon())
		}
	}
	if _, err := c.GetEnvironmentsByIdIcon(ctx, env); !client.IsNotFound(err) {
		t.Errorf("the picture of an environment's icon that is a name = %v, want a 404", err)
	}

	// a picture is kept as Dockhand's own kind, under a name that says whose it is
	set, err := c.PostEnvironmentsByIdIcon(ctx, env, dockhand.PostEnvironmentsByIdIconRequest{Image: onePixel})
	if err != nil || !yes(set.Model.Success) || !strings.HasPrefix(set.Model.Icon, "custom:env-") || icon() != set.Model.Icon {
		t.Fatalf("an environment given a picture = %+v, %v, read back as %q", set.Model, err, icon())
	}
	fetched, err := c.GetEnvironmentsByIdIcon(ctx, env)
	if kind, size := pictureOf(t, fetched.HttpResponse, err); kind != "image/webp" || size == 0 {
		t.Errorf("the picture fetched is %d bytes of %s, want some of image/webp", size, kind)
	}
	// what is no picture at all is taken as one, where a container's and a stack's is refused
	if odd, err := c.PostEnvironmentsByIdIcon(ctx, env, dockhand.PostEnvironmentsByIdIconRequest{Image: "not a picture"}); err != nil || !yes(odd.Model.Success) {
		t.Errorf("an environment given text for a picture = %+v, %v: Dockhand used to take it, which is why the tools look at a picture first", odd.Model, err)
	}
	if _, err := c.PostEnvironmentsByIdIcon(ctx, 999999, dockhand.PostEnvironmentsByIdIconRequest{Image: onePixel}); !client.IsNotFound(err) {
		t.Errorf("a picture for an environment there is none of = %v, want a 404", err)
	}

	// removed, the environment has the plain icon, and has it again when a name is removed
	if gone, err := c.DeleteEnvironmentsByIdIcon(ctx, env); err != nil || !yes(gone.Model.Success) || gone.Model.Icon != "globe" || icon() != "globe" {
		t.Errorf("an environment's picture removed = %+v, %v, leaving %q", gone.Model, err, icon())
	}
	if _, err := c.PutEnvironmentsById(ctx, env, dockhand.PutEnvironmentsByIdRequest{Icon: "server"}); err != nil {
		t.Fatal(err)
	}
	if gone, err := c.DeleteEnvironmentsByIdIcon(ctx, env); err != nil || gone.Model.Icon != "globe" || icon() != "globe" {
		t.Errorf("an environment's named icon removed = %+v, %v, leaving %q", gone.Model, err, icon())
	}
	if _, err := c.DeleteEnvironmentsByIdIcon(ctx, 999999); !client.IsNotFound(err) {
		t.Errorf("removing the icon of an environment there is none of = %v, want a 404", err)
	}
}

// The icons of selfh.st are listed and fetched through Dockhand, which gets them from selfh.st itself: what the list holds, that a reference it has
// no icon for is answered with a picture of nothing rather than refused, and what asking for several at once answers.
func TestSelfhstIconsListedAndFetched(t *testing.T) {
	c := dock(t)
	ctx := t.Context()

	listed, err := c.GetIconsSelfhstManifest(ctx)
	if err != nil || len(listed.Model) < 100 {
		t.Fatalf("the list of selfh.st's icons = %d of them, %v", len(listed.Model), err)
	}
	i := slices.IndexFunc(listed.Model, func(icon dockhand.GetIconsSelfhstManifestResponse) bool { return icon.Reference == "docker" })
	if i < 0 || listed.Model[i].Name == "" || listed.Model[i].Category == "" || listed.Model[i].PNG == "" {
		t.Fatalf("the list has no icon asked for by docker, or says nothing of it: %d", i)
	}

	fetched, err := c.GetIconsSelfhstByRef(ctx, "docker")
	kind, size := pictureOf(t, fetched.HttpResponse, err)
	if !strings.HasPrefix(kind, "image/") || size == 0 {
		t.Errorf("the icon fetched is %d bytes of %s", size, kind)
	}
	// one there is none of is answered with a picture all the same, and a reference that is none is refused
	nothing, err := c.GetIconsSelfhstByRef(ctx, "it-no-such-app-anywhere")
	if kind, empty := pictureOf(t, nothing.HttpResponse, err); kind != "image/svg+xml" || empty == 0 || empty == size {
		t.Errorf("an icon there is none of is answered with %d bytes of %s: Dockhand used to answer a picture of nothing, which is why the tools ask the list first", empty, kind)
	}
	if _, err := c.GetIconsSelfhstByRef(ctx, "Not Valid!"); client.StatusCode(err) != http.StatusBadRequest {
		t.Errorf("a reference that is none = %v, want a 400", err)
	}

	// several at once: each that can be had as a data: address by its reference, and the rest left out
	several, err := c.PostIconsSelfhstBatch(ctx, dockhand.PostIconsSelfhstBatchRequest{Refs: []string{"docker", "it-no-such-app-anywhere"}})
	if err != nil || len(several.Model.Icons) != 1 || !strings.HasPrefix(several.Model.Icons["docker"], "data:image/") {
		t.Errorf("two icons asked for at once = %d of them, %v", len(several.Model.Icons), err)
	}
}
