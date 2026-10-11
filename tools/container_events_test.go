package tools

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// What a 1.0.51 server keeps of what containers did, with invented names: a death with its exit code, the kill before it with its signal, a rename,
// and the start of a stack's container, which carries its labels.
const (
	diedEvent    = `{"id":2343,"environmentId":1,"containerId":"` + looseID + `","containerName":"loose","image":"registry.example/tool:latest","action":"die","actorAttributes":{"execDuration":"3","exitCode":"137","image":"registry.example/tool:latest","name":"loose"},"timestamp":"2026-10-10T13:08:26.073Z","createdAt":"2026-10-10 13:08:26","environmentName":"alpha","environmentIcon":"globe"}`
	killedEvent  = `{"id":2342,"environmentId":1,"containerId":"` + looseID + `","containerName":"loose","image":"registry.example/tool:latest","action":"kill","actorAttributes":{"image":"registry.example/tool:latest","name":"loose","signal":"9"},"timestamp":"2026-10-10T13:08:26.012Z","createdAt":"2026-10-10 13:08:26","environmentName":"alpha","environmentIcon":"globe"}`
	renamedEvent = `{"id":2300,"environmentId":2,"containerId":"1111111111118888888888888888888888888888888888888888888888888888","containerName":"solo","image":"registry.example/tool:latest","action":"rename","actorAttributes":{"image":"registry.example/tool:latest","name":"solo","oldName":"/single"},"timestamp":"2026-10-10T12:00:00.000Z","createdAt":"2026-10-10 12:00:00","environmentName":"beta","environmentIcon":"globe"}`
	startedEvent = `{"id":2200,"environmentId":1,"containerId":"` + frontID + `","containerName":"web-front-1","image":"registry.example/front:1.2","action":"start","actorAttributes":{"com.docker.compose.project":"web","com.docker.compose.service":"front","exitCode":"0","image":"registry.example/front:1.2","name":"web-front-1"},"timestamp":"2026-10-10T07:39:18.727Z","createdAt":"2026-10-10 07:39:18","environmentName":"alpha","environmentIcon":"globe"}`

	activityCounts = `{"total":2339,"today":120,"byAction":{"die":401,"kill":534,"start":404,"health_status: unhealthy":10}}`
)

func TestContainerEvents(t *testing.T) {
	t.Parallel()

	const (
		log   = "GET /api/activity"
		stats = "GET /api/activity/stats"
	)
	dock := twoHosts(t)
	dock.Answer(log, `{"events":[`+diedEvent+`,`+killedEvent+`,`+renamedEvent+`,`+startedEvent+`],"total":4,"limit":50,"offset":0}`)
	dock.Answer(stats, activityCounts)
	suite := session(t, dock, Options{})

	// every host's, the latest first, each with what its kind of event says
	out := suite.Call(t, "container_events", nil)
	rows := acctest.Rows(t, out["events"], "events")
	if acctest.Num(t, out["total"], "total") != 4 || len(rows) != 4 || out["note"] != nil || out["by_action"] != nil {
		t.Fatalf("what the containers did = %v", out)
	}
	if died := rows[0]; died["at"] != "2026-10-10T13:08:26.073Z" || died["env"] != "alpha" || died["container"] != "loose" || died["action"] != "die" || acctest.Num(t, died["exit_code"], "exit_code") != 137 || died["signal"] != nil || died["image"] != "registry.example/tool:latest" || died["stack"] != nil {
		t.Errorf("a death = %v", died)
	}
	if killed, renamed, started := rows[1], rows[2], rows[3]; killed["signal"] != "9" || killed["exit_code"] != nil || renamed["env"] != "beta" || renamed["was"] != "single" || started["stack"] != "web" || started["exit_code"] != nil {
		t.Errorf("a kill, a rename and a start = %v, %v, %v", killed, renamed, started)
	}
	if asked := dock.Last(log).Query; asked.Has("environmentId") || asked.Get("limit") != "50" || asked.Has("containerName") || asked.Has("actions") || asked.Has("fromDate") {
		t.Errorf("the whole log was asked for with %v", asked)
	}

	// narrowed every way it can be, on one host, and counted
	before := time.Now().Add(-2 * time.Hour)
	out = suite.Call(t, "container_events", map[string]any{"env": "beta", "container": " /sol ", "actions": []any{"Die", " oom "}, "since": "2h", "until": "2026-10-10T13:00:00Z", "limit": 2, "counts": true})
	asked := dock.Last(log).Query
	since, err := time.Parse(time.RFC3339, asked.Get("fromDate"))
	if err != nil || since.Before(before.Add(-time.Minute)) || since.After(before.Add(time.Minute)) || asked.Get("environmentId") != "2" || asked.Get("containerName") != "sol" || asked.Get("actions") != "die,oom" || asked.Get("toDate") != "2026-10-10T13:00:00Z" || asked.Get("limit") != "2" {
		t.Errorf("the log narrowed was asked for with %v, %v", asked, err)
	}
	if counts := acctest.Object(t, out["by_action"], "by_action"); acctest.Num(t, counts["die"], "die") != 401 || acctest.Num(t, counts["health_status: unhealthy"], "unhealthy") != 10 || dock.Last(stats).Query.Get("environment_id") != "2" {
		t.Errorf("the log counted = %v, asked with %v", out["by_action"], dock.Last(stats).Query)
	}

	// more matched than are given, and nothing matched
	dock.Answer(log, `{"events":[`+diedEvent+`],"total":140,"limit":1,"offset":0}`)
	if out = suite.Call(t, "container_events", map[string]any{"limit": 1}); !strings.Contains(acctest.Str(out["note"]), "140 events matched and the latest 1 are given") {
		t.Errorf("more events than were asked for = %v", out)
	}
	dock.Answer(log, `{"events":[],"total":0,"limit":50,"offset":0}`)
	dock.Fail(stats, http.StatusInternalServerError, "Failed to get activity stats")
	if out = suite.Call(t, "container_events", map[string]any{"counts": true}); len(acctest.Rows(t, out["events"], "events")) != 0 || !strings.Contains(acctest.Str(out["note"]), "nothing matched: Dockhand keeps what containers did only for hosts it collects activity from") || !strings.Contains(acctest.Str(out["note"]), "the log could not be counted by action") {
		t.Errorf("an empty log that could not be counted = %v", out)
	}

	for _, tc := range []struct {
		why  string
		args map[string]any
		want string
	}{
		{"a since that is no time", map[string]any{"since": "yesterday"}, `since: "yesterday" is neither how long ago`},
		{"an until that is no time", map[string]any{"until": "-5m"}, "until:"},
		{"an action there is none of", map[string]any{"actions": []any{"explode"}}, `actions has "explode": an action is one of create, start, die`},
		{"more than a call gives", map[string]any{"limit": 9000}, "limit is 9000"},
		{"an environment there is none of", map[string]any{"env": "nope"}, "nope"},
	} {
		if msg := suite.CallErr(t, "container_events", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want it to say %q", tc.why, msg, tc.want)
		}
	}
	dock.Fail(log, http.StatusInternalServerError, "Failed to get activity")
	if msg := suite.CallErr(t, "container_events", nil); !strings.Contains(msg, "the activity log could not be read") {
		t.Errorf("a log that could not be read = %q", msg)
	}
}

