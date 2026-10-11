package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/katbyte/go-kt/mcp/acctest"
)

// What a 1.0.51 server measures of the canned hosts' running containers: front holds the most memory on alpha and back uses the most processor.
const (
	alphaStats = `[
		{"id":"` + backID + `","name":"web-back-1","cpuPercent":42.5,"memoryUsage":31457280,"memoryRaw":36700160,"memoryCache":5242880,"memoryLimit":2053652480,"memoryPercent":1.53,"networkRx":1048576,"networkTx":524288,"blockRead":0,"blockWrite":0},
		{"id":"` + frontID + `","name":"web-front-1","cpuPercent":1.25,"memoryUsage":52428800,"memoryRaw":54525952,"memoryCache":2097152,"memoryLimit":67108864,"memoryPercent":78.13,"networkRx":3145728,"networkTx":1572864,"blockRead":4194304,"blockWrite":2097152},
		{"id":"` + looseID + `","name":"loose","cpuPercent":0,"memoryUsage":419430,"memoryRaw":419430,"memoryCache":0,"memoryLimit":2053652480,"memoryPercent":0.02,"networkRx":0,"networkTx":0,"blockRead":0,"blockWrite":0}
	]`
	betaStats = `[{"id":"1111111111118888888888888888888888888888888888888888888888888888","name":"solo","cpuPercent":3,"memoryUsage":10485760,"memoryRaw":10485760,"memoryCache":0,"memoryLimit":2053652480,"memoryPercent":0.51,"networkRx":0,"networkTx":0,"blockRead":0,"blockWrite":0}]`
)

func TestContainerStats(t *testing.T) {
	t.Parallel()

	dock := twoHosts(t)
	dock.AnswerWhen("GET /api/containers/stats", "env", "1", alphaStats)
	dock.AnswerWhen("GET /api/containers/stats", "env", "2", betaStats)
	suite := session(t, dock, Options{})

	// every host's running containers, the one holding the most memory first
	out := suite.Call(t, "container_stats", nil)
	rows := acctest.Rows(t, out["containers"], "containers")
	order := make([]string, 0, len(rows))
	for _, row := range rows {
		order = append(order, acctest.Str(row["env"])+" "+acctest.Str(row["name"]))
	}
	if got := strings.Join(order, "|"); got != "alpha web-front-1|alpha web-back-1|beta solo|alpha loose" || acctest.Num(t, out["total"], "total") != 4 || out["failed"] != nil || out["note"] != nil {
		t.Errorf("what is running, by memory = %s, %v", got, out)
	}
	front := rows[0]
	if acctest.Decimal(t, front["cpu_percent"], "cpu_percent") != 1.25 || acctest.Decimal(t, front["memory_mb"], "memory_mb") != 52.4 || acctest.Decimal(t, front["memory_limit_mb"], "memory_limit_mb") != 67.1 || acctest.Decimal(t, front["memory_percent"], "memory_percent") != 78.13 || acctest.Decimal(t, front["net_rx_mb"], "net_rx_mb") != 3.1 || acctest.Decimal(t, front["net_tx_mb"], "net_tx_mb") != 1.6 || acctest.Decimal(t, front["disk_read_mb"], "disk_read_mb") != 4.2 || acctest.Decimal(t, front["disk_write_mb"], "disk_write_mb") != 2.1 {
		t.Errorf("what front is using = %v", front)
	}

	// by processor, on one host, and only so many
	out = suite.Call(t, "container_stats", map[string]any{"env": "alpha", "sort": "CPU", "limit": 2})
	rows = acctest.Rows(t, out["containers"], "containers")
	if len(rows) != 2 || rows[0]["name"] != "web-back-1" || rows[1]["name"] != "web-front-1" || acctest.Num(t, out["total"], "total") != 3 || !strings.Contains(acctest.Str(out["note"]), "3 containers were measured and the heaviest 2 are listed") {
		t.Errorf("alpha by processor = %v", out)
	}
	// one container, by its name or the start of its id; one that is not running is said to use nothing
	if out = suite.Call(t, "container_stats", map[string]any{"container": "/solo"}); acctest.Num(t, out["total"], "total") != 1 || acctest.Rows(t, out["containers"], "containers")[0]["env"] != "beta" {
		t.Errorf("one container by name = %v", out)
	}
	if out = suite.Call(t, "container_stats", map[string]any{"container": frontID[:6]}); acctest.Num(t, out["total"], "total") != 1 || acctest.Rows(t, out["containers"], "containers")[0]["name"] != "web-front-1" {
		t.Errorf("one container by the start of its id = %v", out)
	}
	if out = suite.Call(t, "container_stats", map[string]any{"container": "crashed"}); acctest.Num(t, out["total"], "total") != 0 || !strings.Contains(acctest.Str(out["note"]), "no running container is named crashed") {
		t.Errorf("a container that is not running = %v", out)
	}

	// a host that cannot be asked is said, and the others are still measured
	dock.FailWhen("GET /api/containers/stats", "env", "2", http.StatusInternalServerError, "Agent unreachable")
	out = suite.Call(t, "container_stats", nil)
	failed := acctest.Rows(t, out["failed"], "failed")
	if acctest.Num(t, out["total"], "total") != 3 || len(failed) != 1 || failed[0]["env"] != "beta" || !strings.Contains(acctest.Str(out["note"]), "1 of the 2 environments could not be asked") {
		t.Errorf("with one host unreachable = %v", out)
	}
	dock.FailWhen("GET /api/containers/stats", "env", "1", http.StatusInternalServerError, "Failed to get stats")
	if msg := suite.CallErr(t, "container_stats", nil); !strings.Contains(msg, "no environment could be asked what its containers are using") {
		t.Errorf("with no host reachable = %q", msg)
	}

	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"sort": "disk"}, `sort is "disk"`},
		{map[string]any{"limit": -1}, "limit is -1"},
		{map[string]any{"env": "gamma"}, "gamma"},
	} {
		if msg := suite.CallErr(t, "container_stats", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("container_stats %v = %q, want %q", tc.args, msg, tc.want)
		}
	}
}

