package workarounds

import (
	"errors"
	"fmt"
	"net/http"

	pandorest "github.com/katbyte/pandorest/importer/workarounds"
	"github.com/katbyte/pandorest/openapi"
)

// backupDestination is a place backups are kept, as a server sends one wherever it sends one: never with its password.
func backupDestination() *openapi.Schema {
	return record(map[string]*openapi.Schema{
		"id":               integer(),
		"name":             text(),
		"repository":       said(text(), "where the backups are kept, as restic names it: a folder's path, or an address that opens with rest:, s3:, b2:, azure: or gs:"),
		"envVars":          said(byName(text()), "what the store is signed in to with, by the names restic reads them under; sent where one destination is read, made or changed, and left out of the list"),
		"flags":            said(text(), "the flags of a backup and of a restore together, as JSON written out as text; null for none. Sent, it is taken as the flags of a backup"),
		"backupFlags":      said(text(), "more that restic is given for a backup; left out of the list"),
		"restoreFlags":     said(text(), "more that restic is given for a restore; left out of the list"),
		"hostPath":         text(),
		"policies":         said(text(), "how the store is looked after by itself, as JSON written out as text: whether and when it is pruned, checked and verified"),
		"lastTestAt":       text(),
		"lastTestStatus":   said(text(), "how the last try of it went: success, failed or needs_init; null for one never tried"),
		"lastTestError":    text(),
		"createdAt":        text(),
		"updatedAt":        text(),
		"hasCacert":        flag(),
		"hasTlsClientCert": flag(),
	})
}

// backupConfig is what is backed up, where to and when, as a server sends one wherever it sends one.
func backupConfig() *openapi.Schema {
	return record(map[string]*openapi.Schema{
		"id":               integer(),
		"type":             said(text(), "container or stack, as it was given: any other word is kept and then backs up nothing"),
		"targetName":       text(),
		"environmentId":    said(integer(), "the environment the container or stack is on; null for one saved with none, which cannot be run"),
		"destinationId":    integer(),
		"enabled":          flag(),
		"allVolumes":       flag(),
		"selectedVolumes":  said(text(), "the volumes to back up when not all of them, as a JSON list written out as text; null for none"),
		"stopBeforeBackup": flag(),
		"schedule":         said(text(), "when it runs by itself, as cron; null or empty for one that only runs when asked"),
		"retention":        said(text(), "how many snapshots are kept, as JSON written out as text: keepLast, keepDaily, keepWeekly, keepMonthly, keepYearly; null to keep every one"),
		"options":          said(text(), "more of how it is backed up, as JSON written out as text; null for none"),
		"tags":             said(text(), "as a JSON list written out as text; null for none"),
		"lastBackupAt":     text(),
		"lastBackupStatus": said(text(), "success, warning, skipped or failed; null for one never run"),
		"createdAt":        text(),
		"updatedAt":        text(),
	})
}

// backupRun is how a backup went, or the job it runs as.
func backupRun() *openapi.Schema {
	return record(map[string]*openapi.Schema{
		"jobId":       said(text(), "the job the backup runs as, for a caller that asked for one: GET /api/jobs/{id} then answers the rest as its result"),
		"status":      said(text(), "success, warning, skipped or error"),
		"executionId": said(integer(), "the run, among those GET /api/schedules/executions lists"),
		"snapshotId":  text(),
		"summary": record(map[string]*openapi.Schema{
			"snapshotId":          text(),
			"filesNew":            integer(),
			"filesChanged":        integer(),
			"filesUnmodified":     integer(),
			"dataAdded":           said(integer(), "bytes the store grew by"),
			"totalBytesProcessed": integer(),
			"totalFilesProcessed": integer(),
		}),
		"retention": said(text(), "applied when older snapshots were looked through for ones to drop, none when the backup keeps every one"),
		"code":      said(text(), "for one that failed, the kind of failure: VALIDATION, RESTIC or UNKNOWN"),
		"error":     text(),
		"reason":    said(text(), "for one that was skipped, why: Backup cancelled for one that was stopped"),
	})
}

