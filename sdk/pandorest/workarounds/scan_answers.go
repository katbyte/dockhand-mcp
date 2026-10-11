package workarounds

import (
	"errors"
	"net/http"
	"slices"

	pandorest "github.com/katbyte/pandorest/importer/workarounds"
	"github.com/katbyte/pandorest/openapi"
)

// scanCounts is how many findings there are by how grave each is, with the fields given beside them.
func scanCounts(more map[string]*openapi.Schema) *openapi.Schema {
	return record(withFields(map[string]*openapi.Schema{"critical": integer(), "high": integer(), "medium": integer(), "low": integer()}, more))
}

// scanResult is one scan of an image by one scanner as the call that makes it answers: what was found, counted by how grave.
func scanResult() *openapi.Schema {
	return record(map[string]*openapi.Schema{
		"imageId":      text(),
		"imageName":    text(),
		"scanner":      said(text(), "grype or trivy"),
		"scannedAt":    text(),
		"scanDuration": said(integer(), "how long the scan took, in milliseconds"),
		"summary":      scanCounts(map[string]*openapi.Schema{"negligible": integer(), "unknown": integer()}),
		"vulnerabilities": listOf(record(map[string]*openapi.Schema{
			"id":           said(text(), "the CVE, or the scanner's own id for what it found"),
			"severity":     said(text(), "critical, high, medium, low, negligible or unknown"),
			"package":      text(),
			"version":      said(text(), "the version of the package the image has"),
			"fixedVersion": said(text(), "the version it is put right in; absent when none is known"),
			"description":  text(),
			"link":         text(),
			"scanner":      text(),
		})),
	})
}

// scanAnswers gives the calls about vulnerability scans what they are asked with and what they answer.
type scanAnswers struct{}

func (scanAnswers) Name() string    { return Service + "-scan-answers" }
func (scanAnswers) Service() string { return Service }

func (scanAnswers) Bug() string {
	return "GET /api/vulnerabilities, its count and its export declare no parameter, and a 1.0.51 server finds nothing without env and narrows by q, severity, image, container and stack, sorted by sort and dir and paged by limit and offset; a finding is declared an object with nothing in it and the list without its summary; GET /api/images/scan declares its result an object with nothing in it; GET /api/settings/scanner declares no answer, and saving them answers the settings without the images the scanners are run from; and POST /api/images/scan and POST /api/vulnerabilities/scan-all declare a stream of events for their only answer, where a caller that asks for JSON alone is answered how the scan went once it has ended"
}

