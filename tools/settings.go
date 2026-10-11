package tools

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // the zones a time zone is checked against, for a machine that keeps none of its own

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// newerVersions leads the names of the settings that say which newer version of an image Dockhand notes, which it keeps apart from the general
	// ones
	newerVersions = "semver."
	// timeZone is the general setting that names a time zone, which Dockhand saves even when it answers that it could not
	timeZone = "defaultTimezone"
	// biggestStep is the setting that says how far a step a newer version may be, and the steps it takes
	biggestStep = newerVersions + "maxBump"
)

// steps are how far a step a newer version may be for Dockhand to note it: any other word it makes major without saying so.
var steps = []string{"major", "minor", "patch"}

// generalSettings is every general setting by the name Dockhand's API gives it, with the kind of value it takes: read off the request that saves
// them, so that it cannot fall behind the client.
var generalSettings = func() map[string]reflect.Kind {
	kinds := map[string]reflect.Kind{}
	for field := range reflect.TypeFor[dockhand.PostSettingsGeneralRequest]().Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		kind := field.Type.Kind()
		if kind == reflect.Pointer {
			kind = field.Type.Elem().Kind()
		}
		kinds[name] = kind
	}

	return kinds
}()

// versionSettings are the settings that say which newer version of an image Dockhand notes, with the kind of value each takes.
var versionSettings = map[string]reflect.Kind{newerVersions + "enabled": reflect.Bool, biggestStep: reflect.String, newerVersions + "matchFlavor": reflect.Bool, newerVersions + "includePrerelease": reflect.Bool}

// settingsOf is what a record of settings holds, by the name the API gives each: a switch Dockhand did not send is left out, and a list it sent empty
// is an empty list.
func settingsOf(record any) map[string]any {
	out := map[string]any{}
	value := reflect.Indirect(reflect.ValueOf(record))
	for i := range value.NumField() {
		name, _, _ := strings.Cut(value.Type().Field(i).Tag.Get("json"), ",")
		switch field := value.Field(i); field.Kind() {
		case reflect.Pointer:
			if !field.IsNil() {
				out[name] = field.Elem().Interface()
			}
		case reflect.Slice:
			list := make([]string, 0, field.Len())
			for j := range field.Len() {
				list = append(list, field.Index(j).String())
			}
			out[name] = list
		default:
			out[name] = field.Interface()
		}
	}

	return out
}

// versionSettingsOf is the same of the settings for newer versions.
func versionSettingsOf(have *dockhand.GetSettingsSemverResponse) map[string]any {
	return map[string]any{newerVersions + "enabled": yes(have.Enabled), biggestStep: have.MaxBump, newerVersions + "matchFlavor": yes(have.MatchFlavor), newerVersions + "includePrerelease": yes(have.IncludePrerelease)}
}

// valueSaid is a setting's value as it is said and compared: a text in quotes, a list as JSON writes one, and a switch or a number as it is.
func valueSaid(value any) string {
	switch value := value.(type) {
	case string:
		return strconv.Quote(value)
	case []string:
		quoted := make([]string, 0, len(value))
		for _, entry := range value {
			quoted = append(quoted, strconv.Quote(entry))
		}

		return "[" + strings.Join(quoted, ",") + "]"
	default:
		return fmt.Sprint(value)
	}
}

