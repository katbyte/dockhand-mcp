package tools

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// What a 1.0.51 server answers of what it does by itself, with invented names: a container kept on the newest image whose latest run was skipped, an
// environment's pruning switched off, a check for newer images, and two of Dockhand's own jobs, one of which can be switched off.
const (
	skippedRun  = `{"id":186,"scheduleType":"container_update","scheduleId":8,"environmentId":1,"entityName":"loose","triggeredBy":"manual","triggeredAt":"2026-10-10T14:06:34.602Z","startedAt":"2026-10-10T14:06:34.610Z","completedAt":"2026-10-10T14:06:35.543Z","duration":945,"status":"skipped","errorMessage":null,"details":{"reason":"Already up-to-date"},"createdAt":"2026-10-10 14:06:34","logs":null}`
	runningRun  = `{"id":187,"scheduleType":"container_update","scheduleId":8,"environmentId":1,"entityName":"loose","triggeredBy":"manual","triggeredAt":"2026-10-10T15:00:00.000Z","startedAt":"2026-10-10T15:00:00.001Z","completedAt":null,"duration":null,"status":"running","errorMessage":null,"details":null,"createdAt":"2026-10-10 15:00:00","logs":null}`
	updatedRun  = `{"id":187,"scheduleType":"container_update","scheduleId":8,"environmentId":1,"entityName":"loose","triggeredBy":"manual","triggeredAt":"2026-10-10T15:00:00.000Z","startedAt":"2026-10-10T15:00:00.001Z","completedAt":"2026-10-10T15:00:12.000Z","duration":12000,"status":"success","errorMessage":null,"details":{"updated":true},"createdAt":"2026-10-10 15:00:00","logs":null}`
	failedRun   = `{"id":163,"scheduleType":"stack_deploy","scheduleId":0,"environmentId":2,"entityName":"web","triggeredBy":"manual","triggeredAt":"2026-10-10T11:18:25.270Z","startedAt":"2026-10-10T11:18:25.273Z","completedAt":"2026-10-10T11:18:25.329Z","duration":56,"status":"failed","errorMessage":"failed to parse compose.yaml: did not find expected node content\n","details":{"exitCode":1},"createdAt":"2026-10-10 11:18:25","logs":null}`
	loggedRun   = `{"id":186,"scheduleType":"container_update","scheduleId":8,"environmentId":1,"entityName":"loose","triggeredBy":"manual","triggeredAt":"2026-10-10T14:06:34.602Z","startedAt":"2026-10-10T14:06:34.610Z","completedAt":"2026-10-10T14:06:35.543Z","duration":945,"status":"skipped","errorMessage":null,"details":{"reason":"Already up-to-date"},"createdAt":"2026-10-10 14:06:34","logs":"[2026-10-10T14:06:34.603Z] Checking container: loose\n[2026-10-10T14:06:34.700Z] Logging in with password=not-a-real-password\n[2026-10-10T14:06:35.542Z] Already up-to-date: loose is running the latest version"}`
	updateLoose = `{"id":8,"type":"container_update","name":"Update container: loose","entityName":"loose","description":null,"environmentId":1,"environmentName":"alpha","enabled":true,"scheduleType":"daily","cronExpression":"0 4 * * *","nextRun":"2026-10-11T04:00:00.000Z","lastExecution":` + skippedRun + `,"recentExecutions":[` + skippedRun + `],"isSystem":false}`
	looseOff    = `{"id":8,"type":"container_update","name":"Update container: loose","entityName":"loose","description":null,"environmentId":1,"environmentName":"alpha","enabled":false,"scheduleType":"daily","cronExpression":"0 4 * * *","nextRun":null,"lastExecution":null,"recentExecutions":[],"isSystem":false}`
	pruneBeta   = `{"id":2,"type":"image_prune","name":"Prune images: beta","entityName":"beta","description":"Prune dangling images only","environmentId":2,"environmentName":"beta","enabled":false,"scheduleType":"custom","cronExpression":"0 3 * * 0","nextRun":null,"lastExecution":null,"recentExecutions":[],"isSystem":false,"pruneMode":"dangling"}`
	checkAlpha  = `{"id":1,"type":"env_update_check","name":"Update environment: alpha","entityName":"alpha","description":"Check containers for updates (notify only)","environmentId":1,"environmentName":"alpha","enabled":true,"scheduleType":"custom","cronExpression":"0 4 * * *","nextRun":"2026-10-11T04:00:00.000Z","lastExecution":null,"recentExecutions":[],"isSystem":false}`
	tidyEvents  = `{"id":2,"type":"system_cleanup","name":"Container event cleanup","entityName":"Container event cleanup","description":"Removes container events older than 30 days","environmentId":null,"environmentName":null,"enabled":true,"scheduleType":"custom","cronExpression":"30 3 * * *","nextRun":"2026-10-11T03:30:00.000Z","lastExecution":null,"recentExecutions":[],"isSystem":true}`
	tidyHelpers = `{"id":3,"type":"system_cleanup","name":"Volume helper cleanup","entityName":"Volume helper cleanup","description":"Cleans up temporary volume browser containers","environmentId":null,"environmentName":null,"enabled":true,"scheduleType":"custom","cronExpression":"*/30 * * * *","nextRun":"2026-10-10T14:30:00.000Z","lastExecution":null,"recentExecutions":[],"isSystem":true}`

	scheduleList = "GET /api/schedules"
	runList      = "GET /api/schedules/executions"
)

