package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// The volumes of the two canned hosts. On alpha: one a running container of the web stack mounts, one a stopped container mounts, one nothing mounts,
// and one nothing mounts that is a mount of a network share, with the share's password in what its driver was told. On beta, a volume by the name
// alpha has one under.
const (
	webData     = `{"name":"web_data","driver":"local","mountpoint":"/var/lib/docker/volumes/web_data/_data","scope":"local","created":"2026-10-03T04:00:00Z","labels":{"com.docker.compose.project":"web","com.docker.compose.volume":"data"},"usedBy":[{"containerId":"aaaaaaaaaaaa1111111111111111111111111111111111111111111111111111","containerName":"web-front-1"}]}`
	scratchData = `{"name":"scratch","driver":"local","mountpoint":"/var/lib/docker/volumes/scratch/_data","scope":"local","created":"2026-10-02T04:00:00Z","labels":{},"usedBy":[{"containerId":"eeeeeeeeeeee5555555555555555555555555555555555555555555555555555","containerName":"stopped"}]}`
	spareData   = `{"name":"spare","driver":"local","mountpoint":"/var/lib/docker/volumes/spare/_data","scope":"local","created":"2026-10-01T04:00:00Z","labels":{"backup.token":"not-a-real-token"},"usedBy":[]}`
	shareData   = `{"name":"share","driver":"local","mountpoint":"/var/lib/docker/volumes/share/_data","scope":"local","created":"2026-09-30T04:00:00Z","labels":{},"usedBy":[]}`

	alphaVolumes = `[` + webData + `,` + scratchData + `,` + spareData + `,` + shareData + `]`
	betaVolumes  = `[{"name":"web_data","driver":"local","mountpoint":"/var/lib/docker/volumes/web_data/_data","scope":"local","created":"2026-10-03T05:00:00Z","labels":{},"usedBy":[{"containerId":"1111111111118888888888888888888888888888888888888888888888888888","containerName":"solo"}]}]`

	shareInspect = `{"CreatedAt":"2026-09-30T04:00:00Z","Driver":"local","Labels":null,"Mountpoint":"/var/lib/docker/volumes/share/_data","Name":"share","Options":{"type":"cifs","device":"//nas.example/media","o":"addr=nas.example,username=media,password=not-a-real-password,vers=3.0"},"Scope":"local"}`
)

// withVolumes is the two canned hosts with their volumes as well.
func withVolumes(t *testing.T) *dockhandtest.Server {
	t.Helper()

	dock := twoHosts(t)
	dock.AnswerWhen("GET /api/volumes", "env", "1", alphaVolumes)
	dock.AnswerWhen("GET /api/volumes", "env", "2", betaVolumes)

	return dock
}

func TestVolumeList(t *testing.T) {
	t.Parallel()

	suite := session(t, withVolumes(t), Options{})

	out := suite.Call(t, "volume_list", nil)
	rows := acctest.Rows(t, out["volumes"], "volumes")
	order := make([]string, 0, len(rows))
	for _, row := range rows {
		order = append(order, acctest.Str(row["env"])+" "+acctest.Str(row["name"]))
	}
	if got := strings.Join(order, "|"); got != "alpha scratch|alpha share|alpha spare|alpha web_data|beta web_data" {
		t.Errorf("the volumes, in order = %s", got)
	}
	if acctest.Num(t, out["total"], "total") != 5 || acctest.Num(t, out["unused"], "unused") != 2 || out["failed"] != nil || out["note"] != nil {
		t.Errorf("the counts = %v", out)
	}
	if web := rows[3]; web["driver"] != "local" || web["stack"] != "web" || web["created"] != "2026-10-03T04:00:00Z" || strings.Join(acctest.Strs(t, web["used_by"], "used_by"), "|") != "web-front-1" {
		t.Errorf("a stack's volume = %v", web)
	}
	if spare := rows[2]; spare["stack"] != nil || len(acctest.Strs(t, spare["used_by"], "used_by")) != 0 {
		t.Errorf("a volume nothing mounts = %v", spare)
	}

	names := func(args map[string]any) string {
		listed := acctest.Rows(t, suite.Call(t, "volume_list", args)["volumes"], "volumes")
		got := make([]string, 0, len(listed))
		for _, row := range listed {
			got = append(got, acctest.Str(row["env"])+" "+acctest.Str(row["name"]))
		}

		return strings.Join(got, "|")
	}
	for name, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"one host":              {map[string]any{"env": "beta"}, "beta web_data"},
		"part of a name":        {map[string]any{"name": "WEB"}, "alpha web_data|beta web_data"},
		"a stack's":             {map[string]any{"stack": "web"}, "alpha web_data"},
		"what nothing mounts":   {map[string]any{"unused": true}, "alpha share|alpha spare"},
		"no more than a number": {map[string]any{"limit": 1}, "alpha scratch"},
	} {
		if got := names(tc.args); got != tc.want {
			t.Errorf("%s = %q, want %q", name, got, tc.want)
		}
	}
	if cut := suite.Call(t, "volume_list", map[string]any{"limit": 1}); acctest.Num(t, cut["total"], "total") != 5 || !strings.Contains(acctest.Str(cut["note"]), "5 volumes matched and the first 1 are listed") {
		t.Errorf("cut at a limit = %v", cut)
	}
	if msg := suite.CallErr(t, "volume_list", map[string]any{"limit": -1}); !strings.Contains(msg, "limit is -1") {
		t.Errorf("a limit below none = %q", msg)
	}
}

