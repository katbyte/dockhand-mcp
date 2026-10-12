//go:build acceptance

package dockhand_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"mime/multipart"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/go-kt/mcp/acctest"
	"github.com/katbyte/pandorest/client"
)

// containerOf is the container a host lists under a name, or nil when it has none.
func containerOf(t *testing.T, env int, name string) *dockhand.GetContainersAnswer {
	t.Helper()

	listed, err := dock(t).GetContainers(t.Context(), dockhand.GetContainersOptions{Env: dockhand.EnvironmentID(env)})
	if err != nil {
		t.Fatalf("listing the containers of environment %d: %v", env, err)
	}
	for i := range listed.Answer {
		if listed.Answer[i].Name == name {
			return &listed.Answer[i]
		}
	}

	return nil
}

// gone removes containers from a host from outside Dockhand, now and when the test ends: what a run that was cut short may have left, and what this
// one makes.
func gone(t *testing.T, host string, names ...string) {
	t.Helper()

	ctx := noCancel(t)
	remove := func() { _, _ = world.Docker(ctx, host, append([]string{"rm", "-f"}, names...)...) }
	remove()
	t.Cleanup(remove)
}

// A running container is measured, has its processes and its shells listed, is looked into and written into, has a command run in it, is held still,
// renamed and given a limit, and is removed: on the host Dockhand reaches by its socket and on the one behind an agent.
func TestContainerMeasuredLookedIntoChangedAndRemoved(t *testing.T) {
	c := dock(t)
	for _, host := range []string{testenv.Alpha, testenv.Beta} {
		env := world.Env(host)
		name := started(t, host, host)
		renamed := name + "-renamed"
		gone(t, host, renamed)
		found := containerOf(t, int(env), name)
		if found == nil {
			t.Fatalf("%s: %s is not listed", host, name)
		}
		id := found.Id

		// what it is using: by itself, and among every running container of the host
		one, err := c.GetContainersByIdStats(t.Context(), id, dockhand.GetContainersByIdStatsOptions{Env: env})
		if err != nil || one.Answer.MemoryUsage <= 0 || one.Answer.MemoryLimit <= 0 || time.Since(one.Answer.Timestamp.Time) > time.Hour || one.Answer.Timestamp.IsZero() {
			t.Errorf("%s: what the container is using = %+v, %v", host, one.Answer, err)
		}
		all, err := c.GetContainersStats(t.Context(), dockhand.GetContainersStatsOptions{Env: env})
		if err != nil || !slices.ContainsFunc(all.Answer, func(s dockhand.GetContainersStatsAnswer) bool {
			return s.Id == id && s.Name == name && s.MemoryUsage > 0 && s.MemoryLimit > 0
		}) {
			t.Errorf("%s: what every running container is using = %+v, %v", host, all.Answer, err)
		}
		sizes, err := c.GetContainersSizes(t.Context(), dockhand.GetContainersSizesOptions{Env: env})
		if size, has := sizes.Answer[id]; err != nil || !has || size.SizeRootFs <= 0 {
			t.Errorf("%s: the sizes of the host's containers = %+v, %v", host, sizes.Answer[id], err)
		}

		// what runs in it, and the shells it has
		top, err := c.GetContainersByIdTop(t.Context(), id, dockhand.GetContainersByIdTopOptions{Env: env})
		if err != nil || !slices.Contains(top.Answer.Titles, "PID") || len(top.Answer.Processes) == 0 || !strings.Contains(strings.Join(top.Answer.Processes[0], " "), "sleep") {
			t.Errorf("%s: its processes = %+v, %v", host, top.Answer, err)
		}
		shells, err := c.GetContainersByIdShells(t.Context(), id, dockhand.GetContainersByIdShellsOptions{Env: env})
		if err != nil || !slices.Contains(shells.Answer.Shells, "/bin/sh") || shells.Answer.DefaultShell == "" || len(shells.Answer.AllShells) == 0 {
			t.Errorf("%s: its shells = %+v, %v", host, shells.Answer, err)
		}

		// a command run in it: what it printed apart from what it printed as errors, and what it exited with
		ran, err := c.PostContainersByIdExecRun(t.Context(), id, dockhand.PostContainersByIdExecRunRequest{Cmd: []string{"sh", "-c", "pwd; echo to-errors >&2; exit 5"}, WorkingDir: "/tmp", User: "nobody"}, dockhand.PostContainersByIdExecRunOptions{EnvId: env})
		if err != nil || ran.Answer.Stdout != "/tmp\n" || ran.Answer.Stderr != "to-errors\n" || ran.Answer.ExitCode != 5 {
			t.Errorf("%s: a command run = %+v, %v", host, ran.Answer, err)
		}
		// a command the container has not is an answer, with Docker's words where the output would be
		if absent, err := c.PostContainersByIdExecRun(t.Context(), id, dockhand.PostContainersByIdExecRunRequest{Cmd: []string{"nothing-by-this-name"}}, dockhand.PostContainersByIdExecRunOptions{EnvId: env}); err != nil || absent.Answer.ExitCode != 127 || !strings.Contains(absent.Answer.Stdout, "executable file not found") {
			t.Errorf("%s: a command the container has not = %+v, %v", host, absent.Answer, err)
		}

		// its files: a directory made, a file written into it, read back, renamed, given a mode and an owner, listed, and removed
		at := func(path string) dockhand.GetContainersByIdFilesContentOptions {
			return dockhand.GetContainersByIdFilesContentOptions{Env: env, Path: path}
		}
		if made, err := c.PostContainersByIdFilesCreate(t.Context(), id, dockhand.PostContainersByIdFilesCreateRequest{Path: "/tmp/it", Type: "directory"}, dockhand.PostContainersByIdFilesCreateOptions{Env: env}); err != nil || !made.Answer.Success {
			t.Fatalf("%s: making a directory = %+v, %v", host, made.Answer, err)
		}
		if wrote, err := c.PutContainersByIdFilesContent(t.Context(), id, dockhand.PutContainersByIdFilesContentRequest{Content: "written\n"}, dockhand.PutContainersByIdFilesContentOptions{Env: env, Path: "/tmp/it/note.txt"}); err != nil || !wrote.Answer.Success {
			t.Fatalf("%s: writing a file = %+v, %v", host, wrote.Answer, err)
		}
		if read, err := c.GetContainersByIdFilesContent(t.Context(), id, at("/tmp/it/note.txt")); err != nil || read.Answer.Content != "written\n" || read.Answer.Path != "/tmp/it/note.txt" {
			t.Errorf("%s: the file read back = %+v, %v", host, read.Answer, err)
		}
		if moved, err := c.PostContainersByIdFilesRename(t.Context(), id, dockhand.PostContainersByIdFilesRenameRequest{OldPath: "/tmp/it/note.txt", NewPath: "/tmp/it/kept.txt"}, dockhand.PostContainersByIdFilesRenameOptions{Env: env}); err != nil || !moved.Answer.Success {
			t.Errorf("%s: renaming the file = %+v, %v", host, moved.Answer, err)
		}
		if moded, err := c.PostContainersByIdFilesChmod(t.Context(), id, dockhand.PostContainersByIdFilesChmodRequest{Path: "/tmp/it/kept.txt", Mode: "600"}, dockhand.PostContainersByIdFilesChmodOptions{Env: env}); err != nil || !moded.Answer.Success {
			t.Errorf("%s: giving the file a mode = %+v, %v", host, moded.Answer, err)
		}
		if owned, err := c.PostContainersByIdFilesChown(t.Context(), id, dockhand.PostContainersByIdFilesChownRequest{Path: "/tmp/it/kept.txt", Owner: "nobody"}, dockhand.PostContainersByIdFilesChownOptions{Env: env}); err != nil || !owned.Answer.Success {
			t.Errorf("%s: giving the file an owner = %+v, %v", host, owned.Answer, err)
		}
		listed, err := c.GetContainersByIdFiles(t.Context(), id, dockhand.GetContainersByIdFilesOptions{Env: env, Path: "/tmp/it"})
		if err != nil || listed.Answer.Path != "/tmp/it" || len(listed.Answer.Entries) != 1 {
			t.Fatalf("%s: the directory listed = %+v, %v", host, listed.Answer, err)
		}
		if e := listed.Answer.Entries[0]; e.Name != "kept.txt" || e.Type != "file" || e.Size != 8 || e.Permissions != "rw-------" || e.Owner != "nobody" || e.Group == "" || e.Modified == "" {
			t.Errorf("%s: the file as listed = %+v", host, e)
		}
		// a link says where it points
		root, err := c.GetContainersByIdFiles(t.Context(), id, dockhand.GetContainersByIdFilesOptions{Env: env, Path: "/proc"})
		if err != nil || !slices.ContainsFunc(root.Answer.Entries, func(e dockhand.GetContainersByIdFilesAnswerEntries) bool {
			return e.Name == "self" && e.Type == "symlink" && e.LinkTarget != ""
		}) {
			t.Errorf("%s: /proc as listed has no link named self: %v", host, err)
		}
		if removed, err := c.DeleteContainersByIdFilesDelete(t.Context(), id, dockhand.DeleteContainersByIdFilesDeleteOptions{Env: env, Path: "/tmp/it"}); err != nil || !removed.Answer.Success {
			t.Errorf("%s: removing the directory = %+v, %v", host, removed.Answer, err)
		}
		// what is not there, what is no file and what is too large each answer in their own way
		if _, err := c.GetContainersByIdFilesContent(t.Context(), id, at("/tmp/it/kept.txt")); !client.IsNotFound(err) {
			t.Errorf("%s: reading a file that is gone = %v", host, err)
		}
		if _, err := c.GetContainersByIdFilesContent(t.Context(), id, at("/tmp")); client.StatusCode(err) != 400 {
			t.Errorf("%s: reading a directory = %v", host, err)
		}
		// a file over the megabyte that is read is written for it: the image's own files are under that on some machines
		if wrote, err := c.PostContainersByIdExecRun(t.Context(), id, dockhand.PostContainersByIdExecRunRequest{Cmd: []string{"sh", "-c", "head -c 1200000 /dev/zero > /tmp/too-large"}}, dockhand.PostContainersByIdExecRunOptions{EnvId: env}); err != nil || wrote.Answer.ExitCode != 0 {
			t.Fatalf("%s: writing a file over 1 MB = %+v, %v", host, wrote.Answer, err)
		}
		if _, err := c.GetContainersByIdFilesContent(t.Context(), id, at("/tmp/too-large")); client.StatusCode(err) != 413 {
			t.Errorf("%s: reading a file over 1 MB = %v", host, err)
		}
		if _, err := c.GetContainersByIdFiles(t.Context(), id, dockhand.GetContainersByIdFilesOptions{Env: env, Path: "/tmp/it"}); client.StatusCode(err) != 500 {
			t.Errorf("%s: listing a directory that is gone = %v", host, err)
		}

		// held still and let go on: each once, and not twice
		if held, err := c.PostContainersByIdPause(t.Context(), id, dockhand.PostContainersByIdPauseOptions{Env: env}); err != nil || !held.Answer.Success {
			t.Fatalf("%s: pausing = %+v, %v", host, held.Answer, err)
		}
		if is := containerOf(t, int(env), name); is == nil || is.State != "paused" {
			t.Errorf("%s: the container once paused = %+v", host, is)
		}
		if _, err := c.PostContainersByIdPause(t.Context(), id, dockhand.PostContainersByIdPauseOptions{Env: env}); client.StatusCode(err) != 500 {
			t.Errorf("%s: pausing what is paused = %v", host, err)
		}
		if let, err := c.PostContainersByIdUnpause(t.Context(), id, dockhand.PostContainersByIdUnpauseOptions{Env: env}); err != nil || !let.Answer.Success {
			t.Fatalf("%s: unpausing = %+v, %v", host, let.Answer, err)
		}
		if _, err := c.PostContainersByIdUnpause(t.Context(), id, dockhand.PostContainersByIdUnpauseOptions{Env: env}); client.StatusCode(err) != 500 {
			t.Errorf("%s: unpausing what is not paused = %v", host, err)
		}

		// renamed, and a name that is taken refused
		if named, err := c.PostContainersByIdRename(t.Context(), id, dockhand.PostContainersByIdRenameRequest{Name: renamed}, dockhand.PostContainersByIdRenameOptions{Env: env}); err != nil || !named.Answer.Success {
			t.Fatalf("%s: renaming = %+v, %v", host, named.Answer, err)
		}
		if containerOf(t, int(env), renamed) == nil || containerOf(t, int(env), name) != nil {
			t.Errorf("%s: the container is not listed under its new name alone", host)
		}
		other := started(t, host, host+"-other")
		if _, err := c.PostContainersByIdRename(t.Context(), id, dockhand.PostContainersByIdRenameRequest{Name: other}, dockhand.PostContainersByIdRenameOptions{Env: env}); client.StatusCode(err) != 500 {
			t.Errorf("%s: renaming to a name that is taken = %v", host, err)
		}

		// given a memory limit and a restart policy while it runs
		limited, err := c.PostContainersByIdUpdateRuntime(t.Context(), id, dockhand.PostContainersByIdUpdateRuntimeRequest{Memory: 67108864, MemorySwap: 67108864, RestartPolicy: map[string]any{"Name": "unless-stopped"}}, dockhand.PostContainersByIdUpdateRuntimeOptions{Env: env})
		if err != nil || !limited.Answer.Success {
			t.Errorf("%s: limiting it = %+v, %v", host, limited.Answer, err)
		}
		if set, err := world.Docker(noCancel(t), host, "inspect", "--format", "{{.HostConfig.Memory}} {{.HostConfig.RestartPolicy.Name}}", renamed); err != nil || strings.TrimSpace(set) != "67108864 unless-stopped" {
			t.Errorf("%s: the limit as the host has it = %q, %v", host, set, err)
		}

		// removed: not while it runs unless forced, and once gone, removing it again is said to have worked
		if _, err := c.DeleteContainersById(t.Context(), id, dockhand.DeleteContainersByIdOptions{Env: env}); client.StatusCode(err) != 500 {
			t.Errorf("%s: removing a running container unforced = %v", host, err)
		}
		if removed, err := c.DeleteContainersById(t.Context(), id, dockhand.DeleteContainersByIdOptions{Env: env, Force: new(true)}); err != nil || !removed.Answer.Success {
			t.Fatalf("%s: removing it forced = %+v, %v", host, removed.Answer, err)
		}
		if containerOf(t, int(env), renamed) != nil {
			t.Errorf("%s: the container is still listed once removed", host)
		}
		if again, err := c.DeleteContainersById(t.Context(), id, dockhand.DeleteContainersByIdOptions{Env: env, Force: new(true)}); err != nil || !again.Answer.Success {
			t.Errorf("%s: removing it twice = %+v, %v", host, again.Answer, err)
		}
	}
}

