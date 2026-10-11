package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// The images of the two canned hosts. On alpha: the four the containers were made from, one of them under two names; one nothing uses; and one with
// no name left. On beta, the one image its container runs, which alpha has too.
//
//nolint:gosec // G101: the token and the password in a label are made up, and there to be hidden
const (
	frontImage  = `{"id":"sha256:f00d","repoTags":["registry.example/front:1.2"],"tags":["registry.example/front:1.2"],"repoDigests":["registry.example/front@sha256:f00d"],"size":52000000,"virtualSize":52000000,"created":1791000000,"labels":{"org.opencontainers.image.title":"front","build.token":"not-a-real-token","org.opencontainers.image.source":"https://builder:not-a-real-password@git.example/front"},"containers":1}`
	backImage   = `{"id":"sha256:beef","repoTags":["registry.example/back:latest","registry.example/back:1.2"],"tags":["registry.example/back:latest","registry.example/back:1.2"],"repoDigests":["registry.example/back@sha256:beef"],"size":81000000,"virtualSize":81000000,"created":1791000100,"labels":{},"containers":1}`
	toolImage   = `{"id":"sha256:cafe","repoTags":["registry.example/tool:latest"],"tags":["registry.example/tool:latest"],"repoDigests":["registry.example/tool@sha256:cafe"],"size":6143972,"virtualSize":6143972,"created":1727386302,"labels":{},"containers":6}`
	jobImage    = `{"id":"sha256:d00d","repoTags":["registry.example/job:1"],"tags":["registry.example/job:1"],"repoDigests":[],"size":9000000,"virtualSize":9000000,"created":1791000200,"labels":{},"containers":1}`
	oldImage    = `{"id":"sha256:0ddba11000000000000000000000000000000000000000000000000000000000","repoTags":["registry.example/old:0.9"],"tags":["registry.example/old:0.9"],"repoDigests":[],"size":120000000,"virtualSize":120000000,"created":1700000000,"labels":{},"containers":0}`
	noNameImage = `{"id":"sha256:9999999999990000000000000000000000000000000000000000000000000000","repoTags":["<none>:<none>"],"tags":["<none>:<none>"],"repoDigests":["registry.example/front@sha256:9999"],"size":50000000,"virtualSize":50000000,"created":1790000000,"labels":{},"containers":0}`

	alphaImages = `[` + frontImage + `,` + backImage + `,` + toolImage + `,` + jobImage + `,` + oldImage + `,` + noNameImage + `]`
	betaImages  = `[{"id":"sha256:cafe","repoTags":["registry.example/tool:latest"],"tags":["registry.example/tool:latest"],"repoDigests":["registry.example/tool@sha256:cafe"],"size":6143972,"virtualSize":6143972,"created":1727386302,"labels":{},"containers":1}]`

	oldID = "sha256:0ddba11000000000000000000000000000000000000000000000000000000000"
)

// withImages is the two canned hosts with their images as well.
func withImages(t *testing.T) *dockhandtest.Server {
	t.Helper()

	dock := twoHosts(t)
	dock.AnswerWhen("GET /api/images", "env", "1", alphaImages)
	dock.AnswerWhen("GET /api/images", "env", "2", betaImages)

	return dock
}

func TestImageList(t *testing.T) {
	t.Parallel()

	out := session(t, withImages(t), Options{}).Call(t, "image_list", nil)
	rows := acctest.Rows(t, out["images"], "images")
	order := make([]string, 0, len(rows))
	for _, row := range rows {
		order = append(order, acctest.Str(row["env"])+" "+acctest.Str(row["id"]))
	}
	// by host, then by name, and the one with no name last
	if got := strings.Join(order, "|"); got != "alpha beef|alpha f00d|alpha d00d|alpha 0ddba1100000|alpha cafe|alpha 999999999999|beta cafe" {
		t.Errorf("the images, in order = %s", got)
	}
	if acctest.Num(t, out["total"], "total") != 7 || acctest.Num(t, out["unused"], "unused") != 2 || acctest.Decimal(t, out["total_mb"], "total_mb") != 324.3 || acctest.Decimal(t, out["unused_mb"], "unused_mb") != 170 || out["failed"] != nil || out["note"] != nil {
		t.Errorf("the counts = %v", out)
	}

	back := rows[0]
	if strings.Join(acctest.Strs(t, back["tags"], "tags"), "|") != "registry.example/back:1.2|registry.example/back:latest" || acctest.Decimal(t, back["size_mb"], "size_mb") != 81 || back["created"] != "2026-10-03T04:01:40Z" || acctest.Num(t, back["containers"], "containers") != 1 || back["untagged"] != nil {
		t.Errorf("an image under two names = %v", back)
	}
	if left := rows[5]; len(acctest.Strs(t, left["tags"], "tags")) != 0 || !acctest.IsBool(left["untagged"], true) || acctest.Num(t, left["containers"], "containers") != 0 {
		t.Errorf("an image with no name left = %v", left)
	}
}

