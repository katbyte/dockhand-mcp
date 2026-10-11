package tools

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// labelColour is the one form of colour a 1.0.51 Dockhand keeps for a label: a # and six hexadecimal digits. Given anything else it answers that it
// was done and takes away the colour the label had, so a colour is checked here first.
var labelColour = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// The actions Dockhand takes on the labels of its environments, all through the one call.
const (
	labelAdd    = "add"
	labelRename = "rename"
	labelColor  = "set-color"
	labelDelete = "delete"
)

// labelsAre is what every tool about the labels of environments says they are
const labelsAre = "A label here is a word Dockhand groups and filters its environments (Docker hosts) by: it is no label of a container, and Docker knows nothing of it. Labels differ by their case: prod and Prod are two"

// labelRow is one label the environments of the Dockhand have.
type labelRow struct {
	Label        string   `json:"label"`
	Color        string   `json:"color,omitempty" jsonschema:"its colour, as #rrggbb; absent for a label given none"`
	Environments []string `json:"environments"    jsonschema:"the environments that have the label, by name; empty for a label no environment has any more, of which Dockhand keeps only the colour"`
}

// labels is every label the environments of the Dockhand have, by name, and after them the ones no environment has any more that it keeps a colour
// for.
func (r *registry) labels(ctx context.Context) ([]labelRow, error) {
	listed, err := r.dock.GetLabels(ctx)
	if err != nil {
		return nil, fmt.Errorf("the labels could not be listed: %w", refusal(err))
	}

	rows := make([]labelRow, 0, len(listed.Model.Labels))
	for _, l := range listed.Model.Labels {
		row := labelRow{Label: l.Label, Color: cmp.Or(l.Color, listed.Model.Colors[l.Label]), Environments: make([]string, 0, len(l.Environments))}
		for _, e := range l.Environments {
			row.Environments = append(row.Environments, e.EnvName)
		}
		rows = append(rows, row)
	}
	for label, colour := range listed.Model.Colors {
		if !slices.ContainsFunc(rows, func(row labelRow) bool { return row.Label == label }) {
			rows = append(rows, labelRow{Label: label, Color: colour, Environments: []string{}})
		}
	}
	slices.SortStableFunc(rows, func(a, b labelRow) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.Label), strings.ToLower(b.Label)), cmp.Compare(a.Label, b.Label))
	})

	return rows, nil
}

// labelIn is the label of a list a call names, letter for letter: one that differs only by its case is another label, and is said to be.
func labelIn(rows []labelRow, given string) (*labelRow, error) {
	wanted := strings.TrimSpace(given)
	if wanted == "" {
		return nil, errors.New("label is required: the label's name, as label_list gives it")
	}
	names := make([]string, 0, len(rows))
	near := ""
	for i := range rows {
		if rows[i].Label == wanted || rows[i].Label == given {
			return &rows[i], nil
		}
		if strings.EqualFold(rows[i].Label, wanted) {
			near = rows[i].Label
		}
		names = append(names, rows[i].Label)
	}
	if near != "" {
		return nil, fmt.Errorf("no label is %q: labels differ by their case, and there is %s", wanted, near)
	}

	return nil, fmt.Errorf("no label is %q: the Dockhand has %s", wanted, cmp.Or(strings.Join(names, ", "), "none"))
}

// labelAct has Dockhand do one thing to its labels, and gives how many environments it says that changed.
func (r *registry) labelAct(ctx context.Context, request dockhand.PostLabelsRequest) (int, error) {
	answer, err := r.dock.PostLabels(ctx, request)
	if err != nil {
		return 0, refusal(err)
	}
	if !yes(answer.Model.Success) {
		return 0, errNotSaid
	}

	return answer.Model.Affected, nil
}

// labelChangeOut is what changing a label, or which environments have it, answers.
type labelChangeOut struct {
	Label   *labelRow `json:"label,omitempty"   jsonschema:"the label as Dockhand has it now, read back; absent once no environment has it and no colour is kept for it"`
	Changes []string  `json:"changes"           jsonschema:"what was done, in words; empty when nothing needed doing"`
	DryRun  bool      `json:"dry_run,omitempty" jsonschema:"nothing was done: changes says what would be"`
	Note    string    `json:"note,omitempty"`
}

