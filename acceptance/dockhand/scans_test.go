//go:build acceptance

package dockhand_test

import (
	"encoding/json"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/go-kt/mcp/acctest"
	"github.com/katbyte/pandorest/client"
)

// scannerOf is how one environment is scanned, as the server has it: with env 0, how those with no setting of their own are.
func scannerOf(t *testing.T, c *dockhand.Client, env int) *dockhand.GetSettingsScannerAnswer {
	t.Helper()

	options := dockhand.GetSettingsScannerOptions{}
	if env != 0 {
		options.Env = dockhand.EnvironmentID(env)
	}
	set, err := c.GetSettingsScanner(t.Context(), options)
	if err != nil || set.Answer == nil || set.Answer.Settings == nil {
		t.Fatalf("how environment %d is scanned = %+v, %v", env, set.Answer, err)
	}

	return set.Answer
}

// written is what a download holds, read to its end.
func written(t *testing.T, body io.ReadCloser) string {
	t.Helper()
	defer func() { _ = body.Close() }()

	text, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}

	return string(text)
}

// An environment is set to scan its images, one image is scanned both ways a scan is answered, what was found is kept and read back, narrowed,
// counted and written out, every image is scanned at once, and the scanner is taken off the host again. The scanner is run from a mirror and the
// image scanned is one old enough to have plenty wrong with it, which is what the assertions about how grave the findings are stand on.
func TestImagesScannedFindingsKeptAndTheScannerRemoved(t *testing.T) {
	c := dock(t)
	if world.Scanner == "" || world.Scanned == "" {
		t.Skip("the environment names no scanner and no image to scan: .scripts/testenv.sh up names both")
	}
	const host, other = testenv.Alpha, testenv.Beta
	env, second := world.Env(host), world.Env(other)
	at := dockhand.PostImagesScanOptions{Env: env}
	box := named(t, "box")
	ctx := noCancel(t)

	// how things stood, to put back: the image the scanner is run from is one for every environment
	stood := scannerOf(t, c, 0).Settings
	restore := func() {
		_, _ = c.DeleteSettingsScanner(ctx, dockhand.DeleteSettingsScannerOptions{RemoveImages: new(true), Env: env})
		_, _ = c.PostSettingsScanner(ctx, dockhand.PostSettingsScannerRequest{EnvId: env, Scanner: "none", GrypeArgs: stood.GrypeArgs})
		_, _ = c.PostSettingsScanner(ctx, dockhand.PostSettingsScannerRequest{GrypeImage: stood.GrypeImage})
		_, _ = world.Docker(ctx, host, "rm", "-f", box)
	}
	restore()
	t.Cleanup(restore)
	without(t, host, world.Scanned)
	if _, err := world.Docker(t.Context(), host, "pull", "-q", world.Scanned); err != nil {
		t.Fatalf("pulling %s to %s: %v", world.Scanned, host, err)
	}
	if _, err := world.Docker(t.Context(), host, "run", "-d", "--name", box, world.Scanned, "sleep", "100000"); err != nil {
		t.Fatalf("starting %s on %s: %v", box, host, err)
	}

	// set: what scans is an environment's own, the image it is run from is everyone's, and only what is sent changes
	if _, err := c.PostSettingsScanner(t.Context(), dockhand.PostSettingsScannerRequest{EnvId: env, Scanner: "nessus"}); client.StatusCode(err) != 400 {
		t.Errorf("setting a scanner there is none of = %v", err)
	}
	if set, err := c.PostSettingsScanner(t.Context(), dockhand.PostSettingsScannerRequest{EnvId: env, GrypeImage: world.Scanner}); err != nil || !set.Answer.Success || set.Answer.Settings.GrypeImage != world.Scanner || set.Answer.Settings.Scanner != "none" {
		t.Fatalf("naming the image the scanner is run from = %+v, %v", set.Answer, err)
	}
	if set, err := c.PostSettingsScanner(t.Context(), dockhand.PostSettingsScannerRequest{EnvId: env, Scanner: "grype", GrypeArgs: "-o json {image}"}); err != nil || set.Answer.Settings.Scanner != "grype" || set.Answer.Settings.GrypeArgs != "-o json {image}" || set.Answer.Settings.GrypeImage != world.Scanner {
		t.Fatalf("setting what scans = %+v, %v", set.Answer, err)
	}
	mine, theirs := scannerOf(t, c, int(env)), scannerOf(t, c, int(second))
	if mine.Settings.Scanner != "grype" || mine.Settings.GrypeArgs != "-o json {image}" || theirs.Settings.Scanner != "none" || theirs.Settings.GrypeArgs != stood.GrypeArgs || theirs.Settings.GrypeImage != world.Scanner || mine.Settings.TrivyArgs != stood.TrivyArgs {
		t.Errorf("the environment set is scanned as %+v, and another as %+v", mine.Settings, theirs.Settings)
	}
	// the host is looked at for the scanners it has unless only the settings are asked for
	if mine.Availability == nil || mine.Availability.Grype || mine.Availability.Trivy || mine.Defaults == nil {
		t.Errorf("a host with no scanner on it yet says %+v", mine.Availability)
	}
	if only, err := c.GetSettingsScanner(t.Context(), dockhand.GetSettingsScannerOptions{Env: env, SettingsOnly: new(true)}); err != nil || only.Answer.Availability != nil || only.Answer.Settings.Scanner != "grype" {
		t.Errorf("the settings alone = %+v, %v", only.Answer, err)
	}

	// an environment that scans with nothing answers a scan as complete, with nothing found and no scanner run
	nothing, err := c.PostImagesScan(t.Context(), dockhand.PostImagesScanRequest{ImageName: world.Image}, dockhand.PostImagesScanOptions{Env: second})
	if err != nil || nothing.Answer.Stage != "complete" || len(nothing.Answer.Results) != 0 || nothing.Answer.Result != nil || nothing.Answer.JobId != "" {
		t.Errorf("a scan where nothing scans = %+v, %v", nothing.Answer, err)
	}
	if _, err := c.PostImagesScan(t.Context(), dockhand.PostImagesScanRequest{}, at); client.StatusCode(err) != 400 {
		t.Errorf("a scan of no image = %v", err)
	}

	// scanned as a job: the scanner's image is pulled and its database downloaded on the way, and the job's result is the scan
	started, err := c.PostImagesScan(t.Context(), dockhand.PostImagesScanRequest{ImageName: world.Scanned}, dockhand.PostImagesScanOptions{Accept: dockhand.AcceptJob, Env: env})
	if err != nil || started.Answer.JobId == "" || started.Answer.Stage != "" {
		t.Fatalf("starting a scan = %+v, %v", started.Answer, err)
	}
	job := ended(t, started.Answer.JobId)
	var first dockhand.PostImagesScanAnswer
	if raw, err := json.Marshal(job.Result); err != nil || json.Unmarshal(raw, &first) != nil || job.Status != "done" || first.Stage != "complete" || len(first.Results) != 1 || first.Result == nil || len(job.Lines) < 3 {
		t.Fatalf("the scan as a job ended %s with %+v", job.Status, job.Result)
	}
	scan := first.Results[0]
	if scan.Scanner != "grype" || scan.ImageName != world.Scanned || !strings.HasPrefix(scan.ImageId, "sha256:") || scan.ScannedAt.IsZero() || scan.ScanDuration <= 0 || scan.Summary == nil || scan.Summary.Critical == 0 || scan.Summary.High == 0 || len(scan.Vulnerabilities) < scan.Summary.Critical+scan.Summary.High {
		t.Fatalf("what the scan found = %+v with %d findings", scan.Summary, len(scan.Vulnerabilities))
	}
	for _, v := range scan.Vulnerabilities {
		if v.Id == "" || v.Package == "" || v.Version == "" || !slices.Contains([]string{"critical", "high", "medium", "low", "negligible", "unknown"}, strings.ToLower(string(v.Severity))) || v.Scanner != "grype" {
			t.Fatalf("a finding = %+v", v)
		}
	}
	if has := scannerOf(t, c, int(env)); !has.Availability.Grype || has.Availability.Trivy {
		t.Errorf("after a scan the host says it has %+v", has.Availability)
	}

	// scanned again and waited for: answered with the scan itself, which is from then on the one the reads give
	again, err := c.PostImagesScan(t.Context(), dockhand.PostImagesScanRequest{ImageName: world.Scanned}, at)
	if err != nil || again.Answer.JobId != "" || again.Answer.Stage != "complete" || len(again.Answer.Results) != 1 || len(again.Answer.Results[0].Vulnerabilities) != len(scan.Vulnerabilities) || again.Answer.Results[0].ScannedAt == scan.ScannedAt {
		t.Fatalf("a scan waited for = %+v, %v", again.Answer, err)
	}
	// a scan that fails is answered with 200 and says so in its stage
	if failed, err := c.PostImagesScan(t.Context(), dockhand.PostImagesScanRequest{ImageName: "it/no-such-image:1"}, at); err != nil || failed.Answer.Stage != "error" || !strings.Contains(failed.Answer.Message, "Scan failed") || len(failed.Answer.Results) != 0 {
		t.Errorf("a scan of an image there is none of = %+v, %v", failed.Answer, err)
	}

	// kept: the latest scan of the image, by its name or by one scanner, and nothing for an image never scanned
	kept, err := c.GetImagesScan(t.Context(), dockhand.GetImagesScanOptions{Env: env, Image: world.Scanned})
	if err != nil || !kept.Answer.Found || kept.Answer.Result == nil || kept.Answer.Result.Scanner != "grype" || kept.Answer.Result.ScannedAt != again.Answer.Results[0].ScannedAt || kept.Answer.Result.CriticalCount != scan.Summary.Critical || kept.Answer.Result.HighCount != scan.Summary.High || len(kept.Answer.Result.Vulnerabilities) != len(scan.Vulnerabilities) || kept.Answer.Result.EnvironmentId != env || kept.Answer.Result.Error != "" {
		t.Fatalf("the scan kept = %+v, %v", kept.Answer.Result, err)
	}
	if byID, err := c.GetImagesScan(t.Context(), dockhand.GetImagesScanOptions{Env: env, Image: scan.ImageId, Scanner: "grype"}); err != nil || !byID.Answer.Found {
		t.Errorf("the scan kept, asked for by the image's id = %+v, %v", byID.Answer, err)
	}
	for what, asked := range map[string]dockhand.GetImagesScanOptions{"by a scanner that never ran": {Env: env, Image: world.Scanned, Scanner: "trivy"}, "of an image never scanned": {Env: env, Image: "it/no-such-image:1"}} {
		if none, err := c.GetImagesScan(t.Context(), asked); err != nil || none.Answer.Found || none.Answer.Result != nil {
			t.Errorf("the scan kept %s = %+v, %v", what, none.Answer, err)
		}
	}
	if _, err := c.GetImagesScan(t.Context(), dockhand.GetImagesScanOptions{Env: env}); client.StatusCode(err) != 400 {
		t.Errorf("the scan kept of no image = %v", err)
	}

	// the findings of the environment: every one unless cut, the gravest first when asked, and counted by how grave
	find := func(options dockhand.GetVulnerabilitiesOptions) *dockhand.GetVulnerabilitiesAnswer {
		options.Env = env
		found, failed := c.GetVulnerabilities(t.Context(), options)
		if failed != nil || found.Answer.Summary == nil {
			t.Fatalf("the findings asked with %+v = %+v, %v", options, found.Answer, failed)
		}

		return found.Answer
	}
	every := find(dockhand.GetVulnerabilitiesOptions{Image: world.Scanned})
	if every.Total != len(every.Findings) || every.Total == 0 || every.Summary.Total != every.Total || every.Summary.Critical != scan.Summary.Critical || every.Summary.High != scan.Summary.High || every.Summary.ImagesScanned < 1 || every.Summary.TotalImages < every.Summary.ImagesScanned {
		t.Fatalf("every finding of the image = %d of %d, counted %+v", len(every.Findings), every.Total, every.Summary)
	}
	if f := every.Findings[0]; f.Cve == "" || f.Package == "" || f.InstalledVersion == "" || f.Severity == "" || f.ImageName != world.Scanned || f.ImageId != scan.ImageId || f.ScannedAt.IsZero() || f.Key == "" {
		t.Errorf("a finding as kept = %+v", f)
	}
	gravest := find(dockhand.GetVulnerabilitiesOptions{Image: world.Scanned, Sort: "severity", Limit: new(3), Offset: new(1)})
	if len(gravest.Findings) != 3 || gravest.Total != every.Total || gravest.Findings[0].Severity != "critical" {
		t.Errorf("three of the gravest = %+v", gravest.Findings)
	}
	if mildest := find(dockhand.GetVulnerabilitiesOptions{Image: world.Scanned, Sort: "severity", Dir: "desc", Limit: new(1)}); len(mildest.Findings) != 1 || mildest.Findings[0].Severity == "critical" {
		t.Errorf("the order turned round begins with %+v", mildest.Findings)
	}
	if counts := find(dockhand.GetVulnerabilitiesOptions{Image: world.Scanned, Limit: new(0)}); len(counts.Findings) != 0 || counts.Total != every.Total {
		t.Errorf("the counts alone = %d findings of %d", len(counts.Findings), counts.Total)
	}
	// narrowed: by how grave, by a part of anything, and by an image that has no findings
	grave := find(dockhand.GetVulnerabilitiesOptions{Image: world.Scanned, Severity: "critical,low"})
	if grave.Total != scan.Summary.Critical+scan.Summary.Low || grave.Summary.High != 0 || slices.ContainsFunc(grave.Findings, func(f dockhand.GetVulnerabilitiesAnswerFindings) bool {
		return f.Severity != "critical" && f.Severity != "low"
	}) {
		t.Errorf("only the critical and the low = %d, counted %+v", grave.Total, grave.Summary)
	}
	part := strings.ToUpper(every.Findings[0].Package)
	if some := find(dockhand.GetVulnerabilitiesOptions{Image: world.Scanned, Q: part}); some.Total == 0 || some.Total >= every.Total || slices.ContainsFunc(some.Findings, func(f dockhand.GetVulnerabilitiesAnswerFindings) bool {
		return !strings.Contains(strings.ToUpper(f.Cve+" "+f.Package+" "+f.ImageName), part)
	}) {
		t.Errorf("only what holds %q = %d of %d", part, some.Total, every.Total)
	}
	if none := find(dockhand.GetVulnerabilitiesOptions{Image: "it/no-such-image:1"}); none.Total != 0 || len(none.Findings) != 0 {
		t.Errorf("the findings of an image never scanned = %d", none.Total)
	}
	// with no environment nothing is found, and an environment there is none of is no error
	if lost, err := c.GetVulnerabilities(t.Context(), dockhand.GetVulnerabilitiesOptions{}); err != nil || lost.Answer.Total != 0 {
		t.Errorf("the findings of no environment = %+v, %v", lost.Answer, err)
	}
	nowhere := 99999
	if lost, err := c.GetVulnerabilities(t.Context(), dockhand.GetVulnerabilitiesOptions{Env: dockhand.EnvironmentID(nowhere)}); err != nil || lost.Answer.Total != 0 {
		t.Errorf("the findings of an environment there is none of = %+v, %v", lost.Answer, err)
	}
	// a finding names the containers made from its image as Dockhand last looked, which is up to half a minute ago
	if !acctest.EventuallyWithin(45*time.Second, func() bool {
		worn := find(dockhand.GetVulnerabilitiesOptions{Container: box, Limit: new(1)})

		return worn.Total == every.Total && len(worn.Findings) == 1 && slices.ContainsFunc(worn.Findings[0].Containers, func(k dockhand.GetVulnerabilitiesAnswerFindingsContainers) bool { return k.Name == box && k.Id != "" })
	}) {
		t.Errorf("the findings were never narrowed to the container %s made from the image", box)
	}

	counted, err := c.GetVulnerabilitiesCount(t.Context(), dockhand.GetVulnerabilitiesCountOptions{Env: env})
	if err != nil || counted.Answer.Total < every.Total || counted.Answer.Summary == nil || counted.Answer.Summary.Critical < scan.Summary.Critical || counted.Answer.Options == nil || !slices.Contains(counted.Answer.Options.Images, world.Scanned) || !slices.Contains(counted.Answer.Options.Containers, box) {
		t.Errorf("the count = %+v with %+v, %v", counted.Answer, counted.Answer.Options, err)
	}

	// written out: the environment's as a sheet, narrowed as the list is, and one image's latest scan as SARIF, asked for by the image's id
	sheet, err := c.GetVulnerabilitiesExport(t.Context(), dockhand.GetVulnerabilitiesExportOptions{Env: env, Format: "csv", Severity: "critical", Image: world.Scanned})
	if err != nil {
		t.Fatal(err)
	}
	if rows := strings.Split(strings.TrimSpace(written(t, sheet.HttpResponse.Body)), "\n"); !strings.HasPrefix(rows[0], "CVE,Severity,Package,Installed Version,Fixed Version,Image,Containers,Stacks,Scanned,Description,Link") || len(rows) < 1+scan.Summary.Critical || !strings.Contains(rows[1], ",critical,") {
		t.Errorf("the sheet begins %q and has %d rows", rows[0], len(rows))
	}
	report, err := c.GetImagesScanExport(t.Context(), dockhand.GetImagesScanExportOptions{Env: env, ImageId: scan.ImageId, Format: "sarif"})
	if err != nil {
		t.Fatal(err)
	}
	var sarif struct {
		Version string `json:"version"`
		Runs    []struct {
			Results []any `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal([]byte(written(t, report.HttpResponse.Body)), &sarif); err != nil || sarif.Version != "2.1.0" || len(sarif.Runs) != 1 || len(sarif.Runs[0].Results) != len(scan.Vulnerabilities) {
		t.Errorf("the report = version %q with %d runs, %v", sarif.Version, len(sarif.Runs), err)
	}

	// every image of the environment at once, as a job that says how each went
	all, err := c.PostVulnerabilitiesScanAll(t.Context(), dockhand.PostVulnerabilitiesScanAllOptions{Accept: dockhand.AcceptJob, Env: env})
	if err != nil || all.Answer.JobId == "" {
		t.Fatalf("starting a scan of everything = %+v, %v", all.Answer, err)
	}
	job = ended(t, all.Answer.JobId)
	images := 0
	for _, line := range job.Lines {
		if line.Event == "scanned" && line.Data["imageName"] != "" && is(line.Data["ok"], true) {
			images++
		}
	}
	if job.Status != "done" || job.Result["scanned"] != float64(images) || images < 2 || job.Result["failed"] != float64(0) || job.Result["total"] != float64(images) || !is(job.Result["cancelled"], false) {
		t.Errorf("a scan of everything ended %s with %v after %d images", job.Status, job.Result, images)
	}
	// an environment there is none of, and none at all, are refused in words with a 200
	for what, asked := range map[string]dockhand.PostVulnerabilitiesScanAllOptions{"an environment there is none of": {Env: dockhand.EnvironmentID(nowhere)}, "no environment": {}} {
		if refused, err := c.PostVulnerabilitiesScanAll(t.Context(), asked); err != nil || refused.Answer.Success || refused.Answer.Error == "" || refused.Answer.Scanned != 0 {
			t.Errorf("a scan of everything on %s = %+v, %v", what, refused.Answer, err)
		}
	}

	// the scanner taken off the host: its image and the volume its database is in, and the scans stay
	for what, asked := range map[string]dockhand.DeleteSettingsScannerOptions{"without saying to remove": {Env: env}, "from no environment": {RemoveImages: new(true)}} {
		if _, err := c.DeleteSettingsScanner(t.Context(), asked); client.StatusCode(err) != 400 {
			t.Errorf("removing the scanners %s = %v", what, err)
		}
	}
	if _, err := c.DeleteSettingsScanner(t.Context(), dockhand.DeleteSettingsScannerOptions{RemoveImages: new(true), Env: dockhand.EnvironmentID(nowhere)}); !client.IsNotFound(err) {
		t.Errorf("removing the scanners from an environment there is none of = %v", err)
	}
	volumes := func() string {
		listed, err := world.Docker(t.Context(), host, "volume", "ls", "--format", "{{.Name}}")
		if err != nil {
			t.Fatal(err)
		}

		return listed
	}
	if !strings.Contains(volumes(), "dockhand-grype-db") {
		t.Errorf("the scanner's database is in none of the host's volumes: %s", volumes())
	}
	if removed, err := c.DeleteSettingsScanner(t.Context(), dockhand.DeleteSettingsScannerOptions{RemoveImages: new(true), Env: env, Scanner: "grype"}); err != nil || !removed.Answer.Success || !slices.Equal(removed.Answer.Removed, []string{"grype"}) {
		t.Errorf("removing the scanner = %+v, %v", removed.Answer, err)
	}
	if has := scannerOf(t, c, int(env)); has.Availability.Grype || has.Settings.Scanner != "grype" || strings.Contains(volumes(), "dockhand-grype-db") {
		t.Errorf("with the scanner removed the host has %+v, is set to %s, and its volumes are %s", has.Availability, has.Settings.Scanner, volumes())
	}
	if removed, err := c.DeleteSettingsScanner(t.Context(), dockhand.DeleteSettingsScannerOptions{RemoveImages: new(true), Env: env}); err != nil || len(removed.Answer.Removed) != 0 {
		t.Errorf("removing the scanners again = %+v, %v", removed.Answer, err)
	}
	if still := find(dockhand.GetVulnerabilitiesOptions{Image: world.Scanned, Limit: new(0)}); still.Total != every.Total {
		t.Errorf("with the scanner removed the findings kept are %d of %d", still.Total, every.Total)
	}
	if cleared, err := c.DeleteSettingsScannerCache(t.Context()); err != nil || !cleared.Answer.Success || len(cleared.Answer.SkippedEnvironments) != 0 {
		t.Errorf("clearing the scanners' databases = %+v, %v", cleared.Answer, err)
	}
}