func TestImageListNarrowed(t *testing.T) {
	t.Parallel()

	suite := session(t, withImages(t), Options{})

	ids := func(args map[string]any) string {
		rows := acctest.Rows(t, suite.Call(t, "image_list", args)["images"], "images")
		got := make([]string, 0, len(rows))
		for _, row := range rows {
			got = append(got, acctest.Str(row["env"])+" "+acctest.Str(row["id"]))
		}

		return strings.Join(got, "|")
	}

	for name, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"one host, by name":     {map[string]any{"env": "beta"}, "beta cafe"},
		"one host, by number":   {map[string]any{"env": 2}, "beta cafe"},
		"part of a name":        {map[string]any{"name": "TOOL"}, "alpha cafe|beta cafe"},
		"what nothing uses":     {map[string]any{"unused": true}, "alpha 0ddba1100000|alpha 999999999999"},
		"both, matching none":   {map[string]any{"unused": true, "name": "tool"}, ""},
		"no more than a number": {map[string]any{"limit": 2}, "alpha beef|alpha f00d"},
	} {
		if got := ids(tc.args); got != tc.want {
			t.Errorf("%s = %q, want %q", name, got, tc.want)
		}
	}

	// cut at the limit, the counts are still of everything that matched, and it says so
	out := suite.Call(t, "image_list", map[string]any{"limit": 2})
	if acctest.Num(t, out["total"], "total") != 7 || acctest.Num(t, out["unused"], "unused") != 2 || !strings.Contains(acctest.Str(out["note"]), "7 images matched and the first 2 are listed") {
		t.Errorf("cut at a limit = %v", out)
	}
	if msg := suite.CallErr(t, "image_list", map[string]any{"limit": -1}); !strings.Contains(msg, "limit is -1") {
		t.Errorf("a limit below none = %q", msg)
	}
	if msg := suite.CallErr(t, "image_list", map[string]any{"env": "gamma"}); !strings.Contains(msg, `no environment is named or numbered "gamma"`) {
		t.Errorf("a host there is none of = %q", msg)
	}
}

func TestImageListWithAHostDown(t *testing.T) {
	t.Parallel()

	dock := withImages(t)
	dock.FailWhen("GET /api/images", "env", "2", http.StatusInternalServerError, "Failed to connect to the agent")
	suite := session(t, dock, Options{})

	out := suite.Call(t, "image_list", nil)
	failed := acctest.Rows(t, out["failed"], "failed")
	if len(failed) != 1 || failed[0]["env"] != "beta" || !strings.Contains(acctest.Str(failed[0]["error"]), "Failed to connect to the agent") || acctest.Num(t, out["total"], "total") != 6 || !strings.Contains(acctest.Str(out["note"]), "1 of the 2 environments could not be asked") {
		t.Errorf("with one host down = %v", out)
	}

	dock.FailWhen("GET /api/images", "env", "1", http.StatusUnauthorized, "Unauthorized")
	if msg := suite.CallErr(t, "image_list", nil); !strings.Contains(msg, "no environment could be asked for its images") || !strings.Contains(msg, "DOCKHAND_TOKEN") {
		t.Errorf("with both down = %q", msg)
	}
}

func TestImageGet(t *testing.T) {
	t.Parallel()

	dock := withImages(t)
	dock.Answer("GET /api/images/sha256:f00d/history", `[{"Comment":"","Created":1791000000,"CreatedBy":" CMD [\"serve\"] ","Id":"sha256:f00d","Size":0,"Tags":["registry.example/front:1.2"]},{"Comment":"","Created":1790990000,"CreatedBy":"COPY /app /app","Id":"<missing>","Size":12400000,"Tags":null},{"Comment":"","Created":0,"CreatedBy":"","Id":"<missing>","Size":39600000,"Tags":null}]`)
	suite := session(t, dock, Options{})

	// by the end of its name: the one image that answers to it, on the one host that has it
	out := suite.Call(t, "image_get", map[string]any{"image": "front"})
	if out["env"] != "alpha" || out["id"] != "f00d" || out["full_id"] != "sha256:f00d" || strings.Join(acctest.Strs(t, out["tags"], "tags"), "|") != "registry.example/front:1.2" || strings.Join(acctest.Strs(t, out["digests"], "digests"), "|") != "registry.example/front@sha256:f00d" || acctest.Decimal(t, out["size_mb"], "size_mb") != 52 || strings.Join(acctest.Strs(t, out["used_by"], "used_by"), "|") != "web-front-1 (running)" {
		t.Errorf("image_get = %v", out)
	}
	if made := dock.Last("GET /api/images/sha256:f00d/history"); made.Query.Get("env") != "1" {
		t.Errorf("its history was asked on environment %q", made.Query.Get("env"))
	}

	// a label named as a secret is hidden whole, a password in an address is hidden alone, and it is said that something was
	labels := acctest.Object(t, out["labels"], "labels")
	if labels["build.token"] != masked || labels["org.opencontainers.image.source"] != "https://builder:"+masked+"@git.example/front" || labels["org.opencontainers.image.title"] != "front" || !strings.Contains(acctest.Str(out["note"]), "2 secrets are hidden") || strings.Contains(flat(out), "not-a-real") {
		t.Errorf("its labels = %v, note %v", labels, out["note"])
	}

	// and shown when asked for
	if shown := acctest.Object(t, suite.Call(t, "image_get", map[string]any{"image": "front", "reveal_secrets": true})["labels"], "labels"); shown["build.token"] != "not-a-real-token" {
		t.Errorf("its labels, revealed = %v", shown)
	}

	layers := acctest.Rows(t, out["layers"], "layers")
	if len(layers) != 3 || layers[0]["created_by"] != `CMD ["serve"]` || acctest.Decimal(t, layers[0]["size_mb"], "size_mb") != 0 || layers[0]["created"] != "2026-10-03T04:00:00Z" || acctest.Decimal(t, layers[1]["size_mb"], "size_mb") != 12.4 || layers[2]["created"] != nil || layers[2]["created_by"] != nil {
		t.Errorf("its layers = %v", layers)
	}

	// what describes an image that is there failing is said, and the rest is still given
	dock.Fail("GET /api/images/sha256:f00d/history", http.StatusInternalServerError, "Failed to get image history")
	dock.FailWhen("GET /api/containers", "env", "1", http.StatusInternalServerError, "Failed to list containers")
	out = suite.Call(t, "image_get", map[string]any{"image": "registry.example/front:1.2", "env": "alpha"})
	if out["id"] != "f00d" || len(acctest.Rows(t, out["layers"], "layers")) != 0 || !strings.Contains(acctest.Str(out["note"]), "how it was built could not be read: ") || !strings.Contains(acctest.Str(out["note"]), "which containers use it could not be read: ") {
		t.Errorf("with its history and users unreadable = %v", out)
	}
}

