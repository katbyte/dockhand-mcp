package tools

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// What a fresh 1.0.51 server answers for its settings: every general one as it comes, and newer versions not looked for.
const (
	generalNow = `{"actionIconSize":"normal","animateIcons":true,"coloredActionButtons":false,"compactPorts":false,"confirmDestructive":true,"darkTheme":"default","dateFormat":"DD.MM.YYYY","defaultBackupImage":"fnsys/dockhand-backup:v1.0.51","defaultComposeTemplate":"services:\n  app:\n    image: nginx:alpine\n","defaultGrypeArgs":"-o json -v {image}","defaultGrypeImage":"mirror.gcr.io/anchore/grype:v0.119.0","defaultScannerDns":[],"defaultScannerNetworkMode":"","defaultTimezone":"UTC","defaultTrivyArgs":"image --format json {image}","defaultTrivyImage":"mirror.gcr.io/aquasec/trivy:0.75.0","deployLogReconcileCron":"0 4 * * *","deployLogReconcileEnabled":true,"downloadFormat":"tar","editorFont":"system-mono","editorIndentGuides":false,"editorTheme":"default","eventCleanupCron":"30 3 * * *","eventCleanupEnabled":true,"eventCollectionMode":"stream","eventPollInterval":60000,"eventRetentionDays":30,"externalStackPaths":[],"font":"system","fontSize":"normal","formatLogTimestamps":false,"gridFontSize":"normal","highlightUpdates":true,"honorProxyLabels":true,"labelFilterMode":"any","lightTheme":"default","logBufferSizeKb":500,"logMaxLines":2000,"metricsCollectionInterval":30000,"primaryStackLocation":null,"protectScannerImages":true,"scanRetentionCron":"30 4 * * *","scanRetentionEnabled":true,"scanRetentionGraceDays":7,"scanRetentionKeep":10,"scannerCleanupCron":"0 3 * * 0","scannerCleanupEnabled":true,"scheduleCleanupCron":"0 3 * * *","scheduleCleanupEnabled":true,"scheduleRetentionDays":30,"showExposedPorts":false,"showGitCommitHash":false,"showImageChangelogLinks":true,"showStoppedContainers":true,"showWhatsNew":true,"stackLogOperations":["restart","deploy","down"],"terminalFont":"system-mono","timeFormat":"24h","useSelfhstIcons":false}`
	semverNow  = `{"enabled":false,"maxBump":"major","matchFlavor":true,"includePrerelease":false}`
	saveFailed = "Failed to save general settings"

	generalCall = "GET /api/settings/general"
	generalSave = "POST /api/settings/general"
	semverCall  = "GET /api/settings/semver"
	semverSave  = "POST /api/settings/semver"
)

// generalWith is the general settings with some of them otherwise: each old text swapped for a new one.
func generalWith(t *testing.T, swaps map[string]string) string {
	t.Helper()

	out := generalNow
	for old, to := range swaps {
		if !strings.Contains(out, old) {
			t.Fatalf("the settings do not hold %s to swap", old)
		}
		out = strings.Replace(out, old, to, 1)
	}

	return out
}

// withItsSettings is a Dockhand with its settings as they come.
func withItsSettings(t *testing.T) *dockhandtest.Server {
	t.Helper()

	dock := dockhandtest.New(t)
	dock.Answer(generalCall, generalNow)
	dock.Answer(semverCall, semverNow)

	return dock
}

func TestGeneralSettingsAreReadOffTheClient(t *testing.T) {
	t.Parallel()

	if len(generalSettings) != 59 {
		t.Errorf("%d general settings are known, and a 1.0.51 server has 59", len(generalSettings))
	}
	for name, kind := range map[string]reflect.Kind{"eventRetentionDays": reflect.Int, "eventCleanupEnabled": reflect.Bool, "eventCleanupCron": reflect.String, "externalStackPaths": reflect.Slice, timeZone: reflect.String} {
		if generalSettings[name] != kind {
			t.Errorf("%s takes a %s, want a %s", name, generalSettings[name], kind)
		}
	}
}

