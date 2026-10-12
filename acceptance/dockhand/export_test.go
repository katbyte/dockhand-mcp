//go:build acceptance

package dockhand_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/pandorest/client"
)

// entriesOf is the names of what a tar archive holds, in order, with what the entry named holds when there is one by that name.
func entriesOf(t *testing.T, archive []byte, named string) (names []string, held string) {
	t.Helper()

	entries := tar.NewReader(bytes.NewReader(archive))
	for {
		entry, err := entries.Next()
		if err == io.EOF {
			return names, held
		}
		if err != nil {
			t.Fatalf("what was answered is no tar archive: %v, after %v", err, names)
		}
		names = append(names, entry.Name)
		if entry.Name == named {
			content, _ := io.ReadAll(entries)
			held = string(content)
		}
	}
}

// unzipped is a gzipped answer unpacked.
func unzipped(t *testing.T, zipped []byte) []byte {
	t.Helper()

	plain, err := gzip.NewReader(bytes.NewReader(zipped))
	if err != nil {
		t.Fatalf("what was answered is not gzipped: %v", err)
	}
	out, err := io.ReadAll(plain)
	if err != nil {
		t.Fatal(err)
	}

	return out
}

// An image is written out as an archive and loaded from one: by its id alone, under the name asked for, gzipped when asked, from behind an agent as
// well; and loaded onto a host Dockhand reaches itself, and refused for one behind an agent.
func TestImageExportedAndLoaded(t *testing.T) {
	const carried = "it-export/carried:one"
	c := dock(t)
	ctx := t.Context()
	alpha, beta := world.Env(testenv.Alpha), world.Env(testenv.Beta)
	after := noCancel(t)
	for _, host := range []string{testenv.Alpha, testenv.Beta} {
		if _, err := world.Docker(ctx, host, "pull", "-q", world.Image); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := world.Docker(ctx, testenv.Alpha, "tag", world.Image, carried); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = world.Docker(after, testenv.Alpha, "rmi", carried) })
	image := imageOf(t, int(alpha), carried)
	if image == nil {
		t.Fatalf("the host does not list the image tagged a moment ago as %s", carried)
	}
	export := func(env int, id, tag string, compress bool) ([]byte, http.Header, error) {
		got, err := c.GetImagesByIdExport(ctx, id, dockhand.GetImagesByIdExportOptions{Env: dockhand.EnvironmentID(env), Tag: tag, Compress: new(compress)})
		if got.HttpResponse == nil || got.HttpResponse.Body == nil {
			return nil, nil, err
		}
		defer func() { _ = got.HttpResponse.Body.Close() }()
		body, _ := io.ReadAll(got.HttpResponse.Body)

		return body, got.HttpResponse.Header, err
	}
	recorded := func(archive []byte) []string {
		_, manifest := entriesOf(t, archive, "manifest.json")
		var held []struct{ RepoTags []string }
		if err := json.Unmarshal([]byte(manifest), &held); err != nil || len(held) != 1 {
			t.Fatalf("the archive's list of images = %q, %v", manifest, err)
		}

		return held[0].RepoTags
	}

	// under a name asked for, the archive records that name alone and is called after it
	plain, header, err := export(int(alpha), image.Id, carried, false)
	if err != nil || header.Get("Content-Type") != "application/x-tar" || !strings.Contains(header.Get("Content-Disposition"), "it-export_carried_one.tar") || !slices.Equal(recorded(plain), []string{carried}) {
		t.Fatalf("an image exported under one of its names = %d bytes, %v, %v, recording %v", len(plain), header, err, recorded(plain))
	}
	// gzipped when asked, and the same archive
	zipped, header, err := export(int(alpha), image.Id, carried, true)
	if err != nil || header.Get("Content-Type") != "application/gzip" || !strings.Contains(header.Get("Content-Disposition"), ".tar.gz") || !slices.Equal(recorded(unzipped(t, zipped)), []string{carried}) {
		t.Errorf("an image exported gzipped = %d bytes, %v, %v", len(zipped), header, err)
	}
	// from behind the agent, whole
	if through, _, err := export(int(beta), imageOf(t, int(beta), world.Image).Id, world.Image, false); err != nil || !slices.Equal(recorded(through), []string{world.Image}) {
		t.Errorf("an image exported from behind the agent = %d bytes, %v", len(through), err)
	}
	// an image is exported by its id, and a name is no id; one there is none of is Docker's own refusal, as a 500
	if _, _, err := export(int(alpha), carried, "", false); client.StatusCode(err) != http.StatusBadRequest || !strings.Contains(err.Error(), "Invalid image ID") {
		t.Errorf("an image exported by a name = %v, want a 400", err)
	}
	if _, _, err := export(int(alpha), "sha256:"+strings.Repeat("0", 64), "", false); client.StatusCode(err) != http.StatusInternalServerError || !strings.Contains(err.Error(), "No such image") {
		t.Errorf("an image there is none of, exported = %v, want a 500", err)
	}

	// loaded: the name it records is the host's again once it was taken away, from a plain archive and from a gzipped one
	load := func(env int, archive []byte) (*dockhand.PostImagesLoadAnswer, error) {
		loaded, err := c.PostImagesLoad(ctx, bytes.NewReader(archive), "application/x-tar", dockhand.PostImagesLoadOptions{Env: dockhand.EnvironmentID(env)})

		return loaded.Answer, err
	}
	for what, archive := range map[string][]byte{"a plain archive": plain, "a gzipped archive": zipped} {
		if _, err := world.Docker(ctx, testenv.Alpha, "rmi", carried); err != nil {
			t.Fatal(err)
		}
		loaded, err := load(int(alpha), archive)
		if err != nil || !loaded.Success || loaded.Loaded != "Loaded image: "+carried || imageOf(t, int(alpha), carried) == nil {
			t.Errorf("%s loaded = %+v, %v", what, loaded, err)
		}
	}
	// a host behind an agent is refused
	if _, err := load(int(beta), plain); client.StatusCode(err) != http.StatusInternalServerError || !strings.Contains(err.Error(), "not supported on Hawser connections") {
		t.Errorf("an archive loaded onto a host behind an agent = %v, want it refused", err)
	}
	// what is no archive is not loaded: Dockhand answers a 500 in Docker's words, or hangs up before it answers anything
	if loaded, err := load(int(alpha), []byte(strings.Repeat("this is no archive\n", 300))); err == nil {
		t.Errorf("what is no archive, loaded = %+v, want it refused", loaded)
	}
}

