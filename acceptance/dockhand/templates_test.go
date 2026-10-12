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

// catalogue is a list of templates in the form Portainer's take, made up: a container template with a volume it names no place for and a variable it
// leaves empty, and a stack template whose repository there is none of.
const catalogue = `{"version":"2","templates":[{"type":1,"title":"It Web","name":"it-web","description":"A made-up web server","categories":["It"],"platform":"linux","logo":"","image":"registry.invalid/it/web:1","ports":["8080:80/tcp"],"volumes":[{"container":"/data"},{"container":"/etc/web","bind":"/srv/web"}],"env":[{"name":"MODE","label":"Mode","default":"demo"},{"name":"API_KEY","label":"Key"}],"restart_policy":"unless-stopped","note":"A note."},{"type":3,"title":"It Stack","name":"it-stack","description":"A made-up stack","categories":["It"],"platform":"linux","repository":{"url":"https://repository.invalid/nobody/nothing","stackfile":"compose.yaml"}}]}`

// from is the templates the server lists from one source, by title.
func from(t *testing.T, c *dockhand.Client, source string) map[string]dockhand.GetTemplatesResponse { //nolint:unparam // the one test that calls it has one source, and the call still says whose templates are meant
	t.Helper()

	listed, err := c.GetTemplates(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]dockhand.GetTemplatesResponse{}
	for _, template := range listed.Model {
		if template.Source == source {
			out[template.Title] = template
		}
	}

	return out
}