func (scanAnswers) Apply(spec *openapi.Spec) error {
	env := pandorest.Param{Name: "env", In: openapi.InQuery, Type: openapi.TypeInteger, Description: "the environment whose findings are asked for: with none, nothing is found"}
	narrowing := []pandorest.Param{
		env,
		{Name: "q", In: openapi.InQuery, Type: openapi.TypeString, Description: "only findings whose CVE, package, image, container or stack holds this text, whatever its case"},
		{Name: "severity", In: openapi.InQuery, Type: openapi.TypeString, Description: "only findings this grave: critical, high, medium or low, or several with commas between"},
		{Name: "image", In: openapi.InQuery, Type: openapi.TypeString, Description: "only findings of the image by this name, whole, or of several with commas between"},
		{Name: "container", In: openapi.InQuery, Type: openapi.TypeString, Description: "only findings of the image a container by this name was made from"},
		{Name: "stack", In: openapi.InQuery, Type: openapi.TypeString, Description: "only findings of the images a stack's containers were made from"},
		{Name: "sort", In: openapi.InQuery, Type: openapi.TypeString, Description: "what to order by: severity, cve, package, image, installed, fixed, container, stack or scannedAt"},
		{Name: "dir", In: openapi.InQuery, Type: openapi.TypeString, Description: "desc for the order turned round; by severity the gravest come first unless it is"},
	}
	if err := pandorest.AddParameters(spec, []string{"GET /api/vulnerabilities"}, slices.Concat(narrowing, []pandorest.Param{{Name: "limit", In: openapi.InQuery, Type: openapi.TypeInteger, Description: "the most findings to answer; every one unless given, and 0 for the counts alone"}, {Name: "offset", In: openapi.InQuery, Type: openapi.TypeInteger, Description: "how many findings to pass over first"}})...); err != nil {
		return err
	}
	if err := pandorest.AddParameters(spec, []string{"GET /api/vulnerabilities/export"}, narrowing...); err != nil {
		return err
	}
	if err := pandorest.AddParameters(spec, []string{"GET /api/vulnerabilities/count"}, env); err != nil {
		return err
	}

	summary := func() *openapi.Schema {
		return said(scanCounts(map[string]*openapi.Schema{"total": integer(), "imagesScanned": said(integer(), "how many of the images the host has were scanned"), "totalImages": said(integer(), "how many images the host has")}), "the findings counted by how grave, of the ones that matched")
	}
	found, err := answerOf(spec, "/api/vulnerabilities")
	if err != nil {
		return err
	}
	if found.Properties["findings"] == nil || found.Properties["findings"].Items == nil || len(found.Properties["findings"].Items.Properties) > 0 || found.Properties["summary"] != nil {
		return errors.New("GET /api/vulnerabilities no longer declares a finding an object with nothing in it")
	}
	found.Properties["summary"] = summary()
	found.Properties["findings"] = listOf(record(map[string]*openapi.Schema{
		"key":              text(),
		"cve":              text(),
		"package":          text(),
		"severity":         said(text(), "critical, high, medium, low, negligible or unknown"),
		"installedVersion": text(),
		"fixedVersion":     said(text(), "the version it is put right in; empty when none is known"),
		"imageId":          text(),
		"imageName":        text(),
		"description":      text(),
		"link":             text(),
		"scannedAt":        text(),
		"containers":       said(listOf(record(map[string]*openapi.Schema{"id": text(), "name": text()})), "the containers made from the image, as Dockhand last looked, which is up to half a minute ago; absent when there are none"),
		"stacks":           said(listOf(text()), "the stacks those containers belong to; absent when there are none"),
	}))

	counted, err := answerOf(spec, "/api/vulnerabilities/count")
	if err != nil {
		return err
	}
	if counted.Properties["summary"] == nil || len(counted.Properties["summary"].Properties) > 0 {
		return errors.New("GET /api/vulnerabilities/count no longer declares its summary an object with nothing in it")
	}
	counted.Properties["summary"] = summary()

	latest, err := answerOf(spec, "/api/images/scan")
	if err != nil {
		return err
	}
	if latest.Properties["result"] == nil || len(latest.Properties["result"].Properties) > 0 {
		return errors.New("GET /api/images/scan no longer declares its result an object with nothing in it")
	}
	kept := scanResult()
	delete(kept.Properties, "summary")
	latest.Properties["result"] = said(record(withFields(kept.Properties, map[string]*openapi.Schema{
		"id":              integer(),
		"environmentId":   integer(),
		"criticalCount":   integer(),
		"highCount":       integer(),
		"mediumCount":     integer(),
		"lowCount":        integer(),
		"negligibleCount": integer(),
		"unknownCount":    integer(),
		"error":           said(text(), "why the scan failed; null for one that did not"),
		"createdAt":       text(),
	})), "the latest scan kept of the image, by the scanner asked for or by whichever ran last; absent when none is kept")

	settings, err := unanswered(spec, http.MethodGet, "/api/settings/scanner")
	if err != nil {
		return err
	}
	each := func(of func() *openapi.Schema) *openapi.Schema {
		return record(map[string]*openapi.Schema{"grype": of(), "trivy": of()})
	}
	set := func() map[string]*openapi.Schema {
		return map[string]*openapi.Schema{
			"grypeArgs":  said(text(), "what grype is run with, {image} standing for the image"),
			"trivyArgs":  said(text(), "what trivy is run with, {image} standing for the image"),
			"grypeImage": said(text(), "the image grype is run from, which is the same for every environment"),
			"trivyImage": said(text(), "the image trivy is run from, which is the same for every environment"),
		}
	}
	answering(settings, record(map[string]*openapi.Schema{
		"settings":     record(withFields(set(), map[string]*openapi.Schema{"scanner": said(text(), "what the environment's images are scanned with: none, grype, trivy or both")})),
		"defaults":     record(withFields(set(), map[string]*openapi.Schema{"networkMode": text(), "dns": listOf(text())})),
		"availability": said(each(flag), "whether the environment has each scanner's image; absent when settingsOnly is asked"),
		"versions":     said(each(text), "the version each scanner says it is, where Dockhand could tell; null where it could not or the image is not there"),
		"updates": said(each(func() *openapi.Schema {
			return record(map[string]*openapi.Schema{"hasUpdate": flag(), "localDigest": text(), "remoteDigest": text()})
		}), "with checkUpdates: whether the registry has a newer image under the same tag"),
		"newerVersions": said(each(func() *openapi.Schema {
			return record(map[string]*openapi.Schema{"current": text(), "latest": said(text(), "the newest release there is; null when it could not be told")})
		}), "with checkNewerVersions: the newest release of each scanner"),
	}))

	saved, err := answerTo(spec, http.MethodPost, "/api/settings/scanner")
	if err != nil {
		return err
	}
	if saved.Properties["settings"] == nil || saved.Properties["settings"].Properties["scanner"] == nil || saved.Properties["settings"].Properties["grypeImage"] != nil {
		return errors.New("POST /api/settings/scanner no longer declares the settings it answers without the images the scanners are run from")
	}
	saved.Properties["settings"].Properties["grypeImage"] = text()
	saved.Properties["settings"].Properties["trivyImage"] = text()

	scanned, err := unanswered(spec, http.MethodPost, "/api/images/scan")
	if err != nil {
		return err
	}
	answering(scanned, record(map[string]*openapi.Schema{
		"jobId":    said(text(), "the job the scan runs as, for a caller that did not ask for JSON alone"),
		"stage":    said(text(), "complete, or error with why in message"),
		"message":  text(),
		"progress": integer(),
		"result":   said(scanResult(), "the first scanner's scan; absent when the environment scans with nothing"),
		"results":  said(listOf(scanResult()), "one scan for each scanner that ran: none when the environment scans with nothing, which is answered as complete all the same"),
	}))

	everything, err := unanswered(spec, http.MethodPost, "/api/vulnerabilities/scan-all")
	if err != nil {
		return err
	}
	answering(everything, record(map[string]*openapi.Schema{
		"jobId":     said(text(), "the job the scans run as, for a caller that did not ask for JSON alone"),
		"scanned":   said(integer(), "how many images were scanned"),
		"failed":    said(integer(), "how many could not be"),
		"total":     said(integer(), "how many images the environment has"),
		"cancelled": flag(),
		"success":   said(flag(), "false, with why in error, for an environment there is none of or none given: answered with 200 all the same"),
		"error":     text(),
	}))

	return nil
}