func TestSettingsGet(t *testing.T) {
	t.Parallel()

	dock := withItsSettings(t)
	suite := session(t, dock, Options{})

	// all of them, by name, with a switch that is off, a number, a text that is empty and a list that is empty each given as it is
	out := suite.Call(t, "settings_get", nil)
	rows := settingsByName(acctest.Rows(t, out["settings"], "settings"))
	if acctest.Num(t, out["total"], "total") != 63 || out["note"] != nil || acctest.Rows(t, out["settings"], "settings")[0]["name"] != "actionIconSize" {
		t.Fatalf("every setting = %v", out)
	}
	if !acctest.IsBool(rows["compactPorts"], false) || rows["eventRetentionDays"] != float64(30) || rows["defaultScannerNetworkMode"] != "" || rows["primaryStackLocation"] != "" || len(acctest.Strs(t, rows["externalStackPaths"], "externalStackPaths")) != 0 || len(acctest.Strs(t, rows["stackLogOperations"], "stackLogOperations")) != 3 || rows["eventCleanupCron"] != "30 3 * * *" || !acctest.IsBool(rows["semver.enabled"], false) || rows["semver.maxBump"] != "major" {
		t.Errorf("the settings as they come = %v", rows)
	}

	// by a part of a name, whatever its case
	if out = suite.Call(t, "settings_get", map[string]any{"name": " CRON "}); acctest.Num(t, out["total"], "total") != 5 {
		t.Errorf("the settings with cron in their name = %v", out)
	}
	if out = suite.Call(t, "settings_get", map[string]any{"name": "semver"}); acctest.Num(t, out["total"], "total") != 4 {
		t.Errorf("the settings for newer versions = %v", out)
	}
	if out = suite.Call(t, "settings_get", map[string]any{"name": "nonsuch"}); acctest.Num(t, out["total"], "total") != 0 || !strings.Contains(acctest.Str(out["note"]), `no setting has "nonsuch" in its name`) {
		t.Errorf("a name no setting has = %v", out)
	}

	// the ones for newer versions unread are said to be, and the general ones are still given
	dock.Fail(semverCall, http.StatusForbidden, notAllowed)
	if out = suite.Call(t, "settings_get", nil); acctest.Num(t, out["total"], "total") != 59 || !strings.Contains(acctest.Str(out["note"]), "the settings for newer versions could not be read") {
		t.Errorf("the settings when those for newer versions cannot be read = %v", out)
	}
	dock.Fail(generalCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "settings_get", nil); !strings.Contains(msg, "the general settings could not be read") {
		t.Errorf("settings the account may not see = %q", msg)
	}
}

// settingsByName is each setting's value by its name.
func settingsByName(rows []map[string]any) map[string]any {
	out := map[string]any{}
	for _, row := range rows {
		out[acctest.Str(row["name"])] = row["value"]
	}

	return out
}

