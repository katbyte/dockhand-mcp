package workarounds

import pandorest "github.com/katbyte/pandorest/importer/workarounds"

// enums is one group of value sets: the values a real 1.0.51 server answered for a field the document declares plain text, read off what it answered
// both live suites. A field Dockhand stores as it is sent (a config set's restart policy) and one whose words are Docker's (a network's driver) are
// left as text; api-defs/README.md says which and why.
func enums(group string, entries map[string]pandorest.Enum) pandorest.Workaround {
	return pandorest.UndeclaredEnums(pandorest.About{Name: Service + "-enums-" + group, Service: Service, Bug: "values drawn from a fixed set are declared plain strings"}, entries)
}

// dates are the fields that are a date and time, every one of which the document declares plain text, and the one that is a number: the moment a
// container's figures were taken, in milliseconds. When a file inside a container or a volume was changed (modified) is left as text: it is written
// with no zone, in whatever zone the container keeps, so a date read from it would claim more than is known. The names were held to what a real
// 1.0.51 server answers, which the live sweep goes on doing.
var dates = []pandorest.Workaround{
	pandorest.UndeclaredDates(pandorest.About{Name: Service + "-dates", Service: Service, Bug: "every date is declared a plain string; the server writes RFC 3339 with and without fractions of a second, and two forms with a space where the T goes"}, "createdAt", "updatedAt", "CreatedAt", "startedAt", "triggeredAt", "completedAt", "timestamp", "expiresAt", "lastUpdated", "mtime", "publishedAt", "checkedAt", "connectedAt", "lastHeartbeat", "nextRun", "scannedAt", "lastSync", "lastLogin", "hawserLastSeen", "lastUsed", "lastChecked", "lastTestAt", "lastBackupAt", "backupTime", "atime", "ctime"),
	pandorest.UndeclaredUnixDates(pandorest.About{Name: Service + "-unix-dates", Service: Service, Bug: "GET /api/containers/{id}/stats declares timestamp a plain integer, and it is the moment the figures were taken, in milliseconds since 1970"}, "milliseconds", "timestamp"),
}

