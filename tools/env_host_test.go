package tools

import (
	"maps"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// What a 1.0.51 server keeps for the canned beta, a host behind an agent, and says of the machine and of what it does for it by itself.
const (
	betaKept    = `{"id":2,"name":"beta","host":"agent-beta","port":2376,"protocol":"http","tlsCa":null,"tlsCert":null,"tlsSkipVerify":false,"icon":"globe","collectActivity":true,"collectMetrics":false,"highlightChanges":true,"labels":["lab"],"connectionType":"hawser-standard","socketPath":"/var/run/docker.sock","hawserLastSeen":null,"hawserAgentId":null,"hawserAgentName":"agent-beta","hawserVersion":null,"hawserCapabilities":null,"createdAt":"2026-10-10 10:02:45","updatedAt":"2026-10-10 10:02:45","hasTlsKey":false,"hasHawserToken":true,"publicIp":"192.0.2.10"}`
	betaHost    = `{"hostname":"docker-beta","ipAddress":"agent-beta","platform":"Alpine Linux v3.24","arch":"aarch64","cpus":2,"totalMemory":2053652480,"freeMemory":0,"uptime":180000,"dockerVersion":"29.9.0","dockerContainers":4,"dockerContainersRunning":3,"dockerImages":2,"environment":{"id":2,"name":"beta","icon":"globe","socketPath":"/var/run/docker.sock","connectionType":"hawser-standard","hawserVersion":"0.2.50","highlightChanges":true}}`
	betaSystem  = `{"docker":{"version":"29.9.0","apiVersion":"1.56","os":"Alpine Linux v3.24","arch":"aarch64","kernelVersion":"6.8.0-117-generic","serverVersion":"29.9.0"},"host":{"name":"docker-beta","cpus":2,"memory":2053652480,"storageDriver":"overlayfs"},"runtime":{},"database":{},"stats":{"containers":{"total":4,"running":3,"stopped":1},"images":2,"volumes":5,"networks":6,"stacks":1}}`
	zoneUTC     = `{"timezone":"UTC"}`
	checkOff    = `{"settings":{"enabled":false,"cron":"0 4 * * *","autoUpdate":false,"vulnerabilityCriteria":"never"}}`
	pruneOff    = `{"settings":{"enabled":false,"cronExpression":"0 3 * * 0","pruneMode":"dangling"}}`
	warningOn   = `{"enabled":true,"mode":"percentage","threshold":80,"thresholdGb":50,"percentageSupported":false,"storageDriver":"overlayfs"}`
	noRemoteDir = `{"remoteStacksDir":null}`
)

// The calls about what Dockhand does for the canned beta by itself.
const (
	betaZone    = "/api/environments/2/timezone"
	betaCheck   = "/api/environments/2/update-check"
	betaPrune   = "/api/environments/2/image-prune"
	betaWarning = "/api/environments/2/disk-warning"
	betaDir     = "/api/environments/2/remote-stacks-dir"
)

// withSettings is the two canned hosts with what Dockhand keeps for beta and says of its machine.
func withSettings(t *testing.T) *dockhandtest.Server {
	t.Helper()

	dock := twoHosts(t)
	dock.Answer("GET /api/environments/2", betaKept)
	dock.AnswerWhen("GET /api/host", "env", "2", betaHost)
	dock.AnswerWhen("GET /api/system", "env", "2", betaSystem)
	dock.Answer("GET "+betaZone, zoneUTC)
	dock.Answer("GET "+betaCheck, checkOff)
	dock.Answer("GET "+betaPrune, pruneOff)
	dock.Answer("GET "+betaWarning, warningOn)
	dock.Answer("GET "+betaDir, noRemoteDir)

	return dock
}

func TestEnvGet(t *testing.T) {
	t.Parallel()

	dock := withSettings(t)
	suite := session(t, dock, Options{})

	out := suite.Call(t, "env_get", map[string]any{"env": "beta"})
	if out["env"] != "beta" || acctest.Num(t, out["id"], "id") != 2 || out["connection"] != "hawser-standard" || out["address"] != "agent-beta:2376" || out["socket_path"] != "/var/run/docker.sock" || !reflect.DeepEqual(acctest.Strs(t, out["labels"], "labels"), []string{"lab"}) || out["public_ip"] != "192.0.2.10" || !acctest.IsBool(out["collect_activity"], true) || !acctest.IsBool(out["collect_metrics"], false) || out["agent"] != "agent-beta" || out["agent_version"] != "0.2.50" || !acctest.IsBool(out["has_agent_token"], true) || out["note"] != nil {
		t.Errorf("what Dockhand keeps for the host = %v", out)
	}
	machine := acctest.Object(t, out["machine"], "machine")
	if machine["hostname"] != "docker-beta" || machine["os"] != "Alpine Linux v3.24" || machine["arch"] != "aarch64" || machine["kernel"] != "6.8.0-117-generic" || acctest.Num(t, machine["cpus"], "cpus") != 2 || acctest.Decimal(t, machine["memory_mb"], "memory_mb") != 2053.7 || acctest.Decimal(t, machine["uptime_hours"], "uptime_hours") != 50 || machine["docker_version"] != "29.9.0" || machine["storage_driver"] != "overlayfs" || acctest.Num(t, machine["containers"], "containers") != 4 || acctest.Num(t, machine["running"], "running") != 3 || acctest.Num(t, machine["volumes"], "volumes") != 5 || acctest.Num(t, machine["stacks"], "stacks") != 1 {
		t.Errorf("the machine = %v", machine)
	}
	check, prune, warning := acctest.Object(t, out["update_check"], "update_check"), acctest.Object(t, out["prune_images"], "prune_images"), acctest.Object(t, out["disk_warning"], "disk_warning")
	if out["timezone"] != "UTC" || !acctest.IsBool(check["enabled"], false) || check["cron"] != "0 4 * * *" || check["hold_back"] != "never" || prune["cron"] != "0 3 * * 0" || prune["mode"] != "dangling" || !acctest.IsBool(warning["enabled"], true) || warning["mode"] != "percentage" || acctest.Num(t, warning["percent"], "percent") != 80 || acctest.Num(t, warning["gb"], "gb") != 50 || out["remote_stacks_dir"] != nil {
		t.Errorf("what Dockhand does for the host = %v", out)
	}
	if dock.Last("GET /api/host").Query.Get("env") != "2" || dock.Last("GET /api/system").Query.Get("env") != "2" {
		t.Errorf("the machine was asked of on %v", dock.Last("GET /api/host").Query)
	}

	// a host that does not answer is still described by what is kept for it; and what Docker says beyond the machine is not needed
	dock.FailWhen("GET /api/system", "env", "2", http.StatusInternalServerError, "Failed to get system info")
	if out = suite.Call(t, "env_get", map[string]any{"env": 2}); acctest.Object(t, out["machine"], "machine")["kernel"] != nil || out["note"] != nil {
		t.Errorf("a host whose Docker says no more = %v", out)
	}
	dock.FailWhen("GET /api/host", "env", "2", http.StatusInternalServerError, "Failed to get host info")
	for _, call := range []string{betaZone, betaCheck, betaPrune, betaWarning, betaDir} {
		dock.Fail("GET "+call, http.StatusInternalServerError, "Failed")
	}
	out = suite.Call(t, "env_get", map[string]any{"env": "beta"})
	if out["machine"] != nil || out["timezone"] != nil || out["update_check"] != nil || !strings.Contains(acctest.Str(out["note"]), "the host did not answer, so nothing is said of the machine") || !strings.Contains(acctest.Str(out["note"]), "its timezone, its update check, its image prune, its disk warning, its remote stacks folder could not be read") {
		t.Errorf("a host that does not answer = %v", out)
	}

	dock.Fail("GET /api/environments/2", http.StatusInternalServerError, "Failed to get environment")
	if msg := suite.CallErr(t, "env_get", map[string]any{"env": "beta"}); !strings.Contains(msg, "beta could not be read") {
		t.Errorf("an environment that could not be read = %q", msg)
	}
	if msg := suite.CallErr(t, "env_get", nil); !strings.Contains(msg, "env is required") {
		t.Errorf("no environment named = %q", msg)
	}
}

func TestEnvEdit(t *testing.T) {
	t.Parallel()

	const (
		save = "PUT /api/environments/2"
		try  = "POST /api/environments/2/test"
	)
	dock := withSettings(t)
	dock.Answer(save, strings.Replace(betaKept, `"name":"beta"`, `"name":"edge"`, 1))
	dock.Answer(try, `{"success":true,"info":{"serverVersion":"29.9.0","containers":4,"images":2,"name":"docker-beta"}}`)
	suite := session(t, dock, everything)
	change := map[string]any{"env": "beta", "name": " edge ", "labels": []any{"lab", "edge"}, "public_ip": "", "collect_activity": false, "collect_metrics": true, "host": "agent-edge", "port": 2377, "protocol": "HTTPS", "agent_token": "a-new-token"}
	said := []string{"its name, which was beta, is edge", "its labels, which were [lab], are [lab edge]", `its public address, which was "192.0.2.10", is ""`, "its containers' activity is collected: false", "its metrics are collected: true", "it is reached at agent-edge, where it was reached at agent-beta", "it is reached on port 2377, where it was 2376", "it is reached over https, where it was http", "its agent token is the one given"}

	// said first, and nothing saved or tried
	dry := suite.Call(t, "env_edit", map[string]any{"env": "beta", "name": " edge ", "labels": []any{"lab", "edge"}, "public_ip": "", "collect_activity": false, "collect_metrics": true, "host": "agent-edge", "port": 2377, "protocol": "HTTPS", "agent_token": "a-new-token", "dry_run": true})
	if got := acctest.Strs(t, dry["changes"], "changes"); !reflect.DeepEqual(got, said) || dry["env"] != "beta" || len(dock.Requests(save)) != 0 || strings.Contains(flat(dry), "a-new-token") {
		t.Errorf("a dry run = %v", dry)
	}

	// saved, with only what changed sent, and the new way of reaching it tried
	out := suite.Call(t, "env_edit", change)
	if got := acctest.Strs(t, out["changes"], "changes"); !reflect.DeepEqual(got, said) || out["env"] != "edge" || !acctest.IsBool(out["reachable"], true) || out["note"] != nil || strings.Contains(flat(out), "a-new-token") {
		t.Errorf("an edit = %v", out)
	}
	want := map[string]any{"name": "edge", "labels": []any{"lab", "edge"}, "collectActivity": false, "collectMetrics": true, "host": "agent-edge", "port": float64(2377), "protocol": "https", "hawserToken": "a-new-token"}
	// the address forgotten is a second call, which sends it empty: the first leaves an empty text out and Dockhand would keep it
	saves := dock.Requests(save)
	if len(saves) != 2 || !reflect.DeepEqual(sent(t, saves[0]), want) || saves[1].Body != `{"publicIp":""}` || len(dock.Requests(try)) != 1 {
		t.Errorf("an edit sent %v, want %v and then the address, empty", saves, want)
	}

	// what is so already is no change, and one that does not touch how it is reached tries nothing
	if out = suite.Call(t, "env_edit", map[string]any{"env": "beta", "name": "beta", "labels": []any{"lab"}, "collect_activity": true, "host": "agent-beta", "port": 2376, "protocol": "http", "socket_path": "/var/run/docker.sock"}); len(acctest.Strs(t, out["changes"], "changes")) != 0 || out["note"] != "nothing was done: it is so already" || len(dock.Requests(save)) != 2 {
		t.Errorf("an edit to what it is = %v", out)
	}
	if out = suite.Call(t, "env_edit", map[string]any{"env": "beta", "labels": []any{}, "socket_path": "/run/docker.sock"}); out["reachable"] == nil || !reflect.DeepEqual(sent(t, dock.Last(save)), map[string]any{"labels": []any{}, "socketPath": "/run/docker.sock"}) {
		t.Errorf("an edit of its labels and socket sent %v: %v", sent(t, dock.Last(save)), out)
	}
	if out = suite.Call(t, "env_edit", map[string]any{"env": "beta", "collect_metrics": true}); out["reachable"] != nil || len(dock.Requests(try)) != 2 {
		t.Errorf("an edit that does not touch how it is reached = %v", out)
	}

	// the new way does not get through, or cannot be tried: the change is saved and that is said
	dock.Answer(try, `{"success":false,"error":"connect ECONNREFUSED"}`)
	if out = suite.Call(t, "env_edit", map[string]any{"env": "beta", "port": 2400}); !acctest.IsBool(out["reachable"], false) || !strings.Contains(acctest.Str(out["note"]), "Dockhand does not reach the host the new way: connect ECONNREFUSED") {
		t.Errorf("an edit after which the host is not reached = %v", out)
	}
	dock.Fail(try, http.StatusInternalServerError, "Failed to test")
	if out = suite.Call(t, "env_edit", map[string]any{"env": "beta", "port": 2400}); out["reachable"] != nil || !strings.Contains(acctest.Str(out["note"]), "could not be tried") {
		t.Errorf("an edit after which the host could not be tried = %v", out)
	}

	for _, tc := range []struct {
		why  string
		args map[string]any
		want string
	}{
		{"nothing to change", map[string]any{"env": "beta"}, "nothing to change was given"},
		{"a protocol there is none of", map[string]any{"env": "beta", "protocol": "ftp"}, `protocol is "ftp"`},
		{"a port that is none", map[string]any{"env": "beta", "port": 70000}, "port is 70000"},
		{"a name that is taken", map[string]any{"env": "beta", "name": "Alpha"}, "another environment is named Alpha already"},
		{"no environment", map[string]any{"name": "x"}, "env is required"},
	} {
		if msg := suite.CallErr(t, "env_edit", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want it to say %q", tc.why, msg, tc.want)
		}
	}

	// the rest is saved and the address is not forgotten: said as it is
	asked := 0
	dock.Then(save, func(dockhandtest.Request) {
		asked++
		if asked == 2 {
			dock.Fail(save, http.StatusInternalServerError, "Failed to update environment")
		}
	})
	if msg := suite.CallErr(t, "env_edit", map[string]any{"env": "beta", "public_ip": " "}); !strings.Contains(msg, "beta was changed, and its public address was not forgotten") {
		t.Errorf("an address Dockhand would not forget = %q", msg)
	}
	dock.Then(save, nil)

	dock.Fail(save, http.StatusConflict, "Cannot rename: /app/data/stacks/edge already exists")
	if msg := suite.CallErr(t, "env_edit", map[string]any{"env": "beta", "name": "edge"}); !strings.Contains(msg, "beta was not changed") || !strings.Contains(msg, "already exists") {
		t.Errorf("an edit Dockhand refused = %q", msg)
	}
	dock.Fail("GET /api/environments/2", http.StatusInternalServerError, "Failed to get environment")
	if msg := suite.CallErr(t, "env_edit", map[string]any{"env": "beta", "name": "edge"}); !strings.Contains(msg, "beta could not be read, so nothing was changed") {
		t.Errorf("an edit of what could not be read = %q", msg)
	}
}

func TestEnvSchedules(t *testing.T) {
	t.Parallel()

	dock := withSettings(t)
	for _, call := range []string{betaZone, betaCheck, betaPrune, betaWarning, betaDir} {
		dock.Answer("POST "+call, `{"success":true}`)
	}
	suite := session(t, dock, everything)
	change := map[string]any{"env": "beta", "timezone": "Europe/Berlin", "update_check": true, "update_check_cron": " 0  5 * * * ", "auto_update": true, "update_hold_back": "Critical", "prune_images": true, "prune_images_cron": "0 2 * * 6", "prune_images_mode": "ALL", "disk_warning_mode": "absolute", "disk_warning_gb": 20, "remote_stacks_dir": "/srv/stacks"}
	said := []string{"its timezone, which was UTC, is Europe/Berlin", "its update check, which was off, is at 0 5 * * *, updating what is behind, held back by a scan: critical", "its image prune, which was off, is at 0 2 * * 6, of all", "its disk warning, which was at 80% full, is at 20 GB", `its remote stacks folder, which was "", is "/srv/stacks"`}

	// said first, with the settings as they stand
	dry := suite.Call(t, "env_schedules", map[string]any{"env": "beta", "timezone": "Europe/Berlin", "update_check": true, "update_check_cron": " 0  5 * * * ", "auto_update": true, "update_hold_back": "Critical", "prune_images": true, "prune_images_cron": "0 2 * * 6", "prune_images_mode": "ALL", "disk_warning_mode": "absolute", "disk_warning_gb": 20, "remote_stacks_dir": "/srv/stacks", "dry_run": true})
	if got := acctest.Strs(t, dry["changes"], "changes"); !reflect.DeepEqual(got, said) || dry["timezone"] != "UTC" || len(dock.Requests("POST "+betaZone)) != 0 {
		t.Errorf("a dry run = %v", dry)
	}

	// each saved by its own call, with what was not given kept, and read back
	dock.Then("POST "+betaDir, func(dockhandtest.Request) {
		dock.Answer("GET "+betaZone, `{"timezone":"Europe/Berlin"}`)
		dock.Answer("GET "+betaCheck, `{"settings":{"enabled":true,"cron":"0 5 * * *","autoUpdate":true,"vulnerabilityCriteria":"critical"}}`)
		dock.Answer("GET "+betaPrune, `{"settings":{"enabled":true,"cronExpression":"0 2 * * 6","pruneMode":"all"}}`)
		dock.Answer("GET "+betaWarning, `{"enabled":true,"mode":"absolute","threshold":80,"thresholdGb":20}`)
		dock.Answer("GET "+betaDir, `{"remoteStacksDir":"/srv/stacks"}`)
	})
	out := suite.Call(t, "env_schedules", change)
	if got := acctest.Strs(t, out["changes"], "changes"); !reflect.DeepEqual(got, said) || out["env"] != "beta" || out["timezone"] != "Europe/Berlin" || out["remote_stacks_dir"] != "/srv/stacks" || !acctest.IsBool(acctest.Object(t, out["update_check"], "update_check")["auto_update"], true) || acctest.Object(t, out["prune_images"], "prune_images")["mode"] != "all" || out["note"] != nil {
		t.Errorf("setting the schedules = %v", out)
	}
	for call, want := range map[string]map[string]any{
		betaZone:    {"timezone": "Europe/Berlin"},
		betaCheck:   {"enabled": true, "cron": "0 5 * * *", "autoUpdate": true, "vulnerabilityCriteria": "critical"},
		betaPrune:   {"enabled": true, "cronExpression": "0 2 * * 6", "pruneMode": "all"},
		betaWarning: {"enabled": true, "mode": "absolute", "threshold": float64(80), "thresholdGb": float64(20)},
		betaDir:     {"remoteStacksDir": "/srv/stacks"},
	} {
		if got := sent(t, dock.Last("POST "+call)); !reflect.DeepEqual(got, want) {
			t.Errorf("%s was sent %v, want %v", call, got, want)
		}
	}

	// what is so already sends nothing; one thing alone sends one call
	if out = suite.Call(t, "env_schedules", change); len(acctest.Strs(t, out["changes"], "changes")) != 0 || out["note"] != "nothing was done: it is so already" || len(dock.Requests("POST "+betaZone)) != 1 {
		t.Errorf("setting what is set = %v", out)
	}
	if out = suite.Call(t, "env_schedules", map[string]any{"env": "beta", "disk_warning": false, "remote_stacks_dir": ""}); len(acctest.Strs(t, out["changes"], "changes")) != 2 || len(dock.Requests("POST "+betaCheck)) != 1 || !acctest.IsBool(sent(t, dock.Last("POST "+betaWarning))["enabled"], false) {
		t.Errorf("switching the disk warning off = %v", out)
	}
}

func TestEnvSchedulesThatGoWrong(t *testing.T) {
	t.Parallel()

	dock := withSettings(t)
	suite := session(t, dock, everything)

	for _, tc := range []struct {
		why  string
		args map[string]any
		want string
	}{
		{"nothing to set", map[string]any{}, "nothing to set was given"},
		{"a check at no time", map[string]any{"update_check_cron": "daily"}, "update_check_cron: cron is"},
		{"a prune at no time", map[string]any{"prune_images_cron": "0 3 * *"}, "prune_images_cron: cron is"},
		{"a hold there is none of", map[string]any{"update_hold_back": "maybe"}, `update_hold_back is "maybe"`},
		{"a prune of what", map[string]any{"prune_images_mode": "everything"}, `prune_images_mode is "everything"`},
		{"a warning by what", map[string]any{"disk_warning_mode": "sideways"}, `disk_warning_mode is "sideways"`},
		{"fuller than full", map[string]any{"disk_warning_percent": 900}, "disk_warning_percent is 900"},
		{"less than nothing", map[string]any{"disk_warning_gb": -1}, "disk_warning_gb is -1"},
	} {
		given := map[string]any{"env": "beta"}
		maps.Copy(given, tc.args)
		if msg := suite.CallErr(t, "env_schedules", given); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want it to say %q", tc.why, msg, tc.want)
		}
	}
	if msg := suite.CallErr(t, "env_schedules", map[string]any{"timezone": "UTC"}); !strings.Contains(msg, "env is required") {
		t.Errorf("no environment named = %q", msg)
	}

	// a timezone there is none of is Dockhand's to refuse, and it is asked first, so nothing else is saved beside it
	dock.Fail("POST "+betaZone, http.StatusBadRequest, "Invalid timezone")
	if msg := suite.CallErr(t, "env_schedules", map[string]any{"env": "beta", "timezone": "Mars/Olympus", "update_check": true}); !strings.Contains(msg, "Invalid timezone") || !strings.Contains(msg, "Saved before it: nothing") || len(dock.Requests("POST "+betaCheck)) != 0 {
		t.Errorf("a timezone there is none of = %q", msg)
	}

	// one that fails stops the rest, and what was saved before it is said
	dock.Answer("POST "+betaZone, `{"success":true,"timezone":"Europe/Berlin"}`)
	dock.Fail("POST "+betaCheck, http.StatusInternalServerError, "Failed to save")
	if msg := suite.CallErr(t, "env_schedules", map[string]any{"env": "beta", "timezone": "Europe/Berlin", "update_check": true, "prune_images": true}); !strings.Contains(msg, "the settings of beta were not all saved") || !strings.Contains(msg, "its timezone, which was UTC, is Europe/Berlin") || len(dock.Requests("POST "+betaPrune)) != 0 {
		t.Errorf("a setting Dockhand refused = %q", msg)
	}

	// saved, and not readable afterwards; and not readable to begin with
	dock.Then("POST "+betaZone, func(dockhandtest.Request) {
		dock.Fail("GET "+betaZone, http.StatusInternalServerError, "Failed")
	})
	if out := suite.Call(t, "env_schedules", map[string]any{"env": "beta", "timezone": "Europe/Berlin"}); !strings.Contains(acctest.Str(out["note"]), "its timezone could not be read back") {
		t.Errorf("a setting that could not be read back = %v", out)
	}
	if msg := suite.CallErr(t, "env_schedules", map[string]any{"env": "beta", "timezone": "Europe/Berlin"}); !strings.Contains(msg, "its timezone could not be read of beta, so nothing was changed") {
		t.Errorf("settings that could not be read = %q", msg)
	}
}
