package tools

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// What a 1.0.51 server has noted of newer images on alpha: another image under the very tag loose runs, a newer version tag for the stack's front,
// and a note left over for a container that has since been made again.
const (
	notedLoose = `{"containerId":"` + looseID + `","containerName":"loose","currentImage":"registry.example/tool:latest","checkedAt":"2026-10-10T12:00:44.902Z","hasImageUpdate":true,"newerVersion":null}`
	notedFront = `{"containerId":"` + frontID + `","containerName":"web-front-1","currentImage":"registry.example/front:1.2","checkedAt":"2026-10-10T12:00:44.900Z","hasImageUpdate":false,"newerVersion":{"tag":"1.4","bump":"minor","skipped":["1.3","1.4"],"digest":"sha256:abcd"}}`
	notedGone  = `{"containerId":"7777777777770000000000000000000000000000000000000000000000000000","containerName":"old-one","currentImage":"registry.example/old:0.9","checkedAt":"2026-10-01T03:00:00.000Z","hasImageUpdate":true,"newerVersion":null}`

	alphaNoted = `{"environmentId":1,"pendingUpdates":[` + notedFront + `,` + notedLoose + `,` + notedGone + `]}`
	noneNoted  = `{"environmentId":2,"pendingUpdates":[]}`

	// what Dockhand answers for a container that is on no schedule, and for one that is
	noSchedule    = `{"enabled":false,"scheduleType":"daily","cronExpression":"0 3 * * *","vulnerabilityCriteria":"never"}`
	looseSchedule = `{"id":1,"environmentId":1,"containerName":"loose","enabled":true,"scheduleType":"daily","cronExpression":"0 3 * * *","vulnerabilityCriteria":"never","lastChecked":"2026-10-10 03:00:00","lastUpdated":"2026-10-03 03:00:07","createdAt":"2026-10-01 11:57:18","updatedAt":"2026-10-01 11:57:18"}`
)

// withNotes is the two canned hosts with what Dockhand has noted of newer images on each.
func withNotes(t *testing.T) *dockhandtest.Server {
	t.Helper()

	dock := twoHosts(t)
	dock.AnswerWhen("GET /api/containers/pending-updates", "env", "1", alphaNoted)
	dock.AnswerWhen("GET /api/containers/pending-updates", "env", "2", noneNoted)

	return dock
}

