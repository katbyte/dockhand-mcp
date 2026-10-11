package workarounds

import (
	"errors"
	"net/http"

	"github.com/katbyte/pandorest/openapi"
)

// preferenceAnswers gives the calls about what the web interface remembers the answers a server sends, where the document declares an object of
// nothing or no answer at all.
type preferenceAnswers struct{}

func (preferenceAnswers) Name() string    { return Service + "-preference-answers" }
func (preferenceAnswers) Service() string { return Service }

func (preferenceAnswers) Bug() string {
	return "GET, POST and DELETE /api/preferences/grid declare the preferences they answer as an object with no fields, and a 1.0.51 server answers the columns saved for each grid, by the grid's id, each column with its id and whether it is shown; GET and POST /api/preferences/favorites declare a list of names and GET and POST /api/preferences/favorite-groups a list of groups, and each answers the one list a server keeps for the environment, names and groups together in order; PUT /api/settings/navigation declares no answer, and answers success with the pages as they now are for the scope that was changed, global or user"
}

func (preferenceAnswers) Apply(spec *openapi.Spec) error {
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		grids, err := answerTo(spec, method, "/api/preferences/grid")
		if err != nil {
			return err
		}
		saved := grids.Properties["preferences"]
		if saved == nil || saved.Type != openapi.TypeObject || len(saved.Properties) != 0 || saved.AdditionalProperties != nil {
			return errors.New(method + " /api/preferences/grid no longer declares its preferences as an object of nothing")
		}
		grids.Properties["preferences"] = said(byName(record(map[string]*openapi.Schema{
			"columns": listOf(record(map[string]*openapi.Schema{
				"id":      text(),
				"visible": flag(),
			})),
		})), "the columns saved for each grid, by the grid's id")
	}

	// the favourites of the log view and their groups are one list, which both calls answer whole: a name is a text in it and a group a record
	for path, field := range map[string]string{"/api/preferences/favorites": "favorites", "/api/preferences/favorite-groups": "groups"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			kept, err := answerTo(spec, method, path)
			if err != nil {
				return err
			}
			list := kept.Properties[field]
			if list == nil || list.Items == nil || (list.Items.Type != openapi.TypeString && list.Items.Properties["containers"] == nil) {
				return errors.New(method + " " + path + " no longer declares " + field + " as a list of names or of groups alone")
			}
			kept.Properties[field] = said(listOf(&openapi.Schema{}), "everything kept for the environment's log view, in order: a container's name as a text, and a group as a record of its name and its containers")
		}
	}

	saved, err := unanswered(spec, http.MethodPut, "/api/settings/navigation")
	if err != nil {
		return err
	}
	pages := func() *openapi.Schema {
		return record(map[string]*openapi.Schema{
			"landingPage":  said(text(), "the page the web interface opens on; null for none chosen"),
			"envClickPage": said(text(), "the page a click on an environment opens; null for none chosen"),
		})
	}
	answering(saved, record(map[string]*openapi.Schema{
		"success": flag(),
		"global":  said(pages(), "the pages for everyone who chose none of their own, when that is what was changed"),
		"user":    said(pages(), "the pages of the user who asked, when that is what was changed"),
	}))

	return nil
}
