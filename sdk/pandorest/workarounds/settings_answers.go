package workarounds

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/katbyte/pandorest/openapi"
)

// copyOf is a schema over again, sharing nothing with the one it was made from, so that what is declared in one place can be declared in another.
func copyOf(s *openapi.Schema) *openapi.Schema {
	raw, err := json.Marshal(s)
	if err != nil {
		panic(err) // a schema read from a document always marshals
	}
	var again openapi.Schema
	if err := json.Unmarshal(raw, &again); err != nil {
		panic(err) // and what was just marshalled always reads back
	}

	return &again
}

// settingsAnswers gives the general settings and the licence an answer, where the document declares none, and the paths stacks are looked for under
// the list they are taken as.
type settingsAnswers struct{}

func (settingsAnswers) Name() string    { return Service + "-settings-answers" }
func (settingsAnswers) Service() string { return Service }

func (settingsAnswers) Bug() string {
	return "GET and POST /api/settings/general declare no answer, and a 1.0.51 server answers both with every general setting, by the same names the request to save them declares; that request declares externalStackPaths a text, and a server passes a text over without a word and takes a list of paths, which is what it answers; GET /api/license declares no answer either, and a server with no licence answers whether one is active, whether it is valid and the host name a licence is bound to"
}

func (settingsAnswers) Apply(spec *openapi.Spec) error {
	saved, err := jsonBody(spec, http.MethodPost, "/api/settings/general")
	if err != nil {
		return err
	}
	if paths := saved.Properties["externalStackPaths"]; paths == nil || paths.Type != openapi.TypeString {
		return errors.New("POST /api/settings/general no longer declares externalStackPaths a text")
	}
	saved.Properties["externalStackPaths"] = said(listOf(text()), "paths, as the list the server takes and answers")

	// what is saved and what is answered go by the same names, so the request's own fields are the answer's
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		answered, failed := unanswered(spec, method, "/api/settings/general")
		if failed != nil {
			return failed
		}
		answering(answered, copyOf(saved))
	}

	licence, err := unanswered(spec, http.MethodGet, "/api/license")
	if err != nil {
		return err
	}
	answering(licence, record(map[string]*openapi.Schema{
		"active":   said(flag(), "whether a licence is in use"),
		"valid":    flag(),
		"hostname": said(text(), "the host name a licence is bound to, which is this server's own"),
	}))

	return nil
}