// settingValue is a value a caller gives for a setting, made the kind the setting takes, or the reason it cannot be: Dockhand refuses nothing, and
// passes over or rewrites what it cannot use without a word.
func settingValue(name string, kind reflect.Kind, given any) (any, error) {
	switch kind {
	case reflect.Bool:
		switch given := given.(type) {
		case bool:
			return given, nil
		case string:
			if flag, err := strconv.ParseBool(strings.TrimSpace(given)); err == nil {
				return flag, nil
			}
		default:
		}

		return nil, fmt.Errorf("%s is %v: it is a switch, true or false", name, given)
	case reflect.Int:
		number := -1.0
		switch given := given.(type) {
		case float64:
			number = given
		case string:
			if parsed, err := strconv.Atoi(strings.TrimSpace(given)); err == nil {
				number = float64(parsed)
			}
		default:
		}
		if number < 0 || number != float64(int(number)) {
			return nil, fmt.Errorf("%s is %v: it is a whole number, none below 0", name, given)
		}

		return int(number), nil
	case reflect.Slice:
		list := []string{}
		switch given := given.(type) {
		case string:
			for entry := range strings.SplitSeq(given, ",") {
				if entry = strings.TrimSpace(entry); entry != "" {
					list = append(list, entry)
				}
			}
		case []any:
			for _, entry := range given {
				text, ok := entry.(string)
				if !ok || strings.TrimSpace(text) == "" {
					return nil, fmt.Errorf("%s holds %v: it is a list of texts", name, entry)
				}
				list = append(list, strings.TrimSpace(text))
			}
		default:
			return nil, fmt.Errorf("%s is %v: it is a list of texts, or one text with commas between them", name, given)
		}

		return list, nil
	default:
		text, ok := given.(string)
		if !ok {
			return nil, fmt.Errorf("%s is %v: it is a text", name, given)
		}
		switch {
		case strings.HasSuffix(name, "Cron"):
			cron, err := cronOf(text)
			if err != nil {
				return nil, fmt.Errorf("%s: %w. Dockhand would save it all the same, and the job it times would then never run", name, err)
			}

			return cron, nil
		case name == timeZone:
			zone := strings.TrimSpace(text)
			if _, err := time.LoadLocation(zone); err != nil || zone == "" || zone == "Local" {
				return nil, fmt.Errorf("%s is %q: it is the name of a time zone, as UTC or Europe/Paris. Dockhand would answer that it failed and save it all the same", name, text)
			}

			return zone, nil
		case name == biggestStep:
			if step := strings.TrimSpace(text); slices.Contains(steps, step) {
				return step, nil
			}

			return nil, fmt.Errorf("%s is %q: it is %s. Dockhand would make any other word major without saying so", name, text, strings.Join(steps, ", "))
		default:
			return text, nil
		}
	}
}

