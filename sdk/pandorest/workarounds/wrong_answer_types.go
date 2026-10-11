package workarounds

import (
	"errors"
	"fmt"
	"net/http"

	pandorest "github.com/katbyte/pandorest/importer/workarounds"
	"github.com/katbyte/pandorest/openapi"
)

// wrongAnswerTypes puts right the fields an answer's example gave one type and the server sends as another, which the client could not read at all.
type wrongAnswerTypes struct{}

func (wrongAnswerTypes) Name() string    { return Service + "-wrong-answer-types" }
func (wrongAnswerTypes) Service() string { return Service }

func (wrongAnswerTypes) Bug() string {
	return "GET /api/changelog declares each release's changes a list of strings and answers a list of objects, a type and a text; GET /api/health/database declares tables a number and answers an object, how many are expected, how many were found and which are missing, and leaves out the connection and schemaVersion it also sends; GET /api/host declares uptime a whole number of seconds and answers a fraction"
}

func (wrongAnswerTypes) Apply(spec *openapi.Spec) error {
	release, err := answerOf(spec, "/api/changelog")
	if err != nil {
		return err
	}
	if release.Items == nil || release.Items.Properties["changes"] == nil || release.Items.Properties["changes"].Items == nil || release.Items.Properties["changes"].Items.Type != openapi.TypeString {
		return errors.New("GET /api/changelog no longer declares a release's changes a list of strings")
	}
	release.Items.Properties["changes"].Items = &openapi.Schema{Type: openapi.TypeObject, Properties: map[string]*openapi.Schema{
		"type": {Type: openapi.TypeString, Description: "what kind of change: feature or fix"},
		"text": {Type: openapi.TypeString},
	}}

	database, err := answerOf(spec, "/api/health/database")
	if err != nil {
		return err
	}
	if database.Properties["tables"] == nil || database.Properties["tables"].Type != openapi.TypeInteger {
		return errors.New("GET /api/health/database no longer declares tables a number")
	}
	database.Properties["tables"] = &openapi.Schema{Type: openapi.TypeObject, Properties: map[string]*openapi.Schema{
		"expected": {Type: openapi.TypeInteger},
		"found":    {Type: openapi.TypeInteger},
		"missing":  {Type: openapi.TypeArray, Items: &openapi.Schema{Type: openapi.TypeString}},
	}}
	host, err := answerOf(spec, "/api/host")
	if err != nil {
		return err
	}
	if host.Properties["uptime"] == nil || host.Properties["uptime"].Type != openapi.TypeInteger {
		return errors.New("GET /api/host no longer declares uptime a whole number")
	}
	host.Properties["uptime"] = &openapi.Schema{Type: openapi.TypeNumber, Format: "double", Description: "how long the host has been up, in seconds"}

	database.Properties["connection"] = &openapi.Schema{Type: openapi.TypeString, Description: "where the database is: a file's path for SQLite"}
	database.Properties["schemaVersion"] = &openapi.Schema{Type: openapi.TypeString, Description: "the newest migration applied"}

	return nil
}

// answerOf is the schema of what a GET answers as JSON, or the error a workaround returns when the document no longer declares one.
func answerOf(spec *openapi.Spec, path string) (*openapi.Schema, error) {
	return answerTo(spec, http.MethodGet, path)
}

// answerTo is answerOf for a call of any method.
func answerTo(spec *openapi.Spec, method, path string) (*openapi.Schema, error) {
	op, err := pandorest.Operation(spec, method, path)
	if err != nil {
		return nil, err
	}
	media, err := pandorest.JSONResponse(op, method+" "+path)
	if err != nil {
		return nil, err
	}
	if media.Schema == nil {
		return nil, fmt.Errorf("%s %s no longer declares the shape of its answer", method, path)
	}

	return media.Schema, nil
}
