package tools

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// long is a time far enough back that anything stopped then is left over, and never how Docker writes a time that has not come.
const long = "2026-01-01T00:00:00Z"

// auditedContainer is one container of the audits' canned host: how the list gives it, and the rest of its inspect document.
type auditedContainer struct {
	name, image, state, status, stack, system string
	// inspect is the State, Config, HostConfig and Mounts of its inspect document; none is a container that cannot be inspected
	inspect string
}

// id is the container's id: the place it has in the list, sixty-four times over.
func auditedID(place int) string {
	return strings.Repeat(string("0123456789abcdef"[place]), 64)
}

// auditedContainers is alpha's containers as the audits' tests have them: one with nothing wrong, and one or two for each thing an audit looks for.
func auditedContainers() []auditedContainer {
	recent := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	sound := `"HostConfig":{"RestartPolicy":{"Name":"always"},"Memory":67108864,"NetworkMode":"bridge","PidMode":""},"Mounts":[]`
	healthy := `"Health":{"Status":"healthy","FailingStreak":0,"Log":[]}`
	socket := `{"Type":"bind","Source":"/var/run/docker.sock","Destination":"/var/run/docker.sock","RW":true}`

	return []auditedContainer{
		{name: "web-front-1", image: "registry.example/front:1.2", state: "running", status: "Up 2 hours (healthy)", stack: "web", inspect: `"RestartCount":0,"State":{"Status":"running","Running":true,"FinishedAt":"` + never + `",` + healthy + `},"Config":{"Image":"registry.example/front:1.2","Healthcheck":{"Test":["CMD","true"]}},` + sound},
		{name: "web-back-1", image: "registry.example/back:latest", state: "running", status: "Up 2 hours (unhealthy)", stack: "web", inspect: `"RestartCount":0,"State":{"Status":"running","Running":true,"FinishedAt":"` + never + `","Health":{"Status":"unhealthy","FailingStreak":5,"Log":[{"Output":"first"},{"Output":"connection\nrefused"}]}},"Config":{"Image":"registry.example/back:latest"},"HostConfig":{"RestartPolicy":{"Name":"no"},"Memory":0,"NetworkMode":"web_default"},"Mounts":[]`},
		{name: "bare", image: "registry.example:5000/tool", state: "running", status: "Up 2 hours", inspect: `"RestartCount":0,"State":{"Status":"running","Running":true,"FinishedAt":"` + never + `"},"Config":{"Image":"registry.example:5000/tool"},"HostConfig":{"RestartPolicy":{"Name":"always"},"Memory":1,"Privileged":true,"NetworkMode":"host","PidMode":"host","CapAdd":["CAP_SYS_ADMIN","NET_RAW","net_admin"]},"Mounts":[` + socket + `,{"Type":"bind","Source":"/","Destination":"/host","RW":false}]`},
		{name: "muted", image: "registry.example/tool:latest@sha256:abcd", state: "running", status: "Up 2 hours", inspect: `"RestartCount":0,"State":{"Status":"running","Running":true,"FinishedAt":"` + never + `"},"Config":{"Image":"registry.example/tool:latest@sha256:abcd","Healthcheck":{"Test":["NONE"]}},"HostConfig":{"RestartPolicy":{"Name":"always"},"Memory":1,"NetworkMode":"bridge"},"Mounts":[{"Type":"bind","Source":"/run/docker.sock","Destination":"/var/run/docker.sock","RW":false}]`},
		{name: "crashed", image: "registry.example/tool:1", state: "exited", status: "Exited (3) 9 months ago", inspect: `"RestartCount":0,"State":{"Status":"exited","ExitCode":3,"FinishedAt":"` + long + `"},"Config":{"Image":"registry.example/tool:1"},` + sound},
		{name: "starved", image: "registry.example/tool:1", state: "exited", status: "Exited (137) 1 hour ago", inspect: `"RestartCount":0,"State":{"Status":"exited","ExitCode":137,"OOMKilled":true,"FinishedAt":"` + recent + `"},"Config":{"Image":"registry.example/tool:1"},` + sound},
		{name: "looping", image: "registry.example/tool:1", state: "restarting", status: "Restarting (1) 3 seconds ago", inspect: `"RestartCount":7,"State":{"Status":"restarting","ExitCode":1,"FinishedAt":"` + recent + `"},"Config":{"Image":"registry.example/tool:1"},` + sound},
		{name: "unborn", image: "sha256:cafe00000000", state: "created", status: "Created", inspect: `"RestartCount":0,"State":{"Status":"created","FinishedAt":"` + never + `"},"Config":{"Image":"sha256:cafe00000000"},` + sound},
		{name: "dockhand", image: "registry.example/dockhand:1", state: "running", status: "Up 2 hours (healthy)", system: "dockhand", inspect: `"RestartCount":0,"State":{"Status":"running","Running":true,"FinishedAt":"` + never + `",` + healthy + `},"Config":{"Image":"registry.example/dockhand:1"},"HostConfig":{"RestartPolicy":{"Name":"always"},"Memory":1,"NetworkMode":"bridge"},"Mounts":[` + socket + `]`},
		{name: "blocked", image: "registry.example/tool:1", state: "created", status: "Created", inspect: `"RestartCount":0,"State":{"Status":"created","ExitCode":128,"Error":"driver failed: port is\nalready allocated","FinishedAt":"` + never + `"},"Config":{"Image":"registry.example/tool:1"},` + sound},
		{name: "flapping", image: "registry.example/tool:1", state: "running", status: "Up 3 seconds", inspect: `"RestartCount":4,"State":{"Status":"running","Running":true,"FinishedAt":"` + recent + `",` + healthy + `},"Config":{"Image":"registry.example/tool:1"},` + sound},
		{name: "vanished", image: "registry.example/tool:1", state: "running", status: "Up 2 hours"},
	}
}