func TestVolumeListWithAHostDown(t *testing.T) {
	t.Parallel()

	dock := withVolumes(t)
	dock.FailWhen("GET /api/volumes", "env", "2", http.StatusInternalServerError, "Failed to connect to the agent")
	suite := session(t, dock, Options{})

	out := suite.Call(t, "volume_list", nil)
	if failed := acctest.Rows(t, out["failed"], "failed"); len(failed) != 1 || failed[0]["env"] != "beta" || acctest.Num(t, out["total"], "total") != 4 || !strings.Contains(acctest.Str(out["note"]), "1 of the 2 environments could not be asked") {
		t.Errorf("with one host down = %v", out)
	}
	dock.FailWhen("GET /api/volumes", "env", "1", http.StatusInternalServerError, "Failed to list volumes")
	if msg := suite.CallErr(t, "volume_list", nil); !strings.Contains(msg, "no environment could be asked for its volumes") {
		t.Errorf("with both down = %q", msg)
	}
}

func TestVolumeGet(t *testing.T) {
	t.Parallel()

	dock := withVolumes(t)
	dock.Answer("GET /api/volumes/scratch/inspect", `{"CreatedAt":"2026-10-02T04:00:00Z","Driver":"local","Labels":null,"Mountpoint":"/var/lib/docker/volumes/scratch/_data","Name":"scratch","Options":null,"Scope":"local"}`)
	dock.Answer("GET /api/volumes/share/inspect", shareInspect)
	dock.Answer("GET /api/volumes/spare/inspect", `{"CreatedAt":"2026-10-01T04:00:00Z","Driver":"local","Labels":{"backup.token":"not-a-real-token"},"Mountpoint":"/var/lib/docker/volumes/spare/_data","Name":"spare","Options":null,"Scope":"local"}`)
	suite := session(t, dock, Options{})

	// who mounts it, with the state the volume's own record does not carry
	out := suite.Call(t, "volume_get", map[string]any{"volume": "scratch"})
	if out["env"] != "alpha" || out["name"] != "scratch" || out["mountpoint"] != "/var/lib/docker/volumes/scratch/_data" || out["scope"] != "local" || strings.Join(acctest.Strs(t, out["used_by"], "used_by"), "|") != "stopped (exited)" || out["options"] != nil || out["labels"] != nil || out["note"] != nil {
		t.Errorf("volume_get = %v", out)
	}
	if made := dock.Last("GET /api/volumes/scratch/inspect"); made.Query.Get("env") != "1" {
		t.Errorf("its record was asked on environment %q", made.Query.Get("env"))
	}

	// a share's password among what its driver was told is hidden, and the rest of the line is kept
	out = suite.Call(t, "volume_get", map[string]any{"volume": "share"})
	options := acctest.Object(t, out["options"], "options")
	if options["o"] != "addr=nas.example,username=media,password="+masked+",vers=3.0" || options["device"] != "//nas.example/media" || !strings.Contains(acctest.Str(out["note"]), "1 secret is hidden") || strings.Contains(flat(out), "not-a-real") {
		t.Errorf("a share's options = %v, note %v", options, out["note"])
	}
	// and a label named as a secret
	out = suite.Call(t, "volume_get", map[string]any{"volume": "spare"})
	if acctest.Object(t, out["labels"], "labels")["backup.token"] != masked || strings.Contains(flat(out), "not-a-real") {
		t.Errorf("a label that is a secret = %v", out)
	}
	// shown when asked for
	out = suite.Call(t, "volume_get", map[string]any{"volume": "share", "reveal_secrets": true})
	if acctest.Object(t, out["options"], "options")["o"] != "addr=nas.example,username=media,password=not-a-real-password,vers=3.0" || out["note"] != nil {
		t.Errorf("a share's options, revealed = %v", out)
	}

	// the rest of its record failing is said, and what the list gave is still given
	dock.Fail("GET /api/volumes/scratch/inspect", http.StatusInternalServerError, "Failed to inspect volume")
	if out = suite.Call(t, "volume_get", map[string]any{"volume": "scratch"}); out["name"] != "scratch" || !strings.Contains(acctest.Str(out["note"]), "what its driver was told could not be read") {
		t.Errorf("with its record unreadable = %v", out)
	}
	// the containers unreadable: who mounts it is still named, with no state to give
	dock.FailWhen("GET /api/containers", "env", "1", http.StatusInternalServerError, "Failed to list containers")
	if out = suite.Call(t, "volume_get", map[string]any{"volume": "scratch"}); strings.Join(acctest.Strs(t, out["used_by"], "used_by"), "|") != "stopped" {
		t.Errorf("with the containers unreadable = %v", out)
	}
}

