package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// The canned answers below are a 1.0.51 server's own: the update check of one that can see its own container, and of one given no Docker socket.
const (
	updateCheckCall = "GET /api/self-update/check"
	updateOut       = `{"updateAvailable":true,"currentImage":"fnsys/dockhand:v1.0.51","newImage":"fnsys/dockhand:v1.0.53","latestVersion":"1.0.53","containerName":"dockhand","isComposeManaged":false}`
	updateNoSocket  = `{"updateAvailable":false,"error":"Check failed: Error: connect ENOENT /var/run/docker.sock"}`
	dependencies    = `[{"name":"zod","version":"4.1.0","license":"MIT","repository":"https://github.com/colinhacks/zod"},{"name":"anchore/grype","version":"v0.119.0","license":"Apache-2.0","repository":"https://github.com/anchore/grype"},{"name":"@1password/sdk","version":"0.4.1-beta.1","license":"MIT","repository":null},{"name":"Chownr","version":"1.1.4","license":"ISC","repository":"https://github.com/isaacs/chownr"},{"name":"odd","version":"1.0.0"}]`
)

func TestServerUpdateCheck(t *testing.T) {
	t.Parallel()

	dock := dockhandtest.New(t)
	dock.Answer(updateCheckCall, updateOut)
	suite := session(t, dock, Options{})

	out := suite.Call(t, "server_update_check", nil)
	if !acctest.IsBool(out["update_available"], true) || !acctest.IsBool(out["checked"], true) || out["image"] != "fnsys/dockhand:v1.0.51" || out["newer_image"] != "fnsys/dockhand:v1.0.53" || out["latest_version"] != "1.0.53" || out["container"] != "dockhand" || out["note"] != nil {
		t.Errorf("an update that is out = %v", out)
	}

	// made by a compose file: the file is what has to change
	dock.Answer(updateCheckCall, `{"updateAvailable":true,"currentImage":"fnsys/dockhand:latest","newImage":"fnsys/dockhand:latest","currentDigest":"sha256:aaa","newDigest":"sha256:bbb","containerName":"dockhand","isComposeManaged":true}`)
	if out = suite.Call(t, "server_update_check", nil); !acctest.IsBool(out["compose_managed"], true) || out["digest"] != "sha256:aaa" || out["newer_digest"] != "sha256:bbb" || !strings.Contains(acctest.Str(out["note"]), "made by a compose file") {
		t.Errorf("an update for one made by compose = %v", out)
	}

	// built on its host: nothing to compare with
	dock.Answer(updateCheckCall, `{"updateAvailable":false,"currentImage":"dockhand:dev","containerName":"dockhand","isComposeManaged":false,"isLocalImage":true}`)
	if out = suite.Call(t, "server_update_check", nil); acctest.BoolOf(out["update_available"]) || !acctest.IsBool(out["local_image"], true) || !strings.Contains(acctest.Str(out["note"]), "built on its host") {
		t.Errorf("one run from an image built on its host = %v", out)
	}

	// up to date: nothing to say
	dock.Answer(updateCheckCall, `{"updateAvailable":false,"currentImage":"fnsys/dockhand:v1.0.53","latestVersion":"1.0.53","containerName":"dockhand","isComposeManaged":true}`)
	if out = suite.Call(t, "server_update_check", nil); acctest.BoolOf(out["update_available"]) || !acctest.IsBool(out["checked"], true) || out["note"] != nil {
		t.Errorf("one that is up to date = %v", out)
	}
}

// A Dockhand that cannot look says so in an answer that otherwise reads as "no update": that is told apart.
func TestServerUpdateCheckThatCouldNotLook(t *testing.T) {
	t.Parallel()

	dock := dockhandtest.New(t)
	dock.Answer(updateCheckCall, updateNoSocket)
	suite := session(t, dock, Options{})

	out := suite.Call(t, "server_update_check", nil)
	if acctest.BoolOf(out["update_available"]) || acctest.BoolOf(out["checked"]) || !strings.Contains(acctest.Str(out["note"]), "was not given it") || !strings.Contains(acctest.Str(out["note"]), "ENOENT") {
		t.Errorf("a Dockhand with no socket = %v", out)
	}

	dock.Answer(updateCheckCall, `{"updateAvailable":false,"error":"Not running in Docker"}`)
	if out = suite.Call(t, "server_update_check", nil); acctest.BoolOf(out["checked"]) || out["note"] != "Dockhand could not look: Not running in Docker" {
		t.Errorf("a Dockhand that gives another reason = %v", out)
	}

	dock.Fail(updateCheckCall, http.StatusForbidden, "Admin access required")
	if msg := suite.CallErr(t, "server_update_check", nil); !strings.Contains(msg, "could not be asked whether an update is out") || !strings.Contains(msg, "the role of the user") {
		t.Errorf("an account that may not ask = %q", msg)
	}
}