// backupSnapshotEntry is one file or folder inside a snapshot.
func backupSnapshotEntry() *openapi.Schema {
	return record(map[string]*openapi.Schema{
		"name":        text(),
		"type":        said(text(), "dir or file, in restic's words"),
		"path":        text(),
		"uid":         integer(),
		"gid":         integer(),
		"size":        said(integer(), "bytes, for a file"),
		"mode":        integer(),
		"permissions": text(),
		"mtime":       text(),
		"atime":       text(),
		"ctime":       text(),
		"inode":       integer(),
	})
}

// backupRestoreTargets is where a restore would write, as the preview works it out.
func backupRestoreTargets() *openapi.Schema {
	return record(map[string]*openapi.Schema{
		"volumes": listOf(record(map[string]*openapi.Schema{
			"key":     text(),
			"type":    said(text(), "volume or bind"),
			"target":  text(),
			"origin":  said(text(), "how the target was come by: in-place-metadata, loose-files or clone"),
			"hasData": said(text(), "what is there now: has-data, empty or missing"),
		})),
		"stackFiles": said(record(map[string]*openapi.Schema{
			"targetDir": text(),
			"willWrite": flag(),
			"hasData":   text(),
		}), "where a stack's files would be written; null for a snapshot that holds none"),
		"unresolved": listOf(record(map[string]*openapi.Schema{"key": text(), "reason": text()})),
		"helperOk":   flag(),
	})
}

// backupStackFolder is where a stack's files are on its host, as the two calls that look for it answer.
func backupStackFolder() *openapi.Schema {
	return record(map[string]*openapi.Schema{
		"kind":          said(text(), "candidate or listed for a folder that was found, tar for a stack only its compose file is known of, unknown or helper-failed with the reason"),
		"hostPath":      text(),
		"localStackDir": text(),
		"composeFile":   text(),
		"entries":       listOf(record(map[string]*openapi.Schema{"name": text(), "type": text(), "size": integer()})),
		"reason":        text(),
	})
}

// backupAnswers gives the backup calls the answers a server sends, where the document declares none but the instance's, and puts right the two
// request fields it declares as something the server does not take.
type backupAnswers struct{}

func (backupAnswers) Name() string    { return Service + "-backup-answers" }
func (backupAnswers) Service() string { return Service }

func (backupAnswers) Bug() string {
	return "of the 31 backup calls only GET /api/backup/instance declares what it answers; the others answer a destination, a configuration, a snapshot, what is in one, how a run or a restore went, and a plain success, each as a 1.0.51 server with FEAT_BACKUPS_ENABLED=true sends it. POST /api/backup/restore/preview declares volumeDestinations as an object, and the server takes a list and fails on an object; POST /api/backup/restore declares its entries as objects of nothing, and the server wants the volume, a kind of volume or path, and the target"
}

// answers gives one call's answer of one status a JSON shape, and refuses a call the document has since given one.
func answers(spec *openapi.Spec, method, path, status string, shape *openapi.Schema) error {
	op, err := pandorest.Operation(spec, method, path)
	if err != nil {
		return err
	}
	answered := op.Responses[status]
	if answered == nil {
		return fmt.Errorf("%s %s no longer declares a %s", method, path, status)
	}
	if media := answered.Content[answersJSON]; media != nil && media.Schema != nil {
		return fmt.Errorf("%s %s now declares what its %s answers", method, path, status)
	}
	answered.Content = map[string]*openapi.MediaType{answersJSON: {Schema: shape}}

	return nil
}