// A volume is found by its name in full and nothing less: it holds data, and is not acted on for having a name like the one asked for.
func TestVolumeFinding(t *testing.T) {
	t.Parallel()

	dock := withVolumes(t)
	dock.Answer("GET /api/volumes/web_data/inspect", `{"CreatedAt":"2026-10-03T05:00:00Z","Driver":"local","Labels":null,"Mountpoint":"/var/lib/docker/volumes/web_data/_data","Name":"web_data","Options":null,"Scope":"local"}`)
	suite := session(t, dock, Options{})

	if out := suite.Call(t, "volume_get", map[string]any{"volume": " web_data ", "env": "beta"}); out["env"] != "beta" || strings.Join(acctest.Strs(t, out["used_by"], "used_by"), "|") != "solo (running)" {
		t.Errorf("one of two by the same name, with its host said = %v", out)
	}
	for name, tc := range map[string]struct {
		args map[string]any
		want []string
	}{
		"none":                    {map[string]any{"volume": " "}, []string{"volume is required"}},
		"one name on two hosts":   {map[string]any{"volume": "web_data"}, []string{`a volume named "web_data" on each of alpha, beta`, "say which with env"}},
		"part of a name":          {map[string]any{"volume": "web"}, []string{`no volume is named "web" on alpha, beta`, "volume_list"}},
		"a name in another case":  {map[string]any{"volume": "SPARE"}, []string{`no volume is named "SPARE"`}},
		"one that is not there":   {map[string]any{"volume": "nope", "env": "alpha"}, []string{`no volume is named "nope" on alpha:`}},
		"a host there is none of": {map[string]any{"volume": "spare", "env": "gamma"}, []string{`no environment is named or numbered "gamma"`}},
	} {
		msg := suite.CallErr(t, "volume_get", tc.args)
		for _, want := range tc.want {
			if !strings.Contains(msg, want) {
				t.Errorf("%s = %q, want %q in it", name, msg, want)
			}
		}
	}

	dock.FailWhen("GET /api/volumes", "env", "2", http.StatusInternalServerError, "Failed to connect to the agent")
	if msg := suite.CallErr(t, "volume_get", map[string]any{"volume": "nope"}); !strings.Contains(msg, "not every environment could be asked") || !strings.Contains(msg, "beta: ") {
		t.Errorf("not found with a host down = %q", msg)
	}
}

func TestVolumeCreate(t *testing.T) {
	t.Parallel()

	const call = "POST /api/volumes"
	made := `{"name":"fresh","driver":"local","mountpoint":"/var/lib/docker/volumes/fresh/_data","scope":"local","created":"2026-10-10T04:00:00Z","labels":{"team":"web"},"usedBy":[]}`

	dock := withVolumes(t)
	dock.Answer(call, `{"success":true,"name":"fresh"}`)
	dock.Then(call, func(dockhandtest.Request) {
		dock.AnswerWhen("GET /api/volumes", "env", "1", `[`+webData+`,`+made+`]`)
	})
	suite := session(t, dock, everything)

	// said first, and nothing is asked of Dockhand
	dry := suite.Call(t, "volume_create", map[string]any{"env": "alpha", "name": "fresh", "dry_run": true})
	if !acctest.IsBool(dry["dry_run"], true) || !acctest.IsBool(dry["created"], false) || dry["volume"] != nil || !strings.Contains(acctest.Str(dry["note"]), "an empty volume named fresh is made on alpha") || len(dock.Requests(call)) != 0 {
		t.Errorf("a dry run = %v", dry)
	}

	out := suite.Call(t, "volume_create", map[string]any{"env": "alpha", "name": " fresh ", "labels": map[string]any{"team": "web"}})
	row := acctest.Object(t, out["volume"], "volume")
	if out["env"] != "alpha" || !acctest.IsBool(out["created"], true) || row["name"] != "fresh" || row["driver"] != "local" || len(acctest.Strs(t, row["used_by"], "used_by")) != 0 || out["note"] != nil {
		t.Errorf("volume_create = %v", out)
	}
	if sent := dock.Last(call); sent.Query.Get("env") != "1" || sent.Body != `{"labels":{"team":"web"},"name":"fresh"}` {
		t.Errorf("the volume was asked for on environment %q with %s", sent.Query.Get("env"), sent.Body)
	}

	// a name the host has a volume under is refused, not answered as made
	asked := len(dock.Requests(call))
	if msg := suite.CallErr(t, "volume_create", map[string]any{"env": "alpha", "name": "web_data"}); !strings.Contains(msg, "alpha already has a volume named web_data") || len(dock.Requests(call)) != asked {
		t.Errorf("a name that is taken = %q", msg)
	}
	for name, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"no name":           {map[string]any{"env": "alpha", "name": " "}, `name is " "`},
		"one letter":        {map[string]any{"env": "alpha", "name": "a"}, `name is "a"`},
		"a slash":           {map[string]any{"env": "alpha", "name": "a/b"}, `name is "a/b"`},
		"a dash to start":   {map[string]any{"env": "alpha", "name": "-ab"}, `name is "-ab"`},
		"no host":           {map[string]any{"name": "fresh2"}, "env is required"},
		"a host of no name": {map[string]any{"env": "gamma", "name": "fresh2"}, `no environment is named or numbered "gamma"`},
	} {
		if msg := suite.CallErr(t, "volume_create", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", name, msg, tc.want)
		}
	}
}