// An image is found by a name and tag in full before a name alone, and by a name before the start of an id; and what answers to more than one is
// refused with each named, never guessed at.
func TestImageFinding(t *testing.T) {
	t.Parallel()

	dock := withImages(t)
	dock.Answer("GET /api/images/sha256:beef/history", `[]`)
	dock.Answer("GET /api/images/"+oldID+"/history", `[]`)
	dock.Answer("GET /api/images/sha256:cafe/history", `[]`)
	suite := session(t, dock, Options{})

	for name, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"a name and tag in full":           {map[string]any{"image": "registry.example/back:1.2"}, "alpha beef"},
		"a name alone, whatever its tags":  {map[string]any{"image": "registry.example/back"}, "alpha beef"},
		"the end of a name with its tag":   {map[string]any{"image": "old:0.9"}, "alpha 0ddba1100000"},
		"the start of an id":               {map[string]any{"image": "0ddb"}, "alpha 0ddba1100000"},
		"the start of an id as it is led":  {map[string]any{"image": "sha256:0ddba11"}, "alpha 0ddba1100000"},
		"one on two hosts, with which one": {map[string]any{"image": "registry.example/tool:latest", "env": "beta"}, "beta cafe"},
	} {
		out := suite.Call(t, "image_get", tc.args)
		if got := acctest.Str(out["env"]) + " " + acctest.Str(out["id"]); got != tc.want {
			t.Errorf("%s found %s, want %s", name, got, tc.want)
		}
	}

	for name, tc := range map[string]struct {
		args map[string]any
		want []string
	}{
		"none":                     {map[string]any{"image": " "}, []string{"image is required"}},
		"one on two hosts":         {map[string]any{"image": "tool"}, []string{`"tool" is more than one image`, "registry.example/tool:latest (cafe) on alpha; registry.example/tool:latest (cafe) on beta", "say which with env"}},
		"an id too short to mean":  {map[string]any{"image": "0dd"}, []string{`no image goes by "0dd"`, "on alpha, beta", "image_list"}},
		"one that is not there":    {map[string]any{"image": "registry.example/nope:1", "env": "alpha"}, []string{`no image goes by "registry.example/nope:1"`, "on alpha:"}},
		"a host there is none of":  {map[string]any{"image": "tool", "env": "gamma"}, []string{`no environment is named or numbered "gamma"`}},
		"an image with no name":    {map[string]any{"image": "<none>:<none>"}, []string{`no image goes by "<none>:<none>"`}},
		"a name that is part only": {map[string]any{"image": "example/back"}, []string{`no image goes by "example/back"`}},
	} {
		msg := suite.CallErr(t, "image_get", tc.args)
		for _, want := range tc.want {
			if !strings.Contains(msg, want) {
				t.Errorf("%s = %q, want %q in it", name, msg, want)
			}
		}
	}

	// not found, with a host that could not be asked: it may be there, and that is said
	dock.FailWhen("GET /api/images", "env", "2", http.StatusInternalServerError, "Failed to connect to the agent")
	if msg := suite.CallErr(t, "image_get", map[string]any{"image": "registry.example/nope:1"}); !strings.Contains(msg, "not every environment could be asked") || !strings.Contains(msg, "beta: ") {
		t.Errorf("not found with a host down = %q", msg)
	}
}

// pulled has the canned alpha list the tool image under another id from when a pull is asked for: how a pull that brought a newer image comes to be
// read back.
func pulled(dock *dockhandtest.Server) {
	dock.Then("POST /api/images/pull", func(dockhandtest.Request) {
		dock.AnswerWhen("GET /api/images", "env", "1", strings.Replace(alphaImages, toolImage, strings.Replace(toolImage, `"id":"sha256:cafe"`, `"id":"sha256:fresh"`, 1), 1))
	})
}

func TestImagePull(t *testing.T) {
	t.Parallel()

	const call = "POST /api/images/pull"

	// started as a job, waited on, and read back: the host has another image under the name than it had
	dock := withImages(t)
	dock.Answer(call, `{"jobId":"j1"}`)
	dock.Answer("GET /api/jobs/j1", pullRunning)
	asked := 0
	dock.Then("GET /api/jobs/j1", func(dockhandtest.Request) {
		asked++
		if asked == 2 {
			dock.Answer("GET /api/jobs/j1", pullDone)
		}
	})
	pulled(dock)

	out := session(t, dock, quick).Call(t, "image_pull", map[string]any{"env": "alpha", "image": " registry.example/tool "})
	if out["env"] != "alpha" || out["image"] != "registry.example/tool" || out["was"] != "cafe" || out["is"] != "fresh" || !acctest.IsBool(out["changed"], true) || !acctest.IsBool(out["running"], false) || out["job"] != nil || out["dry_run"] != nil || !strings.Contains(acctest.Str(out["note"]), "containers made from the older image go on running it") {
		t.Errorf("a pull that brought a newer image = %v", out)
	}
	if said := acctest.Strs(t, out["progress"], "progress"); len(said) != 3 || said[1] != "0a1b2c3d4e5f: Pull complete" {
		t.Errorf("its progress = %q", said)
	}
	made := dock.Last(call)
	if made.Query.Get("env") != "1" || made.Body != `{"image":"registry.example/tool"}` || made.Accept != "*/*" {
		t.Errorf("the pull was asked on environment %q with %s, accepting %q: want it asked as a job", made.Query.Get("env"), made.Body, made.Accept)
	}

	// the newest already there: nothing changed, and that is said
	dock = withImages(t)
	dock.Answer(call, `{"jobId":"j1"}`)
	dock.Answer("GET /api/jobs/j1", pullDone)
	if out = session(t, dock, quick).Call(t, "image_pull", map[string]any{"env": 1, "image": "registry.example/tool:latest"}); out["was"] != "cafe" || out["is"] != "cafe" || !acctest.IsBool(out["changed"], false) || !strings.Contains(acctest.Str(out["note"]), "already had the newest image") {
		t.Errorf("a pull of what was there = %v", out)
	}

	// one the host had none of: there is no older image to speak of
	dock = withImages(t)
	dock.Answer(call, `{"jobId":"j1"}`)
	dock.Answer("GET /api/jobs/j1", pullDone)
	dock.Then(call, func(dockhandtest.Request) {
		dock.AnswerWhen("GET /api/images", "env", "2", `[`+toolImage+`,`+jobImage+`]`)
	})
	if out = session(t, dock, quick).Call(t, "image_pull", map[string]any{"env": "beta", "image": "registry.example/job:1"}); out["env"] != "beta" || out["was"] != nil || out["is"] != "d00d" || !acctest.IsBool(out["changed"], true) || out["note"] != nil {
		t.Errorf("a pull of what was not there = %v", out)
	}

	// a Dockhand that does the work first and answers how it went is taken at its word: no job is asked after
	dock = withImages(t)
	dock.Answer(call, `{"status":"complete"}`)
	pulled(dock)
	if out = session(t, dock, quick).Call(t, "image_pull", map[string]any{"env": "alpha", "image": "registry.example/tool:latest"}); out["is"] != "fresh" || !acctest.IsBool(out["changed"], true) || !acctest.IsBool(out["running"], false) || out["progress"] != nil {
		t.Errorf("a pull answered without a job = %v", out)
	}
}

