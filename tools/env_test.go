package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// dashboardStats is what a 1.0.51 server answers for the two environments, the second of which it cannot reach.
const dashboardStats = `[{"id":1,"name":"alpha","connectionType":"socket","online":true,"containers":{"total":4,"running":3,"stopped":1,"paused":0,"restarting":0,"unhealthy":1,"pendingUpdates":2},"stacks":{"total":2,"running":1,"partial":1,"stopped":0},"images":{"total":1,"totalSize":4042342},"volumes":{"total":0,"totalSize":0},"networks":{"total":4},"events":{"total":12,"today":12},"metrics":{"cpuPercent":1.2,"memoryPercent":40.1,"memoryUsed":812345678,"memoryTotal":2053652480}},{"id":2,"name":"beta","connectionType":"hawser-standard","online":false,"containers":{"total":0,"running":0,"stopped":0,"paused":0,"restarting":0,"unhealthy":0,"pendingUpdates":0},"stacks":{"total":0,"running":0,"partial":0,"stopped":0}}]`

func TestEnvList(t *testing.T) {
	t.Parallel()

	dock := dockhandtest.New(t)
	dock.Answer("GET /api/environments", twoEnvironments)
	dock.Answer("GET /api/dashboard/stats", dashboardStats)

	out := session(t, dock, Options{}).Call(t, "env_list", nil)
	rows := acctest.Rows(t, out["environments"], "environments")
	if len(rows) != 2 {
		t.Fatalf("environments = %v", out)
	}

	alpha, beta := rows[0], rows[1]
	if alpha["env"] != "alpha" || acctest.Num(t, alpha["id"], "id") != 1 || alpha["connection"] != "socket" || alpha["address"] != nil || !acctest.IsBool(alpha["online"], true) {
		t.Errorf("alpha = %v: a socket has no address, and it is online", alpha)
	}
	containers, stacks := acctest.Object(t, alpha["containers"], "containers"), acctest.Object(t, alpha["stacks"], "stacks")
	if acctest.Num(t, containers["total"], "total") != 4 || acctest.Num(t, containers["running"], "running") != 3 || acctest.Num(t, containers["stopped"], "stopped") != 1 || acctest.Num(t, containers["unhealthy"], "unhealthy") != 1 || acctest.Num(t, containers["pending_updates"], "pending_updates") != 2 {
		t.Errorf("alpha's containers = %v", containers)
	}
	if acctest.Num(t, stacks["total"], "total") != 2 || acctest.Num(t, stacks["running"], "running") != 1 || acctest.Num(t, stacks["partial"], "partial") != 1 {
		t.Errorf("alpha's stacks = %v", stacks)
	}

	// a host Dockhand cannot reach is listed as that, without counts that would read as an empty host
	if beta["env"] != "beta" || beta["connection"] != "hawser-standard" || beta["address"] != "agent-beta:2376" || !acctest.IsBool(beta["online"], false) || beta["containers"] != nil || beta["stacks"] != nil {
		t.Errorf("beta = %v: an agent's address, offline, and no counts", beta)
	}
	if said := acctest.Str(out["note"]); !strings.Contains(said, "1 of the 2 hosts could not be reached") {
		t.Errorf("the note = %q", said)
	}
}

func TestEnvListNone(t *testing.T) {
	t.Parallel()

	dock := dockhandtest.New(t)
	dock.Answer("GET /api/environments", `[]`)

	out := session(t, dock, Options{}).Call(t, "env_list", nil)
	if rows := acctest.RowsOf(out["environments"]); len(rows) != 0 || !strings.Contains(acctest.Str(out["note"]), "manages no Docker host yet") {
		t.Errorf("a Dockhand with no hosts = %v", out)
	}
	// with no host there is nothing to ask about
	if made := dock.Requests("GET /api/dashboard/stats"); len(made) != 0 {
		t.Errorf("the hosts' state was asked for when there are none: %v", made)
	}
}

// The hosts are still listed when how they are doing cannot be read, and none is said to be online or offline.
func TestEnvListWithoutStats(t *testing.T) {
	t.Parallel()

	dock := dockhandtest.New(t)
	dock.Answer("GET /api/environments", twoEnvironments)
	dock.Fail("GET /api/dashboard/stats", http.StatusInternalServerError, "Failed to collect stats")

	out := session(t, dock, Options{}).Call(t, "env_list", nil)
	rows := acctest.Rows(t, out["environments"], "environments")
	if len(rows) != 2 || rows[0]["online"] != nil || rows[0]["containers"] != nil {
		t.Errorf("environments = %v, want both and nothing said of their state", rows)
	}
	if said := acctest.Str(out["note"]); !strings.Contains(said, "could not be read") || !strings.Contains(said, "Failed to collect stats") {
		t.Errorf("the note = %q", said)
	}
}

func TestEnvListRefused(t *testing.T) {
	t.Parallel()

	dock := dockhandtest.New(t)
	dock.Unauthorized()

	msg := session(t, dock, Options{}).CallErr(t, "env_list", nil)
	if !strings.Contains(msg, "the environments could not be listed") || !strings.Contains(msg, "DOCKHAND_TOKEN") {
		t.Errorf("a token refused = %q", msg)
	}
}
