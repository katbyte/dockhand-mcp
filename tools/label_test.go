package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// What a 1.0.51 server answers about the labels of its environments, with invented names: one on both hosts with a colour, one that differs from it
// only by case, one whose colour is given beside the list alone, one with no colour, and a colour kept for a label no environment has any more.
const (
	fourLabels   = `{"labels":[{"label":"prod","environments":[{"envId":1,"envName":"alpha"},{"envId":2,"envName":"beta"}],"count":2,"color":"#1f9d55"},{"label":"Prod","environments":[{"envId":1,"envName":"alpha"}],"count":1},{"label":"edge","environments":[{"envId":2,"envName":"beta"}],"count":1},{"label":"plain","environments":[{"envId":2,"envName":"beta"}],"count":1}],"colors":{"prod":"#1f9d55","edge":"#0000ff","old":"#aaaaaa"}}`
	labelledEnvs = `[{"id":1,"name":"alpha","connectionType":"socket","labels":["prod","Prod"]},{"id":2,"name":"beta","connectionType":"hawser-standard","labels":["prod","edge","plain"]}]`
	alphaSaved   = `{"id":1,"name":"alpha","connectionType":"socket","labels":["prod"]}`
	betaSaved    = `{"id":2,"name":"beta","connectionType":"hawser-standard","labels":["prod"]}`

	labelsCall = "GET /api/labels"
	labelsAct  = "POST /api/labels"
	envsCall   = "GET /api/environments"
	alphaSave  = "PUT /api/environments/1"
	betaSave   = "PUT /api/environments/2"
)

// withLabels is a Dockhand whose two environments have four labels between them.
func withLabels(t *testing.T) *dockhandtest.Server {
	t.Helper()

	dock := dockhandtest.New(t)
	dock.Answer(labelsCall, fourLabels)
	dock.Answer(envsCall, labelledEnvs)

	return dock
}

// oneLabel is what the labels call answers once a label is all there is: on the environments given, with a colour or none.
func oneLabel(label, colour string, envs ...string) string {
	on := make([]string, 0, len(envs))
	for i, env := range envs {
		on = append(on, `{"envId":`+string(rune('1'+i))+`,"envName":"`+env+`"}`)
	}
	row, colours := `{"label":"`+label+`","environments":[`+strings.Join(on, ",")+`],"count":1}`, `{}`
	if colour != "" {
		colours = `{"` + label + `":"` + colour + `"}`
	}
	if len(envs) == 0 {
		row = ""
	}

	return `{"labels":[` + row + `],"colors":` + colours + `}`
}

func TestLabelList(t *testing.T) {
	t.Parallel()

	dock := withLabels(t)
	suite := session(t, dock, Options{})

	// by name, the ones that differ by case side by side, and one no environment has listed for the colour kept for it
	out := suite.Call(t, "label_list", nil)
	rows := acctest.Rows(t, out["labels"], "labels")
	order := make([]string, 0, len(rows))
	for _, row := range rows {
		order = append(order, acctest.Str(row["label"]))
	}
	if strings.Join(order, " ") != "edge old plain Prod prod" || acctest.Num(t, out["total"], "total") != 5 || out["note"] != nil {
		t.Fatalf("the labels, by name = %v", out)
	}
	byName := rowsNamed(rows, "label")
	if prod := byName["prod"]; prod["color"] != "#1f9d55" || strings.Join(acctest.Strs(t, prod["environments"], "environments"), " ") != "alpha beta" {
		t.Errorf("a label on two hosts = %v", prod)
	}
	// a colour given beside the list alone is the label's all the same
	if byName["edge"]["color"] != "#0000ff" || byName["plain"]["color"] != nil || byName["old"]["color"] != "#aaaaaa" || len(acctest.Strs(t, byName["old"]["environments"], "environments")) != 0 {
		t.Errorf("the colours = %v", rows)
	}

	dock.Answer(labelsCall, `{"labels":[],"colors":{}}`)
	if out = suite.Call(t, "label_list", nil); acctest.Num(t, out["total"], "total") != 0 || !strings.Contains(acctest.Str(out["note"]), "no environment has a label") {
		t.Errorf("a Dockhand with no label = %v", out)
	}
	dock.Fail(labelsCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "label_list", nil); !strings.Contains(msg, "the labels could not be listed") {
		t.Errorf("labels the account may not see = %q", msg)
	}
}

