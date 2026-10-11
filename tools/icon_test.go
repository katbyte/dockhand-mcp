package tools

import (
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// What a 1.0.51 server answers about icons, with invented names: on alpha a container with a named icon and a name no container has that holds one of
// selfh.st's, a stack Dockhand has the file of with a picture, and a record of a stack that is gone; beta has a picture of its own.
const (
	alphaIcons    = `{"loose":"box","gone":"selfhst:plex"}`
	iconSources   = `{"mail":{"sourceType":"internal","composePath":"/app/data/stacks/alpha/mail/compose.yaml","icon":"custom:stack","envVarCount":3},"archive":{"sourceType":"git","composePath":"/app/data/git/archive/compose.yaml","icon":null,"envVarCount":0},"old":{"sourceType":"external","composePath":null,"icon":"box","envVarCount":0}}`
	alphaRecord   = `{"id":1,"name":"alpha","connectionType":"socket","icon":"globe"}`
	betaRecord    = `{"id":2,"name":"beta","connectionType":"hawser-standard","icon":"custom:env-2.webp"}`
	selfhstIcons  = `[{"Name":"Grafana","Reference":"grafana","SVG":"Yes","PNG":"Yes","WebP":"Yes","Light":"No","Dark":"No","Category":"Self-Hosted","Tags":"Monitoring, Dashboards","CreatedAt":"2024-08-16 00:27:23+00:00"},{"Name":"grafana Alloy","Reference":"grafana-alloy","SVG":"No","PNG":"Yes","WebP":"Yes","Light":"No","Dark":"No","Category":"Self-Hosted","Tags":"","CreatedAt":"2024-08-16 00:27:23+00:00"},{"Name":"Plex","Reference":"plex","SVG":"Yes","PNG":"Yes","WebP":"Yes","Light":"Yes","Dark":"No","Category":"Media","Tags":"Streaming","CreatedAt":"2024-08-16 00:27:23+00:00"}]`
	onePixel      = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	containerIcon = "custom:container"

	iconsCall     = "GET /api/container-icons"
	keptCall      = "GET /api/stacks/sources"
	alphaCall     = "GET /api/environments/1"
	betaCall      = "GET /api/environments/2"
	looseIcon     = "POST /api/container-icons/loose"
	mailIcon      = "POST /api/stacks/mail/icon"
	webIcon       = "POST /api/stacks/web/icon"
	alphaPicture  = "POST /api/environments/1/icon"
	goneIconGone  = "DELETE /api/container-icons/gone"
	mailIconGone  = "DELETE /api/stacks/mail/icon"
	betaIconGone  = "DELETE /api/environments/2/icon"
	manifestCall  = "GET /api/icons/selfhst-manifest"
	loosePicture  = "GET /api/container-icons/loose"
	selfhstFetch  = "GET /api/icons/selfhst/grafana"
	mailPicture   = "GET /api/stacks/mail/icon"
	betaPicture   = "GET /api/environments/2/icon"
	givenIcon     = `{"success":true,"icon":"server"}`
	givenPicture  = `{"success":true,"icon":"custom:container"}`
	aPictureOfSVG = `<svg xmlns="http://www.w3.org/2000/svg"/>`
)

// withIcons is a Dockhand managing alpha and beta where some things were given icons.
func withIcons(t *testing.T) *dockhandtest.Server {
	t.Helper()

	dock := withStackFiles(t)
	dock.AnswerWhen(iconsCall, "env", "1", alphaIcons)
	dock.AnswerWhen(iconsCall, "env", "2", `{}`)
	dock.AnswerWhen(keptCall, "env", "1", iconSources)
	dock.AnswerWhen(keptCall, "env", "2", `{}`)
	dock.Answer(alphaCall, alphaRecord)
	dock.Answer(betaCall, betaRecord)

	return dock
}

// pictures is a folder of this test's own holding a picture, a text, a file too large for an icon, and a folder: each by its full path.
func pictures(t *testing.T) (picture, text, large, folder string) {
	t.Helper()

	folder = t.TempDir()
	pixel, err := base64.StdEncoding.DecodeString(onePixel)
	if err != nil {
		t.Fatal(err)
	}
	picture, text, large = filepath.Join(folder, "icon.png"), filepath.Join(folder, "icon.txt"), filepath.Join(folder, "large.png")
	for file, content := range map[string][]byte{picture: pixel, text: []byte("not a picture"), large: append(pixel, make([]byte, maxIconPicture)...)} {
		if err := os.WriteFile(file, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	return picture, text, large, folder
}

func TestIconList(t *testing.T) {
	t.Parallel()

	dock := withIcons(t)
	suite := session(t, dock, Options{})

	// each host with its own icon, and what was given one on it: a name nothing has any more among them
	out := suite.Call(t, "icon_list", nil)
	rows := acctest.Rows(t, out["icons"], "icons")
	if len(rows) != 2 || out["failed"] != nil || rows[0]["env"] != "alpha" || rows[0]["icon"] != "globe" || rows[1]["env"] != "beta" || rows[1]["icon"] != "custom:env-2.webp" || rows[1]["containers"] != nil || rows[1]["stacks"] != nil {
		t.Fatalf("the icons of both hosts = %v", out)
	}
	if containers, stacks := acctest.Object(t, rows[0]["containers"], "containers"), acctest.Object(t, rows[0]["stacks"], "stacks"); len(containers) != 2 || containers["loose"] != "box" || containers["gone"] != "selfhst:plex" || len(stacks) != 2 || stacks["mail"] != "custom:stack" || stacks["old"] != "box" {
		t.Errorf("what was given an icon on alpha = %v", rows[0])
	}
	if out = suite.Call(t, "icon_list", map[string]any{"env": "beta"}); len(acctest.Rows(t, out["icons"], "icons")) != 1 {
		t.Errorf("the icons of one host = %v", out)
	}
	if msg := suite.CallErr(t, "icon_list", map[string]any{"env": "nowhere"}); !strings.Contains(msg, "nowhere") {
		t.Errorf("a host there is none of = %q", msg)
	}

	// a host that cannot be asked is said, whichever of the three reads fails
	dock.FailWhen(keptCall, "env", "1", http.StatusInternalServerError, "Failed to get stack sources")
	dock.Fail(betaCall, http.StatusInternalServerError, "Failed")
	if out = suite.Call(t, "icon_list", nil); len(acctest.Rows(t, out["failed"], "failed")) != 2 || len(acctest.Rows(t, out["icons"], "icons")) != 0 {
		t.Errorf("hosts that could not be asked = %v", out)
	}
	dock.FailWhen(iconsCall, "env", "1", http.StatusForbidden, notAllowed)
	if out = suite.Call(t, "icon_list", map[string]any{"env": 1}); len(acctest.Rows(t, out["failed"], "failed")) != 1 {
		t.Errorf("a host whose containers' icons cannot be read = %v", out)
	}
}

func TestIconSet(t *testing.T) {
	t.Parallel()

	picture, text, large, folder := pictures(t)
	loose := map[string]any{"env": "alpha", "container": "loose"}

	t.Run("refused before anything is sent", func(t *testing.T) {
		t.Parallel()

		dock := withIcons(t)
		suite := session(t, dock, everything)
		for what, tc := range map[string]struct {
			args map[string]any
			want string
		}{
			"neither a name nor a picture":   {map[string]any{"env": "alpha", "container": "loose"}, "give icon or file, one of them"},
			"both a name and a picture":      {map[string]any{"env": "alpha", "container": "loose", "icon": "box", "file": picture}, "give icon or file, one of them"},
			"Dockhand's mark for a picture":  {map[string]any{"env": "alpha", "container": "loose", "icon": containerIcon}, "custom: is how Dockhand marks a picture it was sent"},
			"a reference that is none":       {map[string]any{"env": "alpha", "container": "loose", "icon": "selfhst:Not Valid"}, "after selfhst: comes a reference"},
			"a name of two words":            {map[string]any{"env": "alpha", "container": "loose", "icon": "hard drive"}, "is one word"},
			"a picture by a part of a path":  {map[string]any{"env": "alpha", "container": "loose", "file": "icon.png"}, "by its full path from the top"},
			"a picture that is not there":    {map[string]any{"env": "alpha", "container": "loose", "file": filepath.Join(folder, "none.png")}, "could not be read"},
			"a folder for a picture":         {map[string]any{"env": "alpha", "container": "loose", "file": folder}, "is a folder, and an icon is one picture"},
			"a picture too large":            {map[string]any{"env": "alpha", "container": "loose", "file": large}, "bytes, and a picture for an icon is 200000 at most"},
			"a file that is no picture":      {map[string]any{"env": "alpha", "container": "loose", "file": text}, "is no PNG, JPEG, GIF or WebP picture"},
			"a container and a stack":        {map[string]any{"env": "alpha", "container": "loose", "stack": "mail", "icon": "box"}, "give container or stack, not both"},
			"no environment":                 {map[string]any{"container": "loose", "icon": "box"}, "env is required"},
			"a name no container has":        {map[string]any{"env": "alpha", "container": "nothing-here", "icon": "box"}, `no container is named "nothing-here" on alpha: container_list gives the ones there are. Dockhand would keep the icon under the name all the same`},
			"a name no stack has":            {map[string]any{"env": "alpha", "stack": "old", "icon": "server"}, `no stack is named "old" on alpha: stack_list gives the ones there are. Dockhand would keep the icon under the name all the same, and list a stack of that name with nothing in it`},
			"a stack Dockhand has no record": {map[string]any{"env": "alpha", "stack": "web", "icon": "box"}, "Dockhand only found the stack web on alpha and has no record of it. Given an icon it keeps one"},
		} {
			if msg := suite.CallErr(t, "icon_set", tc.args); !strings.Contains(msg, tc.want) {
				t.Errorf("%s = %q, want %q", what, msg, tc.want)
			}
		}

		// the icon it has is nothing to do, and a dry run says what would be: for a stack Dockhand only found, the record it would keep
		out := suite.Call(t, "icon_set", over(loose, map[string]any{"icon": " box "}))
		if !strings.Contains(acctest.Str(out["note"]), "nothing to do") || len(acctest.Strs(t, out["changes"], "changes")) != 0 || out["icon"] != "box" {
			t.Errorf("the icon it has already = %v", out)
		}
		out = suite.Call(t, "icon_set", over(loose, map[string]any{"file": picture, "dry_run": true}))
		if acctest.Strs(t, out["changes"], "changes")[0] != "would give the container loose on alpha the picture "+picture+", a png, as its icon" || out["icon"] != "box" || out["note"] != nil || out["container"] != "loose" {
			t.Errorf("a dry run of a picture = %v", out)
		}
		out = suite.Call(t, "icon_set", map[string]any{"env": "alpha", "stack": "web", "icon": "selfhst:grafana", "even_found": true, "dry_run": true})
		if acctest.Strs(t, out["changes"], "changes")[0] != "would give the stack web on alpha the icon selfhst:grafana" || !strings.Contains(acctest.Str(out["note"]), "Dockhand would keep a record of the stack from then on") || out["stack"] != "web" {
			t.Errorf("a dry run for a stack Dockhand only found = %v", out)
		}
		if out = suite.Call(t, "icon_set", map[string]any{"env": "alpha", "icon": "server", "dry_run": true}); acctest.Strs(t, out["changes"], "changes")[0] != "would give the environment alpha the icon server" || out["icon"] != "globe" || out["container"] != nil || out["stack"] != nil {
			t.Errorf("a dry run for an environment = %v", out)
		}
		for _, call := range []string{looseIcon, mailIcon, webIcon, alphaPicture, alphaSave} {
			if n := len(dock.Requests(call)); n != 0 {
				t.Errorf("%s was sent %d times for what was refused, was so already or was a dry run", call, n)
			}
		}
	})

	t.Run("what cannot be read", func(t *testing.T) {
		t.Parallel()

		dock := withIcons(t)
		suite := session(t, dock, everything)
		dock.FailWhen(iconsCall, "env", "1", http.StatusInternalServerError, "Failed")
		if msg := suite.CallErr(t, "icon_set", over(loose, map[string]any{"icon": "server"})); !strings.Contains(msg, "the icon of the container loose on alpha could not be read") {
			t.Errorf("a container's icon that could not be read = %q", msg)
		}
		dock.FailWhen(keptCall, "env", "1", http.StatusInternalServerError, "Failed")
		if msg := suite.CallErr(t, "icon_set", map[string]any{"env": "alpha", "stack": "mail", "icon": "server"}); !strings.Contains(msg, "the icon of the stack mail on alpha could not be read") {
			t.Errorf("a stack's icon that could not be read = %q", msg)
		}
		dock.Fail(alphaCall, http.StatusInternalServerError, "Failed")
		if msg := suite.CallErr(t, "icon_set", map[string]any{"env": "alpha", "icon": "server"}); !strings.Contains(msg, "the icon of the environment alpha could not be read") {
			t.Errorf("an environment's icon that could not be read = %q", msg)
		}
		dock.FailWhen("GET /api/containers", "env", "1", http.StatusInternalServerError, "Failed to list containers")
		if msg := suite.CallErr(t, "icon_set", over(loose, map[string]any{"icon": "server"})); !strings.Contains(msg, "the containers of alpha could not be listed") {
			t.Errorf("containers that could not be listed = %q", msg)
		}
		dock.FailWhen("GET /api/stacks", "env", "1", http.StatusInternalServerError, "Failed to list stacks")
		if msg := suite.CallErr(t, "icon_set", map[string]any{"env": "alpha", "stack": "mail", "icon": "server"}); !strings.Contains(msg, "the stacks of alpha could not be listed") {
			t.Errorf("stacks that could not be listed = %q", msg)
		}
	})

	t.Run("given to a container", func(t *testing.T) {
		t.Parallel()

		dock := withIcons(t)
		suite := session(t, dock, everything)
		dock.Answer(looseIcon, givenIcon)
		dock.Then(looseIcon, func(r dockhandtest.Request) {
			if strings.Contains(r.Body, `"image"`) {
				dock.AnswerWhen(iconsCall, "env", "1", `{"loose":"custom:container"}`)

				return
			}
			dock.AnswerWhen(iconsCall, "env", "1", `{"loose":"server"}`)
		})

		// by name, and then a picture, sent as a data: address of the file's own bytes
		out := suite.Call(t, "icon_set", over(loose, map[string]any{"icon": " server "}))
		last := dock.Last(looseIcon)
		if last.Body != `{"icon":"server"}` || last.Query.Get("env") != "1" || out["icon"] != "server" || out["note"] != nil || acctest.Strs(t, out["changes"], "changes")[0] != "gave the container loose on alpha the icon server" {
			t.Errorf("a container given an icon by name = %v, sent %s", out, last.Body)
		}
		dock.Answer(looseIcon, givenPicture)
		out = suite.Call(t, "icon_set", over(loose, map[string]any{"file": picture}))
		if body := dock.Last(looseIcon).Body; body != `{"image":"data:image/png;base64,`+onePixel+`"}` || out["icon"] != containerIcon || out["note"] != nil {
			t.Errorf("a container given a picture = %v, sent %s", out, body)
		}
	})

	t.Run("given to a stack and to an environment", func(t *testing.T) {
		t.Parallel()

		dock := withIcons(t)
		suite := session(t, dock, everything)
		dock.Answer(mailIcon, `{"success":true,"icon":"selfhst:grafana"}`)
		dock.Then(mailIcon, func(dockhandtest.Request) {
			dock.AnswerWhen(keptCall, "env", "1", strings.Replace(iconSources, `"icon":"custom:stack"`, `"icon":"selfhst:grafana"`, 1))
		})
		out := suite.Call(t, "icon_set", map[string]any{"env": "alpha", "stack": "mail", "icon": "selfhst:grafana"})
		if body := dock.Last(mailIcon).Body; body != `{"icon":"selfhst:grafana"}` || out["icon"] != "selfhst:grafana" || out["stack"] != "mail" || out["note"] != nil {
			t.Errorf("a stack given one of selfh.st's icons = %v, sent %s", out, body)
		}

		// a stack Dockhand only found, when it is asked for: the record Dockhand keeps from then on is said
		dock.Answer(webIcon, `{"success":true,"icon":"box"}`)
		dock.Then(webIcon, func(dockhandtest.Request) {
			dock.AnswerWhen(keptCall, "env", "1", `{"web":{"sourceType":"external","composePath":null,"icon":"box","envVarCount":0}}`)
		})
		out = suite.Call(t, "icon_set", map[string]any{"env": "alpha", "stack": "web", "icon": "box", "even_found": true})
		if out["icon"] != "box" || !strings.Contains(acctest.Str(out["note"]), "Dockhand keeps a record of the stack from now on, with no compose file") {
			t.Errorf("a stack Dockhand only found given an icon = %v", out)
		}

		// an environment's by name is saved with the environment, and a picture for it is sent by a call of its own
		dock.Answer(alphaSave, strings.Replace(alphaRecord, "globe", "server", 1))
		dock.Then(alphaSave, func(dockhandtest.Request) { dock.Answer(alphaCall, strings.Replace(alphaRecord, "globe", "server", 1)) })
		out = suite.Call(t, "icon_set", map[string]any{"env": "alpha", "icon": "server"})
		if body := dock.Last(alphaSave).Body; body != `{"icon":"server"}` || out["icon"] != "server" || out["container"] != nil || acctest.Strs(t, out["changes"], "changes")[0] != "gave the environment alpha the icon server" {
			t.Errorf("an environment given an icon by name = %v, sent %s", out, body)
		}
		dock.Answer(alphaPicture, `{"success":true,"icon":"custom:env-1.webp"}`)
		dock.Then(alphaPicture, func(dockhandtest.Request) {
			dock.Answer(alphaCall, strings.Replace(alphaRecord, "globe", "custom:env-1.webp", 1))
		})
		out = suite.Call(t, "icon_set", map[string]any{"env": "alpha", "file": picture})
		if body := dock.Last(alphaPicture).Body; body != `{"image":"data:image/png;base64,`+onePixel+`"}` || out["icon"] != "custom:env-1.webp" {
			t.Errorf("an environment given a picture = %v, sent %s", out, body)
		}
	})

	t.Run("not given, or not as asked", func(t *testing.T) {
		t.Parallel()

		dock := withIcons(t)
		suite := session(t, dock, everything)
		for call, args := range map[string]map[string]any{
			looseIcon:    over(loose, map[string]any{"icon": "server"}),
			mailIcon:     {"env": "alpha", "stack": "mail", "icon": "server"},
			alphaSave:    {"env": "alpha", "icon": "server"},
			alphaPicture: {"env": "alpha", "file": picture},
		} {
			dock.Fail(call, http.StatusForbidden, notAllowed)
			if msg := suite.CallErr(t, "icon_set", args); !strings.Contains(msg, "was not given the icon") {
				t.Errorf("%s refused = %q", call, msg)
			}
		}

		// answered as given, and read back as it was; and a picture answered as given that is read back as a name
		dock.Answer(looseIcon, givenIcon)
		if msg := suite.CallErr(t, "icon_set", over(loose, map[string]any{"icon": "server"})); !strings.Contains(msg, "the container loose on alpha was answered as given the icon, and what it has is box") {
			t.Errorf("an icon Dockhand did not take = %q", msg)
		}
		if msg := suite.CallErr(t, "icon_set", over(loose, map[string]any{"file": picture})); !strings.Contains(msg, "was answered as given the icon, and what it has is box") {
			t.Errorf("a picture Dockhand did not take = %q", msg)
		}
		dock.Then(looseIcon, func(dockhandtest.Request) { dock.AnswerWhen(iconsCall, "env", "1", `{}`) })
		if msg := suite.CallErr(t, "icon_set", over(loose, map[string]any{"icon": "server"})); !strings.Contains(msg, "and what it has is none") {
			t.Errorf("an icon read back as none = %q", msg)
		}

		// given, and not read back: what Dockhand answered with is what is given
		dock.AnswerWhen(iconsCall, "env", "1", alphaIcons)
		dock.Then(looseIcon, func(dockhandtest.Request) {
			dock.FailWhen(iconsCall, "env", "1", http.StatusInternalServerError, "Failed")
		})
		out := suite.Call(t, "icon_set", over(loose, map[string]any{"icon": "server"}))
		if out["icon"] != "server" || !strings.Contains(acctest.Str(out["note"]), "it was given, and could not be read back") || len(acctest.Strs(t, out["changes"], "changes")) != 1 {
			t.Errorf("an icon given and not read back = %v", out)
		}
	})
}

func TestIconDelete(t *testing.T) {
	t.Parallel()

	t.Run("said before it goes", func(t *testing.T) {
		t.Parallel()

		dock := withIcons(t)
		suite := session(t, dock, everything)
		if msg := suite.CallErr(t, "icon_delete", map[string]any{"env": "alpha", "container": "nothing-here", "confirm": true}); !strings.Contains(msg, `no container is named "nothing-here" on alpha, and the name holds no icon`) {
			t.Errorf("a name with nothing to it = %q", msg)
		}
		if msg := suite.CallErr(t, "icon_delete", map[string]any{"env": "alpha", "container": "loose", "stack": "mail", "confirm": true}); !strings.Contains(msg, "give container or stack, not both") {
			t.Errorf("a container and a stack = %q", msg)
		}
		// what has no icon of its own has nothing to remove: a container given none, and an environment with the plain one
		for what, args := range map[string]map[string]any{"the container web-front-1 on alpha": {"env": "alpha", "container": "web-front-1", "confirm": true}, "the environment alpha": {"env": "alpha", "confirm": true}, "the stack archive on alpha": {"env": "alpha", "stack": "archive", "confirm": true}} {
			if out := suite.Call(t, "icon_delete", args); out["note"] != "nothing to remove: "+what+" has no icon of its own" || !acctest.IsBool(out["removed"], false) {
				t.Errorf("%s = %v", what, out)
			}
		}

		// without confirm, what would go: a name that holds an icon from when a container had it among them
		out := suite.Call(t, "icon_delete", map[string]any{"env": "alpha", "container": "gone"})
		if would := acctest.Strs(t, out["would_go"], "would_go"); len(would) != 1 || would[0] != "the icon of the container gone on alpha, which is selfhst:plex" || out["was"] != "selfhst:plex" || !strings.Contains(acctest.Str(out["note"]), "no container is named gone on alpha now: the name holds the icon from when one was; nothing was removed") || out["container"] != "gone" {
			t.Errorf("what would go for a name nothing has = %v", out)
		}
		out = suite.Call(t, "icon_delete", map[string]any{"env": "beta"})
		if would := acctest.Strs(t, out["would_go"], "would_go"); would[0] != "the icon of the environment beta, which is custom:env-2.webp" || out["icon"] != "custom:env-2.webp" || !strings.HasPrefix(acctest.Str(out["note"]), "nothing was removed") {
			t.Errorf("what would go for an environment = %v", out)
		}
		for _, call := range []string{goneIconGone, mailIconGone, betaIconGone} {
			if n := len(dock.Requests(call)); n != 0 {
				t.Errorf("%s was sent %d times with no confirm", call, n)
			}
		}
	})

	t.Run("removed and read back as gone", func(t *testing.T) {
		t.Parallel()

		dock := withIcons(t)
		suite := session(t, dock, everything)
		dock.Answer(goneIconGone, done)
		dock.Then(goneIconGone, func(dockhandtest.Request) { dock.AnswerWhen(iconsCall, "env", "1", `{"loose":"box"}`) })
		out := suite.Call(t, "icon_delete", map[string]any{"env": "alpha", "container": "gone", "confirm": true})
		if !acctest.IsBool(out["removed"], true) || out["was"] != "selfhst:plex" || out["icon"] != nil || dock.Last(goneIconGone).Query.Get("env") != "1" {
			t.Errorf("a container's icon removed = %v", out)
		}

		dock.Answer(mailIconGone, done)
		dock.Then(mailIconGone, func(dockhandtest.Request) {
			dock.AnswerWhen(keptCall, "env", "1", strings.Replace(iconSources, `"icon":"custom:stack"`, `"icon":null`, 1))
		})
		if out = suite.Call(t, "icon_delete", map[string]any{"env": "alpha", "stack": "mail", "confirm": true}); !acctest.IsBool(out["removed"], true) || out["stack"] != "mail" || out["was"] != "custom:stack" {
			t.Errorf("a stack's icon removed = %v", out)
		}

		// an environment has the plain icon again
		dock.Answer(betaIconGone, `{"success":true,"icon":"globe"}`)
		dock.Then(betaIconGone, func(dockhandtest.Request) {
			dock.Answer(betaCall, strings.Replace(betaRecord, "custom:env-2.webp", "globe", 1))
		})
		if out = suite.Call(t, "icon_delete", map[string]any{"env": "beta", "confirm": true}); !acctest.IsBool(out["removed"], true) || out["icon"] != "globe" || out["was"] != "custom:env-2.webp" {
			t.Errorf("an environment's icon removed = %v", out)
		}
	})

	t.Run("not removed", func(t *testing.T) {
		t.Parallel()

		dock := withIcons(t)
		suite := session(t, dock, everything)
		for call, args := range map[string]map[string]any{goneIconGone: {"env": "alpha", "container": "gone", "confirm": true}, mailIconGone: {"env": "alpha", "stack": "mail", "confirm": true}, betaIconGone: {"env": "beta", "confirm": true}} {
			dock.Fail(call, http.StatusForbidden, notAllowed)
			if msg := suite.CallErr(t, "icon_delete", args); !strings.Contains(msg, "was not removed") {
				t.Errorf("%s refused = %q", call, msg)
			}
		}
		gone := map[string]any{"env": "alpha", "container": "gone", "confirm": true}
		dock.Answer(goneIconGone, notDone)
		if msg := suite.CallErr(t, "icon_delete", gone); !strings.Contains(msg, "without its being said to have succeeded") {
			t.Errorf("a removal not said to be done = %q", msg)
		}
		dock.Answer(goneIconGone, done)
		if msg := suite.CallErr(t, "icon_delete", gone); !strings.Contains(msg, "was answered as done, and it still has selfhst:plex") {
			t.Errorf("an icon still there = %q", msg)
		}
		dock.Then(goneIconGone, func(dockhandtest.Request) {
			dock.FailWhen(iconsCall, "env", "1", http.StatusInternalServerError, "Failed")
		})
		if out := suite.Call(t, "icon_delete", gone); !acctest.IsBool(out["removed"], false) || !strings.Contains(acctest.Str(out["note"]), "it was removed, and what is there now could not be read") {
			t.Errorf("a removal that could not be read back = %v", out)
		}
	})
}

func TestIconDownload(t *testing.T) {
	t.Parallel()

	dock := withIcons(t)
	dock.Answer(manifestCall, selfhstIcons)
	dock.AnswerText(selfhstFetch, "image/svg+xml", aPictureOfSVG)
	dock.AnswerText(mailPicture, "image/webp", "RIFF-webp-bytes")
	dock.AnswerText(betaPicture, "application/octet-stream", "who-knows")
	dock.Fail(loosePicture, http.StatusNotFound, "No custom icon")
	dock.Fail("GET /api/container-icons/web-front-1", http.StatusNotFound, "No custom icon")
	suite := session(t, dock, Options{})
	folder := t.TempDir()

	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"a part of a path":                 {map[string]any{"env": "alpha", "container": "loose", "to": "icon.webp"}, "is a full path from the top"},
		"one of selfh.st's and a host":     {map[string]any{"selfhst": "grafana", "env": "alpha", "to": folder}, "is no environment's, container's or stack's"},
		"one of selfh.st's and a stack":    {map[string]any{"selfhst": "grafana", "stack": "mail", "to": folder}, "is no environment's, container's or stack's"},
		"a reference that is none":         {map[string]any{"selfhst": "Not Valid", "to": folder}, "a reference is as icon_search gives them"},
		"a reference selfh.st has none of": {map[string]any{"selfhst": "selfhst:nothing", "to": folder}, `selfh.st has no icon with the reference "nothing"`},
		"an icon that is a name":           {map[string]any{"env": "alpha", "container": "loose", "to": folder}, "the container loose on alpha has no picture to fetch: its icon is box, a name and no picture that was sent"},
		"a container given no icon":        {map[string]any{"env": "alpha", "container": "web-front-1", "to": folder}, "the container web-front-1 on alpha has no picture to fetch: it was given no icon"},
		"no environment":                   {map[string]any{"container": "loose", "to": folder}, "env is required"},
	} {
		if msg := suite.CallErr(t, "icon_download", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}

	// into a folder that is there, under the name of what it is of with the ending its kind has
	out := suite.Call(t, "icon_download", map[string]any{"selfhst": "selfhst:grafana", "to": folder})
	saved := filepath.Join(folder, "grafana.svg")
	if content, err := os.ReadFile(saved); err != nil || string(content) != aPictureOfSVG || out["saved"] != saved || out["kind"] != "image/svg+xml" || acctest.Num(t, out["bytes"], "bytes") != len(aPictureOfSVG) || out["selfhst"] != "grafana" || out["env"] != nil { //nolint:gosec // G304: the path is the test's own, under its own temporary folder
		t.Errorf("one of selfh.st's icons fetched = %v, holding %q, %v", out, content, err)
	}
	if info, err := os.Stat(saved); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the file written = %v, %v, want it for its owner alone", info, err)
	}
	// never over a file that is there
	if msg := suite.CallErr(t, "icon_download", map[string]any{"selfhst": "grafana", "to": saved}); !strings.Contains(msg, "it could not be written here") {
		t.Errorf("a file that is there = %q", msg)
	}
	out = suite.Call(t, "icon_download", map[string]any{"env": "alpha", "stack": "mail", "to": filepath.Join(folder, "mail-icon")})
	if content, _ := os.ReadFile(filepath.Join(folder, "mail-icon")); string(content) != "RIFF-webp-bytes" || out["kind"] != "image/webp" || out["stack"] != "mail" || out["env"] != "alpha" { //nolint:gosec // G304: the path is the test's own, under its own temporary folder
		t.Errorf("a stack's picture fetched = %v, holding %q", out, content)
	}
	// a kind with no ending of its own
	if out = suite.Call(t, "icon_download", map[string]any{"env": "beta", "to": folder}); out["saved"] != filepath.Join(folder, "beta.img") || out["kind"] != "application/octet-stream" {
		t.Errorf("an environment's picture fetched = %v", out)
	}
	dock.AnswerText(loosePicture, "image/webp; charset=binary", "RIFF")
	if out = suite.Call(t, "icon_download", map[string]any{"env": "alpha", "container": "loose", "to": folder}); out["saved"] != filepath.Join(folder, "loose.webp") || out["kind"] != "image/webp" || out["container"] != "loose" {
		t.Errorf("a container's picture fetched = %v", out)
	}

	// what cannot be fetched
	dock.Fail(mailPicture, http.StatusInternalServerError, "Failed")
	if msg := suite.CallErr(t, "icon_download", map[string]any{"env": "alpha", "stack": "mail", "to": filepath.Join(folder, "again")}); !strings.Contains(msg, "the icon of the stack mail on alpha could not be fetched") {
		t.Errorf("a picture that could not be fetched = %q", msg)
	}
	dock.Fail(selfhstFetch, http.StatusBadGateway, "Failed")
	if msg := suite.CallErr(t, "icon_download", map[string]any{"selfhst": "grafana", "to": filepath.Join(folder, "again")}); !strings.Contains(msg, "the icon grafana could not be fetched") {
		t.Errorf("one of selfh.st's that could not be fetched = %q", msg)
	}
	dock.Fail(manifestCall, http.StatusServiceUnavailable, "Manifest unavailable")
	if msg := suite.CallErr(t, "icon_download", map[string]any{"selfhst": "grafana", "to": filepath.Join(folder, "again")}); !strings.Contains(msg, "the list of selfh.st's icons could not be had") {
		t.Errorf("a list that could not be had = %q", msg)
	}
}

func TestIconSearch(t *testing.T) {
	t.Parallel()

	dock := withIcons(t)
	dock.Answer(manifestCall, selfhstIcons)
	suite := session(t, dock, Options{})

	// every word is to be somewhere in it, whatever its case: its name, its reference, its category or its tags
	out := suite.Call(t, "icon_search", map[string]any{"query": " GRAFANA "})
	rows := acctest.Rows(t, out["icons"], "icons")
	if len(rows) != 2 || acctest.Num(t, out["total"], "total") != 2 || acctest.Num(t, out["of"], "of") != 3 || out["note"] != nil || rows[0]["name"] != "Grafana" || rows[0]["icon"] != "selfhst:grafana" || rows[0]["category"] != "Self-Hosted" || rows[0]["tags"] != "Monitoring, Dashboards" || rows[1]["icon"] != "selfhst:grafana-alloy" || rows[1]["tags"] != nil {
		t.Fatalf("icons found by a word = %v", out)
	}
	if out = suite.Call(t, "icon_search", map[string]any{"query": "dashboards grafana"}); acctest.Num(t, out["total"], "total") != 1 {
		t.Errorf("icons found by two words = %v", out)
	}
	if out = suite.Call(t, "icon_search", map[string]any{"query": "media"}); acctest.Rows(t, out["icons"], "icons")[0]["icon"] != "selfhst:plex" {
		t.Errorf("icons found by their category = %v", out)
	}
	// more than are asked for are counted, and the first by name given
	if out = suite.Call(t, "icon_search", map[string]any{"query": "e", "limit": 2}); len(acctest.Rows(t, out["icons"], "icons")) != 2 || acctest.Num(t, out["total"], "total") != 3 || !strings.Contains(acctest.Str(out["note"]), "the first 2 of 3 by name") {
		t.Errorf("more icons than asked for = %v", out)
	}
	if out = suite.Call(t, "icon_search", map[string]any{"query": "nothing here"}); acctest.Num(t, out["total"], "total") != 0 || out["note"] != "none of selfh.st's 3 icons has nothing and here in its name, reference, category or tags" {
		t.Errorf("no icon found = %v", out)
	}

	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"no words":         {map[string]any{"query": "  "}, "query is required"},
		"a limit too high": {map[string]any{"query": "grafana", "limit": 500}, "limit is 500: it is from 1 to 100"},
		"a limit below 1":  {map[string]any{"query": "grafana", "limit": -1}, "limit is -1"},
	} {
		if msg := suite.CallErr(t, "icon_search", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	dock.Fail(manifestCall, http.StatusServiceUnavailable, "Manifest unavailable")
	if msg := suite.CallErr(t, "icon_search", map[string]any{"query": "grafana"}); !strings.Contains(msg, "the list of selfh.st's icons could not be had, which Dockhand fetches from selfh.st and keeps a week") {
		t.Errorf("a list that could not be had = %q", msg)
	}
}

// A picture that is cut off part way is no picture: the part written is taken away again.
func TestSavedHere(t *testing.T) {
	t.Parallel()

	to := filepath.Join(t.TempDir(), "icon.webp")
	if _, err := savedHere(to, iotest.ErrReader(errors.New("cut off"))); err == nil || !strings.Contains(err.Error(), "it was not all fetched, and the part written to "+to+" was removed") {
		t.Errorf("a picture cut off = %v", err)
	}
	if _, err := os.Stat(to); !os.IsNotExist(err) {
		t.Errorf("the part written is still there: %v", err)
	}
	if size, err := savedHere(to, strings.NewReader("whole")); err != nil || size != 5 {
		t.Errorf("a picture written = %d, %v", size, err)
	}
}

// A picture that cannot be read after it was seen to be there is said.
func TestPictureAt(t *testing.T) {
	t.Parallel()

	picture, _, _, folder := pictures(t)
	if address, kind, err := pictureAt(picture); err != nil || kind != "image/png" || address != "data:image/png;base64,"+onePixel {
		t.Errorf("a picture = %s, %s, %v", address, kind, err)
	}
	locked := filepath.Join(folder, "locked.png")
	if err := os.WriteFile(locked, []byte("x"), 0o000); err != nil {
		t.Fatal(err)
	}
	if _, _, err := pictureAt(locked); err == nil || !strings.Contains(err.Error(), "could not be read") {
		t.Errorf("a picture that may not be read = %v", err)
	}
}