func (backupAnswers) Apply(spec *openapi.Spec) error {
	done := func() *openapi.Schema { return record(map[string]*openapi.Schema{"success": flag(), "error": text()}) }
	stopped := func() *openapi.Schema {
		return record(map[string]*openapi.Schema{"success": flag(), "stopped": said(flag(), "whether anything was running to stop")})
	}
	tried := func() *openapi.Schema {
		return record(map[string]*openapi.Schema{
			"success":   flag(),
			"status":    said(text(), "for a saved destination: success, failed or needs_init"),
			"needsInit": said(flag(), "nothing answered as a repository there: one not made yet, and one that could not be reached, read alike"),
			"error":     text(),
		})
	}
	ran := func(more map[string]*openapi.Schema) *openapi.Schema {
		return record(withFields(map[string]*openapi.Schema{
			"jobId":   said(text(), "the job the work runs as, for a caller that asked for one"),
			"success": flag(),
			"output":  said(text(), "what restic printed"),
			"error":   text(),
		}, more))
	}

	for _, call := range []struct {
		method, path, status string
		shape                *openapi.Schema
	}{
		{http.MethodGet, "/api/backup/destinations", "200", listOf(backupDestination())},
		{http.MethodPost, "/api/backup/destinations", "201", backupDestination()},
		{http.MethodGet, "/api/backup/destinations/{id}", "200", backupDestination()},
		{http.MethodPut, "/api/backup/destinations/{id}", "200", backupDestination()},
		{http.MethodDelete, "/api/backup/destinations/{id}", "200", done()},
		{http.MethodPost, "/api/backup/destinations/test", "200", tried()},
		{http.MethodPost, "/api/backup/destinations/{id}/test", "200", tried()},
		{http.MethodPost, "/api/backup/destinations/{id}/init", "200", record(map[string]*openapi.Schema{"success": flag(), "message": text(), "error": text()})},
		{http.MethodPost, "/api/backup/destinations/{id}/rotate-key", "200", done()},
		{http.MethodPost, "/api/backup/destinations/{id}/task", "200", ran(map[string]*openapi.Schema{"stats": said(record(map[string]*openapi.Schema{"totalSize": integer(), "totalFiles": integer(), "snapshots": integer()}), "for the stats task: what the store holds")})},
		{http.MethodPost, "/api/backup/destinations/{id}/verify", "200", ran(nil)},
		{http.MethodGet, "/api/backup/configs", "200", listOf(backupConfig())},
		{http.MethodPost, "/api/backup/configs", "201", backupConfig()},
		{http.MethodGet, "/api/backup/configs/{id}", "200", backupConfig()},
		{http.MethodPut, "/api/backup/configs/{id}", "200", backupConfig()},
		{http.MethodDelete, "/api/backup/configs/{id}", "200", record(map[string]*openapi.Schema{"success": flag(), "snapshots": said(record(map[string]*openapi.Schema{"deleted": integer(), "skipped": integer(), "error": text()}), "with deleteSnapshots: how many of its snapshots were removed")})},
		{http.MethodPost, "/api/backup/configs/{id}/run", "200", backupRun()},
		{http.MethodPost, "/api/backup/configs/{id}/stop", "200", stopped()},
		{http.MethodGet, "/api/backup/snapshots", "200", listOf(record(map[string]*openapi.Schema{
			"id":       text(),
			"shortId":  text(),
			"time":     said(text(), "when it was taken, as restic writes it"),
			"hostname": text(),
			"tags":     said(listOf(text()), "what Dockhand marked it with: its own instance, the configuration, the environment, container or stack, and the name"),
			"paths":    listOf(text()),
		}))},
		{http.MethodDelete, "/api/backup/snapshots/{id}", "200", done()},
		{http.MethodPost, "/api/backup/snapshots/batch-delete", "200", record(map[string]*openapi.Schema{"success": flag(), "deleted": listOf(text()), "skipped": said(listOf(text()), "the ones that are not this Dockhand's, or are not there")})},
		{http.MethodGet, "/api/backup/snapshots/diff", "200", record(map[string]*openapi.Schema{"added": listOf(text()), "removed": listOf(text()), "modified": listOf(text()), "metadataChanged": listOf(text()), "raw": said(text(), "what restic printed, a JSON line for each change"), "error": text()})},
		{http.MethodGet, "/api/backup/snapshots/{id}/browse", "200", record(map[string]*openapi.Schema{"entries": listOf(backupSnapshotEntry()), "path": text(), "error": text()})},
		{http.MethodGet, "/api/backup/snapshots/{id}/metadata", "200", record(map[string]*openapi.Schema{
			"version":       integer(),
			"type":          text(),
			"targetName":    text(),
			"environmentId": integer(),
			"backupTime":    text(),
			"volumes":       listOf(record(map[string]*openapi.Schema{"key": text(), "name": text(), "source": text(), "type": text()})),
			"hasStackFiles": flag(),
			"container":     said(&openapi.Schema{Type: openapi.TypeObject}, "for a container: Docker's own record of it as it was, with its variables and labels taken out"),
			"stack": said(record(map[string]*openapi.Schema{
				"composeFileName":  text(),
				"fileList":         listOf(record(map[string]*openapi.Schema{"path": text(), "bytes": integer()})),
				"excludedBindDirs": listOf(text()),
				"secretKeys":       said(listOf(text()), "the names of the stack's secrets, never their values"),
			}), "for a stack: the files that were taken with it"),
			"error": text(),
		})},
		{http.MethodPost, "/api/backup/restore", "200", record(map[string]*openapi.Schema{
			"jobId":           said(text(), "the job the restore runs as, for a caller that asked for one"),
			"status":          said(text(), "success, warning or error"),
			"executionId":     integer(),
			"restoredVolumes": listOf(text()),
			"targetPath":      text(),
			"postRestore":     said(record(map[string]*openapi.Schema{"action": said(text(), "none, start or recreate"), "ok": flag(), "error": text()}), "what was done with the container or stack once its data was back"),
			"code":            text(),
			"error":           text(),
		})},
		{http.MethodPost, "/api/backup/restore/preview", "200", record(map[string]*openapi.Schema{
			"snapshotId":          text(),
			"volumes":             listOf(text()),
			"volumeTypes":         byName(text()),
			"volumeSources":       byName(text()),
			"backupTime":          text(),
			"sourceEnvironmentId": integer(),
			"hasMetadata":         flag(),
			"hasStackFiles":       flag(),
			"sourceSecretKeys":    listOf(text()),
			"targets":             said(backupRestoreTargets(), "with includeTargets: where each volume and the stack's files would be written"),
			"error":               text(),
		})},
		{http.MethodPost, "/api/backup/restore/stop", "200", stopped()},
		{http.MethodGet, "/api/backup/stack-path", "200", backupStackFolder()},
		{http.MethodGet, "/api/backup/stack-dir-listing", "200", backupStackFolder()},
	} {
		if err := answers(spec, call.method, call.path, call.status, call.shape); err != nil {
			return err
		}
	}

	preview, err := jsonBody(spec, http.MethodPost, "/api/backup/restore/preview")
	if err != nil {
		return err
	}
	if where := preview.Properties["volumeDestinations"]; where == nil || where.Type != openapi.TypeObject {
		return errors.New("POST /api/backup/restore/preview no longer declares volumeDestinations as an object")
	}
	preview.Properties["volumeDestinations"] = said(listOf(record(map[string]*openapi.Schema{"volume": text(), "target": text()})), "where a volume of the snapshot is to go: the volume as the snapshot names it, and the volume or the path on the host to put it in")

	restore, err := jsonBody(spec, http.MethodPost, "/api/backup/restore")
	if err != nil {
		return err
	}
	if where := restore.Properties["volumeDestinations"]; where == nil || where.Items == nil || len(where.Items.Properties) != 0 {
		return errors.New("POST /api/backup/restore no longer declares volumeDestinations as a list of objects of nothing")
	}
	restore.Properties["volumeDestinations"] = said(listOf(record(map[string]*openapi.Schema{"volume": text(), "kind": said(text(), "volume or path"), "target": text()})), "where a volume of the snapshot is to go: the volume as the snapshot names it, whether the target is a volume or a path on the host, and the target")

	return nil
}
