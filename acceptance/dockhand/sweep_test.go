//go:build acceptance

package dockhand_test

import (
	"net/http/cookiejar"
	"strconv"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/dockhand-mcp/sdk/pandorest/workarounds"
	"github.com/katbyte/pandorest/client"
	"github.com/katbyte/pandorest/config"
	"github.com/katbyte/pandorest/definitions"
	"github.com/katbyte/pandorest/sweep"
)

// loadDefinitions reads the definitions sdk/dockhand was generated from.
func loadDefinitions(t *testing.T) *definitions.Service {
	t.Helper()

	svc, _ := config.Find(workarounds.Services, workarounds.Service)
	svc, err := svc.In("../..").Resolve()
	if err != nil {
		t.Fatal(err)
	}
	defs, err := definitions.Load(svc.Path(svc.Definitions))
	if err != nil {
		t.Fatal(err)
	}

	return defs
}

// sweepStack is a compose stack brought up on alpha for the sweep, from outside Dockhand, with a service that writes a log and keeps a volume.
const (
	sweepStack   = "sweep"
	sweepCompose = `name: sweep
services:
  app:
    image: IMAGE
    command: ["sh", "-c", "echo app up; echo kept > /data/note.txt; while true; do echo tick; sleep 5; done"]
    volumes:
      - data:/data
volumes:
  data:
`
)

// sweepFixtures lays out what the read calls are asked about, on alpha, and resolves each call's arguments to it: a stack of one container with a
// volume it has written a file to and a network of its own, and the image they run.
func sweepFixtures(t *testing.T) sweep.Fixtures {
	t.Helper()

	c := dock(t)
	ctx := noCancel(t)
	if err := world.Compose(t.Context(), testenv.Alpha, sweepStack, strings.ReplaceAll(sweepCompose, "IMAGE", world.Image)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := world.Docker(ctx, testenv.Alpha, "rm", "-f", "-v", sweepStack+"-app-1"); err != nil {
			t.Errorf("removing the sweep's container: %v", err)
		}
		_, _ = world.Docker(ctx, testenv.Alpha, "volume", "rm", "-f", sweepStack+"_data")
		_, _ = world.Docker(ctx, testenv.Alpha, "network", "rm", "-f", sweepStack+"_default")
	})

	env := world.Env(testenv.Alpha)
	containers, err := c.GetContainers(t.Context(), dockhand.GetContainersOperationOptions{Env: &env})
	if err != nil {
		t.Fatal(err)
	}
	var container dockhand.GetContainersResponse
	for _, listed := range containers.Model {
		if listed.Name == sweepStack+"-app-1" {
			container = listed
		}
	}
	if container.Id == "" {
		t.Fatalf("the sweep's container is not among the %d on alpha", len(containers.Model))
	}
	network, err := world.Docker(t.Context(), testenv.Alpha, "network", "inspect", "-f", "{{.Id}}", sweepStack+"_default")
	if err != nil {
		t.Fatal(err)
	}

	fixtures := sweep.Fixtures{
		Path: map[string]string{
			"environments/id": strconv.Itoa(env),
			"containers/id":   container.Id,
			"images/id":       container.ImageId,
			"networks/id":     strings.TrimSpace(network),
			"volumes/name":    sweepStack + "_data",
			"stacks/name":     sweepStack,
			"selfhst/ref":     "docker",
			// what every server has from the start: the registry it pulls from unless told otherwise, and the role that may do everything
			"registries/id": "1",
			"roles/id":      "1",
			// a container is named, where it is not found by its id, by its name
			"auto-update/containerName": container.Name,
			"container-tags/name":       container.Name,
			"container-icons/name":      container.Name,
		},
		Options: map[string]string{
			"containers": container.Id,
			"path":       "/",
			"name":       sweepStack,
			"imageId":    container.ImageId,
			"image":      "library/busybox",
			"tag":        "1.37",
			"term":       "busybox",
			"registry":   "1",
		},
		Always: map[string]string{"env": strconv.Itoa(env)},
	}

	// with its login on the server has a user: the administrator the stage made
	if world.Stage == testenv.StageLogin {
		users, err := c.GetUsers(t.Context())
		if err != nil || len(users.Model) == 0 {
			t.Fatalf("the users of a server with its login on = %d, %v", len(users.Model), err)
		}
		fixtures.Path["users/id"] = strconv.Itoa(users.Model[0].Id)
	}

	return fixtures
}

// Every read the API has is called against the server, and each answers with something that lands in its model, or has a case that says why not.
func TestReadSweep(t *testing.T) {
	c := dock(t)

	sweep.Sweep{
		Definitions: loadDefinitions(t),
		Client:      *c,
		StatusCode:  client.StatusCode,
		Fixtures:    sweepFixtures(t),
		Cases:       sweepCases(world.Stage),
	}.Run(t)
}

// The reads kept for a logged-in session answer one: a token, which is what every other test asks with, is turned away from them.
func TestSessionOnlyReads(t *testing.T) {
	dock(t)
	if world.Stage != testenv.StageLogin {
		t.Skip("a server with its login off has no session to ask as")
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	session, err := dockhand.NewWithoutLogin(world.Server, client.WithCookies(jar))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.PostAuthLogin(t.Context(), dockhand.PostAuthLoginRequest{Username: world.User, Password: world.Password, Provider: "local"}); err != nil {
		t.Fatal(err)
	}

	defs := loadDefinitions(t)
	asSession := sweep.Sweep{Definitions: defs, Client: *session, StatusCode: client.StatusCode}
	swept := 0
	for _, op := range defs.Operations() {
		if op.Name != "GetProfile" && op.Name != "GetProfilePreferences" {
			continue
		}
		swept++
		if outcome := asSession.Operation(t.Context(), op); len(outcome.Failures) > 0 || outcome.Skipped != "" {
			t.Errorf("%s asked as a session: %v %s", op.Name, outcome.Failures, outcome.Skipped)
		}
	}
	if swept != 2 {
		t.Errorf("%d of the two session-only reads were found in the definitions", swept)
	}
}