// A volume is written out as an archive: all of it or a part, as a tar, gzipped, or one file as itself; each export leaves a helper container on the
// host until it is released; and asked to export a volume there is none of, Dockhand makes the volume and answers an empty archive.
func TestVolumeExported(t *testing.T) {
	const host = testenv.Alpha
	c := dock(t)
	ctx := t.Context()
	env := world.Env(host)
	after := noCancel(t)
	volume, phantom := named(t, "volume"), named(t, "phantom")
	helpers := func(of string) int {
		out, err := world.Docker(after, host, "ps", "-aq", "--filter", "name=dockhand-browse-"+of)
		if err != nil {
			t.Fatal(err)
		}

		return len(strings.Fields(out))
	}
	clean := func() {
		for _, name := range []string{volume, phantom} {
			_, _ = c.PostVolumesByNameBrowseRelease(after, name, dockhand.PostVolumesByNameBrowseReleaseOptions{Env: env})
			_, _ = world.Docker(after, host, "volume", "rm", "-f", name)
		}
	}
	clean()
	t.Cleanup(clean)
	if _, err := world.Docker(ctx, host, "run", "--rm", "-v", volume+":/v", world.Image, "sh", "-c", "mkdir -p /v/sub && echo hello > /v/a.txt && echo deep > /v/sub/b.txt"); err != nil {
		t.Fatal(err)
	}
	export := func(name, within, format string) ([]byte, http.Header, error) {
		got, err := c.GetVolumesByNameExport(ctx, name, dockhand.GetVolumesByNameExportOptions{Env: env, Path: within, Format: format})
		if got.HttpResponse == nil || got.HttpResponse.Body == nil {
			return nil, nil, err
		}
		defer func() { _ = got.HttpResponse.Body.Close() }()
		body, _ := io.ReadAll(got.HttpResponse.Body)

		return body, got.HttpResponse.Header, err
	}

	// all of it, under a folder named volume
	whole, header, err := export(volume, "", "")
	names, held := entriesOf(t, whole, "volume/a.txt")
	if err != nil || header.Get("Content-Type") != "application/x-tar" || !strings.Contains(header.Get("Content-Disposition"), volume+".tar") || !slices.Equal(names, []string{"volume/", "volume/a.txt", "volume/sub/", "volume/sub/b.txt"}) || held != "hello\n" {
		t.Fatalf("a volume exported = %v holding %q, %v, %v", names, held, header, err)
	}
	// gzipped, a part of it under its own name, and one file as itself
	if zipped, header, err := export(volume, "", "tar.gz"); err != nil || header.Get("Content-Type") != "application/gzip" || !bytes.Equal(unzipped(t, zipped)[:512], whole[:512]) {
		t.Errorf("a volume exported gzipped = %d bytes, %v, %v", len(zipped), header, err)
	}
	if part, _, err := export(volume, "/sub", ""); err != nil {
		t.Errorf("a part of a volume exported = %v", err)
	} else if names, held := entriesOf(t, part, "sub/b.txt"); !slices.Equal(names, []string{"sub/", "sub/b.txt"}) || held != "deep\n" {
		t.Errorf("a part of a volume exported = %v holding %q", names, held)
	}
	if raw, header, err := export(volume, "/a.txt", "raw"); err != nil || string(raw) != "hello\n" || header.Get("Content-Type") != "application/octet-stream" {
		t.Errorf("one file of a volume as itself = %q, %v, %v", raw, header, err)
	}
	// a form there is none of is a plain tar, and a path there is nothing at is a 500
	if odd, header, err := export(volume, "", "zip"); err != nil || header.Get("Content-Type") != "application/x-tar" || len(odd) != len(whole) {
		t.Errorf("a volume exported in a form there is none of = %d bytes, %v, %v, want a plain tar", len(odd), header, err)
	}
	if _, _, err := export(volume, "/nope", ""); client.StatusCode(err) != http.StatusInternalServerError || !strings.Contains(err.Error(), "Failed to export volume") {
		t.Errorf("a path the volume has nothing at = %v, want a 500", err)
	}

	// every export left a helper container on the host with the volume mounted, which stays until it is released
	if left := helpers(volume); left == 0 {
		t.Error("no helper container was left by the exports, where each leaves one")
	}
	// released, they are taken away, which the answer does not wait for
	if released, err := c.PostVolumesByNameBrowseRelease(ctx, volume, dockhand.PostVolumesByNameBrowseReleaseOptions{Env: env}); err != nil || !released.Answer.Success {
		t.Errorf("the helpers released = %+v, %v", released.Answer, err)
	}
	for waited := 0; helpers(volume) != 0; waited++ {
		if waited == 30 {
			t.Fatalf("%d helper containers are still on the host half a minute after they were released", helpers(volume))
		}
		time.Sleep(time.Second)
	}

	// a volume there is none of: no refusal, an empty archive, and the volume is there afterwards
	if on(t, host, "volume", phantom) {
		t.Fatalf("the volume %s is there before it was asked for", phantom)
	}
	empty, _, err := export(phantom, "", "")
	if names, _ := entriesOf(t, empty, ""); err != nil || !slices.Equal(names, []string{"volume/"}) || !on(t, host, "volume", phantom) {
		t.Errorf("a volume there is none of, exported = %v, %v, and the volume is there: %v, want an empty archive and a volume made", names, err, on(t, host, "volume", phantom))
	}
}
