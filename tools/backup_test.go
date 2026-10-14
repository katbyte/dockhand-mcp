package tools

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// What a 1.0.51 server with its backups on answers, with invented names: two destinations, one in a store whose address carries a password and
// which Dockhand could open, and a folder it could not; three backups, of a container and of a stack on alpha and of a container on beta; and two
// snapshots the first took, with a third another Dockhand left in the same repository.
const (
	upkeepKept       = `"{\"pruneEnabled\":true,\"pruneSchedule\":\"0 0 1 * *\",\"checkEnabled\":true,\"checkSchedule\":\"0 0 1 * *\",\"verifyEnabled\":false,\"autoUnlock\":true}"`
	twoDestinations  = `[{"id":1,"name":"offsite","repository":"s3:https://key:hunter2@s3.example/backups","flags":null,"hostPath":null,"policies":` + upkeepKept + `,"lastTestAt":"2026-10-11T03:00:00.000Z","lastTestStatus":"success","lastTestError":null,"createdAt":"2026-10-11 02:00:00","updatedAt":"2026-10-11T03:00:00.000Z","hasCacert":false,"hasTlsClientCert":false},{"id":2,"name":"local","repository":"/backups/restic","flags":null,"hostPath":null,"policies":` + upkeepKept + `,"lastTestAt":"2026-10-11T03:00:00.000Z","lastTestStatus":"failed","lastTestError":"wrong repository password","createdAt":"2026-10-11 02:05:00","updatedAt":"2026-10-11 02:05:00","hasCacert":false,"hasTlsClientCert":false}]`
	offsiteStore     = `{"id":1,"name":"offsite","repository":"s3:https://key:hunter2@s3.example/backups","envVars":{"AWS_ACCESS_KEY_ID":"AKIAEXAMPLE","AWS_DEFAULT_REGION":"eu","AWS_SECRET_ACCESS_KEY":"shh"},"flags":"{\"backup\":\"--limit-upload 500\",\"restore\":\"\"}","backupFlags":"--limit-upload 500","restoreFlags":"","hostPath":null,"policies":` + upkeepKept + `,"lastTestAt":"2026-10-11T03:00:00.000Z","lastTestStatus":"success","lastTestError":null,"createdAt":"2026-10-11 02:00:00","updatedAt":"2026-10-11T03:00:00.000Z","hasCacert":false,"hasTlsClientCert":false}`
	localStore       = `{"id":2,"name":"local","repository":"/backups/restic","envVars":{},"flags":null,"backupFlags":"","restoreFlags":"","hostPath":null,"policies":` + upkeepKept + `,"lastTestAt":"2026-10-11T03:00:00.000Z","lastTestStatus":"failed","lastTestError":"wrong repository password","createdAt":"2026-10-11 02:05:00","updatedAt":"2026-10-11 02:05:00","hasCacert":false,"hasTlsClientCert":false}`
	frontBackup      = `{"id":3,"type":"container","targetName":"web-front-1","environmentId":1,"destinationId":1,"enabled":true,"allVolumes":true,"selectedVolumes":null,"stopBeforeBackup":false,"schedule":"0 3 * * *","retention":"{\"keepDaily\":7,\"keepWeekly\":4}","options":null,"tags":"[\"nightly\"]","lastBackupAt":"2026-10-12T03:00:05.000Z","lastBackupStatus":"success","createdAt":"2026-10-11 02:10:00","updatedAt":"2026-10-12T03:00:05.000Z"}`
	threeBackups     = `[` + frontBackup + `,{"id":5,"type":"stack","targetName":"web","environmentId":1,"destinationId":2,"enabled":false,"allVolumes":false,"selectedVolumes":"[\"web_data\"]","stopBeforeBackup":true,"schedule":null,"retention":null,"options":"{\"excludeCaches\":true}","tags":null,"lastBackupAt":"2026-10-12T04:00:00.000Z","lastBackupStatus":"failed","createdAt":"2026-10-11 02:20:00","updatedAt":"2026-10-12T04:00:00.000Z"},{"id":8,"type":"container","targetName":"solo","environmentId":2,"destinationId":1,"enabled":true,"allVolumes":true,"selectedVolumes":null,"stopBeforeBackup":false,"schedule":null,"retention":null,"options":null,"tags":null,"lastBackupAt":null,"lastBackupStatus":null,"createdAt":"2026-10-11 02:30:00","updatedAt":"2026-10-11 02:30:00"}]`
	thisInstance     = `{"instanceId":"11111111-aaaa-4bbb-8ccc-222222222222"}`
	earlySnapshot    = "aaaaaaaa11111111aaaaaaaa11111111aaaaaaaa11111111aaaaaaaa11111111"
	lateSnapshot     = "bbbbbbbb22222222bbbbbbbb22222222bbbbbbbb22222222bbbbbbbb22222222"
	foreignSnapshot  = "cccccccc33333333cccccccc33333333cccccccc33333333cccccccc33333333"
	ourTags          = `["dockhand:instance=11111111-aaaa-4bbb-8ccc-222222222222","dockhand:configid=3","dockhand:envid=1","dockhand:type=container","dockhand:name=web-front-1"]`
	threeSnapshots   = `[{"id":"` + lateSnapshot + `","shortId":"bbbbbbbb","time":"2026-10-12T03:00:04.123456789Z","hostname":"dockhand","tags":` + ourTags + `,"paths":["/metadata","/volumes"]},{"id":"` + foreignSnapshot + `","shortId":"cccccccc","time":"2026-10-11T12:00:00.5Z","hostname":"dockhand","tags":["dockhand:instance=99999999-0000-4000-8000-999999999999","dockhand:configid=3","dockhand:envid=4","dockhand:type=stack","dockhand:name=blog"],"paths":["/metadata","/volumes"]},{"id":"` + earlySnapshot + `","shortId":"aaaaaaaa","time":"2026-10-11T03:00:04.5Z","hostname":"dockhand","tags":` + ourTags + `,"paths":["/metadata","/volumes"]}]`
	frontRecord      = `{"version":1,"type":"container","targetName":"web-front-1","environmentId":1,"backupTime":"2026-10-11T03:00:03.000Z","volumes":[{"key":"web_data","name":"web_data","source":"web_data","type":"volume"}],"hasStackFiles":false,"container":{"Name":"/web-front-1","Config":{"Image":"registry.example/front:1.2"}}}`
	dataFolder       = `{"entries":[{"name":"sub","type":"dir","path":"/volumes/web_data/sub","uid":0,"gid":0,"mode":2147484141,"permissions":"drwxr-xr-x","mtime":"2026-10-10T08:00:00.5Z","atime":"2026-10-10T08:00:00.5Z","ctime":"2026-10-10T08:00:00.5Z","inode":12},{"name":"app.conf","type":"file","path":"/volumes/web_data/app.conf","uid":0,"gid":0,"size":31,"mode":420,"permissions":"-rw-r--r--","mtime":"2026-10-10T08:00:00.5Z","atime":"2026-10-10T08:00:00.5Z","ctime":"2026-10-10T08:00:00.5Z","inode":13},{"name":"big.db","type":"file","path":"/volumes/web_data/big.db","uid":0,"gid":0,"size":2097152,"mode":420,"permissions":"-rw-r--r--","mtime":"2026-10-10T08:00:00.5Z","atime":"2026-10-10T08:00:00.5Z","ctime":"2026-10-10T08:00:00.5Z","inode":14}],"path":"/volumes/web_data"}`
	volumesFolder    = `{"entries":[{"name":"web_data","type":"dir","path":"/volumes/web_data","uid":0,"gid":0,"mode":2147484141,"permissions":"drwxr-xr-x","mtime":"2026-10-10T08:00:00.5Z","atime":"2026-10-10T08:00:00.5Z","ctime":"2026-10-10T08:00:00.5Z","inode":3}],"path":"/volumes"}`
	restoreWorkedOut = `{"snapshotId":"` + earlySnapshot + `","volumes":["web_data"],"volumeTypes":{"web_data":"volume"},"volumeSources":{"web_data":"web_data"},"backupTime":"2026-10-11T03:00:03.000Z","sourceEnvironmentId":1,"hasMetadata":true,"hasStackFiles":false,"sourceSecretKeys":[],"targets":{"volumes":[{"key":"web_data","type":"volume","target":"web_data","origin":"in-place-metadata","hasData":"has-data"}],"stackFiles":null,"unresolved":[],"helperOk":true}}`

	storesCall    = "GET /api/backup/destinations"
	storeMake     = "POST /api/backup/destinations"
	storeTry      = "POST /api/backup/destinations/test"
	offsiteCall   = "GET /api/backup/destinations/1"
	offsiteSave   = "PUT /api/backup/destinations/1"
	offsiteTry    = "POST /api/backup/destinations/1/test"
	offsiteTask   = "POST /api/backup/destinations/1/task"
	backupsCall   = "GET /api/backup/configs"
	backupMake    = "POST /api/backup/configs"
	frontCall     = "GET /api/backup/configs/3"
	frontSave     = "PUT /api/backup/configs/3"
	frontRun      = "POST /api/backup/configs/3/run"
	snapshotsCall = "GET /api/backup/snapshots"
	earlyBrowse   = "GET /api/backup/snapshots/aaaaaaaa/browse"
	earlyDump     = "GET /api/backup/snapshots/aaaaaaaa/dump"
	earlyRecord   = "GET /api/backup/snapshots/aaaaaaaa/metadata"
	restoreLook   = "POST /api/backup/restore/preview"
	restoreCall   = "POST /api/backup/restore"
)