func TestServerAboutTexts(t *testing.T) {
	t.Parallel()

	dock := dockhandtest.New(t)
	dock.Answer("GET /api/legal/license", `{"content":"DOCKHAND LICENSE\n\nPart A é Part B"}`)
	dock.Answer("GET /api/legal/privacy", `{"content":"DOCKHAND PRIVACY POLICY"}`)
	suite := session(t, dock, Options{})

	// the licence is the default, whole when it fits
	out := suite.Call(t, "server_about", nil)
	if out["what"] != "licence" || out["text"] != "DOCKHAND LICENSE\n\nPart A é Part B" || acctest.Num(t, out["characters"], "characters") != 33 || out["next"] != nil || out["note"] != nil {
		t.Errorf("the licence = %v", out)
	}

	// a part at a time, counted in characters and not bytes, with where to go on from
	out = suite.Call(t, "server_about", map[string]any{"what": "License", "offset": 18, "limit": 8})
	if out["text"] != "Part A é" || acctest.Num(t, out["next"], "next") != 26 || !strings.Contains(acctest.Str(out["note"]), "characters 18 to 26 of 33: ask again with offset 26") {
		t.Errorf("a part of the licence = %v", out)
	}
	if out = suite.Call(t, "server_about", map[string]any{"offset": 26}); out["text"] != " Part B" || out["next"] != nil {
		t.Errorf("the rest of it = %v", out)
	}
	if out = suite.Call(t, "server_about", map[string]any{"offset": 33}); out["text"] != nil || !strings.Contains(acctest.Str(out["note"]), "offset 33 is past its end") {
		t.Errorf("past its end = %v", out)
	}

	if out = suite.Call(t, "server_about", map[string]any{"what": " PRIVACY "}); out["what"] != "privacy" || out["text"] != "DOCKHAND PRIVACY POLICY" {
		t.Errorf("the privacy policy = %v", out)
	}

	for args, want := range map[string]map[string]any{
		`what is "terms": it is licence, privacy or dependencies`: {"what": "terms"},
		"offset and limit are not negative":                       {"offset": -1},
	} {
		if msg := suite.CallErr(t, "server_about", want); !strings.Contains(msg, args) {
			t.Errorf("server_about %v = %q, want %q", want, msg, args)
		}
	}
	if msg := suite.CallErr(t, "server_about", map[string]any{"limit": -1}); !strings.Contains(msg, "are not negative") {
		t.Errorf("a negative limit = %q", msg)
	}

	dock.Fail("GET /api/legal/license", http.StatusInternalServerError, "Failed to read")
	dock.Fail("GET /api/legal/privacy", http.StatusInternalServerError, "Failed to read")
	if msg := suite.CallErr(t, "server_about", nil); !strings.Contains(msg, "licence could not be read") {
		t.Errorf("a licence that cannot be read = %q", msg)
	}
	if msg := suite.CallErr(t, "server_about", map[string]any{"what": "privacy"}); !strings.Contains(msg, "privacy policy could not be read") {
		t.Errorf("a policy that cannot be read = %q", msg)
	}
}

func TestServerAboutDependencies(t *testing.T) {
	t.Parallel()

	dock := dockhandtest.New(t)
	dock.Answer("GET /api/dependencies", dependencies)
	suite := session(t, dock, Options{})

	// all of them by name whatever the case, and counted by licence, the most first
	out := suite.Call(t, "server_about", map[string]any{"what": "dependencies"})
	rows := acctest.Rows(t, out["dependencies"], "dependencies")
	if acctest.Num(t, out["total"], "total") != 5 || acctest.Num(t, out["matched"], "matched") != 5 || len(rows) != 5 || rows[0]["name"] != "@1password/sdk" || rows[0]["repository"] != nil || rows[2]["name"] != "Chownr" || rows[4]["name"] != "zod" || out["note"] != nil {
		t.Errorf("the dependencies = %v", out)
	}
	licences := acctest.Rows(t, out["licences"], "licences")
	if len(licences) != 4 || licences[0]["licence"] != "MIT" || acctest.Num(t, licences[0]["count"], "count") != 2 || licences[1]["licence"] != "(not stated)" || licences[2]["licence"] != "Apache-2.0" || licences[3]["licence"] != "ISC" {
		t.Errorf("counted by licence = %v", licences)
	}

	// narrowed by name and by licence, and cut at a limit
	if out = suite.Call(t, "server_about", map[string]any{"what": "dependencies", "find": "GRYPE"}); acctest.Num(t, out["matched"], "matched") != 1 || acctest.Rows(t, out["dependencies"], "dependencies")[0]["version"] != "v0.119.0" || acctest.Num(t, out["total"], "total") != 5 {
		t.Errorf("found by name = %v", out)
	}
	if out = suite.Call(t, "server_about", map[string]any{"what": "dependencies", "licence": "mit", "limit": 1}); acctest.Num(t, out["matched"], "matched") != 2 || len(acctest.Rows(t, out["dependencies"], "dependencies")) != 1 || !strings.Contains(acctest.Str(out["note"]), "the first 1 of 2 by name") {
		t.Errorf("under one licence, cut = %v", out)
	}
	if out = suite.Call(t, "server_about", map[string]any{"what": "dependencies", "find": "nothing-of-the-kind"}); out["matched"] != nil || out["dependencies"] != nil || !strings.Contains(acctest.Str(out["note"]), "none of the 5 match") {
		t.Errorf("none found = %v", out)
	}

	dock.Fail("GET /api/dependencies", http.StatusUnauthorized, "Not authenticated")
	if msg := suite.CallErr(t, "server_about", map[string]any{"what": "dependencies"}); !strings.Contains(msg, "built from could not be read") || !strings.Contains(msg, "DOCKHAND_TOKEN") {
		t.Errorf("dependencies refused = %q", msg)
	}
}