func TestContainerTop(t *testing.T) {
	t.Parallel()

	const call = "GET /api/containers/" + frontID + "/top"

	dock := twoHosts(t)
	dock.Answer(call, `{"Titles":["PID","USER","TIME","COMMAND"],"Processes":[["1","root","0:03","serve --listen :80 --db-password=hunter2"],["42","app","0:00","worker","one more than the titles"]],"source":"ps"}`)
	suite := session(t, dock, Options{})

	out := suite.Call(t, "container_top", map[string]any{"container": "web-front-1"})
	processes := acctest.Rows(t, out["processes"], "processes")
	if out["env"] != "alpha" || out["container"] != "web-front-1" || strings.Join(acctest.Strs(t, out["columns"], "columns"), " ") != "PID USER TIME COMMAND" || len(processes) != 2 || dock.Last(call).Query.Get("env") != "1" {
		t.Fatalf("the processes of front = %v", out)
	}
	// a password on a command line is hidden, and said to be
	if processes[0]["PID"] != "1" || processes[0]["USER"] != "root" || processes[0]["COMMAND"] != "serve --listen :80 --db-password=***" || acctest.Num(t, out["hidden"], "hidden") != 1 || !strings.Contains(acctest.Str(out["note"]), "1 secret is hidden") {
		t.Errorf("the first process = %v, %v", processes[0], out["note"])
	}
	// a field with no column to go under is left out rather than guessed at
	if len(processes[1]) != 4 || processes[1]["COMMAND"] != "worker" {
		t.Errorf("a process with a field too many = %v", processes[1])
	}
	if shown := suite.Call(t, "container_top", map[string]any{"container": "web-front-1", "reveal_secrets": true}); acctest.Rows(t, shown["processes"], "processes")[0]["COMMAND"] != "serve --listen :80 --db-password=hunter2" || shown["hidden"] != nil || shown["note"] != nil {
		t.Errorf("with reveal_secrets = %v", shown)
	}

	// only a running container has processes
	if msg := suite.CallErr(t, "container_top", map[string]any{"container": "crashed"}); !strings.Contains(msg, "crashed on alpha is exited, and processes can be listed only of a container that is running") {
		t.Errorf("a container that is not running = %q", msg)
	}
	dock.Fail(call, http.StatusInternalServerError, "Failed to list processes")
	if msg := suite.CallErr(t, "container_top", map[string]any{"container": "web-front-1"}); !strings.Contains(msg, "the processes of web-front-1 could not be listed") {
		t.Errorf("processes that cannot be listed = %q", msg)
	}
	if msg := suite.CallErr(t, "container_top", map[string]any{"container": "nope"}); !strings.Contains(msg, "nope") {
		t.Errorf("a container that is not there = %q", msg)
	}
}