// audited is a canned Dockhand with everything the audits ask: alpha with the containers above and what the other tests' alpha holds, and beta with
// one image, one volume and nothing running.
func audited(t *testing.T) *dockhandtest.Server {
	t.Helper()

	dock := dockhandtest.New(t)
	dock.Answer("GET /api/environments", twoEnvironments)
	dock.Answer("GET /api/dashboard/stats", dashboardStats)

	rows := []string{}
	for place, c := range auditedContainers() {
		id, created, system, labels := auditedID(place), time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), "null", "{}"
		if c.name == "unborn" {
			created = long
		}
		if c.system != "" {
			system = `"` + c.system + `"`
		}
		if c.stack != "" {
			labels = `{"com.docker.compose.project":"` + c.stack + `"}`
		}
		rows = append(rows, fmt.Sprintf(`{"id":%q,"name":%q,"image":%q,"imageId":"sha256:cafe","command":"run","created":1791615491,"state":%q,"status":%q,"health":null,"restartCount":0,"labels":%s,"mounts":[],"networks":{},"ports":[],"systemContainer":%s}`, id, c.name, c.image, c.state, c.status, labels, system))

		if c.inspect == "" {
			dock.Fail("GET /api/containers/"+id+"/inspect", http.StatusNotFound, "No such container")

			continue
		}
		dock.Answer("GET /api/containers/"+id+"/inspect", fmt.Sprintf(`{"Id":%q,"Name":"/%s","Created":%q,%s}`, id, c.name, created, c.inspect))
	}
	dock.AnswerWhen("GET /api/containers", "env", "1", "["+strings.Join(rows, ",")+"]")
	dock.AnswerWhen("GET /api/containers", "env", "2", `[]`)

	dock.AnswerWhen("GET /api/stacks", "env", "1", alphaStacks)
	dock.AnswerWhen("GET /api/stacks", "env", "2", `[]`)
	dock.AnswerWhen("GET /api/containers/pending-updates", "env", "1", alphaNoted)
	dock.AnswerWhen("GET /api/containers/pending-updates", "env", "2", noneNoted)
	dock.AnswerWhen("GET /api/vulnerabilities", "env", "1", alphaFound)
	dock.AnswerWhen("GET /api/vulnerabilities", "env", "2", betaFound)
	dock.AnswerWhen("GET /api/images", "env", "1", alphaImages)
	dock.AnswerWhen("GET /api/images", "env", "2", betaImages)
	dock.AnswerWhen("GET /api/volumes", "env", "1", alphaVolumes)
	dock.AnswerWhen("GET /api/volumes", "env", "2", betaVolumes)
	dock.AnswerWhen("GET /api/networks", "env", "1", alphaNetworks)
	dock.AnswerWhen("GET /api/networks", "env", "2", betaNetworks)

	return dock
}

