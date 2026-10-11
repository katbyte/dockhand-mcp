package workarounds

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/katbyte/pandorest/openapi"
)

// newerVersion is what Dockhand says of a newer version tag of a container's image, where it says anything.
func newerVersion() *openapi.Schema {
	return said(record(map[string]*openapi.Schema{
		"tag":     said(text(), "the newest version tag the registry has for the image"),
		"bump":    said(text(), "how far a step it is from the tag in use: patch, minor or major"),
		"skipped": said(listOf(text()), "the version tags between the one in use and the newest, the newest among them"),
		"digest":  text(),
	}), "a newer version tag of the image, when Dockhand is set to look for them and found one")
}

// updateAnswers gives the calls about newer images, and about updating a container on a schedule, the answers a server sends where the document
// declares a part of them or an object with nothing in it.
type updateAnswers struct{}

func (updateAnswers) Name() string    { return Service + "-update-answers" }
func (updateAnswers) Service() string { return Service }

func (updateAnswers) Bug() string {
	return "a pending update declares its newerVersion an object with nothing in it, and a 1.0.51 server told to look for newer version tags answers the tag, how far a step it is, the tags passed over and the image's digest; GET /api/auto-update declares an object with nothing in it and answers a record by container name of each one's schedule; and reading or saving one container's schedule answers the stored record, with its number, its environment, the container's name and when it was made, changed, last checked and last updated, where the document declares the schedule alone"
}

func (updateAnswers) Apply(spec *openapi.Spec) error {
	pending, err := answerOf(spec, "/api/containers/pending-updates")
	if err != nil {
		return err
	}
	if pending.Properties["pendingUpdates"] == nil || pending.Properties["pendingUpdates"].Items == nil || pending.Properties["pendingUpdates"].Items.Properties["newerVersion"] == nil {
		return errors.New("GET /api/containers/pending-updates no longer declares a pending update's newerVersion")
	}
	if len(pending.Properties["pendingUpdates"].Items.Properties["newerVersion"].Properties) > 0 {
		return errors.New("GET /api/containers/pending-updates now declares what a newerVersion holds")
	}
	pending.Properties["pendingUpdates"].Items.Properties["newerVersion"] = newerVersion()

	schedule := func() map[string]*openapi.Schema {
		return map[string]*openapi.Schema{
			"enabled":               flag(),
			"scheduleType":          said(text(), "what Dockhand makes of the cron expression: daily, weekly or custom"),
			"cronExpression":        said(text(), "when the container is checked and updated, as five cron fields; nothing when it was saved without one, and then it never runs"),
			"vulnerabilityCriteria": said(text(), "what a scan of the new image may find before the update is held back: never holds none back"),
		}
	}

	all, err := answerOf(spec, "/api/auto-update")
	if err != nil {
		return err
	}
	if len(all.Properties) > 0 || len(all.AdditionalProperties) > 0 {
		return errors.New("GET /api/auto-update now declares what it answers")
	}
	*all = *said(byName(record(schedule())), "the schedule of each container of the environment that is updated on one, by the container's name")

	stored := map[string]*openapi.Schema{
		"id":            integer(),
		"environmentId": integer(),
		"containerName": text(),
		"lastChecked":   text(),
		"lastUpdated":   text(),
		"createdAt":     text(),
		"updatedAt":     text(),
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		one, failed := answerTo(spec, method, "/api/auto-update/{containerName}")
		if failed != nil {
			return failed
		}
		if one.Properties["enabled"] == nil || one.Properties["containerName"] != nil {
			return fmt.Errorf("%s /api/auto-update/{containerName} no longer declares a schedule, or now declares the stored record", method)
		}
		for name, field := range stored {
			if one.Properties[name] == nil {
				one.Properties[name] = field
			}
		}
		// a schedule that was turned off answers that it was deleted and no record, so nothing of the record is always there
		one.Required = nil
	}

	return nil
}

// containerUpdateFields gives making a container again the fields the document declares only for making one.
type containerUpdateFields struct{}

func (containerUpdateFields) Name() string    { return Service + "-container-update-fields" }
func (containerUpdateFields) Service() string { return Service }

func (containerUpdateFields) Bug() string {
	return "POST /api/containers/{id}/update declares an image, a name and two flags, and says in words that its body carries the options a container is made with: a 1.0.51 server takes each field POST /api/containers does, makes the container again with those it is given in place of what the container had, and keeps what it is not given"
}

func (containerUpdateFields) Apply(spec *openapi.Spec) error {
	create, err := jsonBody(spec, http.MethodPost, "/api/containers")
	if err != nil {
		return err
	}
	update, err := jsonBody(spec, http.MethodPost, "/api/containers/{id}/update")
	if err != nil {
		return err
	}

	added := 0
	for name, property := range create.Properties {
		// whether the new container is started is the update's own flag, startAfterUpdate
		if _, declared := update.Properties[name]; declared || name == "startAfterCreate" {
			continue
		}
		update.Properties[name] = property
		added++
	}
	if added == 0 {
		return errors.New("POST /api/containers/{id}/update now declares every field making a container does")
	}

	return nil
}