// A pull still going when the wait is over is left running and said to be, with the job to follow; nothing is read back as though it were done.
func TestImagePullLeftRunning(t *testing.T) {
	t.Parallel()

	dock := withImages(t)
	dock.Answer("POST /api/images/pull", `{"jobId":"j1"}`)
	dock.Answer("GET /api/jobs/j1", pullRunning)

	out := session(t, dock, quick).Call(t, "image_pull", map[string]any{"env": "alpha", "image": "registry.example/tool:latest", "wait": 0})
	if !acctest.IsBool(out["running"], true) || out["job"] != "j1" || out["was"] != "cafe" || out["is"] != nil || !acctest.IsBool(out["changed"], false) || !strings.Contains(acctest.Str(out["note"]), "still running after 0 seconds") || !strings.Contains(acctest.Str(out["note"]), "job_get with job j1") {
		t.Errorf("a pull left running = %v", out)
	}
	if said := acctest.Strs(t, out["progress"], "progress"); len(said) != 2 {
		t.Errorf("its progress = %q", said)
	}
	if len(dock.Requests("GET /api/images")) != 1 {
		t.Errorf("the images were listed %d times, want once: there is nothing to read back yet", len(dock.Requests("GET /api/images")))
	}
}

func TestImagePullThatGoesWrong(t *testing.T) {
	t.Parallel()

	const call = "POST /api/images/pull"
	dock := withImages(t)
	suite := session(t, dock, quick)
	args := map[string]any{"env": "alpha", "image": "registry.example/tool:latest"}

	// the job ends in an error: why, in Docker's words
	dock.Answer(call, `{"jobId":"j1"}`)
	dock.Answer("GET /api/jobs/j1", pullFailed)
	if msg := suite.CallErr(t, "image_pull", args); !strings.Contains(msg, "registry.example/tool:latest was not pulled to alpha: Failed to pull image: failed to resolve reference") || strings.Contains(msg, `{"message"`) {
		t.Errorf("a pull whose job failed = %q", msg)
	}
	// the work done first and failed, which Dockhand answers with a 200
	dock.Answer(call, `{"status":"error","error":"Error: Failed to pull image: {\"message\":\"pull access denied\"}\n"}`)
	if msg := suite.CallErr(t, "image_pull", args); !strings.Contains(msg, "was not pulled to alpha: Failed to pull image: pull access denied") {
		t.Errorf("a pull that failed without a job = %q", msg)
	}
	dock.Answer(call, `{"status":"error"}`)
	if msg := suite.CallErr(t, "image_pull", args); !strings.Contains(msg, "Dockhand gave no reason") {
		t.Errorf("a pull that failed without a word = %q", msg)
	}
	// refused outright
	dock.Fail(call, http.StatusForbidden, "Permission denied")
	if msg := suite.CallErr(t, "image_pull", args); !strings.Contains(msg, "was not pulled to alpha") || !strings.Contains(msg, "the role of the user") {
		t.Errorf("a pull the account may not make = %q", msg)
	}
	// the job gone before it could be asked
	dock.Answer(call, `{"jobId":"gone"}`)
	dock.Fail("GET /api/jobs/gone", http.StatusNotFound, "Job not found")
	if msg := suite.CallErr(t, "image_pull", args); !strings.Contains(msg, "no job gone is known to Dockhand") {
		t.Errorf("a pull whose job is gone = %q", msg)
	}

	// done, and the host does not list it: said, rather than called pulled
	dock.Answer(call, `{"status":"complete"}`)
	dock.Then(call, func(dockhandtest.Request) { dock.AnswerWhen("GET /api/images", "env", "1", `[]`) })
	if out := suite.Call(t, "image_pull", args); out["is"] != nil || !strings.Contains(acctest.Str(out["note"]), "the host lists no image under that name") {
		t.Errorf("a pull done and not listed = %v", out)
	}
	// done, and the host cannot be read afterwards
	dock.Then(call, func(dockhandtest.Request) {
		dock.FailWhen("GET /api/images", "env", "1", http.StatusInternalServerError, "Failed to list images")
	})
	if out := suite.Call(t, "image_pull", args); !strings.Contains(acctest.Str(out["note"]), "it was pulled, and the images could not be listed afterwards") {
		t.Errorf("a pull done and not read back = %v", out)
	}
	// and one that cannot be read before is not begun
	dock.Then(call, nil)
	pulls := len(dock.Requests(call))
	if msg := suite.CallErr(t, "image_pull", args); !strings.Contains(msg, "could not be listed, so nothing was pulled") || len(dock.Requests(call)) != pulls {
		t.Errorf("a pull to a host that cannot be read = %q, after %d pulls more", msg, len(dock.Requests(call))-pulls)
	}
}