// withBackups is a Dockhand managing alpha and beta that keeps two backup destinations, three backups, and the snapshots of the first.
func withBackups(t *testing.T) *dockhandtest.Server {
	t.Helper()

	dock := twoHosts(t)
	dock.Answer(storesCall, twoDestinations)
	dock.Answer(offsiteCall, offsiteStore)
	dock.Answer("GET /api/backup/destinations/2", localStore)
	dock.Answer(backupsCall, threeBackups)
	dock.Answer(frontCall, frontBackup)
	dock.Answer("GET /api/backup/instance", thisInstance)
	dock.Answer(snapshotsCall, threeSnapshots)
	dock.Answer(earlyRecord, frontRecord)
	dock.AnswerWhen(earlyBrowse, "path", "/volumes/web_data", dataFolder)
	dock.AnswerWhen(earlyBrowse, "path", "/volumes", volumesFolder)

	return dock
}

func TestBackupsSwitchedOff(t *testing.T) {
	t.Parallel()

	// a Dockhand as it comes answers every backup call 404 in plain text, which each tool says the reason for
	dock := twoHosts(t)
	for _, call := range []string{storesCall, backupsCall, storeTry, "POST /api/backup/restore/stop"} {
		dock.Fail(call, http.StatusNotFound, "Not found")
	}
	suite := session(t, dock, everything)

	for tool, args := range map[string]map[string]any{
		"backup_list":               nil,
		"backup_destination_list":   nil,
		"backup_destination_test":   {"repository": "/backups", "password": "hunter2hunter2"},
		"backup_destination_create": {"name": "new", "repository": "/backups", "password": "hunter2hunter2"},
		"backup_run":                {"backup": 3},
		"backup_snapshot_list":      {"destination": "offsite"},
		"backup_stop":               {"restore": true},
	} {
		if msg := suite.CallErr(t, tool, args); !strings.Contains(msg, "backups switched off") || !strings.Contains(msg, "FEAT_BACKUPS_ENABLED=true") {
			t.Errorf("%s on a Dockhand with no backups = %q", tool, msg)
		}
	}
}

func TestBackupList(t *testing.T) {
	t.Parallel()

	suite := session(t, withBackups(t), Options{})

	// by host and then by what is backed up, each with where to, when, and what it keeps
	out := suite.Call(t, "backup_list", nil)
	rows := acctest.Rows(t, out["backups"], "backups")
	if len(rows) != 3 || rows[0]["of"] != "web" || rows[1]["of"] != "web-front-1" || rows[2]["of"] != "solo" || acctest.Num(t, out["total"], "total") != 3 {
		t.Fatalf("the backups = %v", out)
	}
	if got := flat(rows[1]); got != `{"changed":"2026-10-12T03:00:05Z","created":"2026-10-11T02:10:00Z","destination":"offsite","enabled":true,"env":"alpha","id":3,"keep":{"daily":7,"weekly":4},"last_run":"2026-10-12T03:00:05Z","last_status":"success","of":"web-front-1","schedule":"0 3 * * *","tags":["nightly"],"type":"container"}` {
		t.Errorf("a backup that runs by itself = %s", got)
	}
	if stack := rows[0]; stack["type"] != "stack" || stack["destination"] != "local" || !slices.Equal(acctest.Strs(t, stack["volumes"], "volumes"), []string{"web_data"}) || !acctest.IsBool(stack["stop_first"], true) || stack["schedule"] != nil || stack["keep"] != nil || !acctest.IsBool(stack["enabled"], false) || !acctest.IsBool(acctest.Object(t, stack["options"], "options")["excludeCaches"], true) {
		t.Errorf("a backup of a stack that runs when asked = %v", stack)
	}

	// narrowed, and one by its number and by the name of what it is of
	for what, want := range map[string]struct {
		args map[string]any
		ids  []int
	}{
		"of a host":         {map[string]any{"env": "beta"}, []int{8}},
		"of a destination":  {map[string]any{"destination": "local"}, []int{5}},
		"of one thing":      {map[string]any{"of": "web-front-1"}, []int{3}},
		"by its number":     {map[string]any{"backup": 8}, []int{8}},
		"by what it is of":  {map[string]any{"backup": "web"}, []int{5}},
		"of nothing as yet": {map[string]any{"env": "beta", "of": "web"}, []int{}},
	} {
		listed := acctest.Rows(t, suite.Call(t, "backup_list", want.args)["backups"], "backups")
		ids := make([]int, 0, len(listed))
		for _, row := range listed {
			ids = append(ids, acctest.Num(t, row["id"], "id"))
		}
		if !slices.Equal(ids, want.ids) {
			t.Errorf("the backups %s = %v, want %v", what, ids, want.ids)
		}
	}
	if msg := suite.CallErr(t, "backup_list", map[string]any{"backup": "nothing"}); !strings.Contains(msg, "no backup is numbered nothing") {
		t.Errorf("a backup there is none of = %q", msg)
	}
}