// foundBy is what an audit found, each as name/check, in the order it was given, and the findings by that.
func foundBy(t *testing.T, out map[string]any) (inOrder string, by map[string]map[string]any) {
	t.Helper()

	rows := acctest.Rows(t, out["findings"], "findings")
	order := make([]string, 0, len(rows))
	by = map[string]map[string]any{}
	for _, row := range rows {
		key := acctest.Str(row["name"]) + "/" + acctest.Str(row["check"])
		order = append(order, key)
		by[key] = row
	}

	return strings.Join(order, " "), by
}

// counted checks an audit's counts: how many findings, and how many things it looked at.
func counted(t *testing.T, out map[string]any, total, checked int) {
	t.Helper()

	if acctest.Num(t, out["total"], "total") != total || acctest.Num(t, out["checked"], "checked") != checked {
		t.Errorf("total %v of %v checked, want %d of %d", out["total"], out["checked"], total, checked)
	}

	sum := 0
	for check := range acctest.Object(t, out["by_check"], "by_check") {
		sum += acctest.Num(t, acctest.Object(t, out["by_check"], "by_check")[check], check)
	}
	if sum != total {
		t.Errorf("by_check adds up to %d, want the total %d: %v", sum, total, out["by_check"])
	}
}

func TestAuditHealth(t *testing.T) {
	t.Parallel()

	dock := audited(t)
	out := session(t, dock, Options{}).Call(t, "audit_health", nil)

	order, found := foundBy(t, out)
	if order != "blocked/start_failed crashed/exited_with_error flapping/restart_loop looping/restarting starved/out_of_memory web-back-1/unhealthy" {
		t.Errorf("what is broken = %s", order)
	}
	// every container but the one that could not be inspected, which is said
	counted(t, out, 6, 11)
	if !strings.Contains(acctest.Str(out["note"]), "1 containers could not be inspected") {
		t.Errorf("note = %q", out["note"])
	}

	// the latest check's words are given, on one line, and a finding says whose it is
	sick := found["web-back-1/unhealthy"]
	if sick["env"] != "alpha" || sick["kind"] != "container" || sick["stack"] != "web" || sick["image"] != "registry.example/back:latest" || sick["detail"] != "its health check has failed 5 times in a row; the latest printed: connection refused" {
		t.Errorf("the unhealthy one = %v", sick)
	}
	if got := acctest.Str(found["blocked/start_failed"]["detail"]); got != "Docker could not start it: driver failed: port is already allocated" {
		t.Errorf("the one that could not start = %q", got)
	}
	if got := acctest.Str(found["crashed/exited_with_error"]["detail"]); got != "exited with code 3, at "+long {
		t.Errorf("the one that fell over = %q", got)
	}
	if got := acctest.Str(found["looping/restarting"]["detail"]); !strings.Contains(got, "7 times") || !strings.Contains(got, "code 1") {
		t.Errorf("the one that keeps restarting = %q", got)
	}

	// each container is inspected once, on the environment it is on
	if made := dock.Requests("GET /api/containers/" + auditedID(0) + "/inspect"); len(made) != 1 || made[0].Query.Get("env") != "1" {
		t.Errorf("the first container was inspected %d times", len(made))
	}
}