// A container is made through Dockhand, which does not start it; described as the compose service it would be; made again by an update, which keeps
// what it was given; checked for a newer image; and pruned with the others that are not running.
func TestContainerMadeUpdatedCheckedAndPruned(t *testing.T) {
	c := dock(t)
	const host = testenv.Beta
	env := world.Env(host)
	name := named(t, "c")
	gone(t, host, name)

	made, err := c.PostContainers(t.Context(), dockhand.PostContainersRequest{Name: name, Image: world.Image, Cmd: []string{"sleep", "100000"}, Env: []string{"MODE=demo"}, Labels: map[string]any{"made-by": "it"}, RestartPolicy: "unless-stopped"}, dockhand.PostContainersOptions{Env: env})
	if err != nil || !made.Answer.Success || len(made.Answer.Id) != 64 {
		t.Fatalf("making a container = %+v, %v", made.Answer, err)
	}
	// made, and not started: that is asked for apart
	if is := containerOf(t, int(env), name); is == nil || is.State != "created" || is.Id != made.Answer.Id || is.Labels["made-by"] != "it" {
		t.Fatalf("the container as made = %+v", is)
	}
	// a name that is taken, and an image there is none of, are refused with Docker's reason beside Dockhand's
	if _, err := c.PostContainers(t.Context(), dockhand.PostContainersRequest{Name: name, Image: world.Image}, dockhand.PostContainersOptions{Env: env}); client.StatusCode(err) != 500 || !strings.Contains(err.Error(), "already in use") {
		t.Errorf("making a container under a name that is taken = %v", err)
	}
	if started, err := c.PostContainersByIdStart(t.Context(), made.Answer.Id, dockhand.PostContainersByIdStartOptions{Env: env}); err != nil || !started.Answer.Success {
		t.Fatalf("starting it = %+v, %v", started.Answer, err)
	}

	// as the compose service it would be
	described, err := c.GetContainersByIdCompose(t.Context(), made.Answer.Id, dockhand.GetContainersByIdComposeOptions{Env: env})
	if err != nil || !strings.Contains(described.Answer.Compose, "image: "+world.Image) || !strings.Contains(described.Answer.Compose, "MODE") || !strings.Contains(described.Answer.Compose, "unless-stopped") {
		t.Errorf("the container as a compose service = %+v, %v", described.Answer, err)
	}

	// made again by an update, started when asked, with what it was given kept
	updated, err := c.PostContainersByIdUpdate(t.Context(), made.Answer.Id, dockhand.PostContainersByIdUpdateRequest{StartAfterUpdate: new(true)}, dockhand.PostContainersByIdUpdateOptions{Env: env})
	if err != nil || !updated.Answer.Success || updated.Answer.Id == "" || updated.Answer.Id == made.Answer.Id {
		t.Fatalf("updating it = %+v, %v", updated.Answer, err)
	}
	if is := containerOf(t, int(env), name); is == nil || is.Id != updated.Answer.Id || is.State != "running" || is.Labels["made-by"] != "it" {
		t.Errorf("the container once updated = %+v", is)
	}
	if given := givenTo(t, host, name); !slices.Contains(given, "MODE=demo") {
		t.Errorf("what the updated container was given = %v", given)
	}
	// the one it replaced is gone, so asking for it by its old id is a miss, and a batch says so of each it is given
	batch, err := c.PostContainersBatchUpdate(t.Context(), dockhand.PostContainersBatchUpdateRequest{ContainerIds: []string{made.Answer.Id}}, dockhand.PostContainersBatchUpdateOptions{Env: env})
	if err != nil || batch.Answer.Success || len(batch.Answer.Results) != 1 || batch.Answer.Results[0].Success || batch.Answer.Results[0].Error != "Container not found" || batch.Answer.Summary == nil || batch.Answer.Summary.Total != 1 || batch.Answer.Summary.Failed != 1 {
		t.Errorf("a batch update of a container that is gone = %+v, %v", batch.Answer, err)
	}

	// checked for a newer image, afresh, and what the check left read back
	checked, err := c.PostContainersCheckUpdates(t.Context(), dockhand.PostContainersCheckUpdatesOptions{Env: env})
	if err != nil || checked.Answer.Total == 0 || !slices.ContainsFunc(checked.Answer.Results, func(r dockhand.PostContainersCheckUpdatesAnswerResults) bool {
		return r.ContainerName == name && r.ContainerId == updated.Answer.Id && r.ImageName == world.Image
	}) {
		t.Errorf("a check for newer images = %+v, %v", checked.Answer, err)
	}
	if pending, err := c.GetContainersPendingUpdates(t.Context(), dockhand.GetContainersPendingUpdatesOptions{Env: env}); err != nil || pending.Answer.EnvironmentId != env {
		t.Errorf("the updates that are pending = %+v, %v", pending.Answer, err)
	}

	// stopped, it is one of what a prune takes
	if _, err := world.Docker(noCancel(t), host, "kill", name); err != nil {
		t.Fatal(err)
	}
	pruned, err := c.PostPruneContainers(t.Context(), dockhand.PostPruneContainersOptions{Env: env})
	if err != nil || !pruned.Answer.Success || pruned.Answer.Result == nil || !slices.Contains(pruned.Answer.Result.ContainersDeleted, updated.Answer.Id) {
		t.Errorf("a prune of what is not running = %+v, %v", pruned.Answer.Result, err)
	}
	if containerOf(t, int(env), name) != nil {
		t.Error("the container is still listed once pruned")
	}
}

