package workarounds

import (
	"errors"
	"net/http"

	"github.com/katbyte/pandorest/openapi"
)

// iconAnswers gives the calls about icons the answers a server sends, where the document declares none or an object of nothing.
type iconAnswers struct{}

func (iconAnswers) Name() string    { return Service + "-icon-answers" }
func (iconAnswers) Service() string { return Service }

func (iconAnswers) Bug() string {
	return "GET /api/container-icons declares no answer, and a 1.0.51 server answers the icon each container of the environment was given, by the container's name; GET /api/icons/selfhst-manifest declares none either, and answers the list of the icons selfh.st has, each with its name, the reference it is asked for by, the forms it comes in, its category and its tags; POST /api/icons/selfhst/batch declares the icons it answers as an object of nothing, and they are pictures written as data: addresses, by reference"
}

func (iconAnswers) Apply(spec *openapi.Spec) error {
	given, err := unanswered(spec, http.MethodGet, "/api/container-icons")
	if err != nil {
		return err
	}
	answering(given, said(byName(text()), "the icon each container that was given one has, by the container's name: a name of Dockhand's own icons, selfhst: and a reference for one of selfh.st's, or custom:container for a picture that was sent"))

	manifest, err := unanswered(spec, http.MethodGet, "/api/icons/selfhst-manifest")
	if err != nil {
		return err
	}
	answering(manifest, listOf(record(map[string]*openapi.Schema{
		"Name":      text(),
		"Reference": said(text(), "what the icon is asked for by"),
		"SVG":       said(text(), "Yes when it comes as SVG, and No"),
		"PNG":       text(),
		"WebP":      text(),
		"Light":     said(text(), "Yes when it has a form for a light background"),
		"Dark":      text(),
		"Category":  text(),
		"Tags":      said(text(), "its tags, with commas between"),
		"CreatedAt": text(),
	})))

	batch, err := answerTo(spec, http.MethodPost, "/api/icons/selfhst/batch")
	if err != nil {
		return err
	}
	icons := batch.Properties["icons"]
	if icons == nil || icons.Type != openapi.TypeObject || len(icons.Properties) != 0 || icons.AdditionalProperties != nil {
		return errors.New("POST /api/icons/selfhst/batch no longer declares its icons as an object of nothing")
	}
	batch.Properties["icons"] = said(byName(text()), "each icon that could be had as a data: address, by its reference; one that could not is left out")

	return nil
}