func TestLabelEdit(t *testing.T) {
	t.Parallel()

	t.Run("refused before anything is sent", func(t *testing.T) {
		t.Parallel()

		dock := withLabels(t)
		suite := session(t, dock, everything)
		for what, tc := range map[string]struct {
			args map[string]any
			want string
		}{
			"no label":                   {map[string]any{"label": " ", "name": "x"}, "label is required"},
			"a label in another case":    {map[string]any{"label": "EDGE", "name": "x"}, `no label is "EDGE": labels differ by their case, and there is edge`},
			"a label there is none of":   {map[string]any{"label": "nope", "name": "x"}, `no label is "nope": the Dockhand has edge, old, plain, Prod, prod`},
			"one no environment has":     {map[string]any{"label": "old", "name": "x"}, "no environment has the label old: Dockhand keeps only the colour #aaaaaa for it"},
			"a colour that is not one":   {map[string]any{"label": "prod", "color": "red"}, `color is "red": a label's colour is # and six hexadecimal digits`},
			"a colour too short":         {map[string]any{"label": "prod", "color": "#abc"}, "six hexadecimal digits"},
			"another label's name":       {map[string]any{"label": "prod", "name": "edge"}, "edge is a label already, on beta: with merge the two are made one, which cannot be undone"},
			"nothing to change is given": {map[string]any{"label": "prod"}, "nothing to change was given"},
		} {
			if msg := suite.CallErr(t, "label_edit", tc.args); !strings.Contains(msg, tc.want) {
				t.Errorf("%s = %q, want %q", what, msg, tc.want)
			}
		}

		// its own name is never sent, which Dockhand would take it from every environment for; nor a colour it has in another case
		out := suite.Call(t, "label_edit", map[string]any{"label": "prod", "name": " prod ", "color": "#1F9D55"})
		if !strings.Contains(acctest.Str(out["note"]), "nothing to do") || len(acctest.Strs(t, out["changes"], "changes")) != 0 {
			t.Errorf("a label that is that way already = %v", out)
		}
		if out = suite.Call(t, "label_edit", map[string]any{"label": "plain", "color": ""}); !strings.Contains(acctest.Str(out["note"]), "nothing to do") {
			t.Errorf("no colour for a label that has none = %v", out)
		}

		// a dry run says what would be, the three ways two labels become one among them
		for want, args := range map[string]map[string]any{
			"would rename it from prod to live on 2 environments; would set its colour to #000000": {"label": "prod", "name": "live", "color": "#000000"},
			"would take its colour away": {"label": "prod", "color": " "},
			"would make it one with the label prod, on alpha, beta, whose colour #1f9d55 is kept":             {"label": "Prod", "name": "prod", "merge": true},
			"would make it one with the label plain, on beta, which takes its colour #1f9d55":                 {"label": "prod", "name": "plain", "merge": true},
			"would make it one with the label plain, on beta":                                                 {"label": "Prod", "name": "plain", "merge": true},
			"would make it one with the label edge, on beta, whose colour #0000ff is kept":                    {"label": "prod", "name": "edge", "merge": true, "color": "#0000FF"},
			"would make it one with the label plain, on beta; would set its colour to #010101":                {"label": "Prod", "name": "plain", "merge": true, "color": "#010101"},
			"would rename it from Prod to stage on 1 environment":                                             {"label": "Prod", "name": "stage"},
			"would make it one with the label prod, on alpha, beta, whose colour #1f9d55 is kept; would take": {"label": "Prod", "name": "prod", "merge": true, "color": ""},
		} {
			out = suite.Call(t, "label_edit", over(args, map[string]any{"dry_run": true}))
			if got := strings.Join(acctest.Strs(t, out["changes"], "changes"), "; "); !strings.HasPrefix(got, want) || !acctest.IsBool(out["dry_run"], true) {
				t.Errorf("a dry run of %v = %q, want %q", args, got, want)
			}
		}
		if n := len(dock.Requests(labelsAct)); n != 0 {
			t.Errorf("%d changes were sent for what was refused, was that way already or was a dry run", n)
		}

		dock.Fail(labelsCall, http.StatusForbidden, notAllowed)
		if msg := suite.CallErr(t, "label_edit", map[string]any{"label": "prod", "name": "x"}); !strings.Contains(msg, "the labels could not be listed") {
			t.Errorf("labels the account may not see = %q", msg)
		}
	})

	t.Run("renamed and recoloured", func(t *testing.T) {
		t.Parallel()

		dock := withLabels(t)
		suite := session(t, dock, everything)
		dock.Answer(labelsAct, `{"success":true,"affected":2}`)
		dock.Then(labelsAct, func(r dockhandtest.Request) {
			if strings.Contains(r.Body, "set-color") {
				dock.Answer(labelsCall, oneLabel("live", "#000000", "alpha", "beta"))
			}
		})

		// the name first, then the colour of the label under the name it has by then
		out := suite.Call(t, "label_edit", map[string]any{"label": "prod", "name": " live ", "color": " #000000 "})
		calls := dock.Requests(labelsAct)
		row := acctest.Object(t, out["label"], "label")
		if len(calls) != 2 || calls[0].Body != `{"action":"rename","newLabel":"live","oldLabel":"prod"}` || calls[1].Body != `{"action":"set-color","color":"#000000","label":"live"}` || row["label"] != "live" || row["color"] != "#000000" || out["note"] != nil || strings.Join(acctest.Strs(t, out["changes"], "changes"), "; ") != "renamed it from prod to live on 2 environments; set its colour to #000000" {
			t.Errorf("a label renamed and recoloured = %v, sent %v", out, calls)
		}
	})

	t.Run("its colour taken away, and two made one", func(t *testing.T) {
		t.Parallel()

		dock := withLabels(t)
		suite := session(t, dock, everything)
		dock.Answer(labelsAct, `{"success":true}`)
		dock.Then(labelsAct, func(dockhandtest.Request) { dock.Answer(labelsCall, oneLabel("prod", "", "alpha", "beta")) })

		// no colour is sent as none, which Dockhand takes for taking it away
		out := suite.Call(t, "label_edit", map[string]any{"label": "prod", "color": ""})
		if body := dock.Last(labelsAct).Body; body != `{"action":"set-color","label":"prod"}` || acctest.Object(t, out["label"], "label")["color"] != nil || out["note"] != nil || acctest.Strs(t, out["changes"], "changes")[0] != "took its colour away" {
			t.Errorf("a colour taken away = %v, sent %s", out, body)
		}

		dock.Answer(labelsCall, fourLabels)
		dock.Then(labelsAct, func(dockhandtest.Request) { dock.Answer(labelsCall, oneLabel("plain", "#1f9d55", "alpha", "beta")) })
		out = suite.Call(t, "label_edit", map[string]any{"label": "prod", "name": "plain", "merge": true})
		if body := dock.Last(labelsAct).Body; body != `{"action":"rename","newLabel":"plain","oldLabel":"prod"}` || acctest.Object(t, out["label"], "label")["label"] != "plain" || out["note"] != nil || acctest.Strs(t, out["changes"], "changes")[0] != "made it one with the label plain, on beta, which takes its colour #1f9d55" {
			t.Errorf("two labels made one = %v, sent %s", out, body)
		}
	})

	t.Run("not changed, or not as asked", func(t *testing.T) {
		t.Parallel()

		dock := withLabels(t)
		suite := session(t, dock, everything)

		// answered as done, and read back with another colour, with none, and not at all
		dock.Answer(labelsAct, `{"success":true}`)
		out := suite.Call(t, "label_edit", map[string]any{"label": "prod", "color": "#000000"})
		if !strings.Contains(acctest.Str(out["note"]), "the label has the colour #1f9d55, which is not what was asked") {
			t.Errorf("a colour Dockhand did not take = %v", out)
		}
		if out = suite.Call(t, "label_edit", map[string]any{"label": "plain", "color": "#000000"}); !strings.Contains(acctest.Str(out["note"]), "the label has no colour, which is not what was asked") {
			t.Errorf("a colour Dockhand did not give = %v", out)
		}
		if msg := suite.CallErr(t, "label_edit", map[string]any{"label": "prod", "name": "live"}); !strings.Contains(msg, "the change to the label prod was answered as done, and no environment has a label live now") {
			t.Errorf("a label gone once renamed = %q", msg)
		}
		dock.Then(labelsAct, func(dockhandtest.Request) { dock.Fail(labelsCall, http.StatusInternalServerError, "Failed") })
		if out = suite.Call(t, "label_edit", map[string]any{"label": "prod", "name": "live"}); !strings.Contains(acctest.Str(out["note"]), "it was changed, and could not be read back") || out["label"] != nil {
			t.Errorf("a change that could not be read back = %v", out)
		}
		dock.Then(labelsAct, nil)
		dock.Answer(labelsCall, fourLabels)

		// the name changed and the colour refused
		dock.Then(labelsAct, func(r dockhandtest.Request) {
			if strings.Contains(r.Body, "rename") {
				dock.Fail(labelsAct, http.StatusInternalServerError, "Failed to manage labels")
				dock.Then(labelsAct, nil)
			}
		})
		if msg := suite.CallErr(t, "label_edit", map[string]any{"label": "prod", "name": "live", "color": "#000000"}); !strings.Contains(msg, "the label prod was not renamed") {
			t.Errorf("a rename Dockhand refuses = %q", msg)
		}
		dock.Answer(labelsAct, `{"success":true,"affected":2}`)
		dock.Then(labelsAct, func(r dockhandtest.Request) {
			if strings.Contains(r.Body, "set-color") {
				dock.Fail(labelsAct, http.StatusInternalServerError, "Failed to manage labels")
			}
		})
		if msg := suite.CallErr(t, "label_edit", map[string]any{"label": "prod", "name": "live", "color": "#000000"}); !strings.Contains(msg, "the colour of the label live was not changed, and anything else asked for was done") {
			t.Errorf("a colour Dockhand refuses once the name is changed = %q", msg)
		}
		dock.Then(labelsAct, nil)
		dock.Answer(labelsAct, `{"success":false}`)
		if msg := suite.CallErr(t, "label_edit", map[string]any{"label": "prod", "name": "live"}); !strings.Contains(msg, "the label prod was not renamed: the answer did not say that it was") {
			t.Errorf("a rename not said to be done = %q", msg)
		}
	})
}

