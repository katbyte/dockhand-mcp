//go:build integration

package integration

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

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/go-kt/mcp/acctest"
	"github.com/katbyte/pandorest/client"
)

// containerOf is the container a host lists under a name, or nil when it has none.
func containerOf(t *testing.T, env int, name string) *dockhand.GetContainersResponse {
	t.Helper()

	listed, err := dock(t).GetContainers(t.Context(), dockhand.GetContainersOperationOptions{Env: &env})
	if err != nil {
		t.Fatalf("listing the containers of environment %d: %v", env, err)
	}
	for i := range listed.Model {
		if listed.Model[i].Name == name {
			return &listed.Model[i]
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
		found := containerOf(t, env, name)
		if found == nil {
			t.Fatalf("%s: %s is not listed", host, name)
		}
		id := found.Id

		// what it is using: by itself, and among every running container of the host
		one, err := c.GetContainersByIdStats(t.Context(), id, dockhand.GetContainersByIdStatsOperationOptions{Env: &env})
		if err != nil || one.Model.MemoryUsage <= 0 || one.Model.MemoryLimit <= 0 || one.Model.Timestamp <= 0 {
			t.Errorf("%s: what the container is using = %+v, %v", host, one.Model, err)
		}
		all, err := c.GetContainersStats(t.Context(), dockhand.GetContainersStatsOperationOptions{Env: &env})
		if err != nil || !slices.ContainsFunc(all.Model, func(s dockhand.GetContainersStatsResponse) bool {
			return s.Id == id && s.Name == name && s.MemoryUsage > 0 && s.MemoryLimit > 0
		}) {
			t.Errorf("%s: what every running container is using = %+v, %v", host, all.Model, err)
		}
		sizes, err := c.GetContainersSizes(t.Context(), dockhand.GetContainersSizesOperationOptions{Env: &env})
		if size, has := sizes.Model[id]; err != nil || !has || size.SizeRootFs <= 0 {
			t.Errorf("%s: the sizes of the host's containers = %+v, %v", host, sizes.Model[id], err)
		}

		// what runs in it, and the shells it has
		top, err := c.GetContainersByIdTop(t.Context(), id, dockhand.GetContainersByIdTopOperationOptions{Env: &env})
		if err != nil || !slices.Contains(top.Model.Titles, "PID") || len(top.Model.Processes) == 0 || !strings.Contains(strings.Join(top.Model.Processes[0], " "), "sleep") {
			t.Errorf("%s: its processes = %+v, %v", host, top.Model, err)
		}
		shells, err := c.GetContainersByIdShells(t.Context(), id, dockhand.GetContainersByIdShellsOperationOptions{Env: &env})
		if err != nil || !slices.Contains(shells.Model.Shells, "/bin/sh") || shells.Model.DefaultShell == "" || len(shells.Model.AllShells) == 0 {
			t.Errorf("%s: its shells = %+v, %v", host, shells.Model, err)
		}

		// a command run in it: what it printed apart from what it printed as errors, and what it exited with
		ran, err := c.PostContainersByIdExecRun(t.Context(), id, dockhand.PostContainersByIdExecRunRequest{Cmd: []string{"sh", "-c", "pwd; echo to-errors >&2; exit 5"}, WorkingDir: "/tmp", User: "nobody"}, dockhand.PostContainersByIdExecRunOperationOptions{EnvId: &env})
		if err != nil || ran.Model.Stdout != "/tmp\n" || ran.Model.Stderr != "to-errors\n" || ran.Model.ExitCode != 5 {
			t.Errorf("%s: a command run = %+v, %v", host, ran.Model, err)
		}
		// a command the container has not is an answer, with Docker's words where the output would be
		if absent, err := c.PostContainersByIdExecRun(t.Context(), id, dockhand.PostContainersByIdExecRunRequest{Cmd: []string{"nothing-by-this-name"}}, dockhand.PostContainersByIdExecRunOperationOptions{EnvId: &env}); err != nil || absent.Model.ExitCode != 127 || !strings.Contains(absent.Model.Stdout, "executable file not found") {
			t.Errorf("%s: a command the container has not = %+v, %v", host, absent.Model, err)
		}

		// its files: a directory made, a file written into it, read back, renamed, given a mode and an owner, listed, and removed
		at := func(path string) dockhand.GetContainersByIdFilesContentOperationOptions {
			return dockhand.GetContainersByIdFilesContentOperationOptions{Env: &env, Path: path}
		}
		if made, err := c.PostContainersByIdFilesCreate(t.Context(), id, dockhand.PostContainersByIdFilesCreateRequest{Path: "/tmp/it", Type: "directory"}, dockhand.PostContainersByIdFilesCreateOperationOptions{Env: &env}); err != nil || !yes(made.Model.Success) {
			t.Fatalf("%s: making a directory = %+v, %v", host, made.Model, err)
		}
		if wrote, err := c.PutContainersByIdFilesContent(t.Context(), id, dockhand.PutContainersByIdFilesContentRequest{Content: "written\n"}, dockhand.PutContainersByIdFilesContentOperationOptions{Env: &env, Path: "/tmp/it/note.txt"}); err != nil || !yes(wrote.Model.Success) {
			t.Fatalf("%s: writing a file = %+v, %v", host, wrote.Model, err)
		}
		if read, err := c.GetContainersByIdFilesContent(t.Context(), id, at("/tmp/it/note.txt")); err != nil || read.Model.Content != "written\n" || read.Model.Path != "/tmp/it/note.txt" {
			t.Errorf("%s: the file read back = %+v, %v", host, read.Model, err)
		}
		if moved, err := c.PostContainersByIdFilesRename(t.Context(), id, dockhand.PostContainersByIdFilesRenameRequest{OldPath: "/tmp/it/note.txt", NewPath: "/tmp/it/kept.txt"}, dockhand.PostContainersByIdFilesRenameOperationOptions{Env: &env}); err != nil || !yes(moved.Model.Success) {
			t.Errorf("%s: renaming the file = %+v, %v", host, moved.Model, err)
		}
		if moded, err := c.PostContainersByIdFilesChmod(t.Context(), id, dockhand.PostContainersByIdFilesChmodRequest{Path: "/tmp/it/kept.txt", Mode: "600"}, dockhand.PostContainersByIdFilesChmodOperationOptions{Env: &env}); err != nil || !yes(moded.Model.Success) {
			t.Errorf("%s: giving the file a mode = %+v, %v", host, moded.Model, err)
		}
		if owned, err := c.PostContainersByIdFilesChown(t.Context(), id, dockhand.PostContainersByIdFilesChownRequest{Path: "/tmp/it/kept.txt", Owner: "nobody"}, dockhand.PostContainersByIdFilesChownOperationOptions{Env: &env}); err != nil || !yes(owned.Model.Success) {
			t.Errorf("%s: giving the file an owner = %+v, %v", host, owned.Model, err)
		}
		listed, err := c.GetContainersByIdFiles(t.Context(), id, dockhand.GetContainersByIdFilesOperationOptions{Env: &env, Path: "/tmp/it"})
		if err != nil || listed.Model.Path != "/tmp/it" || len(listed.Model.Entries) != 1 {
			t.Fatalf("%s: the directory listed = %+v, %v", host, listed.Model, err)
		}
		if e := listed.Model.Entries[0]; e.Name != "kept.txt" || e.Type != "file" || e.Size != 8 || e.Permissions != "rw-------" || e.Owner != "nobody" || e.Group == "" || e.Modified == "" {
			t.Errorf("%s: the file as listed = %+v", host, e)
		}
		// a link says where it points
		root, err := c.GetContainersByIdFiles(t.Context(), id, dockhand.GetContainersByIdFilesOperationOptions{Env: &env, Path: "/proc"})
		if err != nil || !slices.ContainsFunc(root.Model.Entries, func(e dockhand.GetContainersByIdFilesResponseEntries) bool {
			return e.Name == "self" && e.Type == "symlink" && e.LinkTarget != ""
		}) {
			t.Errorf("%s: /proc as listed has no link named self: %v", host, err)
		}
		if removed, err := c.DeleteContainersByIdFilesDelete(t.Context(), id, dockhand.DeleteContainersByIdFilesDeleteOperationOptions{Env: &env, Path: "/tmp/it"}); err != nil || !yes(removed.Model.Success) {
			t.Errorf("%s: removing the directory = %+v, %v", host, removed.Model, err)
		}
		// what is not there, what is no file and what is too large each answer in their own way
		if _, err := c.GetContainersByIdFilesContent(t.Context(), id, at("/tmp/it/kept.txt")); !client.IsNotFound(err) {
			t.Errorf("%s: reading a file that is gone = %v", host, err)
		}
		if _, err := c.GetContainersByIdFilesContent(t.Context(), id, at("/tmp")); client.StatusCode(err) != 400 {
			t.Errorf("%s: reading a directory = %v", host, err)
		}
		if _, err := c.GetContainersByIdFilesContent(t.Context(), id, at("/bin/busybox")); client.StatusCode(err) != 413 {
			t.Errorf("%s: reading a file over 1 MB = %v", host, err)
		}
		if _, err := c.GetContainersByIdFiles(t.Context(), id, dockhand.GetContainersByIdFilesOperationOptions{Env: &env, Path: "/tmp/it"}); client.StatusCode(err) != 500 {
			t.Errorf("%s: listing a directory that is gone = %v", host, err)
		}

		// held still and let go on: each once, and not twice
		if held, err := c.PostContainersByIdPause(t.Context(), id, dockhand.PostContainersByIdPauseOperationOptions{Env: &env}); err != nil || !yes(held.Model.Success) {
			t.Fatalf("%s: pausing = %+v, %v", host, held.Model, err)
		}
		if is := containerOf(t, env, name); is == nil || is.State != "paused" {
			t.Errorf("%s: the container once paused = %+v", host, is)
		}
		if _, err := c.PostContainersByIdPause(t.Context(), id, dockhand.PostContainersByIdPauseOperationOptions{Env: &env}); client.StatusCode(err) != 500 {
			t.Errorf("%s: pausing what is paused = %v", host, err)
		}
		if let, err := c.PostContainersByIdUnpause(t.Context(), id, dockhand.PostContainersByIdUnpauseOperationOptions{Env: &env}); err != nil || !yes(let.Model.Success) {
			t.Fatalf("%s: unpausing = %+v, %v", host, let.Model, err)
		}
		if _, err := c.PostContainersByIdUnpause(t.Context(), id, dockhand.PostContainersByIdUnpauseOperationOptions{Env: &env}); client.StatusCode(err) != 500 {
			t.Errorf("%s: unpausing what is not paused = %v", host, err)
		}

		// renamed, and a name that is taken refused
		if named, err := c.PostContainersByIdRename(t.Context(), id, dockhand.PostContainersByIdRenameRequest{Name: renamed}, dockhand.PostContainersByIdRenameOperationOptions{Env: &env}); err != nil || !yes(named.Model.Success) {
			t.Fatalf("%s: renaming = %+v, %v", host, named.Model, err)
		}
		if containerOf(t, env, renamed) == nil || containerOf(t, env, name) != nil {
			t.Errorf("%s: the container is not listed under its new name alone", host)
		}
		other := started(t, host, host+"-other")
		if _, err := c.PostContainersByIdRename(t.Context(), id, dockhand.PostContainersByIdRenameRequest{Name: other}, dockhand.PostContainersByIdRenameOperationOptions{Env: &env}); client.StatusCode(err) != 500 {
			t.Errorf("%s: renaming to a name that is taken = %v", host, err)
		}

		// given a memory limit and a restart policy while it runs
		limited, err := c.PostContainersByIdUpdateRuntime(t.Context(), id, dockhand.PostContainersByIdUpdateRuntimeRequest{Memory: 67108864, MemorySwap: 67108864, RestartPolicy: map[string]any{"Name": "unless-stopped"}}, dockhand.PostContainersByIdUpdateRuntimeOperationOptions{Env: &env})
		if err != nil || !yes(limited.Model.Success) {
			t.Errorf("%s: limiting it = %+v, %v", host, limited.Model, err)
		}
		if set, err := world.Docker(noCancel(t), host, "inspect", "--format", "{{.HostConfig.Memory}} {{.HostConfig.RestartPolicy.Name}}", renamed); err != nil || strings.TrimSpace(set) != "67108864 unless-stopped" {
			t.Errorf("%s: the limit as the host has it = %q, %v", host, set, err)
		}

		// removed: not while it runs unless forced, and once gone, removing it again is said to have worked
		if _, err := c.DeleteContainersById(t.Context(), id, dockhand.DeleteContainersByIdOperationOptions{Env: &env}); client.StatusCode(err) != 500 {
			t.Errorf("%s: removing a running container unforced = %v", host, err)
		}
		if removed, err := c.DeleteContainersById(t.Context(), id, dockhand.DeleteContainersByIdOperationOptions{Env: &env, Force: new(true)}); err != nil || !yes(removed.Model.Success) {
			t.Fatalf("%s: removing it forced = %+v, %v", host, removed.Model, err)
		}
		if containerOf(t, env, renamed) != nil {
			t.Errorf("%s: the container is still listed once removed", host)
		}
		if again, err := c.DeleteContainersById(t.Context(), id, dockhand.DeleteContainersByIdOperationOptions{Env: &env, Force: new(true)}); err != nil || !yes(again.Model.Success) {
			t.Errorf("%s: removing it twice = %+v, %v", host, again.Model, err)
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

	made, err := c.PostContainers(t.Context(), dockhand.PostContainersRequest{Name: name, Image: world.Image, Cmd: []string{"sleep", "100000"}, Env: []string{"MODE=demo"}, Labels: map[string]any{"made-by": "it"}, RestartPolicy: "unless-stopped"}, dockhand.PostContainersOperationOptions{Env: &env})
	if err != nil || !yes(made.Model.Success) || len(made.Model.Id) != 64 {
		t.Fatalf("making a container = %+v, %v", made.Model, err)
	}
	// made, and not started: that is asked for apart
	if is := containerOf(t, env, name); is == nil || is.State != "created" || is.Id != made.Model.Id || is.Labels["made-by"] != "it" {
		t.Fatalf("the container as made = %+v", is)
	}
	// a name that is taken, and an image there is none of, are refused with Docker's reason beside Dockhand's
	if _, err := c.PostContainers(t.Context(), dockhand.PostContainersRequest{Name: name, Image: world.Image}, dockhand.PostContainersOperationOptions{Env: &env}); client.StatusCode(err) != 500 || !strings.Contains(err.Error(), "already in use") {
		t.Errorf("making a container under a name that is taken = %v", err)
	}
	if started, err := c.PostContainersByIdStart(t.Context(), made.Model.Id, dockhand.PostContainersByIdStartOperationOptions{Env: &env}); err != nil || !yes(started.Model.Success) {
		t.Fatalf("starting it = %+v, %v", started.Model, err)
	}

	// as the compose service it would be
	described, err := c.GetContainersByIdCompose(t.Context(), made.Model.Id, dockhand.GetContainersByIdComposeOperationOptions{Env: &env})
	if err != nil || !strings.Contains(described.Model.Compose, "image: "+world.Image) || !strings.Contains(described.Model.Compose, "MODE") || !strings.Contains(described.Model.Compose, "unless-stopped") {
		t.Errorf("the container as a compose service = %+v, %v", described.Model, err)
	}

	// made again by an update, started when asked, with what it was given kept
	updated, err := c.PostContainersByIdUpdate(t.Context(), made.Model.Id, dockhand.PostContainersByIdUpdateRequest{StartAfterUpdate: new(true)}, dockhand.PostContainersByIdUpdateOperationOptions{Env: &env})
	if err != nil || !yes(updated.Model.Success) || updated.Model.Id == "" || updated.Model.Id == made.Model.Id {
		t.Fatalf("updating it = %+v, %v", updated.Model, err)
	}
	if is := containerOf(t, env, name); is == nil || is.Id != updated.Model.Id || is.State != "running" || is.Labels["made-by"] != "it" {
		t.Errorf("the container once updated = %+v", is)
	}
	if given := givenTo(t, host, name); !slices.Contains(given, "MODE=demo") {
		t.Errorf("what the updated container was given = %v", given)
	}
	// the one it replaced is gone, so asking for it by its old id is a miss, and a batch says so of each it is given
	batch, err := c.PostContainersBatchUpdate(t.Context(), dockhand.PostContainersBatchUpdateRequest{ContainerIds: []string{made.Model.Id}}, dockhand.PostContainersBatchUpdateOperationOptions{Env: &env})
	if err != nil || yes(batch.Model.Success) || len(batch.Model.Results) != 1 || yes(batch.Model.Results[0].Success) || batch.Model.Results[0].Error != "Container not found" || batch.Model.Summary == nil || batch.Model.Summary.Total != 1 || batch.Model.Summary.Failed != 1 {
		t.Errorf("a batch update of a container that is gone = %+v, %v", batch.Model, err)
	}

	// checked for a newer image, afresh, and what the check left read back
	checked, err := c.PostContainersCheckUpdates(t.Context(), dockhand.PostContainersCheckUpdatesOperationOptions{Env: &env})
	if err != nil || checked.Model.Total == 0 || !slices.ContainsFunc(checked.Model.Results, func(r dockhand.PostContainersCheckUpdatesResponseResults) bool {
		return r.ContainerName == name && r.ContainerId == updated.Model.Id && r.ImageName == world.Image
	}) {
		t.Errorf("a check for newer images = %+v, %v", checked.Model, err)
	}
	if pending, err := c.GetContainersPendingUpdates(t.Context(), dockhand.GetContainersPendingUpdatesOperationOptions{Env: &env}); err != nil || pending.Model.EnvironmentId != env {
		t.Errorf("the updates that are pending = %+v, %v", pending.Model, err)
	}

	// stopped, it is one of what a prune takes
	if _, err := world.Docker(noCancel(t), host, "kill", name); err != nil {
		t.Fatal(err)
	}
	pruned, err := c.PostPruneContainers(t.Context(), dockhand.PostPruneContainersOperationOptions{Env: &env})
	if err != nil || !yes(pruned.Model.Success) || pruned.Model.Result == nil || !slices.Contains(pruned.Model.Result.ContainersDeleted, updated.Model.Id) {
		t.Errorf("a prune of what is not running = %+v, %v", pruned.Model.Result, err)
	}
	if containerOf(t, env, name) != nil {
		t.Errorf("the container is still listed once pruned")
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
		id := containerOf(t, env, name).Id
		at := func(path, format string) dockhand.GetContainersByIdFilesDownloadOperationOptions {
			return dockhand.GetContainersByIdFilesDownloadOperationOptions{Env: &env, Path: path, Format: format}
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
		sent, err := c.PostContainersByIdFilesUpload(t.Context(), id, body, kind, dockhand.PostContainersByIdFilesUploadOperationOptions{Env: &env, Path: "/srv/it"})
		if err != nil || !yes(sent.Model.Success) || !slices.Equal(sent.Model.Uploaded, []string{"one.txt", "two.bin"}) || len(sent.Model.Errors) != 0 {
			t.Fatalf("%s: sending two files = %+v, %v", host, sent.Model, err)
		}
		if listed, err := world.Docker(noCancel(t), host, "exec", name, "sh", "-c", "cat /srv/it/one.txt; ls -l /srv/it/two.bin"); err != nil || !strings.HasPrefix(listed, "one\n") || !strings.Contains(listed, "nobody") {
			t.Errorf("%s: the files as the container has them = %q, %v", host, listed, err)
		}
		// into a directory that is not there, the lot fails with the reason for each
		body, kind = form("", "one.txt", "one\n")
		if _, err := c.PostContainersByIdFilesUpload(t.Context(), id, body, kind, dockhand.PostContainersByIdFilesUploadOperationOptions{Env: &env, Path: "/srv/absent"}); client.StatusCode(err) != 500 {
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
		if late, err := c.PostContainersByIdFilesUpload(t.Context(), id, body, kind, dockhand.PostContainersByIdFilesUploadOperationOptions{Env: &env, Path: "/srv/it"}); err != nil || !slices.Equal(late.Model.Uploaded, []string{"late.txt"}) {
			t.Errorf("%s: sending a file into a stopped container = %+v, %v", host, late.Model, err)
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
		id := containerOf(t, env, name).Id
		one := dockhand.GetAutoUpdateByContainerNameOperationOptions{Env: &env}
		t.Cleanup(func() {
			_, _ = c.DeleteAutoUpdateByContainerName(noCancel(t), name, dockhand.DeleteAutoUpdateByContainerNameOperationOptions{Env: &env})
		})

		// one on no schedule answers what a schedule would be, switched off; and so does a name no container has
		for _, asked := range []string{name, "it-no-such-container"} {
			if none, err := c.GetAutoUpdateByContainerName(t.Context(), asked, one); err != nil || yes(none.Model.Enabled) || none.Model.Id != 0 || none.Model.CronExpression != "0 3 * * *" || none.Model.ScheduleType != "daily" || none.Model.VulnerabilityCriteria != "never" {
				t.Errorf("%s: the schedule of %s, which has none = %+v, %v", host, asked, none.Model, err)
			}
		}

		// saved, it is the stored record, and Dockhand names the kind of schedule from the cron
		saved, err := c.PostAutoUpdateByContainerName(t.Context(), name, dockhand.PostAutoUpdateByContainerNameRequest{Enabled: new(true), CronExpression: "0 4 * * 0", VulnerabilityCriteria: "critical"}, dockhand.PostAutoUpdateByContainerNameOperationOptions{Env: &env})
		if err != nil || !yes(saved.Model.Enabled) || saved.Model.Id == 0 || saved.Model.EnvironmentId != env || saved.Model.ContainerName != name || saved.Model.ScheduleType != "weekly" || saved.Model.CronExpression != "0 4 * * 0" || saved.Model.VulnerabilityCriteria != "critical" || saved.Model.CreatedAt == "" {
			t.Fatalf("%s: saving a schedule = %+v, %v", host, saved.Model, err)
		}
		if read, err := c.GetAutoUpdateByContainerName(t.Context(), name, one); err != nil || read.Model.Id != saved.Model.Id || !yes(read.Model.Enabled) || read.Model.ContainerName != name {
			t.Errorf("%s: the schedule read back = %+v, %v", host, read.Model, err)
		}
		all, err := c.GetAutoUpdate(t.Context(), dockhand.GetAutoUpdateOperationOptions{Env: &env})
		if kept := all.Model[name]; err != nil || !yes(kept.Enabled) || kept.ScheduleType != "weekly" || kept.CronExpression != "0 4 * * 0" || kept.VulnerabilityCriteria != "critical" {
			t.Errorf("%s: the host's schedules = %+v, %v", host, all.Model, err)
		}
		// it is saved whatever it says: a cron no scheduler reads, and a criteria there is none of
		if odd, err := c.PostAutoUpdateByContainerName(t.Context(), name, dockhand.PostAutoUpdateByContainerNameRequest{Enabled: new(true), CronExpression: "not a cron", VulnerabilityCriteria: "whatever"}, dockhand.PostAutoUpdateByContainerNameOperationOptions{Env: &env}); err != nil || odd.Model.CronExpression != "not a cron" || odd.Model.VulnerabilityCriteria != "whatever" || odd.Model.ScheduleType != "custom" {
			t.Errorf("%s: saving a schedule that says nothing a scheduler reads = %+v, %v", host, odd.Model, err)
		}

		// switched off it is deleted, which is answered in place of the record; and deleting what is gone is said to have worked
		off, err := c.PostAutoUpdateByContainerName(t.Context(), name, dockhand.PostAutoUpdateByContainerNameRequest{Enabled: new(false)}, dockhand.PostAutoUpdateByContainerNameOperationOptions{Env: &env})
		if err != nil || !yes(off.Model.Success) || !yes(off.Model.Deleted) || off.Model.Id != 0 {
			t.Errorf("%s: switching the schedule off = %+v, %v", host, off.Model, err)
		}
		if after, err := c.GetAutoUpdate(t.Context(), dockhand.GetAutoUpdateOperationOptions{Env: &env}); err != nil || after.Model[name].ScheduleType != "" {
			t.Errorf("%s: the host's schedules once it is off = %+v, %v", host, after.Model, err)
		}
		if gone, err := c.DeleteAutoUpdateByContainerName(t.Context(), name, dockhand.DeleteAutoUpdateByContainerNameOperationOptions{Env: &env}); err != nil || !yes(gone.Model.Success) {
			t.Errorf("%s: deleting a schedule that is gone = %+v, %v", host, gone.Model, err)
		}

		// what a check noted is read two ways and cleared two ways, and clearing nothing is said to have worked
		if noted, err := c.GetContainersCheckUpdates(t.Context(), dockhand.GetContainersCheckUpdatesOperationOptions{Env: &env}); err != nil || noted.Model.EnvironmentId != env {
			t.Errorf("%s: what the last check noted = %+v, %v", host, noted.Model, err)
		}
		if cleared, err := c.DeleteContainersPendingUpdates(t.Context(), dockhand.DeleteContainersPendingUpdatesOperationOptions{Env: &env, ContainerId: id}); err != nil || !yes(cleared.Model.Success) {
			t.Errorf("%s: clearing one container's note = %+v, %v", host, cleared.Model, err)
		}
		if cleared, err := c.DeleteContainersPendingUpdates(t.Context(), dockhand.DeleteContainersPendingUpdatesOperationOptions{Env: &env}); err != nil || !yes(cleared.Model.Success) {
			t.Errorf("%s: clearing every note = %+v, %v", host, cleared.Model, err)
		}
		if left, err := c.GetContainersPendingUpdates(t.Context(), dockhand.GetContainersPendingUpdatesOperationOptions{Env: &env}); err != nil || len(left.Model.PendingUpdates) != 0 {
			t.Errorf("%s: what is noted once cleared = %+v, %v", host, left.Model, err)
		}

		// release notes: an image that does not say where its source is has none, and that is an answer
		notes, err := c.GetContainersByIdVersionNotes(t.Context(), id, dockhand.GetContainersByIdVersionNotesOperationOptions{Env: &env, Versions: "1.36,1.37"})
		if err != nil || len(notes.Model.Notes) != 0 || yes(notes.Model.RateLimited) || notes.Model.Source != "" {
			t.Errorf("%s: the release notes of an image with no source named = %+v, %v", host, notes.Model, err)
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
	var kept *dockhand.GetActivityResponse
	if !acctest.Eventually(func() bool {
		answer, err := c.GetActivity(t.Context(), dockhand.GetActivityOperationOptions{EnvironmentId: &env, ContainerName: name})
		kept = answer.Model

		return err == nil && slices.ContainsFunc(kept.Events, func(e dockhand.GetActivityResponseEvents) bool { return e.Action == "destroy" })
	}) {
		t.Fatalf("what %s did was not kept: %+v", name, kept)
	}
	if i := slices.IndexFunc(kept.Events, func(e dockhand.GetActivityResponseEvents) bool { return e.Action == "die" }); i < 0 || kept.Events[i].ActorAttributes["exitCode"] != "137" || kept.Events[i].EnvironmentId != env || kept.Events[i].EnvironmentName != host || kept.Events[i].ContainerName != name || len(kept.Events[i].ContainerId) != 64 || kept.Events[i].Image != world.Image || kept.Events[i].Timestamp == "" {
		t.Errorf("its death as kept = %+v", kept.Events)
	}
	if i := slices.IndexFunc(kept.Events, func(e dockhand.GetActivityResponseEvents) bool { return e.Action == "kill" }); i < 0 || kept.Events[i].ActorAttributes["signal"] != "9" {
		t.Errorf("its kill as kept = %+v", kept.Events)
	}
	// the latest first, and total says how many matched whatever the limit
	one := 1
	if latest, err := c.GetActivity(t.Context(), dockhand.GetActivityOperationOptions{EnvironmentId: &env, ContainerName: name, Limit: &one}); err != nil || len(latest.Model.Events) != 1 || latest.Model.Events[0].Action != "destroy" || latest.Model.Total != kept.Total || latest.Model.Limit != 1 {
		t.Errorf("the latest of what it did = %+v, %v", latest.Model, err)
	}
	// by action, a part of a name, and time: a time that cannot be read matches nothing and is no error
	if deaths, err := c.GetActivity(t.Context(), dockhand.GetActivityOperationOptions{EnvironmentId: &env, ContainerName: name[3:], Actions: "die,oom"}); err != nil || deaths.Model.Total != 1 || deaths.Model.Events[0].Action != "die" {
		t.Errorf("its deaths, asked for by a part of its name = %+v, %v", deaths.Model, err)
	}
	if early, err := c.GetActivity(t.Context(), dockhand.GetActivityOperationOptions{EnvironmentId: &env, ContainerName: name, ToDate: "2020-01-01T00:00:00Z"}); err != nil || early.Model.Total != 0 {
		t.Errorf("what it did before 2020 = %+v, %v", early.Model, err)
	}
	if odd, err := c.GetActivity(t.Context(), dockhand.GetActivityOperationOptions{EnvironmentId: &env, ContainerName: name, FromDate: "yesterday"}); err != nil || odd.Model.Total != 0 {
		t.Errorf("what it did since a time that cannot be read = %+v, %v", odd.Model, err)
	}

	// counted by action, for one host and for all
	here, err := c.GetActivityStats(t.Context(), dockhand.GetActivityStatsOperationOptions{EnvironmentId: &env})
	if err != nil || here.Model.Total < kept.Total || here.Model.ByAction["die"] < 1 || here.Model.ByAction["kill"] < 1 {
		t.Errorf("the host's activity counted = %+v, %v", here.Model, err)
	}
	if all, err := c.GetActivityStats(t.Context(), dockhand.GetActivityStatsOperationOptions{}); err != nil || all.Model.Total < here.Model.Total {
		t.Errorf("every host's activity counted = %+v, %v", all.Model, err)
	}

	// cleared, for every host at once, and what it did is not kept any more
	if cleared, err := c.DeleteActivity(t.Context()); err != nil || !yes(cleared.Model.Success) {
		t.Fatalf("clearing = %+v, %v", cleared.Model, err)
	}
	if after, err := c.GetActivity(t.Context(), dockhand.GetActivityOperationOptions{ContainerName: name}); err != nil || after.Model.Total != 0 {
		t.Errorf("what it did, once cleared = %+v, %v", after.Model, err)
	}
}
