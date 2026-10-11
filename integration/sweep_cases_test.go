//go:build integration

package integration

import (
	"maps"
	"net/http"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/pandorest/sweep"
)

// Why a read cannot simply answer with something on the suite's server, for the reasons that are true of many.
const (
	// the Enterprise edition's calls answer 403 on a server with no licence
	enterprise = "part of Dockhand's Enterprise edition, which the suite's server has no licence for"
	// a feature a server is not set up for answers 404 Not found, in text, to every call of its
	backupOff = "the suite's server has no backup set up, and every backup call answers 404 Not found until one is"
	// a server with its login off has nobody logged in
	nobody = "a server with its login off has no account to ask about: it answers that authentication is not enabled"
	// the hub the server asks is not the suite's to keep up
	dockerHub = "asks Docker Hub over the internet, which the suite does not stand in for yet"
)

// nothing is the path of something of a kind the suite's server has none of: no one thing has this number.
var nothing = map[string]string{"id": "999999"}

// none is a read of one thing of a kind the suite's server has none of, which the server says is not there.
func none(what string) sweep.Case {
	return sweep.Case{Path: nothing, Status: http.StatusNotFound, Why: "the suite's server has no " + what + " to read"}
}

// alwaysCases are the reads that cannot answer with something on the suite's server however it is run.
var alwaysCases = map[string]sweep.Case{
	// the Enterprise edition's
	"GetAudit":                {Status: http.StatusForbidden, Why: enterprise},
	"GetAuditEvents":          {Status: http.StatusForbidden, Why: enterprise},
	"GetAuditExport":          {Status: http.StatusForbidden, Why: enterprise},
	"GetAuditUsers":           {Status: http.StatusForbidden, Why: enterprise},
	"GetAuditOutsideApi":      {Status: http.StatusForbidden, Why: enterprise},
	"GetAuditUsersOutsideApi": {Status: http.StatusForbidden, Why: enterprise},
	"GetAuthLdap":             {Status: http.StatusForbidden, Why: enterprise},
	"GetAuthLdapById":         {Path: nothing, Status: http.StatusForbidden, Why: enterprise},
	"GetUsersByIdRoles":       {Path: nothing, Status: http.StatusForbidden, Why: enterprise},

	// backups, which are not set up
	"GetBackupConfigs":               {Status: http.StatusNotFound, Why: backupOff},
	"GetBackupConfigsById":           {Path: nothing, Status: http.StatusNotFound, Why: backupOff},
	"GetBackupDestinations":          {Status: http.StatusNotFound, Why: backupOff},
	"GetBackupDestinationsById":      {Path: nothing, Status: http.StatusNotFound, Why: backupOff},
	"GetBackupInstance":              {Status: http.StatusNotFound, Why: backupOff},
	"GetBackupSnapshots":             {Status: http.StatusNotFound, Why: backupOff},
	"GetBackupSnapshotsByIdBrowse":   {Path: nothing, Status: http.StatusNotFound, Why: backupOff},
	"GetBackupSnapshotsByIdDump":     {Path: nothing, Status: http.StatusNotFound, Why: backupOff},
	"GetBackupSnapshotsByIdMetadata": {Path: nothing, Status: http.StatusNotFound, Why: backupOff},
	"GetBackupSnapshotsDiff":         {Status: http.StatusNotFound, Why: backupOff},
	"GetBackupStackDirListing":       {Options: map[string]any{"Target": "local"}, Status: http.StatusNotFound, Why: backupOff},
	"GetBackupStackPath":             {Options: map[string]any{"Target": "local"}, Status: http.StatusNotFound, Why: backupOff},

	// switched off on a server as it comes
	"GetDocs":        {Status: http.StatusNotFound, Why: "the API's description of itself is served here only by a server started with FEAT_API_DOCS=true, as its document says; every server serves it at /openapi.json"},
	"GetMetrics":     {Status: http.StatusNotFound, Why: "the metrics a scraper reads are switched off unless the server is started with EXPORT_METRICS=true"},
	"GetDebugMemory": {Status: http.StatusForbidden, Why: "the memory monitor is off unless the server is started with MEMORY_MONITOR=true"},

	// one thing of a kind there is none of
	"GetAuthOidcById":                                  none("single sign-on provider"),
	"GetConfigSetsById":                                none("config set"),
	"GetGitCredentialsById":                            none("git credential"),
	"GetGitRepositoriesById":                           none("git repository"),
	"GetGitRepositoriesByIdSync":                       none("git repository"),
	"GetGitStacksById":                                 none("stack deployed from git"),
	"GetGitStacksByIdEnvFiles":                         none("stack deployed from git"),
	"GetGitStacksByIdWebhook":                          none("stack deployed from git"),
	"GetGitWebhookById":                                none("git repository"),
	"GetNotificationsById":                             none("notification channel"),
	"GetSchedulesExecutionsById":                       none("run of a schedule"),
	"GetSecretProvidersById":                           none("secret provider"),
	"GetEnvironmentsByIdNotificationsByNotificationId": {Path: map[string]string{"notificationId": "999999"}, Status: http.StatusNotFound, Why: "the suite's server has no notification channel for an environment to use"},

	// pictures nobody has given
	"GetContainerIconsByName": {Status: http.StatusNotFound, Why: "the sweep's container has no icon of its own: the server answers No custom icon"},
	"GetEnvironmentsByIdIcon": {Status: http.StatusNotFound, Why: "the environment has no icon of its own: the server answers No custom icon"},
	"GetStacksByNameIcon":     {Status: http.StatusNotFound, Why: "the sweep's stack has no icon of its own: the server answers No custom icon"},

	// a stack Dockhand did not deploy
	"GetStacksByNameCompose":           {Status: http.StatusNotFound, Why: "the sweep's stack was brought up at the host, and Dockhand has not been told where its compose file is"},
	"GetStacksByNameDeploys":           {Empty: "the sweep's stack was brought up at the host, so Dockhand has no run of its own deploying it"},
	"GetStacksByNameDeploysByRunId":    {Path: map[string]string{"runId": "999999"}, Status: http.StatusNotFound, Why: "the sweep's stack was brought up at the host, so Dockhand has no run of its own deploying it"},
	"GetStacksByNameDeploysByRunIdLog": {Path: map[string]string{"runId": "999999"}, Status: http.StatusNotFound, Why: "the sweep's stack was brought up at the host, so Dockhand has no run of its own deploying it"},
	"GetStacksByNameEnv":               {Empty: "the sweep's stack has no variables kept for it by Dockhand"},
	"GetStacksByNameEnvRaw":            {Empty: "the sweep's stack has no variables kept for it by Dockhand"},
	"GetStacksByNameTags":              {Empty: "the sweep's stack has no tag"},
	"GetStacksSources":                 {Empty: "no stack on the host was made by Dockhand, which is what this lists the source of"},

	// what nobody has made on a server as it comes
	"GetAuthOidc":                        {Empty: "no single sign-on provider is set up"},
	"GetAutoUpdate":                      {Empty: "no container is set to update itself"},
	"GetConfigSets":                      {Empty: "no config set has been made"},
	"GetContainerIcons":                  {Empty: "no container has been given an icon of its own"},
	"GetContainerTags":                   {Empty: "no container has been tagged"},
	"GetContainerTagsByName":             {Empty: "the sweep's container has no tag"},
	"GetEnvironmentsByIdNotifications":   {Empty: "no notification channel is set for the environment"},
	"GetEnvironmentsByIdRemoteStacksDir": {Empty: "the environment has no folder of its own set for stacks"},
	"GetGitCredentials":                  {Empty: "no git credential has been made"},
	"GetGitRepositories":                 {Empty: "no git repository has been added"},
	"GetGitStacks":                       {Empty: "no stack has been deployed from git"},
	"GetHawserTokens":                    {Empty: "no agent that calls in has been given a token"},
	"GetLabels":                          {Empty: "no environment carries a label"},
	"GetNotifications":                   {Empty: "no notification channel has been made"},
	"GetPreferencesEnvironmentOrder":     {Empty: "nobody has put the environments in an order of their own"},
	"GetPreferencesFavoriteGroups":       {Empty: "nobody has grouped favourites"},
	"GetPreferencesFavorites":            {Empty: "nobody has marked a favourite"},
	"GetPreferencesGrid":                 {Empty: "nobody has arranged a grid"},
	"GetPreferencesSidebar":              {Empty: "nobody has arranged the sidebar"},
	"GetPreferencesTagOrder":             {Empty: "nobody has put the tags in an order of their own"},
	"GetSchedulesSettings":               {Empty: "the one setting there is, hiding the system's own jobs, is off as it comes"},
	"GetSecretProviders":                 {Empty: "no secret provider has been added"},
	"GetSecretProvidersDefault":          {Empty: "no secret provider has been added, so none is the default"},
	"GetStackTags":                       {Empty: "no stack has been tagged"},
	"GetTags":                            {Empty: "no tag has been made"},
	"GetImagesScan":                      {Empty: "the sweep asks after an image by a name no image on the host goes by, so no scan of it is kept"},
	"GetContainersByIdVersionNotes":      {Options: map[string]any{"Versions": "1.37"}, Empty: "the sweep's image names no source to read release notes from"},

	// what the server will not do here
	"GetRegistryCatalog":    {Status: http.StatusBadRequest, Why: "the one registry a server has as it comes is Docker Hub, which does not list what it holds: the server says to search instead"},
	"GetSelfUpdateProgress": {Options: map[string]any{"Id": "none"}, Status: http.StatusInternalServerError, Why: "the suite's Dockhand is given no Docker socket of its own, which is what it would update itself through"},
	"GetSystemFilesContent": {Options: map[string]any{"Path": "/etc/hostname"}, Status: http.StatusForbidden, Why: "the server refuses to read this file of its own with Access denied; which of its files it will read is not known"},
	"GetJobsById":           {Path: map[string]string{"id": "00000000-0000-0000-0000-000000000000"}, Status: http.StatusNotFound, Why: "a job is made for a caller that will take any answer, and this client asks for JSON alone, which is answered with how the action went and no job"},

	// what reaches past the server
	"GetRegistrySearch": {Status: http.StatusInternalServerError, Sometimes: true, Why: dockerHub},
	"GetRegistryTags":   {Status: http.StatusInternalServerError, Sometimes: true, Why: dockerHub},

	// each wants a path that is a file, where the fixtures name a folder for the calls that list one
	"GetContainersByIdFilesContent": {Options: map[string]any{"Path": "/etc/hostname"}},
	"GetVolumesByNameBrowseContent": {Options: map[string]any{"Path": "/note.txt"}},
}