func TestBackupCreate(t *testing.T) {
	t.Parallel()

	dock := withBackups(t)
	suite := session(t, dock, everything)

	// checked before anything is saved: what is to be backed up, the volumes named, and that it is not backed up there already
	for what, refusal := range map[string]struct {
		args map[string]any
		want string
	}{
		"of nothing":             {map[string]any{"env": "alpha", "of": " ", "destination": "offsite"}, "of is required"},
		"of a kind that is none": {map[string]any{"env": "alpha", "of": "loose", "type": "pod", "destination": "offsite"}, "it is container or stack"},
		"on no host":             {map[string]any{"of": "loose", "destination": "offsite"}, "env is required"},
		"to nowhere":             {map[string]any{"env": "alpha", "of": "loose", "destination": "nowhere"}, "no backup destination is named or numbered"},
		"of a name nothing has":  {map[string]any{"env": "alpha", "of": "ghost", "destination": "offsite"}, "alpha has no container and no stack named ghost"},
		"of a stack as one":      {map[string]any{"env": "alpha", "of": "loose", "type": "stack", "destination": "offsite"}, "alpha has no stack named loose"},
		"of a volume it has not": {map[string]any{"env": "alpha", "of": "web-front-1", "destination": "local", "volumes": []any{"other"}}, "has no volume other, and Dockhand would save it and pass it over at every run: it has web_data"},
		"backed up already":      {map[string]any{"env": "alpha", "of": "web-front-1", "destination": "offsite"}, "already backed up to offsite, as backup 3"},
	} {
		if msg := suite.CallErr(t, "backup_create", refusal.args); !strings.Contains(msg, refusal.want) {
			t.Errorf("a backup %s = %q, want %q", what, msg, refusal.want)
		}
	}
	// a stack whose files Dockhand cannot find on the host would fail at every run, and is not saved
	dock.Answer("GET /api/backup/stack-dir-listing", `{"kind":"unknown","reason":"could not locate the stack folder on the host"}`)
	if msg := suite.CallErr(t, "backup_create", map[string]any{"env": "alpha", "of": "mail", "destination": "offsite"}); !strings.Contains(msg, "could not locate the stack folder on the host") {
		t.Errorf("a backup of a stack whose folder is not found = %q", msg)
	}
	if made := dock.Requests(backupMake); len(made) != 0 {
		t.Fatalf("a refused backup was saved all the same: %v", made)
	}

	// said first, and then saved with everything it was asked for and read back
	asking := map[string]any{"env": "alpha", "of": "loose", "destination": "offsite", "schedule": "0 2 * * *", "keep": map[string]any{"last": 3}, "stop_first": true, "tags": []any{"tools"}}
	if dry := suite.Call(t, "backup_create", over(asking, map[string]any{"dry_run": true})); flat(dry) != `{"changes":["would back up the container loose on alpha to offsite, run at 0 2 * * *, keeping 3 newest"],"dry_run":true}` || len(dock.Requests(backupMake)) != 0 {
		t.Fatalf("a dry run = %s", flat(dry))
	}
	dock.AnswerMade(backupMake, `{"id":9,"type":"container","targetName":"loose","environmentId":1,"destinationId":1}`)
	dock.Answer("GET /api/backup/configs/9", `{"id":9,"type":"container","targetName":"loose","environmentId":1,"destinationId":1,"enabled":true,"allVolumes":true,"selectedVolumes":null,"stopBeforeBackup":true,"schedule":"0 2 * * *","retention":"{\"keepLast\":3}","options":null,"tags":"[\"tools\"]","lastBackupAt":null,"lastBackupStatus":null,"createdAt":"2026-10-12 05:00:00","updatedAt":"2026-10-12 05:00:00"}`)
	made := suite.Call(t, "backup_create", asking)
	if body := dock.Last(backupMake).Body; body != `{"allVolumes":true,"destinationId":1,"enabled":true,"environmentId":1,"retention":{"keepLast":3},"schedule":"0 2 * * *","stopBeforeBackup":true,"tags":["tools"],"targetName":"loose","type":"container"}` {
		t.Errorf("a backup was saved as %s", body)
	}
	if row := acctest.Object(t, made["backup"], "backup"); acctest.Num(t, row["id"], "id") != 9 || row["of"] != "loose" || made["not_taken"] != nil {
		t.Errorf("a backup made = %v", made)
	}

	// one that runs by itself and is given nothing to keep is given Dockhand's own, which is said
	dock.Answer("GET /api/backup/configs/9", `{"id":9,"type":"container","targetName":"loose","environmentId":1,"destinationId":2,"enabled":true,"allVolumes":true,"schedule":"0 2 * * *","retention":"{\"keepDaily\":7,\"keepWeekly\":4,\"keepMonthly\":6}"}`)
	given := suite.Call(t, "backup_create", map[string]any{"env": 1, "of": "loose", "destination": 2, "schedule": "0 2 * * *"})
	if not := acctest.Strs(t, given["not_taken"], "not_taken"); len(not) != 1 || not[0] != "keep: asked for every snapshot, and Dockhand has 7 daily, 4 weekly, 6 monthly, which it writes for any backup that runs by itself and is given nothing to keep" {
		t.Errorf("what Dockhand saved in place of every snapshot = %v", not)
	}
}

func TestBackupEdit(t *testing.T) {
	t.Parallel()

	dock := withBackups(t)
	suite := session(t, dock, everything)

	// what is already so changes nothing
	if same := suite.Call(t, "backup_edit", map[string]any{"backup": 3, "schedule": "0 3 * * *", "enabled": true, "keep": map[string]any{"daily": 7, "weekly": 4}}); len(acctest.Strs(t, same["changes"], "changes")) != 0 || len(dock.Requests(frontSave)) != 0 {
		t.Fatalf("a change to what it is already = %v", same)
	}
	if msg := suite.CallErr(t, "backup_edit", map[string]any{"backup": 3, "volumes": []any{"other"}}); !strings.Contains(msg, "has no volume other") {
		t.Errorf("a volume it has not = %q", msg)
	}

	// the whole of it is sent, what was not named with what it had
	dock.Answer(frontSave, frontBackup)
	dock.Then(frontSave, func(dockhandtest.Request) {
		dock.Answer(frontCall, strings.NewReplacer(`"schedule":"0 3 * * *"`, `"schedule":"0 4 * * *"`, `"destinationId":1`, `"destinationId":2`).Replace(frontBackup))
	})
	changed := suite.Call(t, "backup_edit", map[string]any{"backup": "web-front-1", "schedule": "0 4 * * *", "destination": "local"})
	if body := dock.Last(frontSave).Body; body != `{"allVolumes":true,"destinationId":2,"enabled":true,"retention":{"keepDaily":7,"keepWeekly":4},"schedule":"0 4 * * *","stopBeforeBackup":false}` {
		t.Errorf("a change was sent as %s", body)
	}
	if row := acctest.Object(t, changed["backup"], "backup"); row["schedule"] != "0 4 * * *" || row["destination"] != "local" || len(acctest.Strs(t, changed["changes"], "changes")) != 2 || changed["not_taken"] != nil {
		t.Errorf("a backup changed = %v", changed)
	}

	// a schedule cleared goes by the call that can send it empty, with what is kept beside it; Dockhand saving otherwise is said
	dock.Answer(frontCall, frontBackup)
	dock.Then(frontSave, nil)
	dock.Then("PUT /api/backup/configs/3", nil)
	cleared := suite.Call(t, "backup_edit", map[string]any{"backup": 3, "schedule": "", "keep": map[string]any{}})
	sends := dock.Requests(frontSave)
	if last := sends[len(sends)-1].Body; last != `{"retention":null,"schedule":""}` {
		t.Errorf("a schedule cleared was last sent as %s", last)
	}
	if not := acctest.Strs(t, cleared["not_taken"], "not_taken"); len(not) != 2 || !strings.HasPrefix(not[0], "schedule: asked for none, and Dockhand has 0 3 * * *") || !strings.HasPrefix(not[1], "keep: asked for every snapshot, and Dockhand has 7 daily, 4 weekly") {
		t.Errorf("what Dockhand did not take = %v", not)
	}
	if dry := suite.Call(t, "backup_edit", map[string]any{"backup": 3, "enabled": false, "dry_run": true}); !slices.Equal(acctest.Strs(t, dry["changes"], "changes"), []string{"would set enabled to false"}) {
		t.Errorf("a dry run = %v", dry)
	}
}