func TestContainerFiles(t *testing.T) {
	t.Parallel()

	const call = "GET /api/containers/" + frontID + "/files"

	dock := twoHosts(t)
	dock.Answer(call, `{"path":"/etc/app","entries":[
		{"name":"zeta.conf","type":"file","size":120,"permissions":"rw-r--r--","owner":"root","group":"root","modified":"2026-10-10T10:31:00","readonly":false},
		{"name":"current","type":"symlink","size":0,"permissions":"rwxrwxrwx","owner":"root","group":"root","modified":"2026-10-10T10:31:00","linkTarget":"releases/3","readonly":false},
		{"name":"releases","type":"directory","size":4096,"permissions":"rwxr-xr-x","owner":"app","group":"staff","modified":"2026-10-09T08:00:00","readonly":false},
		{"name":"alpha.conf","type":"file","size":6,"permissions":"rw-------","owner":"app","group":"app","modified":"2026-10-10T10:31:00","readonly":false}
	]}`)
	suite := session(t, dock, Options{})

	// directories first, and then by name
	out := suite.Call(t, "container_files", map[string]any{"container": "web-front-1", "path": "etc/app/"})
	entries := acctest.Rows(t, out["entries"], "entries")
	if out["env"] != "alpha" || out["container"] != "web-front-1" || out["path"] != "/etc/app" || acctest.Num(t, out["total"], "total") != 4 || len(entries) != 4 || entries[0]["name"] != "releases" || entries[1]["name"] != "alpha.conf" || entries[2]["name"] != "current" || entries[3]["name"] != "zeta.conf" || out["note"] != nil {
		t.Fatalf("what is in the directory = %v", out)
	}
	if entries[0]["type"] != "directory" || entries[0]["owner"] != "app:staff" || entries[0]["mode"] != "rwxr-xr-x" || entries[0]["modified"] != "2026-10-09T08:00:00" || entries[2]["target"] != "releases/3" || acctest.Num(t, entries[1]["size"], "size") != 6 || entries[1]["target"] != nil {
		t.Errorf("an entry = %v, %v, %v", entries[0], entries[1], entries[2])
	}
	if asked := dock.Last(call); asked.Query.Get("path") != "/etc/app" || asked.Query.Get("env") != "1" {
		t.Errorf("the listing was asked with %v", asked.Query)
	}

	// left out, the path is the root; and only so many are listed
	out = suite.Call(t, "container_files", map[string]any{"container": "web-front-1", "limit": 1})
	if dock.Last(call).Query.Get("path") != "/" || len(acctest.Rows(t, out["entries"], "entries")) != 1 || !strings.Contains(acctest.Str(out["note"]), "the directory has 4 entries and the first 1 are listed") {
		t.Errorf("the root, one entry asked for = %v, asked with %v", out, dock.Last(call).Query)
	}

	// Dockhand answers a path that is not there as ls failing, which is said for what it is
	dock.Fail(call, http.StatusInternalServerError, `OCI runtime exec failed: exec: "/usr/sbin/ls": no such file or directory`)
	if msg := suite.CallErr(t, "container_files", map[string]any{"container": "web-front-1", "path": "/nope"}); !strings.Contains(msg, "/nope in web-front-1 could not be listed, which is what a path that is not there or is no directory answers") {
		t.Errorf("a path that is not there = %q", msg)
	}
	if msg := suite.CallErr(t, "container_files", map[string]any{"container": "stopped"}); !strings.Contains(msg, "its files can be listed only of a container that is running") {
		t.Errorf("a container that is not running = %q", msg)
	}
	if msg := suite.CallErr(t, "container_files", map[string]any{"container": "web-front-1", "limit": -2}); !strings.Contains(msg, "limit is -2") {
		t.Errorf("a limit below nothing = %q", msg)
	}
}