func TestImagePullRefusalsAndDryRun(t *testing.T) {
	t.Parallel()

	dock := withImages(t)
	suite := session(t, dock, quick)

	for name, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"no image":          {map[string]any{"env": "alpha", "image": ""}, `image is ""`},
		"two words":         {map[string]any{"env": "alpha", "image": "nginx 1.27"}, `image is "nginx 1.27"`},
		"no host":           {map[string]any{"image": "nginx:1.27"}, "env is required"},
		"a host of no name": {map[string]any{"env": "gamma", "image": "nginx:1.27"}, `no environment is named or numbered "gamma"`},
		"a wait too long":   {map[string]any{"env": "alpha", "image": "nginx:1.27", "wait": 900}, "wait is 900"},
	} {
		if msg := suite.CallErr(t, "image_pull", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", name, msg, tc.want)
		}
	}

	out := suite.Call(t, "image_pull", map[string]any{"env": "alpha", "image": "registry.example/tool", "dry_run": true})
	if !acctest.IsBool(out["dry_run"], true) || out["was"] != "cafe" || out["is"] != "cafe" || !acctest.IsBool(out["changed"], false) || !strings.Contains(acctest.Str(out["note"]), "nothing was pulled") {
		t.Errorf("a dry run = %v", out)
	}
	if out = suite.Call(t, "image_pull", map[string]any{"env": "alpha", "image": "registry.example/new:2", "dry_run": true}); out["was"] != nil || out["is"] != nil {
		t.Errorf("a dry run of what is not there = %v", out)
	}
	if made := dock.Requests("POST /api/images/pull"); len(made) != 0 {
		t.Errorf("a refusal or a dry run pulled all the same: %v", made)
	}
}

func TestImageTag(t *testing.T) {
	t.Parallel()

	const call = "POST /api/images/sha256:d00d/tag"
	renamed := func(dock *dockhandtest.Server, tags string) {
		dock.Then(call, func(dockhandtest.Request) {
			dock.AnswerWhen("GET /api/images", "env", "1", strings.Replace(alphaImages, jobImage, strings.Replace(jobImage, `"repoTags":["registry.example/job:1"]`, `"repoTags":[`+tags+`]`, 1), 1))
		})
	}

	// a name with no tag is its latest, and the image keeps the name it had
	dock := withImages(t)
	dock.Answer(call, `{"success":true}`)
	renamed(dock, `"registry.example/job:1","registry.example/team/job:latest"`)
	out := session(t, dock, everything).Call(t, "image_tag", map[string]any{"image": "job", "as": "registry.example/team/job"})
	if out["env"] != "alpha" || out["id"] != "d00d" || !acctest.IsBool(out["added"], true) || strings.Join(acctest.Strs(t, out["tags"], "tags"), "|") != "registry.example/job:1|registry.example/team/job:latest" || out["note"] != nil || out["dry_run"] != nil {
		t.Errorf("image_tag = %v", out)
	}
	if made := dock.Last(call); made.Query.Get("env") != "1" || made.Body != `{"repo":"registry.example/team/job","tag":"latest"}` {
		t.Errorf("the tag was asked on environment %q with %s", made.Query.Get("env"), made.Body)
	}

	// a registry with a port keeps its colon, and the tag is what follows the last
	dock = withImages(t)
	dock.Answer(call, `{"success":true}`)
	renamed(dock, `"registry.example/job:1","registry.example:5000/job:v2"`)
	session(t, dock, everything).Call(t, "image_tag", map[string]any{"image": "job", "as": "registry.example:5000/job:v2"})
	if made := dock.Last(call); made.Body != `{"repo":"registry.example:5000/job","tag":"v2"}` {
		t.Errorf("a name with a port was sent as %s", made.Body)
	}

	// a name another image goes by is taken from it, and that is said
	dock = withImages(t)
	dock.Answer(call, `{"success":true}`)
	renamed(dock, `"registry.example/job:1","registry.example/old:0.9"`)
	if out = session(t, dock, everything).Call(t, "image_tag", map[string]any{"image": "job", "as": "registry.example/old:0.9"}); !acctest.IsBool(out["added"], true) || !strings.Contains(acctest.Str(out["note"]), "registry.example/old:0.9 was the name of another image, 0ddba1100000, which no longer goes by it") {
		t.Errorf("a name taken from another image = %v", out)
	}
}