func TestVolumeCreateThatGoesWrong(t *testing.T) {
	t.Parallel()

	const call = "POST /api/volumes"
	dock := withVolumes(t)
	suite := session(t, dock, everything)
	args := map[string]any{"env": "alpha", "name": "fresh", "driver": "nfs"}

	dock.Fail(call, http.StatusInternalServerError, "Failed to create volume")
	if msg := suite.CallErr(t, "volume_create", args); !strings.Contains(msg, "the volume fresh was not made on alpha") || !strings.Contains(msg, "Failed to create volume") {
		t.Errorf("a volume Dockhand fails to make = %q", msg)
	}
	if sent := dock.Last(call); sent.Body != `{"driver":"nfs","name":"fresh"}` {
		t.Errorf("a driver was sent as %s", sent.Body)
	}
	dock.Answer(call, `{"success":false}`)
	if msg := suite.CallErr(t, "volume_create", args); !strings.Contains(msg, "without its being said to have succeeded") {
		t.Errorf("answered without success = %q", msg)
	}
	dock.Answer(call, `{"success":true,"name":"fresh"}`)
	if msg := suite.CallErr(t, "volume_create", args); !strings.Contains(msg, "answered as done, and alpha does not list it") {
		t.Errorf("made and not listed = %q", msg)
	}
	dock.Then(call, func(dockhandtest.Request) {
		dock.FailWhen("GET /api/volumes", "env", "1", http.StatusInternalServerError, "Failed to list volumes")
	})
	if out := suite.Call(t, "volume_create", args); !acctest.IsBool(out["created"], true) || !strings.Contains(acctest.Str(out["note"]), "it was made, and the volumes could not be listed afterwards") {
		t.Errorf("made and not read back = %v", out)
	}
	dock.Then(call, nil)
	if msg := suite.CallErr(t, "volume_create", args); !strings.Contains(msg, "could not be listed, so nothing was made") {
		t.Errorf("a host that cannot be read = %q", msg)
	}
}

func TestVolumeDelete(t *testing.T) {
	t.Parallel()

	const call = "DELETE /api/volumes/spare"
	dock := withVolumes(t)
	suite := session(t, dock, everything)

	// without confirm: what would go, and nothing is asked of Dockhand
	out := suite.Call(t, "volume_delete", map[string]any{"volume": "spare"})
	if !acctest.IsBool(out["removed"], false) || out["env"] != "alpha" || out["name"] != "spare" || strings.Join(acctest.Strs(t, out["would_go"], "would_go"), "|") != "the volume spare on alpha, and every file in it" || !strings.Contains(acctest.Str(out["note"]), "cannot be got back") || len(dock.Requests(call)) != 0 {
		t.Errorf("without confirm = %v", out)
	}

	dock.Answer(call, `{"success":true}`)
	dock.Then(call, func(dockhandtest.Request) {
		dock.AnswerWhen("GET /api/volumes", "env", "1", `[`+webData+`,`+scratchData+`,`+shareData+`]`)
	})
	out = suite.Call(t, "volume_delete", map[string]any{"volume": "spare", "confirm": true})
	if !acctest.IsBool(out["removed"], true) || out["would_go"] != nil || out["note"] != nil || dock.Last(call).Query.Get("env") != "1" || dock.Last(call).Query.Get("force") != "" {
		t.Errorf("with confirm = %v", out)
	}

	// one a container mounts is refused, running or stopped, confirmed or not, with the containers named
	for volume, users := range map[string]string{"scratch": "stopped (exited)", "web_data": "web-front-1 (running)"} {
		msg := suite.CallErr(t, "volume_delete", map[string]any{"volume": volume, "env": "alpha", "confirm": true})
		if !strings.Contains(msg, "the volume "+volume+" on alpha is mounted by 1 containers ("+users+")") || !strings.Contains(msg, "remove them first") {
			t.Errorf("removing %s = %q", volume, msg)
		}
	}
}

func TestVolumeDeleteThatGoesWrong(t *testing.T) {
	t.Parallel()

	const call = "DELETE /api/volumes/spare"
	dock := withVolumes(t)
	suite := session(t, dock, everything)
	args := map[string]any{"volume": "spare", "confirm": true}

	dock.Fail(call, http.StatusConflict, "Failed to remove volume")
	if msg := suite.CallErr(t, "volume_delete", args); !strings.Contains(msg, "the volume spare was not removed") || !strings.Contains(msg, "Failed to remove volume") {
		t.Errorf("a removal Dockhand refuses = %q", msg)
	}
	dock.Answer(call, `{"success":false}`)
	if msg := suite.CallErr(t, "volume_delete", args); !strings.Contains(msg, "without its being said to have succeeded") {
		t.Errorf("answered without success = %q", msg)
	}
	dock.Answer(call, `{"success":true}`)
	if msg := suite.CallErr(t, "volume_delete", args); !strings.Contains(msg, "answered as done, and it is still listed") {
		t.Errorf("a removal that did not take = %q", msg)
	}
	dock.Then(call, func(dockhandtest.Request) {
		dock.FailWhen("GET /api/volumes", "env", "1", http.StatusInternalServerError, "Failed to list volumes")
	})
	if out := suite.Call(t, "volume_delete", args); !acctest.IsBool(out["removed"], false) || !strings.Contains(acctest.Str(out["note"]), "it was removed, and the volumes could not be listed afterwards") {
		t.Errorf("removed and not read back = %v", out)
	}
}

