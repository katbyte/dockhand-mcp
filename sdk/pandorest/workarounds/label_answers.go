package workarounds

import (
	"errors"

	"github.com/katbyte/pandorest/openapi"
)

// labelAnswers gives the colours of the labels of environments the shape a server sends them in, where the document declares an object of nothing.
type labelAnswers struct{}

func (labelAnswers) Name() string    { return Service + "-label-answers" }
func (labelAnswers) Service() string { return Service }

func (labelAnswers) Bug() string {
	return "GET /api/labels declares the colours it answers as an object with no fields, and a 1.0.51 server answers the colour of each label that has one, as #rrggbb, by the label's name: a label no environment has any more is there too, with the colour it kept"
}

func (labelAnswers) Apply(spec *openapi.Spec) error {
	labels, err := answerOf(spec, "/api/labels")
	if err != nil {
		return err
	}
	colours := labels.Properties["colors"]
	if colours == nil || colours.Type != openapi.TypeObject || len(colours.Properties) != 0 || colours.AdditionalProperties != nil {
		return errors.New("GET /api/labels no longer declares its colours as an object of nothing")
	}
	labels.Properties["colors"] = said(byName(text()), "the colour of each label that has one, by the label's name")

	return nil
}
