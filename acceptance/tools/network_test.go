//go:build acceptance

package tools_test

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// What the network journey makes on a host and takes away again: a network with a range of its own, which nothing else on the host has.
const (
	journeyNetwork = "acc-journey-net"
	journeySubnet  = "172.31.250.0/24"
	journeyGateway = "172.31.250.1"
	// webNetwork is the network compose made for the web stack
	webNetwork = webStack + "_default"
)

// networkOn reports whether a host's own Docker has a network by a name.
func networkOn(t *testing.T, host, name string) bool {
	t.Helper()

	_, err := world.Docker(context.WithoutCancel(t.Context()), host, "network", "inspect", name)

	return err == nil
}

// networksOf is the names of the networks a host's own Docker says a container is on.
func networksOf(t *testing.T, host, container string) []string { //nolint:unparam // the journey runs on one host, and the call still says which
	t.Helper()

	out, err := world.Docker(context.WithoutCancel(t.Context()), host, "inspect", "--format", "{{range $name, $_ := .NetworkSettings.Networks}}{{$name}} {{end}}", container)
	if err != nil {
		t.Fatalf("asking %s which networks %s is on: %v", host, container, err)
	}

	return strings.Fields(out)
}

func TestNetworkList(t *testing.T) {
	out := call(t, "network_list", map[string]any{"env": testenv.Alpha})
	rows := named(acctest.Rows(t, out["networks"], "networks"), "name")

	// Docker's own three, marked as such, and the containers started on their own are on the first of them, stopped ones too
	for _, name := range []string{"bridge", "host", "none"} {
		if rows[name] == nil || !acctest.IsBool(rows[name]["builtin"], true) {
			t.Errorf("%s = %v, want it there and marked as Docker's own", name, rows[name])
		}
	}
	on := acctest.Strs(t, rows["bridge"]["containers"], "containers")
	for _, want := range []string{loose, crashed, looping} {
		if !slices.Contains(on, want) {
			t.Errorf("%s is not listed on the bridge: %v", want, on)
		}
	}
	// the network compose made for the stack, with both its containers
	web := rows[webNetwork]
	if web == nil || web["driver"] != "bridge" || web["builtin"] != nil || len(acctest.Strs(t, web["subnets"], "subnets")) != 1 || strings.Join(acctest.Strs(t, web["containers"], "containers"), "|") != webBack+"|"+webFront {
		t.Errorf("the web stack's network = %v", web)
	}

	// on every host, the same name is listed once for each
	both := acctest.Rows(t, call(t, "network_list", map[string]any{"name": "bridge"})["networks"], "networks")
	if len(both) != 2 || both[0]["env"] != testenv.Alpha || both[1]["env"] != testenv.Beta || strings.Join(acctest.Strs(t, both[1]["containers"], "containers"), "|") != solo {
		t.Errorf("the bridges of both hosts = %v", both)
	}
	for _, row := range acctest.Rows(t, call(t, "network_list", map[string]any{"unused": true})["networks"], "networks") {
		if len(acctest.Strs(t, row["containers"], "containers")) != 0 || row["builtin"] != nil {
			t.Errorf("listed as unused, with a container on it or one of Docker's own: %v", row)
		}
	}
}

func TestNetworkGet(t *testing.T) {
	out := call(t, "network_get", map[string]any{"network": webNetwork})

	if out["env"] != testenv.Alpha || out["name"] != webNetwork || out["stack"] != webStack || acctest.Str(out["created"]) == "" || len(acctest.Str(out["full_id"])) != 64 {
		t.Errorf("the web stack's network = %v", out)
	}
	if ranges := acctest.Rows(t, out["ranges"], "ranges"); len(ranges) != 1 || acctest.Str(ranges[0]["subnet"]) == "" || acctest.Str(ranges[0]["gateway"]) == "" {
		t.Errorf("its ranges = %v", out["ranges"])
	}
	// each container on it with its state, its address there and the hardware address of its end
	members := named(acctest.Rows(t, out["members"], "members"), "container")
	for _, name := range []string{webFront, webBack} {
		if m := members[name]; m == nil || m["state"] != "running" || acctest.Str(m["address"]) == "" || acctest.Str(m["mac"]) == "" {
			t.Errorf("%s on the network = %v", name, m)
		}
	}

	// by the start of its id; and a name every host has is not guessed at
	if found := call(t, "network_get", map[string]any{"network": acctest.Str(out["id"])}); found["full_id"] != out["full_id"] {
		t.Errorf("found by the start of its id = %v", found)
	}
	if msg := refused(t, "network_get", map[string]any{"network": "bridge"}); !strings.Contains(msg, "more than one network") || !strings.Contains(msg, "bridge on "+testenv.Alpha) || !strings.Contains(msg, "bridge on "+testenv.Beta) {
		t.Errorf("the bridge, with no host said = %q", msg)
	}
	// a container that is not running is on its network with no address
	bridge := named(acctest.Rows(t, call(t, "network_get", map[string]any{"network": "bridge", "env": testenv.Alpha})["members"], "members"), "container")
	if m := bridge[crashed]; m == nil || m["state"] != "exited" || m["address"] != nil {
		t.Errorf("a stopped container on the bridge = %v", m)
	}
}

