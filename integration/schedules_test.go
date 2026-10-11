//go:build integration

package integration

import (
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/go-kt/mcp/acctest"
	"github.com/katbyte/pandorest/client"
)

// scheduleOf is one thing Dockhand does by itself as it lists it, or nil when it lists none of that type and number.
func scheduleOf(t *testing.T, c *dockhand.Client, kind string, id int) *dockhand.GetSchedulesResponseSchedules {
	t.Helper()

	listed, err := c.GetSchedules(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if at := slices.IndexFunc(listed.Model.Schedules, func(s dockhand.GetSchedulesResponseSchedules) bool { return s.Type == kind && s.Id == id }); at >= 0 {
		return &listed.Model.Schedules[at]
	}

	return nil
}

// What Dockhand does by itself is listed, run before its time, switched off and on, and removed, and each run is kept with what it did and logged:
// for a container kept on the newest image, on both hosts, and for Dockhand's own jobs, which can be run, three of them switched off, and none
// removed.
func TestSchedulesListedRunSwitchedAndRemoved(t *testing.T) {
	c := dock(t)
	const kind = "container_update"
	for _, host := range []string{testenv.Alpha, testenv.Beta} {
		env := world.Env(host)
		name := started(t, host, host)
		at := dockhand.PostAutoUpdateByContainerNameOperationOptions{Env: &env}
		t.Cleanup(func() {
			_, _ = c.DeleteAutoUpdateByContainerName(noCancel(t), name, dockhand.DeleteAutoUpdateByContainerNameOperationOptions{Env: &env})
		})
		saved, err := c.PostAutoUpdateByContainerName(t.Context(), name, dockhand.PostAutoUpdateByContainerNameRequest{Enabled: new(true), CronExpression: "0 4 * * *", VulnerabilityCriteria: "never"}, at)
		if err != nil || saved.Model.Id == 0 {
			t.Fatalf("%s: saving a schedule = %+v, %v", host, saved.Model, err)
		}
		id := saved.Model.Id

		// listed by the number of its record, with when it next runs and no run yet
		listed := scheduleOf(t, c, kind, id)
		if listed == nil || listed.Name != "Update container: "+name || listed.EntityName != name || listed.EnvironmentId != env || listed.EnvironmentName != host || !yes(listed.Enabled) || listed.CronExpression != "0 4 * * *" || listed.NextRun == "" || yes(listed.IsSystem) || listed.LastExecution != nil || len(listed.RecentExecutions) != 0 || listed.ScheduleType != "daily" {
			t.Fatalf("%s: the schedule as listed = %+v", host, listed)
		}

		// run before its time: answered at once, and the run is kept when it ends, with what it found and logged
		if ran, err := c.PostSchedulesByTypeByIdRun(t.Context(), kind, id); err != nil || !yes(ran.Model.Success) || ran.Model.Message == "" {
			t.Fatalf("%s: running it now = %+v, %v", host, ran.Model, err)
		}
		of := dockhand.GetSchedulesExecutionsOperationOptions{ScheduleType: kind, ScheduleId: &id}
		var runs *dockhand.GetSchedulesExecutionsResponse
		if !acctest.Eventually(func() bool {
			answer, err := c.GetSchedulesExecutions(t.Context(), of)
			runs = answer.Model

			return err == nil && len(runs.Executions) == 1 && runs.Executions[0].Status != "running" && runs.Executions[0].Status != "queued"
		}) {
			t.Fatalf("%s: the run did not end: %+v", host, runs)
		}
		run := runs.Executions[0]
		if runs.Total != 1 || run.ScheduleType != kind || run.ScheduleId != id || run.EnvironmentId != env || run.EntityName != name || run.TriggeredBy != "manual" || run.TriggeredAt == "" || run.StartedAt == "" || run.CompletedAt == "" || run.Logs != "" || !slices.Contains([]string{"success", "skipped", "failed"}, run.Status) {
			t.Errorf("%s: the run as listed = %+v", host, run)
		}
		// one run read by its number has what it logged, which a list of runs leaves out
		one, err := c.GetSchedulesExecutionsById(t.Context(), run.Id)
		if err != nil || one.Model.Id != run.Id || one.Model.Status != run.Status || !strings.Contains(one.Model.Logs, "Checking container: "+name) {
			t.Errorf("%s: the run read by its number = %+v, %v", host, one.Model, err)
		}
		if again := scheduleOf(t, c, kind, id); again == nil || again.LastExecution == nil || again.LastExecution.Id != run.Id || len(again.RecentExecutions) != 1 {
			t.Errorf("%s: the schedule once run = %+v", host, again)
		}

		// the runs narrowed: by what set them off, by how they stand, by environment and by time
		for what, tc := range map[string]struct {
			by   dockhand.GetSchedulesExecutionsOperationOptions
			want int
		}{
			"set off by hand":          {dockhand.GetSchedulesExecutionsOperationOptions{ScheduleType: kind, ScheduleId: &id, TriggeredBy: "manual"}, 1},
			"set off by its schedule":  {dockhand.GetSchedulesExecutionsOperationOptions{ScheduleType: kind, ScheduleId: &id, TriggeredBy: "cron"}, 0},
			"standing as it does":      {dockhand.GetSchedulesExecutionsOperationOptions{ScheduleType: kind, ScheduleId: &id, Statuses: run.Status + ",queued"}, 1},
			"standing another way":     {dockhand.GetSchedulesExecutionsOperationOptions{ScheduleType: kind, ScheduleId: &id, Status: "queued"}, 0},
			"of its environment":       {dockhand.GetSchedulesExecutionsOperationOptions{ScheduleType: kind, ScheduleId: &id, EnvironmentId: &env}, 1},
			"before 2020":              {dockhand.GetSchedulesExecutionsOperationOptions{ScheduleType: kind, ScheduleId: &id, ToDate: "2020-01-01T00:00:00Z"}, 0},
			"a standing there is none": {dockhand.GetSchedulesExecutionsOperationOptions{ScheduleType: kind, ScheduleId: &id, Status: "nonsense"}, 0},
		} {
			if got, err := c.GetSchedulesExecutions(t.Context(), tc.by); err != nil || got.Model.Total != tc.want {
				t.Errorf("%s: the runs %s = %+v, %v, want %d", host, what, got.Model, err, tc.want)
			}
		}

		// switched off and on: there is one call, which turns it over and answers how it then stands
		for _, want := range []bool{false, true} {
			turned, err := c.PostSchedulesByTypeByIdToggle(t.Context(), kind, id)
			if err != nil || yes(turned.Model.Enabled) != want {
				t.Fatalf("%s: turned over = %+v, %v, want switched on: %v", host, turned.Model, err, want)
			}
			if now := scheduleOf(t, c, kind, id); now == nil || yes(now.Enabled) != want || (now.NextRun != "") != want {
				t.Errorf("%s: as listed once turned over = %+v", host, now)
			}
		}

		// the record of the run removed, once
		if gone, err := c.DeleteSchedulesExecutionsById(t.Context(), run.Id); err != nil || !yes(gone.Model.Success) {
			t.Errorf("%s: removing the run's record = %+v, %v", host, gone.Model, err)
		}
		if _, err := c.GetSchedulesExecutionsById(t.Context(), run.Id); !client.IsNotFound(err) {
			t.Errorf("%s: the run's record read once removed = %v", host, err)
		}
		if _, err := c.DeleteSchedulesExecutionsById(t.Context(), run.Id); !client.IsNotFound(err) {
			t.Errorf("%s: removing the run's record again = %v", host, err)
		}

		// the schedule removed: no longer listed, and a second go is answered as done all the same
		for range 2 {
			if gone, err := c.DeleteSchedulesByTypeById(t.Context(), kind, id); err != nil || !yes(gone.Model.Success) {
				t.Errorf("%s: removing the schedule = %+v, %v", host, gone.Model, err)
			}
		}
		if scheduleOf(t, c, kind, id) != nil {
			t.Errorf("%s: the schedule is still listed once removed", host)
		}
		if _, err := c.PostSchedulesByTypeByIdRun(t.Context(), kind, id); !client.IsNotFound(err) {
			t.Errorf("%s: running a schedule that was removed = %v", host, err)
		}
	}

	// Dockhand's own jobs: run now like any other, three of them switched off by a call of their own, and none removed
	const tidy, helpers, events = "system_cleanup", 3, 2
	if own := scheduleOf(t, c, tidy, helpers); own == nil || !yes(own.IsSystem) || !yes(own.Enabled) || own.Description == "" || own.EnvironmentId != 0 || own.CronExpression == "" {
		t.Fatalf("one of Dockhand's own jobs = %+v", own)
	}
	if ran, err := c.PostSchedulesByTypeByIdRun(t.Context(), tidy, helpers); err != nil || !yes(ran.Model.Success) {
		t.Errorf("running one of Dockhand's own jobs = %+v, %v", ran.Model, err)
	}
	id := helpers
	if !acctest.Eventually(func() bool {
		runs, err := c.GetSchedulesExecutions(t.Context(), dockhand.GetSchedulesExecutionsOperationOptions{ScheduleType: tidy, ScheduleId: &id, TriggeredBy: "manual"})

		return err == nil && len(runs.Model.Executions) > 0 && runs.Model.Executions[0].Status == "success" && runs.Model.Executions[0].EnvironmentId == 0
	}) {
		t.Errorf("the run of one of Dockhand's own jobs was not kept as a success")
	}
	if _, err := c.PostSchedulesByTypeByIdToggle(t.Context(), tidy, helpers); client.StatusCode(err) != 400 || !strings.Contains(err.Error(), "cannot be paused") {
		t.Errorf("turning one of Dockhand's own jobs over as any other = %v", err)
	}
	if _, err := c.PostSchedulesSystemByIdToggle(t.Context(), helpers); client.StatusCode(err) != 400 || !strings.Contains(err.Error(), "Unknown system schedule") {
		t.Errorf("turning over one that cannot be switched off = %v", err)
	}
	for _, want := range []bool{false, true} {
		if turned, err := c.PostSchedulesSystemByIdToggle(t.Context(), events); err != nil || yes(turned.Model.Enabled) != want {
			t.Fatalf("turning over one that can be = %+v, %v, want switched on: %v", turned.Model, err, want)
		}
		if now := scheduleOf(t, c, tidy, events); now == nil || yes(now.Enabled) != want {
			t.Errorf("as listed once turned over = %+v", now)
		}
	}
	if _, err := c.DeleteSchedulesByTypeById(t.Context(), tidy, helpers); client.StatusCode(err) != 400 || !strings.Contains(err.Error(), "cannot be removed") {
		t.Errorf("removing one of Dockhand's own jobs = %v", err)
	}
	if _, err := c.PostSchedulesByTypeByIdRun(t.Context(), "hourly", 1); client.StatusCode(err) != 400 || !strings.Contains(err.Error(), "Invalid schedule type") {
		t.Errorf("running a type there is none of = %v", err)
	}

	// whether the page that lists them hides Dockhand's own jobs is kept, and read back
	for _, hide := range []bool{true, false} {
		if set, err := c.PutSchedulesSettings(t.Context(), dockhand.PutSchedulesSettingsRequest{HideSystemJobs: new(hide)}); err != nil || !yes(set.Model.Success) {
			t.Fatalf("hiding Dockhand's own jobs: %v = %+v, %v", hide, set.Model, err)
		}
		if got, err := c.GetSchedulesSettings(t.Context()); err != nil || yes(got.Model.HideSystemJobs) != hide {
			t.Errorf("read back = %+v, %v, want %v", got.Model, err, hide)
		}
	}
}