func TestBackupRunStopAndDelete(t *testing.T) {
	t.Parallel()

	dock := withBackups(t)
	suite := session(t, dock, everything)

	// run as a job, and waited for: a snapshot, and what it took
	dock.Answer(frontRun, `{"jobId":"j7"}`)
	dock.Answer("GET /api/jobs/j7", `{"id":"j7","status":"done","lines":[{"event":"progress","data":{"status":"backing-up","message":"Backing up 1 volumes:"}}],"result":{"status":"success","executionId":41,"snapshotId":"`+lateSnapshot+`","summary":{"snapshotId":"`+lateSnapshot+`","filesNew":2,"filesChanged":1,"filesUnmodified":9,"dataAdded":4096,"totalBytesProcessed":90000,"totalFilesProcessed":12},"retention":"applied"}}`)
	if got := flat(suite.Call(t, "backup_run", map[string]any{"backup": 3})); got != `{"added_bytes":4096,"backup":3,"changed":3,"destination":"offsite","env":"alpha","files":12,"of":"web-front-1","progress":["backing-up: Backing up 1 volumes:"],"pruned":true,"run":41,"running":false,"snapshot":"bbbbbbbb","status":"success"}` || dock.Last(frontRun).Accept != "*/*" {
		t.Errorf("a run = %s, asked for as %q", got, dock.Last(frontRun).Accept)
	}
	// still going when the wait ends, it is left as a job; one that failed is an error with why; one stopped made no snapshot
	dock.Answer("GET /api/jobs/j7", `{"id":"j7","status":"running","lines":[],"result":null}`)
	if going := suite.Call(t, "backup_run", map[string]any{"backup": 3, "wait": 0}); !acctest.IsBool(going["running"], true) || going["job"] != "j7" || !strings.Contains(acctest.Str(going["note"]), "job_get") {
		t.Errorf("a run that outlasts the wait = %v", going)
	}
	dock.Answer("GET /api/jobs/j7", `{"id":"j7","status":"done","lines":[],"result":{"status":"error","executionId":42,"code":"VALIDATION","error":"No container \"web-front-1\" found — refusing to back up nothing."}}`)
	if msg := suite.CallErr(t, "backup_run", map[string]any{"backup": 3}); !strings.Contains(msg, "the backup of web-front-1 on alpha failed") || !strings.Contains(msg, "refusing to back up nothing") {
		t.Errorf("a run that failed = %q", msg)
	}
	dock.Answer("GET /api/jobs/j7", `{"id":"j7","status":"done","lines":[],"result":{"status":"skipped","executionId":43,"reason":"Backup cancelled"}}`)
	if skipped := suite.Call(t, "backup_run", map[string]any{"backup": 3}); skipped["status"] != "skipped" || skipped["snapshot"] != nil || skipped["note"] != "no snapshot was made: Backup cancelled" {
		t.Errorf("a run that was stopped = %v", skipped)
	}

	// stopped: a backup by what it is a run of, and a restore, each saying when there was nothing to stop
	dock.Answer("POST /api/backup/configs/3/stop", `{"success":true,"stopped":true}`)
	dock.Answer("POST /api/backup/restore/stop", `{"success":true,"stopped":false}`)
	if stopped := suite.Call(t, "backup_stop", map[string]any{"backup": 3}); !acctest.IsBool(stopped["stopped"], true) || stopped["note"] != nil {
		t.Errorf("a backup stopped = %v", stopped)
	}
	if stopped := suite.Call(t, "backup_stop", map[string]any{"restore": true, "snapshot": "aaaaaaaa"}); !acctest.IsBool(stopped["stopped"], false) || stopped["note"] == nil || dock.Last("POST /api/backup/restore/stop").Body != `{"snapshotId":"aaaaaaaa"}` {
		t.Errorf("a restore stopped when none runs = %v", stopped)
	}
	for what, args := range map[string]map[string]any{"give one": {"backup": 3, "restore": true}, "give restore with it": {"backup": 3, "snapshot": "aaaaaaaa"}, "backup is required": nil} {
		if msg := suite.CallErr(t, "backup_stop", args); !strings.Contains(msg, what) {
			t.Errorf("stopping %v = %q, want %q", args, msg, what)
		}
	}

	// removed only once confirmed, and its snapshots only when asked
	if preview := suite.Call(t, "backup_delete", map[string]any{"backup": 3, "with_snapshots": true}); !acctest.IsBool(preview["removed"], false) || !slices.Equal(acctest.Strs(t, preview["would_go"], "would_go"), []string{"the backup of the container web-front-1, with its schedule", "3 snapshots it took, which cannot be had back"}) {
		t.Errorf("a removal not confirmed = %v", preview)
	}
	dock.Answer("DELETE /api/backup/configs/3", `{"success":true,"snapshots":{"deleted":2,"skipped":1,"error":"one is not this Dockhand's"}}`)
	dock.Then("DELETE /api/backup/configs/3", func(dockhandtest.Request) { dock.Answer(backupsCall, `[]`) })
	gone := suite.Call(t, "backup_delete", map[string]any{"backup": 3, "with_snapshots": true, "confirm": true})
	if !acctest.IsBool(gone["removed"], true) || acctest.Num(t, gone["snapshots"], "snapshots") != 2 || gone["would_go"] != nil || !strings.Contains(acctest.Str(gone["note"]), "1 snapshot could not be removed") || dock.Last("DELETE /api/backup/configs/3").Query.Get("deleteSnapshots") != "true" {
		t.Errorf("a backup removed with its snapshots = %v", gone)
	}
}

//nolint:gosec // G101: the passwords are made up, and there to be seen hidden
func TestBackupDestinationListTestAndCheck(t *testing.T) {
	t.Parallel()

	dock := withBackups(t)
	suite := session(t, dock, Options{})

	// each with whether it opens and what is kept in it, and what it is signed in to with hidden
	out := suite.Call(t, "backup_destination_list", nil)
	rows := acctest.Rows(t, out["destinations"], "destinations")
	if len(rows) != 2 || rows[0]["name"] != "local" || rows[1]["name"] != "offsite" || acctest.Num(t, out["hidden"], "hidden") != 3 {
		t.Fatalf("the destinations = %v", out)
	}
	if got := flat(rows[1]); got != `{"backup_flags":"--limit-upload 500","backups":["3 (web-front-1, container)","8 (solo, container)"],"changed":"2026-10-11T03:00:00Z","created":"2026-10-11T02:00:00Z","id":1,"name":"offsite","repository":"s3:https://key:***@s3.example/backups","tried":"2026-10-11T03:00:00Z","upkeep":{"auto_unlock":true,"check":true,"check_schedule":"0 0 1 * *","prune":true,"prune_schedule":"0 0 1 * *","verify":false},"usable":true,"variables":{"AWS_ACCESS_KEY_ID":"***","AWS_DEFAULT_REGION":"eu","AWS_SECRET_ACCESS_KEY":"***"}}` {
		t.Errorf("a destination in a store = %s", got)
	}
	if local := rows[0]; !acctest.IsBool(local["usable"], false) || local["error"] != "wrong repository password" || local["variables"] != nil {
		t.Errorf("a destination that does not open = %v", local)
	}
	if shown := acctest.Rows(t, suite.Call(t, "backup_destination_list", map[string]any{"destination": 1, "reveal_secrets": true})["destinations"], "destinations"); len(shown) != 1 || shown[0]["repository"] != "s3:https://key:hunter2@s3.example/backups" || acctest.Object(t, shown[0]["variables"], "variables")["AWS_SECRET_ACCESS_KEY"] != "shh" {
		t.Errorf("a destination with its secrets asked for = %v", shown)
	}

	// tried: one that is saved, and one that is not, where nothing answering reads as a repository still to be made
	dock.Answer(offsiteTry, `{"success":true,"status":"success"}`)
	if tried := suite.Call(t, "backup_destination_test", map[string]any{"destination": "offsite"}); flat(tried) != `{"destination":"offsite","repository":"s3:https://key:***@s3.example/backups","usable":true}` {
		t.Errorf("a saved destination tried = %s", flat(tried))
	}
	dock.Answer(storeTry, `{"success":false,"needsInit":true,"error":"Repository not initialized"}`)
	tried := suite.Call(t, "backup_destination_test", map[string]any{"repository": "rest:http://backups.example:8000/new", "password": "hunter2hunter2", "variables": map[string]any{"A": "b"}})
	if !acctest.IsBool(tried["needs_init"], true) || !strings.Contains(acctest.Str(tried["note"]), "does not tell apart") || dock.Last(storeTry).Body != `{"envVars":{"A":"b"},"password":"hunter2hunter2","repository":"rest:http://backups.example:8000/new"}` {
		t.Errorf("a repository not made yet, tried = %v, sent as %s", tried, dock.Last(storeTry).Body)
	}
	for what, args := range map[string]map[string]any{"destination or repository is required": nil, "password is required with repository": {"repository": "/backups"}, "one the Dockhand keeps is tried as it is kept": {"destination": 1, "password": "x"}} {
		if msg := suite.CallErr(t, "backup_destination_test", args); !strings.Contains(msg, what) {
			t.Errorf("a try of %v = %q, want %q", args, msg, what)
		}
	}

	// looked at: how much it holds, and what restic finds wrong, which is an answer and no failure
	dock.Answer(offsiteTask, `{"success":true,"output":"{\"total_size\":21354}","stats":{"totalSize":21354,"totalFiles":23,"snapshots":3}}`)
	if stats := suite.Call(t, "backup_destination_check", map[string]any{"destination": "offsite"}); flat(stats) != `{"bytes":21354,"check":"stats","destination":"offsite","files":23,"running":false,"snapshots":3,"sound":true}` || dock.Last(offsiteTask).Body != `{"task":"stats"}` {
		t.Errorf("a destination's stats = %s", flat(stats))
	}
	dock.Answer("POST /api/backup/destinations/1/verify", `{"success":false,"output":"load indexes\nreading packs\n","error":"Fatal: repository contains errors"}`)
	found := suite.Call(t, "backup_destination_check", map[string]any{"destination": "offsite", "check": "verify", "subset": "10%"})
	if !acctest.IsBool(found["sound"], false) || found["error"] != "Fatal: repository contains errors" || !slices.Equal(acctest.Strs(t, found["printed"], "printed"), []string{"load indexes", "reading packs"}) || dock.Last("POST /api/backup/destinations/1/verify").Body != `{"dataSubset":"10%"}` {
		t.Errorf("a verify that found something wrong = %v", found)
	}
	for what, args := range map[string]map[string]any{"it is stats, check, verify": {"destination": 1, "check": "prune"}, "give check verify with it": {"destination": 1, "subset": "10%"}} {
		if msg := suite.CallErr(t, "backup_destination_check", args); !strings.Contains(msg, what) {
			t.Errorf("a check of %v = %q, want %q", args, msg, what)
		}
	}
}