func TestImageTagThatDoesNothingOrGoesWrong(t *testing.T) {
	t.Parallel()

	const call = "POST /api/images/sha256:d00d/tag"
	dock := withImages(t)
	suite := session(t, dock, everything)

	// what is already so is left so, and nothing is asked of Dockhand
	out := suite.Call(t, "image_tag", map[string]any{"image": "job", "as": "registry.example/job:1"})
	if !acctest.IsBool(out["added"], false) || !strings.Contains(acctest.Str(out["note"]), "already goes by that name") {
		t.Errorf("a name the image has = %v", out)
	}
	out = suite.Call(t, "image_tag", map[string]any{"image": "job", "as": "registry.example/team/job:2", "dry_run": true})
	if !acctest.IsBool(out["dry_run"], true) || !acctest.IsBool(out["added"], false) || out["note"] != "nothing was done: without dry_run the image is given the name registry.example/team/job:2" {
		t.Errorf("a dry run = %v", out)
	}
	out = suite.Call(t, "image_tag", map[string]any{"image": "job", "as": "registry.example/old:0.9", "dry_run": true})
	if !strings.Contains(acctest.Str(out["note"]), "it is the name of another image, 0ddba1100000, which would no longer go by it") {
		t.Errorf("a dry run that would take a name = %v", out)
	}
	if made := dock.Requests(call); len(made) != 0 {
		t.Errorf("nothing was to be done, and Dockhand was asked: %v", made)
	}

	for name, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"no new name":      {map[string]any{"image": "job", "as": " "}, `as is " "`},
		"a digest":         {map[string]any{"image": "job", "as": "registry.example/job@sha256:d00d"}, "as is "},
		"no image":         {map[string]any{"image": "", "as": "a/b:1"}, "image is required"},
		"an image of none": {map[string]any{"image": "nope", "as": "a/b:1"}, `no image goes by "nope"`},
	} {
		if msg := suite.CallErr(t, "image_tag", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", name, msg, tc.want)
		}
	}

	args := map[string]any{"image": "job", "as": "registry.example/team/job:2"}
	dock.Fail(call, http.StatusInternalServerError, "Failed to tag image")
	if msg := suite.CallErr(t, "image_tag", args); !strings.Contains(msg, "d00d was not given the name registry.example/team/job:2") || !strings.Contains(msg, "Failed to tag image") {
		t.Errorf("a tag Dockhand fails = %q", msg)
	}
	dock.Answer(call, `{"success":false}`)
	if msg := suite.CallErr(t, "image_tag", args); !strings.Contains(msg, "without its being said to have succeeded") {
		t.Errorf("a tag answered without success = %q", msg)
	}
	// answered as done, and the host does not list it under the name
	dock.Answer(call, `{"success":true}`)
	if msg := suite.CallErr(t, "image_tag", args); !strings.Contains(msg, "answered as done, and the host does not list it under that name") {
		t.Errorf("a tag that did not take = %q", msg)
	}
	// done, and the host cannot be read afterwards
	dock.Then(call, func(dockhandtest.Request) {
		dock.FailWhen("GET /api/images", "env", "1", http.StatusInternalServerError, "Failed to list images")
	})
	if out = suite.Call(t, "image_tag", args); !acctest.IsBool(out["added"], true) || !strings.Contains(acctest.Str(out["note"]), "it was tagged, and the images could not be listed afterwards") {
		t.Errorf("a tag done and not read back = %v", out)
	}
}

func TestImageDelete(t *testing.T) {
	t.Parallel()

	call := "DELETE /api/images/" + oldID
	without := func(dock *dockhandtest.Server, call, image string) {
		dock.Then(call, func(dockhandtest.Request) {
			dock.AnswerWhen("GET /api/images", "env", "1", strings.Replace(strings.Replace(alphaImages, image+",", "", 1), ","+image, "", 1))
		})
	}

	// without confirm: what would go, and nothing is asked of Dockhand
	dock := withImages(t)
	suite := session(t, dock, everything)
	out := suite.Call(t, "image_delete", map[string]any{"image": "old"})
	if !acctest.IsBool(out["removed"], false) || out["env"] != "alpha" || out["id"] != "0ddba1100000" || acctest.Decimal(t, out["freed_mb"], "freed_mb") != 120 || len(acctest.Strs(t, out["used_by"], "used_by")) != 0 || strings.Join(acctest.Strs(t, out["would_go"], "would_go"), "|") != "the image 0ddba1100000 on alpha|its name registry.example/old:0.9" || !strings.Contains(acctest.Str(out["note"]), "call again with confirm") {
		t.Errorf("without confirm = %v", out)
	}
	if made := dock.Requests(call); len(made) != 0 {
		t.Errorf("without confirm, Dockhand was asked: %v", made)
	}

	// with it: removed, not forced since nothing needs it, and read back as gone
	dock.Answer(call, `{"success":true}`)
	without(dock, call, oldImage)
	out = suite.Call(t, "image_delete", map[string]any{"image": "old", "confirm": true})
	if !acctest.IsBool(out["removed"], true) || out["would_go"] != nil || out["note"] != nil {
		t.Errorf("with confirm = %v", out)
	}
	if made := dock.Last(call); made.Query.Get("env") != "1" || made.Query.Get("force") != "false" {
		t.Errorf("the removal was asked on environment %q with force %q", made.Query.Get("env"), made.Query.Get("force"))
	}

	// an image a container was made from is refused, with the containers named, until force says so
	dock = withImages(t)
	suite = session(t, dock, everything)
	if msg := suite.CallErr(t, "image_delete", map[string]any{"image": "job", "confirm": true}); !strings.Contains(msg, "registry.example/job:1 is the image of 1 containers on alpha (done (exited))") || !strings.Contains(msg, "give force") {
		t.Errorf("an image in use = %q", msg)
	}
	out = suite.Call(t, "image_delete", map[string]any{"image": "job", "force": true})
	if !acctest.IsBool(out["removed"], false) || strings.Join(acctest.Strs(t, out["used_by"], "used_by"), "|") != "done (exited)" || !strings.Contains(acctest.Str(out["note"]), "the 1 containers made from it would be left without their image") {
		t.Errorf("an image in use, forced and not confirmed = %v", out)
	}
	dock.Answer("DELETE /api/images/sha256:d00d", `{"success":true}`)
	without(dock, "DELETE /api/images/sha256:d00d", jobImage)
	if out = suite.Call(t, "image_delete", map[string]any{"image": "job", "force": true, "confirm": true}); !acctest.IsBool(out["removed"], true) || dock.Last("DELETE /api/images/sha256:d00d").Query.Get("force") != "true" {
		t.Errorf("an image in use, forced = %v", out)
	}

	// one under two names needs Dockhand's force for that alone, and both names are said to go
	dock = withImages(t)
	dock.AnswerWhen("GET /api/containers", "env", "1", `[]`)
	dock.Answer("DELETE /api/images/sha256:beef", `{"success":true}`)
	without(dock, "DELETE /api/images/sha256:beef", backImage)
	suite = session(t, dock, everything)
	if out = suite.Call(t, "image_delete", map[string]any{"image": "back:1.2"}); strings.Join(acctest.Strs(t, out["would_go"], "would_go"), "|") != "the image beef on alpha|its name registry.example/back:1.2|its name registry.example/back:latest" {
		t.Errorf("an image under two names, without confirm = %v", out)
	}
	if out = suite.Call(t, "image_delete", map[string]any{"image": "back:1.2", "confirm": true}); !acctest.IsBool(out["removed"], true) || dock.Last("DELETE /api/images/sha256:beef").Query.Get("force") != "true" {
		t.Errorf("an image under two names = %v", out)
	}
}