// A network is made behind the agent with a range of its own, a container is put on it and taken off, and it is removed and pruned, twice over, each
// step held against the host's own Docker. On the way it is shown that a network only a stopped container is on is neither removed nor pruned unless
// that is asked for by name, since the container could not be started again without it.
func TestJourneyANetworkMadeJoinedLeftAndRemoved(t *testing.T) {
	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	const host = testenv.Beta
	ctx := context.WithoutCancel(t.Context())
	on := func(more map[string]any) map[string]any {
		args := map[string]any{"env": host}
		maps.Copy(args, more)

		return args
	}
	// a run that was cut short may have left it behind
	_, _ = world.Docker(ctx, host, "network", "rm", "-f", journeyNetwork)
	t.Cleanup(func() { _, _ = world.Docker(ctx, host, "network", "rm", "-f", journeyNetwork) })
	user := started(t, host, "acc-journey-net-user")

	for round := range 2 {
		// said first, then made as asked, and a second by its name refused
		if dry := call(t, "network_create", on(map[string]any{"name": journeyNetwork, "dry_run": true})); !acctest.IsBool(dry["dry_run"], true) || networkOn(t, host, journeyNetwork) {
			t.Fatalf("round %d: a dry run of making it = %v", round, dry)
		}
		made := call(t, "network_create", on(map[string]any{"name": journeyNetwork, "subnet": journeySubnet, "gateway": journeyGateway, "internal": true, "labels": map[string]any{"made-by": "journey"}}))
		row := acctest.Object(t, made["network"], "network")
		if !acctest.IsBool(made["created"], true) || row["name"] != journeyNetwork || row["driver"] != "bridge" || !acctest.IsBool(row["internal"], true) || strings.Join(acctest.Strs(t, row["subnets"], "subnets"), "|") != journeySubnet || len(acctest.Strs(t, row["containers"], "containers")) != 0 {
			t.Fatalf("round %d: made = %v", round, made)
		}
		if said, err := world.Docker(ctx, host, "network", "inspect", "--format", "{{(index .IPAM.Config 0).Subnet}} {{(index .IPAM.Config 0).Gateway}} {{.Internal}} {{index .Labels \"made-by\"}}", journeyNetwork); err != nil || strings.TrimSpace(said) != journeySubnet+" "+journeyGateway+" true journey" {
			t.Fatalf("round %d: the host's own Docker says of it %q, %v", round, said, err)
		}
		if msg := refused(t, "network_create", on(map[string]any{"name": journeyNetwork})); !strings.Contains(msg, "already has a network named "+journeyNetwork) {
			t.Errorf("round %d: made a second time = %q", round, msg)
		}

		// a container put on it: said first, then done, with an address in the range; and a second time nothing
		if dry := call(t, "network_connect", map[string]any{"network": journeyNetwork, "container": user, "dry_run": true}); !acctest.IsBool(dry["is"], false) || slices.Contains(networksOf(t, host, user), journeyNetwork) {
			t.Fatalf("round %d: a dry run of the connection = %v", round, dry)
		}
		joined := call(t, "network_connect", map[string]any{"network": journeyNetwork, "container": user})
		if joined["env"] != host || !acctest.IsBool(joined["was"], false) || !acctest.IsBool(joined["is"], true) || !strings.HasPrefix(acctest.Str(joined["address"]), "172.31.250.") || !slices.Contains(networksOf(t, host, user), journeyNetwork) {
			t.Fatalf("round %d: connected = %v, and the host says it is on %v", round, joined, networksOf(t, host, user))
		}
		if again := call(t, "network_connect", map[string]any{"network": journeyNetwork, "container": user}); !acctest.IsBool(again["was"], true) || !strings.Contains(acctest.Str(again["note"]), "already on") {
			t.Errorf("round %d: connected a second time = %v", round, again)
		}
		got := call(t, "network_get", on(map[string]any{"network": journeyNetwork}))
		if members := acctest.Rows(t, got["members"], "members"); len(members) != 1 || members[0]["container"] != user || members[0]["address"] != joined["address"] || acctest.Object(t, got["labels"], "labels")["made-by"] != "journey" {
			t.Errorf("round %d: the network with the container on it = %v", round, got)
		}

		// with a running container on it, it is not removed, and a prune would not take it
		if msg := refused(t, "network_delete", on(map[string]any{"network": journeyNetwork, "confirm": true})); !strings.Contains(msg, "has 1 containers on it ("+user+" (running))") || !networkOn(t, host, journeyNetwork) {
			t.Fatalf("round %d: removing a network in use = %q", round, msg)
		}
		if would := call(t, "network_prune", on(nil)); slices.Contains(acctest.Strs(t, acctest.OrEmptyList(would["would_go"]), "would_go"), journeyNetwork) {
			t.Fatalf("round %d: a prune would take a network a running container is on: %v", round, would)
		}

		// its container stopped, Docker would prune it and leave the container unable to start: that is said, and refused until asked for
		call(t, "container_stop", map[string]any{"container": user})
		would := call(t, "network_prune", on(nil))
		if !slices.Contains(acctest.Strs(t, would["would_go"], "would_go"), journeyNetwork) || !slices.Contains(acctest.Strs(t, would["would_strand"], "would_strand"), user+" (exited), on "+journeyNetwork) {
			t.Fatalf("round %d: what a prune would take with the container stopped = %v", round, would)
		}
		if msg := refused(t, "network_prune", on(map[string]any{"confirm": true})); !strings.Contains(msg, "nothing was removed") || !strings.Contains(msg, user) || !networkOn(t, host, journeyNetwork) {
			t.Fatalf("round %d: a prune that would strand, confirmed and not told to = %q", round, msg)
		}
		if msg := refused(t, "network_delete", on(map[string]any{"network": journeyNetwork, "confirm": true})); !strings.Contains(msg, user+" (exited)") || !networkOn(t, host, journeyNetwork) {
			t.Fatalf("round %d: removing a network a stopped container is on = %q", round, msg)
		}
		// which is why the container can still be started
		if back := call(t, "container_start", map[string]any{"container": user}); back["is"] != "running" {
			t.Fatalf("round %d: the container started again = %v", round, back)
		}

		// taken off it, and a second time nothing
		left := call(t, "network_disconnect", map[string]any{"network": journeyNetwork, "container": user})
		if !acctest.IsBool(left["was"], true) || !acctest.IsBool(left["is"], false) || left["address"] != nil || slices.Contains(networksOf(t, host, user), journeyNetwork) {
			t.Fatalf("round %d: disconnected = %v, and the host says it is on %v", round, left, networksOf(t, host, user))
		}
		if again := call(t, "network_disconnect", map[string]any{"network": journeyNetwork, "container": user}); !acctest.IsBool(again["was"], false) || !strings.Contains(acctest.Str(again["note"]), "is not on") {
			t.Errorf("round %d: disconnected a second time = %v", round, again)
		}
		if stateOf(t, host, user) != "running" || !slices.Contains(networksOf(t, host, user), "bridge") {
			t.Fatalf("round %d: the container did not come through being taken off the network", round)
		}

		// with nothing on it: removed on the first round, and pruned on the second, said first each time
		if round == 0 {
			if preview := call(t, "network_delete", on(map[string]any{"network": journeyNetwork})); !acctest.IsBool(preview["removed"], false) || !networkOn(t, host, journeyNetwork) {
				t.Fatalf("removing it, not confirmed = %v", preview)
			}
			if gone := call(t, "network_delete", on(map[string]any{"network": journeyNetwork, "confirm": true})); !acctest.IsBool(gone["removed"], true) || networkOn(t, host, journeyNetwork) {
				t.Fatalf("removed = %v", gone)
			}

			continue
		}
		if preview := call(t, "network_prune", on(nil)); !slices.Contains(acctest.Strs(t, preview["would_go"], "would_go"), journeyNetwork) || preview["would_strand"] != nil || !networkOn(t, host, journeyNetwork) {
			t.Fatalf("pruning, not confirmed = %v", preview)
		}
		pruned := call(t, "network_prune", on(map[string]any{"confirm": true}))
		if !slices.Contains(acctest.Strs(t, pruned["removed"], "removed"), journeyNetwork) || pruned["stranded"] != nil || networkOn(t, host, journeyNetwork) || !networkOn(t, host, "bridge") {
			t.Fatalf("pruned = %v", pruned)
		}
	}

	// Docker's own cannot be removed, and a range that is none is refused in Docker's words
	if msg := refused(t, "network_delete", on(map[string]any{"network": "bridge", "confirm": true})); !strings.Contains(msg, "one of the three networks every Docker host has") {
		t.Errorf("removing the bridge = %q", msg)
	}
	if msg := refused(t, "network_create", on(map[string]any{"name": journeyNetwork, "subnet": "not-a-range"})); !strings.Contains(msg, "was not made on "+host) || networkOn(t, host, journeyNetwork) {
		t.Errorf("a network with a range that is none = %q", msg)
	}
}
