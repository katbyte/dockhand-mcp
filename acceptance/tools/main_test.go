//go:build acceptance

// Package tools_test covers every tool against a real Dockhand running in Docker, with Docker hosts of its own to manage, so the shapes the tools
// read, the parameters they send and what they say of a container are checked against the thing dockhand-mcp actually talks to rather than a stub.
//
// The suite runs twice on the same server, because a Dockhand is run two ways: open, as it comes, with no login, and with its login switched on and
// asked with an API token. DOCKHAND_TEST_STAGE says which a run is, and the login is switched on by the run that asks for it. The fixtures are what a
// host really holds: on the first, a compose stack someone brought up at the host with a healthy service and an unhealthy one, a container on its
// own, one that exited with an error and one Docker keeps starting again; and on the second, behind its agent, one container.
//
// Each run fails when a registered tool was never seen to answer in it.
//
//	make testacc-tools                      # both stages, server and hosts started and torn down
//
//	eval "$(.scripts/testenv.sh up)"         # or drive it by hand: a fresh server,
//	go test -tags acceptance ./acceptance/tools/...
//	DOCKHAND_TEST_STAGE=login go test -tags acceptance ./acceptance/tools/...   # and with its login on
//	.scripts/testenv.sh down
package tools_test

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) { os.Exit(runSuite(m)) }