func TestVolumePrune(t *testing.T) {
	t.Parallel()

	const call = "POST /api/prune/volumes"
	dock := withVolumes(t)
	suite := session(t, dock, everything)

	// without confirm: every volume nothing mounts, named ones too, and nothing is asked of Dockhand
	out := suite.Call(t, "volume_prune", map[string]any{"env": "alpha"})
	if strings.Join(acctest.Strs(t, out["would_go"], "would_go"), "|") != "share|spare" || len(acctest.Strs(t, out["removed"], "removed")) != 0 || !strings.Contains(acctest.Str(out["note"]), "cannot be got back") || len(dock.Requests(call)) != 0 {
		t.Errorf("without confirm = %v", out)
	}

	dock.Answer(call, `{"success":true,"result":{"VolumesDeleted":["spare","share"],"SpaceReclaimed":73400000}}`)
	out = suite.Call(t, "volume_prune", map[string]any{"env": "alpha", "confirm": true})
	if strings.Join(acctest.Strs(t, out["removed"], "removed"), "|") != "share|spare" || acctest.Decimal(t, out["freed_mb"], "freed_mb") != 73.4 || out["would_go"] != nil || out["note"] != nil || dock.Last(call).Query.Get("env") != "1" {
		t.Errorf("with confirm = %v", out)
	}
	// what was unused came to be used in between
	dock.Answer(call, `{"success":true,"result":{"VolumesDeleted":[],"SpaceReclaimed":0}}`)
	if out = suite.Call(t, "volume_prune", map[string]any{"env": "alpha", "confirm": true}); len(acctest.Strs(t, out["removed"], "removed")) != 0 || !strings.Contains(acctest.Str(out["note"]), "nothing was removed") {
		t.Errorf("a pruning that took nothing = %v", out)
	}

	// a host where every volume is mounted has nothing to remove, and is not asked to
	asked := len(dock.Requests(call))
	if out = suite.Call(t, "volume_prune", map[string]any{"env": "beta", "confirm": true}); !strings.Contains(acctest.Str(out["note"]), "every volume on the host is mounted by a container") || len(dock.Requests(call)) != asked {
		t.Errorf("nothing unused = %v", out)
	}

	if msg := suite.CallErr(t, "volume_prune", map[string]any{"confirm": true}); !strings.Contains(msg, "env is required") {
		t.Errorf("no host = %q", msg)
	}
	args := map[string]any{"env": "alpha", "confirm": true}
	dock.Fail(call, http.StatusInternalServerError, "Failed to prune volumes")
	if msg := suite.CallErr(t, "volume_prune", args); !strings.Contains(msg, "the volumes of alpha were not pruned") {
		t.Errorf("a pruning Dockhand fails = %q", msg)
	}
	dock.Answer(call, `{"success":true}`)
	if msg := suite.CallErr(t, "volume_prune", args); !strings.Contains(msg, "without its being said to have succeeded") {
		t.Errorf("answered with no report = %q", msg)
	}
	dock.FailWhen("GET /api/volumes", "env", "1", http.StatusInternalServerError, "Failed to list volumes")
	if msg := suite.CallErr(t, "volume_prune", args); !strings.Contains(msg, "could not be listed, so nothing was removed") {
		t.Errorf("a host that cannot be read = %q", msg)
	}
}