//nolint:gosec // G101: the passwords are made up, and there to be seen hidden
func TestBackupDestinationCreate(t *testing.T) {
	t.Parallel()

	dock := withBackups(t)
	suite := session(t, dock, everything)
	asking := map[string]any{"name": "third", "repository": "rest:http://user:hunter2@backups.example:8000/third", "password": "hunter2hunter2", "backup_flags": "--limit-upload 500", "upkeep": map[string]any{"verify": true, "verify_subset": "10%"}}

	// tried first: one Dockhand cannot open is not saved, nor is one whose name is taken
	dock.Answer(storeTry, `{"success":false,"error":"Fatal: wrong password or no key found"}`)
	if msg := suite.CallErr(t, "backup_destination_create", asking); !strings.Contains(msg, "Dockhand could not open the repository, so nothing was saved: Fatal: wrong password or no key found") || len(dock.Requests(storeMake)) != 0 {
		t.Fatalf("a destination that does not open = %q", msg)
	}
	if msg := suite.CallErr(t, "backup_destination_create", over(asking, map[string]any{"name": "offsite"})); !strings.Contains(msg, "already named offsite") {
		t.Errorf("a name that is taken = %q", msg)
	}

	// one still to be made is said first, and then saved, with its password nowhere in the answer
	dock.Answer(storeTry, `{"success":false,"needsInit":true,"error":"Repository not initialized"}`)
	if dry := suite.Call(t, "backup_destination_create", over(asking, map[string]any{"dry_run": true})); !slices.Equal(acctest.Strs(t, dry["changes"], "changes"), []string{"would save the backup destination third at rest:http://user:***@backups.example:8000/third"}) || !strings.Contains(acctest.Str(dry["note"]), "has Dockhand make one there") {
		t.Fatalf("a dry run = %v", dry)
	}
	third := `{"id":7,"name":"third","repository":"rest:http://user:hunter2@backups.example:8000/third","envVars":{},"flags":"{\"backup\":\"--limit-upload 500\",\"restore\":\"\"}","backupFlags":"--limit-upload 500","restoreFlags":"","policies":"{\"verifyEnabled\":true,\"verifyDataSubset\":\"10%\"}","lastTestAt":"2026-10-12T05:00:00.000Z","lastTestStatus":"success","lastTestError":null,"createdAt":"2026-10-12 05:00:00","updatedAt":"2026-10-12 05:00:00"}`
	dock.AnswerMade(storeMake, third)
	dock.Answer("GET /api/backup/destinations/7", third)
	made := suite.Call(t, "backup_destination_create", asking)
	if body := dock.Last(storeMake).Body; body != `{"backupFlags":"--limit-upload 500","name":"third","password":"hunter2hunter2","policies":"{\"verifyDataSubset\":\"10%\",\"verifyEnabled\":true}","repository":"rest:http://user:hunter2@backups.example:8000/third"}` {
		t.Errorf("a destination was saved as %s", body)
	}
	if row := acctest.Object(t, made["destination"], "destination"); !acctest.IsBool(row["usable"], true) || row["repository"] != "rest:http://user:***@backups.example:8000/third" || strings.Contains(flat(made), "hunter2") {
		t.Errorf("a destination made = %v", made)
	}

	// Dockhand saves one it then cannot make a repository for: it is taken away again, unless it was asked for all the same
	dock.Answer("GET /api/backup/destinations/7", strings.NewReplacer(`"lastTestStatus":"success","lastTestError":null`, `"lastTestStatus":"failed","lastTestError":"Fatal: create repository failed: no such host"`).Replace(third))
	dock.Answer("DELETE /api/backup/destinations/7", `{"success":true}`)
	if msg := suite.CallErr(t, "backup_destination_create", asking); !strings.Contains(msg, "the destination was not kept: Fatal: create repository failed: no such host") || len(dock.Requests("DELETE /api/backup/destinations/7")) != 1 {
		t.Errorf("a destination no repository could be made for = %q", msg)
	}
	kept := suite.Call(t, "backup_destination_create", over(asking, map[string]any{"save_unusable": true}))
	if !strings.Contains(acctest.Str(kept["note"]), "kept as save_unusable asked") || len(dock.Requests("DELETE /api/backup/destinations/7")) != 1 {
		t.Errorf("a destination kept though it cannot be opened = %v", kept)
	}
}

//nolint:gosec // G101: the passwords are made up, and there to be seen hidden
func TestBackupDestinationEdit(t *testing.T) {
	t.Parallel()

	dock := withBackups(t)
	suite := session(t, dock, everything)
	dock.Answer(offsiteSave, offsiteStore)

	// refused before anything is sent: a name that is taken, and a new address with no password to try it with
	for what, args := range map[string]map[string]any{"already named local": {"destination": 1, "name": "local"}, "password is required with repository": {"destination": 1, "repository": "/elsewhere"}} {
		if msg := suite.CallErr(t, "backup_destination_edit", args); !strings.Contains(msg, what) {
			t.Errorf("a change of %v = %q, want %q", args, msg, what)
		}
	}
	// a password is tried against the repository, with what it is signed in to with, before it is saved
	dock.Answer(storeTry, `{"success":false,"error":"Fatal: wrong password or no key found"}`)
	if msg := suite.CallErr(t, "backup_destination_edit", map[string]any{"destination": 1, "password": "not-the-password"}); !strings.Contains(msg, "nothing was changed: Fatal: wrong password or no key found") || len(dock.Requests(offsiteSave)) != 0 {
		t.Fatalf("a password the repository does not take = %q", msg)
	}
	if body := dock.Last(storeTry).Body; body != `{"envVars":{"AWS_ACCESS_KEY_ID":"AKIAEXAMPLE","AWS_DEFAULT_REGION":"eu","AWS_SECRET_ACCESS_KEY":"shh"},"password":"not-the-password","repository":"s3:https://key:hunter2@s3.example/backups"}` {
		t.Errorf("a password was tried as %s", body)
	}

	// its upkeep is sent whole, what was named over what was kept, since Dockhand replaces the whole of it
	changed := suite.Call(t, "backup_destination_edit", map[string]any{"destination": "offsite", "name": "far", "upkeep": map[string]any{"verify": true, "prune": false}})
	if body := dock.Last(offsiteSave).Body; body != `{"name":"far","policies":"{\"autoUnlock\":true,\"checkEnabled\":true,\"checkSchedule\":\"0 0 1 * *\",\"pruneEnabled\":false,\"pruneSchedule\":\"0 0 1 * *\",\"verifyEnabled\":true}"}` {
		t.Errorf("a change was sent as %s", body)
	}
	if !slices.Equal(acctest.Strs(t, changed["changes"], "changes"), []string{"set its name to far", "set its upkeep: pruneEnabled, verifyEnabled"}) {
		t.Errorf("a destination changed = %v", changed)
	}
	// flags are cleared by the call that can send one empty, and nothing else is sent
	before := len(dock.Requests(offsiteSave))
	suite.Call(t, "backup_destination_edit", map[string]any{"destination": 1, "backup_flags": ""})
	if sends := dock.Requests(offsiteSave); len(sends) != before+1 || sends[before].Body != `{"backupFlags":""}` {
		t.Errorf("flags cleared sent %v", sends[before:])
	}

	// what it is signed in to with is tried once saved, and put back when the repository no longer opens
	dock.Answer(offsiteTry, `{"success":false,"status":"failed","error":"Fatal: unable to open config file: access denied"}`)
	before = len(dock.Requests(offsiteSave))
	if msg := suite.CallErr(t, "backup_destination_edit", map[string]any{"destination": 1, "variables": map[string]any{"AWS_ACCESS_KEY_ID": "other"}}); !strings.Contains(msg, "what it was signed in to with before was put back: Fatal: unable to open config file: access denied") {
		t.Errorf("variables the store does not take = %q", msg)
	}
	if sends := dock.Requests(offsiteSave); len(sends) != before+2 || sends[before].Body != `{"envVars":{"AWS_ACCESS_KEY_ID":"other"}}` || sends[before+1].Body != `{"envVars":{"AWS_ACCESS_KEY_ID":"AKIAEXAMPLE","AWS_DEFAULT_REGION":"eu","AWS_SECRET_ACCESS_KEY":"shh"}}` {
		t.Errorf("variables saved and put back sent %v", sends[before:])
	}
	if same := suite.Call(t, "backup_destination_edit", map[string]any{"destination": 1, "name": "offsite", "backup_flags": "--limit-upload 500"}); len(acctest.Strs(t, same["changes"], "changes")) != 0 {
		t.Errorf("a change to what it is already = %v", same)
	}
}