func TestAuditNarrowed(t *testing.T) {
	t.Parallel()

	suite := session(t, audited(t), Options{})

	// one stack is its containers alone
	out := suite.Call(t, "audit_health", map[string]any{"stack": "WEB"})
	if order, _ := foundBy(t, out); order != "web-back-1/unhealthy" || out["note"] != nil {
		t.Errorf("the web stack = %s, note %v", order, out["note"])
	}
	counted(t, out, 1, 2)

	// one environment is asked alone, and one with nothing on it has nothing wrong and nothing looked at
	out = suite.Call(t, "audit_health", map[string]any{"env": "beta"})
	counted(t, out, 0, 0)
	if rows := acctest.Rows(t, out["findings"], "findings"); len(rows) != 0 {
		t.Errorf("beta = %v", rows)
	}

	// the list is cut at the limit and the counts are not
	out = suite.Call(t, "audit_security", map[string]any{"limit": 2})
	if rows := acctest.Rows(t, out["findings"], "findings"); len(rows) != 2 || !strings.Contains(acctest.Str(out["note"]), "the first 2 of 7") {
		t.Errorf("cut at 2 = %d rows, note %q", len(rows), out["note"])
	}
	counted(t, out, 7, 11)

	if msg := suite.CallErr(t, "audit_health", map[string]any{"limit": -1}); !strings.Contains(msg, "limit is -1") {
		t.Errorf("a limit below 0 = %q", msg)
	}
	if msg := suite.CallErr(t, "audit_stopped", map[string]any{"days": -3}); !strings.Contains(msg, "days is -3") {
		t.Errorf("days below 0 = %q", msg)
	}
	if msg := suite.CallErr(t, "audit_all", map[string]any{"env": "gamma"}); !strings.Contains(msg, `no environment is named or numbered "gamma"`) {
		t.Errorf("an environment there is not = %q", msg)
	}
}

func TestAuditHealthcheck(t *testing.T) {
	t.Parallel()

	out := session(t, audited(t), Options{}).Call(t, "audit_healthcheck", nil)

	// only what runs is looked at: the six running containers that could be inspected
	if order, _ := foundBy(t, out); order != "bare/no_health_check muted/health_check_off" {
		t.Errorf("what nothing checks = %s", order)
	}
	counted(t, out, 2, 6)
}

func TestAuditRestartPolicy(t *testing.T) {
	t.Parallel()

	out := session(t, audited(t), Options{}).Call(t, "audit_restart_policy", nil)

	if order, _ := foundBy(t, out); order != "web-back-1/no_restart_policy" {
		t.Errorf("what would stay down = %s", order)
	}
	counted(t, out, 1, 6)
}

func TestAuditLimits(t *testing.T) {
	t.Parallel()

	out := session(t, audited(t), Options{}).Call(t, "audit_limits", nil)

	if order, _ := foundBy(t, out); order != "web-back-1/no_memory_limit" {
		t.Errorf("what may use everything = %s", order)
	}
	counted(t, out, 1, 6)
}

func TestAuditImageTags(t *testing.T) {
	t.Parallel()

	out := session(t, audited(t), Options{}).Call(t, "audit_image_tags", nil)

	// a registry's port is no tag, and an image pinned by its digest is exact whatever tag stands beside it
	if order, _ := foundBy(t, out); order != "bare/no_tag unborn/image_without_name web-back-1/latest_tag" {
		t.Errorf("what does not say its version = %s", order)
	}
	counted(t, out, 3, 11)
}