// scheduled is a Dockhand with two hosts and the given things it does by itself.
func scheduled(t *testing.T, schedules ...string) *dockhandtest.Server {
	t.Helper()

	dock := twoHosts(t)
	dock.Answer(scheduleList, `{"schedules":[`+strings.Join(schedules, ",")+`]}`)

	return dock
}

// runs is a page of runs as the server answers one.
func runs(total int, each ...string) string {
	return `{"executions":[` + strings.Join(each, ",") + `],"total":` + strconv.Itoa(total) + `,"limit":20,"offset":0}`
}

func TestScheduleList(t *testing.T) {
	t.Parallel()

	dock := scheduled(t, tidyHelpers, pruneBeta, tidyEvents, updateLoose, checkAlpha)
	suite := session(t, dock, Options{})

	// the ones for containers and environments first, by environment, then Dockhand's own
	out := suite.Call(t, "schedule_list", nil)
	rows := acctest.Rows(t, out["schedules"], "schedules")
	if acctest.Num(t, out["total"], "total") != 5 || len(rows) != 5 || rows[0]["type"] != "container-update" || rows[1]["type"] != "env-update-check" || rows[2]["type"] != "image-prune" || rows[3]["name"] != "Container event cleanup" || rows[4]["name"] != "Volume helper cleanup" || out["note"] != nil {
		t.Fatalf("what Dockhand does by itself = %v", out)
	}
	if kept := rows[0]; acctest.Num(t, kept["id"], "id") != 8 || kept["name"] != "Update container: loose" || kept["env"] != "alpha" || !acctest.IsBool(kept["enabled"], true) || kept["cron"] != "0 4 * * *" || kept["next_run"] != "2026-10-11T04:00:00Z" || kept["does"] != nil || kept["system"] != nil {
		t.Errorf("a container kept on the newest image = %v", kept)
	}
	if last := acctest.Object(t, rows[0]["last_run"], "last_run"); acctest.Num(t, last["run"], "run") != 186 || last["status"] != "skipped" || last["triggered_by"] != "manual" || last["at"] != "2026-10-10T14:06:34Z" || acctest.Decimal(t, last["seconds"], "seconds") != 0.9 || acctest.Object(t, last["details"], "details")["reason"] != "Already up-to-date" || last["error"] != nil {
		t.Errorf("its latest run = %v", last)
	}
	if off, own := rows[2], rows[3]; !acctest.IsBool(off["enabled"], false) || off["next_run"] != nil || off["does"] != "Prune dangling images only" || off["env"] != "beta" || off["last_run"] != nil || !acctest.IsBool(own["system"], true) || own["env"] != nil {
		t.Errorf("a pruning switched off, and one of Dockhand's own jobs = %v, %v", off, own)
	}

	// narrowed by environment, by type, and to what is not Dockhand's own
	for what, tc := range map[string]struct {
		args map[string]any
		want int
	}{
		"one environment's":      {map[string]any{"env": "alpha"}, 2},
		"one type":               {map[string]any{"type": " System_Cleanup "}, 2},
		"without Dockhand's own": {map[string]any{"hide_system": true}, 3},
		"both":                   {map[string]any{"env": 2, "type": "image_prune"}, 1},
	} {
		if out = suite.Call(t, "schedule_list", tc.args); acctest.Num(t, out["total"], "total") != tc.want {
			t.Errorf("%s = %v, want %d", what, out, tc.want)
		}
	}
	if out = suite.Call(t, "schedule_list", map[string]any{"type": "backup"}); !strings.Contains(acctest.Str(out["note"]), "nothing matched") {
		t.Errorf("a type there is none of here = %v", out)
	}

	if msg := suite.CallErr(t, "schedule_list", map[string]any{"type": "hourly"}); !strings.Contains(msg, `type is "hourly"`) {
		t.Errorf("a type that is none = %q", msg)
	}
	if msg := suite.CallErr(t, "schedule_list", map[string]any{"env": "gamma"}); !strings.Contains(msg, `no environment is named or numbered "gamma"`) {
		t.Errorf("an environment that is none = %q", msg)
	}
	dock.Fail(scheduleList, http.StatusForbidden, "Permission denied")
	if msg := suite.CallErr(t, "schedule_list", nil); !strings.Contains(msg, "the schedules could not be listed") || !strings.Contains(msg, "Permission denied") {
		t.Errorf("schedules the account may not see = %q", msg)
	}
	dock.Fail("GET /api/environments", http.StatusInternalServerError, "Failed")
	if msg := suite.CallErr(t, "schedule_list", nil); !strings.Contains(msg, "the environments could not be listed") {
		t.Errorf("environments that could not be listed = %q", msg)
	}
}