func TestBackupDestinationTaskRotateAndDelete(t *testing.T) {
	t.Parallel()

	dock := withBackups(t)
	suite := session(t, dock, everything)

	// upkeep: what only looks is another tool's, a dry run does nothing, and restic failing is an error with why
	for what, args := range map[string]map[string]any{"task is required": {"destination": 1, "task": " "}, "only looks, and is backup_destination_check's": {"destination": 1, "task": "check"}, "it is unlock, prune": {"destination": 1, "task": "dance"}} {
		if msg := suite.CallErr(t, "backup_destination_task", args); !strings.Contains(msg, what) {
			t.Errorf("a task of %v = %q, want %q", args, msg, what)
		}
	}
	if dry := suite.Call(t, "backup_destination_task", map[string]any{"destination": 1, "task": "prune", "dry_run": true}); !acctest.IsBool(dry["done"], false) || len(dock.Requests(offsiteTask)) != 0 {
		t.Fatalf("a dry run of a prune = %v", dry)
	}
	dock.Answer(offsiteTask, `{"success":true,"output":"loading all snapshots...\nfinding data that is still in use\ndone\n"}`)
	if pruned := suite.Call(t, "backup_destination_task", map[string]any{"destination": "offsite", "task": "prune"}); !acctest.IsBool(pruned["done"], true) || !slices.Equal(acctest.Strs(t, pruned["printed"], "printed"), []string{"loading all snapshots...", "finding data that is still in use", "done"}) || dock.Last(offsiteTask).Accept != "*/*" {
		t.Errorf("a prune = %v", pruned)
	}
	dock.Answer(offsiteTask, `{"success":false,"error":"Fatal: unable to create lock in backend: repository is already locked"}`)
	if msg := suite.CallErr(t, "backup_destination_task", map[string]any{"destination": 1, "task": "repair-index"}); !strings.Contains(msg, "restic could not repair-index the repository of offsite: Fatal: unable to create lock") {
		t.Errorf("a repair that failed = %q", msg)
	}
	dock.Answer("POST /api/backup/destinations/2/init", `{"success":true,"message":"Repository initialized"}`)
	if made := suite.Call(t, "backup_destination_task", map[string]any{"destination": "local", "task": "init"}); !acctest.IsBool(made["done"], true) {
		t.Errorf("a repository made = %v", made)
	}

	// a new password: checked here for what Dockhand would refuse, and the repository tried with what Dockhand kept
	for what, args := range map[string]map[string]any{"are required": {"destination": 1, "current_password": "", "new_password": "longenough"}, "shorter than 8 characters": {"destination": 1, "current_password": "hunter2hunter2", "new_password": "short"}, "nothing to change": {"destination": 1, "current_password": "hunter2hunter2", "new_password": "hunter2hunter2"}} {
		if msg := suite.CallErr(t, "backup_destination_rotate_key", args); !strings.Contains(msg, what) {
			t.Errorf("a new password of %v = %q, want %q", args, msg, what)
		}
	}
	dock.Answer("POST /api/backup/destinations/1/rotate-key", `{"success":true}`)
	dock.Answer(offsiteTry, `{"success":true,"status":"success"}`)
	turned := suite.Call(t, "backup_destination_rotate_key", map[string]any{"destination": 1, "current_password": "hunter2hunter2", "new_password": "correct-horse"})
	if flat(turned) != `{"destination":"offsite","rotated":true,"usable":true}` || dock.Last("POST /api/backup/destinations/1/rotate-key").Body != `{"currentPassword":"hunter2hunter2","newPassword":"correct-horse"}` {
		t.Errorf("a password changed = %s", flat(turned))
	}

	// removed: not while backups are kept in it, which Dockhand would remove with it, unless that is asked for
	if preview := suite.Call(t, "backup_destination_delete", map[string]any{"destination": "offsite"}); !acctest.IsBool(preview["removed"], false) || preview["note"] != "nothing was removed: confirm with even_used removes it" || len(acctest.Strs(t, preview["backups"], "backups")) != 2 {
		t.Errorf("a removal not confirmed = %v", preview)
	}
	if msg := suite.CallErr(t, "backup_destination_delete", map[string]any{"destination": "offsite", "confirm": true}); !strings.Contains(msg, "the destination offsite holds 2 backups: 3 (web-front-1, container), 8 (solo, container)") || len(dock.Requests("DELETE /api/backup/destinations/1")) != 0 {
		t.Fatalf("a destination with backups in it, removed = %q", msg)
	}
	dock.Answer("DELETE /api/backup/destinations/1", `{"success":true}`)
	dock.Then("DELETE /api/backup/destinations/1", func(dockhandtest.Request) { dock.Answer(storesCall, `[]`) })
	if gone := suite.Call(t, "backup_destination_delete", map[string]any{"destination": "offsite", "confirm": true, "even_used": true}); !acctest.IsBool(gone["removed"], true) || gone["would_go"] != nil || !strings.Contains(acctest.Str(gone["note"]), "its snapshots are where they were") {
		t.Errorf("a destination removed = %v", gone)
	}
}

