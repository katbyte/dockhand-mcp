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

// A host Dockhand cannot reach is added as one whose agent calls in: saved with its token, reached once an agent started with that token calls, cut
// off when the token is replaced and again when it is revoked, and forgotten with its tokens. Once with a token Dockhand makes and once with one of
// the caller's own.
func TestJourneyAnAgentCallsInItsTokenReplacedAndRevoked(t *testing.T) {
	const name, host, agent = "acc-journey-caller", testenv.Beta, "caller-" + testenv.Beta
	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	ctx := context.WithoutCancel(t.Context())
	t.Cleanup(func() {
		if err := world.HangUp(ctx, host); err != nil {
			t.Error(err)
		}
	})
	listed := func() map[string]map[string]any {
		return named(acctest.Rows(t, call(t, "env_list", nil)["environments"], "environments"), "env")
	}
	agents := func(more map[string]any) (connected []map[string]any, waiting []string, tokens []map[string]any) {
		args := map[string]any{"env": name}
		maps.Copy(args, more)
		out := call(t, "agent_list", args)

		return acctest.Rows(t, out["connected"], "connected"), acctest.Strs(t, out["waiting"], "waiting"), acctest.Rows(t, out["tokens"], "tokens")
	}
	calledIn := func() bool {
		connected, _, _ := agents(nil)

		return len(connected) == 1
	}
	cutOff := func() bool {
		connected, waiting, _ := agents(nil)

		return len(connected) == 0 && slices.Equal(waiting, []string{name})
	}
	// a run that was cut short may have left it behind
	if listed()[name] != nil {
		call(t, "env_delete", map[string]any{"env": name, "confirm": true})
	}
	before := len(listed())

	for round, own := range []string{"", "acc-a-token-of-my-own-choosing"} {
		add := func(more map[string]any) map[string]any {
			args := map[string]any{"name": name, "connection": testenv.ConnectionCaller, "labels": []string{"far"}}
			if own != "" {
				args["agent_token"] = own
			}
			maps.Copy(args, more)

			return args
		}

		// there is nothing to try of an agent that has not called, and one that calls is at no address
		if msg := refused(t, "env_test", map[string]any{"connection": testenv.ConnectionCaller}); !strings.Contains(msg, "cannot be tried before it is saved") {
			t.Errorf("round %d: trying an agent that is to call = %q", round, msg)
		}
		if msg := refused(t, "env_create", add(map[string]any{"host": "somewhere"})); !strings.Contains(msg, "takes no host") {
			t.Errorf("round %d: an agent that calls, given an address = %q", round, msg)
		}
		if dry := call(t, "env_create", add(map[string]any{"dry_run": true})); len(acctest.Strs(t, dry["changes"], "changes")) != 2 || dry["environment"] != nil || dry["token"] != nil || len(listed()) != before {
			t.Fatalf("round %d: a dry run of adding it = %v", round, dry)
		}

		made := call(t, "env_create", add(nil))
		token := cmp(own, acctest.Str(made["token"]))
		if row := acctest.Object(t, made["environment"], "environment"); row["env"] != name || row["connection"] != testenv.ConnectionCaller || !acctest.IsBool(row["online"], false) || made["found"] != nil || len(token) < 16 || (own != "" && (made["token"] != nil || strings.Contains(flat(made), own))) || !strings.Contains(acctest.Str(made["note"]), "no agent has called in yet") {
			t.Fatalf("round %d: added = %v", round, made)
		}

		// nothing has called: it is waited for, its token has never been used, and trying it says so
		connected, waiting, tokens := agents(nil)
		if len(connected) != 0 || !slices.Equal(waiting, []string{name}) || len(tokens) != 1 || !acctest.IsBool(tokens[0]["active"], true) || tokens[0]["starts"] != token[:8] || tokens[0]["last_used"] != nil || tokens[0]["env"] != name {
			t.Fatalf("round %d: before any agent called = %v, %v, %v", round, connected, waiting, tokens)
		}
		if tried := call(t, "env_test", map[string]any{"env": name}); !acctest.IsBool(tried["reachable"], false) || !strings.Contains(acctest.Str(tried["error"]), "not connected") {
			t.Errorf("round %d: tried before any agent called = %v", round, tried)
		}

		// an agent started with the token calls in, and the host is reached through it like any other
		if err := world.CallIn(ctx, host, token); err != nil {
			t.Fatal(err)
		}
		if !acctest.Eventually(calledIn) {
			t.Fatalf("round %d: the agent started with the token did not call in", round)
		}
		connected, waiting, tokens = agents(nil)
		if c := connected[0]; c["env"] != name || c["agent"] != agent || c["agent_version"] == nil || c["docker_version"] == nil || c["hostname"] == nil || c["since"] == nil || len(waiting) != 0 || tokens[0]["last_used"] == nil {
			t.Errorf("round %d: once its agent called = %v, %v, %v", round, connected, waiting, tokens)
		}
		if tried := call(t, "env_test", map[string]any{"env": name}); !acctest.IsBool(tried["reachable"], true) || tried["agent"] != agent || tried["docker_version"] == nil {
			t.Errorf("round %d: tried once its agent called = %v", round, tried)
		}
		if through := named(acctest.Rows(t, call(t, "container_list", map[string]any{"env": name})["containers"], "containers"), "name"); through[solo] == nil || through[solo]["env"] != name {
			t.Errorf("round %d: what runs on the host, read through the agent that called = %v", round, through)
		}
		if got := call(t, "env_get", map[string]any{"env": name}); got["connection"] != testenv.ConnectionCaller || got["agent"] != agent || got["agent_last_seen"] == nil || got["address"] != nil || acctest.Object(t, got["machine"], "machine")["docker_version"] == nil || !acctest.IsBool(listed()[name]["online"], true) {
			t.Errorf("round %d: the environment in full once its agent called = %v", round, got)
		}
		// a token is for an agent that calls in, and for no other kind of host
		if msg := refused(t, "agent_token_create", map[string]any{"env": host}); !strings.Contains(msg, "is reached by "+testenv.ConnectionAgent) {
			t.Errorf("round %d: a token for a host Dockhand calls = %q", round, msg)
		}

		// its token replaced: what that ends is said first and nothing is ended
		dry := call(t, "agent_token_create", map[string]any{"env": name, "name": "second", "expires": "30d", "dry_run": true})
		if changes := acctest.Strs(t, dry["changes"], "changes"); len(changes) != 3 || !strings.Contains(changes[1], "would end the token "+name) || !strings.Contains(changes[2], "would cut off the agent "+agent) || !strings.Contains(acctest.Str(dry["note"]), "five minutes") || dry["token"] != nil || !calledIn() {
			t.Fatalf("round %d: a dry run of replacing its token = %v", round, dry)
		}
		// the agent is held still before its token is ended: left running it would call again with the old one, and Dockhand turns its address away
		// for five minutes after
		if err := world.Freeze(ctx, host); err != nil {
			t.Fatal(err)
		}
		second := call(t, "agent_token_create", map[string]any{"env": name, "name": "second", "expires": "30d"})
		next := acctest.Str(second["token"])
		if changes := acctest.Strs(t, second["changes"], "changes"); len(changes) != 3 || !strings.HasPrefix(changes[2], "cut off the agent "+agent) || len(next) < 32 || next == token || second["expires"] == nil || !strings.Contains(acctest.Str(second["note"]), "this is the only time the token is shown") {
			t.Fatalf("round %d: replacing its token = %v", round, second)
		}
		if !acctest.Eventually(cutOff) {
			t.Errorf("round %d: the agent that called with the token before is still connected", round)
		}
		// the one that works is listed, and the one before it only when asked for
		if _, _, working := agents(nil); len(working) != 1 || working[0]["name"] != "second" || working[0]["starts"] != next[:8] || working[0]["expires"] != second["expires"] {
			t.Errorf("round %d: the token that works = %v", round, working)
		}
		if _, _, all := agents(map[string]any{"revoked": true}); len(all) != 2 || !acctest.IsBool(all[0]["active"], true) || !acctest.IsBool(all[1]["active"], false) || all[1]["starts"] != token[:8] {
			t.Errorf("round %d: every token it has had = %v", round, all)
		}

		// an agent started with the new one calls in; then that token is revoked, said first and then done, and a second go finds nothing to do
		if err := world.CallIn(ctx, host, next); err != nil {
			t.Fatal(err)
		}
		if !acctest.Eventually(calledIn) {
			t.Fatalf("round %d: the agent started with the new token did not call in", round)
		}
		id := acctest.Num(t, second["token_id"], "token_id")
		if preview := call(t, "agent_token_delete", map[string]any{"id": id}); acctest.BoolOf(preview["revoked"]) || len(acctest.Strs(t, preview["would_go"], "would_go")) != 2 || !strings.Contains(acctest.Str(preview["note"]), "nothing was revoked") || !calledIn() {
			t.Fatalf("round %d: a revoking not confirmed = %v", round, preview)
		}
		if err := world.Freeze(ctx, host); err != nil {
			t.Fatal(err)
		}
		if gone := call(t, "agent_token_delete", map[string]any{"id": id, "confirm": true}); !acctest.BoolOf(gone["revoked"]) || acctest.IsBool(acctest.Object(t, gone["token"], "token")["active"], true) || !strings.Contains(acctest.Str(gone["note"]), "the agent "+agent+" was cut off") {
			t.Fatalf("round %d: revoking = %v", round, gone)
		}
		if !acctest.Eventually(cutOff) {
			t.Errorf("round %d: the agent whose token was revoked is still connected", round)
		}
		if again := call(t, "agent_token_delete", map[string]any{"id": id, "confirm": true}); acctest.BoolOf(again["revoked"]) || !strings.Contains(acctest.Str(again["note"]), "nothing to revoke") {
			t.Errorf("round %d: revoking it again = %v", round, again)
		}
		if msg := refused(t, "agent_token_delete", map[string]any{"id": 987654, "confirm": true}); !strings.Contains(msg, "there is no agent token numbered 987654") {
			t.Errorf("round %d: revoking a token there is none of = %q", round, msg)
		}
		if err := world.HangUp(ctx, host); err != nil {
			t.Fatal(err)
		}

		// forgotten, and its tokens with it; what runs on the host still runs
		if gone := call(t, "env_delete", map[string]any{"env": name, "confirm": true}); !acctest.IsBool(gone["removed"], true) || len(listed()) != before {
			t.Fatalf("round %d: removed = %v, and %d environments are left", round, gone, len(listed()))
		}
		if left := acctest.Rows(t, call(t, "agent_list", map[string]any{"revoked": true})["tokens"], "tokens"); slices.ContainsFunc(left, func(k map[string]any) bool { return k["name"] == "second" || k["env"] == name }) {
			t.Errorf("round %d: the tokens of an environment that was forgotten = %v", round, left)
		}
		if got := call(t, "container_get", map[string]any{"container": solo}); got["env"] != host || got["state"] != "running" {
			t.Errorf("round %d: %s after the environment was forgotten = %v", round, solo, got)
		}
	}
}