func TestScheduleHistory(t *testing.T) {
	t.Parallel()

	const one = "GET /api/schedules/executions/186"
	dock := scheduled(t)
	dock.Answer(runList, runs(2, skippedRun, failedRun))
	dock.Answer(one, loggedRun)
	suite := session(t, dock, Options{})

	// the latest first, each with what it was a run of and how it went
	out := suite.Call(t, "schedule_history", nil)
	rows := acctest.Rows(t, out["runs"], "runs")
	if acctest.Num(t, out["total"], "total") != 2 || len(rows) != 2 || out["note"] != nil || out["log"] != nil {
		t.Fatalf("the runs = %v", out)
	}
	if failed := rows[1]; acctest.Num(t, failed["run"], "run") != 163 || failed["type"] != "stack-deploy" || failed["id"] != nil || failed["name"] != "web" || failed["env"] != "beta" || failed["status"] != "failed" || failed["error"] != "failed to parse compose.yaml: did not find expected node content" || acctest.Decimal(t, failed["seconds"], "seconds") != 0.1 {
		t.Errorf("a stack that failed to come up = %v", failed)
	}
	if asked := dock.Last(runList).Query; asked.Get("limit") != "20" || asked.Has("scheduleType") || asked.Has("statuses") || asked.Has("environmentId") || asked.Has("offset") {
		t.Errorf("every run was asked for with %v", asked)
	}

	// narrowed every way it can be
	before := time.Now().Add(-2 * time.Hour)
	suite.Call(t, "schedule_history", map[string]any{"env": "alpha", "type": " Container_Update ", "id": 8, "status": []any{"Failed", " warning "}, "triggered_by": "CRON", "since": "2h", "until": "2026-10-10T15:00:00Z", "limit": 5, "offset": 10})
	asked := dock.Last(runList).Query
	since, err := time.Parse(time.RFC3339, asked.Get("fromDate"))
	if err != nil || since.Before(before.Add(-time.Minute)) || since.After(before.Add(time.Minute)) || asked.Get("environmentId") != "1" || asked.Get("scheduleType") != "container_update" || asked.Get("scheduleId") != "8" || asked.Get("statuses") != "failed,warning" || asked.Get("triggeredBy") != "cron" || asked.Get("toDate") != "2026-10-10T15:00:00Z" || asked.Get("limit") != "5" || asked.Get("offset") != "10" {
		t.Errorf("the runs narrowed were asked for with %v, %v", asked, err)
	}

	// more matched than are given, and nothing matched
	dock.Answer(runList, runs(170, skippedRun))
	if out = suite.Call(t, "schedule_history", map[string]any{"limit": 1, "offset": 4}); !strings.Contains(acctest.Str(out["note"]), "170 runs matched and 1 are given") || !strings.Contains(acctest.Str(out["note"]), "offset 5") {
		t.Errorf("more runs than were asked for = %v", out)
	}
	dock.Answer(runList, runs(0))
	if out = suite.Call(t, "schedule_history", map[string]any{"type": "stack_deploy"}); len(acctest.Rows(t, out["runs"], "runs")) != 0 || !strings.Contains(acctest.Str(out["note"]), "nothing matched") {
		t.Errorf("no runs = %v", out)
	}

	// one run in full: what it logged, a secret in it hidden until asked for, and its end when it is long
	out = suite.Call(t, "schedule_history", map[string]any{"run": 186})
	if rows = acctest.Rows(t, out["runs"], "runs"); len(rows) != 1 || rows[0]["status"] != "skipped" || !strings.Contains(acctest.Str(out["log"]), "Checking container: loose") || strings.Contains(flat(out), "not-a-real-password") || acctest.Num(t, out["hidden"], "hidden") != 1 || !strings.Contains(acctest.Str(out["note"]), "hidden") {
		t.Errorf("one run with its log = %v", out)
	}
	if out = suite.Call(t, "schedule_history", map[string]any{"run": 186, "reveal_secrets": true, "max_chars": 14}); out["log"] != "latest version" || !acctest.IsBool(out["truncated"], true) || !strings.Contains(acctest.Str(out["note"]), "its last 14 are given") || out["hidden"] != nil {
		t.Errorf("the end of its log = %v", out)
	}
	dock.Answer(one, skippedRun)
	if out = suite.Call(t, "schedule_history", map[string]any{"run": 186}); out["log"] != nil || !strings.Contains(acctest.Str(out["note"]), "the run logged nothing") {
		t.Errorf("a run that logged nothing = %v", out)
	}
}