func TestLabelAssign(t *testing.T) {
	t.Parallel()

	t.Run("refused before anything is sent", func(t *testing.T) {
		t.Parallel()

		dock := withLabels(t)
		suite := session(t, dock, everything)
		for what, tc := range map[string]struct {
			args map[string]any
			want string
		}{
			"no label":                      {map[string]any{"label": " ", "add": []any{"alpha"}}, "label is required"},
			"no environment":                {map[string]any{"label": "lab"}, "nothing to do was given"},
			"an environment there is none":  {map[string]any{"label": "lab", "add": []any{"alpha", "nowhere"}}, `no environment is named or numbered "nowhere": the Dockhand manages alpha, beta. Dockhand would pass over it without a word`},
			"a number that is none's":       {map[string]any{"label": "lab", "remove": []any{99}}, `no environment is named or numbered "99"`},
			"an environment that is a flag": {map[string]any{"label": "lab", "remove": []any{true}}, "an environment is true: it is a name or a number"},
			"one both given and taken":      {map[string]any{"label": "lab", "add": []any{"alpha"}, "remove": []any{1}}, "alpha is in both add and remove"},
		} {
			if msg := suite.CallErr(t, "label_assign", tc.args); !strings.Contains(msg, tc.want) {
				t.Errorf("%s = %q, want %q", what, msg, tc.want)
			}
		}

		// what each has already, or has not, is nothing to do; a dry run says what would be and how the label stands
		out := suite.Call(t, "label_assign", map[string]any{"label": "edge", "add": []any{"beta", 2}, "remove": []any{"ALPHA"}})
		if !strings.Contains(acctest.Str(out["note"]), "nothing to do") || acctest.Object(t, out["label"], "label")["label"] != "edge" {
			t.Errorf("a label where it is already = %v", out)
		}
		out = suite.Call(t, "label_assign", map[string]any{"label": " prod ", "add": []any{"alpha"}, "remove": []any{"beta"}, "dry_run": true})
		if acctest.Strs(t, out["changes"], "changes")[0] != "would take prod from beta" || !acctest.IsBool(out["dry_run"], true) || len(acctest.Strs(t, acctest.Object(t, out["label"], "label")["environments"], "environments")) != 2 || out["note"] != nil {
			t.Errorf("a dry run = %v", out)
		}
		// a new label that another differs from only by case is said to be another
		out = suite.Call(t, "label_assign", map[string]any{"label": "EDGE", "add": []any{"alpha", "beta"}, "dry_run": true})
		if acctest.Strs(t, out["changes"], "changes")[0] != "would give EDGE to alpha, beta" || out["label"] != nil || out["note"] != "EDGE is a new label, and another from edge, which beta has: labels differ by their case" {
			t.Errorf("a dry run of a new label = %v", out)
		}
		if n := len(dock.Requests(labelsAct)) + len(dock.Requests(alphaSave)) + len(dock.Requests(betaSave)); n != 0 {
			t.Errorf("%d changes were sent for what was refused or a dry run", n)
		}

		dock.Fail(envsCall, http.StatusInternalServerError, "Failed")
		if msg := suite.CallErr(t, "label_assign", map[string]any{"label": "lab", "add": []any{"alpha"}}); !strings.Contains(msg, "the environments could not be listed") {
			t.Errorf("environments that could not be listed = %q", msg)
		}
	})

	t.Run("given and taken", func(t *testing.T) {
		t.Parallel()

		dock := withLabels(t)
		suite := session(t, dock, everything)
		dock.Answer(labelsAct, `{"success":true,"affected":1}`)
		dock.Answer(betaSave, betaSaved)
		dock.Then(betaSave, func(dockhandtest.Request) { dock.Answer(labelsCall, oneLabel("edge", "#0000ff", "alpha")) })

		// given through the one call for labels, and taken by saving the environment with the labels it is to keep
		out := suite.Call(t, "label_assign", map[string]any{"label": "edge", "add": []any{"alpha"}, "remove": []any{"beta"}})
		given, taken := dock.Last(labelsAct), dock.Last(betaSave)
		if given.Body != `{"action":"add","environmentIds":[1],"label":"edge"}` || taken.Body != `{"labels":["prod","plain"]}` || strings.Join(acctest.Strs(t, out["changes"], "changes"), "; ") != "gave edge to alpha; took edge from beta" || strings.Join(acctest.Strs(t, acctest.Object(t, out["label"], "label")["environments"], "environments"), " ") != "alpha" || out["note"] != nil {
			t.Errorf("a label given and taken = %v, sent %s and %s", out, given.Body, taken.Body)
		}
	})

	t.Run("taken from its last environment", func(t *testing.T) {
		t.Parallel()

		dock := withLabels(t)
		suite := session(t, dock, everything)
		dock.Answer(envsCall, `[{"id":1,"name":"alpha","connectionType":"socket","labels":["prod"]},{"id":2,"name":"beta","connectionType":"hawser-standard","labels":[]}]`)
		dock.Answer(alphaSave, alphaSaved)
		dock.Then(alphaSave, func(dockhandtest.Request) { dock.Answer(labelsCall, oneLabel("prod", "#1f9d55")) })

		// its only label gone, an empty list is sent, and the colour Dockhand keeps is said
		out := suite.Call(t, "label_assign", map[string]any{"label": "prod", "remove": []any{"alpha", "beta"}})
		if body := dock.Last(alphaSave).Body; body != `{"labels":[]}` || len(dock.Requests(betaSave)) != 0 || !strings.Contains(acctest.Str(out["note"]), "no environment has it now, and Dockhand keeps the colour #1f9d55 for it") || len(acctest.Strs(t, acctest.Object(t, out["label"], "label")["environments"], "environments")) != 0 {
			t.Errorf("a label taken from its last environment = %v, sent %s", out, body)
		}

		// with no colour kept, nothing of it is left to give
		dock.Then(alphaSave, func(dockhandtest.Request) { dock.Answer(labelsCall, oneLabel("prod", "")) })
		if out = suite.Call(t, "label_assign", map[string]any{"label": "prod", "remove": []any{"alpha"}}); out["label"] != nil || out["note"] != nil {
			t.Errorf("a label with no colour taken from its last environment = %v", out)
		}
	})

	t.Run("not given, not taken, or not read", func(t *testing.T) {
		t.Parallel()

		dock := withLabels(t)
		suite := session(t, dock, everything)

		// Dockhand says it gave it to fewer than were asked for
		dock.Answer(labelsAct, `{"success":true,"affected":0}`)
		out := suite.Call(t, "label_assign", map[string]any{"label": "edge", "add": []any{"alpha"}})
		if !strings.Contains(acctest.Str(out["note"]), "Dockhand says it gave the label to 0 environments, of the 1 asked for") {
			t.Errorf("a label given to fewer than asked = %v", out)
		}
		dock.Fail(labelsAct, http.StatusForbidden, notAllowed)
		if msg := suite.CallErr(t, "label_assign", map[string]any{"label": "edge", "add": []any{"alpha"}, "remove": []any{"beta"}}); !strings.Contains(msg, "the label edge was not given to alpha") || len(dock.Requests(betaSave)) != 0 {
			t.Errorf("a label Dockhand refuses to give = %q", msg)
		}

		// not taken: with nothing before it, after a label was given, and after another environment lost it
		dock.Fail(betaSave, http.StatusInternalServerError, "Failed to update environment")
		if msg := suite.CallErr(t, "label_assign", map[string]any{"label": "prod", "remove": []any{"beta"}}); !strings.Contains(msg, "the label prod was not taken from beta, and nothing else was done") {
			t.Errorf("a label Dockhand refuses to take = %q", msg)
		}
		dock.Answer(labelsAct, `{"success":true,"affected":1}`)
		if msg := suite.CallErr(t, "label_assign", map[string]any{"label": "edge", "add": []any{"alpha"}, "remove": []any{"beta"}}); !strings.Contains(msg, "the label edge was not taken from beta, and what came before it was done") {
			t.Errorf("a label given and then not taken = %q", msg)
		}
		dock.Answer(alphaSave, alphaSaved)
		if msg := suite.CallErr(t, "label_assign", map[string]any{"label": "prod", "remove": []any{"alpha", "beta"}}); !strings.Contains(msg, "the label prod was not taken from beta, and what came before it was done") || dock.Last(alphaSave).Body != `{"labels":["Prod"]}` {
			t.Errorf("a label taken from one and not from the next = %q", msg)
		}

		// done, and what has it now not read
		dock.Then(alphaSave, func(dockhandtest.Request) { dock.Fail(labelsCall, http.StatusInternalServerError, "Failed") })
		if out = suite.Call(t, "label_assign", map[string]any{"label": "Prod", "remove": []any{"alpha"}}); !strings.Contains(acctest.Str(out["note"]), "what has the label now could not be read") || out["label"] != nil || len(acctest.Strs(t, out["changes"], "changes")) != 1 {
			t.Errorf("a label taken and not read back = %v", out)
		}
	})
}

