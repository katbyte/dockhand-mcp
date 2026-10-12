//go:build acceptance

package dockhand_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/pandorest/client"
)

// registryOf is one registry as the server lists it, or nil when it lists none by that name.
func registryOf(t *testing.T, c *dockhand.Client, name string) *dockhand.GetRegistriesAnswer {
	t.Helper()

	listed, err := c.GetRegistries(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if at := slices.IndexFunc(listed.Answer, func(k dockhand.GetRegistriesAnswer) bool { return k.Name == name }); at >= 0 {
		return &listed.Answer[at]
	}

	return nil
}

// A registry is tried before it is saved, changed and removed, and between those an image is pushed to it from one host both ways a push is answered,
// found in it, pulled from it by another host with the login Dockhand keeps, and deleted from it, which takes every tag of the same contents.
func TestRegistrySavedPushedToBrowsedAndRemoved(t *testing.T) {
	c := dock(t)
	if world.Registry == "" {
		t.Skip("the environment names no registry: .scripts/testenv.sh up starts one")
	}
	address := "http://" + world.Registry
	name := named(t, "kept")
	repository := "it/" + named(t, "box")
	pushed := world.Registry + "/" + repository
	const from, to = testenv.Alpha, testenv.Beta
	without(t, from, pushed+":1", pushed+":2")
	without(t, to, pushed+":1")
	if old := registryOf(t, c, name); old != nil {
		_, _ = c.DeleteRegistriesById(t.Context(), old.Id)
	}

	// tried before it is saved: every outcome is an answer, and only no address at all is refused
	for what, tc := range map[string]struct {
		with          dockhand.PostRegistriesTestRequest
		success       bool
		authenticated *bool
		says          string
	}{
		"the right login":       {dockhand.PostRegistriesTestRequest{Url: address, Username: world.User, Password: world.Password}, true, new(true), "authenticated as " + world.User},
		"the wrong password":    {dockhand.PostRegistriesTestRequest{Url: address, Username: world.User, Password: "wrong"}, false, new(false), "401"},
		"no login":              {dockhand.PostRegistriesTestRequest{Url: address}, true, nil, "requires authentication"},
		"an address nowhere":    {dockhand.PostRegistriesTestRequest{Url: "http://nowhere.invalid:5000"}, false, nil, "Connection failed"},
		"an address, no scheme": {dockhand.PostRegistriesTestRequest{Url: world.Registry}, false, nil, "Connection failed"},
	} {
		tried, err := c.PostRegistriesTest(t.Context(), tc.with)
		if err != nil || tried.Answer.Success != tc.success || (tried.Answer.Authenticated == nil) != (tc.authenticated == nil) || (tc.authenticated != nil && *tried.Answer.Authenticated != *tc.authenticated) || !strings.Contains(tried.Answer.Message, tc.says) {
			t.Errorf("tried with %s = %+v, %v", what, tried.Answer, err)
		}
	}
	if _, err := c.PostRegistriesTest(t.Context(), dockhand.PostRegistriesTestRequest{}); client.StatusCode(err) != 400 {
		t.Errorf("trying no registry = %v", err)
	}

	made, err := c.PostRegistries(t.Context(), dockhand.PostRegistriesRequest{Name: name, Url: address, Username: world.User, Password: world.Password})
	if err != nil {
		t.Fatal(err)
	}
	id := made.Answer.Id
	ctx := noCancel(t)
	t.Cleanup(func() {
		if _, err := c.DeleteRegistriesById(ctx, id); err != nil && !client.IsNotFound(err) {
			t.Errorf("removing %s: %v", name, err)
		}
	})
	if id == 0 || made.Answer.Name != name || made.Answer.Url != address || made.Answer.Username != world.User || !made.Answer.HasCredentials || made.Answer.IsDefault || made.Answer.CreatedAt.IsZero() {
		t.Errorf("made = %+v", made.Answer)
	}
	if _, err := c.PostRegistries(t.Context(), dockhand.PostRegistriesRequest{Name: name, Url: address}); client.StatusCode(err) != 400 || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("a second registry named %s = %v", name, err)
	}
	if kept := registryOf(t, c, name); kept == nil || kept.Id != id || kept.Username != world.User || !kept.HasCredentials {
		t.Errorf("as listed = %+v", kept)
	}
	if tried, err := c.PostRegistriesTest(t.Context(), dockhand.PostRegistriesTestRequest{RegistryId: id}); err != nil || !tried.Answer.Success || !yes(tried.Answer.Authenticated) {
		t.Errorf("tried once saved = %+v, %v", tried.Answer, err)
	}

	// an image on one host pushed, the work done before the answer: newTag is the whole name it goes under, and the host's image keeps that name
	started(t, from, "source")
	image := imageOf(t, int(world.Env(from)), world.Image)
	if image == nil {
		t.Fatalf("%s has no %s to push", from, world.Image)
	}
	source := world.Env(from)
	done, err := c.PostImagesPush(t.Context(), dockhand.PostImagesPushRequest{ImageId: image.Id, RegistryId: id, NewTag: repository + ":1"}, dockhand.PostImagesPushOptions{Env: source})
	if err != nil || done.Answer.Status != "complete" || done.Answer.TargetTag != pushed+":1" || done.Answer.JobId != "" {
		t.Fatalf("pushed and waited for = %+v, %v", done.Answer, err)
	}
	if imageOf(t, int(source), pushed+":1") == nil {
		t.Errorf("the image on %s does not go by the name it was pushed under", from)
	}
	// and as a job, whose result is the same answer
	job, err := c.PostImagesPush(t.Context(), dockhand.PostImagesPushRequest{ImageId: image.Id, RegistryId: id, NewTag: repository + ":2"}, dockhand.PostImagesPushOptions{Accept: dockhand.AcceptJob, Env: source})
	if err != nil || job.Answer.JobId == "" || job.Answer.Status != "" {
		t.Fatalf("pushed as a job = %+v, %v", job.Answer, err)
	}
	if end := ended(t, job.Answer.JobId); end.Status != "done" || !strings.Contains(fmt.Sprint(end.Result), "targetTag:"+pushed+":2") {
		t.Errorf("the job of a push = %+v", end)
	}
	// what cannot be pushed: an image there is none of, to a registry there is none of
	if _, err := c.PostImagesPush(t.Context(), dockhand.PostImagesPushRequest{ImageId: "sha256:" + strings.Repeat("0", 64), RegistryId: id, NewTag: repository + ":3"}, dockhand.PostImagesPushOptions{Env: source}); client.StatusCode(err) != 500 || !strings.Contains(err.Error(), "No such image") {
		t.Errorf("pushing an image there is none of = %v", err)
	}
	if _, err := c.PostImagesPush(t.Context(), dockhand.PostImagesPushRequest{ImageId: image.Id, RegistryId: 99999, NewTag: repository + ":3"}, dockhand.PostImagesPushOptions{Env: source}); !client.IsNotFound(err) {
		t.Errorf("pushing to a registry there is none of = %v", err)
	}

	// found in the registry: among what it holds, by a part of its name, and by its tags, which come as names alone with the size a call of its own
	holds, err := c.GetRegistryCatalog(t.Context(), dockhand.GetRegistryCatalogOptions{Registry: id})
	if err != nil || !slices.ContainsFunc(holds.Answer.Repositories, func(k dockhand.GetRegistryCatalogAnswerRepositories) bool { return k.Name == repository }) {
		t.Errorf("what the registry holds = %+v, %v", holds.Answer, err)
	}
	if found, err := c.GetRegistrySearch(t.Context(), dockhand.GetRegistrySearchOptions{Registry: id, Term: strings.TrimPrefix(repository, "it/")}); err != nil || len(found.Answer) != 1 || found.Answer[0].Name != repository {
		t.Errorf("searched by its name = %+v, %v", found.Answer, err)
	}
	tags, err := c.GetRegistryTags(t.Context(), dockhand.GetRegistryTagsOptions{Registry: id, Image: repository})
	if err != nil || tags.Answer.Total != 2 || tags.Answer.Tags[0].Name != "1" || tags.Answer.Tags[1].Name != "2" || tags.Answer.Tags[0].Size != 0 || tags.Answer.HasNext {
		t.Errorf("its tags = %+v, %v", tags.Answer, err)
	}
	if info, err := c.GetRegistryTagInfo(t.Context(), dockhand.GetRegistryTagInfoOptions{Registry: id, Image: repository, Tag: "1"}); err != nil || info.Answer.Size <= 0 {
		t.Errorf("the size of a tag = %+v, %v", info.Answer, err)
	}
	if _, err := c.GetRegistryTags(t.Context(), dockhand.GetRegistryTagsOptions{Registry: id, Image: "it/no-such-image"}); client.StatusCode(err) != 500 || !strings.Contains(err.Error(), "Image not found in registry") {
		t.Errorf("the tags of an image the registry has none of = %v", err)
	}

	// another host pulls it, signed in with what Dockhand keeps for the registry
	target := world.Env(to)
	if pulled, err := c.PostImagesPull(t.Context(), dockhand.PostImagesPullRequest{Image: pushed + ":1"}, dockhand.PostImagesPullOptions{Env: target}); err != nil || pulled.Answer.Status != "complete" || imageOf(t, int(target), pushed+":1") == nil {
		t.Errorf("pulled by %s = %+v, %v", to, pulled.Answer, err)
	}

	// changed: a change must carry the address, and one that does not is failed rather than refused
	if _, err := c.PutRegistriesById(t.Context(), id, dockhand.PutRegistriesByIdRequest{Name: name + "-two"}); client.StatusCode(err) != 500 {
		t.Errorf("a change that leaves the address out = %v", err)
	}
	renamed, err := c.PutRegistriesById(t.Context(), id, dockhand.PutRegistriesByIdRequest{Name: name + "-two", Url: address})
	if err != nil || renamed.Answer.Name != name+"-two" || renamed.Answer.Username != world.User || !renamed.Answer.HasCredentials {
		t.Errorf("renamed = %+v, %v: the login it was not sent is kept", renamed.Answer, err)
	}
	// the login forgotten, which the generated call cannot ask for: the registry is then reached and not signed in to, and what it holds is refused,
	// the registry's 401 handed on
	if forgot, err := c.ForgetRegistryCredentials(t.Context(), int(id), name, address); err != nil || forgot.Answer.Name != name || forgot.Answer.Username != "" || forgot.Answer.HasCredentials {
		t.Errorf("its login forgotten = %+v, %v", forgot.Answer, err)
	}
	if tried, err := c.PostRegistriesTest(t.Context(), dockhand.PostRegistriesTestRequest{RegistryId: id}); err != nil || !tried.Answer.Success || tried.Answer.Authenticated != nil {
		t.Errorf("tried with no login kept = %+v, %v", tried.Answer, err)
	}
	if _, err := c.GetRegistryCatalog(t.Context(), dockhand.GetRegistryCatalogOptions{Registry: id}); client.StatusCode(err) != 401 {
		t.Errorf("what it holds, asked with no login kept = %v", err)
	}
	if back, err := c.PutRegistriesById(t.Context(), id, dockhand.PutRegistriesByIdRequest{Name: name, Url: address, Username: world.User, Password: world.Password}); err != nil || !back.Answer.HasCredentials {
		t.Fatalf("its login put back = %+v, %v", back.Answer, err)
	}

	// the default moved to it and back to the one that was
	was, err := c.GetRegistries(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if moved, err := c.PostRegistriesByIdDefault(t.Context(), id); err != nil || !moved.Answer.Success || !registryOf(t, c, name).IsDefault {
		t.Errorf("made the default = %+v, %v", moved.Answer, err)
	}
	for i := range was.Answer {
		if was.Answer[i].IsDefault {
			if _, err := c.PostRegistriesByIdDefault(t.Context(), was.Answer[i].Id); err != nil || registryOf(t, c, name).IsDefault {
				t.Errorf("the default given back to %s: %v", was.Answer[i].Name, err)
			}
		}
	}

	// a tag deleted from the registry takes the other tag of the same contents with it, and leaves the image named with none
	if gone, err := c.DeleteRegistryImage(t.Context(), dockhand.DeleteRegistryImageOptions{Registry: id, Image: repository, Tag: "1"}); err != nil || !gone.Answer.Success {
		t.Fatalf("deleting a tag = %+v, %v", gone.Answer, err)
	}
	if left, err := c.GetRegistryTags(t.Context(), dockhand.GetRegistryTagsOptions{Registry: id, Image: repository}); err != nil || left.Answer.Total != 0 {
		t.Errorf("its tags once one was deleted = %+v, %v", left.Answer, err)
	}
	if _, err := c.DeleteRegistryImage(t.Context(), dockhand.DeleteRegistryImageOptions{Registry: id, Image: repository, Tag: "1"}); !client.IsNotFound(err) {
		t.Errorf("deleting it again = %v", err)
	}

	// removed, once
	if gone, err := c.DeleteRegistriesById(t.Context(), id); err != nil || !gone.Answer.Success || registryOf(t, c, name) != nil {
		t.Errorf("removing = %+v, %v", gone.Answer, err)
	}
	if _, err := c.DeleteRegistriesById(t.Context(), id); !client.IsNotFound(err) {
		t.Errorf("removing it again = %v", err)
	}
	if _, err := c.GetRegistriesById(t.Context(), id); !client.IsNotFound(err) {
		t.Errorf("read once removed = %v", err)
	}
}
