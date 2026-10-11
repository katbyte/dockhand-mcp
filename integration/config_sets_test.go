//go:build integration

package integration

import (
	"net/http"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/pandorest/client"
)

// Config sets are saved, read, changed and removed: what one is given when only its name is, that nothing of one but its name is checked, that names
// differ by case, that a change sets what it is sent and keeps the rest, and what is answered of a set there is none of.
func TestConfigSetsSavedChangedAndRemoved(t *testing.T) {
	const name, full, nobody = "it-set-plain", "it-set-full", 999999
	c := dock(t)
	ctx := t.Context()

	sets := func() map[string]dockhand.GetConfigSetsResponse {
		listed, err := c.GetConfigSets(ctx)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]dockhand.GetConfigSetsResponse{}
		for _, set := range listed.Model {
			out[set.Name] = set
		}

		return out
	}
	clean := func() {
		listed, err := c.GetConfigSets(noCancel(t))
		if err != nil {
			return
		}
		for _, set := range listed.Model {
			if strings.HasPrefix(strings.ToLower(set.Name), "it-set-") {
				_, _ = c.DeleteConfigSetsById(noCancel(t), set.Id)
			}
		}
	}
	clean()
	t.Cleanup(clean)
	before := len(sets())

	// one with no name is refused; with a name alone it is given a network mode and a restart policy, and holds nothing
	if _, err := c.PostConfigSets(ctx, dockhand.PostConfigSetsRequest{Name: "  ", NetworkMode: "host"}); client.StatusCode(err) != http.StatusBadRequest {
		t.Errorf("a config set with no name = %v, want a 400", err)
	}
	plain, err := c.PostConfigSets(ctx, dockhand.PostConfigSetsRequest{Name: " " + name + " "})
	if err != nil || plain.Model.Id == 0 || plain.Model.Name != name || plain.Model.NetworkMode != "bridge" || plain.Model.RestartPolicy != "no" || plain.Model.Description != "" || len(plain.Model.EnvVars)+len(plain.Model.Labels)+len(plain.Model.Ports)+len(plain.Model.Volumes) != 0 || plain.Model.CreatedAt == "" {
		t.Fatalf("a config set saved with a name alone = %+v, %v", plain.Model, err)
	}
	// the same name is refused, and in another case it is another set
	if _, err := c.PostConfigSets(ctx, dockhand.PostConfigSetsRequest{Name: name}); client.StatusCode(err) != http.StatusBadRequest {
		t.Errorf("a second config set of the same name = %v, want a 400", err)
	}
	twin, err := c.PostConfigSets(ctx, dockhand.PostConfigSetsRequest{Name: strings.ToUpper(name)})
	if err != nil || twin.Model.Id == plain.Model.Id {
		t.Fatalf("a config set of the same name in another case = %+v, %v: names used to differ by their case", twin.Model, err)
	}

	// everything one holds is given back as it was sent, a value that is a secret among it
	made, err := c.PostConfigSets(ctx, dockhand.PostConfigSetsRequest{
		Name: full, Description: "all of it", NetworkMode: "host", RestartPolicy: "unless-stopped",
		EnvVars: []dockhand.PostConfigSetsRequestEnvVars{{Key: "MODE", Value: "demo"}, {Key: "API_TOKEN", Value: "not-a-real-token"}},
		Labels:  []dockhand.PostConfigSetsRequestLabels{{Key: "team", Value: "ops"}},
		Ports:   []dockhand.PostConfigSetsRequestPorts{{HostPort: "8080", ContainerPort: "80", Protocol: "tcp"}},
		Volumes: []dockhand.PostConfigSetsRequestVolumes{{HostPath: "/srv/data", ContainerPath: "/data", Mode: "rw"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := made.Model.Id
	read, err := c.GetConfigSetsById(ctx, id)
	if err != nil || read.Model.Name != full || read.Model.Description != "all of it" || read.Model.NetworkMode != "host" || read.Model.RestartPolicy != "unless-stopped" || len(read.Model.EnvVars) != 2 || read.Model.EnvVars[1].Key != "API_TOKEN" || read.Model.EnvVars[1].Value != "not-a-real-token" || len(read.Model.Labels) != 1 || read.Model.Labels[0].Value != "ops" || len(read.Model.Ports) != 1 || read.Model.Ports[0].HostPort != "8080" || read.Model.Ports[0].ContainerPort != "80" || read.Model.Ports[0].Protocol != "tcp" || len(read.Model.Volumes) != 1 || read.Model.Volumes[0].HostPath != "/srv/data" || read.Model.Volumes[0].Mode != "rw" {
		t.Errorf("the config set read by its number = %+v, %v", read.Model, err)
	}
	if listed := sets()[full]; listed.Id != id || len(listed.EnvVars) != 2 || listed.EnvVars[1].Value != "not-a-real-token" || len(sets()) != before+3 {
		t.Errorf("the config set as listed = %+v, among %d", listed, len(sets()))
	}

	// nothing of a set but its name is checked: a port that is no port, a variable with no name and a policy that is none are saved
	odd, err := c.PostConfigSets(ctx, dockhand.PostConfigSetsRequest{
		Name: "it-set-odd", NetworkMode: "no-such-mode", RestartPolicy: "whenever",
		EnvVars: []dockhand.PostConfigSetsRequestEnvVars{{Key: "", Value: "x"}, {Key: "A B", Value: "1"}, {Key: "A B", Value: "2"}},
		Ports:   []dockhand.PostConfigSetsRequestPorts{{HostPort: "not-a-port", ContainerPort: "99999", Protocol: "icmp"}},
		Volumes: []dockhand.PostConfigSetsRequestVolumes{{HostPath: "", ContainerPath: "relative", Mode: "zz"}},
	})
	if err != nil || odd.Model.NetworkMode != "no-such-mode" || odd.Model.RestartPolicy != "whenever" || len(odd.Model.EnvVars) != 3 || odd.Model.EnvVars[0].Key != "" || odd.Model.EnvVars[2].Key != "A B" || odd.Model.Ports[0].HostPort != "not-a-port" || odd.Model.Volumes[0].Mode != "zz" {
		t.Errorf("a config set of nonsense = %+v, %v: Dockhand used to save it as it came, which is why the tools check one first", odd.Model, err)
	}

	// a change sets what it is sent and keeps the rest; an empty list takes everything of its kind away
	changed, err := c.PutConfigSetsById(ctx, id, dockhand.PutConfigSetsByIdRequest{Name: " " + full + "-renamed ", RestartPolicy: "always", Labels: []dockhand.PutConfigSetsByIdRequestLabels{}})
	if err != nil || changed.Model.Name != full+"-renamed" || changed.Model.RestartPolicy != "always" || len(changed.Model.Labels) != 0 || len(changed.Model.EnvVars) != 2 || changed.Model.Description != "all of it" || changed.Model.NetworkMode != "host" || len(changed.Model.Ports) != 1 {
		t.Errorf("the config set changed = %+v, %v", changed.Model, err)
	}
	// another set's name is refused, and its description is forgotten when it is sent empty, which the generated call leaves out
	if _, err := c.PutConfigSetsById(ctx, id, dockhand.PutConfigSetsByIdRequest{Name: name}); client.StatusCode(err) != http.StatusBadRequest {
		t.Errorf("a config set renamed to another's name = %v, want a 400", err)
	}
	if forgot, err := c.ForgetConfigSetDescription(ctx, id); err != nil || forgot.Model.Description != "" || forgot.Model.Name != full+"-renamed" || len(forgot.Model.EnvVars) != 2 {
		t.Errorf("the description forgotten = %+v, %v", forgot.Model, err)
	}

	// a set there is none of is said to be none, whatever is asked of it
	if _, err := c.GetConfigSetsById(ctx, nobody); !client.IsNotFound(err) {
		t.Errorf("a config set there is none of, read = %v, want a 404", err)
	}
	if _, err := c.PutConfigSetsById(ctx, nobody, dockhand.PutConfigSetsByIdRequest{Name: "it-set-nobody"}); !client.IsNotFound(err) {
		t.Errorf("a config set there is none of, changed = %v, want a 404", err)
	}
	if _, err := c.DeleteConfigSetsById(ctx, nobody); !client.IsNotFound(err) {
		t.Errorf("a config set there is none of, removed = %v, want a 404", err)
	}

	// removed, each, and the list is as it was
	for _, number := range []int{plain.Model.Id, twin.Model.Id, id, odd.Model.Id} {
		if gone, err := c.DeleteConfigSetsById(ctx, number); err != nil || !yes(gone.Model.Success) {
			t.Errorf("removing config set %d = %+v, %v", number, gone.Model, err)
		}
	}
	if after := len(sets()); after != before {
		t.Errorf("%d config sets at the end, want the %d there were", after, before)
	}
}