func TestImageDeleteThatGoesWrong(t *testing.T) {
	t.Parallel()

	call := "DELETE /api/images/" + oldID
	dock := withImages(t)
	suite := session(t, dock, everything)
	args := map[string]any{"image": "old", "confirm": true}

	dock.Fail(call, http.StatusConflict, "Cannot delete image: it is being used by a running container. Stop the container first.")
	if msg := suite.CallErr(t, "image_delete", args); !strings.Contains(msg, "0ddba1100000 was not removed") || !strings.Contains(msg, "Stop the container first") {
		t.Errorf("a removal Dockhand refuses = %q", msg)
	}
	dock.Answer(call, `{"success":false}`)
	if msg := suite.CallErr(t, "image_delete", args); !strings.Contains(msg, "without its being said to have succeeded") {
		t.Errorf("a removal answered without success = %q", msg)
	}
	dock.Answer(call, `{"success":true}`)
	if msg := suite.CallErr(t, "image_delete", args); !strings.Contains(msg, "answered as done, and it is still listed") {
		t.Errorf("a removal that did not take = %q", msg)
	}
	dock.Then(call, func(dockhandtest.Request) {
		dock.FailWhen("GET /api/images", "env", "1", http.StatusInternalServerError, "Failed to list images")
	})
	if out := suite.Call(t, "image_delete", args); !acctest.IsBool(out["removed"], false) || !strings.Contains(acctest.Str(out["note"]), "it was removed, and the images could not be listed afterwards") {
		t.Errorf("a removal done and not read back = %v", out)
	}

	// which containers use it cannot be said, so it is not removed
	dock = withImages(t)
	dock.FailWhen("GET /api/containers", "env", "1", http.StatusInternalServerError, "Failed to list containers")
	if msg := session(t, dock, everything).CallErr(t, "image_delete", args); !strings.Contains(msg, "which containers use 0ddba1100000 could not be read, so it was not removed") || len(dock.Requests(call)) != 0 {
		t.Errorf("a removal with the containers unreadable = %q", msg)
	}
	if msg := session(t, dock, everything).CallErr(t, "image_delete", map[string]any{"image": " ", "confirm": true}); !strings.Contains(msg, "image is required") {
		t.Errorf("no image = %q", msg)
	}
}

func TestImagePrune(t *testing.T) {
	t.Parallel()

	const call = "POST /api/prune/images"

	// without confirm: the ones with no name left, and nothing is asked of Dockhand
	dock := withImages(t)
	suite := session(t, dock, quick)
	out := suite.Call(t, "image_prune", map[string]any{"env": "alpha"})
	if strings.Join(acctest.Strs(t, out["would_go"], "would_go"), "|") != "999999999999" || acctest.Decimal(t, out["freed_mb"], "freed_mb") != 50 || len(acctest.Strs(t, out["removed"], "removed")) != 0 || !strings.Contains(acctest.Str(out["note"]), "call again with confirm") {
		t.Errorf("without confirm = %v", out)
	}
	// with all: every one nothing uses, and the spare name of one in use, which is taken off it
	out = suite.Call(t, "image_prune", map[string]any{"env": "alpha", "all": true})
	if got := strings.Join(acctest.Strs(t, out["would_go"], "would_go"), "|"); got != "999999999999|registry.example/old:0.9|the name registry.example/back:latest, and not its image, which is in use under another name" || acctest.Decimal(t, out["freed_mb"], "freed_mb") != 170 {
		t.Errorf("without confirm, all = %q, %v", got, out["freed_mb"])
	}
	if made := dock.Requests(call); len(made) != 0 {
		t.Errorf("without confirm, Dockhand was asked: %v", made)
	}

	// with it, as a job: what Docker says went, by the names it was listed under
	dock.Answer(call, `{"jobId":"j2"}`)
	dock.Answer("GET /api/jobs/j2", `{"id":"j2","status":"done","lines":[{"event":"result","data":{}}],"result":{"success":true,"result":{"ImagesDeleted":[{"Untagged":"registry.example/old:0.9"},{"Deleted":"`+oldID+`"},{"Deleted":"sha256:1a7e4000000000000000000000000000000000000000000000000000000000000"},{"Deleted":"sha256:9999999999990000000000000000000000000000000000000000000000000000"},{"Untagged":"registry.example/back:latest"}],"SpaceReclaimed":163200000}}}`)
	out = suite.Call(t, "image_prune", map[string]any{"env": "alpha", "all": true, "confirm": true})
	if strings.Join(acctest.Strs(t, out["removed"], "removed"), "|") != "999999999999|registry.example/old:0.9" || strings.Join(acctest.Strs(t, out["untagged"], "untagged"), "|") != "registry.example/back:latest" || acctest.Decimal(t, out["freed_mb"], "freed_mb") != 163.2 || out["would_go"] != nil || !acctest.IsBool(out["running"], false) || out["note"] != nil {
		t.Errorf("with confirm, all = %v", out)
	}
	if made := dock.Last(call); made.Query.Get("env") != "1" || made.Query.Get("dangling") != "false" || made.Accept != "*/*" {
		t.Errorf("the pruning was asked on environment %q with dangling %q, accepting %q", made.Query.Get("env"), made.Query.Get("dangling"), made.Accept)
	}

	// left as it is, only the ones with no name are asked for; and a Dockhand that does the work first is taken at its word
	dock.Answer(call, `{"success":true,"result":{"ImagesDeleted":[{"Deleted":"sha256:9999999999990000000000000000000000000000000000000000000000000000"}],"SpaceReclaimed":50000000}}`)
	out = suite.Call(t, "image_prune", map[string]any{"env": "alpha", "confirm": true})
	if strings.Join(acctest.Strs(t, out["removed"], "removed"), "|") != "999999999999" || out["untagged"] != nil || acctest.Decimal(t, out["freed_mb"], "freed_mb") != 50 || dock.Last(call).Query.Get("dangling") != "true" {
		t.Errorf("with confirm = %v", out)
	}
	// nothing went
	dock.Answer(call, `{"success":true,"result":{"ImagesDeleted":null,"SpaceReclaimed":0}}`)
	if out = suite.Call(t, "image_prune", map[string]any{"env": "alpha", "confirm": true}); len(acctest.Strs(t, out["removed"], "removed")) != 0 || !strings.Contains(acctest.Str(out["note"]), "nothing was removed") {
		t.Errorf("a pruning that took nothing = %v", out)
	}
}