func TestAuditSecurity(t *testing.T) {
	t.Parallel()

	out := session(t, audited(t), Options{}).Call(t, "audit_security", nil)

	// Dockhand's own container has the socket and is passed over
	order, found := foundBy(t, out)
	if order != "bare/privileged bare/host_network bare/host_pid bare/docker_socket bare/host_root bare/added_capabilities muted/docker_socket" {
		t.Errorf("what was given too much = %s", order)
	}
	counted(t, out, 7, 11)

	// only the capabilities that reach outside are named, whatever way they were written
	if got := acctest.Str(found["bare/added_capabilities"]["detail"]); !strings.HasSuffix(got, ": NET_ADMIN, SYS_ADMIN") {
		t.Errorf("its capabilities = %q", got)
	}
	if got := acctest.Str(found["muted/docker_socket"]["detail"]); !strings.Contains(got, "read-only") {
		t.Errorf("a socket mounted to read = %q", got)
	}
	if got := acctest.Str(found["bare/host_root"]["detail"]); !strings.Contains(got, "at /host, to read") {
		t.Errorf("the host's disk = %q", got)
	}
}

func TestAuditStopped(t *testing.T) {
	t.Parallel()

	suite := session(t, audited(t), Options{})
	out := suite.Call(t, "audit_stopped", nil)

	// what stopped an hour ago is not left over, and what keeps restarting is not stopped
	order, found := foundBy(t, out)
	if order != "crashed/stopped_long unborn/never_started" {
		t.Errorf("what was left lying = %s", order)
	}
	counted(t, out, 2, 4)
	if got := acctest.Str(found["crashed/stopped_long"]["detail"]); !strings.Contains(got, "at "+long+", with exit code 3") {
		t.Errorf("the one that stopped long ago = %q", got)
	}

	// and how long is long is the caller's to say
	out = suite.Call(t, "audit_stopped", map[string]any{"days": 100000})
	counted(t, out, 0, 4)
}

func TestAuditHosts(t *testing.T) {
	t.Parallel()

	suite := session(t, audited(t), Options{})
	out := suite.Call(t, "audit_hosts", nil)

	order, found := foundBy(t, out)
	if order != "beta/offline" || found["beta/offline"]["kind"] != "host" || found["beta/offline"]["env"] != "beta" {
		t.Errorf("the hosts that cannot be reached = %s, %v", order, found)
	}
	counted(t, out, 1, 2)

	out = suite.Call(t, "audit_hosts", map[string]any{"env": "alpha"})
	counted(t, out, 0, 1)
}

func TestAuditStacks(t *testing.T) {
	t.Parallel()

	suite := session(t, audited(t), Options{})
	out := suite.Call(t, "audit_stacks", nil)

	order, found := foundBy(t, out)
	if order != "archive/not_running mail/partly_running" {
		t.Errorf("the stacks not wholly up = %s", order)
	}
	counted(t, out, 2, 3)
	if got := acctest.Str(found["mail/partly_running"]["detail"]); got != "1 of its 2 containers are not running: mail-spool-1 (exited)" {
		t.Errorf("the one partly up = %q", got)
	}
	if got := acctest.Str(found["archive/not_running"]["detail"]); !strings.Contains(got, "none of its 1 containers") || !strings.Contains(got, "stopped") {
		t.Errorf("the one down = %q", got)
	}

	out = suite.Call(t, "audit_stacks", map[string]any{"stack": "web"})
	counted(t, out, 0, 1)
}