// cmp is the first of two texts that is not empty.
func cmp(a, b string) string {
	if a != "" {
		return a
	}

	return b
}

// What a host's Docker holds is counted by kind, agrees with what the lists of each kind say, and tells a volume's size, which nothing else does.
func TestEnvDisk(t *testing.T) {
	const volume = "acc-disk-held"
	for _, host := range []string{testenv.Alpha, testenv.Beta} {
		ctx := context.WithoutCancel(t.Context())
		_, _ = world.Docker(ctx, host, "volume", "rm", "-f", volume)
		// two megabytes written into a volume by a container that has ended, so that nothing mounts it
		if _, err := world.Docker(ctx, host, "run", "--rm", "-v", volume+":/held", world.Image, "sh", "-c", "head -c 2097152 /dev/zero > /held/two-megabytes"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := world.Docker(ctx, host, "volume", "rm", "-f", volume); err != nil {
				t.Errorf("removing %s from %s: %v", volume, host, err)
			}
		})

		out := call(t, "env_disk", map[string]any{"env": host, "biggest": 50})
		images, containers, volumes := acctest.Object(t, out["images"], "images"), acctest.Object(t, out["containers"], "containers"), acctest.Object(t, out["volumes"], "volumes")
		// each kind is as many as its own list says
		if acctest.Num(t, images["count"], "count") != acctest.Num(t, call(t, "image_list", map[string]any{"env": host})["total"], "total") || acctest.Num(t, containers["count"], "count") != acctest.Num(t, call(t, "container_list", map[string]any{"env": host})["total"], "total") || acctest.Num(t, volumes["count"], "count") != acctest.Num(t, call(t, "volume_list", map[string]any{"env": host})["total"], "total") {
			t.Errorf("%s: the counts = %v, %v, %v", host, images, containers, volumes)
		}
		// the image everything here runs from is in use, and takes something
		if used := named(acctest.Rows(t, images["biggest"], "biggest"), "name")[world.Image]; used == nil || !acctest.IsBool(used["in_use"], true) || acctest.Decimal(t, used["mb"], "mb") <= 0 || acctest.Decimal(t, images["mb"], "mb") <= 0 {
			t.Errorf("%s: the images = %v", host, images)
		}
		// the volume's size is told, and that nothing mounts it: the biggest there is, on hosts that hold next to nothing
		biggest := acctest.Rows(t, volumes["biggest"], "biggest")
		if len(biggest) == 0 || biggest[0]["name"] != volume || acctest.Decimal(t, biggest[0]["mb"], "mb") < 2 || !acctest.IsBool(biggest[0]["in_use"], false) || acctest.Decimal(t, volumes["unused_mb"], "unused_mb") < 2 || acctest.Num(t, volumes["unused"], "unused") < 1 {
			t.Errorf("%s: the volumes = %v", host, volumes)
		}
		if out["env"] != host || acctest.Decimal(t, out["total_mb"], "total_mb") < acctest.Decimal(t, images["mb"], "mb")+2 || acctest.Decimal(t, out["unused_mb"], "unused_mb") < 2 {
			t.Errorf("%s: the whole of it = %v", host, out)
		}
		// only as many of the biggest as are asked for
		if one := acctest.Object(t, call(t, "env_disk", map[string]any{"env": host, "biggest": 1})["volumes"], "volumes"); len(acctest.Rows(t, one["biggest"], "biggest")) != 1 {
			t.Errorf("%s: the one biggest volume = %v", host, one)
		}
	}

	if msg := refused(t, "env_disk", nil); !strings.Contains(msg, "env is required") {
		t.Errorf("the disk of no host = %q", msg)
	}
	if msg := refused(t, "env_disk", map[string]any{"env": "nowhere"}); !strings.Contains(msg, "no environment is named") {
		t.Errorf("the disk of a host there is none of = %q", msg)
	}
	if msg := refused(t, "env_disk", map[string]any{"env": testenv.Alpha, "biggest": 51}); !strings.Contains(msg, "biggest is 51") {
		t.Errorf("more of the biggest than are given = %q", msg)
	}
}

