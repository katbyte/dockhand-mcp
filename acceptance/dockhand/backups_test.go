//go:build acceptance

package dockhand_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/pandorest/client"
)

// backupPassword is what the suites' repositories are encrypted with: made up, and good for nothing but the throwaway backup server.
const backupPassword = "it-not-a-real-backup-password" //nolint:gosec // G101: a made-up value for the throwaway server, which is no credential of anything real

// repositoryOn is a repository of a run's own on a backup server: what a run before left there stays there, the server having no call to remove one.
func repositoryOn(server, name string) string {
	return server + "/" + name + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

// backupsGone removes every backup destination a test of this file made, with the configurations kept in it, before the test starts in case a run
// cut short left one and when it ends.
func backupsGone(t *testing.T, prefix string) {
	t.Helper()

	c := dock(t)
	ctx := noCancel(t)
	clean := func() {
		listed, err := c.GetBackupDestinations(ctx)
		if err != nil {
			t.Errorf("listing the backup destinations to tidy up: %v", err)

			return
		}
		for i := range listed.Answer {
			if strings.HasPrefix(listed.Answer[i].Name, prefix) {
				_, _ = c.DeleteBackupDestinationsById(ctx, listed.Answer[i].Id)
			}
		}
	}
	clean()
	t.Cleanup(clean)
}

// backedUp is a container on alpha with a volume it has written two files to and a variable that is a secret, for a test to back up.
func backedUp(t *testing.T) (container, volume string) {
	t.Helper()

	volume = named(t, "data")
	ctx := noCancel(t)
	_, _ = world.Docker(ctx, testenv.Alpha, "volume", "rm", "-f", volume)
	container = started(t, testenv.Alpha, "app", "sh", "-c", "sleep 100000")
	// started again with its volume: started has no way to give a container one
	if _, err := world.Docker(ctx, testenv.Alpha, "rm", "-f", container); err != nil {
		t.Fatal(err)
	}
	if _, err := world.Docker(ctx, testenv.Alpha, "run", "-d", "--name", container, "-e", "API_TOKEN=it-not-a-real-token", "-v", volume+":/data", world.Image, "sh", "-c", "echo first > /data/notes.txt; mkdir -p /data/sub; echo deep > /data/sub/deep.txt; sleep 100000"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = world.Docker(ctx, testenv.Alpha, "volume", "rm", "-f", volume) })

	return container, volume
}