func TestContainerUpdates(t *testing.T) {
	t.Parallel()

	const (
		check = "POST /api/containers/check-updates"
		notes = "GET /api/containers/" + frontID + "/version-notes"
	)
	dock := withNotes(t)
	suite := session(t, dock, Options{})

	// what the last check found, with the notes of containers that are gone counted and not listed
	out := suite.Call(t, "container_updates", nil)
	rows := acctest.Rows(t, out["updates"], "updates")
	if acctest.Num(t, out["total"], "total") != 2 || len(rows) != 2 || acctest.Num(t, out["stale"], "stale") != 1 || out["checked"] != nil || !strings.Contains(acctest.Str(out["note"]), "1 more notes are for containers that are gone") || len(dock.Requests(check)) != 0 {
		t.Fatalf("what is noted = %v", out)
	}
	if loose := rows[0]; loose["env"] != "alpha" || loose["container"] != "loose" || loose["image"] != "registry.example/tool:latest" || !acctest.IsBool(loose["newer_image"], true) || loose["newer_tag"] != nil || loose["checked_at"] != "2026-10-10T12:00:44.902Z" {
		t.Errorf("a container with another image under its tag = %v", loose)
	}
	if front := rows[1]; front["container"] != "web-front-1" || !acctest.IsBool(front["newer_image"], false) || front["newer_tag"] != "1.4" || front["step"] != "minor" || !reflect.DeepEqual(acctest.Strs(t, front["versions"], "versions"), []string{"1.3", "1.4"}) || front["notes"] != nil {
		t.Errorf("a container with a newer version tag = %v", front)
	}

	// nothing noted says only what the last check found
	if out = suite.Call(t, "container_updates", map[string]any{"env": "beta"}); acctest.Num(t, out["total"], "total") != 0 || !strings.Contains(acctest.Str(out["note"]), "Dockhand has nothing noted, which says only what its last check found") {
		t.Errorf("nothing noted = %v", out)
	}

	// the registries asked afresh: how many were asked about, and the ones that could not be
	dock.AnswerWhen(check, "env", "1", `{"total":9,"updatesFound":1,"results":[{"containerId":"`+looseID+`","containerName":"loose","imageName":"registry.example/tool:latest","hasUpdate":true,"currentDigest":"registry.example/tool@sha256:cafe","newDigest":"sha256:fresh","systemContainer":null,"updateDisabled":false},{"containerId":"ffffffffffff6666666666666666666666666666666666666666666666666666","containerName":"done","imageName":"registry.example/job:1","hasUpdate":false,"error":"Registry returned 404 (image or tag not found)","isLocalImage":true,"systemContainer":null,"updateDisabled":false},{"containerId":"333333333333aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","containerName":"steady","imageName":"registry.example/tool:latest","hasUpdate":false,"systemContainer":null,"updateDisabled":true}]}`)
	dock.AnswerWhen(check, "env", "2", `{"total":1,"updatesFound":0,"results":[{"containerName":"solo","imageName":"registry.example/tool:latest","hasUpdate":false}]}`)
	out = suite.Call(t, "container_updates", map[string]any{"check": true})
	unchecked := rowsNamed(acctest.Rows(t, out["unchecked"], "unchecked"), "container")
	if acctest.Num(t, out["checked"], "checked") != 10 || acctest.Num(t, out["total"], "total") != 2 || len(unchecked) != 2 || !strings.Contains(acctest.Str(unchecked["done"]["why"]), "Registry returned 404") || unchecked["done"]["env"] != "alpha" || !strings.Contains(acctest.Str(unchecked["steady"]["why"]), "it carries the label dockhand.update=false") {
		t.Errorf("a fresh check = %v", out)
	}

	// what the newer versions changed, cut to its start, and GitHub answering no more
	dock.Answer(notes, `{"changelogUrl":"https://github.example/front/releases","source":"github","rateLimited":true,"notes":[{"version":"1.3","name":"1.3","githubTag":"v1.3","publishedAt":"2026-09-01T00:00:00Z","url":"https://github.example/front/releases/v1.3","body":"`+strings.Repeat("x", 2000)+`"},{"version":"1.4","url":"https://github.example/front/releases/v1.4","body":" fixes \n"}]}`)
	out = suite.Call(t, "container_updates", map[string]any{"env": "alpha", "notes": true})
	front := rowsNamed(acctest.Rows(t, out["updates"], "updates"), "container")["web-front-1"]
	said := acctest.Rows(t, front["notes"], "notes")
	if len(said) != 2 || front["changelog"] != "https://github.example/front/releases" || said[0]["version"] != "1.3" || said[0]["published"] != "2026-09-01T00:00:00Z" || len(acctest.Str(said[0]["body"])) != 1504 || said[1]["body"] != "fixes" || dock.Last(notes).Query.Get("versions") != "1.3,1.4" || !strings.Contains(acctest.Str(out["note"]), "GitHub would answer no more for now") {
		t.Errorf("release notes = %v, with %v", front, out["note"])
	}
	dock.Fail(notes, http.StatusInternalServerError, "Failed to resolve notes")
	if out = suite.Call(t, "container_updates", map[string]any{"env": "alpha", "notes": true}); !strings.Contains(acctest.Str(out["note"]), "the release notes of web-front-1 could not be fetched") {
		t.Errorf("release notes that could not be fetched = %v", out)
	}
}

