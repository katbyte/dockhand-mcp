package dockhand

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/katbyte/go-kt/version"
	"github.com/katbyte/pandorest/client"
)

// service is what is Dockhand's own about being talked to. This file is written by hand, beside the generated ones.
var service = client.Service{
	Name:           "Dockhand",
	UserAgent:      "dockhand-mcp/" + version.Version,
	Example:        "dockhand:3000",
	RedirectAdvice: "set the server url to the address Dockhand itself answers on",
	// what Dockhand is sent that a trace would otherwise show: the keys it is given to reach a host and a repository with. Its tokens, passwords and
	// secrets are named as such, and hidden unasked
	SecretNames: []string{"tlsKey", "sshPrivateKey", "sshPassphrase"},
	Messages:    errorMessages,
	Note:        note,
}

// NewWithoutLogin returns a client that sends no credentials: for a Dockhand run with its login switched off, and, made with client.WithCookies, for
// logging in to one that has it on, which is how a token is first come by. New, which is generated, wants a token.
func NewWithoutLogin(baseURL string, opts ...client.Option) (*Client, error) {
	return Connect(baseURL, "", opts...)
}

// Connect returns a client for a Dockhand as it is run: one that sends the API token it is given, or nothing for a Dockhand whose login is switched
// off and so has no token to give.
func Connect(baseURL, token string, opts ...client.Option) (*Client, error) {
	c, err := service.New(baseURL, client.Bearer(strings.TrimSpace(token)), opts...)
	if err != nil {
		return nil, err
	}

	return &Client{Client: c}, nil
}

// AcceptJob is what a call Dockhand can run in the background is given as its Accept option to be answered with the id of a job at once, where it
// otherwise does all of the work before it answers. The answer then holds JobId alone, and GetJobsById says how the job goes and how it went.
const AcceptJob = "*/*"

// sendExactly sends a call the fields it is given and no others, each as it is, and reads the answer into a model: for what a generated call cannot
// say, which leaves a text that is empty and a number that is 0 out of what it sends, where Dockhand keeps what it is not sent.
func (c Client) sendExactly(ctx context.Context, method, path string, fields, model any) (*client.Response, error) {
	req, err := c.Client.NewRequest(ctx, client.RequestOptions{ContentType: "application/json", ExpectedStatusCodes: []int{http.StatusOK}, HTTPMethod: method, Path: path})
	if err != nil {
		return nil, err
	}
	if err := req.Marshal(fields); err != nil {
		return nil, err
	}

	resp, err := req.Execute(ctx)
	if err != nil {
		return resp, err
	}

	err = resp.Unmarshal(model)

	return resp, err
}

// ForgetEnvironmentPublicIP has Dockhand forget the address an environment's published ports are reached at from outside. PutEnvironmentsById cannot:
// it leaves an empty publicIp out of what it sends, as it does every text it is not given, and Dockhand keeps what it is not sent.
func (c Client) ForgetEnvironmentPublicIP(ctx context.Context, id int) (result PutEnvironmentsByIdResponse, err error) {
	var model PutEnvironmentsByIdAnswer
	resp, err := c.sendExactly(ctx, http.MethodPut, fmt.Sprintf("/api/environments/%d", id), map[string]string{"publicIp": ""}, &model)
	if resp != nil {
		result.HttpResponse = resp.Response
	}
	if err != nil {
		return result, err
	}
	result.Answer = &model

	return result, nil
}

// refusal is how Dockhand says a call went wrong: why in error, sometimes with more in message or details.
type refusal struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	Details string `json:"details"`
}

// errorMessages reads what Dockhand said went wrong out of a refusal's body, each thing it said once, or nothing when the body is not one of its
// refusals.
func errorMessages(body []byte) []string {
	var said refusal
	if err := json.Unmarshal(body, &said); err != nil {
		return nil
	}

	var out []string
	for _, part := range []string{said.Error, said.Message, said.Details} {
		if part = strings.TrimSpace(part); part != "" && !containsFold(out, part) {
			out = append(out, part)
		}
	}

	return out
}

// containsFold reports whether a list already holds a text, whatever its case: Dockhand says "Unauthorized" in one field and more in the next.
func containsFold(list []string, text string) bool {
	for _, have := range list {
		if strings.EqualFold(have, text) {
			return true
		}
	}

	return false
}

// enterpriseOnly is what Dockhand says of a call its free edition does not have.
const enterpriseOnly = "Enterprise license required"

// sessionOnly is what Dockhand says to a token, good or not, on the calls it keeps for a session: a token it does not take at all is told
// "Authentication required" instead.
const sessionOnly = "Not authenticated"

