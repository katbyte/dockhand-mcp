//go:build acceptance

package dockhand_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/pandorest/client"
)

// The labels of environments are given, coloured, renamed, made one and removed through the one call that does all of it: what each action answers
// and leaves, which colours a server keeps, that labels differ by case, that one renamed to itself is taken from every environment, and that a colour
// outlasts the label it was given to.
func TestEnvironmentLabelsGivenColouredRenamedAndRemoved(t *testing.T) {
	const one, two, three = "it-label-one", "it-label-two", "it-label-three"
	c := dock(t)
	ctx := t.Context()
	alpha, beta := world.Env(testenv.Alpha), world.Env(testenv.Beta)

	act := func(request dockhand.PostLabelsRequest) (int, error) {
		answer, err := c.PostLabels(ctx, request)
		if err != nil {
			return 0, err
		}
		if !answer.Answer.Success {
			t.Errorf("%+v was answered without its being said to have succeeded", request)
		}

		return answer.Answer.Affected, nil
	}
	// on is the environments a label is on, by name in order, and its colour; colourOf is the colour kept under a name, whether or not any
	// environment has the label
	on := func(label string) (string, string) {
		listed, err := c.GetLabels(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range listed.Answer.Labels {
			if l.Label != label {
				continue
			}

			names := make([]string, 0, len(l.Environments))
			for _, e := range l.Environments {
				names = append(names, e.EnvName)
			}
			slices.Sort(names)
			if l.Count != len(names) {
				t.Errorf("%s is counted on %d environments and listed on %v", label, l.Count, names)
			}

			return strings.Join(names, " "), l.Color
		}

		return "", ""
	}
	colourOf := func(label string) string {
		listed, err := c.GetLabels(ctx)
		if err != nil {
			t.Fatal(err)
		}

		return listed.Answer.Colors[label]
	}
	labelsOf := func(env int) []string {
		got, err := c.GetEnvironmentsById(ctx, dockhand.EnvironmentID(env))
		if err != nil {
			t.Fatal(err)
		}

		return got.Answer.Labels
	}
	had := map[int][]string{int(alpha): labelsOf(int(alpha)), int(beta): labelsOf(int(beta))}
	clean := func() {
		for _, label := range []string{one, two, three, strings.ToUpper(one)} {
			_, _ = c.PostLabels(noCancel(t), dockhand.PostLabelsRequest{Action: "delete", Label: label})
		}
		for env, labels := range had {
			_, _ = c.PutEnvironmentsById(noCancel(t), dockhand.EnvironmentID(env), dockhand.PutEnvironmentsByIdRequest{Labels: append([]string{}, labels...)})
		}
	}
	clean()
	t.Cleanup(clean)

	// an action that is none, and one with what it needs left out, are refused
	for what, request := range map[string]dockhand.PostLabelsRequest{
		"no action":                      {},
		"an action that is none":         {Action: "merge", Label: one},
		"a label given to nothing":       {Action: "add", Label: one},
		"no label to give":               {Action: "add", Label: "  ", EnvironmentIds: []dockhand.EnvironmentID{alpha}},
		"no label to remove":             {Action: "delete"},
		"no label to colour":             {Action: "set-color", Color: "#123456"},
		"a rename with no name to give":  {Action: "rename", OldLabel: one},
		"a rename to a name of nothing":  {Action: "rename", OldLabel: one, NewLabel: "  "},
		"a rename with no label to name": {Action: "rename", NewLabel: two},
	} {
		if _, err := act(request); client.StatusCode(err) != http.StatusBadRequest {
			t.Errorf("%s = %v, want a 400", what, err)
		}
	}

	// given to two environments, the spaces round it taken off; given again it changes none, and an environment there is none of is passed over
	if given, err := act(dockhand.PostLabelsRequest{Action: "add", Label: "  " + one + " ", EnvironmentIds: []dockhand.EnvironmentID{alpha, beta}}); err != nil || given != 2 {
		t.Fatalf("a label given to two environments = %d, %v", given, err)
	}
	if where, colour := on(one); where != "alpha beta" || colour != "" || !slices.Contains(labelsOf(int(alpha)), one) {
		t.Errorf("the label is on %q with the colour %q, and alpha has %v", where, colour, labelsOf(int(alpha)))
	}
	if again, err := act(dockhand.PostLabelsRequest{Action: "add", Label: one, EnvironmentIds: []dockhand.EnvironmentID{alpha, 999999}}); err != nil || again != 0 {
		t.Errorf("a label given to an environment that has it and to one there is none of = %d, %v: Dockhand answers that it was done to none", again, err)
	}
	// in another case it is another label
	if given, err := act(dockhand.PostLabelsRequest{Action: "add", Label: strings.ToUpper(one), EnvironmentIds: []dockhand.EnvironmentID{alpha}}); err != nil || given != 1 {
		t.Errorf("the label in another case = %d, %v", given, err)
	}
	if where, _ := on(strings.ToUpper(one)); where != "alpha" {
		t.Errorf("the label in another case is on %q: labels used to differ by their case", where)
	}
	if _, err := act(dockhand.PostLabelsRequest{Action: "delete", Label: strings.ToUpper(one)}); err != nil {
		t.Fatal(err)
	}

	// a colour is # and six hexadecimal digits, kept as it is written; anything else is answered as done and takes the colour away
	if _, err := act(dockhand.PostLabelsRequest{Action: "set-color", Label: one, Color: "#12AbCd"}); err != nil {
		t.Fatal(err)
	}
	if _, colour := on(one); colour != "#12AbCd" || colourOf(one) != "#12AbCd" {
		t.Errorf("the colour of the label = %q, and beside the list %q", colour, colourOf(one))
	}
	for _, other := range []string{"red", "#abc", "123456", "#12345678", ""} {
		if _, err := act(dockhand.PostLabelsRequest{Action: "set-color", Label: one, Color: "#12AbCd"}); err != nil {
			t.Fatal(err)
		}
		if _, err := act(dockhand.PostLabelsRequest{Action: "set-color", Label: one, Color: other}); err != nil {
			t.Errorf("the colour %q = %v, want it answered as done", other, err)
		}
		if _, colour := on(one); colour != "" {
			t.Errorf("after the colour %q the label is %q: Dockhand used to take its colour away, which is why the tools check one first", other, colour)
		}
	}
	if _, err := act(dockhand.PostLabelsRequest{Action: "set-color", Label: one, Color: "#111111"}); err != nil {
		t.Fatal(err)
	}

	// renamed, it keeps its environments and its colour, and a label there is none of is answered as renamed on none
	if renamed, err := act(dockhand.PostLabelsRequest{Action: "rename", OldLabel: one, NewLabel: " " + two + " "}); err != nil || renamed != 2 {
		t.Fatalf("a label renamed = %d, %v", renamed, err)
	}
	if where, colour := on(two); where != "alpha beta" || colour != "#111111" || colourOf(one) != "" {
		t.Errorf("once renamed the label is on %q with the colour %q, and the name it had keeps %q", where, colour, colourOf(one))
	}
	if ghost, err := act(dockhand.PostLabelsRequest{Action: "rename", OldLabel: one, NewLabel: three}); err != nil || ghost != 0 {
		t.Errorf("a label there is none of, renamed = %d, %v", ghost, err)
	}

	// renamed to a label there is already, the two are one: on every environment that had either, with the colour of the one it joins when that has
	// one
	if _, err := act(dockhand.PostLabelsRequest{Action: "add", Label: three, EnvironmentIds: []dockhand.EnvironmentID{beta}}); err != nil {
		t.Fatal(err)
	}
	if joined, err := act(dockhand.PostLabelsRequest{Action: "rename", OldLabel: two, NewLabel: three}); err != nil || joined != 2 {
		t.Errorf("a label renamed to another's name = %d, %v", joined, err)
	}
	if where, colour := on(three); where != "alpha beta" || colour != "#111111" || slices.Contains(labelsOf(int(beta)), two) {
		t.Errorf("the two made one are on %q with the colour %q: the one with no colour used to take the other's", where, colour)
	}
	if _, err := act(dockhand.PostLabelsRequest{Action: "add", Label: one, EnvironmentIds: []dockhand.EnvironmentID{alpha}}); err != nil {
		t.Fatal(err)
	}
	if _, err := act(dockhand.PostLabelsRequest{Action: "set-color", Label: one, Color: "#222222"}); err != nil {
		t.Fatal(err)
	}
	if _, err := act(dockhand.PostLabelsRequest{Action: "rename", OldLabel: three, NewLabel: one}); err != nil {
		t.Fatal(err)
	}
	if where, colour := on(one); where != "alpha beta" || colour != "#222222" || colourOf(three) != "" {
		t.Errorf("joined to a label with a colour, the two are on %q with the colour %q: the colour of the one joined used to be kept", where, colour)
	}

	// taken from one environment by saving the environment with the labels it is to keep; taken from the last, its colour is kept under its name
	for _, env := range []int{int(alpha), int(beta)} {
		kept := slices.DeleteFunc(labelsOf(env), func(label string) bool { return label == one })
		if _, err := c.PutEnvironmentsById(ctx, dockhand.EnvironmentID(env), dockhand.PutEnvironmentsByIdRequest{Labels: append([]string{}, kept...)}); err != nil {
			t.Fatalf("saving environment %d without the label: %v", env, err)
		}
	}
	if where, _ := on(one); where != "" || colourOf(one) != "#222222" {
		t.Errorf("taken from every environment, the label is on %q and its colour is %q: Dockhand used to keep the colour", where, colourOf(one))
	}
	// removing a label no environment has clears the colour kept for it
	if gone, err := act(dockhand.PostLabelsRequest{Action: "delete", Label: one}); err != nil || gone != 0 || colourOf(one) != "" {
		t.Errorf("a label no environment has, removed = %d, %v, leaving the colour %q", gone, err, colourOf(one))
	}

	// renamed to itself, a label is taken from every environment, with its colour
	if _, err := act(dockhand.PostLabelsRequest{Action: "add", Label: two, EnvironmentIds: []dockhand.EnvironmentID{alpha, beta}}); err != nil {
		t.Fatal(err)
	}
	if _, err := act(dockhand.PostLabelsRequest{Action: "set-color", Label: two, Color: "#333333"}); err != nil {
		t.Fatal(err)
	}
	if itself, err := act(dockhand.PostLabelsRequest{Action: "rename", OldLabel: two, NewLabel: two}); err != nil || itself != 2 {
		t.Errorf("a label renamed to itself = %d, %v", itself, err)
	}
	if where, _ := on(two); where != "" || colourOf(two) != "" {
		t.Errorf("renamed to itself, the label is on %q with the colour %q: Dockhand used to take it from every environment, which is why the tools never send that", where, colourOf(two))
	}

	// removed from every environment that has it, with its colour; one there is none of is answered as removed from none
	if _, err := act(dockhand.PostLabelsRequest{Action: "add", Label: three, EnvironmentIds: []dockhand.EnvironmentID{alpha, beta}}); err != nil {
		t.Fatal(err)
	}
	if _, err := act(dockhand.PostLabelsRequest{Action: "set-color", Label: three, Color: "#444444"}); err != nil {
		t.Fatal(err)
	}
	if gone, err := act(dockhand.PostLabelsRequest{Action: "delete", Label: three}); err != nil || gone != 2 || colourOf(three) != "" {
		t.Errorf("a label removed = %d, %v, leaving the colour %q", gone, err, colourOf(three))
	}
	if ghost, err := act(dockhand.PostLabelsRequest{Action: "delete", Label: three}); err != nil || ghost != 0 {
		t.Errorf("a label there is none of, removed = %d, %v", ghost, err)
	}
	for env, labels := range had {
		if now := labelsOf(env); !slices.Equal(now, labels) && len(now)+len(labels) != 0 {
			t.Errorf("environment %d has the labels %v at the end, want the %v it had", env, now, labels)
		}
	}
}