func TestAuditUpdates(t *testing.T) {
	t.Parallel()

	// the two hosts of the other tests, whose containers are the ones the notes are for
	dock := twoHosts(t)
	dock.AnswerWhen("GET /api/containers/pending-updates", "env", "1", alphaNoted)
	dock.AnswerWhen("GET /api/containers/pending-updates", "env", "2", noneNoted)
	suite := session(t, dock, Options{})
	out := suite.Call(t, "audit_updates", nil)

	order, found := foundBy(t, out)
	if order != "loose/newer_image web-front-1/newer_version" {
		t.Errorf("what something newer is to be had for = %s", order)
	}
	counted(t, out, 2, 10)
	if got := found["web-front-1/newer_version"]; got["detail"] != "version 1.4 is out, a minor step from the one it runs" || got["stack"] != "web" || got["image"] != "registry.example/front:1.2" {
		t.Errorf("the newer version = %v", got)
	}
	// a note for a container that is gone is no finding, and is said
	if !strings.Contains(acctest.Str(out["note"]), "1 notes of an update are for containers that are gone") {
		t.Errorf("note = %q", out["note"])
	}
	// it reads what was noted and asks no registry
	if made := dock.Requests("POST /api/containers/check-updates"); len(made) != 0 {
		t.Errorf("the registries were asked %d times", len(made))
	}

	out = suite.Call(t, "audit_updates", map[string]any{"stack": "web"})
	if order, _ = foundBy(t, out); order != "web-front-1/newer_version" {
		t.Errorf("the web stack = %s", order)
	}
	counted(t, out, 1, 2)
}

func TestAuditVulnerabilities(t *testing.T) {
	t.Parallel()

	dock := audited(t)
	out := session(t, dock, Options{}).Call(t, "audit_vulnerabilities", nil)

	// one finding an image, whatever was found in it, and one a host with images nothing scanned; what is not grave is no finding
	order, found := foundBy(t, out)
	if order != "alpha/images_not_scanned registry.example/tool:latest/critical_vulnerabilities beta/images_not_scanned" {
		t.Errorf("what the scans found = %s", order)
	}
	counted(t, out, 3, 7)
	if got := found["registry.example/tool:latest/critical_vulnerabilities"]; got["kind"] != "image" || !strings.HasPrefix(acctest.Str(got["detail"]), "1 critical and 1 high, 1 of them put right by a newer version of the package; used by ") || !strings.Contains(acctest.Str(got["detail"]), "web-front-1") {
		t.Errorf("the image = %v", got)
	}
	if got := acctest.Str(found["alpha/images_not_scanned"]["detail"]); !strings.HasPrefix(got, "4 of its 6 images have never been scanned") {
		t.Errorf("alpha's unscanned = %q", got)
	}

	// only the grave ones are asked for
	if made := dock.Last("GET /api/vulnerabilities"); made.Query.Get("severity") != "critical,high" || made.Query.Get("limit") != "5000" {
		t.Errorf("asked with %v", made.Query)
	}
}

func TestAuditUnused(t *testing.T) {
	t.Parallel()

	// the two hosts of the other tests, whose containers are on the networks
	dock := twoHosts(t)
	dock.AnswerWhen("GET /api/images", "env", "1", alphaImages)
	dock.AnswerWhen("GET /api/images", "env", "2", betaImages)
	dock.AnswerWhen("GET /api/volumes", "env", "1", alphaVolumes)
	dock.AnswerWhen("GET /api/volumes", "env", "2", betaVolumes)
	dock.AnswerWhen("GET /api/networks", "env", "1", alphaNetworks)
	dock.AnswerWhen("GET /api/networks", "env", "2", betaNetworks)
	out := session(t, dock, Options{}).Call(t, "audit_unused", nil)

	// an image with no name left goes by its id, and Docker's own networks are never unused
	order, found := foundBy(t, out)
	if order != "999999999999/unused_image registry.example/old:0.9/unused_image spare/unused_network share/unused_volume spare/unused_volume" {
		t.Errorf("what nothing uses = %s", order)
	}
	// six images, four volumes and five networks on alpha, and one of each on beta
	counted(t, out, 5, 18)
	if got := acctest.Str(found["registry.example/old:0.9/unused_image"]["detail"]); !strings.Contains(got, "it is 120 MB") {
		t.Errorf("the old image = %q", got)
	}
	if !strings.Contains(acctest.Str(out["note"]), "add up to 170 MB") {
		t.Errorf("note = %q", out["note"])
	}
}