// entries is the names in a gzipped archive, with what the first one holds.
func entries(t *testing.T, body io.ReadCloser) (names []string, first string) {
	t.Helper()
	defer func() { _ = body.Close() }()

	plain, err := gzip.NewReader(body)
	if err != nil {
		t.Fatalf("what was answered is not gzipped: %v", err)
	}
	archive := tar.NewReader(plain)
	for {
		entry, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return names, first
		}
		if err != nil {
			t.Fatalf("what was answered is no archive: %v", err)
		}
		if len(names) == 0 {
			held, _ := io.ReadAll(archive)
			first = string(held)
		}
		names = append(names, entry.Name)
	}
}

// Files are sent into a container as the parts of a form and fetched out as an archive, of a container that is running and of one that is stopped: on
// the host Dockhand reaches by its socket and on the one behind an agent, where a plain archive comes cut short and a gzipped one whole.
func TestContainerFilesSentAndFetched(t *testing.T) {
	c := dock(t)
	for _, host := range []string{testenv.Alpha, testenv.Beta} {
		env := world.Env(host)
		name := started(t, host, host)
		id := containerOf(t, int(env), name).Id
		at := func(path, format string) dockhand.GetContainersByIdFilesDownloadOptions {
			return dockhand.GetContainersByIdFilesDownloadOptions{Env: env, Path: path, Format: format}
		}
		form := func(owner string, files ...string) (io.Reader, string) {
			var body bytes.Buffer
			w := multipart.NewWriter(&body)
			for i := 0; i+1 < len(files); i += 2 {
				part, err := w.CreateFormFile("files", files[i])
				if err != nil {
					t.Fatal(err)
				}
				if _, err := part.Write([]byte(files[i+1])); err != nil {
					t.Fatal(err)
				}
			}
			if owner != "" {
				if err := w.WriteField("owner", owner); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}

			return &body, w.FormDataContentType()
		}
		if _, err := world.Docker(noCancel(t), host, "exec", name, "mkdir", "-p", "/srv/it"); err != nil {
			t.Fatal(err)
		}

		// two files sent, one of them not text, and given to a user of the container's
		body, kind := form("nobody", "one.txt", "one\n", "two.bin", "\x00\x01two")
		sent, err := c.PostContainersByIdFilesUpload(t.Context(), id, body, kind, dockhand.PostContainersByIdFilesUploadOptions{Env: env, Path: "/srv/it"})
		if err != nil || !sent.Answer.Success || !slices.Equal(sent.Answer.Uploaded, []string{"one.txt", "two.bin"}) || len(sent.Answer.Errors) != 0 {
			t.Fatalf("%s: sending two files = %+v, %v", host, sent.Answer, err)
		}
		if listed, err := world.Docker(noCancel(t), host, "exec", name, "sh", "-c", "cat /srv/it/one.txt; ls -l /srv/it/two.bin"); err != nil || !strings.HasPrefix(listed, "one\n") || !strings.Contains(listed, "nobody") {
			t.Errorf("%s: the files as the container has them = %q, %v", host, listed, err)
		}
		// into a directory that is not there, the lot fails with the reason for each
		body, kind = form("", "one.txt", "one\n")
		if _, err := c.PostContainersByIdFilesUpload(t.Context(), id, body, kind, dockhand.PostContainersByIdFilesUploadOptions{Env: env, Path: "/srv/absent"}); client.StatusCode(err) != 500 {
			t.Errorf("%s: sending into a directory that is not there = %v", host, err)
		}

		// fetched gzipped, a directory and a file come whole from either host
		whole, err := c.GetContainersByIdFilesDownload(t.Context(), id, at("/srv/it", "tar.gz"))
		if err != nil {
			t.Fatalf("%s: fetching a directory gzipped: %v", host, err)
		}
		if names, _ := entries(t, whole.HttpResponse.Body); !slices.Equal(names, []string{"it/", "it/one.txt", "it/two.bin"}) || !strings.HasPrefix(whole.HttpResponse.Header.Get("Content-Type"), "application/gzip") {
			t.Errorf("%s: a directory fetched gzipped holds %v, sent as %s", host, names, whole.HttpResponse.Header.Get("Content-Type"))
		}
		one, err := c.GetContainersByIdFilesDownload(t.Context(), id, at("/srv/it/two.bin", "tar.gz"))
		if err != nil {
			t.Fatalf("%s: fetching a file gzipped: %v", host, err)
		}
		if names, held := entries(t, one.HttpResponse.Body); !slices.Equal(names, []string{"two.bin"}) || held != "\x00\x01two" {
			t.Errorf("%s: a file fetched gzipped = %v holding %q", host, names, held)
		}

		// fetched plain, the archive is whole from the host Dockhand reaches itself, and from behind an agent it is cut to the length the agent sent
		// it compressed
		plain, err := c.GetContainersByIdFilesDownload(t.Context(), id, at("/srv/it/two.bin", ""))
		if err != nil {
			t.Fatalf("%s: fetching a file plain: %v", host, err)
		}
		raw, err := io.ReadAll(plain.HttpResponse.Body)
		_ = plain.HttpResponse.Body.Close()
		// a tar is whole blocks of 512 bytes: a header, the file, and two empty ones to end it
		if cut := len(raw)%512 != 0 || len(raw) < 2048; err != nil || cut != (host == testenv.Beta) {
			t.Errorf("%s: a file fetched plain is %d bytes, %v: want it whole from the socket host and cut short from behind the agent, which is why every archive is asked for gzipped", host, len(raw), err)
		}

		// what is not there answers as a failure of Dockhand's own, where its document says 404
		if _, err := c.GetContainersByIdFilesDownload(t.Context(), id, at("/srv/absent", "tar.gz")); client.StatusCode(err) != 500 {
			t.Errorf("%s: fetching what is not there = %v", host, err)
		}

		// stopped, a container still takes files and gives them up
		if _, err := world.Docker(noCancel(t), host, "kill", name); err != nil {
			t.Fatal(err)
		}
		body, kind = form("", "late.txt", "late\n")
		if late, err := c.PostContainersByIdFilesUpload(t.Context(), id, body, kind, dockhand.PostContainersByIdFilesUploadOptions{Env: env, Path: "/srv/it"}); err != nil || !slices.Equal(late.Answer.Uploaded, []string{"late.txt"}) {
			t.Errorf("%s: sending a file into a stopped container = %+v, %v", host, late.Answer, err)
		}
		still, err := c.GetContainersByIdFilesDownload(t.Context(), id, at("/srv/it/late.txt", "tar.gz"))
		if err != nil {
			t.Fatalf("%s: fetching from a stopped container: %v", host, err)
		}
		if _, held := entries(t, still.HttpResponse.Body); held != "late\n" {
			t.Errorf("%s: the file fetched from a stopped container holds %q", host, held)
		}
	}
}