func TestContainerUpdatesWithAHostDown(t *testing.T) {
	t.Parallel()

	dock := withNotes(t)
	suite := session(t, dock, Options{})

	// each thing a host has to be asked can fail, and the other host is still given
	dock.FailWhen("GET /api/containers/pending-updates", "env", "2", http.StatusInternalServerError, "Environment offline")
	out := suite.Call(t, "container_updates", nil)
	if failed := acctest.Rows(t, out["failed"], "failed"); len(failed) != 1 || failed[0]["env"] != "beta" || acctest.Num(t, out["total"], "total") != 2 || !strings.Contains(acctest.Str(out["note"]), "1 of the 2 environments could not be asked") {
		t.Errorf("one host down = %v", out)
	}
	dock.AnswerWhen("GET /api/containers/pending-updates", "env", "2", noneNoted)
	dock.FailWhen("GET /api/containers", "env", "2", http.StatusInternalServerError, "Environment offline")
	if out = suite.Call(t, "container_updates", nil); len(acctest.Rows(t, out["failed"], "failed")) != 1 {
		t.Errorf("one host's containers unlisted = %v", out)
	}
	dock.AnswerWhen("POST /api/containers/check-updates", "env", "1", `{"total":0,"updatesFound":0,"results":[]}`)
	dock.FailWhen("POST /api/containers/check-updates", "env", "2", http.StatusInternalServerError, "Environment offline")
	if out = suite.Call(t, "container_updates", map[string]any{"check": true}); !strings.Contains(acctest.Str(acctest.Rows(t, out["failed"], "failed")[0]["error"]), "the registries could not be asked") {
		t.Errorf("one host's registries unasked = %v", out)
	}

	dock.FailWhen("GET /api/containers/pending-updates", "env", "1", http.StatusInternalServerError, "Environment offline")
	dock.FailWhen("GET /api/containers/pending-updates", "env", "2", http.StatusInternalServerError, "Environment offline")
	if msg := suite.CallErr(t, "container_updates", nil); !strings.Contains(msg, "no environment could be asked what is newer") {
		t.Errorf("every host down = %q", msg)
	}
	if msg := suite.CallErr(t, "container_updates", map[string]any{"env": "nope"}); !strings.Contains(msg, "nope") {
		t.Errorf("an environment there is none of = %q", msg)
	}
}