func TestContainerRead(t *testing.T) {
	t.Parallel()

	const call = "GET /api/containers/" + frontID + "/files/content"

	dock := twoHosts(t)
	dock.Answer(call, `{"content":"listen: 80\ndb_password: hunter2\nmode: demo\n","path":"/etc/app/app.yaml"}`)
	suite := session(t, dock, Options{})
	args := map[string]any{"container": "web-front-1", "path": "etc/app/app.yaml"}

	out := suite.Call(t, "container_read", args)
	if out["env"] != "alpha" || out["container"] != "web-front-1" || out["path"] != "/etc/app/app.yaml" || out["content"] != "listen: 80\ndb_password: ***\nmode: demo\n" || acctest.Num(t, out["hidden"], "hidden") != 1 || out["truncated"] != nil || !strings.Contains(acctest.Str(out["note"]), "1 secret is hidden") {
		t.Errorf("a file read = %v", out)
	}
	if asked := dock.Last(call); asked.Query.Get("path") != "/etc/app/app.yaml" || asked.Query.Get("env") != "1" {
		t.Errorf("the file was asked for with %v", asked.Query)
	}
	args["reveal_secrets"] = true
	if shown := suite.Call(t, "container_read", args); !strings.Contains(acctest.Str(shown["content"]), "db_password: hunter2") || shown["hidden"] != nil || shown["note"] != nil {
		t.Errorf("with reveal_secrets = %v", shown)
	}
	// its start alone when it is longer than asked for, with how long the whole is
	args["max_chars"] = 6
	if cut := suite.Call(t, "container_read", args); cut["content"] != "listen" || acctest.Num(t, cut["chars"], "chars") != 43 || !acctest.IsBool(cut["truncated"], true) || !strings.Contains(acctest.Str(cut["note"]), "the file is 43 characters and its first 6 are given") {
		t.Errorf("cut short = %v", cut)
	}
	delete(args, "max_chars")

	// what is not text is refused rather than shown as noise
	dock.Answer(call, `{"content":"\u0000\u0001binary","path":"/bin/app"}`)
	if msg := suite.CallErr(t, "container_read", args); !strings.Contains(msg, "is not a text file, so it is not shown") {
		t.Errorf("a file that is not text = %q", msg)
	}
	for status, want := range map[int]string{
		http.StatusNotFound:              "web-front-1 has no file /etc/app/app.yaml",
		http.StatusBadRequest:            "/etc/app/app.yaml in web-front-1 is a directory: container_files lists it",
		http.StatusRequestEntityTooLarge: "/etc/app/app.yaml in web-front-1 is larger than the 1 MB Dockhand reads of a file",
		http.StatusInternalServerError:   "/etc/app/app.yaml in web-front-1 could not be read",
	} {
		dock.Fail(call, status, "no")
		if msg := suite.CallErr(t, "container_read", args); !strings.Contains(msg, want) {
			t.Errorf("HTTP %d = %q, want %q", status, msg, want)
		}
	}

	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"container": "web-front-1", "path": " "}, "path is required"},
		{map[string]any{"container": "web-front-1", "path": "/x", "max_chars": -1}, "max_chars is -1"},
		{map[string]any{"container": "done", "path": "/x"}, "a file can be read only of a container that is running"},
	} {
		if msg := suite.CallErr(t, "container_read", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("container_read %v = %q, want %q", tc.args, msg, tc.want)
		}
	}
}