func TestAuditAll(t *testing.T) {
	t.Parallel()

	dock := audited(t)
	out := session(t, dock, Options{}).Call(t, "audit_all", nil)

	rows := acctest.Rows(t, out["audits"], "audits")
	order, counts := make([]string, 0, len(rows)), map[string]int{}
	for _, row := range rows {
		order = append(order, acctest.Str(row["audit"]))
		counts[acctest.Str(row["audit"])] = acctest.Num(t, row["findings"], "findings")
	}
	// every audit, what is broken now first
	if got := strings.Join(order, " "); got != "audit_health audit_hosts audit_stacks audit_healthcheck audit_restart_policy audit_updates audit_vulnerabilities audit_security audit_image_tags audit_limits audit_stopped audit_unused" {
		t.Errorf("the audits = %s", got)
	}
	// the notes are all for containers this host has not got, and with no container on a network every one but Docker's own is unused
	want := map[string]int{"audit_health": 6, "audit_hosts": 1, "audit_stacks": 2, "audit_healthcheck": 2, "audit_restart_policy": 1, "audit_updates": 0, "audit_vulnerabilities": 3, "audit_security": 7, "audit_image_tags": 3, "audit_limits": 1, "audit_stopped": 2, "audit_unused": 6}
	total := 0
	for name, n := range want {
		total += n
		if counts[name] != n {
			t.Errorf("%s counted %d, want %d", name, counts[name], n)
		}
	}
	if acctest.Num(t, out["total"], "total") != total || out["failed"] != nil || out["note"] != nil {
		t.Errorf("total %v, want %d; failed %v, note %v", out["total"], total, out["failed"], out["note"])
	}

	health := rowsNamed(rows, "audit")["audit_health"]
	if acctest.Num(t, health["checked"], "checked") != 11 || acctest.Num(t, acctest.Object(t, health["by_check"], "by_check")["unhealthy"], "unhealthy") != 1 || !strings.Contains(acctest.Str(health["note"]), "could not be inspected") {
		t.Errorf("the health row = %v", health)
	}

	// seven audits read the containers, and each container was inspected once for all of them
	for place := range auditedContainers() {
		if made := dock.Requests("GET /api/containers/" + auditedID(place) + "/inspect"); len(made) != 1 {
			t.Errorf("container %d was inspected %d times", place, len(made))
		}
	}
	// and nothing was changed: every call made was a read
	for _, call := range dock.Calls() {
		if !strings.HasPrefix(call, "GET ") {
			t.Errorf("an audit made the call %s", call)
		}
	}
}

func TestAuditHostThatCannotBeAsked(t *testing.T) {
	t.Parallel()

	dock := audited(t)
	dock.FailWhen("GET /api/containers", "env", "2", http.StatusBadGateway, "Environment is offline")
	suite := session(t, dock, Options{})

	// what the other host answered is still given
	out := suite.Call(t, "audit_health", nil)
	failed := acctest.Rows(t, out["failed"], "failed")
	if len(failed) != 1 || failed[0]["env"] != "beta" || !strings.Contains(acctest.Str(failed[0]["error"]), "offline") {
		t.Errorf("failed = %v", failed)
	}
	counted(t, out, 6, 11)

	out = suite.Call(t, "audit_all", nil)
	if failed = acctest.Rows(t, out["failed"], "failed"); len(failed) != 1 || failed[0]["env"] != "beta" || !strings.Contains(acctest.Str(out["note"]), "1 of the 2 environments") {
		t.Errorf("failed = %v, note %q", failed, out["note"])
	}
	unasked := []string{}
	for _, row := range acctest.Rows(t, out["audits"], "audits") {
		if row["unasked"] != nil {
			unasked = append(unasked, acctest.Str(row["audit"]))
		}
	}
	// the audits that read containers, and the one of what nothing uses, which reads them to say who is on a network
	if !slices.Contains(unasked, "audit_health") || !slices.Contains(unasked, "audit_unused") || slices.Contains(unasked, "audit_stacks") {
		t.Errorf("the audits that could not ask beta = %v", unasked)
	}
}