func TestContainerUpdatesClear(t *testing.T) {
	t.Parallel()

	const (
		noted  = "GET /api/containers/pending-updates"
		forget = "DELETE /api/containers/pending-updates"
	)
	dock := withNotes(t)
	dock.Answer(forget, `{"success":true}`)
	suite := session(t, dock, everything)

	// said first: every note of the host, the left-over one among them
	dry := suite.Call(t, "container_updates_clear", map[string]any{"env": "alpha", "dry_run": true})
	if got := acctest.Strs(t, dry["cleared"], "cleared"); !reflect.DeepEqual(got, []string{"loose", "old-one", "web-front-1"}) || acctest.Num(t, dry["left"], "left") != 3 || len(dock.Requests(forget)) != 0 {
		t.Errorf("a dry run = %v", dry)
	}

	// one container's, by its name and by the start of its id
	dock.Then(forget, func(dockhandtest.Request) {
		dock.AnswerWhen(noted, "env", "1", `{"environmentId":1,"pendingUpdates":[`+notedFront+`,`+notedGone+`]}`)
	})
	out := suite.Call(t, "container_updates_clear", map[string]any{"env": "alpha", "container": "loose"})
	if got := acctest.Strs(t, out["cleared"], "cleared"); len(got) != 1 || got[0] != "loose" || acctest.Num(t, out["left"], "left") != 2 || dock.Last(forget).Query.Get("containerId") != looseID || dock.Last(forget).Query.Get("env") != "1" {
		t.Errorf("clearing one container's note = %v", out)
	}
	if out = suite.Call(t, "container_updates_clear", map[string]any{"env": "alpha", "container": "777777"}); acctest.Strs(t, out["cleared"], "cleared")[0] != "old-one" || !strings.HasPrefix(dock.Last(forget).Query.Get("containerId"), "777777") {
		t.Errorf("clearing a note by the start of an id = %v", out)
	}
	if out = suite.Call(t, "container_updates_clear", map[string]any{"env": "alpha", "container": "loose"}); len(acctest.Strs(t, out["cleared"], "cleared")) != 0 || !strings.Contains(acctest.Str(out["note"]), "Dockhand has nothing noted for a container named loose on alpha") {
		t.Errorf("clearing a note there is none of = %v", out)
	}

	// every one of a host's, in one call
	dock.Then(forget, func(dockhandtest.Request) {
		dock.AnswerWhen(noted, "env", "1", `{"environmentId":1,"pendingUpdates":[]}`)
	})
	if out = suite.Call(t, "container_updates_clear", map[string]any{"env": "alpha"}); len(acctest.Strs(t, out["cleared"], "cleared")) != 2 || acctest.Num(t, out["left"], "left") != 0 || dock.Last(forget).Query.Has("containerId") {
		t.Errorf("clearing every note = %v", out)
	}
	if out = suite.Call(t, "container_updates_clear", map[string]any{"env": "alpha"}); !strings.Contains(acctest.Str(out["note"]), "nothing was done: Dockhand has nothing noted for alpha") {
		t.Errorf("clearing when nothing is noted = %v", out)
	}

	// what goes wrong
	if msg := suite.CallErr(t, "container_updates_clear", nil); !strings.Contains(msg, "env is required") {
		t.Errorf("no environment named = %q", msg)
	}
	dock.Then(forget, nil)
	dock.AnswerWhen(noted, "env", "1", alphaNoted)
	dock.Fail(forget, http.StatusInternalServerError, "Failed to forget")
	if msg := suite.CallErr(t, "container_updates_clear", map[string]any{"env": "alpha"}); !strings.Contains(msg, "what Dockhand has noted for alpha was not all cleared") {
		t.Errorf("a forget Dockhand refused = %q", msg)
	}
	dock.Answer(forget, `{"success":true}`)
	dock.Then(forget, func(dockhandtest.Request) {
		dock.FailWhen(noted, "env", "1", http.StatusInternalServerError, "Failed to get the pending updates")
	})
	if out = suite.Call(t, "container_updates_clear", map[string]any{"env": "alpha"}); !strings.Contains(acctest.Str(out["note"]), "what is left could not be read back") {
		t.Errorf("a forget that could not be read back = %v", out)
	}
	if msg := suite.CallErr(t, "container_updates_clear", map[string]any{"env": "alpha"}); !strings.Contains(msg, "could not be read, so nothing was cleared") {
		t.Errorf("notes that could not be read = %q", msg)
	}
}

