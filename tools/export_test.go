package tools

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

const (
	backExport   = "GET /api/images/sha256:beef/export"
	noNameExport = "GET /api/images/sha256:9999999999990000000000000000000000000000000000000000000000000000/export"
	imageLoad    = "POST /api/images/load"
	spareExport  = "GET /api/volumes/spare/export"
	spareRelease = "POST /api/volumes/spare/browse/release"
	tarType      = "application/x-tar"
)

// onDisk puts a file of a test's own in a folder of its own and returns where.
func onDisk(t *testing.T, name, holds string) string {
	t.Helper()

	file := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(file, []byte(holds), 0o600); err != nil {
		t.Fatal(err)
	}

	return file
}

// heldBy is what a file a tool wrote holds.
func heldBy(t *testing.T, file string) string {
	t.Helper()

	holds, err := os.ReadFile(file) //nolint:gosec // G304: a file the test's own tool call wrote
	if err != nil {
		t.Fatal(err)
	}

	return string(holds)
}

func TestNamesInAnArchive(t *testing.T) {
	t.Parallel()

	saved := archived(t, "blobs/", "", "manifest.json", `[{"RepoTags":["registry.example/a:1","registry.example/a:latest"]},{"RepoTags":null},{"RepoTags":["registry.example/b:2"]}]`)
	for what, tc := range map[string]struct {
		file string
		want string
	}{
		"as docker save writes it":       {onDisk(t, "a.tar", saved), "registry.example/a:1 registry.example/a:latest registry.example/b:2"},
		"gzipped":                        {onDisk(t, "a.tar.gz", zipped(t, saved)), "registry.example/a:1 registry.example/a:latest registry.example/b:2"},
		"an archive that names nothing":  {onDisk(t, "plain.tar", archived(t, "notes.txt", "hello")), ""},
		"a list of images that is none":  {onDisk(t, "odd.tar", archived(t, "manifest.json", "not json")), ""},
		"what only starts as a gzip":     {onDisk(t, "odd.gz", "\x1f\x8bnot gzipped at all"), ""},
		"what is no archive":             {onDisk(t, "text.txt", "hello"), ""},
		"a file that is not there":       {filepath.Join(t.TempDir(), "nope.tar"), ""},
		"an archive cut short in a name": {onDisk(t, "cut.tar", saved[:700]), ""},
	} {
		if got := strings.Join(namesIn(tc.file), " "); got != tc.want {
			t.Errorf("the images in %s = %q, want %q", what, got, tc.want)
		}
	}
}