func TestContainerEventsClear(t *testing.T) {
	t.Parallel()

	const (
		stats = "GET /api/activity/stats"
		wipe  = "DELETE /api/activity"
	)
	dock := twoHosts(t)
	dock.Answer(stats, activityCounts)
	dock.Answer(wipe, `{"success":true}`)
	suite := session(t, dock, everything)

	// said first: how much would go
	out := suite.Call(t, "container_events_clear", nil)
	if acctest.Num(t, out["events"], "events") != 2339 || !acctest.IsBool(out["cleared"], false) || !strings.Contains(acctest.Str(out["note"]), "nothing was cleared: call again with confirm") || len(dock.Requests(wipe)) != 0 {
		t.Errorf("what clearing would take = %v", out)
	}

	// cleared, and read back with the few events the hosts have made since; and said to be cleared and as full as before
	dock.Then(wipe, func(dockhandtest.Request) { dock.Answer(stats, `{"total":2,"today":2,"byAction":{"start":2}}`) })
	if out = suite.Call(t, "container_events_clear", map[string]any{"confirm": true}); acctest.Num(t, out["events"], "events") != 2339 || !acctest.IsBool(out["cleared"], true) || out["note"] != nil {
		t.Errorf("clearing = %v", out)
	}
	dock.Then(wipe, nil)
	if out = suite.Call(t, "container_events_clear", map[string]any{"confirm": true}); !acctest.IsBool(out["cleared"], false) || !strings.Contains(acctest.Str(out["note"]), "Dockhand said it cleared the log, and it holds 2 events") {
		t.Errorf("a clear that did not take = %v", out)
	}

	// an empty log, a refusal, and one that cannot be read before or after
	dock.Answer(stats, `{"total":0,"today":0,"byAction":{}}`)
	if out = suite.Call(t, "container_events_clear", map[string]any{"confirm": true}); out["note"] != "nothing to clear: the log holds no events" || len(dock.Requests(wipe)) != 2 {
		t.Errorf("clearing an empty log = %v", out)
	}
	dock.Answer(stats, activityCounts)
	dock.Fail(wipe, http.StatusForbidden, "Permission denied")
	if msg := suite.CallErr(t, "container_events_clear", map[string]any{"confirm": true}); !strings.Contains(msg, "the activity log was not cleared") {
		t.Errorf("a clear Dockhand refused = %q", msg)
	}
	dock.Answer(wipe, `{"success":true}`)
	dock.Then(wipe, func(dockhandtest.Request) {
		dock.Fail(stats, http.StatusInternalServerError, "Failed to get activity stats")
	})
	if out = suite.Call(t, "container_events_clear", map[string]any{"confirm": true}); !strings.Contains(acctest.Str(out["note"]), "that it is cleared could not be read back") {
		t.Errorf("a clear that could not be read back = %v", out)
	}
	if msg := suite.CallErr(t, "container_events_clear", map[string]any{"confirm": true}); !strings.Contains(msg, "the activity log could not be read, so nothing was cleared") {
		t.Errorf("a log that could not be read = %q", msg)
	}
}