func TestContainerAutoupdate(t *testing.T) {
	t.Parallel()

	const list = "GET /api/auto-update"
	dock := twoHosts(t)
	dock.AnswerWhen(list, "env", "1", `{"loose":{"enabled":true,"scheduleType":"daily","cronExpression":"0 3 * * *","vulnerabilityCriteria":"never"},"long-gone":{"enabled":true,"scheduleType":"custom","cronExpression":null,"vulnerabilityCriteria":"critical"}}`)
	dock.AnswerWhen(list, "env", "2", `{}`)
	dock.Answer("GET /api/auto-update/loose", looseSchedule)
	suite := session(t, dock, Options{})

	out := suite.Call(t, "container_autoupdate", nil)
	rows := rowsNamed(acctest.Rows(t, out["schedules"], "schedules"), "container")
	if acctest.Num(t, out["total"], "total") != 2 || out["note"] != nil || rows["loose"]["env"] != "alpha" || rows["loose"]["cron"] != "0 3 * * *" || rows["loose"]["kind"] != "daily" || rows["loose"]["hold_back"] != "never" || rows["loose"]["gone"] != nil || rows["loose"]["last_checked"] != nil {
		t.Errorf("the schedules = %v", out)
	}
	if gone := rows["long-gone"]; !acctest.IsBool(gone["gone"], true) || gone["cron"] != "" || gone["hold_back"] != "critical" {
		t.Errorf("a schedule whose container is gone = %v", gone)
	}

	// one container's, with when it last ran
	out = suite.Call(t, "container_autoupdate", map[string]any{"container": " /loose "})
	if rows := acctest.Rows(t, out["schedules"], "schedules"); len(rows) != 1 || rows[0]["last_checked"] != "2026-10-10 03:00:00" || rows[0]["last_updated"] != "2026-10-03 03:00:07" || dock.Last("GET /api/auto-update/loose").Query.Get("env") != "1" {
		t.Errorf("one container's schedule = %v", out)
	}
	if out = suite.Call(t, "container_autoupdate", map[string]any{"container": "web-back-1"}); acctest.Num(t, out["total"], "total") != 0 || !strings.Contains(acctest.Str(out["note"]), "no container named web-back-1 is updated on a schedule") {
		t.Errorf("a container on no schedule = %v", out)
	}
	if out = suite.Call(t, "container_autoupdate", map[string]any{"env": "beta"}); !strings.Contains(acctest.Str(out["note"]), "no container is updated on a schedule on the environments asked") {
		t.Errorf("a host with no schedules = %v", out)
	}

	// a host down
	dock.FailWhen(list, "env", "2", http.StatusInternalServerError, "Environment offline")
	if out = suite.Call(t, "container_autoupdate", nil); len(acctest.Rows(t, out["failed"], "failed")) != 1 || acctest.Num(t, out["total"], "total") != 2 || !strings.Contains(acctest.Str(out["note"]), "1 of the 2 environments could not be asked") {
		t.Errorf("one host down = %v", out)
	}
	dock.FailWhen(list, "env", "1", http.StatusInternalServerError, "Environment offline")
	if msg := suite.CallErr(t, "container_autoupdate", nil); !strings.Contains(msg, "no environment could be asked what it updates by itself") {
		t.Errorf("every host down = %q", msg)
	}
	if msg := suite.CallErr(t, "container_autoupdate", map[string]any{"env": "nope"}); !strings.Contains(msg, "nope") {
		t.Errorf("an environment there is none of = %q", msg)
	}
}

