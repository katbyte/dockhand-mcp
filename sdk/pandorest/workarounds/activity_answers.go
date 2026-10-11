package workarounds

import (
	"errors"

	"github.com/katbyte/pandorest/openapi"
)

// activityAnswers gives what the activity log answers the fields a server sends, where the document declares a few of an event's and none of the
// counts by action.
type activityAnswers struct{}

func (activityAnswers) Name() string    { return Service + "-activity-answers" }
func (activityAnswers) Service() string { return Service }

func (activityAnswers) Bug() string {
	return "GET /api/activity declares four fields of an event, its number, the container's name, the action and when, and a 1.0.51 server sends the environment's number and name, the container's id, its image and what Docker said of the event as well, the exit code of a death and the signal of a kill among it; GET /api/activity/stats declares its counts by action an object with nothing in it, and answers a number for each action"
}

func (activityAnswers) Apply(spec *openapi.Spec) error {
	log, err := answerOf(spec, "/api/activity")
	if err != nil {
		return err
	}
	if log.Properties["events"] == nil || log.Properties["events"].Items == nil || log.Properties["events"].Items.Properties == nil {
		return errors.New("GET /api/activity no longer declares a list of events")
	}
	event := log.Properties["events"].Items
	if event.Properties["containerId"] != nil {
		return errors.New("GET /api/activity now declares an event's containerId, so the record may be whole")
	}
	event.Properties["environmentId"] = integer()
	event.Properties["environmentName"] = text()
	event.Properties["environmentIcon"] = text()
	event.Properties["containerId"] = text()
	event.Properties["image"] = text()
	event.Properties["createdAt"] = text()
	event.Properties["actorAttributes"] = said(byName(text()), "what Docker said of the event: the container's labels, and by kind exitCode for a death, signal for a kill and oldName for a rename, each as text")
	event.Properties["action"] = said(text(), "what the container did, in Docker's word: create, start, die, kill, stop, restart, destroy, pause, unpause, rename, update, oom, or health_status: healthy and health_status: unhealthy")

	stats, err := answerOf(spec, "/api/activity/stats")
	if err != nil {
		return err
	}
	if stats.Properties["byAction"] == nil || len(stats.Properties["byAction"].Properties) > 0 || len(stats.Properties["byAction"].AdditionalProperties) > 0 {
		return errors.New("GET /api/activity/stats no longer declares its counts by action an object with nothing in it")
	}
	stats.Properties["byAction"] = said(byName(integer()), "how many events of each action the log holds")

	return nil
}