// Dockhand is set to update a container by itself, the schedule is read alone and among the host's, changed and forgotten; what a check noted is
// cleared for one container and for all; and the release notes of versions are asked for, of an image that says nothing of where its source is.
func TestContainerScheduledNotedAndCleared(t *testing.T) {
	c := dock(t)
	for _, host := range []string{testenv.Alpha, testenv.Beta} {
		env := world.Env(host)
		name := started(t, host, host)
		id := containerOf(t, int(env), name).Id
		one := dockhand.GetAutoUpdateByContainerNameOptions{Env: env}
		t.Cleanup(func() {
			_, _ = c.DeleteAutoUpdateByContainerName(noCancel(t), name, dockhand.DeleteAutoUpdateByContainerNameOptions{Env: env})
		})

		// one on no schedule answers what a schedule would be, switched off; and so does a name no container has
		for _, asked := range []string{name, "it-no-such-container"} {
			if none, err := c.GetAutoUpdateByContainerName(t.Context(), asked, one); err != nil || none.Answer.Enabled || none.Answer.Id != 0 || none.Answer.CronExpression != "0 3 * * *" || none.Answer.ScheduleType != "daily" || none.Answer.VulnerabilityCriteria != "never" {
				t.Errorf("%s: the schedule of %s, which has none = %+v, %v", host, asked, none.Answer, err)
			}
		}

		// saved, it is the stored record, and Dockhand names the kind of schedule from the cron
		saved, err := c.PostAutoUpdateByContainerName(t.Context(), name, dockhand.PostAutoUpdateByContainerNameRequest{Enabled: new(true), CronExpression: "0 4 * * 0", VulnerabilityCriteria: "critical"}, dockhand.PostAutoUpdateByContainerNameOptions{Env: env})
		if err != nil || !saved.Answer.Enabled || saved.Answer.Id == 0 || saved.Answer.EnvironmentId != env || saved.Answer.ContainerName != name || saved.Answer.ScheduleType != "weekly" || saved.Answer.CronExpression != "0 4 * * 0" || saved.Answer.VulnerabilityCriteria != "critical" || saved.Answer.CreatedAt.IsZero() {
			t.Fatalf("%s: saving a schedule = %+v, %v", host, saved.Answer, err)
		}
		if read, err := c.GetAutoUpdateByContainerName(t.Context(), name, one); err != nil || read.Answer.Id != saved.Answer.Id || !read.Answer.Enabled || read.Answer.ContainerName != name {
			t.Errorf("%s: the schedule read back = %+v, %v", host, read.Answer, err)
		}
		all, err := c.GetAutoUpdate(t.Context(), dockhand.GetAutoUpdateOptions{Env: env})
		if kept := all.Answer[name]; err != nil || !kept.Enabled || kept.ScheduleType != "weekly" || kept.CronExpression != "0 4 * * 0" || kept.VulnerabilityCriteria != "critical" {
			t.Errorf("%s: the host's schedules = %+v, %v", host, all.Answer, err)
		}
		// it is saved whatever it says: a cron no scheduler reads, and a criteria there is none of
		if odd, err := c.PostAutoUpdateByContainerName(t.Context(), name, dockhand.PostAutoUpdateByContainerNameRequest{Enabled: new(true), CronExpression: "not a cron", VulnerabilityCriteria: "whatever"}, dockhand.PostAutoUpdateByContainerNameOptions{Env: env}); err != nil || odd.Answer.CronExpression != "not a cron" || odd.Answer.VulnerabilityCriteria != "whatever" || odd.Answer.ScheduleType != "custom" {
			t.Errorf("%s: saving a schedule that says nothing a scheduler reads = %+v, %v", host, odd.Answer, err)
		}

		// switched off it is deleted, which is answered in place of the record; and deleting what is gone is said to have worked
		off, err := c.PostAutoUpdateByContainerName(t.Context(), name, dockhand.PostAutoUpdateByContainerNameRequest{Enabled: new(false)}, dockhand.PostAutoUpdateByContainerNameOptions{Env: env})
		if err != nil || !off.Answer.Success || !off.Answer.Deleted || off.Answer.Id != 0 {
			t.Errorf("%s: switching the schedule off = %+v, %v", host, off.Answer, err)
		}
		if after, err := c.GetAutoUpdate(t.Context(), dockhand.GetAutoUpdateOptions{Env: env}); err != nil || after.Answer[name].ScheduleType != "" {
			t.Errorf("%s: the host's schedules once it is off = %+v, %v", host, after.Answer, err)
		}
		if gone, err := c.DeleteAutoUpdateByContainerName(t.Context(), name, dockhand.DeleteAutoUpdateByContainerNameOptions{Env: env}); err != nil || !gone.Answer.Success {
			t.Errorf("%s: deleting a schedule that is gone = %+v, %v", host, gone.Answer, err)
		}

		// what a check noted is read two ways and cleared two ways, and clearing nothing is said to have worked
		if noted, err := c.GetContainersCheckUpdates(t.Context(), dockhand.GetContainersCheckUpdatesOptions{Env: env}); err != nil || noted.Answer.EnvironmentId != env {
			t.Errorf("%s: what the last check noted = %+v, %v", host, noted.Answer, err)
		}
		if cleared, err := c.DeleteContainersPendingUpdates(t.Context(), dockhand.DeleteContainersPendingUpdatesOptions{Env: env, ContainerId: id}); err != nil || !cleared.Answer.Success {
			t.Errorf("%s: clearing one container's note = %+v, %v", host, cleared.Answer, err)
		}
		if cleared, err := c.DeleteContainersPendingUpdates(t.Context(), dockhand.DeleteContainersPendingUpdatesOptions{Env: env}); err != nil || !cleared.Answer.Success {
			t.Errorf("%s: clearing every note = %+v, %v", host, cleared.Answer, err)
		}
		if left, err := c.GetContainersPendingUpdates(t.Context(), dockhand.GetContainersPendingUpdatesOptions{Env: env}); err != nil || len(left.Answer.PendingUpdates) != 0 {
			t.Errorf("%s: what is noted once cleared = %+v, %v", host, left.Answer, err)
		}

		// release notes: an image that does not say where its source is has none, and that is an answer
		notes, err := c.GetContainersByIdVersionNotes(t.Context(), id, dockhand.GetContainersByIdVersionNotesOptions{Env: env, Versions: "1.36,1.37"})
		if err != nil || len(notes.Answer.Notes) != 0 || notes.Answer.RateLimited || notes.Answer.Source != "" {
			t.Errorf("%s: the release notes of an image with no source named = %+v, %v", host, notes.Answer, err)
		}
	}
}