//nolint:gocyclo // four tools registered in one function, each a handler of its own: the count is theirs added up
func registerLabelTools(r *registry) {
	type labelListOut struct {
		Labels []labelRow `json:"labels"         jsonschema:"the labels, by name"`
		Total  int        `json:"total"`
		Note   string     `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "label_list",
		Description: "List the labels of the Dockhand's environments (Docker hosts): each with its colour, if it was given one, and the environments that have it. " + labelsAre + ". A label no environment has any more is listed too when Dockhand still keeps a colour for it.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, labelListOut, error) {
		rows, err := r.labels(ctx)
		if err != nil {
			return nil, labelListOut{}, err
		}
		out := labelListOut{Labels: rows, Total: len(rows)}
		if out.Total == 0 {
			out.Note = "no environment has a label: label_assign gives one to environments"
		}

		return nil, out, nil
	})

	type labelEditIn struct {
		dryRunArg

		Label string  `json:"label"           jsonschema:"the label to change, letter for letter as label_list gives it"`
		Name  string  `json:"name,omitempty"  jsonschema:"another name for it, on every environment that has it"`
		Color *string `json:"color,omitempty" jsonschema:"its colour, as # and six hexadecimal digits: #1f9d55; empty to take its colour away"`
		Merge bool    `json:"merge,omitempty" jsonschema:"when name is a label some environment has already, make the two one: without this that is refused, since it cannot be undone"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "label_edit",
		Description: "Rename a label of the Dockhand's environments (Docker hosts) on every environment that has it, and set or take away its colour. " + labelsAre + ". A name another label has is refused unless merge is given: the two then become one, on every environment that had either, with the colour the other label had if it had one, and that cannot be undone. A colour is # and six hexadecimal digits; any other is refused, which Dockhand would answer as done while taking away the colour the label had. The same name is never sent: Dockhand takes a label renamed to itself from every environment. To give a label to environments or take it from some, use label_assign.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in labelEditIn) (*mcp.CallToolResult, labelChangeOut, error) {
		rows, err := r.labels(ctx)
		if err != nil {
			return nil, labelChangeOut{}, err
		}
		saved, err := labelIn(rows, in.Label)
		if err != nil {
			return nil, labelChangeOut{}, err
		}
		if len(saved.Environments) == 0 {
			return nil, labelChangeOut{}, fmt.Errorf("no environment has the label %s: Dockhand keeps only the colour %s for it, which label_delete clears, and label_assign gives the label to environments", saved.Label, saved.Color)
		}
		row := *saved
		out := labelChangeOut{Label: &row, Changes: []string{}, DryRun: in.DryRun}

		done := func(did, would string) string {
			if in.DryRun {
				return would
			}

			return did
		}
		name := strings.TrimSpace(in.Name)
		rename := name != "" && name != saved.Label
		joins := -1
		if rename {
			joins = slices.IndexFunc(rows, func(other labelRow) bool { return other.Label == name && len(other.Environments) > 0 })
		}
		wanted := saved.Color
		switch {
		case joins >= 0 && !in.Merge:
			return nil, out, fmt.Errorf("%s is a label already, on %s: with merge the two are made one, which cannot be undone", name, strings.Join(rows[joins].Environments, ", "))
		case joins >= 0:
			wanted = cmp.Or(rows[joins].Color, saved.Color)
			colour := ""
			switch {
			case rows[joins].Color != "":
				colour = ", whose colour " + rows[joins].Color + " is kept"
			case saved.Color != "":
				colour = ", which takes its colour " + saved.Color
			}
			out.Changes = append(out.Changes, fmt.Sprintf("%s it one with the label %s, on %s%s", done("made", "would make"), name, strings.Join(rows[joins].Environments, ", "), colour))
		case rename:
			out.Changes = append(out.Changes, fmt.Sprintf("%s it from %s to %s on %s", done("renamed", "would rename"), saved.Label, name, several(len(saved.Environments), "environment")))
		}
		recolour := false
		if in.Color != nil {
			colour := strings.TrimSpace(*in.Color)
			if colour != "" && !labelColour.MatchString(colour) {
				return nil, out, fmt.Errorf("color is %q: a label's colour is # and six hexadecimal digits, as #1f9d55. Dockhand would answer that it was set and take away the colour the label has", *in.Color)
			}
			if !strings.EqualFold(colour, wanted) {
				recolour, wanted = true, colour
				if colour == "" {
					out.Changes = append(out.Changes, done("took", "would take")+" its colour away")
				} else {
					out.Changes = append(out.Changes, fmt.Sprintf("%s its colour to %s", done("set", "would set"), colour))
				}
			}
		}
		if len(out.Changes) == 0 {
			if name == "" && in.Color == nil {
				return nil, out, errors.New("nothing to change was given: name or color")
			}
			out.Note = "nothing to do: it is that way already"

			return nil, out, nil
		}
		if in.DryRun {
			return nil, out, nil
		}

		is := saved.Label
		if rename {
			if _, failed := r.labelAct(ctx, dockhand.PostLabelsRequest{Action: labelRename, OldLabel: saved.Label, NewLabel: name}); failed != nil {
				out.Changes = []string{}

				return nil, out, fmt.Errorf("the label %s was not renamed: %w", saved.Label, failed)
			}
			is = name
		}
		if recolour {
			if _, failed := r.labelAct(ctx, dockhand.PostLabelsRequest{Action: labelColor, Label: is, Color: wanted}); failed != nil {
				return nil, out, fmt.Errorf("the colour of the label %s was not changed, and anything else asked for was done: %w", is, failed)
			}
		}
		after, err := r.labels(ctx)
		if err != nil {
			out.Label = nil
			out.Note = "it was changed, and could not be read back: " + err.Error()

			return nil, out, nil //nolint:nilerr // the change went through: that it could not be read back is said in the answer
		}
		i := slices.IndexFunc(after, func(l labelRow) bool { return l.Label == is })
		if i < 0 {
			out.Label = nil

			return nil, out, fmt.Errorf("the change to the label %s was answered as done, and no environment has a label %s now", saved.Label, is)
		}
		row = after[i]
		if !strings.EqualFold(row.Color, wanted) {
			out.Note = fmt.Sprintf("Dockhand answered that it was done, and the label has %s, which is not what was asked", map[bool]string{true: "the colour " + row.Color, false: "no colour"}[row.Color != ""])
		}

		return nil, out, nil
	})

	type labelAssignIn struct {
		dryRunArg

		Label  string `json:"label"            jsonschema:"the label, letter for letter: one the environments have already, or a new one"`
		Add    []any  `json:"add,omitempty"    jsonschema:"the environments to give it to, each by name or number as env_list gives them"`
		Remove []any  `json:"remove,omitempty" jsonschema:"the environments to take it from, each by name or number"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "label_assign",
		Description: "Give a label to environments (Docker hosts) of the Dockhand, and take it from others: a label is made by giving it to its first environment and is gone when the last one loses it. " + labelsAre + ". An environment that is none of the Dockhand's is refused, which Dockhand would pass over without a word. The other labels of each environment are kept. A label taken from its last environment leaves the colour it was given behind, which label_delete clears. Nothing on a Docker host is changed.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in labelAssignIn) (*mcp.CallToolResult, labelChangeOut, error) {
		label := strings.TrimSpace(in.Label)
		if label == "" {
			return nil, labelChangeOut{}, errors.New("label is required: the label to give or take")
		}
		if len(in.Add)+len(in.Remove) == 0 {
			return nil, labelChangeOut{}, errors.New("nothing to do was given: add, the environments to give the label to, or remove, the ones to take it from")
		}
		listed, err := r.dock.GetEnvironments(ctx)
		if err != nil {
			return nil, labelChangeOut{}, fmt.Errorf("the environments could not be listed: %w", refusal(err))
		}
		// each environment named, as its place in the list: one that is none of them is refused with the ones there are
		places := func(given []any) ([]int, error) {
			found := make([]int, 0, len(given))
			for _, one := range given {
				wanted, unknown := named(one, "an environment")
				if unknown != nil {
					return nil, unknown
				}
				i := slices.IndexFunc(listed.Model, func(e dockhand.GetEnvironmentsResponse) bool {
					return strconv.Itoa(e.Id) == wanted || strings.EqualFold(e.Name, wanted)
				})
				if i < 0 {
					names := make([]string, 0, len(listed.Model))
					for _, e := range listed.Model {
						names = append(names, e.Name)
					}

					return nil, fmt.Errorf("no environment is named or numbered %q: the Dockhand manages %s. Dockhand would pass over it without a word", wanted, cmp.Or(strings.Join(names, ", "), "none"))
				}
				if !slices.Contains(found, i) {
					found = append(found, i)
				}
			}

			return found, nil
		}
		adds, err := places(in.Add)
		if err != nil {
			return nil, labelChangeOut{}, err
		}
		removes, err := places(in.Remove)
		if err != nil {
			return nil, labelChangeOut{}, err
		}
		if i := slices.IndexFunc(adds, func(i int) bool { return slices.Contains(removes, i) }); i >= 0 {
			return nil, labelChangeOut{}, fmt.Errorf("%s is in both add and remove: the label is given to it or taken from it, not both", listed.Model[adds[i]].Name)
		}

		out := labelChangeOut{Changes: []string{}, DryRun: in.DryRun}
		done := func(did, would string) string {
			if in.DryRun {
				return would
			}

			return did
		}
		has := func(i int) bool { return slices.Contains(listed.Model[i].Labels, label) }
		adds = slices.DeleteFunc(adds, has)
		removes = slices.DeleteFunc(removes, func(i int) bool { return !has(i) })
		called := func(places []int) string {
			names := make([]string, 0, len(places))
			for _, i := range places {
				names = append(names, listed.Model[i].Name)
			}

			return strings.Join(names, ", ")
		}
		isNew := !slices.ContainsFunc(listed.Model, func(e dockhand.GetEnvironmentsResponse) bool { return slices.Contains(e.Labels, label) })
		if len(adds) > 0 {
			out.Changes = append(out.Changes, fmt.Sprintf("%s %s to %s", done("gave", "would give"), label, called(adds)))
			if isNew {
				for _, e := range listed.Model {
					if i := slices.IndexFunc(e.Labels, func(other string) bool { return strings.EqualFold(other, label) }); i >= 0 {
						out.Note = fmt.Sprintf("%s is a new label, and another from %s, which %s has: labels differ by their case", label, e.Labels[i], e.Name)

						break
					}
				}
			}
		}
		if len(removes) > 0 {
			out.Changes = append(out.Changes, fmt.Sprintf("%s %s from %s", done("took", "would take"), label, called(removes)))
		}
		back := func() {
			rows, err := r.labels(ctx)
			if err != nil {
				out.Note = note(out.Note, "what has the label now could not be read: "+err.Error())

				return
			}
			if i := slices.IndexFunc(rows, func(l labelRow) bool { return l.Label == label }); i >= 0 {
				out.Label = &rows[i]
				if len(rows[i].Environments) == 0 && len(removes) > 0 && !in.DryRun {
					out.Note = note(out.Note, fmt.Sprintf("no environment has it now, and Dockhand keeps the colour %s for it: label_delete clears that", rows[i].Color))
				}
			}
		}
		if len(out.Changes) == 0 {
			out.Note = "nothing to do: each environment named has it, or has it not, already"
			back()

			return nil, out, nil
		}
		if in.DryRun {
			back()

			return nil, out, nil
		}

		if len(adds) > 0 {
			ids := make([]int, 0, len(adds))
			for _, i := range adds {
				ids = append(ids, listed.Model[i].Id)
			}
			given, failed := r.labelAct(ctx, dockhand.PostLabelsRequest{Action: labelAdd, Label: label, EnvironmentIds: ids})
			if failed != nil {
				out.Changes = []string{}

				return nil, out, fmt.Errorf("the label %s was not given to %s: %w", label, called(adds), failed)
			}
			if given != len(adds) {
				out.Note = note(out.Note, fmt.Sprintf("Dockhand says it gave the label to %s, of the %d asked for", several(given, "environment"), len(adds)))
			}
		}
		for n, i := range removes {
			// every label it is to keep, sent as a list even when that is none: one left out would be kept as it is
			kept := slices.DeleteFunc(slices.Clone(listed.Model[i].Labels), func(other string) bool { return other == label })
			if _, failed := r.dock.PutEnvironmentsById(ctx, listed.Model[i].Id, dockhand.PutEnvironmentsByIdRequest{Labels: append([]string{}, kept...)}); failed != nil {
				out.Changes = []string{}
				was := "nothing else was done"
				if len(adds) > 0 || n > 0 {
					was = "what came before it was done: label_list says where the label is now"
				}

				return nil, out, fmt.Errorf("the label %s was not taken from %s, and %s: %w", label, listed.Model[i].Name, was, refusal(failed))
			}
		}
		back()

		return nil, out, nil
	})

	type labelDeleteIn struct {
		Label   string `json:"label"             jsonschema:"the label to remove, letter for letter as label_list gives it"`
		Confirm bool   `json:"confirm,omitempty" jsonschema:"remove it: without this the answer says what would go and nothing is removed"`
	}
	type labelDeleteOut struct {
		Label   string   `json:"label"`
		Removed bool     `json:"removed"            jsonschema:"the label was removed, and is read back as gone"`
		WouldGo []string `json:"would_go,omitempty" jsonschema:"what removing it takes away, said before anything is removed"`
		Note    string   `json:"note,omitempty"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "label_delete",
		Description: "Remove a label from every environment (Docker host) of the Dockhand that has it, with the colour it was given. " + labelsAre + ". No environment is otherwise touched, and nothing on a Docker host is changed. To take a label from some environments and keep it on others, use label_assign. Without confirm the answer says which environments have it and removes nothing.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in labelDeleteIn) (*mcp.CallToolResult, labelDeleteOut, error) {
		rows, err := r.labels(ctx)
		if err != nil {
			return nil, labelDeleteOut{}, err
		}
		saved, err := labelIn(rows, in.Label)
		if err != nil {
			return nil, labelDeleteOut{}, err
		}
		out := labelDeleteOut{Label: saved.Label}
		if !in.Confirm {
			if len(saved.Environments) > 0 {
				out.WouldGo = append(out.WouldGo, fmt.Sprintf("the label %s from %s", saved.Label, strings.Join(saved.Environments, ", ")))
			}
			if saved.Color != "" {
				out.WouldGo = append(out.WouldGo, fmt.Sprintf("the colour %s kept for it", saved.Color))
			}
			out.Note = "nothing was removed: call again with confirm to remove it. No environment is otherwise touched"

			return nil, out, nil
		}

		taken, err := r.labelAct(ctx, dockhand.PostLabelsRequest{Action: labelDelete, Label: saved.Label})
		if err != nil {
			return nil, out, fmt.Errorf("the label %s was not removed: %w", saved.Label, err)
		}
		left, err := r.labels(ctx)
		if err != nil {
			out.Note = "it was removed, and the labels could not be listed afterwards: " + err.Error()

			return nil, out, nil //nolint:nilerr // the removal went through: that it could not be read back is said in the answer
		}
		if slices.ContainsFunc(left, func(l labelRow) bool { return l.Label == saved.Label }) {
			return nil, out, fmt.Errorf("the removal of the label %s was answered as done, and it is still listed", saved.Label)
		}
		out.Removed = true
		out.Note = "it was taken from " + several(taken, "environment")
		if taken == 0 {
			out.Note = "no environment had it: the colour kept for it was cleared"
		}

		return nil, out, nil
	})
}