func TestContainerAutoupdateSet(t *testing.T) {
	t.Parallel()

	const (
		one  = "GET /api/auto-update/loose"
		save = "POST /api/auto-update/loose"
		drop = "DELETE /api/auto-update/loose"
	)
	weekly := strings.NewReplacer(`"scheduleType":"daily"`, `"scheduleType":"weekly"`, `"cronExpression":"0 3 * * *"`, `"cronExpression":"0 4 * * 0"`, `"vulnerabilityCriteria":"never"`, `"vulnerabilityCriteria":"critical"`).Replace(looseSchedule)

	dock := twoHosts(t)
	dock.Answer(one, noSchedule)
	dock.Answer(save, looseSchedule)
	dock.Answer(drop, `{"success":true}`)
	suite := session(t, dock, everything)

	// a new schedule: said first, then saved with the defaults and read back
	dry := suite.Call(t, "container_autoupdate_set", map[string]any{"container": "loose", "enabled": true, "dry_run": true})
	if got := acctest.Strs(t, dry["changes"], "changes"); len(got) != 1 || got[0] != "would set Dockhand to update loose by itself at 0 3 * * *, holding an update back: never" || !acctest.IsBool(dry["enabled"], false) || dry["cron"] != nil || len(dock.Requests(save)) != 0 {
		t.Errorf("a dry run = %v", dry)
	}
	dock.Then(save, func(dockhandtest.Request) { dock.Answer(one, looseSchedule) })
	out := suite.Call(t, "container_autoupdate_set", map[string]any{"container": "loose", "enabled": true})
	if got := acctest.Strs(t, out["changes"], "changes"); out["env"] != "alpha" || out["container"] != "loose" || !acctest.IsBool(out["enabled"], true) || out["cron"] != "0 3 * * *" || out["kind"] != "daily" || out["hold_back"] != "never" || len(got) != 1 || got[0] != "set Dockhand to update loose by itself at 0 3 * * *, holding an update back: never" || out["note"] != nil {
		t.Errorf("a new schedule = %v", out)
	}
	if got := sent(t, dock.Last(save)); !reflect.DeepEqual(got, map[string]any{"enabled": true, "cronExpression": "0 3 * * *", "vulnerabilityCriteria": "never"}) || dock.Last(save).Query.Get("env") != "1" {
		t.Errorf("a new schedule sent %v", got)
	}

	// the schedule it has is left; changed, what is not given is kept
	if out = suite.Call(t, "container_autoupdate_set", map[string]any{"container": "loose", "enabled": true, "cron": " 0  3 * * * "}); len(acctest.Strs(t, out["changes"], "changes")) != 0 || out["note"] != "nothing was done: that is its schedule already" || len(dock.Requests(save)) != 1 {
		t.Errorf("the schedule it has = %v", out)
	}
	dock.Then(save, func(dockhandtest.Request) { dock.Answer(one, weekly) })
	out = suite.Call(t, "container_autoupdate_set", map[string]any{"container": "loose", "enabled": true, "cron": "0 4 * * 0", "hold_back": "Critical"})
	if got := acctest.Strs(t, out["changes"], "changes"); got[0] != "changed the schedule of loose from 0 3 * * * (holding back: never) to 0 4 * * 0 (holding back: critical)" || out["kind"] != "weekly" || out["hold_back"] != "critical" {
		t.Errorf("a changed schedule = %v", out)
	}
	suite.Call(t, "container_autoupdate_set", map[string]any{"container": "loose", "enabled": true, "hold_back": "any"})
	if got := sent(t, dock.Last(save)); got["cronExpression"] != "0 4 * * 0" || got["vulnerabilityCriteria"] != "any" {
		t.Errorf("a change of one thing sent %v: the other is kept", got)
	}

	// stopped: said first, then forgotten and read back
	dry = suite.Call(t, "container_autoupdate_set", map[string]any{"container": "loose", "enabled": false, "dry_run": true})
	if got := acctest.Strs(t, dry["changes"], "changes"); got[0] != "would stop Dockhand updating loose by itself and forget its schedule, 0 4 * * 0" || !acctest.IsBool(dry["enabled"], true) || len(dock.Requests(drop)) != 0 {
		t.Errorf("a dry run of stopping = %v", dry)
	}
	dock.Then(drop, func(dockhandtest.Request) { dock.Answer(one, noSchedule) })
	out = suite.Call(t, "container_autoupdate_set", map[string]any{"container": "loose", "enabled": false})
	if got := acctest.Strs(t, out["changes"], "changes"); got[0] != "stopped Dockhand updating loose by itself and forgot its schedule, 0 4 * * 0" || !acctest.IsBool(out["enabled"], false) || out["cron"] != nil || dock.Last(drop).Query.Get("env") != "1" {
		t.Errorf("stopping = %v", out)
	}
	if out = suite.Call(t, "container_autoupdate_set", map[string]any{"container": "loose", "enabled": false}); out["note"] != "nothing was done: Dockhand does not update it by itself" || len(dock.Requests(drop)) != 1 {
		t.Errorf("stopping what is not scheduled = %v", out)
	}

	// a container labelled to be left alone can be scheduled, and it is said what that comes to
	dock.AnswerWhen("GET /api/containers", "env", "1", strings.Replace(alphaContainers, `"name":"loose","image":"registry.example/tool:latest","imageId":"sha256:cafe","command":"sleep 100000","created":1791615492,"state":"running","status":"Up 12 seconds","health":null,"restartCount":0,"labels":{}`, `"name":"loose","image":"registry.example/tool:latest","imageId":"sha256:cafe","command":"sleep 100000","created":1791615492,"state":"running","status":"Up 12 seconds","health":null,"restartCount":0,"labels":{"dockhand.update":"false"}`, 1))
	if out = suite.Call(t, "container_autoupdate_set", map[string]any{"container": "loose", "enabled": true, "dry_run": true}); !strings.Contains(acctest.Str(out["note"]), "it carries the label dockhand.update=false") {
		t.Errorf("a schedule for a container labelled to be left alone = %v", out)
	}
}