func TestContainerSizes(t *testing.T) {
	t.Parallel()

	const call = "GET /api/containers/sizes"
	dock := twoHosts(t)
	dock.AnswerWhen(call, "env", "1", `{"`+frontID+`":{"sizeRw":1048576,"sizeRootFs":53048576},"`+crashedID+`":{"sizeRw":734003200,"sizeRootFs":740147172},"`+looseID+`":{"sizeRw":0,"sizeRootFs":6143972},"7777777777770000000000000000000000000000000000000000000000000000":{"sizeRw":5,"sizeRootFs":5}}`)
	dock.AnswerWhen(call, "env", "2", `{"1111111111118888888888888888888888888888888888888888888888888888":{"sizeRw":2500000,"sizeRootFs":8643972}}`)
	suite := session(t, dock, Options{})

	// every host's, the one that has written most first, and a stopped one among them
	out := suite.Call(t, "container_sizes", nil)
	rows := acctest.Rows(t, out["containers"], "containers")
	if acctest.Num(t, out["total"], "total") != 4 || len(rows) != 4 || out["note"] != nil || out["failed"] != nil {
		t.Fatalf("the sizes = %v", out)
	}
	if first := rows[0]; first["env"] != "alpha" || first["name"] != "crashed" || first["state"] != "exited" || acctest.Decimal(t, first["written_mb"], "written_mb") != 734 || acctest.Decimal(t, first["total_mb"], "total_mb") != 740.1 {
		t.Errorf("the container that has written most = %v", first)
	}
	if rows[1]["name"] != "solo" || rows[1]["env"] != "beta" || rows[2]["name"] != "web-front-1" || rows[3]["name"] != "loose" {
		t.Errorf("the order = %v", rows)
	}

	// one host's, cut to the first few
	out = suite.Call(t, "container_sizes", map[string]any{"env": "alpha", "limit": 2})
	if rows = acctest.Rows(t, out["containers"], "containers"); acctest.Num(t, out["total"], "total") != 3 || len(rows) != 2 || !strings.Contains(acctest.Str(out["note"]), "3 containers were measured and the 2 that have written most are listed") || len(dock.Requests(call)) != 3 {
		t.Errorf("one host's sizes, limited = %v", out)
	}

	// a host that cannot be asked, for either of the two things it is asked
	dock.FailWhen(call, "env", "2", http.StatusInternalServerError, "Failed to get container sizes")
	if out = suite.Call(t, "container_sizes", nil); len(acctest.Rows(t, out["failed"], "failed")) != 1 || acctest.Num(t, out["total"], "total") != 3 || !strings.Contains(acctest.Str(out["note"]), "1 of the 2 environments could not be asked") {
		t.Errorf("one host down = %v", out)
	}
	dock.FailWhen("GET /api/containers", "env", "1", http.StatusInternalServerError, "Failed to list containers")
	if msg := suite.CallErr(t, "container_sizes", nil); !strings.Contains(msg, "no environment could be asked what its containers take") {
		t.Errorf("every host down = %q", msg)
	}
	if msg := suite.CallErr(t, "container_sizes", map[string]any{"limit": -1}); !strings.Contains(msg, "limit is -1") {
		t.Errorf("a limit below nothing = %q", msg)
	}
	if msg := suite.CallErr(t, "container_sizes", map[string]any{"env": "nope"}); !strings.Contains(msg, "nope") {
		t.Errorf("an environment there is none of = %q", msg)
	}
}
