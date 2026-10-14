package workarounds

import (
	"fmt"
	"maps"
	"net/http"
	"slices"

	pandorest "github.com/katbyte/pandorest/importer/workarounds"
	"github.com/katbyte/pandorest/openapi"
)

// The kinds of answer a call gives. Anything but JSON is left unread for the caller: a file to save, or events to read as they come.
const (
	answersJSON   = "application/json"
	answersEvents = "text/event-stream"
	answersTar    = "application/x-tar"
	answersImage  = "image/*"
	answersText   = "text/plain"
	answersPage   = "text/html"
	// answersFile is a download whose format the request picks: an export as JSON, CSV or a report
	answersFile = "application/octet-stream"
)

// undeclared is every GET the document declares a 200 for and says nothing of what it holds, with what the server answers there, but for the ones a
// fix of their own gives a shape as well (the stack list, a stack's compose file, where each stack's files are kept, the git stacks, and the backups).
// api-defs/README.md says which of these were seen on a real server and which could not be.
var undeclared = map[string]string{
	// lists and records, answered as JSON
	"/api/docs":                    answersJSON,
	"/api/containers/{id}":         answersJSON,
	"/api/containers/{id}/inspect": answersJSON,
	"/api/profile/preferences":     answersJSON,
	"/api/settings/theme":          answersJSON,

	// what is happening, sent as it happens until the caller hangs up
	"/api/activity/events":             answersEvents,
	"/api/audit/events":                answersEvents,
	"/api/containers/stats/stream":     answersEvents,
	"/api/containers/{id}/logs/stream": answersEvents,
	"/api/dashboard/stats/stream":      answersEvents,
	"/api/events":                      answersEvents,
	"/api/logs/merged":                 answersEvents,
	"/api/schedules/stream":            answersEvents,

	// archives
	"/api/containers/{id}/files/download": answersTar,
	"/api/images/{id}/export":             answersTar,
	"/api/volumes/{name}/export":          answersTar,

	// pictures
	"/api/container-icons/{name}": answersImage,
	"/api/environments/{id}/icon": answersImage,
	"/api/icons/selfhst/{ref}":    answersImage,
	"/api/stacks/{name}/icon":     answersImage,

	// downloads in the format asked for
	"/api/audit/export":               answersFile,
	"/api/backup/snapshots/{id}/dump": answersFile,
	"/api/images/scan/export":         answersFile,
	"/api/vulnerabilities/export":     answersFile,

	// text as it was written: what a deploy printed, and what a metrics scraper reads
	"/api/stacks/{name}/deploys/{runId}/log": answersText,
	"/metrics":                               answersText,
}

// undeclaredResponses says what the GETs that declare no answer do answer.
type undeclaredResponses struct{}

func (undeclaredResponses) Name() string    { return Service + "-undeclared-responses" }
func (undeclaredResponses) Service() string { return Service }

func (undeclaredResponses) Bug() string {
	return fmt.Sprintf("%d GETs declare a 200 and nothing of what it holds, because the handler has no example to draw one from; each answers JSON, a file or a stream of events", len(undeclared))
}

func (undeclaredResponses) Apply(spec *openapi.Spec) error {
	for _, path := range slices.Sorted(maps.Keys(undeclared)) {
		op, err := pandorest.Operation(spec, http.MethodGet, path)
		if err != nil {
			return err
		}

		ok := op.Responses["200"]
		if ok == nil {
			return fmt.Errorf("GET %s no longer declares a 200", path)
		}
		if len(ok.Content) > 0 {
			return fmt.Errorf("GET %s now declares what it answers", path)
		}

		ok.Content = map[string]*openapi.MediaType{undeclared[path]: {}}
	}

	return nil
}
