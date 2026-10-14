package workarounds

import "github.com/katbyte/pandorest/config"

// ids are the kinds of id the client tells apart, so that one handed to a call that wants another does not compile: the ids Dockhand gives its own
// records, which are all numbers and so pass for each other. A kind is the id of its record wherever the record is answered, and of every parameter
// and property that names it.
var ids = []config.ID{
	// env is also what a container's variables are called where one is made, changed or written as a template: a list there, and not an id
	{Name: "EnvironmentID", Of: []string{"GetEnvironmentsAnswer", "GetEnvironmentsByIdAnswer", "PostEnvironmentsAnswer", "PutEnvironmentsByIdAnswer", "GetDashboardStatsAnswer"}, Names: []string{"environmentId", "environmentIds", "environment_id", "envId", "targetEnvId", "sourceEnvironmentId", "env"}, Except: []string{"GetTemplatesAnswer .env", "PostContainersByIdUpdateRequest .env", "PostContainersRequest .env", "PostTemplatesComposeRequestTemplate .env"}},
	{Name: "NotificationID", Of: []string{"GetNotificationsAnswer", "GetNotificationsByIdAnswer", "PostNotificationsAnswer", "PutNotificationsByIdAnswer"}, Names: []string{"notificationId"}},
	{Name: "RegistryID", Of: []string{"GetRegistriesAnswer", "GetRegistriesByIdAnswer", "PostRegistriesAnswer", "PutRegistriesByIdAnswer"}, Names: []string{"registryId", "registry"}},
	{Name: "UserID", Of: []string{"GetUsersAnswer", "GetUsersByIdAnswer", "PostUsersAnswer", "PutUsersByIdAnswer", "GetAuthSessionAnswerUser"}, Names: []string{"userId"}},
	{Name: "RoleID", Of: []string{"GetRolesAnswer", "GetRolesByIdAnswer"}, Names: []string{"roleId"}},
	{Name: "GitRepositoryID", Of: []string{"GetGitRepositoriesAnswer", "GetGitRepositoriesByIdAnswer", "PostGitRepositoriesAnswer", "PutGitRepositoriesByIdAnswer", "GetGitStacksAnswerRepository", "GetGitStacksByIdAnswerRepository", "PostGitStacksAnswerRepository", "PutGitStacksByIdAnswerRepository"}, Names: []string{"repositoryId"}},
	{Name: "GitCredentialID", Of: []string{"GetGitCredentialsAnswer", "GetGitCredentialsByIdAnswer", "PostGitCredentialsAnswer", "PutGitCredentialsByIdAnswer"}, Names: []string{"credentialId"}},
	{Name: "GitStackID", Of: []string{"GetGitStacksAnswer", "GetGitStacksByIdAnswer", "PostGitStacksAnswer", "PutGitStacksByIdAnswer"}},
	{Name: "SecretProviderID", Of: []string{"GetSecretProvidersAnswer", "GetSecretProvidersByIdAnswer", "PostSecretProvidersAnswer", "PutSecretProvidersByIdAnswer"}, Names: []string{"secretProviderId", "providerId"}},
	{Name: "ConfigSetID", Of: []string{"GetConfigSetsAnswer", "GetConfigSetsByIdAnswer", "PostConfigSetsAnswer", "PutConfigSetsByIdAnswer"}},
	{Name: "TagID", Of: []string{"GetTagsAnswerTags", "PostTagsAnswer"}, Names: []string{"tagIds"}},
	{Name: "TemplateSourceID", Of: []string{"GetTemplatesSourcesAnswer", "PostTemplatesSourcesAnswer"}},
	{Name: "BackupDestinationID", Of: []string{"GetBackupDestinationsAnswer", "GetBackupDestinationsByIdAnswer", "PostBackupDestinationsAnswer", "PutBackupDestinationsByIdAnswer"}, Names: []string{"destinationId"}},
	{Name: "BackupConfigID", Of: []string{"GetBackupConfigsAnswer", "GetBackupConfigsByIdAnswer", "PostBackupConfigsAnswer", "PutBackupConfigsByIdAnswer"}, Names: []string{"configId"}},
}

// plain are the names that look like an id and are left as the document has them.
var plain = []string{
	// Docker's own: a container, an image, a network or what runs in one is named by its id, the start of it or its name in the same place
	"EndpointID", "ID", "Id", "containerId", "containerIds", "imageId", "execId", "helperId",
	// used once and handed straight back: a job, a run of a deploy, a rule of the compose check
	"jobId", "runId", "ruleId",
	// whose id it is depends on what kind of schedule or audit record is beside it
	"scheduleId", "entityId",
	// an id given by someone else: a single sign-on client's, an agent's own, a grid of the web interface's
	"clientId", "agentId", "hawserAgentId", "gridId", "instanceId",
	// a snapshot is restic's, named by the id restic gave it or the start of it, and a run of a backup is one of the runs the schedules list
	"snapshotId", "snapshotIds", "shortId", "executionId",
	// who owns a file inside a snapshot
	"uid", "gid",
	// a token's number, met in one answer, and the name a template source goes by, which is text
	"tokenId", "sourceId",
	// every record's own id that is not one of the kinds above
	"id",
}