func TestContainerAutoupdateSetThatGoesWrong(t *testing.T) {
	t.Parallel()

	const (
		one  = "GET /api/auto-update/loose"
		save = "POST /api/auto-update/loose"
		drop = "DELETE /api/auto-update/loose"
	)
	dock := twoHosts(t)
	dock.Answer(one, noSchedule)
	suite := session(t, dock, everything)

	for _, tc := range []struct {
		why  string
		args map[string]any
		want string
	}{
		{"neither on nor off", map[string]any{"container": "loose"}, `missing properties: ["enabled"]`},
		{"a hold there is none of", map[string]any{"container": "loose", "enabled": true, "hold_back": "sometimes"}, `hold_back is "sometimes": it is one of never, any, critical_high, critical, more_than_current`},
		{"a cron of words", map[string]any{"container": "loose", "enabled": true, "cron": "every day"}, `cron is "every day": it is five fields`},
		{"a cron with a name in it", map[string]any{"container": "loose", "enabled": true, "cron": "0 3 * * MON"}, "it is five fields"},
		{"a container there is none of", map[string]any{"container": "nope", "enabled": true}, "nope"},
	} {
		if msg := suite.CallErr(t, "container_autoupdate_set", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want it to say %q", tc.why, msg, tc.want)
		}
	}

	// Dockhand refuses to save, and saves something other than was asked
	dock.Fail(save, http.StatusInternalServerError, "Failed to save auto-update setting")
	if msg := suite.CallErr(t, "container_autoupdate_set", map[string]any{"container": "loose", "enabled": true}); !strings.Contains(msg, "the schedule of loose on alpha was not saved") {
		t.Errorf("a schedule Dockhand would not save = %q", msg)
	}
	dock.Answer(save, looseSchedule)
	if out := suite.Call(t, "container_autoupdate_set", map[string]any{"container": "loose", "enabled": true}); !acctest.IsBool(out["enabled"], false) || !strings.Contains(acctest.Str(out["note"]), "the schedule read back is not as it was asked for") {
		t.Errorf("a schedule that did not take = %v", out)
	}
	dock.Then(save, func(dockhandtest.Request) {
		dock.Fail(one, http.StatusInternalServerError, "Failed to get auto-update setting")
	})
	if out := suite.Call(t, "container_autoupdate_set", map[string]any{"container": "loose", "enabled": true}); !strings.Contains(acctest.Str(out["note"]), "the schedule as it is now could not be read back") {
		t.Errorf("a schedule that could not be read back = %v", out)
	}
	if msg := suite.CallErr(t, "container_autoupdate_set", map[string]any{"container": "loose", "enabled": true}); !strings.Contains(msg, "the schedule of loose on alpha could not be read, so nothing was changed") {
		t.Errorf("a schedule that could not be read = %q", msg)
	}

	// and to forget one
	dock.Answer(one, looseSchedule)
	dock.Fail(drop, http.StatusInternalServerError, "Failed to delete auto-update setting")
	if msg := suite.CallErr(t, "container_autoupdate_set", map[string]any{"container": "loose", "enabled": false}); !strings.Contains(msg, "the schedule of loose on alpha was not removed") {
		t.Errorf("a schedule Dockhand would not forget = %q", msg)
	}
}