// A backup destination is tried, saved, changed, looked after and removed, with what Dockhand saves untried and what it refuses.
func TestBackupDestinationsTriedSavedChangedAndRemoved(t *testing.T) {
	c := dock(t)
	prefix := named(t, "")
	backupsGone(t, prefix)
	repository := repositoryOn(world.Backups, prefix)

	// every Dockhand has an id of its own, which it marks its snapshots with
	if instance, err := c.GetBackupInstance(t.Context()); err != nil || instance.Answer.InstanceId == "" {
		t.Fatalf("the instance = %+v, %v", instance.Answer, err)
	}

	// tried unsaved: a kind of address it does not take and its own address are refused, and nothing there reads as a repository to be made,
	// whether a server answers there or none does
	for repository, want := range map[string]string{"ftp://nowhere.example/srv": "Invalid repository", "rest:http://127.0.0.1:8000/x": "not allowed"} {
		if _, err := c.PostBackupDestinationsTest(t.Context(), dockhand.PostBackupDestinationsTestRequest{Repository: repository, Password: backupPassword}); client.StatusCode(err) != http.StatusBadRequest || !strings.Contains(err.Error(), want) {
			t.Errorf("%s tried = %v, want a 400 saying %s", repository, err, want)
		}
	}
	for _, nothing := range []string{repository, "rest:http://it-nobody-home:8000/x"} {
		tried, err := c.PostBackupDestinationsTest(t.Context(), dockhand.PostBackupDestinationsTestRequest{Repository: nothing, Password: backupPassword})
		if err != nil || tried.Answer.Success || !tried.Answer.NeedsInit {
			t.Errorf("%s tried = %+v, %v, want one that needs making", nothing, tried.Answer, err)
		}
	}

	// saved, which makes the repository: the record comes back without the password, and the list without what it is signed in to with
	made, err := c.PostBackupDestinations(t.Context(), dockhand.PostBackupDestinationsRequest{Name: prefix + "store", Repository: repository, Password: backupPassword, EnvVars: map[string]any{"IT_REGION": "nowhere"}})
	if err != nil || made.Answer.Id == 0 || made.Answer.Name != prefix+"store" || made.Answer.Repository != repository || made.Answer.EnvVars["IT_REGION"] != "nowhere" || made.Answer.CreatedAt.IsZero() {
		t.Fatalf("a destination saved = %+v, %v", made.Answer, err)
	}
	id := made.Answer.Id
	if _, err := c.PostBackupDestinations(t.Context(), dockhand.PostBackupDestinationsRequest{Name: prefix + "store", Repository: repository, Password: backupPassword}); client.StatusCode(err) != http.StatusConflict {
		t.Errorf("a second destination of the same name = %v, want a 409", err)
	}
	one, err := c.GetBackupDestinationsById(t.Context(), id)
	if err != nil || one.Answer.LastTestStatus != "success" || one.Answer.LastTestAt.IsZero() || one.Answer.EnvVars["IT_REGION"] != "nowhere" || !strings.Contains(one.Answer.Policies, `"pruneEnabled":true`) {
		t.Fatalf("the destination read = %+v, %v", one.Answer, err)
	}
	listed, err := c.GetBackupDestinations(t.Context())
	i := slices.IndexFunc(listed.Answer, func(d dockhand.GetBackupDestinationsAnswer) bool { return d.Id == id })
	if err != nil || i < 0 || listed.Answer[i].EnvVars != nil || listed.Answer[i].LastTestStatus != "success" {
		t.Fatalf("the destination listed = %+v, %v", listed.Answer, err)
	}
	if _, err := c.GetBackupDestinationsById(t.Context(), 999999); !client.IsNotFound(err) {
		t.Errorf("a destination there is none of = %v", err)
	}

	// one whose password is not the repository's is saved all the same, and only its last try says so
	wrong, err := c.PostBackupDestinations(t.Context(), dockhand.PostBackupDestinationsRequest{Name: prefix + "wrong", Repository: repository, Password: "it-not-the-password"})
	if err != nil {
		t.Fatalf("a destination with a wrong password = %v, where Dockhand saves one", err)
	}
	if read, err := c.GetBackupDestinationsById(t.Context(), wrong.Answer.Id); err != nil || read.Answer.LastTestStatus != "failed" || !strings.Contains(read.Answer.LastTestError, "wrong") {
		t.Errorf("a destination saved with a wrong password reads %+v, %v", read.Answer, err)
	}
	if tried, err := c.PostBackupDestinationsTest(t.Context(), dockhand.PostBackupDestinationsTestRequest{Repository: repository, Password: "it-not-the-password"}); err != nil || tried.Answer.Success || tried.Answer.NeedsInit || !strings.Contains(tried.Answer.Error, "wrong password") {
		t.Errorf("the repository tried with a wrong password = %+v, %v", tried.Answer, err)
	}
	if _, err := c.DeleteBackupDestinationsById(t.Context(), wrong.Answer.Id); err != nil {
		t.Fatal(err)
	}

	// tried saved, both ways there are
	if tried, err := c.PostBackupDestinationsByIdTest(t.Context(), id); err != nil || !tried.Answer.Success || tried.Answer.Status != "success" {
		t.Errorf("the saved destination tried = %+v, %v", tried.Answer, err)
	}
	if tried, err := c.PostBackupDestinationsTest(t.Context(), dockhand.PostBackupDestinationsTestRequest{DestinationId: id}); err != nil || !tried.Answer.Success {
		t.Errorf("the saved destination tried by its number = %+v, %v", tried.Answer, err)
	}

	// changed: its flags are kept as one text of the two, a flag Dockhand does not allow and a schedule that is none are refused, and what it is
	// signed in to with is replaced whole
	changed, err := c.PutBackupDestinationsById(t.Context(), id, dockhand.PutBackupDestinationsByIdRequest{Name: prefix + "renamed", BackupFlags: "--limit-upload 500", Policies: `{"pruneEnabled":false,"verifyEnabled":true,"verifySchedule":"0 4 * * 0"}`, EnvVars: map[string]any{"IT_OTHER": "elsewhere"}})
	if err != nil || changed.Answer.Name != prefix+"renamed" || changed.Answer.BackupFlags != "--limit-upload 500" || !strings.Contains(changed.Answer.Flags, `"backup":"--limit-upload 500"`) || !strings.Contains(changed.Answer.Policies, `"verifyEnabled":true`) {
		t.Fatalf("the destination changed = %+v, %v", changed.Answer, err)
	}
	if _, has := changed.Answer.EnvVars["IT_REGION"]; has || changed.Answer.EnvVars["IT_OTHER"] != "elsewhere" {
		t.Errorf("what the destination is signed in to with after a change = %v, want the new set alone", changed.Answer.EnvVars)
	}
	for what, request := range map[string]dockhand.PutBackupDestinationsByIdRequest{
		"Disallowed restic flag":  {BackupFlags: "--password-command true"},
		"Invalid cron expression": {Policies: `{"checkEnabled":true,"checkSchedule":"whenever"}`},
		"Invalid policies JSON":   {Policies: "not json"},
	} {
		if _, err := c.PutBackupDestinationsById(t.Context(), id, request); client.StatusCode(err) != http.StatusBadRequest || !strings.Contains(err.Error(), what) {
			t.Errorf("a change of %+v = %v, want a 400 saying %s", request, err, what)
		}
	}
	if cleared, err := c.ForgetBackupDestinationFlags(t.Context(), id, dockhand.BackupDestinationFlags{Backup: true}); err != nil || cleared.Answer.BackupFlags != "" {
		t.Errorf("the destination's flags cleared = %+v, %v", cleared.Answer, err)
	}
	// a password that is not the repository's is saved without a word, and only a try says so
	if _, err := c.PutBackupDestinationsById(t.Context(), id, dockhand.PutBackupDestinationsByIdRequest{Password: "it-not-the-password"}); err != nil {
		t.Fatalf("a wrong password saved = %v, where Dockhand saves one", err)
	}
	if tried, err := c.PostBackupDestinationsByIdTest(t.Context(), id); err != nil || tried.Answer.Success || tried.Answer.Status != "failed" {
		t.Errorf("the destination tried with a wrong password kept = %+v, %v", tried.Answer, err)
	}
	if _, err := c.PutBackupDestinationsById(t.Context(), id, dockhand.PutBackupDestinationsByIdRequest{Password: backupPassword}); err != nil {
		t.Fatal(err)
	}

	// its upkeep is schedules of its own among what Dockhand does by itself
	schedules, err := c.GetSchedules(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[dockhand.ScheduleType]bool{}
	for _, schedule := range schedules.Answer.Schedules {
		if schedule.EntityName == prefix+"renamed" {
			kinds[schedule.Type] = true
		}
	}
	// the upkeep that was sent replaced the whole of what was kept: the check it had before, which was not named, is gone with the prune
	if !kinds["repo_verify"] || kinds["repo_check"] || kinds["repo_prune"] {
		t.Errorf("the schedules of a destination whose upkeep was replaced with a verify alone = %v", kinds)
	}

	// looked after: what restic is asked to do, and what it finds wrong, come back as an answer and not as a failure
	stats, err := c.PostBackupDestinationsByIdTask(t.Context(), id, dockhand.PostBackupDestinationsByIdTaskRequest{Task: "stats"})
	if err != nil || !stats.Answer.Success || stats.Answer.Stats == nil || stats.Answer.Stats.Snapshots != 0 {
		t.Errorf("the repository's stats = %+v, %v", stats.Answer, err)
	}
	for _, task := range []string{"check", "unlock", "prune", "repair-index", "repair-snapshots"} {
		if did, err := c.PostBackupDestinationsByIdTask(t.Context(), id, dockhand.PostBackupDestinationsByIdTaskRequest{Task: task}); err != nil || !did.Answer.Success || did.Answer.Output == "" {
			t.Errorf("the task %s = %+v, %v", task, did.Answer, err)
		}
	}
	if _, err := c.PostBackupDestinationsByIdTask(t.Context(), id, dockhand.PostBackupDestinationsByIdTaskRequest{Task: "dance"}); client.StatusCode(err) != http.StatusBadRequest {
		t.Errorf("a task there is none of = %v, want a 400", err)
	}
	job, err := c.PostBackupDestinationsByIdTask(t.Context(), id, dockhand.PostBackupDestinationsByIdTaskRequest{Task: "stats"}, dockhand.PostBackupDestinationsByIdTaskOptions{Accept: dockhand.AcceptJob})
	if err != nil || job.Answer.JobId == "" {
		t.Fatalf("a task asked for as a job = %+v, %v", job.Answer, err)
	}
	done := ended(t, job.Answer.JobId)
	if went, said := done.Result["success"].(bool); done.Status != "done" || !said || !went {
		t.Errorf("the job of a task = %+v", done)
	}
	if verified, err := c.PostBackupDestinationsByIdVerify(t.Context(), id, dockhand.PostBackupDestinationsByIdVerifyRequest{DataSubset: "100%"}); err != nil || !verified.Answer.Success {
		t.Errorf("the repository verified = %+v, %v", verified.Answer, err)
	}
	if verified, err := c.PostBackupDestinationsByIdVerify(t.Context(), id, dockhand.PostBackupDestinationsByIdVerifyRequest{DataSubset: "lots"}); err != nil || verified.Answer.Success || verified.Answer.Error == "" {
		t.Errorf("a verify of a part that is none = %+v, %v, want it answered as a failure of restic's", verified.Answer, err)
	}
	if made, err := c.PostBackupDestinationsByIdInit(t.Context(), id); err != nil || !made.Answer.Success {
		t.Errorf("making a repository that is there = %+v, %v", made.Answer, err)
	}

	// its password changed in the repository itself: not with a wrong one, a short one or the same one
	for what, request := range map[string]dockhand.PostBackupDestinationsByIdRotateKeyRequest{
		"does not match": {CurrentPassword: "it-not-the-password", NewPassword: backupPassword + "-2"},
		"at least 8":     {CurrentPassword: backupPassword, NewPassword: "short"},
		"must differ":    {CurrentPassword: backupPassword, NewPassword: backupPassword},
	} {
		if _, err := c.PostBackupDestinationsByIdRotateKey(t.Context(), id, request); client.StatusCode(err) != http.StatusBadRequest || !strings.Contains(err.Error(), what) {
			t.Errorf("a new password refused = %v, want a 400 saying %s", err, what)
		}
	}
	if turned, err := c.PostBackupDestinationsByIdRotateKey(t.Context(), id, dockhand.PostBackupDestinationsByIdRotateKeyRequest{CurrentPassword: backupPassword, NewPassword: backupPassword + "-2"}); err != nil || !turned.Answer.Success {
		t.Fatalf("the repository's password changed = %+v, %v", turned.Answer, err)
	}
	if tried, err := c.PostBackupDestinationsByIdTest(t.Context(), id); err != nil || !tried.Answer.Success {
		t.Errorf("the destination tried after its password changed = %+v, %v", tried.Answer, err)
	}
	if tried, err := c.PostBackupDestinationsTest(t.Context(), dockhand.PostBackupDestinationsTestRequest{Repository: repository, Password: backupPassword}); err != nil || tried.Answer.Success {
		t.Errorf("the repository tried with the password it had = %+v, %v", tried.Answer, err)
	}

	// removed, and with it every configuration kept in it, which Dockhand says nothing of
	kept, err := c.PostBackupConfigs(t.Context(), dockhand.PostBackupConfigsRequest{DestinationId: id, TargetName: "it-anything", Type: "container", EnvironmentId: world.Env(testenv.Alpha)})
	if err != nil {
		t.Fatal(err)
	}
	if gone, err := c.DeleteBackupDestinationsById(t.Context(), id); err != nil || !gone.Answer.Success {
		t.Fatalf("the destination removed = %+v, %v", gone.Answer, err)
	}
	if _, err := c.GetBackupConfigsById(t.Context(), kept.Answer.Id); !client.IsNotFound(err) {
		t.Errorf("a configuration kept in a destination that was removed = %v, want it gone with it", err)
	}
	if _, err := c.DeleteBackupDestinationsById(t.Context(), id); !client.IsNotFound(err) {
		t.Errorf("a destination removed twice = %v", err)
	}
}

// A backup configuration is saved, changed and removed, with what Dockhand saves unchecked and what it changes that it was not asked to.
func TestBackupConfigsSavedChangedAndRemoved(t *testing.T) {
	c := dock(t)
	prefix := named(t, "")
	backupsGone(t, prefix)
	env := world.Env(testenv.Alpha)
	made, err := c.PostBackupDestinations(t.Context(), dockhand.PostBackupDestinationsRequest{Name: prefix + "store", Repository: repositoryOn(world.Backups, prefix), Password: backupPassword})
	if err != nil {
		t.Fatal(err)
	}
	destination := made.Answer.Id

	// saved unchecked: a name nothing has, a kind that is none, no environment, and a volume there is none of
	for what, request := range map[string]dockhand.PostBackupConfigsRequest{
		"of a container there is none of": {DestinationId: destination, TargetName: "it-no-such-container", Type: "container", EnvironmentId: env},
		"of a kind that is none":          {DestinationId: destination, TargetName: "it-anything", Type: "thing", EnvironmentId: env},
		"with no environment":             {DestinationId: destination, TargetName: "it-anything", Type: "container"},
		"of a volume there is none of":    {DestinationId: destination, TargetName: "it-anything", Type: "container", EnvironmentId: env, AllVolumes: new(false), SelectedVolumes: []string{"it-no-such-volume"}},
	} {
		saved, refused := c.PostBackupConfigs(t.Context(), request)
		if refused != nil || saved.Answer.Id == 0 || saved.Answer.Type != request.Type || saved.Answer.EnvironmentId != request.EnvironmentId {
			t.Errorf("a configuration %s = %+v, %v, where Dockhand saves one", what, saved.Answer, refused)
		}
	}
	// refused: an environment and a destination there is none of, as a failure of its database, and a schedule, a count and a name that are none
	for what, refusal := range map[string]struct {
		request dockhand.PostBackupConfigsRequest
		status  int
	}{
		"FOREIGN KEY":             {dockhand.PostBackupConfigsRequest{DestinationId: destination, TargetName: "it-anything", Type: "container", EnvironmentId: 999999}, http.StatusInternalServerError},
		"Invalid cron expression": {dockhand.PostBackupConfigsRequest{DestinationId: destination, TargetName: "it-anything", EnvironmentId: env, Schedule: "sometimes"}, http.StatusBadRequest},
		"Invalid retention":       {dockhand.PostBackupConfigsRequest{DestinationId: destination, TargetName: "it-anything", EnvironmentId: env, Retention: &dockhand.PostBackupConfigsRequestRetention{KeepLast: 10001}}, http.StatusBadRequest},
		"Invalid targetName":      {dockhand.PostBackupConfigsRequest{DestinationId: destination, TargetName: "../etc", EnvironmentId: env}, http.StatusBadRequest},
	} {
		if _, err := c.PostBackupConfigs(t.Context(), refusal.request); client.StatusCode(err) != refusal.status || !strings.Contains(err.Error(), what) {
			t.Errorf("a configuration refused = %v, want a %d saying %s", err, refusal.status, what)
		}
	}

	// one in full: its lists and what it keeps come back as JSON written out as text
	saved, err := c.PostBackupConfigs(t.Context(), dockhand.PostBackupConfigsRequest{DestinationId: destination, TargetName: "it-app", Type: "container", EnvironmentId: env, AllVolumes: new(false), SelectedVolumes: []string{"it-data"}, StopBeforeBackup: new(true), Retention: &dockhand.PostBackupConfigsRequestRetention{KeepLast: 2}, Tags: []string{"it"}})
	if err != nil || saved.Answer.SelectedVolumes != `["it-data"]` || saved.Answer.Retention != `{"keepLast":2}` || saved.Answer.Tags != `["it"]` || saved.Answer.AllVolumes || !saved.Answer.StopBeforeBackup || !saved.Answer.Enabled || saved.Answer.Schedule != "" || saved.Answer.CreatedAt.IsZero() {
		t.Fatalf("a configuration saved in full = %+v, %v", saved.Answer, err)
	}
	id := saved.Answer.Id
	listed, err := c.GetBackupConfigs(t.Context(), dockhand.GetBackupConfigsOptions{Target: "it-app", Env: env})
	if err != nil || len(listed.Answer) != 1 || listed.Answer[0].Id != id {
		t.Errorf("the configurations of one target = %+v, %v", listed.Answer, err)
	}
	if _, err := c.GetBackupConfigsById(t.Context(), 999999); !client.IsNotFound(err) {
		t.Errorf("a configuration there is none of = %v", err)
	}

	// changed: what is not sent is kept, but for what it keeps, which a schedule sent alone has Dockhand write over with a policy of its own
	read := func(what string, changed dockhand.PutBackupConfigsByIdResponse, err error) *dockhand.PutBackupConfigsByIdAnswer {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}

		return changed.Answer
	}
	change := func(what string, request dockhand.PutBackupConfigsByIdRequest) *dockhand.PutBackupConfigsByIdAnswer {
		t.Helper()
		changed, refused := c.PutBackupConfigsById(t.Context(), id, request)

		return read(what, changed, refused)
	}
	if kept := change("stop first alone", dockhand.PutBackupConfigsByIdRequest{StopBeforeBackup: new(false)}); kept.StopBeforeBackup || kept.Retention != `{"keepLast":2}` || kept.SelectedVolumes != `["it-data"]` {
		t.Errorf("a change of one thing = %+v, want the rest kept", kept)
	}
	if scheduled := change("a schedule alone", dockhand.PutBackupConfigsByIdRequest{Schedule: "0 3 * * *"}); scheduled.Schedule != "0 3 * * *" || scheduled.Retention != `{"keepDaily":7,"keepWeekly":4,"keepMonthly":6}` {
		t.Errorf("a schedule sent alone = %+v, where Dockhand writes its own policy over what was kept", scheduled)
	}
	if kept := change("a schedule with what to keep", dockhand.PutBackupConfigsByIdRequest{Schedule: "0 4 * * *", Retention: &dockhand.PutBackupConfigsByIdRequestRetention{KeepLast: 5}}); kept.Schedule != "0 4 * * *" || kept.Retention != `{"keepLast":5}` {
		t.Errorf("a schedule sent with what to keep = %+v", kept)
	}
	// the schedule cleared, which the generated call cannot ask for: with what to keep beside it, and with nothing, which keeps every snapshot
	cleared, err := c.ForgetBackupConfigSchedule(t.Context(), id, &dockhand.PutBackupConfigsByIdRequestRetention{KeepLast: 5})
	if now := read("the schedule cleared", cleared, err); now.Schedule != "" || now.Retention != `{"keepLast":5}` {
		t.Errorf("the schedule cleared with what to keep = %+v", now)
	}
	cleared, err = c.ForgetBackupConfigSchedule(t.Context(), id, nil)
	if now := read("the schedule cleared again", cleared, err); now.Schedule != "" || now.Retention != "" {
		t.Errorf("the schedule cleared with nothing to keep = %+v", now)
	}
	// given a schedule where it had none, it is switched on whatever is said, and the same again is then taken
	off := dockhand.PutBackupConfigsByIdRequest{Schedule: "0 5 * * *", Enabled: new(false), Retention: &dockhand.PutBackupConfigsByIdRequestRetention{KeepLast: 5}}
	if first := change("a schedule switched off, where there was none", off); !first.Enabled {
		t.Errorf("a schedule given switched off to a configuration that had none = %+v, where Dockhand switches it on", first)
	}
	if second := change("the same again", off); second.Enabled {
		t.Errorf("the same change again = %+v, want it switched off", second)
	}
	for what, refusal := range map[string]struct {
		request dockhand.PutBackupConfigsByIdRequest
		status  int
	}{
		"Invalid cron expression": {dockhand.PutBackupConfigsByIdRequest{Schedule: "whenever"}, http.StatusBadRequest},
		"FOREIGN KEY":             {dockhand.PutBackupConfigsByIdRequest{DestinationId: 999999}, http.StatusInternalServerError},
	} {
		if _, err := c.PutBackupConfigsById(t.Context(), id, refusal.request); client.StatusCode(err) != refusal.status || !strings.Contains(err.Error(), what) {
			t.Errorf("a change refused = %v, want a %d saying %s", err, refusal.status, what)
		}
	}

	// a configuration that runs by itself is a schedule among what Dockhand does unasked
	schedules, err := c.GetSchedules(t.Context())
	if err != nil || !slices.ContainsFunc(schedules.Answer.Schedules, func(s dockhand.GetSchedulesAnswerSchedules) bool {
		return s.Type == "backup" && s.EntityName == "it-app"
	}) {
		t.Errorf("the schedules do not hold the backup of it-app: %v", err)
	}

	// nothing runs, so there is nothing to stop; and removed, once and not twice
	if stopped, err := c.PostBackupConfigsByIdStop(t.Context(), id); err != nil || !stopped.Answer.Success || stopped.Answer.Stopped {
		t.Errorf("stopping a configuration that is not running = %+v, %v", stopped.Answer, err)
	}
	if gone, err := c.DeleteBackupConfigsById(t.Context(), id); err != nil || !gone.Answer.Success || gone.Answer.Snapshots != nil {
		t.Fatalf("the configuration removed = %+v, %v", gone.Answer, err)
	}
	if _, err := c.DeleteBackupConfigsById(t.Context(), id); !client.IsNotFound(err) {
		t.Errorf("a configuration removed twice = %v", err)
	}
}