func TestScheduleHistoryRefused(t *testing.T) {
	t.Parallel()

	dock := scheduled(t)
	suite := session(t, dock, Options{})

	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"a type that is none":         {map[string]any{"type": "hourly"}, `type is "hourly"`},
		"a number with no type":       {map[string]any{"id": 8}, "it is given with type"},
		"a standing that is none":     {map[string]any{"status": []any{"broken"}}, `status has "broken"`},
		"a trigger that is none":      {map[string]any{"triggered_by": "magic"}, `triggered_by is "magic"`},
		"a time that is none":         {map[string]any{"since": "yesterday"}, "since:"},
		"an end that is none":         {map[string]any{"until": "tomorrow"}, "until:"},
		"more than can be given":      {map[string]any{"limit": 201}, "limit is 201"},
		"fewer than none passed":      {map[string]any{"offset": -1}, "offset -1"},
		"more of a log than is read":  {map[string]any{"run": 1, "max_chars": 200001}, "max_chars is 200001"},
		"an environment that is none": {map[string]any{"env": "gamma"}, `no environment is named or numbered "gamma"`},
	} {
		if msg := suite.CallErr(t, "schedule_history", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	if asked := dock.Requests(runList); len(asked) != 0 {
		t.Errorf("the runs were asked for %d times by calls that were refused", len(asked))
	}

	dock.Fail("GET /api/schedules/executions/9", http.StatusNotFound, "Execution not found")
	if msg := suite.CallErr(t, "schedule_history", map[string]any{"run": 9}); !strings.Contains(msg, "there is no run numbered 9") {
		t.Errorf("a run there is none of = %q", msg)
	}
	dock.Fail("GET /api/schedules/executions/9", http.StatusForbidden, "Permission denied")
	if msg := suite.CallErr(t, "schedule_history", map[string]any{"run": 9}); !strings.Contains(msg, "run 9 could not be read") {
		t.Errorf("a run the account may not read = %q", msg)
	}
	dock.Fail(runList, http.StatusInternalServerError, "Unexpected error")
	if msg := suite.CallErr(t, "schedule_history", nil); !strings.Contains(msg, "the runs could not be listed") {
		t.Errorf("runs that could not be listed = %q", msg)
	}
	dock.Fail("GET /api/environments", http.StatusInternalServerError, "Failed")
	if msg := suite.CallErr(t, "schedule_history", nil); !strings.Contains(msg, "the environments could not be listed") {
		t.Errorf("environments that could not be listed = %q", msg)
	}
}

func TestScheduleRun(t *testing.T) {
	t.Parallel()

	const run = "POST /api/schedules/container_update/8/run"
	dock := scheduled(t, updateLoose)
	dock.Answer(runList, runs(1, skippedRun))
	dock.Answer(run, `{"success":true,"message":"Schedule triggered successfully"}`)
	suite := session(t, dock, quick)

	// a dry run says what would run and runs nothing
	out := suite.Call(t, "schedule_run", map[string]any{"type": "container_update", "id": 8, "dry_run": true})
	if changes := acctest.Strs(t, out["changes"], "changes"); len(changes) != 1 || changes[0] != `would run "Update container: loose" (container-update 8) now` || out["run"] != nil || len(dock.Requests(run)) != 0 {
		t.Errorf("a dry run = %v, with %d run", out, len(dock.Requests(run)))
	}

	// run, and waited for: the run it set off is the one newer than the newest there was
	dock.Then(run, func(dockhandtest.Request) { dock.Answer(runList, runs(2, updatedRun, skippedRun)) })
	out = suite.Call(t, "schedule_run", map[string]any{"type": " Container_Update ", "id": 8})
	set := acctest.Object(t, out["run"], "run")
	if acctest.Num(t, set["run"], "run") != 187 || set["status"] != "success" || acctest.Decimal(t, set["seconds"], "seconds") != 12 || acctest.BoolOf(out["running"]) || out["name"] != "Update container: loose" || out["note"] != nil || acctest.Strs(t, out["changes"], "changes")[0] != `ran "Update container: loose" (container-update 8)` {
		t.Errorf("a schedule run now = %v", out)
	}
	if asked := dock.Last(runList).Query; asked.Get("scheduleType") != "container_update" || asked.Get("scheduleId") != "8" || asked.Get("limit") != "1" {
		t.Errorf("its newest run was asked for with %v", asked)
	}

	// still going when the wait is over, and not yet listed at all
	dock.Answer(runList, runs(1, skippedRun))
	dock.Then(run, func(dockhandtest.Request) { dock.Answer(runList, runs(2, runningRun, skippedRun)) })
	out = suite.Call(t, "schedule_run", map[string]any{"type": "container_update", "id": 8, "wait": 0})
	if !acctest.BoolOf(out["running"]) || acctest.Object(t, out["run"], "run")["status"] != "running" || !strings.Contains(acctest.Str(out["note"]), "schedule_history with run 187") {
		t.Errorf("a run still going = %v", out)
	}
	dock.Answer(runList, runs(1, skippedRun))
	dock.Then(run, func(dockhandtest.Request) {})
	if out = suite.Call(t, "schedule_run", map[string]any{"type": "container_update", "id": 8, "wait": 0}); !acctest.BoolOf(out["running"]) || out["run"] != nil || !strings.Contains(acctest.Str(out["note"]), "has not yet listed its run") {
		t.Errorf("a run not yet listed = %v", out)
	}

	// one that is not listed is still run, since an environment's check is not listed while it is off; Dockhand says when there is none
	dock.Answer(runList, runs(0))
	if out = suite.Call(t, "schedule_run", map[string]any{"type": "env_update_check", "id": 1, "dry_run": true}); out["name"] != nil || !strings.Contains(acctest.Str(out["note"]), "no schedule of that type and number is listed") {
		t.Errorf("a dry run of one that is not listed = %v", out)
	}
	dock.Fail("POST /api/schedules/env_update_check/7/run", http.StatusNotFound, "Schedule not found")
	if msg := suite.CallErr(t, "schedule_run", map[string]any{"type": "env_update_check", "id": 7}); !strings.Contains(msg, "the env-update-check numbered 7 was not run") || !strings.Contains(msg, "Schedule not found") {
		t.Errorf("one there is none of = %q", msg)
	}
}

func TestScheduleRunRefused(t *testing.T) {
	t.Parallel()

	dock := scheduled(t, updateLoose)
	suite := session(t, dock, quick)

	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"a type that is none": {map[string]any{"type": "hourly", "id": 1}, `type is "hourly"`},
		"no number":           {map[string]any{"type": "image_prune", "id": 0}, "id is required"},
		"a wait too long":     {map[string]any{"type": "image_prune", "id": 1, "wait": 301}, "wait is 301"},
	} {
		if msg := suite.CallErr(t, "schedule_run", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}

	// its runs not to be read, before and after it is set off
	dock.Fail(runList, http.StatusInternalServerError, "Unexpected error")
	if msg := suite.CallErr(t, "schedule_run", map[string]any{"type": "container_update", "id": 8}); !strings.Contains(msg, "so it was not run") || len(dock.Calls()) == 0 {
		t.Errorf("runs that could not be read = %q", msg)
	}
	const run = "POST /api/schedules/container_update/8/run"
	dock.Answer(runList, runs(1, skippedRun))
	dock.Answer(run, `{"success":true,"message":"Schedule triggered successfully"}`)
	dock.Then(run, func(dockhandtest.Request) { dock.Fail(runList, http.StatusInternalServerError, "Unexpected error") })
	if out := suite.Call(t, "schedule_run", map[string]any{"type": "container_update", "id": 8}); !strings.Contains(acctest.Str(out["note"]), "it was set off, and its run could not be read") || len(acctest.Strs(t, out["changes"], "changes")) != 1 {
		t.Errorf("a run set off and not read = %v", out)
	}
	dock.Fail(scheduleList, http.StatusForbidden, "Permission denied")
	if msg := suite.CallErr(t, "schedule_run", map[string]any{"type": "container_update", "id": 8}); !strings.Contains(msg, "the schedules could not be listed") {
		t.Errorf("schedules that could not be listed = %q", msg)
	}
}

func TestScheduleSet(t *testing.T) {
	t.Parallel()

	const (
		turn   = "POST /api/schedules/container_update/8/toggle"
		system = "POST /api/schedules/system/2/toggle"
		check  = "POST /api/schedules/env_update_check/1/toggle"
	)
	dock := scheduled(t, updateLoose, tidyEvents, tidyHelpers)
	dock.Answer(turn, `{"success":true,"enabled":false}`)
	suite := session(t, dock, everything)

	// as it stands already: nothing to do, and nothing turned over
	out := suite.Call(t, "schedule_set", map[string]any{"type": "container_update", "id": 8, "enabled": true})
	if len(acctest.Strs(t, out["changes"], "changes")) != 0 || !strings.Contains(acctest.Str(out["note"]), "switched on already") || !acctest.IsBool(out["enabled"], true) || len(dock.Requests(turn)) != 0 {
		t.Errorf("one that stands that way already = %v", out)
	}
	// said first, then switched off and read back
	if out = suite.Call(t, "schedule_set", map[string]any{"type": "container_update", "id": 8, "enabled": false, "dry_run": true}); acctest.Strs(t, out["changes"], "changes")[0] != `would switch "Update container: loose" (container-update 8) off` || len(dock.Requests(turn)) != 0 {
		t.Errorf("a dry run = %v", out)
	}
	dock.Then(turn, func(dockhandtest.Request) { dock.Answer(scheduleList, `{"schedules":[`+looseOff+`]}`) })
	out = suite.Call(t, "schedule_set", map[string]any{"type": "container_update", "id": 8, "enabled": false})
	if acctest.Strs(t, out["changes"], "changes")[0] != `switched "Update container: loose" (container-update 8) off` || !acctest.IsBool(out["enabled"], false) || out["next_run"] != nil || out["name"] != "Update container: loose" {
		t.Errorf("one switched off = %v", out)
	}

	// one of Dockhand's own that can be switched off goes by another call, and one that cannot is refused
	dock.Answer(scheduleList, `{"schedules":[`+tidyEvents+`,`+tidyHelpers+`]}`)
	dock.Answer(system, `{"success":true,"enabled":false}`)
	if out = suite.Call(t, "schedule_set", map[string]any{"type": "system_cleanup", "id": 2, "enabled": false}); len(acctest.Strs(t, out["changes"], "changes")) != 1 || len(dock.Requests(system)) != 1 {
		t.Errorf("one of Dockhand's own switched off = %v", out)
	}
	for what, args := range map[string]map[string]any{
		"a job that cannot be switched off": {"type": "system_cleanup", "id": 3, "enabled": false},
		"the other kind of its own jobs":    {"type": "deploy_log_reconcile", "id": 1, "enabled": false},
	} {
		if msg := suite.CallErr(t, "schedule_set", args); !strings.Contains(msg, "cannot be switched off") {
			t.Errorf("%s = %q", what, msg)
		}
	}

	// an environment's check is not listed while it is off: switched on it is turned over unseen, switched off there is nothing to do
	dock.Answer(check, `{"success":true,"enabled":true}`)
	dock.Then(check, func(dockhandtest.Request) { dock.Answer(scheduleList, `{"schedules":[`+checkAlpha+`]}`) })
	if out = suite.Call(t, "schedule_set", map[string]any{"type": "env_update_check", "id": 1, "enabled": true}); acctest.Strs(t, out["changes"], "changes")[0] != "switched the env-update-check numbered 1 on" || !acctest.IsBool(out["enabled"], true) || out["next_run"] != "2026-10-11T04:00:00Z" || out["name"] != "Update environment: alpha" {
		t.Errorf("an environment's check switched on = %v", out)
	}
	if out = suite.Call(t, "schedule_set", map[string]any{"type": "env_update_check", "id": 2, "enabled": false}); !strings.Contains(acctest.Str(out["note"]), "is switched off already") {
		t.Errorf("an environment's check that is off, switched off = %v", out)
	}

	// one there is none of, one turned the wrong way, and one Dockhand refuses
	if msg := suite.CallErr(t, "schedule_set", map[string]any{"type": "image_prune", "id": 9, "enabled": true}); !strings.Contains(msg, "there is no image-prune numbered 9") {
		t.Errorf("one there is none of = %q", msg)
	}
	dock.Answer(check, `{"success":true,"enabled":true}`)
	dock.Then(check, func(dockhandtest.Request) {})
	if msg := suite.CallErr(t, "schedule_set", map[string]any{"type": "env_update_check", "id": 1, "enabled": false}); !strings.Contains(msg, "now stands switched on, which is not what was asked") {
		t.Errorf("one turned the wrong way = %q", msg)
	}
	dock.Fail(check, http.StatusForbidden, "Permission denied")
	if msg := suite.CallErr(t, "schedule_set", map[string]any{"type": "env_update_check", "id": 1, "enabled": false}); !strings.Contains(msg, "was not switched off") || !strings.Contains(msg, "Permission denied") {
		t.Errorf("one Dockhand refuses = %q", msg)
	}
	dock.Answer(scheduleList, `{"schedules":[`+tidyEvents+`]}`)
	dock.Fail(system, http.StatusForbidden, "Permission denied")
	if msg := suite.CallErr(t, "schedule_set", map[string]any{"type": "system_cleanup", "id": 2, "enabled": false}); !strings.Contains(msg, "was not switched off") {
		t.Errorf("one of its own that Dockhand refuses = %q", msg)
	}
	if msg := suite.CallErr(t, "schedule_set", map[string]any{"type": "hourly", "id": 1, "enabled": true}); !strings.Contains(msg, `type is "hourly"`) {
		t.Errorf("a type that is none = %q", msg)
	}
}

func TestScheduleDelete(t *testing.T) {
	t.Parallel()

	const (
		remove = "DELETE /api/schedules/container_update/8"
		check  = "DELETE /api/schedules/env_update_check/1"
	)
	dock := scheduled(t, updateLoose, checkAlpha, tidyEvents)
	dock.Answer(remove, `{"success":true}`)
	dock.Answer(check, `{"success":true}`)
	suite := session(t, dock, everything)

	// without confirm: what would go, and nothing removed
	out := suite.Call(t, "schedule_delete", map[string]any{"type": "container_update", "id": 8})
	if would := acctest.Strs(t, out["would_go"], "would_go"); len(would) != 1 || !strings.Contains(would[0], `"Update container: loose" (container-update 8)`) || acctest.BoolOf(out["removed"]) || !strings.Contains(acctest.Str(out["note"]), "nothing was removed") || len(dock.Requests(remove)) != 0 {
		t.Errorf("a preview = %v", out)
	}
	// with it: removed, and read back as no longer listed
	dock.Then(remove, func(dockhandtest.Request) { dock.Answer(scheduleList, `{"schedules":[`+checkAlpha+`,`+tidyEvents+`]}`) })
	if out = suite.Call(t, "schedule_delete", map[string]any{"type": "container_update", "id": 8, "confirm": true}); !acctest.BoolOf(out["removed"]) || out["name"] != "Update container: loose" || out["note"] != nil {
		t.Errorf("one removed = %v", out)
	}
	// one Dockhand keeps listed, switched off, is removed all the same; one it leaves switched on is not
	dock.Then(check, func(dockhandtest.Request) {
		dock.Answer(scheduleList, `{"schedules":[`+strings.Replace(checkAlpha, `"enabled":true`, `"enabled":false`, 1)+`]}`)
	})
	if out = suite.Call(t, "schedule_delete", map[string]any{"type": "env_update_check", "id": 1, "confirm": true}); !acctest.BoolOf(out["removed"]) || !strings.Contains(acctest.Str(out["note"]), "keeps it listed, switched off") {
		t.Errorf("one switched off by its removal = %v", out)
	}
	dock.Answer(scheduleList, `{"schedules":[`+checkAlpha+`,`+tidyEvents+`]}`)
	dock.Then(check, func(dockhandtest.Request) {})
	if msg := suite.CallErr(t, "schedule_delete", map[string]any{"type": "env_update_check", "id": 1, "confirm": true}); !strings.Contains(msg, "still listed and switched on") {
		t.Errorf("one removed in word alone = %q", msg)
	}

	// Dockhand's own, one there is none of, and one Dockhand refuses or answers oddly
	if msg := suite.CallErr(t, "schedule_delete", map[string]any{"type": "system_cleanup", "id": 2, "confirm": true}); !strings.Contains(msg, "which cannot be removed") {
		t.Errorf("one of Dockhand's own = %q", msg)
	}
	if msg := suite.CallErr(t, "schedule_delete", map[string]any{"type": "image_prune", "id": 9, "confirm": true}); !strings.Contains(msg, "there is no image-prune numbered 9") {
		t.Errorf("one there is none of = %q", msg)
	}
	dock.Answer(check, `{"success":false}`)
	if msg := suite.CallErr(t, "schedule_delete", map[string]any{"type": "env_update_check", "id": 1, "confirm": true}); !strings.Contains(msg, "without its being said to have succeeded") {
		t.Errorf("one answered without success = %q", msg)
	}
	dock.Fail(check, http.StatusForbidden, "Permission denied")
	if msg := suite.CallErr(t, "schedule_delete", map[string]any{"type": "env_update_check", "id": 1, "confirm": true}); !strings.Contains(msg, "was not removed") || !strings.Contains(msg, "Permission denied") {
		t.Errorf("one Dockhand refuses = %q", msg)
	}
	dock.Answer(check, `{"success":true}`)
	dock.Then(check, func(dockhandtest.Request) {
		dock.Fail(scheduleList, http.StatusInternalServerError, "Unexpected error")
	})
	if out = suite.Call(t, "schedule_delete", map[string]any{"type": "env_update_check", "id": 1, "confirm": true}); acctest.BoolOf(out["removed"]) || !strings.Contains(acctest.Str(out["note"]), "could not be listed afterwards") {
		t.Errorf("one removed and not read back = %v", out)
	}
}

func TestScheduleHistoryDelete(t *testing.T) {
	t.Parallel()

	const (
		one    = "GET /api/schedules/executions/186"
		remove = "DELETE /api/schedules/executions/186"
	)
	dock := scheduled(t)
	dock.Answer(one, loggedRun)
	dock.Answer(remove, `{"success":true}`)
	suite := session(t, dock, everything)

	// without confirm: which run would go, and nothing removed
	out := suite.Call(t, "schedule_history_delete", map[string]any{"run": 186})
	if run := acctest.Object(t, out["run"], "run"); acctest.Num(t, run["run"], "run") != 186 || run["name"] != "loose" || run["env"] != "alpha" || acctest.BoolOf(out["removed"]) || !strings.Contains(acctest.Str(out["note"]), "nothing was removed") || len(dock.Requests(remove)) != 0 {
		t.Errorf("a preview = %v", out)
	}
	// with it: removed, and read back as gone
	dock.Then(remove, func(dockhandtest.Request) { dock.Fail(one, http.StatusNotFound, "Execution not found") })
	if out = suite.Call(t, "schedule_history_delete", map[string]any{"run": 186, "confirm": true}); !acctest.BoolOf(out["removed"]) || out["note"] != nil {
		t.Errorf("a record removed = %v", out)
	}
	if msg := suite.CallErr(t, "schedule_history_delete", map[string]any{"run": 186, "confirm": true}); !strings.Contains(msg, "there is no run numbered 186") {
		t.Errorf("removing it again = %q", msg)
	}

	// answered as removed and still there, removed and not read back, refused, and not to be read
	dock.Answer(one, loggedRun)
	dock.Then(remove, func(dockhandtest.Request) {})
	if msg := suite.CallErr(t, "schedule_history_delete", map[string]any{"run": 186, "confirm": true}); !strings.Contains(msg, "it is still there") {
		t.Errorf("a record removed in word alone = %q", msg)
	}
	dock.Then(remove, func(dockhandtest.Request) { dock.Fail(one, http.StatusInternalServerError, "Unexpected error") })
	if out = suite.Call(t, "schedule_history_delete", map[string]any{"run": 186, "confirm": true}); acctest.BoolOf(out["removed"]) || !strings.Contains(acctest.Str(out["note"]), "could not be read back") {
		t.Errorf("a record removed and not read back = %v", out)
	}
	dock.Answer(one, loggedRun)
	dock.Fail(remove, http.StatusForbidden, "Permission denied")
	if msg := suite.CallErr(t, "schedule_history_delete", map[string]any{"run": 186, "confirm": true}); !strings.Contains(msg, "the record of run 186 was not removed") {
		t.Errorf("a record Dockhand will not remove = %q", msg)
	}
	dock.Fail(one, http.StatusForbidden, "Permission denied")
	if msg := suite.CallErr(t, "schedule_history_delete", map[string]any{"run": 186}); !strings.Contains(msg, "so nothing was removed") {
		t.Errorf("a run that could not be read = %q", msg)
	}
	if msg := suite.CallErr(t, "schedule_history_delete", map[string]any{"run": 0}); !strings.Contains(msg, "run is required") {
		t.Errorf("no run = %q", msg)
	}
	dock.Fail("GET /api/environments", http.StatusInternalServerError, "Failed")
	if msg := suite.CallErr(t, "schedule_history_delete", map[string]any{"run": 186}); !strings.Contains(msg, "the environments could not be listed") {
		t.Errorf("environments that could not be listed = %q", msg)
	}
}