// What a container did is kept as it happens, asked for by name, by action and by time, counted, and cleared, which clears every host's at once.
func TestActivityKeptCountedAndCleared(t *testing.T) {
	c := dock(t)
	const host = testenv.Beta
	env := world.Env(host)
	name := started(t, host, host)
	for _, did := range [][]string{{"kill", name}, {"rm", name}} {
		if _, err := world.Docker(noCancel(t), host, did...); err != nil {
			t.Fatal(err)
		}
	}

	// kept a moment after it happens: the death with its exit code and the kill with its signal, each as text among what Docker said
	var kept *dockhand.GetActivityAnswer
	if !acctest.Eventually(func() bool {
		answer, err := c.GetActivity(t.Context(), dockhand.GetActivityOptions{EnvironmentId: env, ContainerName: name})
		kept = answer.Answer

		return err == nil && slices.ContainsFunc(kept.Events, func(e dockhand.GetActivityAnswerEvents) bool { return e.Action == "destroy" })
	}) {
		t.Fatalf("what %s did was not kept: %+v", name, kept)
	}
	if i := slices.IndexFunc(kept.Events, func(e dockhand.GetActivityAnswerEvents) bool { return e.Action == "die" }); i < 0 || kept.Events[i].ActorAttributes["exitCode"] != "137" || kept.Events[i].EnvironmentId != env || kept.Events[i].EnvironmentName != host || kept.Events[i].ContainerName != name || len(kept.Events[i].ContainerId) != 64 || kept.Events[i].Image != world.Image || kept.Events[i].Timestamp.IsZero() {
		t.Errorf("its death as kept = %+v", kept.Events)
	}
	if i := slices.IndexFunc(kept.Events, func(e dockhand.GetActivityAnswerEvents) bool { return e.Action == "kill" }); i < 0 || kept.Events[i].ActorAttributes["signal"] != "9" {
		t.Errorf("its kill as kept = %+v", kept.Events)
	}
	// the latest first, and total says how many matched whatever the limit
	if latest, err := c.GetActivity(t.Context(), dockhand.GetActivityOptions{EnvironmentId: env, ContainerName: name, Limit: new(1)}); err != nil || len(latest.Answer.Events) != 1 || latest.Answer.Events[0].Action != "destroy" || latest.Answer.Total != kept.Total || latest.Answer.Limit != 1 {
		t.Errorf("the latest of what it did = %+v, %v", latest.Answer, err)
	}
	// by action, a part of a name, and time: a time that cannot be read matches nothing and is no error
	if deaths, err := c.GetActivity(t.Context(), dockhand.GetActivityOptions{EnvironmentId: env, ContainerName: name[3:], Actions: "die,oom"}); err != nil || deaths.Answer.Total != 1 || deaths.Answer.Events[0].Action != "die" {
		t.Errorf("its deaths, asked for by a part of its name = %+v, %v", deaths.Answer, err)
	}
	if early, err := c.GetActivity(t.Context(), dockhand.GetActivityOptions{EnvironmentId: env, ContainerName: name, ToDate: "2020-01-01T00:00:00Z"}); err != nil || early.Answer.Total != 0 {
		t.Errorf("what it did before 2020 = %+v, %v", early.Answer, err)
	}
	if odd, err := c.GetActivity(t.Context(), dockhand.GetActivityOptions{EnvironmentId: env, ContainerName: name, FromDate: "yesterday"}); err != nil || odd.Answer.Total != 0 {
		t.Errorf("what it did since a time that cannot be read = %+v, %v", odd.Answer, err)
	}

	// counted by action, for one host and for all
	here, err := c.GetActivityStats(t.Context(), dockhand.GetActivityStatsOptions{EnvironmentId: env})
	if err != nil || here.Answer.Total < kept.Total || here.Answer.ByAction["die"] < 1 || here.Answer.ByAction["kill"] < 1 {
		t.Errorf("the host's activity counted = %+v, %v", here.Answer, err)
	}
	if all, err := c.GetActivityStats(t.Context(), dockhand.GetActivityStatsOptions{}); err != nil || all.Answer.Total < here.Answer.Total {
		t.Errorf("every host's activity counted = %+v, %v", all.Answer, err)
	}

	// cleared, for every host at once, and what it did is not kept any more
	if cleared, err := c.DeleteActivity(t.Context()); err != nil || !cleared.Answer.Success {
		t.Fatalf("clearing = %+v, %v", cleared.Answer, err)
	}
	if after, err := c.GetActivity(t.Context(), dockhand.GetActivityOptions{ContainerName: name}); err != nil || after.Answer.Total != 0 {
		t.Errorf("what it did, once cleared = %+v, %v", after.Answer, err)
	}
}