func registerSettingsTools(r *registry) {
	// general and versions read the two records settings are kept in, as each is asked for
	general := func(ctx context.Context) (map[string]any, error) {
		have, err := r.dock.GetSettingsGeneral(ctx)
		if err != nil {
			return nil, fmt.Errorf("the general settings could not be read: %w", refusal(err))
		}

		return settingsOf(have.Model), nil
	}
	versions := func(ctx context.Context) (map[string]any, error) {
		have, err := r.dock.GetSettingsSemver(ctx)
		if err != nil {
			return nil, fmt.Errorf("the settings for newer versions could not be read: %w", refusal(err))
		}

		return versionSettingsOf(have.Model), nil
	}

	type settingRow struct {
		Name  string `json:"name"  jsonschema:"the setting, by the name Dockhand's API gives it, which is the name settings_set takes"`
		Value any    `json:"value" jsonschema:"what it is set to: a switch, a number, a text or a list of texts"`
	}
	type settingsGetIn struct {
		Name string `json:"name,omitempty" jsonschema:"only the settings with this in their name, whatever its case: cron, retention, scanner, stack, semver"`
	}
	type settingsGetOut struct {
		Settings []settingRow `json:"settings"       jsonschema:"the settings, by name"`
		Total    int          `json:"total"`
		Note     string       `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "settings_get",
		Description: "Every setting of the Dockhand itself, by the name its API gives it: the sixty or so behind its Settings page, and the four that say which newer version of an image it notes for a container (semver.*). Among the general ones are how many days of what containers did, of schedule runs and of scans it keeps (the Retention settings), when its own clean-up jobs run and whether each is on (the Cron and Enabled settings), where stacks are looked for and kept (externalStackPaths, primaryStackLocation), its time zone, the scanner images and arguments it starts from, how it gathers events and metrics, and how its web interface looks. Filter by name. How each host is scanned and who may sign in are settings of their own, and are not among these.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in settingsGetIn) (*mcp.CallToolResult, settingsGetOut, error) {
		have, err := general(ctx)
		if err != nil {
			return nil, settingsGetOut{}, err
		}
		out := settingsGetOut{Settings: []settingRow{}}
		if newer, failed := versions(ctx); failed != nil {
			out.Note = failed.Error()
		} else {
			maps.Copy(have, newer)
		}

		filter := strings.ToLower(strings.TrimSpace(in.Name))
		for _, name := range slices.Sorted(maps.Keys(have)) {
			if strings.Contains(strings.ToLower(name), filter) {
				out.Settings = append(out.Settings, settingRow{Name: name, Value: have[name]})
			}
		}
		if out.Total = len(out.Settings); out.Total == 0 {
			out.Note = note(out.Note, fmt.Sprintf("no setting has %q in its name: leave name out to see them all", in.Name))
		}

		return nil, out, nil
	})

	type settingsSetIn struct {
		dryRunArg

		Settings map[string]any `json:"settings" jsonschema:"each setting to change, by the name settings_get gives it, with its new value: true or false for a switch, a number, a text, or a list of texts. Settings not named are left as they are"`
	}
	type settingsSetOut struct {
		Changes  []string       `json:"changes"             jsonschema:"each setting changed, with what it was; with a dry run, each that would be"`
		NotTaken []string       `json:"not_taken,omitempty" jsonschema:"each setting Dockhand did not take as it was given, with what it made of it: each is as it was before"`
		After    map[string]any `json:"after,omitempty"     jsonschema:"the settings named, as Dockhand has them now, read back; absent from a dry run"`
		DryRun   bool           `json:"dry_run,omitempty"   jsonschema:"nothing was changed: changes says what would be"`
		Note     string         `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "settings_set",
		Description: "Change settings of the Dockhand itself, by the names settings_get gives them: only the settings named change, and a name it does not have is refused. Dockhand itself refuses nothing here: it passes over a word it does not know, raises a number it finds too small, saves a schedule it cannot read, after which that clean-up job never runs, and saves a time zone it does not know while answering that it failed. So a schedule (five fields, as 0 3 * * *) and a time zone are checked before anything is sent, every setting is read back, and one Dockhand did not take as given is put back to what it was and named in not_taken: a setting ends as it was asked for or as it was. A Retention setting is what the clean-up jobs say they go by: a smaller number has the next run remove more, and what is removed cannot be got back. The semver settings are saved together, the ones not named as they are.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in settingsSetIn) (*mcp.CallToolResult, settingsSetOut, error) {
		if len(in.Settings) == 0 {
			return nil, settingsSetOut{}, errors.New("which settings: give each by the name settings_get gives it, with its new value")
		}
		known := maps.Clone(generalSettings)
		maps.Copy(known, versionSettings)
		names := slices.Sorted(maps.Keys(known))
		want := map[string]any{}
		for _, given := range slices.Sorted(maps.Keys(in.Settings)) {
			i := slices.IndexFunc(names, func(name string) bool { return strings.EqualFold(name, strings.TrimSpace(given)) })
			if i < 0 {
				return nil, settingsSetOut{}, fmt.Errorf("there is no setting named %q: settings_get lists them, and takes a part of a name to find one by", given)
			}
			value, err := settingValue(names[i], known[names[i]], in.Settings[given])
			if err != nil {
				return nil, settingsSetOut{}, err
			}
			want[names[i]] = value
		}

		// what each is now, from whichever of the two records it is kept in
		before := map[string]any{}
		if slices.ContainsFunc(slices.Collect(maps.Keys(want)), func(name string) bool { return !strings.HasPrefix(name, newerVersions) }) {
			have, err := general(ctx)
			if err != nil {
				return nil, settingsSetOut{}, err
			}
			maps.Copy(before, have)
		}
		newer := map[string]any{}
		if slices.ContainsFunc(slices.Collect(maps.Keys(want)), func(name string) bool { return strings.HasPrefix(name, newerVersions) }) {
			have, err := versions(ctx)
			if err != nil {
				return nil, settingsSetOut{}, err
			}
			maps.Copy(before, have)
			newer = have
		}

		out := settingsSetOut{Changes: []string{}, DryRun: in.DryRun}
		send, sendNewer := map[string]any{}, false
		said := map[string]string{}
		for _, name := range slices.Sorted(maps.Keys(want)) {
			if valueSaid(before[name]) == valueSaid(want[name]) || (before[name] == nil && want[name] == "") {
				continue
			}
			said[name] = fmt.Sprintf("%s %s from %s to %s", map[bool]string{true: "would set", false: "set"}[in.DryRun], name, valueSaid(before[name]), valueSaid(want[name]))
			if strings.HasPrefix(name, newerVersions) {
				newer[name], sendNewer = want[name], true
			} else {
				send[name] = want[name]
			}
		}
		if len(said) == 0 {
			out.Note = "nothing to do: each is that way already"

			return nil, out, nil
		}
		if in.DryRun {
			for _, name := range slices.Sorted(maps.Keys(said)) {
				out.Changes = append(out.Changes, said[name])
			}
			out.Note = "nothing was changed. What Dockhand makes of a value is only told by saving it: one it does not take as given is put back"

			return nil, out, nil
		}
		out.After = map[string]any{}

		if len(send) > 0 {
			_, failed := r.dock.SaveGeneralSettings(ctx, send)
			now, err := general(ctx)
			if err != nil {
				return nil, out, fmt.Errorf("the settings were sent and could not be read back, so what Dockhand has of them is not known: %w", cmp.Or(failed, err))
			}

			// what is not as it was asked for goes back to what it was: all of it when Dockhand said the save failed, which it says of some it has
			// saved
			back := map[string]any{}
			for _, name := range slices.Sorted(maps.Keys(send)) {
				taken := valueSaid(now[name]) == valueSaid(send[name]) || (now[name] == nil && send[name] == "")
				if taken && failed == nil {
					out.Changes, out.After[name] = append(out.Changes, said[name]), now[name]

					continue
				}
				made := "left it " + valueSaid(before[name])
				if valueSaid(now[name]) != valueSaid(before[name]) {
					back[name] = before[name]
					made = fmt.Sprintf("made it %s; it was put back to %s", valueSaid(now[name]), valueSaid(before[name]))
				}
				if failed == nil {
					out.NotTaken = append(out.NotTaken, fmt.Sprintf("%s: asked for %s, and Dockhand %s", name, valueSaid(send[name]), made))
				}
			}
			if len(back) > 0 {
				_, _ = r.dock.SaveGeneralSettings(ctx, back)
				again, err := general(ctx)
				for _, name := range slices.Sorted(maps.Keys(back)) {
					if err != nil || valueSaid(again[name]) != valueSaid(before[name]) {
						return nil, out, fmt.Errorf("%s was not taken as given and could not be put back to %s: settings_get says what Dockhand has now", name, valueSaid(before[name]))
					}
				}
			}
			if failed != nil {
				kept := "nothing of it was kept"
				if len(back) > 0 {
					kept = "it had saved " + strings.Join(slices.Sorted(maps.Keys(back)), ", ") + " all the same, which was put back"
				}

				return nil, out, fmt.Errorf("the settings were not saved: %w. Dockhand can answer so and have saved some of it all the same: %s, and every setting named is as it was", refusal(failed), kept)
			}
		}

		if sendNewer {
			request := dockhand.PostSettingsSemverRequest{}
			for name, value := range newer {
				switch value := value.(type) {
				case string:
					request.MaxBump = value
				case bool:
					switch name {
					case newerVersions + "enabled":
						request.Enabled = &value
					case newerVersions + "matchFlavor":
						request.MatchFlavor = &value
					default:
						request.IncludePrerelease = &value
					}
				default:
				}
			}
			if _, err := r.dock.PostSettingsSemver(ctx, request); err != nil {
				return nil, out, fmt.Errorf("the settings for newer versions were not saved: %w", refusal(err))
			}
			now, err := versions(ctx)
			if err != nil {
				return nil, out, err
			}
			for _, name := range slices.Sorted(maps.Keys(said)) {
				if !strings.HasPrefix(name, newerVersions) {
					continue
				}
				if valueSaid(now[name]) != valueSaid(want[name]) {
					return nil, out, fmt.Errorf("%s was saved as %s and is read back as %s", name, valueSaid(want[name]), valueSaid(now[name]))
				}
				out.Changes, out.After[name] = append(out.Changes, said[name]), now[name]
			}
		}
		slices.Sort(out.Changes)
		if len(out.NotTaken) > 0 {
			out.Note = "Dockhand did not take every value as it was given: each in not_taken is as it was before"
		}

		return nil, out, nil
	})
}