// openCases are the reads that answer otherwise on a server with its login off.
var openCases = map[string]sweep.Case{
	"GetAuthTokens":           {Status: http.StatusBadRequest, Why: nobody},
	"GetProfile":              {Status: http.StatusBadRequest, Why: nobody},
	"GetProfilePreferences":   {Status: http.StatusBadRequest, Why: nobody},
	"GetAuthOidcByIdInitiate": {Path: nothing, Status: http.StatusBadRequest, Why: nobody},
	"GetAuthProviders":        {Empty: "a server with its login off offers no way to log in"},
	"GetAuthSession":          {Empty: "a server with its login off says that login is off and that nobody is logged in, which is all it says"},
	"GetUsers":                {Empty: "a server as it comes has no user"},
	"GetUsersById":            none("user"),
}

// sessionOnly is why a call answers a token the server takes with 401: it is kept for a logged-in session.
const sessionOnly = "answers a logged-in session and no API token, which is what the suite asks with: TestSessionOnlyReads asks it as a session"

// outsideAPI is why a call outside /api is not asked with the login on.
const outsideAPI = "a token is read under /api and nowhere else: with its login on the server sends this call to its login page, which is no answer of the call's"

// loginCases are the reads that answer otherwise on a server with its login on, asked with a token.
var loginCases = map[string]sweep.Case{
	"GetProfile":              {Status: http.StatusUnauthorized, Why: sessionOnly},
	"GetProfilePreferences":   {Status: http.StatusUnauthorized, Why: sessionOnly},
	"GetRoles":                {Status: http.StatusForbidden, Why: enterprise + "; with its login off the same server lists its built-in roles"},
	"GetRolesById":            {Status: http.StatusForbidden, Why: enterprise + "; with its login off the same server gives a built-in role"},
	"GetAuditOutsideApi":      {Skip: outsideAPI},
	"GetAuditUsersOutsideApi": {Skip: outsideAPI},
	// no single sign-on provider is set up, so the browser is sent back to the login page with why: the redirect is the answer
	"GetAuthOidcByIdInitiate": {Path: nothing},
}

// sweepCases are the reads that cannot simply answer with something on the suite's server, each with why, for the way the server is run.
func sweepCases(stage testenv.Stage) map[string]sweep.Case {
	cases := maps.Clone(alwaysCases)
	if stage == testenv.StageOpen {
		maps.Copy(cases, openCases)
	} else {
		maps.Copy(cases, loginCases)
	}

	return cases
}
