//go:build acceptance

package tools_test

import (
	"archive/tar"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// tarred is the names of what a tar archive on this machine holds, in order.
func tarred(t *testing.T, file string) []string {
	t.Helper()

	opened, err := os.Open(file) //nolint:gosec // G304: a file the test's own tool call wrote
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = opened.Close() }()

	var names []string
	entries := tar.NewReader(opened)
	for {
		entry, err := entries.Next()
		if err == io.EOF {
			return names
		}
		if err != nil {
			t.Fatalf("%s is no tar archive: %v", file, err)
		}
		names = append(names, entry.Name)
	}
}

// An image and a volume are carried as files, with the tools alone: an image is given a name of its own, written out under it, loses the name, and
// has it back once the archive is loaded; a host behind an agent refuses the load; and a volume is written out whole and in part, with nothing left
// on the host by it and no volume made by asking for one there is none of.
func TestJourneyAnImageAndAVolumeCarriedAsFiles(t *testing.T) {
	const carried, volume, host = "acc-journey/carried:one", "acc-journey-carried", testenv.Alpha
	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	ctx := context.WithoutCancel(t.Context())
	folder := t.TempDir()
	clean := func() {
		_, _ = world.Docker(ctx, host, "rmi", carried)
		_, _ = world.Docker(ctx, host, "volume", "rm", "-f", volume)
	}
	clean()
	t.Cleanup(clean)
	names := func() []string {
		rows := acctest.Rows(t, call(t, "image_list", map[string]any{"env": host})["images"], "images")
		all := make([]string, 0, len(rows))
		for _, row := range rows {
			all = append(all, acctest.Strs(t, row["tags"], "tags")...)
		}

		return all
	}

	// the image under a name of its own, written out under that name: into a folder, and never over what is there
	call(t, "image_tag", map[string]any{"env": host, "image": world.Image, "as": carried})
	if msg := refused(t, "image_export", map[string]any{"env": host, "image": carried, "to": folder, "as": "acc-journey/no-such:name"}); !strings.Contains(msg, "which is none of the image's names") {
		t.Errorf("an export under a name the image does not go by = %q", msg)
	}
	exported := call(t, "image_export", map[string]any{"env": host, "image": carried, "to": folder, "as": carried})
	archive := filepath.Join(folder, "acc-journey_carried_one.tar")
	if exported["saved"] != archive || exported["recorded"] != carried || !slices.Contains(tarred(t, archive), "manifest.json") {
		t.Fatalf("an image exported = %v", exported)
	}
	if msg := refused(t, "image_export", map[string]any{"env": host, "image": carried, "to": folder, "as": carried}); !strings.Contains(msg, "it could not be written here") {
		t.Errorf("an export over the file written a moment ago = %q", msg)
	}

	// the name taken from the image on the host itself, and given back by loading the archive: said first, then done, then seen on the host
	if _, err := world.Docker(ctx, host, "rmi", carried); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(names(), carried) {
		t.Fatalf("the image still goes by %s after the name was removed", carried)
	}
	if dry := call(t, "image_load", map[string]any{"env": host, "file": archive, "dry_run": true}); strings.Join(acctest.Strs(t, dry["images"], "images"), " ") != carried || slices.Contains(names(), carried) {
		t.Fatalf("a dry run of loading the archive = %v", dry)
	}
	if loaded := call(t, "image_load", map[string]any{"env": host, "file": archive}); strings.Join(acctest.Strs(t, loaded["images"], "images"), " ") != carried || !slices.Contains(names(), carried) {
		t.Errorf("the archive loaded = %v", loaded)
	}
	if msg := refused(t, "image_load", map[string]any{"env": testenv.Beta, "file": archive}); !strings.Contains(msg, "not supported on Hawser connections") {
		t.Errorf("the archive loaded onto a host behind an agent = %q", msg)
	}

	// a volume with two files in it, written out whole and in part
	call(t, "volume_create", map[string]any{"env": host, "name": volume})
	if _, err := world.Docker(ctx, host, "run", "--rm", "-v", volume+":/v", world.Image, "sh", "-c", "mkdir -p /v/sub && echo hello > /v/a.txt && echo deep > /v/sub/b.txt"); err != nil {
		t.Fatal(err)
	}
	whole := call(t, "volume_export", map[string]any{"env": host, "volume": volume, "to": folder})
	if got := tarred(t, acctest.Str(whole["saved"])); whole["saved"] != filepath.Join(folder, volume+".tar") || !slices.Equal(got, []string{"volume/", "volume/a.txt", "volume/sub/", "volume/sub/b.txt"}) || whole["note"] != nil {
		t.Errorf("a volume exported = %v holding %v", whole, got)
	}
	part := call(t, "volume_export", map[string]any{"env": host, "volume": volume, "path": "sub", "to": filepath.Join(folder, "part.tar")})
	if got := tarred(t, acctest.Str(part["saved"])); part["path"] != "/sub" || !slices.Equal(got, []string{"sub/", "sub/b.txt"}) {
		t.Errorf("a part of a volume exported = %v holding %v", part, got)
	}
	// a volume there is none of is refused, where Dockhand left to itself would make it
	if msg := refused(t, "volume_export", map[string]any{"env": host, "volume": "acc-journey-no-such-volume", "to": folder}); !strings.Contains(msg, "acc-journey-no-such-volume") {
		t.Errorf("a volume there is none of, exported = %q", msg)
	}
	if out, err := world.Docker(ctx, host, "volume", "ls", "--format", "{{.Name}}"); err != nil || strings.Contains(out, "acc-journey-no-such-volume") {
		t.Errorf("the volumes of the host after that = %q, %v, want none made", out, err)
	}

	// with nothing left looking into it, the volume goes
	call(t, "volume_delete", map[string]any{"env": host, "volume": volume, "confirm": true})
}
