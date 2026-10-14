//go:build acceptance

package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// backupPassword is what the suites' repositories are encrypted with: made up, and good for nothing but the throwaway backup server.
const backupPassword = "acc-not-a-real-backup-password" //nolint:gosec // G101: a made-up value for the throwaway server, which is no credential of anything real

// backupsClean takes away every destination a backup test made, with the backups kept in it, now and when the test ends.
func backupsClean(t *testing.T, prefix string) {
	t.Helper()

	clean := func() {
		listed := acctest.RowsOf(call(t, "backup_destination_list", nil)["destinations"])
		for name := range named(listed, "name") {
			if strings.HasPrefix(name, prefix) {
				suite.Call(t, "backup_destination_delete", map[string]any{"destination": name, "confirm": true, "even_used": true})
			}
		}
	}
	clean()
	t.Cleanup(clean)
}

// repositoryOn is a repository of a run's own on a backup server: what a run before left there stays there, the server having no call to remove
// one, and holds snapshots this run did not take, under a password this run may not have.
func repositoryOn(server, name string) string {
	return server + "/" + name + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

// heldBy is what a file in a volume of the first host holds, read by the host's own Docker.
func heldBy(t *testing.T, volume, file string) string {
	t.Helper()

	out, err := world.Docker(context.WithoutCancel(t.Context()), testenv.Alpha, "run", "--rm", "-v", volume+":/held", world.Image, "cat", "/held/"+file)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(out)
}

// A container is backed up and had back, with the tools alone: a destination is tried and saved, a backup of a container is made and run twice with
// a change between, the snapshots are listed, looked into, read from, compared and fetched, one is restored beside what is there and then over it,
// the repository is looked after and given a new password, and everything is removed again, each only once what it would take has been named. No
// answer on the way holds the repository's password or the container's token.
func TestJourneyAContainerBackedUpAndHadBack(t *testing.T) {
	const (
		host                 = testenv.Alpha
		prefix               = "acc-journey-backup"
		destination, renamed = prefix + "-store", prefix + "-store-renamed"
		app, volume, copied  = prefix + "-app", prefix + "-data", prefix + "-copy"
		token, rotated       = "acc-not-a-real-container-token", "acc-not-a-real-rotated-password" //nolint:gosec // G101: made-up values for the throwaway server
		restored             = "/acc-journey-restored"
	)
	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	ctx := context.WithoutCancel(t.Context())
	if err := world.MirrorBackupImage(ctx); err != nil {
		t.Fatal(err)
	}
	repository := repositoryOn(world.Backups, prefix)
	without(t, host, []string{app}, []string{volume, copied}, nil)
	backupsClean(t, prefix)
	if _, err := world.Docker(ctx, host, "run", "-d", "--name", app, "-e", "API_TOKEN="+token, "-v", volume+":/data", world.Image, "sh", "-c", "echo first > /data/notes.txt; mkdir -p /data/sub; echo deep > /data/sub/deep.txt; sleep 100000"); err != nil {
		t.Fatal(err)
	}
	// said holds every answer of the journey against the passwords and the token, none of which a tool may give
	said := func(what string, answer map[string]any) map[string]any {
		t.Helper()
		if text := flat(answer); strings.Contains(text, backupPassword) || strings.Contains(text, rotated) || strings.Contains(text, token) {
			t.Errorf("%s gave a password or the token away: %s", what, text)
		}

		return answer
	}
	destinations := func() map[string]map[string]any {
		return named(acctest.Rows(t, call(t, "backup_destination_list", nil)["destinations"], "destinations"), "name")
	}
	before := len(destinations())

	// tried before it is saved: an address that is none is refused, and a server with no repository yet reads as needing one made
	if msg := refused(t, "backup_destination_test", map[string]any{"repository": "ftp://nowhere.example/srv", "password": backupPassword}); !strings.Contains(msg, "Invalid repository") {
		t.Errorf("a kind of repository Dockhand does not take = %q", msg)
	}
	// an SFTP server is a kind Dockhand takes from 1.0.52 on, with a key the tool cannot give, and no kind at all before that: neither saves one
	if msg := refused(t, "backup_destination_create", map[string]any{"name": destination, "repository": "sftp:nobody@nowhere.example:/srv", "password": backupPassword}); (!strings.Contains(msg, "SSH private key") && !strings.Contains(msg, "Invalid repository")) || len(destinations()) != before {
		t.Errorf("an SFTP server for a destination = %q, with %d destinations where there were %d", msg, len(destinations()), before)
	}
	if tried := said("a try", call(t, "backup_destination_test", map[string]any{"repository": repository, "password": backupPassword})); !acctest.IsBool(tried["usable"], false) || !acctest.IsBool(tried["needs_init"], true) {
		t.Errorf("a repository that is not made yet, tried = %v", tried)
	}

	// saved: said first, and then for real, which has Dockhand make the repository
	saving := map[string]any{"name": destination, "repository": repository, "password": backupPassword}
	if dry := said("a dry run", call(t, "backup_destination_create", over(saving, map[string]any{"dry_run": true}))); len(acctest.Strs(t, dry["changes"], "changes")) != 1 || len(destinations()) != before {
		t.Fatalf("a dry run of saving a destination = %v", dry)
	}
	made := said("saving the destination", call(t, "backup_destination_create", saving))
	if row := acctest.Object(t, made["destination"], "destination"); row["name"] != destination || row["repository"] != repository || !acctest.IsBool(row["usable"], true) {
		t.Fatalf("a destination saved = %v", made)
	}
	if msg := refused(t, "backup_destination_create", saving); !strings.Contains(msg, "already named") {
		t.Errorf("a second destination of the same name = %q", msg)
	}
	// one whose password is not the repository's is tried, refused and not kept
	if msg := refused(t, "backup_destination_create", map[string]any{"name": prefix + "-wrong", "repository": repository, "password": "acc-not-the-password"}); !strings.Contains(msg, "nothing was saved") || len(destinations()) != before+1 {
		t.Errorf("a destination with a wrong password = %q, with %d destinations kept", msg, len(destinations()))
	}
	if tried := said("a try of the saved one", call(t, "backup_destination_test", map[string]any{"destination": destination})); !acctest.IsBool(tried["usable"], true) {
		t.Errorf("the saved destination tried = %v", tried)
	}
	if stats := call(t, "backup_destination_check", map[string]any{"destination": destination}); !acctest.IsBool(stats["sound"], true) || acctest.NumOr0(stats["snapshots"]) != 0 {
		t.Errorf("a new repository's stats = %v", stats)
	}

	// a backup of the container: not of one there is none of, nor of a volume it has not, which Dockhand would save
	backing := map[string]any{"env": host, "of": app, "destination": destination, "schedule": "0 3 * * *", "tags": []any{"acc"}}
	if msg := refused(t, "backup_create", over(backing, map[string]any{"of": prefix + "-nothing"})); !strings.Contains(msg, "has no container and no stack named") {
		t.Errorf("a backup of a container there is none of = %q", msg)
	}
	if msg := refused(t, "backup_create", over(backing, map[string]any{"volumes": []any{"acc-no-such-volume"}})); !strings.Contains(msg, "has no volume acc-no-such-volume") || !strings.Contains(msg, volume) {
		t.Errorf("a backup of a volume the container has not = %q", msg)
	}
	if dry := call(t, "backup_create", over(backing, map[string]any{"dry_run": true})); len(acctest.Strs(t, dry["changes"], "changes")) != 1 || acctest.NumOr0(call(t, "backup_list", nil)["total"]) != 0 {
		t.Fatalf("a dry run of making a backup = %v", dry)
	}
	saved := said("making the backup", call(t, "backup_create", backing))
	backup := acctest.Object(t, saved["backup"], "backup")
	number := acctest.NumOr0(backup["id"])
	if number == 0 || backup["of"] != app || backup["type"] != "container" || backup["env"] != host || backup["destination"] != destination || backup["schedule"] != "0 3 * * *" || !acctest.IsBool(backup["enabled"], true) {
		t.Fatalf("a backup made = %v", saved)
	}
	// Dockhand gives a backup that runs by itself a policy of its own when it is given none, which is said
	if not := acctest.Strs(t, saved["not_taken"], "not_taken"); len(not) != 1 || !strings.Contains(not[0], "keep") || acctest.NumOr0(acctest.Object(t, backup["keep"], "keep")["daily"]) != 7 {
		t.Errorf("what Dockhand saved in place of keeping every snapshot = %v, with keep %v", not, backup["keep"])
	}
	if msg := refused(t, "backup_create", backing); !strings.Contains(msg, "already backed up") {
		t.Errorf("a second backup of the same thing = %q", msg)
	}
	if listed := call(t, "backup_list", map[string]any{"env": host, "of": app}); acctest.NumOr0(listed["total"]) != 1 || acctest.NumOr0(call(t, "backup_list", map[string]any{"env": testenv.Beta})["total"]) != 0 {
		t.Errorf("the backups of %s on %s = %v", app, host, listed)
	}
	if row := destinations()[destination]; len(acctest.Strs(t, row["backups"], "backups")) != 1 {
		t.Errorf("the destination's backups = %v", row["backups"])
	}

	// changed: to run only when asked, keeping the newest three, and the same again changes nothing
	edited := call(t, "backup_edit", map[string]any{"backup": number, "schedule": "", "keep": map[string]any{"last": 3}})
	if row := acctest.Object(t, edited["backup"], "backup"); row["schedule"] != nil || acctest.NumOr0(acctest.Object(t, row["keep"], "keep")["last"]) != 3 || acctest.Object(t, row["keep"], "keep")["daily"] != nil || edited["not_taken"] != nil {
		t.Fatalf("a backup changed to run when asked and keep three = %v", edited)
	}
	if again := call(t, "backup_edit", map[string]any{"backup": app, "schedule": "", "keep": map[string]any{"last": 3}}); len(acctest.Strs(t, again["changes"], "changes")) != 0 {
		t.Errorf("the same change again = %v", again)
	}

	// run twice, with the file changed between: each makes a snapshot
	first := said("the first run", call(t, "backup_run", map[string]any{"backup": number, "wait": 240}))
	if first["status"] != "success" || acctest.Str(first["snapshot"]) == "" || acctest.NumOr0(first["files"]) == 0 || !acctest.IsBool(first["pruned"], true) {
		t.Fatalf("the first run = %v", first)
	}
	if _, err := world.Docker(ctx, host, "exec", app, "sh", "-c", "echo second > /data/notes.txt; echo more > /data/added.txt"); err != nil {
		t.Fatal(err)
	}
	second := call(t, "backup_run", map[string]any{"backup": number, "wait": 240})
	if second["status"] != "success" || second["snapshot"] == first["snapshot"] {
		t.Fatalf("the second run = %v", second)
	}
	early, late := acctest.Str(first["snapshot"]), acctest.Str(second["snapshot"])

	// the snapshots: listed by the backup and by the destination, the newest first, and each this Dockhand's own
	taken := acctest.Rows(t, call(t, "backup_snapshot_list", map[string]any{"backup": number})["snapshots"], "snapshots")
	if len(taken) != 2 || taken[0]["id"] != late || taken[1]["id"] != early || taken[0]["of"] != app || taken[0]["env"] != host || acctest.NumOr0(taken[0]["backup"]) != number || !acctest.IsBool(taken[0]["ours"], true) {
		t.Fatalf("the snapshots of the backup = %v", taken)
	}
	if held := call(t, "backup_snapshot_list", map[string]any{"destination": destination, "limit": 1}); acctest.NumOr0(held["total"]) != 2 || len(acctest.Rows(t, held["snapshots"], "snapshots")) != 1 {
		t.Errorf("the newest snapshot of the destination = %v", held)
	}
	at := map[string]any{"destination": destination, "snapshot": early}
	got := said("one snapshot", call(t, "backup_snapshot_get", at))
	if volumes := acctest.Rows(t, got["volumes"], "volumes"); got["of"] != app || got["type"] != "container" || got["env"] != host || got["image"] != world.Image || len(volumes) != 1 || volumes[0]["name"] != volume {
		t.Errorf("the first snapshot = %v", got)
	}

	// looked into, read from, compared and fetched to this machine
	files := named(acctest.Rows(t, call(t, "backup_snapshot_files", over(at, map[string]any{"path": "/volumes/" + volume}))["entries"], "entries"), "name")
	if len(files) != 2 || files["notes.txt"]["type"] != "file" || files["sub"]["type"] != "dir" {
		t.Errorf("what the first snapshot holds of the volume = %v", files)
	}
	if nowhere := call(t, "backup_snapshot_files", over(at, map[string]any{"path": "/volumes/acc-nothing"})); acctest.NumOr0(nowhere["total"]) != 0 || acctest.Str(nowhere["note"]) == "" {
		t.Errorf("a folder the snapshot does not have = %v", nowhere)
	}
	if read := call(t, "backup_snapshot_read", over(at, map[string]any{"path": "/volumes/" + volume + "/notes.txt"})); strings.TrimSpace(acctest.Str(read["content"])) != "first" {
		t.Errorf("the file as the first snapshot has it = %v", read)
	}
	if msg := refused(t, "backup_snapshot_read", over(at, map[string]any{"path": "/volumes/" + volume + "/added.txt"})); !strings.Contains(msg, "has nothing at") {
		t.Errorf("a file made after the snapshot, read from it = %q", msg)
	}
	differs := call(t, "backup_snapshot_diff", map[string]any{"backup": number, "from": early, "to": late})
	if !slices.Contains(acctest.Strs(t, differs["added"], "added"), "/volumes/"+volume+"/added.txt") || !slices.Contains(acctest.Strs(t, differs["changed"], "changed"), "/volumes/"+volume+"/notes.txt") {
		t.Errorf("what changed between the two snapshots = %v", differs)
	}
	here := t.TempDir()
	one := call(t, "backup_snapshot_download", over(at, map[string]any{"path": "/volumes/" + volume + "/notes.txt", "to": here}))
	//nolint:gosec // G304: the path is the test's own, under its own temporary folder
	if content, err := os.ReadFile(filepath.Join(here, "notes.txt")); err != nil || strings.TrimSpace(string(content)) != "first" || !acctest.IsBool(one["archive"], false) {
		t.Errorf("one file fetched = %v, holding %q, %v", one, content, err)
	}
	whole := call(t, "backup_snapshot_download", over(at, map[string]any{"path": "/volumes/" + volume, "to": here}))
	if names := tarred(t, acctest.Str(whole["saved"])); !acctest.IsBool(whole["archive"], true) || !slices.ContainsFunc(names, func(name string) bool { return strings.HasSuffix(name, "sub/deep.txt") }) {
		t.Errorf("a volume fetched whole = %v, holding %v", whole, names)
	}
	if msg := refused(t, "backup_snapshot_download", over(at, map[string]any{"path": "/metadata", "to": here})); !strings.Contains(msg, "only what is under /volumes") {
		t.Errorf("Dockhand's own record of the container, fetched = %q", msg)
	}
	if msg := refused(t, "backup_snapshot_download", over(at, map[string]any{"path": "/volumes/" + volume + "/notes.txt", "to": here})); !strings.Contains(msg, "could not be written here") {
		t.Errorf("a file fetched over one that is there = %q", msg)
	}

	// restored beside what is there: said first, then into a folder of the host, and into a volume of its own
	away := over(at, map[string]any{"to": restored})
	if dry := call(t, "backup_restore", over(away, map[string]any{"dry_run": true})); len(acctest.Rows(t, dry["targets"], "targets")) != 1 || !acctest.IsBool(dry["dry_run"], true) {
		t.Fatalf("a dry run of a restore = %v", dry)
	}
	t.Cleanup(func() {
		_, _ = world.Docker(ctx, host, "run", "--rm", "-v", "/:/hostfs", world.Image, "rm", "-rf", "/hostfs"+restored)
	})
	if put := call(t, "backup_restore", over(away, map[string]any{"wait": 240})); put["status"] != "success" || !slices.Equal(acctest.Strs(t, put["restored"], "restored"), []string{volume}) || put["after"] != nil {
		t.Fatalf("a restore into a folder = %v", put)
	}
	if out, err := world.Docker(ctx, host, "run", "--rm", "-v", restored+":/r", world.Image, "cat", "/r/volumes/"+volume+"/notes.txt"); err != nil || strings.TrimSpace(out) != "first" {
		t.Errorf("the folder the snapshot was restored into holds %q, %v", out, err)
	}
	if put := call(t, "backup_restore", over(at, map[string]any{"volumes_to": map[string]any{volume: copied}, "wait": 240})); put["status"] != "success" || heldBy(t, copied, "notes.txt") != "first" {
		t.Fatalf("a restore into a volume of its own = %v, which holds %q", put, heldBy(t, copied, "notes.txt"))
	}
	if msg := refused(t, "backup_restore", over(at, map[string]any{"volumes_to": map[string]any{"acc-no-such-volume": copied}})); !strings.Contains(msg, "holds no volume acc-no-such-volume") {
		t.Errorf("a volume the snapshot has not, restored = %q", msg)
	}
	// nothing was made or started beside it, as Dockhand does unasked
	if out, err := world.Docker(ctx, host, "ps", "-a", "--filter", "volume="+volume, "--format", "{{.Names}}"); err != nil || strings.TrimSpace(out) != app {
		t.Errorf("the containers on the volume after two restores = %q, %v", out, err)
	}

	// and over what is there: only once it is confirmed, and then the volume is as the snapshot has it, with what was made since gone
	inPlace := over(at, map[string]any{"in_place": true})
	if dry := call(t, "backup_restore", inPlace); !acctest.IsBool(dry["dry_run"], true) || heldBy(t, volume, "notes.txt") != "second" {
		t.Fatalf("a restore over what is there, not confirmed = %v", dry)
	}
	was := startedAt(t, host, app)
	if put := call(t, "backup_restore", over(inPlace, map[string]any{"confirm": true, "after": "start", "wait": 240})); put["status"] != "success" || put["after"] != "start" {
		t.Fatalf("a restore over what is there = %v", put)
	}
	if heldBy(t, volume, "notes.txt") != "first" || heldBy(t, volume, "added.txt") != "" || startedAt(t, host, app) == was {
		t.Errorf("after the restore the volume holds %q and %q, and the container started at %s, as before %s", heldBy(t, volume, "notes.txt"), heldBy(t, volume, "added.txt"), startedAt(t, host, app), was)
	}
	if stopped := call(t, "backup_stop", map[string]any{"restore": true}); !acctest.IsBool(stopped["stopped"], false) {
		t.Errorf("stopping a restore when none runs = %v", stopped)
	}

	// the repository looked after: checked, a part of it read back, and what no snapshot uses cleared out
	for _, check := range []string{"check", "verify"} {
		if looked := call(t, "backup_destination_check", map[string]any{"destination": destination, "check": check, "wait": 240}); !acctest.IsBool(looked["sound"], true) {
			t.Errorf("a %s of the repository = %v", check, looked)
		}
	}
	if msg := refused(t, "backup_destination_task", map[string]any{"destination": destination, "task": "stats"}); !strings.Contains(msg, "backup_destination_check") {
		t.Errorf("a task that only looks = %q", msg)
	}
	if pruned := call(t, "backup_destination_task", map[string]any{"destination": destination, "task": "prune", "wait": 240}); !acctest.IsBool(pruned["done"], true) {
		t.Errorf("a prune of the repository = %v", pruned)
	}

	// changed: renamed, its upkeep and its flags set and the flags cleared again, and a password that is not the repository's refused
	changed := said("changing the destination", call(t, "backup_destination_edit", map[string]any{"destination": destination, "name": renamed, "backup_flags": "--limit-upload 500", "upkeep": map[string]any{"verify": true, "verify_schedule": "0 4 * * 0"}}))
	if row := acctest.Object(t, changed["destination"], "destination"); row["name"] != renamed || row["backup_flags"] != "--limit-upload 500" || !acctest.IsBool(acctest.Object(t, row["upkeep"], "upkeep")["verify"], true) || !acctest.IsBool(acctest.Object(t, row["upkeep"], "upkeep")["prune"], true) {
		t.Fatalf("a destination changed = %v", changed)
	}
	if cleared := call(t, "backup_destination_edit", map[string]any{"destination": renamed, "backup_flags": ""}); acctest.Object(t, cleared["destination"], "destination")["backup_flags"] != nil {
		t.Errorf("a destination's flags cleared = %v", cleared)
	}
	if msg := refused(t, "backup_destination_edit", map[string]any{"destination": renamed, "password": "acc-not-the-password"}); !strings.Contains(msg, "nothing was changed") {
		t.Errorf("a password that is not the repository's = %q", msg)
	}
	if msg := refused(t, "backup_destination_edit", map[string]any{"destination": renamed, "repository": world.Backups + "/acc-elsewhere"}); !strings.Contains(msg, "password is required with repository") {
		t.Errorf("a destination moved with no password given = %q", msg)
	}

	// its password changed in the repository itself: not with a wrong one, and after it a backup still runs
	if msg := refused(t, "backup_destination_rotate_key", map[string]any{"destination": renamed, "current_password": "acc-not-the-password", "new_password": rotated}); !strings.Contains(msg, "does not match") {
		t.Errorf("a new password with a wrong current one = %q", msg)
	}
	if turned := said("a new password", call(t, "backup_destination_rotate_key", map[string]any{"destination": renamed, "current_password": backupPassword, "new_password": rotated})); !acctest.IsBool(turned["rotated"], true) || !acctest.IsBool(turned["usable"], true) {
		t.Fatalf("the repository's password changed = %v", turned)
	}
	if third := call(t, "backup_run", map[string]any{"backup": number, "wait": 240}); third["status"] != "success" {
		t.Fatalf("a run after the password changed = %v", third)
	}

	// a snapshot removed: not one the destination does not hold, and only once it is confirmed
	if msg := refused(t, "backup_snapshot_delete", map[string]any{"destination": renamed, "snapshots": []any{"00000000"}, "confirm": true}); !strings.Contains(msg, "holds no snapshot") {
		t.Errorf("a snapshot that is not there, removed = %q", msg)
	}
	going := map[string]any{"destination": renamed, "snapshots": []any{early}}
	if preview := call(t, "backup_snapshot_delete", going); len(acctest.Strs(t, preview["would_go"], "would_go")) != 1 || acctest.NumOr0(call(t, "backup_snapshot_list", map[string]any{"destination": renamed})["total"]) != 3 {
		t.Fatalf("a snapshot's removal, not confirmed = %v", preview)
	}
	if gone := call(t, "backup_snapshot_delete", over(going, map[string]any{"confirm": true})); !slices.Equal(acctest.Strs(t, gone["removed"], "removed"), []string{early}) || acctest.NumOr0(call(t, "backup_snapshot_list", map[string]any{"destination": renamed})["total"]) != 2 {
		t.Fatalf("a snapshot removed = %v", gone)
	}

	// the destination is not removed while a backup is kept in it, which Dockhand would remove with it without a word
	if msg := refused(t, "backup_destination_delete", map[string]any{"destination": renamed, "confirm": true}); !strings.Contains(msg, "holds 1 backup") || acctest.NumOr0(call(t, "backup_list", nil)["total"]) != 1 {
		t.Errorf("a destination with a backup in it, removed = %q", msg)
	}
	if preview := call(t, "backup_delete", map[string]any{"backup": number, "with_snapshots": true}); !acctest.IsBool(preview["removed"], false) || len(acctest.Strs(t, preview["would_go"], "would_go")) != 2 {
		t.Fatalf("a backup's removal, not confirmed = %v", preview)
	}
	if gone := call(t, "backup_delete", map[string]any{"backup": number, "with_snapshots": true, "confirm": true}); !acctest.IsBool(gone["removed"], true) || acctest.NumOr0(gone["snapshots"]) != 2 {
		t.Fatalf("a backup removed with its snapshots = %v", gone)
	}
	if gone := call(t, "backup_destination_delete", map[string]any{"destination": renamed, "confirm": true}); !acctest.IsBool(gone["removed"], true) || len(destinations()) != before {
		t.Fatalf("the destination removed = %v", gone)
	}
}

// A backup to somewhere Dockhand reaches and the Docker host does not is saved, since Dockhand alone tries a destination, and then never ends: it is
// left running as a job, stopped, and seen to have failed.
func TestBackupStoppedWhenItsHostCannotReachTheDestination(t *testing.T) {
	const (
		host        = testenv.Alpha
		prefix      = "acc-unreached-backup"
		destination = prefix + "-store"
		app, volume = prefix + "-app", prefix + "-data"
	)
	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	ctx := context.WithoutCancel(t.Context())
	if err := world.MirrorBackupImage(ctx); err != nil {
		t.Fatal(err)
	}
	without(t, host, []string{app}, []string{volume}, nil)
	backupsClean(t, prefix)
	if _, err := world.Docker(ctx, host, "run", "-d", "--name", app, "-v", volume+":/data", world.Image, "sleep", "100000"); err != nil {
		t.Fatal(err)
	}

	// by the server's name, which Dockhand finds on the network it shares and a container on the host does not
	made := call(t, "backup_destination_create", map[string]any{"name": destination, "repository": repositoryOn(world.BackupsByName(), prefix), "password": backupPassword})
	if !acctest.IsBool(acctest.Object(t, made["destination"], "destination")["usable"], true) {
		t.Fatalf("a destination Dockhand reaches by name = %v", made)
	}
	saved := call(t, "backup_create", map[string]any{"env": host, "of": app, "destination": destination})
	number := acctest.NumOr0(acctest.Object(t, saved["backup"], "backup")["id"])

	running := call(t, "backup_run", map[string]any{"backup": number, "wait": 5})
	job := acctest.Str(running["job"])
	if !acctest.IsBool(running["running"], true) || job == "" {
		t.Fatalf("a backup the host cannot reach the destination of, after 5 seconds = %v", running)
	}
	if msg := refused(t, "backup_delete", map[string]any{"backup": number, "confirm": true}); !strings.Contains(msg, "running") {
		t.Errorf("a backup removed while it runs = %q", msg)
	}
	if stopped := call(t, "backup_stop", map[string]any{"backup": number}); !acctest.IsBool(stopped["stopped"], true) {
		t.Fatalf("the running backup stopped = %v", stopped)
	}
	ended := call(t, "job_get", map[string]any{"job": job, "wait": 60})
	// Dockhand ends the job of a backup that was stopped as done, with a result that says it was skipped and why
	if result := acctest.Object(t, ended["result"], "result"); !acctest.IsBool(ended["running"], false) || result["status"] != "skipped" || result["reason"] != "Backup cancelled" {
		t.Errorf("the job of a backup that was stopped = %v", ended)
	}
	if listed := acctest.Rows(t, call(t, "backup_list", map[string]any{"backup": number})["backups"], "backups"); listed[0]["last_status"] != "failed" {
		t.Errorf("a backup that was stopped is kept as %v", listed[0])
	}
	if stopped := call(t, "backup_stop", map[string]any{"backup": number}); !acctest.IsBool(stopped["stopped"], false) {
		t.Errorf("stopping a backup that is not running = %v", stopped)
	}
}

// A stack on the host behind the agent is backed up with its compose file, and the snapshot says where putting it back would write both.
func TestBackupOfAStackBehindTheAgent(t *testing.T) {
	const (
		host        = testenv.Beta
		prefix      = "acc-stack-backup"
		destination = prefix + "-store"
		stack       = prefix
		volume      = stack + "_data"
		secret      = "acc-not-a-real-stack-secret" //nolint:gosec // G101: a made-up value for the throwaway server
	)
	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	if err := world.MirrorBackupImage(context.WithoutCancel(t.Context())); err != nil {
		t.Fatal(err)
	}
	forgotten(t, host, stack, volume)
	backupsClean(t, prefix)
	file := "services:\n  app:\n    image: " + world.Image + "\n    command: [\"sh\", \"-c\", \"echo kept > /data/kept.txt; sleep 100000\"]\n    stop_grace_period: 1s\n    environment:\n      STACK_TOKEN: " + secret + "\n    volumes:\n      - data:/data\nvolumes:\n  data:\n"
	call(t, "stack_create", map[string]any{"env": host, "name": stack, "compose": file})
	call(t, "backup_destination_create", map[string]any{"name": destination, "repository": repositoryOn(world.Backups, prefix), "password": backupPassword})

	saved := call(t, "backup_create", map[string]any{"env": host, "of": stack, "destination": destination})
	backup := acctest.Object(t, saved["backup"], "backup")
	if backup["type"] != "stack" || backup["env"] != host || backup["schedule"] != nil || backup["keep"] != nil {
		t.Fatalf("a backup of a stack = %v", saved)
	}
	ran := call(t, "backup_run", map[string]any{"backup": stack, "wait": 240})
	if ran["status"] != "success" {
		t.Fatalf("the backup of the stack = %v", ran)
	}
	at := map[string]any{"backup": stack, "snapshot": acctest.Str(ran["snapshot"])}

	got := call(t, "backup_snapshot_get", at)
	if files := acctest.Rows(t, got["stack_files"], "stack_files"); got["type"] != "stack" || got["env"] != host || len(files) == 0 || len(acctest.Rows(t, got["volumes"], "volumes")) != 2 || strings.Contains(flat(got), secret) {
		t.Errorf("the snapshot of the stack = %v", got)
	}
	// the compose file is in the snapshot, and what it holds of a secret is hidden unless asked for
	read := call(t, "backup_snapshot_read", over(at, map[string]any{"path": "/volumes/__dockhand_stackdir__/compose.yaml"}))
	if text := acctest.Str(read["content"]); !strings.Contains(text, "STACK_TOKEN") || strings.Contains(text, secret) || acctest.NumOr0(read["hidden"]) != 1 {
		t.Errorf("the stack's compose file as the snapshot has it = %v", read)
	}
	if shown := call(t, "backup_snapshot_read", over(at, map[string]any{"path": "/volumes/__dockhand_stackdir__/compose.yaml", "reveal_secrets": true})); !strings.Contains(acctest.Str(shown["content"]), secret) {
		t.Errorf("the compose file with its secrets asked for = %v", shown)
	}
	dry := call(t, "backup_restore", over(at, map[string]any{"in_place": true, "dry_run": true}))
	if targets := acctest.Rows(t, dry["targets"], "targets"); len(targets) != 1 || targets[0]["volume"] != volume || targets[0]["there"] != "has-data" || acctest.Str(dry["stack_files"]) == "" {
		t.Errorf("where the stack's snapshot would be put back = %v", dry)
	}
}