func TestSettingsSetRefusesWhatDockhandWouldMangle(t *testing.T) {
	t.Parallel()

	dock := withItsSettings(t)
	suite := session(t, dock, everything)

	for what, tc := range map[string]struct {
		settings map[string]any
		want     string
	}{
		"nothing":                       {map[string]any{}, "which settings"},
		"a setting there is none of":    {map[string]any{"nonsuch": 1}, `there is no setting named "nonsuch"`},
		"a switch given a number":       {map[string]any{"showWhatsNew": 1}, "showWhatsNew is 1: it is a switch, true or false"},
		"a switch given a word":         {map[string]any{"showWhatsNew": "perhaps"}, "it is a switch"},
		"a number below nothing":        {map[string]any{"eventRetentionDays": -5}, "eventRetentionDays is -5: it is a whole number, none below 0"},
		"a number with a part":          {map[string]any{"eventRetentionDays": 1.5}, "it is a whole number"},
		"a number given a word":         {map[string]any{"eventRetentionDays": "soon"}, "it is a whole number"},
		"a number given a switch":       {map[string]any{"eventRetentionDays": true}, "it is a whole number"},
		"a list given a number":         {map[string]any{"externalStackPaths": 3}, "it is a list of texts, or one text with commas between them"},
		"a list holding a number":       {map[string]any{"externalStackPaths": []any{"/srv/one", 2}}, "externalStackPaths holds 2: it is a list of texts"},
		"a text given a number":         {map[string]any{"dateFormat": 12}, "dateFormat is 12: it is a text"},
		"a schedule that is none":       {map[string]any{"eventCleanupCron": "every night"}, "the job it times would then never run"},
		"a time zone there is none of":  {map[string]any{timeZone: "Mars/Nowhere"}, "it is the name of a time zone"},
		"this machine's own time zone":  {map[string]any{timeZone: "Local"}, "it is the name of a time zone"},
		"no time zone":                  {map[string]any{timeZone: " "}, "it is the name of a time zone"},
		"a step that is none":           {map[string]any{biggestStep: "none"}, "semver.maxBump is \"none\": it is major, minor, patch"},
		"a switch of the newer given 1": {map[string]any{"semver.enabled": 1}, "it is a switch"},
	} {
		if msg := suite.CallErr(t, "settings_set", map[string]any{"settings": tc.settings}); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	// what is so already, by a name in any case and a value written as text; and a dry run
	if out := suite.Call(t, "settings_set", map[string]any{"settings": map[string]any{"EVENTRETENTIONDAYS": "30", "showwhatsnew": "true", "stackLogOperations": "restart, deploy ,down", "primaryStackLocation": "", "semver.maxBump": " major "}}); !strings.Contains(acctest.Str(out["note"]), "each is that way already") || out["after"] != nil {
		t.Errorf("settings set to what they are = %v", out)
	}
	out := suite.Call(t, "settings_set", map[string]any{"settings": map[string]any{"eventRetentionDays": 14, "scanRetentionGraceDays": 0, "externalStackPaths": []any{" /srv/one ", "/srv/two"}, "semver.enabled": true}, "dry_run": true})
	if changes := acctest.Strs(t, out["changes"], "changes"); !acctest.IsBool(out["dry_run"], true) || len(changes) != 4 || changes[0] != "would set eventRetentionDays from 30 to 14" || changes[1] != `would set externalStackPaths from [] to ["/srv/one","/srv/two"]` || changes[2] != "would set scanRetentionGraceDays from 7 to 0" || changes[3] != "would set semver.enabled from false to true" || out["after"] != nil || !strings.Contains(acctest.Str(out["note"]), "nothing was changed") {
		t.Errorf("a dry run of changing settings = %v", out)
	}
	if len(dock.Requests(generalSave))+len(dock.Requests(semverSave)) != 0 {
		t.Fatal("settings were saved by a call that was refused, changed nothing or was a dry run")
	}
}

func TestSettingsSet(t *testing.T) {
	t.Parallel()

	dock := withItsSettings(t)
	dock.Answer(generalSave, generalNow)
	dock.Answer(semverSave, `{"success":true}`)
	suite := session(t, dock, everything)

	// only what changes is sent, each as it is: a number that is 0, a list, a text, a switch that is off
	changed := generalWith(t, map[string]string{`"eventRetentionDays":30`: `"eventRetentionDays":14`, `"scanRetentionGraceDays":7`: `"scanRetentionGraceDays":0`, `"externalStackPaths":[]`: `"externalStackPaths":["/srv/one"]`, `"primaryStackLocation":null`: `"primaryStackLocation":"/srv/stacks"`, `"showWhatsNew":true`: `"showWhatsNew":false`, `"defaultTimezone":"UTC"`: `"defaultTimezone":"Europe/Paris"`, `"eventCleanupCron":"30 3 * * *"`: `"eventCleanupCron":"0 5 * * *"`})
	dock.Then(generalSave, func(dockhandtest.Request) { dock.Answer(generalCall, changed) })
	out := suite.Call(t, "settings_set", map[string]any{"settings": map[string]any{"eventRetentionDays": 14, "scanRetentionGraceDays": "0", "externalStackPaths": "/srv/one", "primaryStackLocation": "/srv/stacks", "showWhatsNew": false, timeZone: "Europe/Paris", "eventCleanupCron": " 0  5 * * * ", "logMaxLines": 2000}})
	body := sent(t, dock.Last(generalSave))
	if body["eventRetentionDays"] != float64(14) || body["scanRetentionGraceDays"] != float64(0) || len(acctest.Strs(t, body["externalStackPaths"], "externalStackPaths")) != 1 || body["primaryStackLocation"] != "/srv/stacks" || !acctest.IsBool(body["showWhatsNew"], false) || body[timeZone] != "Europe/Paris" || body["eventCleanupCron"] != "0 5 * * *" || len(body) != 7 {
		t.Errorf("saving settings sent %v", body)
	}
	after := acctest.Object(t, out["after"], "after")
	if changes := acctest.Strs(t, out["changes"], "changes"); len(changes) != 7 || changes[0] != `set defaultTimezone from "UTC" to "Europe/Paris"` || changes[6] != "set showWhatsNew from true to false" || len(after) != 7 || after["scanRetentionGraceDays"] != float64(0) || after["primaryStackLocation"] != "/srv/stacks" || out["not_taken"] != nil || out["note"] != nil {
		t.Errorf("settings saved = %v", out)
	}

	// a text made empty, which is how the place stacks are kept is forgotten
	dock.Then(generalSave, func(dockhandtest.Request) { dock.Answer(generalCall, generalNow) })
	out = suite.Call(t, "settings_set", map[string]any{"settings": map[string]any{"primaryStackLocation": ""}})
	if body = sent(t, dock.Last(generalSave)); body["primaryStackLocation"] != "" || len(body) != 1 || acctest.Object(t, out["after"], "after")["primaryStackLocation"] != "" {
		t.Errorf("a text forgotten = %v, sent %v", out, body)
	}

	// a general setting and one for newer versions in one call: each goes to the record it is kept in
	dock.Then(generalSave, func(dockhandtest.Request) {
		dock.Answer(generalCall, generalWith(t, map[string]string{`"logMaxLines":2000`: `"logMaxLines":1000`}))
	})
	dock.Then(semverSave, func(dockhandtest.Request) {
		dock.Answer(semverCall, `{"enabled":false,"maxBump":"major","matchFlavor":false,"includePrerelease":false}`)
	})
	out = suite.Call(t, "settings_set", map[string]any{"settings": map[string]any{"logMaxLines": 1000, "semver.matchFlavor": false}})
	if changes := acctest.Strs(t, out["changes"], "changes"); len(changes) != 2 || changes[0] != "set logMaxLines from 2000 to 1000" || changes[1] != "set semver.matchFlavor from true to false" || len(sent(t, dock.Last(generalSave))) != 1 || !acctest.IsBool(sent(t, dock.Last(semverSave))["matchFlavor"], false) || len(acctest.Object(t, out["after"], "after")) != 2 {
		t.Errorf("a general setting and one for newer versions saved together = %v", out)
	}
	dock.Answer(semverCall, semverNow)

	// the settings for newer versions go together, the ones not named as they are, and are read back
	dock.Then(semverSave, func(dockhandtest.Request) {
		dock.Answer(semverCall, `{"enabled":true,"maxBump":"minor","matchFlavor":true,"includePrerelease":false}`)
	})
	out = suite.Call(t, "settings_set", map[string]any{"settings": map[string]any{"semver.enabled": "true", "SEMVER.MAXBUMP": "minor", "semver.matchFlavor": true}})
	if body = sent(t, dock.Last(semverSave)); !acctest.IsBool(body["enabled"], true) || body["maxBump"] != "minor" || !acctest.IsBool(body["matchFlavor"], true) || !acctest.IsBool(body["includePrerelease"], false) || len(body) != 4 {
		t.Errorf("saving the settings for newer versions sent %v", body)
	}
	if changes := acctest.Strs(t, out["changes"], "changes"); len(changes) != 2 || changes[0] != "set semver.enabled from false to true" || changes[1] != `set semver.maxBump from "major" to "minor"` || acctest.Object(t, out["after"], "after")["semver.maxBump"] != "minor" {
		t.Errorf("the settings for newer versions saved = %v", out)
	}
}

func TestSettingsSetPutsBackWhatDockhandDidNotTake(t *testing.T) {
	t.Parallel()

	dock := withItsSettings(t)
	dock.Answer(generalSave, generalNow)
	suite := session(t, dock, everything)
	asked := map[string]any{"settings": map[string]any{"eventPollInterval": 5, "eventCollectionMode": "nonsense", "eventRetentionDays": 14}}

	// one taken, one passed over and one made something else: the last is put back, and each is said
	mangled := generalWith(t, map[string]string{`"eventPollInterval":60000`: `"eventPollInterval":30000`, `"eventRetentionDays":30`: `"eventRetentionDays":14`})
	saves := 0
	dock.Then(generalSave, func(dockhandtest.Request) {
		saves++
		if saves == 1 {
			dock.Answer(generalCall, mangled)
		} else {
			dock.Answer(generalCall, generalWith(t, map[string]string{`"eventRetentionDays":30`: `"eventRetentionDays":14`}))
		}
	})
	out := suite.Call(t, "settings_set", asked)
	if back := sent(t, dock.Last(generalSave)); saves != 2 || back["eventPollInterval"] != float64(60000) || len(back) != 1 {
		t.Errorf("putting back sent %v, in %d saves", back, saves)
	}
	notTaken := acctest.Strs(t, out["not_taken"], "not_taken")
	if changes := acctest.Strs(t, out["changes"], "changes"); len(changes) != 1 || changes[0] != "set eventRetentionDays from 30 to 14" || len(notTaken) != 2 || notTaken[0] != `eventCollectionMode: asked for "nonsense", and Dockhand left it "stream"` || notTaken[1] != "eventPollInterval: asked for 5, and Dockhand made it 30000; it was put back to 60000" || len(acctest.Object(t, out["after"], "after")) != 1 || !strings.Contains(acctest.Str(out["note"]), "did not take every value") {
		t.Errorf("settings Dockhand did not take = %v", out)
	}

	// and when it will not go back, that is an error which says where to look
	dock.Answer(generalCall, generalNow)
	dock.Then(generalSave, func(dockhandtest.Request) { dock.Answer(generalCall, mangled) })
	if msg := suite.CallErr(t, "settings_set", asked); !strings.Contains(msg, "eventPollInterval was not taken as given and could not be put back to 60000") {
		t.Errorf("a setting that would not go back = %q", msg)
	}
}

func TestSettingsSetWhenDockhandSaysItFailed(t *testing.T) {
	t.Parallel()

	dock := withItsSettings(t)
	suite := session(t, dock, everything)
	asked := map[string]any{"settings": map[string]any{timeZone: "Europe/Paris", "eventRetentionDays": 14}}

	// it says the save failed and has saved it: everything named goes back, and the error says so
	saved := generalWith(t, map[string]string{`"defaultTimezone":"UTC"`: `"defaultTimezone":"Europe/Paris"`, `"eventRetentionDays":30`: `"eventRetentionDays":14`})
	saves := 0
	dock.Fail(generalSave, http.StatusInternalServerError, saveFailed)
	dock.Then(generalSave, func(dockhandtest.Request) {
		saves++
		if saves == 1 {
			dock.Answer(generalCall, saved)
		} else {
			dock.Answer(generalCall, generalNow)
		}
	})
	msg := suite.CallErr(t, "settings_set", asked)
	if back := sent(t, dock.Last(generalSave)); saves != 2 || back[timeZone] != "UTC" || back["eventRetentionDays"] != float64(30) || len(back) != 2 {
		t.Errorf("putting back after a failed save sent %v, in %d saves", back, saves)
	}
	if !strings.Contains(msg, "the settings were not saved") || !strings.Contains(msg, saveFailed) || !strings.Contains(msg, "it had saved defaultTimezone, eventRetentionDays all the same, which was put back") || !strings.Contains(msg, "every setting named is as it was") {
		t.Errorf("a save that failed and was kept = %q", msg)
	}

	// it says the save failed and has kept nothing
	dock.Then(generalSave, nil)
	if msg = suite.CallErr(t, "settings_set", asked); !strings.Contains(msg, "nothing of it was kept") {
		t.Errorf("a save that failed and kept nothing = %q", msg)
	}
	// saved, and what it has cannot be read
	dock.Answer(generalSave, generalNow)
	dock.Then(generalSave, func(dockhandtest.Request) {
		dock.Fail(generalCall, http.StatusInternalServerError, "database is locked")
	})
	if msg = suite.CallErr(t, "settings_set", asked); !strings.Contains(msg, "were sent and could not be read back") {
		t.Errorf("a save not read back = %q", msg)
	}
	dock.Then(generalSave, nil)
	if msg = suite.CallErr(t, "settings_set", asked); !strings.Contains(msg, "the general settings could not be read") {
		t.Errorf("settings changed when they cannot be read = %q", msg)
	}
}

func TestSettingsSetNewerVersionsWhenItGoesWrong(t *testing.T) {
	t.Parallel()

	dock := withItsSettings(t)
	dock.Answer(semverSave, `{"success":true}`)
	suite := session(t, dock, everything)
	asked := map[string]any{"settings": map[string]any{"semver.enabled": true}}

	// answered as saved and read back as it was; saved and not read back; and refused
	if msg := suite.CallErr(t, "settings_set", asked); !strings.Contains(msg, "semver.enabled was saved as true and is read back as false") {
		t.Errorf("a setting for newer versions answered and not made = %q", msg)
	}
	dock.Then(semverSave, func(dockhandtest.Request) {
		dock.Fail(semverCall, http.StatusInternalServerError, "database is locked")
	})
	if msg := suite.CallErr(t, "settings_set", asked); !strings.Contains(msg, "the settings for newer versions could not be read") {
		t.Errorf("a setting for newer versions not read back = %q", msg)
	}
	dock.Then(semverSave, nil)
	dock.Answer(semverCall, semverNow)
	dock.Fail(semverSave, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "settings_set", asked); !strings.Contains(msg, "the settings for newer versions were not saved") {
		t.Errorf("a setting for newer versions Dockhand refuses = %q", msg)
	}
	dock.Fail(semverCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "settings_set", asked); !strings.Contains(msg, "the settings for newer versions could not be read") {
		t.Errorf("a setting for newer versions changed when they cannot be read = %q", msg)
	}
}