// valueSets are the value sets, which come after every workaround that gives an answer its shape: most of these fields are in those shapes.
var valueSets = []pandorest.Workaround{
	// one set wherever the name is
	enums("everywhere", map[string]pandorest.Enum{
		"connectionType": {Values: []string{"socket", "direct", "hawser-standard", "hawser-edge"}},
		"authType":       {Name: "GitAuthType", Values: []string{"none", "password", "ssh"}},
		"scanner":        {Values: []string{"none", "grype", "trivy", "both"}},
		"sourceType":     {Name: "StackSourceType", Values: []string{"internal", "external", "git"}, In: []string{"GET /api/stacks/sources .sourceType", "GET /api/stacks/{name}/delete-preview"}},
		"health":         {Name: "ContainerHealth", Values: []string{"healthy", "unhealthy", "starting"}},
	}),
	enums("containers", map[string]pandorest.Enum{
		"state": {Name: "ContainerState", Values: []string{"running", "exited", "paused", "restarting", "created", "dead", "removing"}, In: []string{"GET /api/containers .state", "GET /api/stacks containerDetails.state"}},
	}),
	// a finding's own severity, and not the search parameter of the same name, which takes several with commas between
	enums("findings", map[string]pandorest.Enum{
		"severity": {Values: []string{"critical", "high", "medium", "low", "negligible", "unknown"}, In: []string{"GET /api/images/scan result.vulnerabilities.severity", "GET /api/vulnerabilities findings.severity", "POST /api/images/scan result.vulnerabilities.severity", "POST /api/images/scan results.vulnerabilities.severity"}},
		"stage":    {Name: "ScanStage", Values: []string{"complete", "error"}, In: []string{"POST /api/images/scan .stage"}},
	}),
	enums("checks", map[string]pandorest.Enum{
		"severity": {Name: "CheckSeverity", Values: []string{"error", "warn", "info"}, In: []string{"POST /api/stacks/{name}/validate findings.severity"}},
	}),
	enums("environments", map[string]pandorest.Enum{
		"protocol": {Name: "EnvironmentProtocol", Values: []string{"http", "https"}, In: []string{"GET /api/environments", "GET /api/environments/{id}", "POST /api/environments", "POST /api/environments/test", "PUT /api/environments/{id}"}},
		"mode":     {Name: "DiskWarningMode", Values: []string{"percentage", "absolute"}, In: []string{"GET /api/environments/{id}/disk-warning", "POST /api/environments/{id}/disk-warning"}},
	}),
	enums("jobs", map[string]pandorest.Enum{
		"status": {Name: "JobStatus", Values: []string{"running", "done", "error"}, In: []string{"GET /api/jobs/{id} .status"}},
	}),
	enums("stacks", map[string]pandorest.Enum{
		"status": {Name: "StackStatus", Values: []string{"running", "partial", "stopped", "created"}, In: []string{"GET /api/stacks .status"}},
	}),
	enums("runs", map[string]pandorest.Enum{
		"status": {Name: "RunStatus", Values: []string{"queued", "running", "success", "warning", "failed", "skipped"}, In: []string{"GET /api/schedules/executions", "GET /api/schedules/executions/{id}", "GET /api/schedules"}},
	}),
	enums("deploys", map[string]pandorest.Enum{
		"status": {Name: "DeployStatus", Values: []string{"running", "success", "failed"}, In: []string{"GET /api/stacks/{name}/deploys", "GET /api/stacks/{name}/deploys/{runId}"}},
	}),
	enums("notifications", map[string]pandorest.Enum{
		"type":  {Name: "NotificationType", Values: []string{"smtp", "apprise"}, In: []string{"GET /api/notifications", "GET /api/notifications/{id}", "POST /api/notifications", "PUT /api/notifications/{id}", "POST /api/notifications/test"}},
		"scope": {Name: "EventScope", Values: []string{"environment", "system"}, In: []string{"GET /api/notifications/trigger-test"}},
	}),
	enums("secret-providers", map[string]pandorest.Enum{
		"type": {Name: "SecretProviderType", Values: []string{"vault", "infisical", "doppler", "op-connect", "op-service-account", "azure-kv", "bitwarden", "keepass"}, In: []string{"GET /api/secret-providers", "GET /api/secret-providers/{id}", "POST /api/secret-providers", "PUT /api/secret-providers/{id}", "POST /api/secret-providers/test"}},
	}),
	enums("changelog", map[string]pandorest.Enum{
		"type": {Name: "ChangeType", Values: []string{"feature", "fix"}, In: []string{"GET /api/changelog"}},
	}),
	enums("schedules", map[string]pandorest.Enum{
		"type": {Name: "ScheduleType", Values: []string{"container_update", "git_stack_sync", "system_cleanup", "env_update_check", "image_prune", "deploy_log_reconcile", "backup", "repo_prune", "repo_check", "repo_verify"}, In: []string{"GET /api/schedules schedules.type"}},
	}),
	enums("files", map[string]pandorest.Enum{
		"type": {Name: "FileType", Values: []string{"file", "directory", "symlink", "other"}, In: []string{"GET /api/system/files", "GET /api/containers/{id}/files", "GET /api/volumes/{name}/browse"}},
	}),
	enums("sign-in", map[string]pandorest.Enum{
		"type": {Name: "SignInType", Values: []string{"local", "oidc", "ldap"}, In: []string{"GET /api/auth/providers"}},
	}),
	enums("templates", map[string]pandorest.Enum{
		"type":          {Name: "TemplateType", Values: []string{"container", "stack"}, In: []string{"GET /api/templates .type"}},
		"restartPolicy": {Values: []string{"no", "always", "unless-stopped", "on-failure"}, In: []string{"GET /api/templates"}},
	}),
}