// A container is backed up, and its snapshots are listed, looked into, read from, compared, restored and removed.
func TestBackupsRunLookedIntoRestoredAndRemoved(t *testing.T) {
	c := dock(t)
	ctx := noCancel(t)
	prefix := named(t, "")
	if err := world.MirrorBackupImage(ctx); err != nil {
		t.Fatal(err)
	}
	backupsGone(t, prefix)
	env := world.Env(testenv.Alpha)
	container, volume := backedUp(t)
	made, err := c.PostBackupDestinations(t.Context(), dockhand.PostBackupDestinationsRequest{Name: prefix + "store", Repository: repositoryOn(world.Backups, prefix), Password: backupPassword})
	if err != nil {
		t.Fatal(err)
	}
	destination := made.Answer.Id
	saved, err := c.PostBackupConfigs(t.Context(), dockhand.PostBackupConfigsRequest{DestinationId: destination, TargetName: container, Type: "container", EnvironmentId: env})
	if err != nil {
		t.Fatal(err)
	}
	config := saved.Answer.Id

	// run, which a caller that asks for JSON alone waits for: a snapshot, and how much it took
	first, err := c.PostBackupConfigsByIdRun(t.Context(), config)
	if err != nil || first.Answer.Status != "success" || len(first.Answer.SnapshotId) != 64 || first.Answer.Summary == nil || first.Answer.Summary.TotalFilesProcessed == 0 || first.Answer.Retention != "none" || first.Answer.ExecutionId == 0 {
		t.Fatalf("the first run = %+v, %v", first.Answer, err)
	}
	if _, err := world.Docker(ctx, testenv.Alpha, "exec", container, "sh", "-c", "echo second > /data/notes.txt; echo more > /data/added.txt"); err != nil {
		t.Fatal(err)
	}
	// and as a job, whose result is the same answer
	started, err := c.PostBackupConfigsByIdRun(t.Context(), config, dockhand.PostBackupConfigsByIdRunOptions{Accept: dockhand.AcceptJob})
	if err != nil || started.Answer.JobId == "" || started.Answer.Status != "" {
		t.Fatalf("a run asked for as a job = %+v, %v", started.Answer, err)
	}
	job := ended(t, started.Answer.JobId)
	later, said := job.Result["snapshotId"].(string)
	if job.Status != "done" || job.Result["status"] != "success" || !said || len(later) != 64 {
		t.Fatalf("the job of a run = %+v", job)
	}
	early := first.Answer.SnapshotId
	if read, err := c.GetBackupConfigsById(t.Context(), config); err != nil || read.Answer.LastBackupStatus != "success" || read.Answer.LastBackupAt.IsZero() {
		t.Errorf("the configuration after two runs = %+v, %v", read.Answer, err)
	}

	// a run of something that is not there fails as an answer, with the kind of failure, and not as an error status
	nothing, err := c.PostBackupConfigs(t.Context(), dockhand.PostBackupConfigsRequest{DestinationId: destination, TargetName: "it-no-such-container", Type: "container", EnvironmentId: env})
	if err != nil {
		t.Fatal(err)
	}
	if failed, err := c.PostBackupConfigsByIdRun(t.Context(), nothing.Answer.Id); err != nil || failed.Answer.Status != "error" || failed.Answer.Code != "VALIDATION" || !strings.Contains(failed.Answer.Error, "refusing to back up nothing") {
		t.Errorf("a run of a container there is none of = %+v, %v", failed.Answer, err)
	}
	if _, err := c.PostBackupConfigsByIdRun(t.Context(), 999999); !client.IsNotFound(err) {
		t.Errorf("a run of a configuration there is none of = %v", err)
	}

	// the snapshots, by the configuration and by the destination, the newest first, each marked with what it is of
	taken, err := c.GetBackupSnapshots(t.Context(), dockhand.GetBackupSnapshotsOptions{ConfigId: config})
	if err != nil || len(taken.Answer) != 2 || taken.Answer[0].Id != later || taken.Answer[1].Id != early || taken.Answer[0].ShortId != later[:8] || !slices.Contains(taken.Answer[0].Tags, "dockhand:name="+container) || !slices.Contains(taken.Answer[0].Tags, "dockhand:type=container") {
		t.Fatalf("the snapshots of the configuration = %+v, %v", taken.Answer, err)
	}
	if _, err := time.Parse(time.RFC3339Nano, taken.Answer[0].Time); err != nil {
		t.Errorf("when a snapshot was taken is written %q: %v", taken.Answer[0].Time, err)
	}
	if held, err := c.GetBackupSnapshots(t.Context(), dockhand.GetBackupSnapshotsOptions{DestinationId: destination}); err != nil || len(held.Answer) != 2 {
		t.Errorf("the snapshots of the destination = %+v, %v", held.Answer, err)
	}
	if _, err := c.GetBackupSnapshots(t.Context()); client.StatusCode(err) != http.StatusBadRequest {
		t.Errorf("the snapshots of nothing named = %v, want a 400", err)
	}
	if _, err := c.GetBackupSnapshots(t.Context(), dockhand.GetBackupSnapshotsOptions{ConfigId: 999999}); !client.IsNotFound(err) {
		t.Errorf("the snapshots of a configuration there is none of = %v", err)
	}
	// a destination there is none of is answered as a success that holds an error, which is no list
	if _, err := c.GetBackupSnapshots(t.Context(), dockhand.GetBackupSnapshotsOptions{DestinationId: 999999}); err == nil || client.StatusCode(err) != 0 {
		t.Errorf("the snapshots of a destination there is none of = %v, want an answer that does not read as a list", err)
	}

	// looked into: a folder, one that is not there, which reads as empty, and a snapshot that is not there, which is an error in a success
	at := dockhand.GetBackupSnapshotsByIdBrowseOptions{DestinationId: destination, Path: "/volumes/" + volume}
	inside, err := c.GetBackupSnapshotsByIdBrowse(t.Context(), early[:8], at)
	notes := slices.IndexFunc(inside.Answer.Entries, func(e dockhand.GetBackupSnapshotsByIdBrowseAnswerEntries) bool { return e.Name == "notes.txt" })
	if err != nil || len(inside.Answer.Entries) != 2 || notes < 0 || inside.Answer.Entries[notes].Type != "file" || inside.Answer.Entries[notes].Size != 6 || inside.Answer.Entries[notes].Mtime.IsZero() || inside.Answer.Path != "/volumes/"+volume {
		t.Fatalf("what the first snapshot holds of the volume = %+v, %v", inside.Answer, err)
	}
	if nowhere, err := c.GetBackupSnapshotsByIdBrowse(t.Context(), early, dockhand.GetBackupSnapshotsByIdBrowseOptions{DestinationId: destination, Path: "/volumes/it-nothing"}); err != nil || len(nowhere.Answer.Entries) != 0 || nowhere.Answer.Error != "" {
		t.Errorf("a folder the snapshot does not have = %+v, %v, want it empty", nowhere.Answer, err)
	}
	if unknown, err := c.GetBackupSnapshotsByIdBrowse(t.Context(), "deadbeef", at); err != nil || !strings.Contains(unknown.Answer.Error, "snapshot not found") {
		t.Errorf("a snapshot there is none of, looked into = %+v, %v", unknown.Answer, err)
	}

	// what Dockhand wrote down of the container, with its variables left out
	record, err := c.GetBackupSnapshotsByIdMetadata(t.Context(), early, dockhand.GetBackupSnapshotsByIdMetadataOptions{DestinationId: destination})
	if err != nil || record.Answer.Type != "container" || record.Answer.TargetName != container || record.Answer.EnvironmentId != env || record.Answer.BackupTime.IsZero() || len(record.Answer.Volumes) != 1 || record.Answer.Volumes[0].Key != volume || record.Answer.HasStackFiles {
		t.Fatalf("the snapshot's record = %+v, %v", record.Answer, err)
	}
	if written, _ := json.Marshal(record.Answer.Container); len(record.Answer.Container) == 0 || strings.Contains(string(written), "it-not-a-real-token") {
		t.Errorf("the container as the snapshot's record has it holds its token, or nothing: %s", written)
	}

	// read from: a file as text inside JSON, and with download as the file itself; a folder as a tar; the record itself not at all
	file := dockhand.GetBackupSnapshotsByIdDumpOptions{DestinationId: destination, Path: "/volumes/" + volume + "/notes.txt"}
	inline, err := c.GetBackupSnapshotsByIdDump(t.Context(), early, file)
	if err != nil {
		t.Fatal(err)
	}
	if text, err := inline.Bytes(1 << 20); err != nil || string(text) != `{"content":"first\n"}` {
		t.Errorf("a file read out of the snapshot = %s, %v", text, err)
	}
	file.Download = "1"
	raw, err := c.GetBackupSnapshotsByIdDump(t.Context(), early, file)
	if err != nil {
		t.Fatal(err)
	}
	if text, err := raw.Bytes(1 << 20); err != nil || string(text) != "first\n" || raw.Filename() != "notes.txt" {
		t.Errorf("a file fetched out of the snapshot = %q named %q, %v", text, raw.Filename(), err)
	}
	folder, err := c.GetBackupSnapshotsByIdDump(t.Context(), early, dockhand.GetBackupSnapshotsByIdDumpOptions{DestinationId: destination, Path: "/volumes/" + volume, Download: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if archive, err := folder.Bytes(1 << 20); err != nil || !strings.Contains(string(archive), "sub/deep.txt") || !strings.Contains(string(archive), "ustar") {
		t.Errorf("a folder fetched out of the snapshot is no tar of it: %d bytes, %v", len(archive), err)
	}
	for what, refusal := range map[string]struct {
		path   string
		status int
	}{
		"cannot be downloaded raw": {"/metadata/metadata.json", http.StatusForbidden},
		"not found in snapshot":    {"/volumes/" + volume + "/it-nothing.txt", http.StatusInternalServerError},
		"Invalid path":             {"/etc/passwd", http.StatusBadRequest},
	} {
		if _, err := c.GetBackupSnapshotsByIdDump(t.Context(), early, dockhand.GetBackupSnapshotsByIdDumpOptions{DestinationId: destination, Path: refusal.path, Download: "1"}); client.StatusCode(err) != refusal.status || !strings.Contains(err.Error(), what) {
			t.Errorf("%s fetched = %v, want a %d saying %s", refusal.path, err, refusal.status, what)
		}
	}

	// compared
	differs, err := c.GetBackupSnapshotsDiff(t.Context(), dockhand.GetBackupSnapshotsDiffOptions{DestinationId: destination, SnapshotA: early, SnapshotB: later})
	if err != nil || !slices.Contains(differs.Answer.Added, "/volumes/"+volume+"/added.txt") || !slices.Contains(differs.Answer.Modified, "/volumes/"+volume+"/notes.txt") || len(differs.Answer.Removed) != 0 || differs.Answer.Raw == "" {
		t.Errorf("what differs between the two snapshots = %+v, %v", differs.Answer, err)
	}
	if _, err := c.GetBackupSnapshotsDiff(t.Context(), dockhand.GetBackupSnapshotsDiffOptions{DestinationId: destination, SnapshotA: early}); client.StatusCode(err) != http.StatusBadRequest {
		t.Errorf("a comparison with one snapshot = %v, want a 400", err)
	}

	// what a restore would write, worked out before anything is: over what is there, and to a volume of its own
	preview, err := c.PostBackupRestorePreview(t.Context(), dockhand.PostBackupRestorePreviewRequest{DestinationId: destination, SnapshotId: early, IncludeTargets: new(true), Mode: "in-place", TargetType: "container", EnvironmentId: env})
	if err != nil || !slices.Equal(preview.Answer.Volumes, []string{volume}) || preview.Answer.SourceEnvironmentId != env || !preview.Answer.HasMetadata || preview.Answer.Targets == nil || len(preview.Answer.Targets.Volumes) != 1 || preview.Answer.Targets.Volumes[0].Target != volume || preview.Answer.Targets.Volumes[0].HasData != "has-data" {
		t.Fatalf("what a restore over what is there would write = %+v, %v", preview.Answer, err)
	}
	copied := named(t, "copy")
	t.Cleanup(func() { _, _ = world.Docker(ctx, testenv.Alpha, "volume", "rm", "-f", copied, copied+"-2") })
	where := []dockhand.PostBackupRestorePreviewRequestVolumeDestinations{{Volume: volume, Target: copied}}
	if away, err := c.PostBackupRestorePreview(t.Context(), dockhand.PostBackupRestorePreviewRequest{DestinationId: destination, SnapshotId: early, IncludeTargets: new(true), Mode: "new-location", TargetType: "container", EnvironmentId: env, VolumeDestinations: where}); err != nil || away.Answer.Targets == nil || len(away.Answer.Targets.Volumes) != 1 || away.Answer.Targets.Volumes[0].Target != copied || away.Answer.Targets.Volumes[0].HasData != "missing" || len(away.Answer.Targets.Unresolved) != 0 {
		t.Errorf("what a restore to a volume of its own would write = %+v, %v", away.Answer, err)
	}
	if unknown, err := c.PostBackupRestorePreview(t.Context(), dockhand.PostBackupRestorePreviewRequest{DestinationId: destination, SnapshotId: "deadbeef"}); err != nil || !strings.Contains(unknown.Answer.Error, "snapshot not found") {
		t.Errorf("a restore of a snapshot there is none of, worked out = %+v, %v", unknown.Answer, err)
	}

	// restored: not over what is there unconfirmed, nor to nowhere; to a volume of its own with nothing done after; and over what is there
	for what, request := range map[string]dockhand.PostBackupRestoreRequest{
		"requires explicit confirmation": {DestinationId: destination, SnapshotId: early, Mode: "in-place", TargetType: "container", EnvironmentId: env},
		"a target path is required":      {DestinationId: destination, SnapshotId: early, Mode: "new-location", TargetType: "container", EnvironmentId: env},
		"must be kind":                   {DestinationId: destination, SnapshotId: early, Mode: "new-location", TargetType: "container", EnvironmentId: env, VolumeDestinations: []dockhand.PostBackupRestoreRequestVolumeDestinations{{Volume: volume, Target: copied}}},
	} {
		if _, err := c.PostBackupRestore(t.Context(), request); client.StatusCode(err) != http.StatusBadRequest || !strings.Contains(err.Error(), "Invalid restore request") {
			t.Errorf("a restore that %s = %v, want a 400", what, err)
		}
	}
	containers := func() int {
		out, unlisted := world.Docker(ctx, testenv.Alpha, "ps", "-aq")
		if unlisted != nil {
			t.Fatal(unlisted)
		}

		return len(strings.Fields(out))
	}
	before := containers()
	to := []dockhand.PostBackupRestoreRequestVolumeDestinations{{Volume: volume, Kind: "volume", Target: copied}}
	put, err := c.PostBackupRestore(t.Context(), dockhand.PostBackupRestoreRequest{DestinationId: destination, SnapshotId: early, Mode: "new-location", TargetType: "container", EnvironmentId: env, VolumeDestinations: to, PostRestore: "none"})
	if err != nil || put.Answer.Status != "success" || !slices.Equal(put.Answer.RestoredVolumes, []string{volume}) || put.Answer.PostRestore == nil || put.Answer.PostRestore.Action != "none" || containers() != before {
		t.Fatalf("a restore to a volume of its own = %+v, %v, leaving %d containers where there were %d", put.Answer, err, containers(), before)
	}
	if out, err := world.Docker(ctx, testenv.Alpha, "run", "--rm", "-v", copied+":/held", world.Image, "cat", "/held/notes.txt"); err != nil || strings.TrimSpace(out) != "first" {
		t.Errorf("the volume the snapshot was restored to holds %q, %v", out, err)
	}
	// a volume that is there is not restored into
	if taken, err := c.PostBackupRestore(t.Context(), dockhand.PostBackupRestoreRequest{DestinationId: destination, SnapshotId: early, Mode: "new-location", TargetType: "container", EnvironmentId: env, VolumeDestinations: to, PostRestore: "none"}); err != nil || taken.Answer.Status != "error" || taken.Answer.Code != "VALIDATION" || !strings.Contains(taken.Answer.Error, "already exists") {
		t.Errorf("a restore to a volume that is there = %+v, %v", taken.Answer, err)
	}
	// left to itself, a restore to somewhere else makes the container again: a second one, on the volumes the first has
	to[0].Target = copied + "-2"
	again, err := c.PostBackupRestore(t.Context(), dockhand.PostBackupRestoreRequest{DestinationId: destination, SnapshotId: early, Mode: "new-location", TargetType: "container", EnvironmentId: env, VolumeDestinations: to})
	if err != nil || again.Answer.PostRestore == nil || again.Answer.PostRestore.Action != "recreate" || containers() != before+1 {
		t.Errorf("a restore to somewhere else with nothing said of what follows = %+v, %v, leaving %d containers where there were %d", again.Answer, err, containers(), before)
	}
	if made, err := world.Docker(ctx, testenv.Alpha, "ps", "-a", "--filter", "volume="+volume, "--format", "{{.Names}}"); err == nil {
		for name := range strings.FieldsSeq(made) {
			if name != container {
				_, _ = world.Docker(ctx, testenv.Alpha, "rm", "-f", name)
			}
		}
	}
	// as a job, over what is there, with the container started again: the volume is as the snapshot has it, and what was made since is gone
	over, err := c.PostBackupRestore(t.Context(), dockhand.PostBackupRestoreRequest{DestinationId: destination, SnapshotId: early, Mode: "in-place", TargetType: "container", TargetName: container, EnvironmentId: env, ConfirmOverwrite: new(true), PostRestore: "start"}, dockhand.PostBackupRestoreOptions{Accept: dockhand.AcceptJob})
	if err != nil || over.Answer.JobId == "" {
		t.Fatalf("a restore asked for as a job = %+v, %v", over.Answer, err)
	}
	if done := ended(t, over.Answer.JobId); done.Status != "done" || done.Result["status"] != "success" {
		t.Fatalf("the job of a restore over what is there = %+v", done)
	}
	if out, err := world.Docker(ctx, testenv.Alpha, "exec", container, "sh", "-c", "cat /data/notes.txt; ls /data"); err != nil || !strings.HasPrefix(out, "first") || strings.Contains(out, "added.txt") {
		t.Errorf("after a restore over it the volume holds %q, %v", out, err)
	}
	// a restore that cannot be made fails as an answer
	if failed, err := c.PostBackupRestore(t.Context(), dockhand.PostBackupRestoreRequest{DestinationId: destination, SnapshotId: "deadbeef", Mode: "new-location", TargetPath: "/it-nowhere", EnvironmentId: env}); err != nil || failed.Answer.Status != "error" || failed.Answer.Code != "VALIDATION" {
		t.Errorf("a restore of a snapshot there is none of = %+v, %v", failed.Answer, err)
	}
	if stopped, err := c.PostBackupRestoreStop(t.Context(), dockhand.PostBackupRestoreStopRequest{}); err != nil || !stopped.Answer.Success || stopped.Answer.Stopped {
		t.Errorf("stopping a restore when none runs = %+v, %v", stopped.Answer, err)
	}

	// removed: one by the start of its id, several at once with one that is not there passed over, and the rest with their configuration
	third, err := c.PostBackupConfigsByIdRun(t.Context(), config)
	if err != nil || third.Answer.Status != "success" {
		t.Fatalf("a third run = %+v, %v", third.Answer, err)
	}
	if gone, err := c.DeleteBackupSnapshotsById(t.Context(), early[:8], dockhand.DeleteBackupSnapshotsByIdOptions{DestinationId: destination}); err != nil || !gone.Answer.Success {
		t.Errorf("a snapshot removed = %+v, %v", gone.Answer, err)
	}
	if _, err := c.DeleteBackupSnapshotsById(t.Context(), "deadbeef", dockhand.DeleteBackupSnapshotsByIdOptions{DestinationId: destination}); !client.IsNotFound(err) {
		t.Errorf("a snapshot there is none of, removed = %v", err)
	}
	several, err := c.PostBackupSnapshotsBatchDelete(t.Context(), dockhand.PostBackupSnapshotsBatchDeleteRequest{DestinationId: destination, SnapshotIds: []string{later, "deadbeef"}})
	if err != nil || !slices.Equal(several.Answer.Deleted, []string{later}) || !slices.Equal(several.Answer.Skipped, []string{"deadbeef"}) {
		t.Errorf("two snapshots removed, one of them not there = %+v, %v", several.Answer, err)
	}
	if _, err := c.PostBackupSnapshotsBatchDelete(t.Context(), dockhand.PostBackupSnapshotsBatchDeleteRequest{DestinationId: destination, SnapshotIds: []string{}}); client.StatusCode(err) != http.StatusBadRequest {
		t.Errorf("no snapshots removed = %v, want a 400", err)
	}
	gone, err := c.DeleteBackupConfigsById(t.Context(), config, dockhand.DeleteBackupConfigsByIdOptions{DeleteSnapshots: new(true)})
	if err != nil || !gone.Answer.Success || gone.Answer.Snapshots == nil || gone.Answer.Snapshots.Deleted != 1 {
		t.Errorf("the configuration removed with its snapshots = %+v, %v", gone.Answer, err)
	}
	if left, err := c.GetBackupSnapshots(t.Context(), dockhand.GetBackupSnapshotsOptions{DestinationId: destination}); err != nil || len(left.Answer) != 0 {
		t.Errorf("the snapshots left in the destination = %+v, %v", left.Answer, err)
	}
}

// Where a stack's files are on its host is found for a stack Dockhand made, on the host it shares the folder with and behind the agent, and is said
// to be unknown, with why, for one there is none of.
func TestBackupStackFolderFound(t *testing.T) {
	c := dock(t)
	ctx := noCancel(t)
	stack := named(t, "stack")
	for _, host := range []string{testenv.Alpha, testenv.Beta} {
		env := world.Env(host)
		_, _ = c.DeleteStacksByName(ctx, stack, dockhand.DeleteStacksByNameOptions{Env: env, Force: new(true)})
		if _, err := c.PostStacks(t.Context(), dockhand.PostStacksRequest{Name: stack, Compose: "services:\n  app:\n    image: " + world.Image + "\n    command: sleep 100000\n    stop_grace_period: 1s\n", Start: new(true)}, dockhand.PostStacksOptions{Env: env}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = c.DeleteStacksByName(ctx, stack, dockhand.DeleteStacksByNameOptions{Env: env, Force: new(true)})
		})

		at, err := c.GetBackupStackPath(t.Context(), dockhand.GetBackupStackPathOptions{Target: stack, Env: env})
		if err != nil || at.Answer.Kind != "candidate" || !strings.HasSuffix(at.Answer.HostPath, "/"+stack) || at.Answer.ComposeFile == "" {
			t.Errorf("where the stack's folder is on %s = %+v, %v", host, at.Answer, err)
		}
		listed, err := c.GetBackupStackDirListing(t.Context(), dockhand.GetBackupStackDirListingOptions{Target: stack, Env: env})
		if err != nil || listed.Answer.Kind != "listed" || len(listed.Answer.Entries) == 0 || listed.Answer.Entries[0].Name == "" {
			t.Errorf("what the stack's folder holds on %s = %+v, %v", host, listed.Answer, err)
		}
	}

	if nothing, err := c.GetBackupStackDirListing(t.Context(), dockhand.GetBackupStackDirListingOptions{Target: "it-no-such-stack", Env: world.Env(testenv.Alpha)}); err != nil || nothing.Answer.Kind != "unknown" || nothing.Answer.Reason == "" {
		t.Errorf("the folder of a stack there is none of = %+v, %v", nothing.Answer, err)
	}
	if _, err := c.GetBackupStackPath(t.Context(), dockhand.GetBackupStackPathOptions{}); client.StatusCode(err) != http.StatusBadRequest {
		t.Errorf("the folder of no stack named = %v, want a 400", err)
	}
}