func TestLabelDelete(t *testing.T) {
	t.Parallel()

	t.Run("said before it goes", func(t *testing.T) {
		t.Parallel()

		dock := withLabels(t)
		suite := session(t, dock, everything)
		for label, want := range map[string]string{
			"prod": "the label prod from alpha, beta | the colour #1f9d55 kept for it",
			"Prod": "the label Prod from alpha",
			"old":  "the colour #aaaaaa kept for it",
		} {
			out := suite.Call(t, "label_delete", map[string]any{"label": label})
			if would := strings.Join(acctest.Strs(t, out["would_go"], "would_go"), " | "); would != want || !acctest.IsBool(out["removed"], false) || out["label"] != label || !strings.Contains(acctest.Str(out["note"]), "nothing was removed") {
				t.Errorf("what removing %s would take = %v, want %q", label, out, want)
			}
		}
		if n := len(dock.Requests(labelsAct)); n != 0 {
			t.Errorf("%d removals were sent with no confirm", n)
		}
		if msg := suite.CallErr(t, "label_delete", map[string]any{"label": "PLAIN", "confirm": true}); !strings.Contains(msg, "labels differ by their case, and there is plain") {
			t.Errorf("a label in another case = %q", msg)
		}
		dock.Fail(labelsCall, http.StatusForbidden, notAllowed)
		if msg := suite.CallErr(t, "label_delete", map[string]any{"label": "prod", "confirm": true}); !strings.Contains(msg, "the labels could not be listed") {
			t.Errorf("labels the account may not see = %q", msg)
		}
	})

	t.Run("removed and read back as gone", func(t *testing.T) {
		t.Parallel()

		dock := withLabels(t)
		suite := session(t, dock, everything)
		dock.Answer(labelsAct, `{"success":true,"affected":2}`)
		dock.Then(labelsAct, func(dockhandtest.Request) { dock.Answer(labelsCall, oneLabel("old", "#aaaaaa")) })
		out := suite.Call(t, "label_delete", map[string]any{"label": "prod", "confirm": true})
		if body := dock.Last(labelsAct).Body; body != `{"action":"delete","label":"prod"}` || !acctest.IsBool(out["removed"], true) || out["would_go"] != nil || out["note"] != "it was taken from 2 environments" {
			t.Errorf("a label removed = %v, sent %s", out, body)
		}

		// one no environment had: what goes is the colour kept for it
		dock.Answer(labelsAct, `{"success":true,"affected":0}`)
		dock.Then(labelsAct, func(dockhandtest.Request) { dock.Answer(labelsCall, `{"labels":[],"colors":{}}`) })
		if out = suite.Call(t, "label_delete", map[string]any{"label": "old", "confirm": true}); !acctest.IsBool(out["removed"], true) || out["note"] != "no environment had it: the colour kept for it was cleared" {
			t.Errorf("a label no environment had, removed = %v", out)
		}
	})

	t.Run("not removed", func(t *testing.T) {
		t.Parallel()

		dock := withLabels(t)
		suite := session(t, dock, everything)

		// answered as done and still listed
		dock.Answer(labelsAct, `{"success":true,"affected":2}`)
		if msg := suite.CallErr(t, "label_delete", map[string]any{"label": "prod", "confirm": true}); !strings.Contains(msg, "was answered as done, and it is still listed") {
			t.Errorf("a label still listed = %q", msg)
		}
		dock.Answer(labelsAct, `{"success":false}`)
		if msg := suite.CallErr(t, "label_delete", map[string]any{"label": "prod", "confirm": true}); !strings.Contains(msg, "the label prod was not removed: the answer did not say that it was") {
			t.Errorf("a removal not said to be done = %q", msg)
		}
		dock.Fail(labelsAct, http.StatusForbidden, notAllowed)
		if msg := suite.CallErr(t, "label_delete", map[string]any{"label": "prod", "confirm": true}); !strings.Contains(msg, "the label prod was not removed") {
			t.Errorf("a removal Dockhand refuses = %q", msg)
		}

		// removed, and the list not read afterwards: it is not called gone
		dock.Answer(labelsAct, `{"success":true,"affected":2}`)
		dock.Then(labelsAct, func(dockhandtest.Request) { dock.Fail(labelsCall, http.StatusInternalServerError, "Failed") })
		out := suite.Call(t, "label_delete", map[string]any{"label": "prod", "confirm": true})
		if !acctest.IsBool(out["removed"], false) || !strings.Contains(acctest.Str(out["note"]), "it was removed, and the labels could not be listed afterwards") {
			t.Errorf("a removal that could not be read back = %v", out)
		}
	})
}