func TestImagePruneWithNothingToDoOrGoingWrong(t *testing.T) {
	t.Parallel()

	const call = "POST /api/prune/images"
	dock := withImages(t)
	suite := session(t, dock, quick)

	// a host with nothing this would take says so, and what would take more
	out := suite.Call(t, "image_prune", map[string]any{"env": "beta"})
	if out["would_go"] != nil || !strings.Contains(acctest.Str(out["note"]), "no image on the host is without a name") || !strings.Contains(acctest.Str(out["note"]), "all removes the named ones") {
		t.Errorf("nothing without a name = %v", out)
	}
	if out = suite.Call(t, "image_prune", map[string]any{"env": "beta", "all": true}); out["note"] != "there is nothing to remove" {
		t.Errorf("nothing unused = %v", out)
	}

	for name, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"no host":         {map[string]any{"confirm": true}, "env is required"},
		"a wait too long": {map[string]any{"env": "alpha", "wait": 301}, "wait is 301"},
	} {
		if msg := suite.CallErr(t, "image_prune", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", name, msg, tc.want)
		}
	}

	args := map[string]any{"env": "alpha", "all": true, "confirm": true}
	// still going when the wait is over
	dock.Answer(call, `{"jobId":"j2"}`)
	dock.Answer("GET /api/jobs/j2", `{"id":"j2","status":"running","lines":[],"result":null}`)
	out = suite.Call(t, "image_prune", map[string]any{"env": "alpha", "all": true, "confirm": true, "wait": 1})
	if !acctest.IsBool(out["running"], true) || out["job"] != "j2" || len(acctest.Strs(t, out["removed"], "removed")) != 0 || !strings.Contains(acctest.Str(out["note"]), "still running after 1 second and") {
		t.Errorf("a pruning left running = %v", out)
	}
	// the job fails
	dock.Answer("GET /api/jobs/j2", `{"id":"j2","status":"error","lines":[],"result":{"success":false,"error":"Failed to prune images"}}`)
	if msg := suite.CallErr(t, "image_prune", args); !strings.Contains(msg, "the images of alpha were not pruned: Failed to prune images") {
		t.Errorf("a pruning whose job failed = %q", msg)
	}
	// the work done first and failed, which Dockhand answers with a 200
	dock.Answer(call, `{"success":false,"error":"Failed to prune images"}`)
	if msg := suite.CallErr(t, "image_prune", args); !strings.Contains(msg, "the images of alpha were not pruned: Failed to prune images") {
		t.Errorf("a pruning that failed without a job = %q", msg)
	}
	// done without being said to have worked, and with nothing of what went
	dock.Answer(call, `{"success":false}`)
	if msg := suite.CallErr(t, "image_prune", args); !strings.Contains(msg, "without its being said to have succeeded") {
		t.Errorf("a pruning answered without success = %q", msg)
	}
	dock.Answer(call, `{"success":true}`)
	if msg := suite.CallErr(t, "image_prune", args); !strings.Contains(msg, "without its being said to have succeeded") {
		t.Errorf("a pruning answered with no report = %q", msg)
	}
	dock.Fail(call, http.StatusForbidden, "Permission denied")
	if msg := suite.CallErr(t, "image_prune", args); !strings.Contains(msg, "the images of alpha were not pruned") || !strings.Contains(msg, "Permission denied") {
		t.Errorf("a pruning the account may not make = %q", msg)
	}

	// what the host has cannot be read, so nothing is removed
	pruned := len(dock.Requests(call))
	dock.FailWhen("GET /api/containers", "env", "1", http.StatusInternalServerError, "Failed to list containers")
	if msg := suite.CallErr(t, "image_prune", args); !strings.Contains(msg, "the containers of alpha could not be listed to say which images they use, so nothing was removed") {
		t.Errorf("a pruning with the containers unreadable = %q", msg)
	}
	dock.FailWhen("GET /api/images", "env", "1", http.StatusInternalServerError, "Failed to list images")
	if msg := suite.CallErr(t, "image_prune", args); !strings.Contains(msg, "the images of alpha could not be listed, so nothing was removed") || len(dock.Requests(call)) != pruned {
		t.Errorf("a pruning with the images unreadable = %q", msg)
	}
}

func TestImageHelpers(t *testing.T) {
	t.Parallel()

	for bytes, want := range map[int]float64{0: 0, 49999: 0, 50000: 0.1, 6143972: 6.1, 81000000: 81, 1234567890: 1234.6} {
		if got := megabytes(bytes); got != want {
			t.Errorf("megabytes(%d) = %v, want %v", bytes, got, want)
		}
	}
	for tag, want := range map[string]string{
		"nginx":                        "nginx",
		"nginx:1.27":                   "nginx",
		"ghcr.io/org/app:v2":           "ghcr.io/org/app",
		"registry.example:5000/app":    "registry.example:5000/app",
		"registry.example:5000/app:v2": "registry.example:5000/app",
	} {
		if got := repository(tag); got != want {
			t.Errorf("repository(%q) = %q, want %q", tag, got, want)
		}
	}
	for id, want := range map[string]string{"sha256:f00d": "f00d", "sha256:" + strings.Repeat("ab", 32): "abababababab", "abcdef": "abcdef"} {
		if got := shortImageID(id); got != want {
			t.Errorf("shortImageID(%q) = %q, want %q", id, got, want)
		}
	}
}
