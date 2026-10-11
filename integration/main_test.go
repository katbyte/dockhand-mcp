//go:build integration

// Package integration runs the client in sdk/dockhand against a Dockhand running in Docker, with Docker hosts of its own to manage. It tests one
// thing: that every request the client builds is one a real server accepts, and that what the server answers decodes into the client's types without
// losing what it carries. The generated tests hold the client against its definitions; this holds it against the server, which is what the document
// behind those definitions only describes. Nothing here is about the tools; ../acceptance covers those.
//
// The suite runs twice on the same server, because a Dockhand is run two ways: open, as it comes, with no login, and with its login switched on and
// asked with an API token. DOCKHAND_TEST_STAGE says which a run is, and the login is switched on by the run that asks for it, through the client.
//
// Every test makes what it needs on the hosts, named for itself, and removes it when it ends.
//
//	make testacc-integration                # both stages, server and hosts started and torn down
//
//	eval "$(.scripts/testenv.sh up)"         # or drive it by hand: a fresh server,
//	go test -tags integration ./integration/...
//	DOCKHAND_TEST_STAGE=login go test -tags integration ./integration/...   # and with its login on
//	.scripts/testenv.sh down
package integration

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
)

// world is the Dockhand under test and how it is run, nil when the environment names none and every test skips.
var world *testenv.World

func TestMain(m *testing.M) { os.Exit(runSuite(m)) }

// runSuite finds the Dockhand under test, runs it the way the environment asks - which for the login stage is switching the login on - and runs the
// tests, returning the code to exit with.
func runSuite(m *testing.M) int {
	if testenv.Configured() {
		var err error
		if world, err = testenv.Open(context.Background()); err != nil {
			fmt.Fprintln(os.Stderr, "integration setup:", err)

			return 1
		}
	}

	return m.Run()
}

// dock is the client for the Dockhand under test, as this stage asks it. It skips the test when the environment names no server.
func dock(t *testing.T) *dockhand.Client {
	t.Helper()

	if world == nil {
		t.Skip(testenv.NotConfigured)
	}

	return world.Dock
}

// noCancel is a test's context for its clean-ups, which run after the test's own has been cancelled.
func noCancel(t *testing.T) context.Context {
	t.Helper()

	return context.WithoutCancel(t.Context())
}

// named is a name for something a test makes, the test's own in the letters Docker and Dockhand both take.
func named(t *testing.T, what string) string {
	t.Helper()

	name := strings.ToLower(strings.TrimPrefix(t.Name(), "Test"))
	name = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}

		return '-'
	}, name)

	return "it-" + name + "-" + what
}

// started runs a container on a host for the length of a test, from outside Dockhand, and returns its name.
func started(t *testing.T, host, what string, command ...string) string {
	t.Helper()

	name := named(t, what)
	// a run that was cut short may have left it behind
	_, _ = world.Docker(t.Context(), host, "rm", "-f", name)
	if len(command) == 0 {
		command = []string{"sleep", "100000"}
	}
	if _, err := world.Docker(t.Context(), host, append([]string{"run", "-d", "--name", name, world.Image}, command...)...); err != nil {
		t.Fatalf("starting %s on %s: %v", name, host, err)
	}
	ctx := noCancel(t)
	t.Cleanup(func() {
		if _, err := world.Docker(ctx, host, "rm", "-f", name); err != nil {
			t.Errorf("removing %s from %s: %v", name, host, err)
		}
	})

	return name
}