// Dockhand's own filesystem is listed and read where it shows it, a folder is made on it once, and the parts it keeps to itself are said to be kept.
func TestServerFilesReadAndMkdir(t *testing.T) {
	const dir = "/srv/acc-own"
	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	ctx := context.WithoutCancel(t.Context())
	t.Cleanup(func() {
		if err := world.Unlay(ctx, dir); err != nil {
			t.Errorf("removing %s: %v", dir, err)
		}
	})
	if err := world.Lay(ctx, dir+"/web/compose.yaml", "services:\n  one:\n    image: "+world.Image+"\n"); err != nil {
		t.Fatal(err)
	}
	if err := world.Lay(ctx, dir+"/web/.env", "TAG=1.2\nDB_PASSWORD=not-a-real-password\n"); err != nil {
		t.Fatal(err)
	}

	// the root shows where stacks are kept and leaves out what Dockhand keeps to itself
	root := named(acctest.Rows(t, call(t, "server_files", nil)["entries"], "entries"), "name")
	if root["srv"] == nil || root["srv"]["type"] != "directory" || root["app"] == nil || root["etc"] != nil || root["proc"] != nil {
		t.Errorf("the root = %v", root)
	}
	listed := call(t, "server_files", map[string]any{"path": dir + "/web/"})
	if entries := acctest.Rows(t, listed["entries"], "entries"); listed["path"] != dir+"/web" || acctest.Num(t, listed["total"], "total") != 2 || entries[0]["name"] != ".env" || entries[1]["name"] != "compose.yaml" || entries[1]["type"] != "file" || acctest.Num(t, entries[0]["size"], "size") != 40 || entries[0]["mode"] != "644" || entries[0]["modified"] == nil {
		t.Errorf("a folder of a stack = %v", listed)
	}
	if one := call(t, "server_files", map[string]any{"path": dir + "/web", "limit": 1}); len(acctest.Rows(t, one["entries"], "entries")) != 1 || !strings.Contains(acctest.Str(one["note"]), "the first 1 are listed") {
		t.Errorf("one entry of it = %v", one)
	}

	// a file read, its secret hidden until asked for
	read := call(t, "server_read", map[string]any{"path": dir + "/web/.env"})
	if !strings.Contains(acctest.Str(read["content"]), "TAG=1.2") || strings.Contains(flat(read), "not-a-real-password") || acctest.Num(t, read["hidden"], "hidden") != 1 || read["modified"] == nil {
		t.Errorf("a file with a secret in it = %v", read)
	}
	if shown := call(t, "server_read", map[string]any{"path": dir + "/web/.env", "reveal_secrets": true}); !strings.Contains(acctest.Str(shown["content"]), "DB_PASSWORD=not-a-real-password") || shown["hidden"] != nil {
		t.Errorf("the same with its secrets asked for = %v", shown)
	}
	if start := call(t, "server_read", map[string]any{"path": dir + "/web/compose.yaml", "max_chars": 9}); start["content"] != "services:" || !acctest.IsBool(start["truncated"], true) {
		t.Errorf("the start of a file = %v", start)
	}

	for what, tc := range map[string]struct {
		tool string
		path string
		want string
	}{
		"listing a part kept back":     {"server_files", "/etc", "is not shown: Dockhand keeps parts of its own filesystem to itself"},
		"listing what is not there":    {"server_files", dir + "/nowhere", "there is no " + dir + "/nowhere"},
		"listing a file":               {"server_files", dir + "/web/.env", "is a file and not a directory"},
		"listing a path not from root": {"server_files", "srv", "it begins with a /"},
		"reading Dockhand's database":  {"server_read", "/app/data/db/dockhand.db", "is not shown"},
		"reading what is not there":    {"server_read", dir + "/web/nowhere.txt", "there is no file"},
		"reading a directory":          {"server_read", dir + "/web", "is a directory and not a file"},
		"making one in a part kept":    {"server_mkdir", "/etc/acc-own", "is not made: Dockhand keeps parts"},
		"making one where a file is":   {"server_mkdir", dir + "/web/.env", "is a file, so no directory can be made there"},
		"making one not from the root": {"server_mkdir", "srv/acc-own", "it begins with a /"},
	} {
		if msg := refused(t, tc.tool, map[string]any{"path": tc.path}); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}

	// a folder made with the ones above it: said first, then made, and a second go finds it there
	deep := dir + "/kept/deeper"
	if dry := call(t, "server_mkdir", map[string]any{"path": deep, "dry_run": true}); len(acctest.Strs(t, dry["changes"], "changes")) != 1 || !strings.Contains(refused(t, "server_files", map[string]any{"path": deep}), "there is no") {
		t.Fatalf("a dry run of making a folder = %v", dry)
	}
	if made := call(t, "server_mkdir", map[string]any{"path": deep + "/"}); len(acctest.Strs(t, made["changes"], "changes")) != 1 || made["path"] != deep {
		t.Fatalf("making a folder = %v", made)
	}
	if under := acctest.Rows(t, call(t, "server_files", map[string]any{"path": dir + "/kept"})["entries"], "entries"); len(under) != 1 || under[0]["name"] != "deeper" || under[0]["type"] != "directory" {
		t.Errorf("what was made = %v", under)
	}
	if again := call(t, "server_mkdir", map[string]any{"path": deep}); len(acctest.Strs(t, again["changes"], "changes")) != 0 || !strings.Contains(acctest.Str(again["note"]), "nothing to do") {
		t.Errorf("making it again = %v", again)
	}
}