// note says what is known of a refusal on a Dockhand: who is being turned away, and what a path it does not have looks like beside a thing that is
// not there.
func note(f client.Failure) string {
	switch {
	case f.StatusCode == http.StatusUnauthorized && f.Credentialed && bytes.Contains(f.Body, []byte(sessionOnly)):
		return "this call answers a logged-in session and no API token, however good the token: it is about whoever is logged in to the web console"
	case f.StatusCode == http.StatusUnauthorized && f.Credentialed:
		return "the API token was refused: it is wrong, expired or revoked"
	case f.StatusCode == http.StatusUnauthorized:
		return "this Dockhand has its login switched on and was sent no API token: make one in the web console under Profile, API tokens"
	case f.StatusCode == http.StatusForbidden && bytes.Contains(f.Body, []byte(enterpriseOnly)):
		return "this is part of Dockhand's Enterprise edition, which this server has no licence for"
	case f.StatusCode == http.StatusForbidden:
		return "the account the API token belongs to may not do this"
	case f.StatusCode == http.StatusNotFound && !looksLikeJSON(f.Body):
		return "this server does not answer this call: it may be an older Dockhand than this client was written against, the call may be for something this server has switched off, or the server url may not be a Dockhand"
	default:
		return ""
	}
}

// looksLikeJSON reports whether a body starts as a JSON object or list does, which is how Dockhand's own refusals are told from a path it does not
// answer: its API says "not found" in JSON, and a path with no handler, or one for a feature that is switched off, in text.
func looksLikeJSON(body []byte) bool {
	trimmed := bytes.TrimSpace(body)

	return len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[')
}

// ForgetRegistryCredentials has Dockhand forget the user name and password it signs in to a registry with. PutRegistriesById cannot: it leaves an
// empty name and password out of what it sends, and Dockhand keeps what it is not sent. The registry's name and address go with it, since Dockhand
// fails a change that does not carry the address.
func (c Client) ForgetRegistryCredentials(ctx context.Context, id int, name, address string) (result PutRegistriesByIdResponse, err error) {
	var model PutRegistriesByIdAnswer
	resp, err := c.sendExactly(ctx, http.MethodPut, fmt.Sprintf("/api/registries/%d", id), map[string]string{"name": name, "url": address, "username": "", "password": ""}, &model)
	if resp != nil {
		result.HttpResponse = resp.Response
	}
	if err != nil {
		return result, err
	}
	result.Answer = &model

	return result, nil
}

// SaveGeneralSettings has Dockhand save the general settings named, each exactly as it is given, and leave the rest as they are. PostSettingsGeneral
// cannot say every value: it leaves a number that is 0 and a text that is empty out of what it sends, and Dockhand keeps what it is not sent. A grace
// of no days is a setting Dockhand takes, and an empty text is how the place it keeps its stacks is forgotten. Dockhand can answer that the save
// failed and have saved it all the same: what it has is told by reading the settings, not by this answer.
func (c Client) SaveGeneralSettings(ctx context.Context, settings map[string]any) (result PostSettingsGeneralResponse, err error) {
	var model PostSettingsGeneralAnswer
	resp, err := c.sendExactly(ctx, http.MethodPost, "/api/settings/general", settings, &model)
	if resp != nil {
		result.HttpResponse = resp.Response
	}
	if err != nil {
		return result, err
	}
	result.Answer = &model

	return result, nil
}

// ForgetUserDetails has Dockhand forget a user's email address, the name it shows for the user, or both. PutUsersById cannot: it leaves an empty text
// out of what it sends, and Dockhand keeps what it is not sent.
func (c Client) ForgetUserDetails(ctx context.Context, id int, email, displayName bool) (result PutUsersByIdResponse, err error) {
	forgotten := map[string]string{}
	if email {
		forgotten["email"] = ""
	}
	if displayName {
		forgotten["displayName"] = ""
	}

	var model PutUsersByIdAnswer
	resp, err := c.sendExactly(ctx, http.MethodPut, fmt.Sprintf("/api/users/%d", id), forgotten, &model)
	if resp != nil {
		result.HttpResponse = resp.Response
	}
	if err != nil {
		return result, err
	}
	result.Answer = &model

	return result, nil
}

// NeverEndSessions has Dockhand keep a signed-in session until it is signed out of, however long that is. PutAuthSettings cannot: a session that
// never ends is a time of 0, which it leaves out of what it sends.
func (c Client) NeverEndSessions(ctx context.Context) (result PutAuthSettingsResponse, err error) {
	var model PutAuthSettingsAnswer
	resp, err := c.sendExactly(ctx, http.MethodPut, "/api/auth/settings", map[string]int{"sessionTimeout": 0}, &model)
	if resp != nil {
		result.HttpResponse = resp.Response
	}
	if err != nil {
		return result, err
	}
	result.Answer = &model

	return result, nil
}

// ForgetOidcAdminClaim has Dockhand forget the claim, and the value of it, that a single sign-on provider's record names for an administrator.
// PutAuthOidcById cannot: it leaves an empty text out of what it sends, and Dockhand keeps what it is not sent.
func (c Client) ForgetOidcAdminClaim(ctx context.Context, id int) (result PutAuthOidcByIdResponse, err error) {
	var model PutAuthOidcByIdAnswer
	resp, err := c.sendExactly(ctx, http.MethodPut, fmt.Sprintf("/api/auth/oidc/%d", id), map[string]string{"adminClaim": "", "adminValue": ""}, &model)
	if resp != nil {
		result.HttpResponse = resp.Response
	}
	if err != nil {
		return result, err
	}
	result.Answer = &model

	return result, nil
}

// ForgetTagIcon has Dockhand forget the icon a tag was given. PutTagsById cannot: it leaves an empty text out of what it sends, and Dockhand keeps
// what it is not sent.
func (c Client) ForgetTagIcon(ctx context.Context, id int) (result PutTagsByIdResponse, err error) {
	var model PutTagsByIdAnswer
	resp, err := c.sendExactly(ctx, http.MethodPut, fmt.Sprintf("/api/tags/%d", id), map[string]string{"icon": ""}, &model)
	if resp != nil {
		result.HttpResponse = resp.Response
	}
	if err != nil {
		return result, err
	}
	result.Answer = &model

	return result, nil
}

// ForgetConfigSetDescription has Dockhand forget the description a config set was given. PutConfigSetsById cannot: it leaves an empty text out of
// what it sends, and Dockhand keeps what it is not sent.
func (c Client) ForgetConfigSetDescription(ctx context.Context, id int) (result PutConfigSetsByIdResponse, err error) {
	var model PutConfigSetsByIdAnswer
	resp, err := c.sendExactly(ctx, http.MethodPut, fmt.Sprintf("/api/config-sets/%d", id), map[string]string{"description": ""}, &model)
	if resp != nil {
		result.HttpResponse = resp.Response
	}
	if err != nil {
		return result, err
	}
	result.Answer = &model

	return result, nil
}

// ForgetNavigationPages has Dockhand forget the page it opens on, the page a click on an environment opens, or both, for everyone who chose none of
// their own. PutSettingsNavigation cannot: it leaves an empty text out of what it sends, and Dockhand keeps what it is not sent.
func (c Client) ForgetNavigationPages(ctx context.Context, landing, envClick bool) (result PutSettingsNavigationResponse, err error) {
	pages := map[string]any{}
	if landing {
		pages["landingPage"] = nil
	}
	if envClick {
		pages["envClickPage"] = nil
	}
	var model PutSettingsNavigationAnswer
	resp, err := c.sendExactly(ctx, http.MethodPut, "/api/settings/navigation?scope=global", pages, &model)
	if resp != nil {
		result.HttpResponse = resp.Response
	}
	if err != nil {
		return result, err
	}
	result.Answer = &model

	return result, nil
}

// SetLogFavorites saves everything kept for an environment's log view, in order: a container's name as a text and a group as a record of its name and
// its containers. PostPreferencesFavorites cannot send a group: it takes names alone, and what is sent is all that is kept afterwards.
func (c Client) SetLogFavorites(ctx context.Context, env int, kept []any) (result PostPreferencesFavoritesResponse, err error) {
	var model PostPreferencesFavoritesAnswer
	resp, err := c.sendExactly(ctx, http.MethodPost, "/api/preferences/favorites", map[string]any{"environmentId": env, "action": "reorder", "favorites": append([]any{}, kept...)}, &model)
	if resp != nil {
		result.HttpResponse = resp.Response
	}
	if err != nil {
		return result, err
	}
	result.Answer = &model

	return result, nil
}

// ForgetStackSecretProvider saves a stack's compose file and takes the stack off the secret provider its secrets were loaded from.
// PutStacksByNameCompose cannot: it leaves a provider of 0 out of what it sends, and Dockhand keeps the provider it is not sent.
func (c Client) ForgetStackSecretProvider(ctx context.Context, name, content string, env int) (result PutStacksByNameComposeResponse, err error) {
	var model PutStacksByNameComposeAnswer
	resp, err := c.sendExactly(ctx, http.MethodPut, "/api/stacks/"+url.PathEscape(name)+"/compose?env="+strconv.Itoa(env), map[string]any{"content": content, "secretProviderId": nil}, &model)
	if resp != nil {
		result.HttpResponse = resp.Response
	}
	if err != nil {
		return result, err
	}
	result.Answer = &model

	return result, nil
}

// GitStackSettings names the settings of a git stack that can be taken away rather than changed: the branch of its own, the extra .env file, the
// folder copied beside its compose file, the secret provider its secrets are loaded from, and its webhook with the secret that goes with it.
type GitStackSettings struct {
	Branch, EnvFilePath, ContextDir, SecretProvider, Webhook bool
}

// ForgetGitStackSettings takes the settings named away from a git stack, which then follows its repository's own branch, reads no extra .env file,
// and so on. PutGitStacksById cannot: it leaves an empty value out of what it sends, and Dockhand keeps what it is not sent. The webhook is switched
// off with its secret, since Dockhand refuses to forget the secret of one that is on.
func (c Client) ForgetGitStackSettings(ctx context.Context, id int, which GitStackSettings) (result PutGitStacksByIdResponse, err error) {
	forgotten := map[string]any{}
	for field, named := range map[string]bool{"branch": which.Branch, "envFilePath": which.EnvFilePath, "contextDir": which.ContextDir, "secretProviderId": which.SecretProvider, "webhookSecret": which.Webhook} {
		if named {
			forgotten[field] = nil
		}
	}
	if which.Webhook {
		forgotten["webhookEnabled"] = false
	}

	var model PutGitStacksByIdAnswer
	resp, err := c.sendExactly(ctx, http.MethodPut, fmt.Sprintf("/api/git/stacks/%d", id), forgotten, &model)
	if resp != nil {
		result.HttpResponse = resp.Response
	}
	if err != nil {
		return result, err
	}
	result.Answer = &model

	return result, nil
}

// ForgetGitRepositoryCredential has a git repository fetched with no credential from then on. PutGitRepositoriesById cannot: it leaves a credential
// of 0 out of what it sends, and Dockhand keeps the one it is not sent.
func (c Client) ForgetGitRepositoryCredential(ctx context.Context, id int) (result PutGitRepositoriesByIdResponse, err error) {
	var model PutGitRepositoriesByIdAnswer
	resp, err := c.sendExactly(ctx, http.MethodPut, fmt.Sprintf("/api/git/repositories/%d", id), map[string]any{"credentialId": nil}, &model)
	if resp != nil {
		result.HttpResponse = resp.Response
	}
	if err != nil {
		return result, err
	}
	result.Answer = &model

	return result, nil
}

// ForgetBackupConfigSchedule has a backup configuration run only when it is asked to, and says what it keeps from then on. PutBackupConfigsById
// cannot: it leaves an empty schedule out of what it sends, and Dockhand keeps the one it is not sent. What to keep is always sent beside it, since
// Dockhand left to itself drops it when a schedule goes and keeps it when there was none to go: nil keeps every snapshot, which only a
// configuration with no schedule can.
func (c Client) ForgetBackupConfigSchedule(ctx context.Context, id BackupConfigID, keep *PutBackupConfigsByIdRequestRetention) (result PutBackupConfigsByIdResponse, err error) {
	fields := map[string]any{"schedule": "", "retention": nil}
	if keep != nil {
		fields["retention"] = keep
	}

	var model PutBackupConfigsByIdAnswer
	resp, err := c.sendExactly(ctx, http.MethodPut, fmt.Sprintf("/api/backup/configs/%d", id), fields, &model)
	if resp != nil {
		result.HttpResponse = resp.Response
	}
	if err != nil {
		return result, err
	}
	result.Answer = &model

	return result, nil
}

// BackupDestinationFlags names which of the flags a backup destination gives restic are to be forgotten: the ones for a backup, and the ones for a
// restore.
type BackupDestinationFlags struct {
	Backup, Restore bool
}

// ForgetBackupDestinationFlags takes the flags named away from a backup destination, which then gives restic none of them. PutBackupDestinationsById
// cannot: it leaves an empty value out of what it sends, and Dockhand keeps what it is not sent.
func (c Client) ForgetBackupDestinationFlags(ctx context.Context, id BackupDestinationID, which BackupDestinationFlags) (result PutBackupDestinationsByIdResponse, err error) {
	forgotten := map[string]string{}
	for field, named := range map[string]bool{"backupFlags": which.Backup, "restoreFlags": which.Restore} {
		if named {
			forgotten[field] = ""
		}
	}

	var model PutBackupDestinationsByIdAnswer
	resp, err := c.sendExactly(ctx, http.MethodPut, fmt.Sprintf("/api/backup/destinations/%d", id), forgotten, &model)
	if resp != nil {
		result.HttpResponse = resp.Response
	}
	if err != nil {
		return result, err
	}
	result.Answer = &model

	return result, nil
}