func TestImageExport(t *testing.T) {
	t.Parallel()

	dock := withImages(t)
	suite := session(t, dock, Options{})
	archive := archived(t, "manifest.json", `[{"RepoTags":["registry.example/back:1.2"]}]`)
	dock.AnswerText(backExport, tarType, archive)
	folder := t.TempDir()

	// into a folder: under the name the archive records, which is the first of the image's names unless another is asked for
	out := suite.Call(t, "image_export", map[string]any{"image": "registry.example/back:latest", "to": folder})
	saved := filepath.Join(folder, "registry.example_back_1.2.tar")
	if out["env"] != "alpha" || out["id"] != "beef" || out["recorded"] != "registry.example/back:1.2" || out["saved"] != saved || acctest.Num(t, out["bytes"], "bytes") != len(archive) || heldBy(t, saved) != archive || out["note"] != nil {
		t.Errorf("an image exported into a folder = %v", out)
	}
	if asked := dock.Last(backExport); asked.Query.Get("env") != "1" || asked.Query.Get("tag") != "registry.example/back:1.2" || asked.Query.Get("compress") != "false" {
		t.Errorf("the export was asked with %v", asked.Query)
	}
	// never over a file that is there
	if msg := suite.CallErr(t, "image_export", map[string]any{"image": "registry.example/back:latest", "to": folder}); !strings.Contains(msg, "it could not be written here") || heldBy(t, saved) != archive {
		t.Errorf("an export over a file that is there = %q", msg)
	}

	// to a file by name, gzipped when the name says so, under the name asked for
	zip := filepath.Join(folder, "back.TGZ")
	out = suite.Call(t, "image_export", map[string]any{"image": "beef", "to": " " + zip + " ", "as": " registry.example/back:latest "})
	if asked := dock.Last(backExport); out["recorded"] != "registry.example/back:latest" || out["saved"] != zip || asked.Query.Get("tag") != "registry.example/back:latest" || asked.Query.Get("compress") != "true" {
		t.Errorf("an image exported gzipped under another of its names = %v, asked with %v", out, asked.Query)
	}

	// an image with no name is exported by its id, and the archive records none
	dock.AnswerText(noNameExport, tarType, archive)
	out = suite.Call(t, "image_export", map[string]any{"image": "999999999999", "to": folder})
	if asked := dock.Last(noNameExport); out["recorded"] != nil || out["saved"] != filepath.Join(folder, "999999999999.tar") || asked.Query.Get("tag") != "" || !strings.Contains(acctest.Str(out["note"]), "the image has no name, so the archive records none") {
		t.Errorf("an image with no name exported = %v, asked with %v", out, asked.Query)
	}

	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"nowhere to write it":       {map[string]any{"image": "beef", "to": " "}, "to is required"},
		"an image there is none of": {map[string]any{"image": "registry.example/nope:1", "to": folder}, "nope"},
		"a name it does not go by":  {map[string]any{"image": "beef", "to": folder, "as": "registry.example/front:1.2"}, `as is "registry.example/front:1.2", which is none of the image's names: it goes by registry.example/back:1.2, registry.example/back:latest`},
	} {
		if msg := suite.CallErr(t, "image_export", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	dock.Fail(backExport, http.StatusInternalServerError, "Failed to export image: 404 - No such image")
	if msg := suite.CallErr(t, "image_export", map[string]any{"image": "beef", "to": filepath.Join(folder, "gone.tar")}); !strings.Contains(msg, "could not be exported from alpha") || !strings.Contains(msg, "No such image") {
		t.Errorf("an export Dockhand could not make = %q", msg)
	}
}

func TestImageLoad(t *testing.T) {
	t.Parallel()

	archive := archived(t, "manifest.json", `[{"RepoTags":["registry.example/new:1"]}]`)
	file := onDisk(t, "new.tar", archive)

	t.Run("refused before anything is sent", func(t *testing.T) {
		t.Parallel()

		dock := withImages(t)
		suite := session(t, dock, everything)
		for what, tc := range map[string]struct {
			args map[string]any
			want string
		}{
			"no file":                 {map[string]any{"env": "alpha", "file": " "}, "file is required"},
			"a file that is not here": {map[string]any{"env": "alpha", "file": filepath.Join(t.TempDir(), "nope.tar")}, "file could not be read here"},
			"a folder":                {map[string]any{"env": "alpha", "file": t.TempDir()}, "which is a folder: it is the archive itself"},
			"an empty file":           {map[string]any{"env": "alpha", "file": onDisk(t, "empty.tar", "")}, "which is empty"},
			"no host named":           {map[string]any{"file": file}, "env is required"},
		} {
			if msg := suite.CallErr(t, "image_load", tc.args); !strings.Contains(msg, tc.want) {
				t.Errorf("%s = %q, want %q", what, msg, tc.want)
			}
		}

		// a dry run says what the archive holds, and which of those names the host has given to something already
		dry := suite.Call(t, "image_load", map[string]any{"env": "alpha", "file": file, "dry_run": true})
		if strings.Join(acctest.Strs(t, dry["images"], "images"), " ") != "registry.example/new:1" || !acctest.IsBool(dry["dry_run"], true) || acctest.Num(t, dry["bytes"], "bytes") != len(archive) || dry["note"] != nil {
			t.Errorf("a dry run = %v", dry)
		}
		taken := onDisk(t, "tool.tar.gz", zipped(t, archived(t, "manifest.json", `[{"RepoTags":["registry.example/tool:latest"]}]`)))
		if dry = suite.Call(t, "image_load", map[string]any{"env": "alpha", "file": taken, "dry_run": true}); !strings.Contains(acctest.Str(dry["note"]), "alpha has an image named registry.example/tool:latest already: the name goes to the one in the archive") {
			t.Errorf("a dry run of a name the host has = %v", dry)
		}
		if dry = suite.Call(t, "image_load", map[string]any{"env": "alpha", "file": onDisk(t, "odd.tar", "no archive"), "dry_run": true}); len(acctest.Strs(t, dry["images"], "images")) != 0 || !strings.Contains(acctest.Str(dry["note"]), "the archive does not say which images it holds") {
			t.Errorf("a dry run of what names no image = %v", dry)
		}
		if n := len(dock.Requests(imageLoad)); n != 0 {
			t.Errorf("%d archives were sent by what was refused or was a dry run", n)
		}

		// a file this user may not read is refused here, before Dockhand is asked anything
		locked := onDisk(t, "locked.tar", archive)
		if err := os.Chmod(locked, 0); err != nil {
			t.Fatal(err)
		}
		if msg := suite.CallErr(t, "image_load", map[string]any{"env": "alpha", "file": locked}); os.Getuid() != 0 && !strings.Contains(msg, "file could not be read here") {
			t.Errorf("a file that may not be read = %q", msg)
		}
		dock.FailWhen("GET /api/images", "env", "1", http.StatusBadGateway, "the host did not answer")
		if msg := suite.CallErr(t, "image_load", map[string]any{"env": "alpha", "file": file}); !strings.Contains(msg, "the images of alpha could not be listed, so nothing was loaded") {
			t.Errorf("a host that cannot be asked for its images = %q", msg)
		}
	})

	t.Run("loaded", func(t *testing.T) {
		t.Parallel()

		dock := withImages(t)
		suite := session(t, dock, everything)
		dock.Answer(imageLoad, `{"success":true,"loaded":"Loaded image: registry.example/new:1\nLoaded image ID: sha256:abcd\n"}`)
		dock.Then(imageLoad, func(dockhandtest.Request) {
			dock.AnswerWhen("GET /api/images", "env", "1", `[`+toolImage+`,{"id":"sha256:1111","repoTags":["registry.example/new:1"],"tags":["registry.example/new:1"],"size":1,"created":1},{"id":"sha256:abcd","repoTags":["<none>:<none>"],"tags":["<none>:<none>"],"size":1,"created":1}]`)
		})

		// the whole file goes as it is, and what Docker says it loaded is read back from the host, an image with no name by its id
		out := suite.Call(t, "image_load", map[string]any{"env": "alpha", "file": file})
		if sent := dock.Last(imageLoad); sent.Body != archive || sent.Query.Get("env") != "1" {
			t.Errorf("the archive was sent as %d bytes to %v, want its %d", len(sent.Body), sent.Query, len(archive))
		}
		if got := strings.Join(acctest.Strs(t, out["images"], "images"), " "); got != "registry.example/new:1 sha256:abcd" || out["env"] != "alpha" || out["file"] != file || out["note"] != nil || out["dry_run"] != nil {
			t.Errorf("an archive loaded = %v", out)
		}
	})

	t.Run("not loaded, or not read back", func(t *testing.T) {
		t.Parallel()

		dock := withImages(t)
		suite := session(t, dock, everything)

		dock.Fail(imageLoad, http.StatusInternalServerError, "Uploading an image tar is not supported on Hawser connections yet; use a local or direct-TCP environment.")
		if msg := suite.CallErr(t, "image_load", map[string]any{"env": "beta", "file": file}); !strings.Contains(msg, "the archive was not loaded on beta") || !strings.Contains(msg, "not supported on Hawser connections") {
			t.Errorf("an archive for a host behind an agent = %q", msg)
		}
		dock.Answer(imageLoad, `{"success":false}`)
		if msg := suite.CallErr(t, "image_load", map[string]any{"env": "alpha", "file": file}); !strings.Contains(msg, "the archive was not loaded on alpha, and Dockhand does not say why") {
			t.Errorf("a load answered as failed = %q", msg)
		}
		// answered as loaded and not on the host is an error, not a success
		dock.Answer(imageLoad, `{"success":true,"loaded":"Loaded image: registry.example/new:1"}`)
		if msg := suite.CallErr(t, "image_load", map[string]any{"env": "alpha", "file": file}); !strings.Contains(msg, "Dockhand answered that registry.example/new:1 was loaded on alpha, and the host does not list it") {
			t.Errorf("a load answered as done and not there = %q", msg)
		}
		dock.Then(imageLoad, func(dockhandtest.Request) {
			dock.FailWhen("GET /api/images", "env", "1", http.StatusBadGateway, "the host did not answer")
		})
		if out := suite.Call(t, "image_load", map[string]any{"env": "alpha", "file": file}); !strings.Contains(acctest.Str(out["note"]), "it was loaded, and the images of alpha could not be listed afterwards") || len(acctest.Strs(t, out["images"], "images")) != 1 {
			t.Errorf("a load that could not be read back = %v", out)
		}
	})
}

func TestVolumeExport(t *testing.T) {
	t.Parallel()

	dock := withVolumes(t)
	suite := session(t, dock, everything)
	archive := archived(t, "volume/", "", "volume/a.txt", "hello")
	dock.AnswerText(spareExport, gzipType, zipped(t, archive))
	dock.Answer(spareRelease, done)
	folder := t.TempDir()

	// into a folder, under the volume's name: asked for gzipped whatever is wanted, unpacked here to a plain tar, and the helper taken away
	out := suite.Call(t, "volume_export", map[string]any{"env": "alpha", "volume": "spare", "to": folder})
	saved := filepath.Join(folder, "spare.tar")
	if out["env"] != "alpha" || out["volume"] != "spare" || out["path"] != "/" || out["saved"] != saved || heldBy(t, saved) != archive || acctest.Num(t, out["bytes"], "bytes") != len(archive) || out["note"] != nil {
		t.Errorf("a volume exported into a folder = %v", out)
	}
	if asked := dock.Last(spareExport); asked.Query.Get("env") != "1" || asked.Query.Get("path") != "/" || asked.Query.Get("format") != "tar.gz" || len(dock.Requests(spareRelease)) != 1 {
		t.Errorf("the export was asked with %v, and the helper released %d times", asked.Query, len(dock.Requests(spareRelease)))
	}

	// a part of it, from the volume's top and never above it, kept gzipped when the name says so
	out = suite.Call(t, "volume_export", map[string]any{"env": "alpha", "volume": "spare", "path": "../../etc/app/", "to": filepath.Join(folder, "app.tar.gz")})
	if asked := dock.Last(spareExport); out["path"] != "/etc/app" || asked.Query.Get("path") != "/etc/app" || heldBy(t, filepath.Join(folder, "app.tar.gz")) != zipped(t, archive) {
		t.Errorf("a part of a volume exported = %v, asked with %v", out, asked.Query)
	}
	if out = suite.Call(t, "volume_export", map[string]any{"env": "alpha", "volume": "spare", "path": "conf", "to": folder}); out["saved"] != filepath.Join(folder, "spare-conf.tar") {
		t.Errorf("a part of a volume exported into a folder = %v", out)
	}

	// a helper Dockhand does not say it took away is said, on the answer and on a failure alike
	dock.Answer(spareRelease, notDone)
	if msg := suite.CallErr(t, "volume_export", map[string]any{"env": "alpha", "volume": "spare", "to": folder}); !strings.Contains(msg, "it could not be written here") || !strings.Contains(msg, "the helper container Dockhand started on the host to look inside the volume was not said to have been taken away") {
		t.Errorf("an export over a file that is there, the helper left = %q", msg)
	}
	if out = suite.Call(t, "volume_export", map[string]any{"env": "alpha", "volume": "spare", "to": filepath.Join(folder, "again.tar")}); !strings.Contains(acctest.Str(out["note"]), "was not said to have been taken away") {
		t.Errorf("an export whose helper was left = %v", out)
	}
	dock.Answer(spareRelease, done)

	dock.AnswerText(spareExport, gzipType, "this is no archive")
	if msg := suite.CallErr(t, "volume_export", map[string]any{"env": "alpha", "volume": "spare", "to": filepath.Join(folder, "odd.tar")}); !strings.Contains(msg, "what Dockhand answered for the volume spare is no archive") {
		t.Errorf("an answer that is no archive = %q", msg)
	}
	dock.Fail(spareExport, http.StatusInternalServerError, "Failed to export volume")
	if msg := suite.CallErr(t, "volume_export", map[string]any{"env": "alpha", "volume": "spare", "path": "nope", "to": filepath.Join(folder, "nope.tar")}); !strings.Contains(msg, "/nope of the volume spare could not be exported from alpha") {
		t.Errorf("a path the volume has nothing at = %q", msg)
	}

	// a volume there is none of is refused here: asked for it, Dockhand would make it
	before := len(dock.Requests(spareExport))
	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"nowhere to write it":       {map[string]any{"env": "alpha", "volume": "spare", "to": ""}, "to is required"},
		"a volume there is none of": {map[string]any{"env": "alpha", "volume": "nope", "to": folder}, "nope"},
	} {
		if msg := suite.CallErr(t, "volume_export", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	if len(dock.Requests(spareExport)) != before || len(dock.Calls()) == 0 {
		t.Error("an export was asked of Dockhand for what was refused here")
	}
}

func TestAVolumesHelperIsWaitedFor(t *testing.T) {
	t.Parallel()

	const helper = `[{"id":"abcdefabcdef0000000000000000000000000000000000000000000000000000","name":"dockhand-browse-spare-ro-x1","image":"busybox:latest","state":"running","status":"Up 1 second"}]`
	dock := withVolumes(t)
	suite := session(t, dock, quick)
	dock.AnswerText(spareExport, gzipType, zipped(t, archived(t, "volume/", "")))
	dock.Answer(spareRelease, done)
	folder := t.TempDir()

	// Dockhand says the helper was taken away while it is still there: it is asked after until it is gone, and nothing is said of it
	asked := 0
	dock.AnswerWhen("GET /api/containers", "env", "1", helper)
	dock.Then("GET /api/containers", func(dockhandtest.Request) {
		asked++
		if asked == 3 {
			dock.AnswerWhen("GET /api/containers", "env", "1", alphaContainers)
		}
	})
	if out := suite.Call(t, "volume_export", map[string]any{"env": "alpha", "volume": "spare", "to": filepath.Join(folder, "a.tar")}); out["note"] != nil || asked < 3 {
		t.Errorf("an export whose helper went after a moment = %v, the host asked %d times", out, asked)
	}

	// one that never goes is said, once it has been waited for
	dock.Then("GET /api/containers", nil)
	dock.AnswerWhen("GET /api/containers", "env", "1", helper)
	if out := suite.Call(t, "volume_export", map[string]any{"env": "alpha", "volume": "spare", "to": filepath.Join(folder, "b.tar")}); !strings.Contains(acctest.Str(out["note"]), "was not gone some seconds after Dockhand said it was taken away") {
		t.Errorf("an export whose helper never went = %v", out)
	}
	// and a host that cannot be asked is not waited on
	dock.FailWhen("GET /api/containers", "env", "1", http.StatusBadGateway, "the host did not answer")
	if out := suite.Call(t, "volume_export", map[string]any{"env": "alpha", "volume": "spare", "to": filepath.Join(folder, "c.tar")}); out["note"] != nil {
		t.Errorf("an export on a host that cannot be asked for its containers = %v", out)
	}
}
