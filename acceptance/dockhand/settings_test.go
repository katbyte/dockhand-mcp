//go:build acceptance

package dockhand_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/pandorest/client"
)

// The general settings are saved a key at a time and read back: what the server takes as given, what it changes or passes over without a word, a
// schedule it cannot read and a time zone it does not know, both of which it saves; and the settings for newer versions, which it replaces whole.
// Each is put back as it was.
func TestSettingsSavedMangledAndPutBack(t *testing.T) {
	c := dock(t)
	ctx := t.Context()

	before, err := c.GetSettingsGeneral(ctx)
	if err != nil {
		t.Fatal(err)
	}
	was := before.Model
	if was.EventRetentionDays == 0 || was.EventCleanupCron == "" || was.DefaultTimezone == "" || was.LogMaxLines == 0 || was.ShowWhatsNew == nil || len(was.StackLogOperations) == 0 {
		t.Fatalf("the general settings as they are = %+v", was)
	}
	// put back whatever happens: each key this test touches, as it was
	back := map[string]any{"eventRetentionDays": was.EventRetentionDays, "scanRetentionGraceDays": was.ScanRetentionGraceDays, "scanRetentionKeep": was.ScanRetentionKeep, "externalStackPaths": slices.Clone(was.ExternalStackPaths), "primaryStackLocation": was.PrimaryStackLocation, "logMaxLines": was.LogMaxLines, "downloadFormat": was.DownloadFormat, "stackLogOperations": slices.Clone(was.StackLogOperations), "eventCleanupCron": was.EventCleanupCron, "defaultTimezone": was.DefaultTimezone}
	if was.ExternalStackPaths == nil {
		back["externalStackPaths"] = []string{}
	}
	t.Cleanup(func() { _, _ = c.SaveGeneralSettings(noCancel(t), back) })
	read := func() *dockhand.GetSettingsGeneralResponse {
		now, failed := c.GetSettingsGeneral(ctx)
		if failed != nil {
			t.Fatal(failed)
		}

		return now.Model
	}

	// taken as given: only the keys sent change, a number that is 0 and a text that is empty among them, and the answer is every setting
	saved, err := c.SaveGeneralSettings(ctx, map[string]any{"eventRetentionDays": was.EventRetentionDays + 1, "scanRetentionGraceDays": 0, "externalStackPaths": []string{"/srv/one", "/srv/two"}, "primaryStackLocation": "/srv/stacks"})
	if err != nil || saved.Model.EventRetentionDays != was.EventRetentionDays+1 || saved.Model.ScanRetentionGraceDays != 0 || !slices.Equal(saved.Model.ExternalStackPaths, []string{"/srv/one", "/srv/two"}) || saved.Model.PrimaryStackLocation != "/srv/stacks" || saved.Model.EventCleanupCron != was.EventCleanupCron || saved.Model.DefaultTimezone != was.DefaultTimezone {
		t.Fatalf("settings saved = %+v, %v", saved.Model, err)
	}
	if now := read(); now.EventRetentionDays != was.EventRetentionDays+1 || now.ScanRetentionGraceDays != 0 || len(now.ExternalStackPaths) != 2 || now.PrimaryStackLocation != "/srv/stacks" || now.LogMaxLines != was.LogMaxLines {
		t.Errorf("the settings read back = %+v", now)
	}
	// the generated call sends the paths as the list they are, and the text is forgotten by an empty one
	if listed, err := c.PostSettingsGeneral(ctx, dockhand.PostSettingsGeneralRequest{ExternalStackPaths: []string{"/srv/three"}}); err != nil || !slices.Equal(listed.Model.ExternalStackPaths, []string{"/srv/three"}) {
		t.Errorf("the paths saved by the generated call = %+v, %v", listed.Model.ExternalStackPaths, err)
	}
	if forgot, err := c.SaveGeneralSettings(ctx, map[string]any{"primaryStackLocation": "", "externalStackPaths": []string{}}); err != nil || forgot.Model.PrimaryStackLocation != "" || len(forgot.Model.ExternalStackPaths) != 0 {
		t.Errorf("a place and the paths forgotten = %q and %v, %v", forgot.Model.PrimaryStackLocation, forgot.Model.ExternalStackPaths, err)
	}

	// changed or passed over without a word, each answered 200: a number too small raised, one too big brought down, a word it does not know left,
	// and the entries of a list it does not know dropped
	for what, tc := range map[string]struct {
		sent map[string]any
		ok   func(now *dockhand.GetSettingsGeneralResponse) bool
	}{
		"a number too small":      {map[string]any{"scanRetentionKeep": 0}, func(now *dockhand.GetSettingsGeneralResponse) bool { return now.ScanRetentionKeep == 1 }},
		"a number too big":        {map[string]any{"logMaxLines": 5000}, func(now *dockhand.GetSettingsGeneralResponse) bool { return now.LogMaxLines == 2000 }},
		"a word it does not know": {map[string]any{"downloadFormat": "no-such-format"}, func(now *dockhand.GetSettingsGeneralResponse) bool { return now.DownloadFormat == was.DownloadFormat }},
		"a list with an unknown entry": {map[string]any{"stackLogOperations": []string{"deploy", "no-such-operation"}}, func(now *dockhand.GetSettingsGeneralResponse) bool {
			return slices.Equal(now.StackLogOperations, []string{"deploy"})
		}},
		"a key it does not have": {map[string]any{"noSuchSetting": true}, func(now *dockhand.GetSettingsGeneralResponse) bool {
			return now.EventRetentionDays == was.EventRetentionDays+1
		}},
	} {
		if _, err := c.SaveGeneralSettings(ctx, tc.sent); err != nil {
			t.Errorf("%s (%v) was refused, and Dockhand refuses nothing here: %v", what, tc.sent, err)

			continue
		}
		if now := read(); !tc.ok(now) {
			t.Errorf("%s (%v) left scanRetentionKeep %d, logMaxLines %d, downloadFormat %q, stackLogOperations %v", what, tc.sent, now.ScanRetentionKeep, now.LogMaxLines, now.DownloadFormat, now.StackLogOperations)
		}
	}

	// a schedule it cannot read is saved as it is
	if _, err := c.SaveGeneralSettings(ctx, map[string]any{"eventCleanupCron": "not a cron"}); err != nil || read().EventCleanupCron != "not a cron" {
		t.Errorf("a schedule that is none = %q, %v: Dockhand saves it", read().EventCleanupCron, err)
	}
	if _, err := c.SaveGeneralSettings(ctx, map[string]any{"eventCleanupCron": was.EventCleanupCron}); err != nil || read().EventCleanupCron != was.EventCleanupCron {
		t.Fatalf("the schedule put back = %q, %v", read().EventCleanupCron, err)
	}
	// a time zone it does not know is answered as a failure and saved all the same
	if _, err := c.SaveGeneralSettings(ctx, map[string]any{"defaultTimezone": "Nowhere/Atall"}); client.StatusCode(err) != http.StatusInternalServerError || !strings.Contains(err.Error(), "Failed to save general settings") {
		t.Errorf("a time zone there is none of = %v, want a 500 that says the save failed", err)
	}
	if now := read(); now.DefaultTimezone != "Nowhere/Atall" {
		t.Errorf("the time zone after a save that was said to fail = %q: Dockhand saves it all the same. If it no longer does, the tools need not put it back", now.DefaultTimezone)
	}

	// put back, and read back as it was
	if _, err := c.SaveGeneralSettings(ctx, back); err != nil {
		t.Fatal(err)
	}
	if now := read(); now.EventRetentionDays != was.EventRetentionDays || now.ScanRetentionGraceDays != was.ScanRetentionGraceDays || now.ScanRetentionKeep != was.ScanRetentionKeep || !slices.Equal(now.ExternalStackPaths, was.ExternalStackPaths) || now.PrimaryStackLocation != was.PrimaryStackLocation || now.LogMaxLines != was.LogMaxLines || now.DownloadFormat != was.DownloadFormat || !slices.Equal(now.StackLogOperations, was.StackLogOperations) || now.EventCleanupCron != was.EventCleanupCron || now.DefaultTimezone != was.DefaultTimezone {
		t.Errorf("the settings put back = %+v, want them as they were: %+v", now, was)
	}
}

