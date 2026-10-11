package workarounds

import (
	"errors"

	"github.com/katbyte/pandorest/openapi"
)

// scheduleRun is one run of something Dockhand does by itself, as a server sends it wherever it sends one.
func scheduleRun() *openapi.Schema {
	return record(map[string]*openapi.Schema{
		"id":            integer(),
		"scheduleType":  text(),
		"scheduleId":    said(integer(), "the schedule's number among those of its type; 0 for a run that belongs to no schedule, as a stack brought up by hand"),
		"environmentId": integer(),
		"entityName":    said(text(), "what the run was of: a container, an environment, a stack or a system job, by name"),
		"triggeredBy":   said(text(), "cron, manual or webhook"),
		"triggeredAt":   text(),
		"startedAt":     text(),
		"completedAt":   text(),
		"duration":      said(integer(), "how long it took, in milliseconds"),
		"status":        said(text(), "queued, running, success, warning, failed or skipped"),
		"errorMessage":  text(),
		"details":       said(&openapi.Schema{Type: openapi.TypeObject}, "what the run did, by its kind: what a prune removed, what a check found, why an update was skipped"),
		"logs":          said(text(), "what the run logged, where one run is read; null in a list of runs"),
		"createdAt":     text(),
	})
}

// scheduleAnswers gives the calls about what Dockhand does by itself the answers a server sends, where the document declares a part of a schedule and
// four fields of a run.
type scheduleAnswers struct{}

func (scheduleAnswers) Name() string    { return Service + "-schedule-answers" }
func (scheduleAnswers) Service() string { return Service }

func (scheduleAnswers) Bug() string {
	return "GET /api/schedules declares nine fields of a schedule, and a 1.0.51 server sends what the schedule does in words, its environment's name, its last run and its latest few as well; a run is declared with its number, type, status and start in GET /api/schedules/executions, and is sent with which schedule it was of, what set it off, how long it took, what it did and why it failed; and one run read by its number declares its log as log, where the server sends logs"
}

func (scheduleAnswers) Apply(spec *openapi.Spec) error {
	list, err := answerOf(spec, "/api/schedules")
	if err != nil {
		return err
	}
	if list.Properties["schedules"] == nil || list.Properties["schedules"].Items == nil || list.Properties["schedules"].Items.Properties == nil {
		return errors.New("GET /api/schedules no longer declares a list of schedules")
	}
	schedule := list.Properties["schedules"].Items
	if schedule.Properties["lastExecution"] != nil {
		return errors.New("GET /api/schedules now declares a schedule's lastExecution, so the record may be whole")
	}
	schedule.Properties["description"] = said(text(), "what the schedule does, in words; null for a container's update")
	schedule.Properties["environmentName"] = text()
	schedule.Properties["scheduleType"] = said(text(), "how its time was given: daily, weekly or custom")
	schedule.Properties["pruneMode"] = said(text(), "for an image prune: dangling or all")
	schedule.Properties["lastExecution"] = said(scheduleRun(), "its latest run, with what it logged; null for one that has never run")
	schedule.Properties["recentExecutions"] = said(listOf(scheduleRun()), "its latest few runs, the latest first")

	runs, err := answerOf(spec, "/api/schedules/executions")
	if err != nil {
		return err
	}
	if runs.Properties["executions"] == nil || runs.Properties["executions"].Items == nil || runs.Properties["executions"].Items.Properties["triggeredBy"] != nil {
		return errors.New("GET /api/schedules/executions no longer declares a list of runs with four fields each")
	}
	runs.Properties["executions"].Items = scheduleRun()

	one, err := answerOf(spec, "/api/schedules/executions/{id}")
	if err != nil {
		return err
	}
	if one.Properties["log"] == nil || one.Properties["logs"] != nil {
		return errors.New("GET /api/schedules/executions/{id} no longer declares a run's log as log")
	}
	*one = *scheduleRun()

	return nil
}