func TestBackupSnapshotsListedAndLookedInto(t *testing.T) {
	t.Parallel()

	dock := withBackups(t)
	suite := session(t, dock, Options{})

	// the newest first, each with what it is of, and one another Dockhand left marked as not this one's
	out := suite.Call(t, "backup_snapshot_list", map[string]any{"destination": "offsite"})
	rows := acctest.Rows(t, out["snapshots"], "snapshots")
	if len(rows) != 3 || acctest.Num(t, out["total"], "total") != 3 || flat(rows[0]) != `{"backup":3,"env":"alpha","id":"bbbbbbbb","of":"web-front-1","ours":true,"taken":"2026-10-12T03:00:04Z","type":"container"}` || flat(rows[1]) != `{"backup":3,"env":"#4","id":"cccccccc","of":"blog","ours":false,"taken":"2026-10-11T12:00:00Z","type":"stack"}` {
		t.Fatalf("the snapshots of a destination = %v", out)
	}
	// a backup's own are the ones it took, which another Dockhand's backup of the same number did not
	if own := suite.Call(t, "backup_snapshot_list", map[string]any{"backup": 3, "limit": 1}); acctest.Num(t, own["total"], "total") != 2 || len(acctest.Rows(t, own["snapshots"], "snapshots")) != 1 || own["note"] != "the newest 1 of 2: limit gives more" {
		t.Errorf("the newest snapshot of a backup = %v", own)
	}
	if none := suite.Call(t, "backup_snapshot_list", map[string]any{"destination": 1, "of": "nothing"}); acctest.Num(t, none["total"], "total") != 0 || none["note"] != "no snapshot is as asked for; the destination holds 3 snapshots" {
		t.Errorf("the snapshots of nothing = %v", none)
	}
	for what, args := range map[string]map[string]any{"destination or backup is required": nil, "give one": {"destination": 1, "backup": 3}} {
		if msg := suite.CallErr(t, "backup_snapshot_list", args); !strings.Contains(msg, what) {
			t.Errorf("the snapshots of %v = %q, want %q", args, msg, what)
		}
	}

	at := map[string]any{"backup": 3, "snapshot": "aaaaaaaa"}
	if got := flat(suite.Call(t, "backup_snapshot_get", at)); got != `{"destination":"offsite","env":"alpha","image":"registry.example/front:1.2","of":"web-front-1","snapshot":"aaaaaaaa","taken":"2026-10-11T03:00:03Z","type":"container","volumes":[{"from":"web_data","kind":"volume","name":"web_data"}]}` {
		t.Errorf("one snapshot = %s", got)
	}
	dock.Answer("GET /api/backup/snapshots/deadbeef/metadata", `{"error":"snapshot not found for this installation"}`)
	if msg := suite.CallErr(t, "backup_snapshot_get", map[string]any{"destination": 1, "snapshot": "deadbeef"}); !strings.Contains(msg, "could not be read from offsite: snapshot not found for this installation") {
		t.Errorf("a snapshot there is none of = %q", msg)
	}

	// a folder, folders first, and a path the snapshot does not have, which reads as empty
	files := suite.Call(t, "backup_snapshot_files", over(at, map[string]any{"path": "volumes/web_data/"}))
	entries := acctest.Rows(t, files["entries"], "entries")
	if files["path"] != "/volumes/web_data" || len(entries) != 3 || entries[0]["name"] != "sub" || flat(entries[1]) != `{"bytes":31,"modified":"2026-10-10T08:00:00Z","name":"app.conf","permissions":"-rw-r--r--","type":"file"}` {
		t.Errorf("a folder of a snapshot = %v", files)
	}
	dock.AnswerWhen(earlyBrowse, "path", "/volumes/nothing", `{"entries":[],"path":"/volumes/nothing"}`)
	if empty := suite.Call(t, "backup_snapshot_files", over(at, map[string]any{"path": "/volumes/nothing"})); acctest.Num(t, empty["total"], "total") != 0 || !strings.Contains(acctest.Str(empty["note"]), "read alike") {
		t.Errorf("a folder the snapshot does not have = %v", empty)
	}
	if msg := suite.CallErr(t, "backup_snapshot_files", over(at, map[string]any{"path": "/volumes/../../etc"})); !strings.Contains(msg, "with no .. in it") {
		t.Errorf("a path that climbs out = %q", msg)
	}

	// a file as text, with what is told for a secret hidden; not a folder, one too big, or one that is not there
	dock.AnswerTextWhen(earlyDump, "path", "/volumes/web_data/app.conf", "application/json", `{"content":"mode=live\npassword=hunter2\n"}`)
	read := suite.Call(t, "backup_snapshot_read", over(at, map[string]any{"path": "/volumes/web_data/app.conf"}))
	if read["content"] != "mode=live\npassword=***\n" || acctest.Num(t, read["hidden"], "hidden") != 1 || dock.Last(earlyDump).Query.Get("download") != "" {
		t.Errorf("a file read out of a snapshot = %v", read)
	}
	if shown := suite.Call(t, "backup_snapshot_read", over(at, map[string]any{"path": "/volumes/web_data/app.conf", "reveal_secrets": true})); shown["content"] != "mode=live\npassword=hunter2\n" || shown["hidden"] != nil {
		t.Errorf("a file with its secrets asked for = %v", shown)
	}
	for file, want := range map[string]string{"/volumes/web_data/sub": "is a folder in the snapshot", "/volumes/web_data/big.db": "more than the 1 MB that is read of a file", "/volumes/web_data/gone.txt": "has nothing at /volumes/web_data/gone.txt"} {
		if msg := suite.CallErr(t, "backup_snapshot_read", over(at, map[string]any{"path": file})); !strings.Contains(msg, want) {
			t.Errorf("%s read = %q, want %q", file, msg, want)
		}
	}

	// compared, the first hundred of each kind listed
	many := `"/volumes/web_data/f` + strings.Repeat(`","/volumes/web_data/f`, 119) + `"`
	dock.Answer("GET /api/backup/snapshots/diff", `{"added":[`+many+`],"removed":[],"modified":["/volumes/web_data/app.conf"],"metadataChanged":[],"raw":"{}"}`)
	differs := suite.Call(t, "backup_snapshot_diff", map[string]any{"destination": "offsite", "from": "aaaaaaaa", "to": "bbbbbbbb"})
	if len(acctest.Strs(t, differs["added"], "added")) != 100 || acctest.Num(t, differs["total"], "total") != 121 || !slices.Equal(acctest.Strs(t, differs["changed"], "changed"), []string{"/volumes/web_data/app.conf"}) || differs["note"] != "the first 100 paths of a kind are listed, of 121 that differ in all" {
		t.Errorf("two snapshots compared = %v", differs)
	}
	if query := dock.Last("GET /api/backup/snapshots/diff").Query; query.Get("snapshotA") != "aaaaaaaa" || query.Get("snapshotB") != "bbbbbbbb" || query.Get("destinationId") != "1" {
		t.Errorf("two snapshots were compared as %v", query)
	}
}

func TestBackupSnapshotDownloadAndDelete(t *testing.T) {
	t.Parallel()

	dock := withBackups(t)
	suite := session(t, dock, everything)
	at := map[string]any{"destination": "offsite", "snapshot": "aaaaaaaa"}
	here := t.TempDir()

	// only the data of a volume is fetched: Dockhand's own record of the container is read by the tool that leaves its secrets out
	for path, want := range map[string]string{"/metadata": "only what is under /volumes", "/metadata/metadata.json": "which backup_snapshot_get reads with its secrets left out", "/volumes": "only what is under /volumes", "/": "only what is under /volumes"} {
		if msg := suite.CallErr(t, "backup_snapshot_download", over(at, map[string]any{"path": path, "to": here})); !strings.Contains(msg, want) {
			t.Errorf("%s fetched = %q, want %q", path, msg, want)
		}
	}
	if len(dock.Requests(earlyDump)) != 0 {
		t.Fatalf("something outside the volumes was asked for all the same: %v", dock.Requests(earlyDump))
	}

	// a file as it is, a folder as a tar under its own name, and never over a file that is there
	dock.AnswerTextWhen(earlyDump, "path", "/volumes/web_data/app.conf", "application/octet-stream", "mode=live\n")
	one := suite.Call(t, "backup_snapshot_download", over(at, map[string]any{"path": "/volumes/web_data/app.conf", "to": here}))
	//nolint:gosec // G304: the path is the test's own, under its own temporary folder
	if content, err := os.ReadFile(filepath.Join(here, "app.conf")); err != nil || string(content) != "mode=live\n" || one["saved"] != filepath.Join(here, "app.conf") || !acctest.IsBool(one["archive"], false) || acctest.Num(t, one["bytes"], "bytes") != 10 || dock.Last(earlyDump).Query.Get("download") != "1" {
		t.Errorf("a file fetched = %v, holding %q, %v", one, content, err)
	}
	dock.AnswerTextWhen(earlyDump, "path", "/volumes/web_data", "application/octet-stream", "a tar archive")
	if whole := suite.Call(t, "backup_snapshot_download", over(at, map[string]any{"path": "/volumes/web_data", "to": here})); whole["saved"] != filepath.Join(here, "web_data.tar") || !acctest.IsBool(whole["archive"], true) {
		t.Errorf("a volume fetched whole = %v", whole)
	}
	if msg := suite.CallErr(t, "backup_snapshot_download", over(at, map[string]any{"path": "/volumes/web_data/app.conf", "to": here})); !strings.Contains(msg, "could not be written here") {
		t.Errorf("a file fetched over one that is there = %q", msg)
	}

	// removed: each looked up first, so that one that is not there, or is another Dockhand's, removes nothing
	for what, refusal := range map[string]struct {
		ids  []any
		want string
	}{
		"none":               {nil, "snapshots is required"},
		"one not there":      {[]any{"aaaaaaaa", "deadbeef"}, `offsite holds no snapshot "deadbeef", so nothing was removed`},
		"another Dockhand's": {[]any{"cccccccc"}, "was taken by another Dockhand"},
	} {
		if msg := suite.CallErr(t, "backup_snapshot_delete", map[string]any{"destination": 1, "snapshots": refusal.ids, "confirm": true}); !strings.Contains(msg, refusal.want) {
			t.Errorf("removing %s = %q, want %q", what, msg, refusal.want)
		}
	}
	going := map[string]any{"backup": 3, "snapshots": []any{"aaaaaaaa", earlySnapshot, "bbbbbbbb"}}
	if preview := suite.Call(t, "backup_snapshot_delete", going); !slices.Equal(acctest.Strs(t, preview["would_go"], "would_go"), []string{"aaaaaaaa, taken 2026-10-11T03:00:04Z of the container web-front-1 on alpha", "bbbbbbbb, taken 2026-10-12T03:00:04Z of the container web-front-1 on alpha"}) || preview["note"] != "nothing was removed: confirm removes 2 snapshots for good" {
		t.Errorf("a removal not confirmed = %v", preview)
	}
	dock.Answer("POST /api/backup/snapshots/batch-delete", `{"success":true,"deleted":["`+earlySnapshot+`"],"skipped":["`+lateSnapshot+`"]}`)
	gone := suite.Call(t, "backup_snapshot_delete", over(going, map[string]any{"confirm": true}))
	if flat(gone) != `{"destination":"offsite","note":"1 snapshot Dockhand passed over and left where it was","removed":["aaaaaaaa"],"skipped":["bbbbbbbb"]}` || dock.Last("POST /api/backup/snapshots/batch-delete").Body != `{"destinationId":1,"snapshotIds":["`+earlySnapshot+`","`+lateSnapshot+`"]}` {
		t.Errorf("snapshots removed = %s, asked for as %s", flat(gone), dock.Last("POST /api/backup/snapshots/batch-delete").Body)
	}
}