func TestVolumeName(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]bool{"ab": true, "web_data": true, "a.b-c_d": true, "9lives": true, "": false, "a": false, "_ab": false, ".ab": false, "a b": false, "a/b": false, "a:b": false} {
		if got := volumeName(name); got != want {
			t.Errorf("volumeName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestVolumeClone(t *testing.T) {
	t.Parallel()

	const call = "POST /api/volumes/web_data/clone"
	copied := strings.Replace(webData, `"name":"web_data"`, `"name":"web_copy"`, 1)
	copied = strings.Replace(copied, `"usedBy":[{"containerId":"aaaaaaaaaaaa1111111111111111111111111111111111111111111111111111","containerName":"web-front-1"}]`, `"usedBy":[]`, 1)

	dock := withVolumes(t)
	dock.Answer(call, `{"success":true,"name":"web_copy"}`)
	dock.Then(call, func(dockhandtest.Request) {
		dock.AnswerWhen("GET /api/volumes", "env", "1", `[`+webData+`,`+copied+`]`)
	})
	suite := session(t, dock, everything)

	dry := suite.Call(t, "volume_clone", map[string]any{"volume": "web_data", "env": "alpha", "as": "web_copy", "dry_run": true})
	if !acctest.IsBool(dry["dry_run"], true) || !acctest.IsBool(dry["cloned"], false) || dry["volume"] != nil || !strings.Contains(acctest.Str(dry["note"]), "web_data is copied into a new volume named web_copy on alpha") || len(dock.Requests(call)) != 0 {
		t.Errorf("a dry run = %v", dry)
	}

	out := suite.Call(t, "volume_clone", map[string]any{"volume": "web_data", "env": "alpha", "as": " web_copy "})
	row := acctest.Object(t, out["volume"], "volume")
	if out["env"] != "alpha" || out["from"] != "web_data" || !acctest.IsBool(out["cloned"], true) || row["name"] != "web_copy" || row["stack"] != "web" || len(acctest.Strs(t, row["used_by"], "used_by")) != 0 || out["note"] != nil {
		t.Errorf("volume_clone = %v", out)
	}
	if sent := dock.Last(call); sent.Query.Get("env") != "1" || sent.Body != `{"name":"web_copy"}` {
		t.Errorf("the copy was asked for on environment %q with %s", sent.Query.Get("env"), sent.Body)
	}

	// a name the host has a volume under is refused: Dockhand would copy into what is there
	dock.AnswerWhen("GET /api/volumes", "env", "1", alphaVolumes)
	asked := len(dock.Requests(call))
	for name, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"a name that is taken":  {map[string]any{"volume": "web_data", "env": "alpha", "as": "spare"}, "alpha already has a volume named spare"},
		"its own name":          {map[string]any{"volume": "web_data", "env": "alpha", "as": "web_data"}, "alpha already has a volume named web_data"},
		"no new name":           {map[string]any{"volume": "web_data", "env": "alpha", "as": " "}, `as is " "`},
		"a name Docker refuses": {map[string]any{"volume": "web_data", "env": "alpha", "as": "a/b"}, `as is "a/b"`},
		"a volume of none":      {map[string]any{"volume": "nope", "as": "copy"}, `no volume is named "nope"`},
	} {
		if msg := suite.CallErr(t, "volume_clone", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", name, msg, tc.want)
		}
	}
	if len(dock.Requests(call)) != asked {
		t.Error("a copy that was refused was asked of Dockhand all the same")
	}
}

func TestVolumeCloneThatGoesWrong(t *testing.T) {
	t.Parallel()

	const call = "POST /api/volumes/spare/clone"
	dock := withVolumes(t)
	suite := session(t, dock, everything)
	args := map[string]any{"volume": "spare", "as": "spare_copy"}

	dock.Fail(call, http.StatusInternalServerError, "Failed to clone volume")
	if msg := suite.CallErr(t, "volume_clone", args); !strings.Contains(msg, "spare was not copied into spare_copy") || !strings.Contains(msg, "Failed to clone volume") {
		t.Errorf("a copy Dockhand fails = %q", msg)
	}
	dock.Answer(call, `{"success":false}`)
	if msg := suite.CallErr(t, "volume_clone", args); !strings.Contains(msg, "without its being said to have succeeded") {
		t.Errorf("answered without success = %q", msg)
	}
	dock.Answer(call, `{"success":true,"name":"spare_copy"}`)
	if msg := suite.CallErr(t, "volume_clone", args); !strings.Contains(msg, "answered as done, and alpha does not list it") {
		t.Errorf("copied and not listed = %q", msg)
	}
	dock.Then(call, func(dockhandtest.Request) {
		dock.FailWhen("GET /api/volumes", "env", "1", http.StatusInternalServerError, "Failed to list volumes")
	})
	if out := suite.Call(t, "volume_clone", args); !acctest.IsBool(out["cloned"], true) || !strings.Contains(acctest.Str(out["note"]), "it was copied, and the volumes could not be listed afterwards") {
		t.Errorf("copied and not read back = %v", out)
	}
}

// What a volume holds is listed through a helper container Dockhand starts on the host, and the helper is taken away again whether the listing worked
// or not.
func TestVolumeFiles(t *testing.T) {
	t.Parallel()

	const browse, release = "GET /api/volumes/spare/browse", "POST /api/volumes/spare/browse/release"
	dock := withVolumes(t)
	dock.Answer(browse, `{"path":"/","entries":[{"name":"note.txt","type":"file","size":5,"permissions":"rw-r--r--","owner":"root","group":"root","modified":"2026-10-10T09:39:00","readonly":false},{"name":"sub","type":"directory","size":4096,"permissions":"rwxr-xr-x","owner":"app","group":"staff","modified":"2026-10-10T09:39:00","readonly":false},{"name":".env","type":"file","size":32,"permissions":"rw-------","owner":"","group":"","modified":"2026-10-10T09:40:00","readonly":false},{"name":"link","type":"symlink","size":8,"permissions":"rwxrwxrwx","owner":"root","group":"root","modified":"2026-10-10T09:40:00","readonly":false}],"usage":[],"isInUse":false,"helperId":"h1"}`)
	dock.Answer(release, `{"success":true}`)
	suite := session(t, dock, everything)

	// the top, when no directory is said: directories first, then by name
	out := suite.Call(t, "volume_files", map[string]any{"volume": "spare"})
	entries := acctest.Rows(t, out["entries"], "entries")
	order := make([]string, 0, len(entries))
	for _, e := range entries {
		order = append(order, acctest.Str(e["name"]))
	}
	if out["env"] != "alpha" || out["volume"] != "spare" || out["path"] != "/" || strings.Join(order, "|") != "sub|.env|link|note.txt" || out["note"] != nil {
		t.Errorf("volume_files = %v", out)
	}
	if dir := entries[0]; dir["type"] != "directory" || dir["mode"] != "rwxr-xr-x" || dir["owner"] != "app:staff" || dir["modified"] != "2026-10-10T09:39:00" || acctest.Num(t, dir["size"], "size") != 4096 {
		t.Errorf("a directory = %v", dir)
	}
	if entries[1]["owner"] != nil || entries[2]["type"] != "symlink" {
		t.Errorf("a file nobody is said to own, and a link = %v, %v", entries[1], entries[2])
	}
	if sent := dock.Last(browse); sent.Query.Get("env") != "1" || sent.Query.Get("path") != "/" {
		t.Errorf("the top was asked on environment %q as %q", sent.Query.Get("env"), sent.Query.Get("path"))
	}
	if freed := dock.Requests(release); len(freed) != 1 || freed[0].Query.Get("env") != "1" {
		t.Errorf("the helper was released %d times, want once on the volume's host", len(freed))
	}

	// a path is taken from the volume's top and never above it, however it is written
	for given, sent := range map[string]string{"sub": "/sub", "/sub/": "/sub", "sub/../sub/deep": "/sub/deep", "/../../etc": "/etc", "..": "/", "/sub\t": "/sub"} {
		suite.Call(t, "volume_files", map[string]any{"volume": "spare", "path": given})
		if got := dock.Last(browse).Query.Get("path"); got != sent {
			t.Errorf("path %q was asked as %q, want %q", given, got, sent)
		}
	}

	// asked for a file, Dockhand answers the file alone by where its helper has it: named as it is in the volume, and said to be a file
	dock.Answer(browse, `{"path":"/note.txt","entries":[{"name":"/volume/note.txt","type":"file","size":5,"permissions":"rw-r--r--","owner":"root","group":"root","modified":"2026-10-10T09:39:00","readonly":false}],"usage":[],"isInUse":false,"helperId":"h1"}`)
	out = suite.Call(t, "volume_files", map[string]any{"volume": "spare", "path": "/note.txt"})
	if one := acctest.Rows(t, out["entries"], "entries"); len(one) != 1 || one[0]["name"] != "note.txt" || !strings.Contains(acctest.Str(out["note"]), "/note.txt is a file, not a directory: volume_read reads it") {
		t.Errorf("a file asked for as a directory = %v", out)
	}
	// an empty directory is a list of nothing
	dock.Answer(browse, `{"path":"/sub","entries":[],"usage":[],"isInUse":false,"helperId":"h1"}`)
	if out = suite.Call(t, "volume_files", map[string]any{"volume": "spare", "path": "sub"}); len(acctest.Rows(t, out["entries"], "entries")) != 0 || out["path"] != "/sub" {
		t.Errorf("an empty directory = %v", out)
	}
}

func TestVolumeFilesThatGoWrong(t *testing.T) {
	t.Parallel()

	const browse, release = "GET /api/volumes/spare/browse", "POST /api/volumes/spare/browse/release"
	dock := withVolumes(t)
	dock.Answer(release, `{"success":true}`)
	suite := session(t, dock, everything)

	dock.Fail(browse, http.StatusNotFound, "Directory not found")
	if msg := suite.CallErr(t, "volume_files", map[string]any{"volume": "spare", "path": "/nope"}); !strings.Contains(msg, "the volume spare has no directory /nope") {
		t.Errorf("a directory there is none of = %q", msg)
	}
	dock.Fail(browse, http.StatusInternalServerError, "Failed to browse volume")
	if msg := suite.CallErr(t, "volume_files", map[string]any{"volume": "spare"}); !strings.Contains(msg, "the volume spare could not be looked into") || !strings.Contains(msg, "Failed to browse volume") {
		t.Errorf("a volume Dockhand cannot look into = %q", msg)
	}
	// the helper is taken away after a listing that failed as after one that worked
	if freed := len(dock.Requests(release)); freed != 2 {
		t.Errorf("after two listings that failed the helper was released %d times, want twice", freed)
	}

	// a helper that could not be taken away is said, beside what was found
	dock.Answer(browse, `{"path":"/","entries":[],"usage":[],"isInUse":false,"helperId":"h1"}`)
	dock.Fail(release, http.StatusInternalServerError, "Failed to release helper")
	if out := suite.Call(t, "volume_files", map[string]any{"volume": "spare"}); !strings.Contains(acctest.Str(out["note"]), "the helper container Dockhand started on the host to look inside the volume was not taken away") || !strings.Contains(acctest.Str(out["note"]), "Failed to release helper") {
		t.Errorf("a helper that was not released = %v", out)
	}
	dock.Answer(release, `{"success":false}`)
	if out := suite.Call(t, "volume_files", map[string]any{"volume": "spare"}); !strings.Contains(acctest.Str(out["note"]), "was not said to have been taken away") {
		t.Errorf("a helper not said to be released = %v", out)
	}

	// and with a failure, when the look failed too
	dock.Fail(release, http.StatusInternalServerError, "Failed to release helper")
	dock.Fail(browse, http.StatusNotFound, "Directory not found")
	if msg := suite.CallErr(t, "volume_files", map[string]any{"volume": "spare", "path": "/nope"}); !strings.Contains(msg, "has no directory /nope") || !strings.Contains(msg, "was not taken away") {
		t.Errorf("a look that failed with its helper left behind = %q", msg)
	}
	dock.Answer(browse, `{"path":"/note.txt","entries":[{"name":"/volume/note.txt","type":"file","size":5,"permissions":"rw-r--r--","owner":"root","group":"root","modified":"2026-10-10T09:39:00","readonly":false}],"usage":[],"isInUse":false,"helperId":"h1"}`)
	if out := suite.Call(t, "volume_files", map[string]any{"volume": "spare", "path": "/note.txt"}); !strings.Contains(acctest.Str(out["note"]), "is a file, not a directory") || !strings.Contains(acctest.Str(out["note"]), "was not taken away") {
		t.Errorf("a file asked for as a directory with its helper left behind = %v", out)
	}

	// a volume there is none of is refused before Dockhand is asked to look, since looking would make it
	looked := len(dock.Requests(browse))
	if msg := suite.CallErr(t, "volume_files", map[string]any{"volume": "nope"}); !strings.Contains(msg, `no volume is named "nope"`) || len(dock.Requests(browse)) != looked {
		t.Errorf("a volume there is none of = %q", msg)
	}
}

func TestVolumeRead(t *testing.T) {
	t.Parallel()

	const content, release = "GET /api/volumes/spare/browse/content", "POST /api/volumes/spare/browse/release"
	dock := withVolumes(t)
	dock.Answer(release, `{"success":true}`)
	dock.Answer(content, `{"content":"MODE=demo\nDB_PASSWORD=not-a-real-password\nURL=postgres://app:not-a-real-password@db/app\n","path":"/.env"}`)
	suite := session(t, dock, everything)

	// what is told for a secret by the name beside it is hidden, and the rest is as written
	out := suite.Call(t, "volume_read", map[string]any{"volume": "spare", "path": ".env"})
	if out["env"] != "alpha" || out["volume"] != "spare" || out["path"] != "/.env" || out["content"] != "MODE=demo\nDB_PASSWORD="+masked+"\nURL=postgres://app:"+masked+"@db/app\n" || acctest.Num(t, out["hidden"], "hidden") != 2 || out["truncated"] != nil || !strings.Contains(acctest.Str(out["note"]), "2 secrets are hidden") {
		t.Errorf("volume_read = %v", out)
	}
	if sent := dock.Last(content); sent.Query.Get("env") != "1" || sent.Query.Get("path") != "/.env" {
		t.Errorf("the file was asked on environment %q as %q", sent.Query.Get("env"), sent.Query.Get("path"))
	}
	if freed := len(dock.Requests(release)); freed != 1 {
		t.Errorf("the helper was released %d times, want once", freed)
	}
	// shown when asked for
	shown := suite.Call(t, "volume_read", map[string]any{"volume": "spare", "path": "/.env", "reveal_secrets": true})
	if !strings.Contains(acctest.Str(shown["content"]), "DB_PASSWORD=not-a-real-password") || shown["hidden"] != nil || shown["note"] != nil {
		t.Errorf("revealed = %v", shown)
	}

	// its start alone when it is longer than asked for, with how long the whole is
	dock.Answer(content, `{"content":"héllo wörld\n","path":"/note.txt"}`)
	cut := suite.Call(t, "volume_read", map[string]any{"volume": "spare", "path": "/note.txt", "max_chars": 5})
	if cut["content"] != "héllo" || acctest.Num(t, cut["chars"], "chars") != 12 || !acctest.IsBool(cut["truncated"], true) || !strings.Contains(acctest.Str(cut["note"]), "the file is 12 characters and its first 5 are given") {
		t.Errorf("cut short = %v", cut)
	}
	// an empty file is one with nothing in it
	dock.Answer(content, `{"content":"","path":"/empty"}`)
	if empty := suite.Call(t, "volume_read", map[string]any{"volume": "spare", "path": "/empty"}); empty["content"] != "" || acctest.Num(t, empty["chars"], "chars") != 0 {
		t.Errorf("an empty file = %v", empty)
	}
}

func TestVolumeReadThatGoesWrong(t *testing.T) {
	t.Parallel()

	const content, release = "GET /api/volumes/spare/browse/content", "POST /api/volumes/spare/browse/release"
	dock := withVolumes(t)
	dock.Answer(release, `{"success":true}`)
	suite := session(t, dock, everything)
	args := map[string]any{"volume": "spare", "path": "/data.bin"}

	// what is not text is refused rather than shown as noise: bytes that are no character, and the mark of ones that could not be read
	for name, body := range map[string]string{"a file with a zero byte": `{"content":"\u0000\u0001binary","path":"/data.bin"}`, "a file that is not UTF-8": `{"content":"bin�ary","path":"/data.bin"}`} {
		dock.Answer(content, body)
		if msg := suite.CallErr(t, "volume_read", args); !strings.Contains(msg, "/data.bin in the volume spare is not a text file") {
			t.Errorf("%s = %q", name, msg)
		}
	}

	for status, want := range map[int]string{
		http.StatusNotFound:              "the volume spare has no file /data.bin",
		http.StatusBadRequest:            "/data.bin in the volume spare is a directory: volume_files lists it",
		http.StatusRequestEntityTooLarge: "/data.bin in the volume spare is larger than the 1 MB Dockhand reads of a file",
		http.StatusInternalServerError:   "/data.bin in the volume spare could not be read",
	} {
		dock.Fail(content, status, "no")
		if msg := suite.CallErr(t, "volume_read", args); !strings.Contains(msg, want) {
			t.Errorf("HTTP %d = %q, want %q", status, msg, want)
		}
	}
	// a helper left behind after a read that failed is said with the failure
	dock.Fail(content, http.StatusInternalServerError, "no")
	dock.Fail(release, http.StatusInternalServerError, "Failed to release helper")
	if msg := suite.CallErr(t, "volume_read", args); !strings.Contains(msg, "could not be read") || !strings.Contains(msg, "the helper container Dockhand started on the host to look inside the volume was not taken away") {
		t.Errorf("a read that failed with its helper left behind = %q", msg)
	}
	dock.Answer(content, `{"content":"kept\n","path":"/data.bin"}`)
	if out := suite.Call(t, "volume_read", map[string]any{"volume": "spare", "path": "/data.bin", "max_chars": 2}); out["content"] != "ke" || !strings.Contains(acctest.Str(out["note"]), "its first 2 are given") || !strings.Contains(acctest.Str(out["note"]), "was not taken away") {
		t.Errorf("a read that worked with its helper left behind = %v", out)
	}
	dock.Answer(release, `{"success":true}`)

	// the helper is taken away, or tried to be, after every one of them
	if freed := len(dock.Requests(release)); freed != 8 {
		t.Errorf("after eight reads the helper was released %d times", freed)
	}

	read := len(dock.Requests(content))
	for name, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"no path":           {map[string]any{"volume": "spare", "path": " "}, "path is required"},
		"more than may be":  {map[string]any{"volume": "spare", "path": "/a", "max_chars": 200001}, "max_chars is 200001"},
		"less than nothing": {map[string]any{"volume": "spare", "path": "/a", "max_chars": -1}, "max_chars is -1"},
		"a volume of none":  {map[string]any{"volume": "nope", "path": "/a"}, `no volume is named "nope"`},
	} {
		if msg := suite.CallErr(t, "volume_read", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", name, msg, tc.want)
		}
	}
	if len(dock.Requests(content)) != read {
		t.Error("a read that was refused for its arguments was asked of Dockhand all the same")
	}
}
