// Package workarounds is what is dockhand-mcp's own about generating its client: the one service pandorest generates here, and a named fix for each
// thing Dockhand's API document gets wrong. The generator itself is github.com/katbyte/pandorest.
//
// Dockhand writes its document from its route handlers rather than by hand: the paths, methods, parameters and request bodies are read out of the
// code, and are right. What a call answers is taken from an example where the handler has one, so it is missing for a quarter of the calls and a part
// of the truth for many of the rest. Each fix here was made by asking a real server, and api-defs/README.md says how.
package workarounds

import (
	"time"

	"github.com/katbyte/pandorest/config"
	pandorest "github.com/katbyte/pandorest/importer/workarounds"
)

// Service is the name the document, its definitions and every workaround carry.
const Service = "dockhand"

// Services is the one API generated here.
var Services = []config.Service{{
	Name:    Service,
	Package: "dockhand",
	Output:  "sdk/dockhand",
	// the document's operationIds are made from the method and the path by machine (get_api_stacks_name_compose), so the methods are named from the
	// two themselves: GetStacksByNameCompose
	Naming:        config.PathNaming,
	PathPrefix:    "/api",
	Auth:          "Bearer",
	ClientService: "service",
	// the document's required properties are read out of what each handler refuses a body without, and a body has to be able to send one empty:
	// saving a stack's .env file with nothing in it is how the file is removed, and a body with no content at all is refused
	SendRequired: true,
	IDs:          ids,
	Plain:        plain,
	// a date is sent as RFC 3339, and read in that and in the two other forms Dockhand writes one in; one in no form known is kept as its text and
	// read as no date, which the live sweep then fails on, so that a new form is found there and not by a tool failing to read an answer
	Dates: config.Dates{Send: time.RFC3339Nano, Read: []string{"2006-01-02 15:04:05", "2006-01-02 15:04:05-07:00"}, KeepUnread: true},
	Notes: "Nearly every call that is about a Docker host takes it as the query parameter env, the environment's number from GetEnvironments. Left out, most calls answer with nothing rather than an error, so a caller sets it.",
}}

// All is every workaround, in the order they are applied.
var All = []pandorest.Workaround{
	auditOutsideAPI{},
	undeclaredResponses{},
	environmentCreateFields{},
	environmentRecord{},
	wrongAnswerTypes{},
	containerList{},
	stackList{},
	dashboardStatsEnv{},
	stackActionAnswers{},
	vulnerabilityCountOptions{},
	oidcRedirects{},
	scanExportEnv{},
	imageAnswers{},
	volumeAnswers{},
	networkAnswers{},
	pruneAnswers{},
	stackAnswers{},
	containerAnswers{},
	updateAnswers{},
	containerUpdateFields{},
	activityAnswers{},
	diskAnswers{},
	scheduleAnswers{},
	registryAnswers{},
	notificationAnswers{},
	scanAnswers{},
	userAnswers{},
	ssoAnswers{},
	settingsAnswers{},
	templateAnswers{},
	tagAnswers{},
	labelAnswers{},
	iconAnswers{},
	preferenceAnswers{},
	secretProviderAnswers{},
	gitAnswers{},
	backupAnswers{},
	jobAccept{},
	unsaidFlags{},
}

// the dates and the value sets go last: see valueSets
func init() { All = append(append(All, dates...), valueSets...) }