func TestBackupRestore(t *testing.T) {
	t.Parallel()

	dock := withBackups(t)
	suite := session(t, dock, everything)
	at := map[string]any{"destination": "offsite", "snapshot": "aaaaaaaa"}
	dock.Answer(restoreLook, restoreWorkedOut)

	// one way is named, and what follows is one of three
	for what, refusal := range map[string]struct {
		args map[string]any
		want string
	}{
		"no way":            {nil, "one of in_place, to and volumes_to is required"},
		"two ways":          {map[string]any{"in_place": true, "to": "/restored"}, "and only one"},
		"a path that is no": {map[string]any{"to": "restored"}, "by its whole path"},
		"a word for after":  {map[string]any{"to": "/restored", "after": "sing"}, "it is none, start or recreate"},
		"a volume it lacks": {map[string]any{"volumes_to": map[string]any{"other": "copy"}}, "holds no volume other: it holds web_data"},
		"a volume nowhere":  {map[string]any{"volumes_to": map[string]any{"web_data": " "}}, "nowhere to put it"},
	} {
		if msg := suite.CallErr(t, "backup_restore", over(at, refusal.args)); !strings.Contains(msg, refusal.want) {
			t.Errorf("a restore with %s = %q, want %q", what, msg, refusal.want)
		}
	}

	// over what is there: said and not done until confirmed, with where each volume goes and what is there now
	if got := flat(suite.Call(t, "backup_restore", over(at, map[string]any{"in_place": true}))); got != `{"destination":"offsite","dry_run":true,"env":"alpha","mode":"in-place","note":"nothing was restored: confirm writes the snapshot over what targets says is there now, all of which is replaced","of":"web-front-1","running":false,"snapshot":"aaaaaaaa","targets":[{"kind":"volume","there":"has-data","to":"web_data","volume":"web_data"}]}` || len(dock.Requests(restoreCall)) != 0 {
		t.Fatalf("a restore over what is there, not confirmed = %s", got)
	}
	if body := dock.Last(restoreLook).Body; body != `{"destinationId":1,"environmentId":1,"includeTargets":true,"mode":"in-place","snapshotId":"aaaaaaaa","targetEnvId":1,"targetName":"web-front-1","targetType":"container"}` {
		t.Errorf("where a restore would write was asked as %s", body)
	}
	// confirmed, it runs as a job with nothing done after unless asked
	dock.Answer(restoreCall, `{"jobId":"j3"}`)
	dock.Answer("GET /api/jobs/j3", `{"id":"j3","status":"done","lines":[],"result":{"status":"warning","executionId":51,"restoredVolumes":["web_data"],"postRestore":{"action":"start","ok":false,"error":"target container no longer exists"}}}`)
	put := suite.Call(t, "backup_restore", over(at, map[string]any{"in_place": true, "confirm": true, "after": "start"}))
	if put["status"] != "warning" || put["after"] != "start could not be done: target container no longer exists" || !slices.Equal(acctest.Strs(t, put["restored"], "restored"), []string{"web_data"}) || acctest.Num(t, put["run"], "run") != 51 {
		t.Errorf("a restore over what is there = %v", put)
	}
	if body := dock.Last(restoreCall).Body; body != `{"confirmOverwrite":true,"destinationId":1,"environmentId":1,"mode":"in-place","postRestore":"start","snapshotId":"aaaaaaaa","targetName":"web-front-1","targetType":"container"}` || dock.Last(restoreCall).Accept != "*/*" {
		t.Errorf("a restore was asked for as %s", body)
	}

	// beside what is there: each volume to a volume or a path of its own, on another host when one is named, and nothing made after
	suite.Call(t, "backup_restore", over(at, map[string]any{"env": "beta", "volumes_to": map[string]any{"web_data": "/restored/web"}}))
	if body := dock.Last(restoreCall).Body; body != `{"destinationId":1,"environmentId":2,"mode":"new-location","postRestore":"none","snapshotId":"aaaaaaaa","targetName":"web-front-1","targetType":"container","volumeDestinations":[{"kind":"path","target":"/restored/web","volume":"web_data"}],"volumes":["web_data"]}` {
		t.Errorf("a restore to a path of its own was asked for as %s", body)
	}
	if dry := suite.Call(t, "backup_restore", over(at, map[string]any{"to": "/restored", "dry_run": true})); !acctest.IsBool(dry["dry_run"], true) || dry["mode"] != "new-location" || dock.Last(restoreLook).Body != `{"destinationId":1,"environmentId":1,"includeTargets":true,"mode":"new-location","snapshotId":"aaaaaaaa","targetEnvId":1,"targetName":"web-front-1","targetPath":"/restored","targetType":"container"}` {
		t.Errorf("a dry run to a folder = %v, asked as %s", dry, dock.Last(restoreLook).Body)
	}
	// a volume Dockhand has nowhere to put, and a restore that failed, are errors with why
	dock.Answer(restoreLook, strings.Replace(restoreWorkedOut, `"unresolved":[]`, `"unresolved":[{"key":"web_data","reason":"no destination mapping and no target path to extract to"}]`, 1))
	if msg := suite.CallErr(t, "backup_restore", over(at, map[string]any{"to": "/restored"})); !strings.Contains(msg, "Dockhand has nowhere to put 1 volume of the snapshot aaaaaaaa, so nothing was restored: web_data: no destination mapping") {
		t.Errorf("a volume with nowhere to go = %q", msg)
	}
	dock.Answer(restoreLook, restoreWorkedOut)
	dock.Answer("GET /api/jobs/j3", `{"id":"j3","status":"done","lines":[],"result":{"status":"error","code":"VALIDATION","error":"volume \"copy\" already exists on the target environment"}}`)
	if msg := suite.CallErr(t, "backup_restore", over(at, map[string]any{"volumes_to": map[string]any{"web_data": "copy"}})); !strings.Contains(msg, "was not restored on alpha") || !strings.Contains(msg, "already exists on the target environment") {
		t.Errorf("a restore that failed = %q", msg)
	}

	// over what is there needs deletes switched on: without them it is said and not done
	writesOnly := session(t, dock, Options{EnableWrite: true})
	if msg := writesOnly.CallErr(t, "backup_restore", over(at, map[string]any{"in_place": true, "confirm": true})); !strings.Contains(msg, "nothing is removed without --enable-delete") {
		t.Errorf("a restore over what is there with deletes off = %q", msg)
	}
	if said := writesOnly.Call(t, "backup_restore", over(at, map[string]any{"in_place": true})); !acctest.IsBool(said["dry_run"], true) {
		t.Errorf("a restore over what is there with deletes off, not confirmed = %v", said)
	}
}
