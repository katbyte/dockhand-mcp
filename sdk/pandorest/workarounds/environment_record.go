package workarounds

import (
	"errors"
	"fmt"
	"maps"
	"net/http"

	pandorest "github.com/katbyte/pandorest/importer/workarounds"
	"github.com/katbyte/pandorest/openapi"
)

// environmentRecord gives an environment the fields the server sends for one, wherever one is answered.
type environmentRecord struct{}

func (environmentRecord) Name() string    { return Service + "-environment-record" }
func (environmentRecord) Service() string { return Service }

func (environmentRecord) Bug() string {
	return "an environment is declared with between three and eleven fields, differently by each call that answers one, and a 1.0.51 server sends the same record from all of them: twenty-five fields from the list, with how it is reached, what is collected from it and when it was made; labels are declared a string and are a list of them, in what is sent and in what is answered, except by the call that makes an environment, which answers the text of the list; and a connection test that fails says why in error, which neither test call declares"
}

func (environmentRecord) Apply(spec *openapi.Spec) error {
	list, err := answerOf(spec, "/api/environments")
	if err != nil {
		return err
	}
	if list.Items == nil || list.Items.Properties == nil {
		return errors.New("GET /api/environments no longer declares a list of records")
	}
	if list.Items.Properties["socketPath"] != nil {
		return errors.New("GET /api/environments now declares the record's socketPath, so the record may be whole")
	}
	// what only the list adds to the record: settings kept beside it
	list.Items.Properties = withFields(environmentFields(), map[string]*openapi.Schema{
		"timezone":              {Type: openapi.TypeString},
		"updateCheckEnabled":    {Type: openapi.TypeBoolean},
		"updateCheckAutoUpdate": {Type: openapi.TypeBoolean},
		"imagePruneEnabled":     {Type: openapi.TypeBoolean},
	})

	for _, call := range []struct{ method, path string }{{http.MethodGet, "/api/environments/{id}"}, {http.MethodPost, "/api/environments"}, {http.MethodPut, "/api/environments/{id}"}} {
		op, err := pandorest.Operation(spec, call.method, call.path)
		if err != nil {
			return err
		}
		media, err := pandorest.JSONResponse(op, call.method+" "+call.path)
		if err != nil {
			return err
		}
		if media.Schema == nil {
			return fmt.Errorf("%s %s no longer declares the shape of its answer", call.method, call.path)
		}
		media.Schema.Properties = environmentFields()
		media.Schema.Required = nil
		// the one place the labels are not a list: making an environment answers them as the text of one
		if call.method == http.MethodPost {
			media.Schema.Properties["labels"] = &openapi.Schema{Type: openapi.TypeString, Description: "the labels given, as the text of a JSON list: [\"a\",\"b\"]; every other call answers the list itself"}
		}
	}

	for _, call := range []struct{ method, path string }{{http.MethodPost, "/api/environments"}, {http.MethodPut, "/api/environments/{id}"}} {
		body, err := jsonBody(spec, call.method, call.path)
		if err != nil {
			return err
		}
		if body.Properties["labels"] == nil || body.Properties["labels"].Type != openapi.TypeString {
			return fmt.Errorf("%s %s no longer declares labels a string", call.method, call.path)
		}
		body.Properties["labels"] = &openapi.Schema{Type: openapi.TypeArray, Items: &openapi.Schema{Type: openapi.TypeString}}
	}

	for _, path := range []string{"/api/environments/test", "/api/environments/{id}/test"} {
		op, err := pandorest.Operation(spec, http.MethodPost, path)
		if err != nil {
			return err
		}
		media, err := pandorest.JSONResponse(op, "POST "+path)
		if err != nil {
			return err
		}
		if media.Schema == nil || media.Schema.Properties == nil {
			return fmt.Errorf("POST %s no longer declares the shape of its answer", path)
		}
		if media.Schema.Properties["error"] != nil {
			return fmt.Errorf("POST %s now declares the error a failed test gives", path)
		}
		media.Schema.Properties["error"] = &openapi.Schema{Type: openapi.TypeString, Description: "why the host could not be reached, when success is false"}
	}

	return nil
}

// environmentFields is an environment as a 1.0.51 server sends one.
func environmentFields() map[string]*openapi.Schema {
	return map[string]*openapi.Schema{
		"id":               integer(),
		"name":             text(),
		"connectionType":   said(text(), "how the host is reached: socket, direct, hawser-standard or hawser-edge"),
		"socketPath":       said(text(), "the Docker socket, for a socket connection; sent for the others too, and means nothing there"),
		"host":             said(text(), "the host or its agent, for every connection but a socket"),
		"port":             integer(),
		"protocol":         text(),
		"tlsCa":            text(),
		"tlsCert":          text(),
		"tlsSkipVerify":    flag(),
		"hasTlsKey":        said(flag(), "a TLS key is kept for the host; the key itself is never sent"),
		"hasHawserToken":   said(flag(), "an agent's token is kept for the host; the token itself is never sent"),
		"icon":             text(),
		"labels":           {Type: openapi.TypeArray, Items: text()},
		"publicIp":         text(),
		"collectActivity":  flag(),
		"collectMetrics":   flag(),
		"highlightChanges": flag(),
		"hawserLastSeen":   said(text(), "when an agent that calls in was last heard from; empty for every other connection"),
		"hawserAgentId":    text(),
		"hawserAgentName":  text(),
		"hawserVersion":    text(),
		"createdAt":        text(),
		"updatedAt":        text(),
	}
}

// withFields is a set of fields with more added.
func withFields(fields, more map[string]*openapi.Schema) map[string]*openapi.Schema {
	maps.Copy(fields, more)

	return fields
}