// The settings for newer versions are read and saved: every one is set by each save, one left out going to what it is by default, and a step it does
// not know made the biggest.
func TestNewerVersionSettingsAreReplacedWhole(t *testing.T) {
	c := dock(t)
	ctx := t.Context()

	before, err := c.GetSettingsSemver(ctx)
	if err != nil {
		t.Fatal(err)
	}
	was := before.Model
	t.Cleanup(func() {
		_, _ = c.PostSettingsSemver(noCancel(t), dockhand.PostSettingsSemverRequest{Enabled: was.Enabled, MaxBump: was.MaxBump, MatchFlavor: was.MatchFlavor, IncludePrerelease: was.IncludePrerelease})
	})
	save := func(request dockhand.PostSettingsSemverRequest) *dockhand.GetSettingsSemverResponse {
		if answered, err := c.PostSettingsSemver(ctx, request); err != nil || !yes(answered.Model.Success) {
			t.Fatalf("saving %+v = %+v, %v", request, answered.Model, err)
		}
		now, err := c.GetSettingsSemver(ctx)
		if err != nil {
			t.Fatal(err)
		}

		return now.Model
	}

	// all four as given, each step it knows
	for _, step := range []string{"minor", "patch", "major"} {
		if now := save(dockhand.PostSettingsSemverRequest{Enabled: new(true), MaxBump: step, MatchFlavor: new(false), IncludePrerelease: new(true)}); !yes(now.Enabled) || now.MaxBump != step || yes(now.MatchFlavor) || !yes(now.IncludePrerelease) {
			t.Errorf("newer versions looked for a %s step at most = %+v", step, now)
		}
	}
	// a step it does not know is made the biggest, and the save is answered as a success
	if now := save(dockhand.PostSettingsSemverRequest{Enabled: new(true), MaxBump: "no-such-step", MatchFlavor: new(false), IncludePrerelease: new(true)}); now.MaxBump != "major" {
		t.Errorf("a step there is none of = %+v, want it made major", now)
	}
	// one alone: the rest go to what they are by default, which switches the looking off
	if now := save(dockhand.PostSettingsSemverRequest{MaxBump: "minor"}); yes(now.Enabled) || now.MaxBump != "minor" || !yes(now.MatchFlavor) || yes(now.IncludePrerelease) {
		t.Errorf("one setting saved alone = %+v, want the others at their defaults: off, flavours matched, no pre-releases", now)
	}

	if now := save(dockhand.PostSettingsSemverRequest{Enabled: was.Enabled, MaxBump: was.MaxBump, MatchFlavor: was.MatchFlavor, IncludePrerelease: was.IncludePrerelease}); yes(now.Enabled) != yes(was.Enabled) || now.MaxBump != was.MaxBump || yes(now.MatchFlavor) != yes(was.MatchFlavor) || yes(now.IncludePrerelease) != yes(was.IncludePrerelease) {
		t.Errorf("the settings for newer versions put back = %+v, want %+v", now, was)
	}
}