// A source of templates is added, read at once, switched off and on, and removed, with a list of templates served on the test network: what a
// template holds, the compose file Dockhand writes from one, and what it answers of a source it has none of.
func TestTemplatesListedComposedAndTheirSourcesKept(t *testing.T) {
	const name, served = "it-template-source", "it-templates"
	c := dock(t)
	ctx := t.Context()

	sources := func() []dockhand.GetTemplatesSourcesResponse {
		listed, err := c.GetTemplatesSources(ctx)
		if err != nil {
			t.Fatal(err)
		}

		return listed.Model
	}
	clean := func() {
		listed, err := c.GetTemplatesSources(noCancel(t))
		if err != nil {
			return
		}
		for i := range listed.Model {
			if listed.Model[i].Name == name {
				_, _ = c.DeleteTemplatesSources(noCancel(t), dockhand.DeleteTemplatesSourcesOperationOptions{Id: &listed.Model[i].Id})
			}
		}
	}
	clean()
	address, err := world.Serve(ctx, testenv.Alpha, served, "it-templates.json", catalogue)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		clean()
		_ = world.Unserve(noCancel(t), testenv.Alpha, served)
	})
	before := sources()
	if len(before) == 0 || !slices.ContainsFunc(before, func(s dockhand.GetTemplatesSourcesResponse) bool { return yes(s.Builtin) && s.SourceId != "" }) {
		t.Fatalf("the sources a server comes with = %+v", before)
	}

	// an address that is none is refused, and a source is added switched on, as one that is not Dockhand's own
	if _, err := c.PostTemplatesSources(ctx, dockhand.PostTemplatesSourcesRequest{Name: name, Url: "not an address"}); client.StatusCode(err) != http.StatusBadRequest {
		t.Errorf("a source at an address that is none = %v, want a 400", err)
	}
	made, err := c.PostTemplatesSources(ctx, dockhand.PostTemplatesSourcesRequest{Name: name, Url: address})
	if err != nil || !yes(made.Model.Enabled) || yes(made.Model.Builtin) || made.Model.SourceId == "" || made.Model.Name != name || made.Model.Url != address {
		t.Fatalf("a source added = %+v, %v", made.Model, err)
	}
	id := made.Model.Id

	// its templates are listed at once, with what each would be run with
	mine := from(t, c, name)
	web, stack := mine["It Web"], mine["It Stack"]
	if len(mine) != 2 || web.Type != "container" || web.Image != "registry.invalid/it/web:1" || !slices.Equal(web.Ports, []string{"8080:80/tcp"}) || len(web.Volumes) != 2 || web.Volumes[0].Container != "/data" || web.Volumes[0].Bind != "" || web.Volumes[1].Bind != "/srv/web" || len(web.Env) != 2 || web.Env[0].Default != "demo" || web.Env[1].Name != "API_KEY" || web.RestartPolicy != "unless-stopped" || web.Note != "A note." || web.Id == "" {
		t.Fatalf("the container template as listed = %+v, of %d", web, len(mine))
	}
	if stack.Type != "stack" || stack.Repository == nil || stack.Repository.Url != "https://repository.invalid/nobody/nothing" || stack.Repository.Stackfile != "compose.yaml" || stack.Image != "" {
		t.Errorf("the stack template as listed = %+v", stack)
	}

	// the compose file of a container template is written from what Dockhand is sent, so the template goes back whole; a volume it names no place for
	// is written as "undefined"
	given := dockhand.PostTemplatesComposeRequestTemplate{Id: web.Id, Type: web.Type, Title: web.Title, Image: web.Image, Ports: web.Ports, RestartPolicy: web.RestartPolicy}
	for _, volume := range web.Volumes {
		given.Volumes = append(given.Volumes, dockhand.PostTemplatesComposeRequestTemplateVolumes(volume))
	}
	for _, variable := range web.Env {
		given.Env = append(given.Env, dockhand.PostTemplatesComposeRequestTemplateEnv(variable))
	}
	written, err := c.PostTemplatesCompose(ctx, dockhand.PostTemplatesComposeRequest{Template: &given})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"it-web:", "image: registry.invalid/it/web:1", "restart: unless-stopped", `"8080:80/tcp"`, "/srv/web:/etc/web", "MODE=demo", "API_KEY="} {
		if !strings.Contains(written.Model.Compose, want) {
			t.Errorf("the compose file of the container template lacks %q:\n%s", want, written.Model.Compose)
		}
	}
	if !strings.Contains(written.Model.Compose, "undefined:/data") {
		t.Errorf("a volume with no place on the host is no longer written as undefined, so the tools need not say so:\n%s", written.Model.Compose)
	}
	if _, err := c.PostTemplatesCompose(ctx, dockhand.PostTemplatesComposeRequest{Template: &dockhand.PostTemplatesComposeRequestTemplate{Id: web.Id}}); client.StatusCode(err) != http.StatusInternalServerError {
		t.Errorf("the compose file of a template named by its id alone = %v, want a 500: Dockhand writes it from the template it is sent", err)
	}
	// a stack template's is fetched from its repository, which here there is none of
	if _, err := c.PostTemplatesCompose(ctx, dockhand.PostTemplatesComposeRequest{Template: &dockhand.PostTemplatesComposeRequestTemplate{Id: stack.Id, Type: stack.Type, Title: stack.Title, Repository: &dockhand.PostTemplatesComposeRequestTemplateRepository{Url: stack.Repository.Url, Stackfile: stack.Repository.Stackfile}}}); client.StatusCode(err) != http.StatusInternalServerError || !strings.Contains(err.Error(), "fetch failed") {
		t.Errorf("the compose file of a stack template whose repository is not there = %v, want a 500 that says the fetch failed", err)
	}

	// switched off its templates go from the list at once, and switched on they are back
	if off, err := c.PutTemplatesSources(ctx, dockhand.PutTemplatesSourcesRequest{Id: id, Enabled: new(false)}); err != nil || !yes(off.Model.Ok) || len(from(t, c, name)) != 0 {
		t.Errorf("the source switched off = %+v, %v, with %d of its templates still listed", off.Model, err, len(from(t, c, name)))
	}
	if !slices.ContainsFunc(sources(), func(s dockhand.GetTemplatesSourcesResponse) bool { return s.Id == id && !yes(s.Enabled) }) {
		t.Errorf("the source is not listed as switched off: %+v", sources())
	}
	if on, err := c.PutTemplatesSources(ctx, dockhand.PutTemplatesSourcesRequest{Id: id, Enabled: new(true)}); err != nil || !yes(on.Model.Ok) || len(from(t, c, name)) != 2 {
		t.Errorf("the source switched on again = %+v, %v, with %d of its templates listed", on.Model, err, len(from(t, c, name)))
	}

	// a source there is none of is answered as changed and as removed, and a second of the same name and address is kept beside the first
	if changed, err := c.PutTemplatesSources(ctx, dockhand.PutTemplatesSourcesRequest{Id: 99999, Enabled: new(false)}); err != nil || !yes(changed.Model.Ok) {
		t.Errorf("a source there is none of, changed = %+v, %v: Dockhand answers that it was", changed.Model, err)
	}
	if gone, err := c.DeleteTemplatesSources(ctx, dockhand.DeleteTemplatesSourcesOperationOptions{Id: new(99999)}); err != nil || !yes(gone.Model.Ok) {
		t.Errorf("a source there is none of, removed = %+v, %v: Dockhand answers that it was", gone.Model, err)
	}
	twin, err := c.PostTemplatesSources(ctx, dockhand.PostTemplatesSourcesRequest{Name: name, Url: address})
	if err != nil || twin.Model.Id == id {
		t.Errorf("a second source of the same name and address = %+v, %v: Dockhand keeps both", twin.Model, err)
	}

	// removed, both, and the list is as it was
	for _, number := range []int{twin.Model.Id, id} {
		if gone, err := c.DeleteTemplatesSources(ctx, dockhand.DeleteTemplatesSourcesOperationOptions{Id: &number}); err != nil || !yes(gone.Model.Ok) {
			t.Errorf("removing source %d = %+v, %v", number, gone.Model, err)
		}
	}
	if after := sources(); len(after) != len(before) || len(from(t, c, name)) != 0 {
		t.Errorf("%d sources at the end and %d of the removed one's templates, want the %d there were and none", len(after), len(from(t, c, name)), len(before))
	}
}
