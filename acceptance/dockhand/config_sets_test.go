//go:build acceptance

package dockhand_test

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

	sets := func() map[string]dockhand.GetConfigSetsAnswer {
		listed, err := c.GetConfigSets(ctx)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]dockhand.GetConfigSetsAnswer{}
		for _, set := range listed.Answer {
			out[set.Name] = set
		}

		return out
	}
	clean := func() {
		listed, err := c.GetConfigSets(noCancel(t))
		if err != nil {
			return
		}
		for _, set := range listed.Answer {
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
	if err != nil || plain.Answer.Id == 0 || plain.Answer.Name != name || plain.Answer.NetworkMode != "bridge" || plain.Answer.RestartPolicy != "no" || plain.Answer.Description != "" || len(plain.Answer.EnvVars)+len(plain.Answer.Labels)+len(plain.Answer.Ports)+len(plain.Answer.Volumes) != 0 || plain.Answer.CreatedAt.IsZero() {
		t.Fatalf("a config set saved with a name alone = %+v, %v", plain.Answer, err)
	}
	// the same name is refused, and in another case it is another set
	if _, err := c.PostConfigSets(ctx, dockhand.PostConfigSetsRequest{Name: name}); client.StatusCode(err) != http.StatusBadRequest {
		t.Errorf("a second config set of the same name = %v, want a 400", err)
	}
	twin, err := c.PostConfigSets(ctx, dockhand.PostConfigSetsRequest{Name: strings.ToUpper(name)})
	if err != nil || twin.Answer.Id == plain.Answer.Id {
		t.Fatalf("a config set of the same name in another case = %+v, %v: names used to differ by their case", twin.Answer, err)
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
	id := made.Answer.Id
	read, err := c.GetConfigSetsById(ctx, id)
	if err != nil || read.Answer.Name != full || read.Answer.Description != "all of it" || read.Answer.NetworkMode != "host" || read.Answer.RestartPolicy != "unless-stopped" || len(read.Answer.EnvVars) != 2 || read.Answer.EnvVars[1].Key != "API_TOKEN" || read.Answer.EnvVars[1].Value != "not-a-real-token" || len(read.Answer.Labels) != 1 || read.Answer.Labels[0].Value != "ops" || len(read.Answer.Ports) != 1 || read.Answer.Ports[0].HostPort != "8080" || read.Answer.Ports[0].ContainerPort != "80" || read.Answer.Ports[0].Protocol != "tcp" || len(read.Answer.Volumes) != 1 || read.Answer.Volumes[0].HostPath != "/srv/data" || read.Answer.Volumes[0].Mode != "rw" {
		t.Errorf("the config set read by its number = %+v, %v", read.Answer, err)
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
	if err != nil || odd.Answer.NetworkMode != "no-such-mode" || odd.Answer.RestartPolicy != "whenever" || len(odd.Answer.EnvVars) != 3 || odd.Answer.EnvVars[0].Key != "" || odd.Answer.EnvVars[2].Key != "A B" || odd.Answer.Ports[0].HostPort != "not-a-port" || odd.Answer.Volumes[0].Mode != "zz" {
		t.Errorf("a config set of nonsense = %+v, %v: Dockhand used to save it as it came, which is why the tools check one first", odd.Answer, err)
	}

	// a change sets what it is sent and keeps the rest; an empty list takes everything of its kind away
	changed, err := c.PutConfigSetsById(ctx, id, dockhand.PutConfigSetsByIdRequest{Name: " " + full + "-renamed ", RestartPolicy: "always", Labels: []dockhand.PutConfigSetsByIdRequestLabels{}})
	if err != nil || changed.Answer.Name != full+"-renamed" || changed.Answer.RestartPolicy != "always" || len(changed.Answer.Labels) != 0 || len(changed.Answer.EnvVars) != 2 || changed.Answer.Description != "all of it" || changed.Answer.NetworkMode != "host" || len(changed.Answer.Ports) != 1 {
		t.Errorf("the config set changed = %+v, %v", changed.Answer, err)
	}
	// another set's name is refused, and its description is forgotten when it is sent empty, which the generated call leaves out
	if _, err := c.PutConfigSetsById(ctx, id, dockhand.PutConfigSetsByIdRequest{Name: name}); client.StatusCode(err) != http.StatusBadRequest {
		t.Errorf("a config set renamed to another's name = %v, want a 400", err)
	}
	if forgot, err := c.ForgetConfigSetDescription(ctx, int(id)); err != nil || forgot.Answer.Description != "" || forgot.Answer.Name != full+"-renamed" || len(forgot.Answer.EnvVars) != 2 {
		t.Errorf("the description forgotten = %+v, %v", forgot.Answer, err)
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
	for _, number := range []int{int(plain.Answer.Id), int(twin.Answer.Id), int(id), int(odd.Answer.Id)} {
		if gone, err := c.DeleteConfigSetsById(ctx, dockhand.ConfigSetID(number)); err != nil || !gone.Answer.Success {
			t.Errorf("removing config set %d = %+v, %v", number, gone.Answer, err)
		}
	}
	if after := len(sets()); after != before {
		t.Errorf("%d config sets at the end, want the %d there were", after, before)
	}
}
