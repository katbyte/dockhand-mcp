package tools

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The ways a 1.0.51 Dockhand signs in to a git server, as it names them. It refuses any other.
const (
	gitNone     = "none"
	gitPassword = "password"
	gitSSH      = "ssh"
)

const (
	// mainBranch is the branch Dockhand follows for a repository saved with none
	mainBranch = "main"
	// defaultComposePath is where Dockhand looks for a git stack's compose file unless told
	defaultComposePath = "compose.yaml"
)

var (
	// privateKey tells a text that is a private key, as ssh-keygen and openssl write one
	privateKey = regexp.MustCompile(`-{5}BEGIN [A-Z ]*PRIVATE KEY-{5}`)
	// userOnly finds the user part of a web address that carries no password, https://user@host, which is where an access token is often written
	userOnly = regexp.MustCompile(`^(https?://)[^\s:/@]+@`)
)

// gitAddress is a repository's address as it is shown: with a password in it hidden, and a user part that has no password hidden too, since that is
// where an access token is written. It reports whether anything was hidden.
func gitAddress(address string, reveal bool) (string, bool) {
	if reveal {
		return address, false
	}
	if shown, hidden := maskValue("", address); hidden {
		return shown, true
	}
	if userOnly.MatchString(address) {
		return userOnly.ReplaceAllString(address, "${1}"+masked+"@"), true
	}

	return address, false
}

// gitSaid is what git or Dockhand said went wrong, with any secret in it hidden, to go in the middle of a sentence.
func gitSaid(said string) string {
	shown, _ := maskText(strings.TrimSpace(said))

	return reasonOf(shown)
}

// oneOf is the one thing of a list a call names, by its number or by its name. Dockhand keeps two whose names differ only by case, so a name is
// matched letter for letter first, and whatever its case only when that is one thing alone. kind is what such a thing is called.
func oneOf[T any](listed []T, given any, what, kind string, id func(*T) int, name func(*T) string) (*T, error) {
	wanted, err := named(given, what)
	if err != nil {
		return nil, err
	}
	if wanted == "" {
		return nil, fmt.Errorf("%s is required: the %s's name or number", what, kind)
	}
	var near []*T
	names := make([]string, 0, len(listed))
	for i := range listed {
		if strconv.Itoa(id(&listed[i])) == wanted || strings.TrimSpace(name(&listed[i])) == wanted {
			return &listed[i], nil
		}
		if strings.EqualFold(strings.TrimSpace(name(&listed[i])), wanted) {
			near = append(near, &listed[i])
		}
		names = append(names, strings.TrimSpace(name(&listed[i])))
	}
	switch len(near) {
	case 0:
		return nil, fmt.Errorf("no %s is named or numbered %q: the Dockhand keeps %s", kind, wanted, cmp.Or(strings.Join(names, ", "), "none"))
	case 1:
		return near[0], nil
	default:
		numbers := make([]string, 0, len(near))
		for _, one := range near {
			numbers = append(numbers, name(one)+" ("+strconv.Itoa(id(one))+")")
		}

		return nil, fmt.Errorf("no %s is named %q letter for letter, and %d are but for their case: give the number of one, %s", kind, wanted, len(near), strings.Join(numbers, " or "))
	}
}

// repositoriesCounted counts repositories in words: 1 repository, 3 repositories.
func repositoriesCounted(n int) string {
	if n == 1 {
		return "1 repository"
	}

	return strconv.Itoa(n) + " repositories"
}

// gitCredentialArg names one git credential the Dockhand keeps.
type gitCredentialArg struct {
	Credential any `json:"credential,omitempty" jsonschema:"the git credential, by its name or its number as git_credential_list gives them"`
}

// gitRepositoryArg names one git repository the Dockhand keeps.
type gitRepositoryArg struct {
	Repository any `json:"repository,omitempty" jsonschema:"the git repository, by its name or its number as git_repository_list gives them"`
}

// gitCredentialRow is a git credential as the tools give it: never with its password or key, which Dockhand does not give back.
type gitCredentialRow struct {
	ID           int      `json:"id"`
	Name         string   `json:"name"`
	Kind         string   `json:"kind"                   jsonschema:"how it signs in: password with a user name and a password or access token, ssh with a private key, none with nothing"`
	Username     string   `json:"username,omitempty"`
	HasPassword  bool     `json:"has_password,omitempty" jsonschema:"Dockhand keeps a password or token for it, which it never gives back"`
	HasKey       bool     `json:"has_key,omitempty"      jsonschema:"Dockhand keeps a private key for it, which it never gives back"`
	Repositories []string `json:"repositories,omitempty" jsonschema:"the repositories fetched with it, by name"`
	Created      string   `json:"created,omitempty"`
	Changed      string   `json:"changed,omitempty"`
}

// gitStackUse is one git stack deployed from a repository.
type gitStackUse struct {
	Env   string `json:"env"`
	Stack string `json:"stack"`
}

// gitRepositoryRow is a git repository as the tools give it.
type gitRepositoryRow struct {
	ID         int           `json:"id"`
	Name       string        `json:"name"`
	URL        string        `json:"url"                  jsonschema:"where Dockhand fetches it from, as Dockhand's own container reaches it; a password or token written into it is hidden"`
	Branch     string        `json:"branch"               jsonschema:"the branch a git stack follows unless it names one of its own"`
	Credential string        `json:"credential,omitempty" jsonschema:"the credential it is fetched with, by name; absent for a repository anyone may read"`
	Stacks     []gitStackUse `json:"stacks,omitempty"     jsonschema:"the git stacks deployed from it"`
	Created    string        `json:"created,omitempty"`
	Changed    string        `json:"changed,omitempty"`
}

// gitCredentials is every git credential the Dockhand keeps.
func (r *registry) gitCredentials(ctx context.Context) ([]dockhand.GetGitCredentialsResponse, error) {
	listed, err := r.dock.GetGitCredentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("the git credentials could not be listed: %w", refusal(err))
	}

	return listed.Model, nil
}

// gitRepositories is every git repository the Dockhand keeps.
func (r *registry) gitRepositories(ctx context.Context) ([]dockhand.GetGitRepositoriesResponse, error) {
	listed, err := r.dock.GetGitRepositories(ctx)
	if err != nil {
		return nil, fmt.Errorf("the git repositories could not be listed: %w", refusal(err))
	}

	return listed.Model, nil
}

// gitStacks is every git stack the Dockhand keeps, on whichever environment.
func (r *registry) gitStacks(ctx context.Context) ([]dockhand.GetGitStacksResponse, error) {
	listed, err := r.dock.GetGitStacks(ctx, dockhand.GetGitStacksOperationOptions{})
	if err != nil {
		return nil, fmt.Errorf("the git stacks could not be listed: %w", refusal(err))
	}

	return listed.Model, nil
}

// How a credential and a repository are told apart and put in order: by number, and by name whatever its case.
func gitCredentialID(c *dockhand.GetGitCredentialsResponse) int { return c.Id }

func gitCredentialName(c *dockhand.GetGitCredentialsResponse) string { return c.Name }

func gitRepositoryID(p *dockhand.GetGitRepositoriesResponse) int { return p.Id }

func gitRepositoryName(p *dockhand.GetGitRepositoriesResponse) string { return p.Name }

func credentialsByName(a, b gitCredentialRow) int {
	return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.ID, b.ID))
}

func repositoriesByName(a, b gitRepositoryRow) int {
	return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.ID, b.ID))
}

// gitCredentialRowOf is a credential as the tools give it, with the repositories of a list that are fetched with it.
func gitCredentialRowOf(c *dockhand.GetGitCredentialsResponse, repositories []dockhand.GetGitRepositoriesResponse) gitCredentialRow {
	row := gitCredentialRow{ID: c.Id, Name: c.Name, Kind: c.AuthType, Username: c.Username, HasPassword: yes(c.HasPassword), HasKey: yes(c.HasSshKey), Created: c.CreatedAt, Changed: c.UpdatedAt}
	for i := range repositories {
		if repositories[i].CredentialId == c.Id {
			row.Repositories = append(row.Repositories, repositories[i].Name)
		}
	}
	slices.Sort(row.Repositories)

	return row
}

// stacksFrom is the git stacks of a list that are deployed from a repository, each with the name of its environment.
func stacksFrom(repository int, stacks []dockhand.GetGitStacksResponse, envs []environment) []gitStackUse {
	var uses []gitStackUse
	for i := range stacks {
		if stacks[i].RepositoryId != repository {
			continue
		}
		use := gitStackUse{Env: noEnvironment, Stack: stacks[i].StackName}
		if stacks[i].EnvironmentId != 0 {
			at := slices.IndexFunc(envs, func(e environment) bool { return e.ID == stacks[i].EnvironmentId })
			use.Env = nameOr(at, stacks[i].EnvironmentId, func(i int) string { return envs[i].Name })
		}
		uses = append(uses, use)
	}
	slices.SortFunc(uses, func(a, b gitStackUse) int { return cmp.Or(cmp.Compare(a.Env, b.Env), cmp.Compare(a.Stack, b.Stack)) })

	return uses
}

// stacksSaid is the git stacks deployed from a repository in words: web on alpha, mail on beta.
func stacksSaid(uses []gitStackUse) string {
	said := make([]string, 0, len(uses))
	for _, use := range uses {
		said = append(said, use.Stack+" on "+use.Env)
	}

	return strings.Join(said, ", ")
}

// gitRepositoryRowOf is a repository as the tools give it, with the name of its credential and the git stacks deployed from it.
func gitRepositoryRowOf(p *dockhand.GetGitRepositoriesResponse, credentials []dockhand.GetGitCredentialsResponse, stacks []dockhand.GetGitStacksResponse, envs []environment, reveal bool) (gitRepositoryRow, bool) {
	address, hidden := gitAddress(p.Url, reveal)
	row := gitRepositoryRow{ID: p.Id, Name: p.Name, URL: address, Branch: p.Branch, Stacks: stacksFrom(p.Id, stacks, envs), Created: p.CreatedAt, Changed: p.UpdatedAt}
	if i := slices.IndexFunc(credentials, func(c dockhand.GetGitCredentialsResponse) bool { return c.Id == p.CredentialId }); i >= 0 {
		row.Credential = credentials[i].Name
	}

	return row, hidden
}

// gitTried is what trying a repository found.
type gitTried struct {
	Reached bool
	Branch  string
	Commit  string
	Error   string
}

// tryRepository has Dockhand reach for a repository that is not saved, or not saved this way yet, and says what it found.
func (r *registry) tryRepository(ctx context.Context, address, branch string, credential int) (gitTried, error) {
	tried, err := r.dock.PostGitRepositoriesTest(ctx, dockhand.PostGitRepositoriesTestRequest{Url: address, Branch: branch, CredentialId: credential})
	if err != nil {
		return gitTried{}, refusal(err)
	}

	return gitTried{Reached: yes(tried.Model.Success), Branch: tried.Model.Branch, Commit: tried.Model.LastCommit, Error: tried.Model.Error}, nil
}

// embedsSecret is the note for an address with a password or token written into it, or nothing for one without.
func embedsSecret(address string) string {
	if _, hidden := gitAddress(address, false); !hidden {
		return ""
	}

	return "the address has a password or token written into it, which Dockhand gives back whole to whoever lists its repositories: a credential keeps one where it is never given back"
}

//nolint:gocyclo,maintidx // the git credential and repository tools registered in one function, each a handler of its own: the count is theirs added up
func registerGitTools(r *registry) {
	type gitCredentialListIn struct {
		gitCredentialArg
	}
	type gitCredentialListOut struct {
		Credentials []gitCredentialRow `json:"credentials"`
		Total       int                `json:"total"`
		Note        string             `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "git_credential_list",
		Description: "List the git credentials the Dockhand keeps, or one: what it signs in to a git server with to fetch a repository that not everyone may read. Each is given with how it signs in, its user name, whether a password or a private key is kept for it, and the repositories fetched with it. The password and the key themselves are never given: Dockhand keeps them and does not give them back.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in gitCredentialListIn) (*mcp.CallToolResult, gitCredentialListOut, error) {
		listed, err := r.gitCredentials(ctx)
		if err != nil {
			return nil, gitCredentialListOut{}, err
		}
		if in.Credential != nil {
			one, unknown := oneOf(listed, in.Credential, "credential", "git credential", gitCredentialID, gitCredentialName)
			if unknown != nil {
				return nil, gitCredentialListOut{}, unknown
			}
			listed = []dockhand.GetGitCredentialsResponse{*one}
		}
		out := gitCredentialListOut{Credentials: make([]gitCredentialRow, 0, len(listed))}
		if len(listed) == 0 {
			out.Note = "the Dockhand keeps no git credential: it can fetch only repositories anyone may read"

			return nil, out, nil
		}
		repositories, err := r.gitRepositories(ctx)
		if err != nil {
			return nil, out, err
		}
		for i := range listed {
			out.Credentials = append(out.Credentials, gitCredentialRowOf(&listed[i], repositories))
		}
		slices.SortFunc(out.Credentials, credentialsByName)
		out.Total = len(out.Credentials)

		return nil, out, nil
	})

	type gitCredentialOut struct {
		Credential *gitCredentialRow `json:"credential,omitempty" jsonschema:"the credential as it is now, read back; absent for a dry run of making one"`
		Changes    []string          `json:"changes"              jsonschema:"what was done, in words; empty when nothing needed doing"`
		DryRun     bool              `json:"dry_run,omitempty"    jsonschema:"nothing was done: changes says what would be"`
		Note       string            `json:"note,omitempty"`
	}
	// credentialBack is a credential as it stands after a change, with the note that it could not be read when it could not
	credentialBack := func(ctx context.Context, out *gitCredentialOut, id int) {
		got, err := r.dock.GetGitCredentialsById(ctx, id)
		if err != nil {
			out.Note = noted(out.Note, "it was done, and could not be read back: "+refusal(err).Error())

			return
		}
		repositories, err := r.gitRepositories(ctx)
		if err != nil {
			out.Note = noted(out.Note, "the repositories fetched with it could not be listed: "+err.Error())
		}
		row := gitCredentialRowOf(&dockhand.GetGitCredentialsResponse{Id: got.Model.Id, Name: got.Model.Name, AuthType: got.Model.AuthType, Username: got.Model.Username, HasPassword: got.Model.HasPassword, HasSshKey: got.Model.HasSshKey, CreatedAt: got.Model.CreatedAt, UpdatedAt: got.Model.UpdatedAt}, repositories)
		out.Credential = &row
	}
	// signsInWith checks that a credential of a kind has what that kind signs in with: given now, or kept already
	signsInWith := func(kind, password, key string, hasPassword, hasKey bool) error {
		switch {
		case kind != gitNone && kind != gitPassword && kind != gitSSH:
			return fmt.Errorf("kind is %q: a git credential signs in with password, ssh or none", kind)
		case kind == gitPassword && password == "" && !hasPassword:
			return errors.New("password is required for a credential that signs in with one: the password, or an access token")
		case kind == gitSSH && key == "" && !hasKey:
			return errors.New("ssh_key is required for a credential that signs in over ssh: the private key's text, whole")
		case key != "" && !privateKey.MatchString(key):
			return errors.New("ssh_key is not a private key: it is the key's text, whole, from its BEGIN line to its END line, which Dockhand would save whatever it held")
		default:
			return nil
		}
	}

	type gitCredentialCreateIn struct {
		dryRunArg

		Name       string `json:"name"                     jsonschema:"what to call the credential: the name the tools then take, which no other credential has letter for letter"`
		Kind       string `json:"kind"                     jsonschema:"how it signs in: password, ssh or none"`
		Username   string `json:"username,omitempty"       jsonschema:"for password: the user name the git server takes; with an access token, whatever that server asks for beside one"`
		Password   string `json:"password,omitempty"       jsonschema:"for password: the password or access token. It is sent to Dockhand and kept there, and is never given back"`
		SSHKey     string `json:"ssh_key,omitempty"        jsonschema:"for ssh: the private key's text, whole, from its BEGIN line to its END line. It is sent to Dockhand and kept there, and is never given back"`
		Passphrase string `json:"ssh_passphrase,omitempty" jsonschema:"for ssh: the passphrase the key is locked with, when it is"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "git_credential_create",
		Description: "Save a git credential on the Dockhand: a user name with a password or access token, or a private key for ssh, that a repository is then fetched with. It is not tried here, since a credential is tried against a repository: git_repository_test with it does that before anything is saved to use it. Nothing uses it until a repository is given it.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in gitCredentialCreateIn) (*mcp.CallToolResult, gitCredentialOut, error) {
		name, kind := strings.TrimSpace(in.Name), strings.TrimSpace(in.Kind)
		if name == "" {
			return nil, gitCredentialOut{}, errors.New("name is required: what to call the credential")
		}
		if err := signsInWith(kind, in.Password, in.SSHKey, false, false); err != nil {
			return nil, gitCredentialOut{}, err
		}
		for _, misplaced := range []struct {
			given bool
			what  string
			kind  string
		}{{in.Password != "" || in.Username != "", "username and password", gitPassword}, {in.SSHKey != "" || in.Passphrase != "", "ssh_key and ssh_passphrase", gitSSH}} {
			if misplaced.given && kind != misplaced.kind {
				return nil, gitCredentialOut{}, fmt.Errorf("%s are for a credential of the kind %s, and this one is %s: Dockhand would keep them unused", misplaced.what, misplaced.kind, kind)
			}
		}
		listed, err := r.gitCredentials(ctx)
		if err != nil {
			return nil, gitCredentialOut{}, err
		}
		if slices.ContainsFunc(listed, func(c dockhand.GetGitCredentialsResponse) bool { return strings.TrimSpace(c.Name) == name }) {
			return nil, gitCredentialOut{}, fmt.Errorf("a git credential is already named %s: git_credential_edit changes it, and a new one takes a name of its own", name)
		}
		out := gitCredentialOut{Changes: []string{}, DryRun: in.DryRun}
		if in.DryRun {
			out.Changes = append(out.Changes, fmt.Sprintf("would save the git credential %s, which signs in with %s", name, kind))

			return nil, out, nil
		}

		made, err := r.dock.PostGitCredentials(ctx, dockhand.PostGitCredentialsRequest{Name: name, AuthType: kind, Username: strings.TrimSpace(in.Username), Password: in.Password, SshPrivateKey: in.SSHKey, SshPassphrase: in.Passphrase})
		if err != nil {
			return nil, out, fmt.Errorf("the git credential %s was not saved: %w", name, refusal(err))
		}
		out.Changes = append(out.Changes, fmt.Sprintf("saved the git credential %s, which signs in with %s", name, kind))
		credentialBack(ctx, &out, made.Model.Id)

		return nil, out, nil
	})

	type gitCredentialEditIn struct {
		gitCredentialArg
		dryRunArg

		Name       string  `json:"name,omitempty"           jsonschema:"another name for the credential"`
		Kind       string  `json:"kind,omitempty"           jsonschema:"another way for it to sign in: password, ssh or none. What it signed in with before is kept by Dockhand, unused"`
		Username   *string `json:"username,omitempty"       jsonschema:"another user name"`
		Password   string  `json:"password,omitempty"       jsonschema:"another password or access token, in place of the one kept"`
		SSHKey     string  `json:"ssh_key,omitempty"        jsonschema:"another private key, whole, in place of the one kept"`
		Passphrase string  `json:"ssh_passphrase,omitempty" jsonschema:"the passphrase the new key is locked with"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "git_credential_edit",
		Description: "Change a git credential the Dockhand keeps: its name, how it signs in, its user name, and its password, token or key. What is left out is kept. A password or key is never read back, so one given is always saved again. Every repository fetched with the credential signs in with what is saved from then on, so each is tried afterwards and the ones that can no longer be fetched are named.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in gitCredentialEditIn) (*mcp.CallToolResult, gitCredentialOut, error) {
		name, kind := strings.TrimSpace(in.Name), strings.TrimSpace(in.Kind)
		if name == "" && kind == "" && in.Username == nil && in.Password == "" && in.SSHKey == "" && in.Passphrase == "" {
			return nil, gitCredentialOut{}, errors.New("nothing to change was given: name, kind, username, password, ssh_key or ssh_passphrase")
		}
		listed, err := r.gitCredentials(ctx)
		if err != nil {
			return nil, gitCredentialOut{}, err
		}
		credential, err := oneOf(listed, in.Credential, "credential", "git credential", gitCredentialID, gitCredentialName)
		if err != nil {
			return nil, gitCredentialOut{}, err
		}
		if name != "" && name != credential.Name && slices.ContainsFunc(listed, func(c dockhand.GetGitCredentialsResponse) bool { return strings.TrimSpace(c.Name) == name }) {
			return nil, gitCredentialOut{}, fmt.Errorf("a git credential is already named %s", name)
		}
		if err := signsInWith(cmp.Or(kind, credential.AuthType), in.Password, in.SSHKey, yes(credential.HasPassword), yes(credential.HasSshKey)); err != nil {
			return nil, gitCredentialOut{}, err
		}
		out := gitCredentialOut{Changes: []string{}, DryRun: in.DryRun}
		does := func(did, would string) string {
			if in.DryRun {
				return would
			}

			return did
		}

		request := dockhand.PutGitCredentialsByIdRequest{}
		signsIn := false
		if name != "" && name != credential.Name {
			request.Name = name
			out.Changes = append(out.Changes, fmt.Sprintf("%s it from %s to %s", does("renamed", "would rename"), credential.Name, name))
		}
		if kind != "" && kind != credential.AuthType {
			request.AuthType, signsIn = kind, true
			out.Changes = append(out.Changes, fmt.Sprintf("%s how it signs in from %s to %s", does("changed", "would change"), credential.AuthType, kind))
		}
		if in.Username != nil && strings.TrimSpace(*in.Username) != credential.Username {
			if strings.TrimSpace(*in.Username) == "" {
				return nil, out, errors.New("username is empty: Dockhand keeps the user name it is not given another for, and takes none away")
			}
			request.Username, signsIn = strings.TrimSpace(*in.Username), true
			out.Changes = append(out.Changes, fmt.Sprintf("%s its user name to %s", does("changed", "would change"), request.Username))
		}
		if in.Password != "" {
			request.Password, signsIn = in.Password, true
			out.Changes = append(out.Changes, does("saved", "would save")+" a new password")
		}
		if in.SSHKey != "" || in.Passphrase != "" {
			request.SshPrivateKey, request.SshPassphrase, signsIn = in.SSHKey, in.Passphrase, true
			out.Changes = append(out.Changes, does("saved", "would save")+" a new private key")
		}
		if len(out.Changes) == 0 {
			out.Note = "nothing to do: it is that way already"
			credentialBack(ctx, &out, credential.Id)

			return nil, out, nil
		}
		if in.DryRun {
			credentialBack(ctx, &out, credential.Id)

			return nil, out, nil
		}

		if _, err := r.dock.PutGitCredentialsById(ctx, credential.Id, request); err != nil {
			return nil, gitCredentialOut{Changes: []string{}}, fmt.Errorf("the git credential %s was not changed: %w", credential.Name, refusal(err))
		}
		credentialBack(ctx, &out, credential.Id)

		// every repository fetched with it signs in with what was just saved
		if signsIn && out.Credential != nil && len(out.Credential.Repositories) > 0 {
			repositories, _ := r.gitRepositories(ctx)
			var lost []string
			for i := range repositories {
				if repositories[i].CredentialId != credential.Id {
					continue
				}
				tried, err := r.dock.PostGitRepositoriesByIdTest(ctx, repositories[i].Id)
				switch {
				case err != nil:
					lost = append(lost, fmt.Sprintf("%s (could not be tried: %s)", repositories[i].Name, refusal(err)))
				case !yes(tried.Model.Success):
					lost = append(lost, fmt.Sprintf("%s (%s)", repositories[i].Name, gitSaid(tried.Model.Error)))
				default:
				}
			}
			switch {
			case len(lost) == len(out.Credential.Repositories):
				out.Note = noted(out.Note, fmt.Sprintf("%s fetched with it can no longer be fetched: %s", repositoriesCounted(len(lost)), strings.Join(lost, "; ")))
			case len(lost) > 0:
				out.Note = noted(out.Note, fmt.Sprintf("%d of the %s fetched with it can no longer be fetched: %s", len(lost), repositoriesCounted(len(out.Credential.Repositories)), strings.Join(lost, "; ")))
			default:
			}
		}

		return nil, out, nil
	})

	type gitCredentialDeleteIn struct {
		gitCredentialArg

		Confirm  bool `json:"confirm,omitempty"   jsonschema:"remove it: without this the answer says what would go and nothing is removed"`
		EvenUsed bool `json:"even_used,omitempty" jsonschema:"remove it though repositories are fetched with it. Each is then fetched with no credential, which a repository that asks for one refuses"`
	}
	type gitCredentialDeleteOut struct {
		Credential   string   `json:"credential"`
		Repositories []string `json:"repositories,omitempty" jsonschema:"the repositories fetched with it"`
		Removed      bool     `json:"removed"                jsonschema:"the credential was removed, and is read back as gone"`
		WouldGo      []string `json:"would_go,omitempty"     jsonschema:"what removing it takes away, said before anything is removed"`
		Note         string   `json:"note,omitempty"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "git_credential_delete",
		Description: "Remove a git credential from the Dockhand. Without confirm the answer says what would go and removes nothing. Dockhand removes one that repositories are fetched with without a word and leaves each with no credential, so that the next fetch of a repository that asks for one fails, and with it every deploy of a git stack from that repository: so a credential in use is removed only with even_used, after the repositories are named.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in gitCredentialDeleteIn) (*mcp.CallToolResult, gitCredentialDeleteOut, error) {
		listed, err := r.gitCredentials(ctx)
		if err != nil {
			return nil, gitCredentialDeleteOut{}, err
		}
		credential, err := oneOf(listed, in.Credential, "credential", "git credential", gitCredentialID, gitCredentialName)
		if err != nil {
			return nil, gitCredentialDeleteOut{}, err
		}
		repositories, err := r.gitRepositories(ctx)
		if err != nil {
			return nil, gitCredentialDeleteOut{}, fmt.Errorf("%w, so nothing was removed", err)
		}
		row := gitCredentialRowOf(credential, repositories)
		out := gitCredentialDeleteOut{Credential: credential.Name, Repositories: row.Repositories}
		out.WouldGo = append(out.WouldGo, fmt.Sprintf("the git credential %s, with what Dockhand keeps for it to sign in with", credential.Name))
		if len(row.Repositories) > 0 {
			out.WouldGo = append(out.WouldGo, fmt.Sprintf("the credential of %s: %s. Each is left with none", repositoriesCounted(len(row.Repositories)), strings.Join(row.Repositories, ", ")))
		}
		if !in.Confirm {
			out.Note = "nothing was removed: confirm removes it"
			if len(row.Repositories) > 0 {
				out.Note = "nothing was removed: confirm with even_used removes it"
			}

			return nil, out, nil
		}
		if len(row.Repositories) > 0 && !in.EvenUsed {
			return nil, out, fmt.Errorf("%s signs in for %s: %s. Dockhand would leave each with no credential without a word: give each another first, or even_used removes it all the same", credential.Name, repositoriesCounted(len(row.Repositories)), strings.Join(row.Repositories, ", "))
		}

		if _, err := r.dock.DeleteGitCredentialsById(ctx, credential.Id); err != nil {
			return nil, out, fmt.Errorf("the git credential %s was not removed: %w", credential.Name, refusal(err))
		}
		after, err := r.gitCredentials(ctx)
		switch {
		case err != nil:
			out.Removed, out.WouldGo = true, nil
			out.Note = "it was removed, and the credentials could not be listed afterwards: " + err.Error()
		case slices.ContainsFunc(after, func(c dockhand.GetGitCredentialsResponse) bool { return c.Id == credential.Id }):
			return nil, out, fmt.Errorf("Dockhand answered that %s was removed, and still lists it", credential.Name) //nolint:staticcheck // ST1005: the sentence begins with the server's name
		default:
			out.Removed, out.WouldGo = true, nil
			if len(row.Repositories) > 0 {
				out.Note = fmt.Sprintf("%s left with no credential: %s", repositoriesCounted(len(row.Repositories)), strings.Join(row.Repositories, ", "))
			}
		}

		return nil, out, nil
	})

	type gitRepositoryListIn struct {
		gitRepositoryArg
		revealArg
	}
	type gitRepositoryListOut struct {
		Repositories []gitRepositoryRow `json:"repositories"`
		Total        int                `json:"total"`
		Hidden       int                `json:"hidden,omitempty" jsonschema:"how many addresses have a password or token in them hidden"`
		Note         string             `json:"note,omitempty"`
	}
	// repositoryRows is the repositories given as the tools give them, with everything each is told with read once
	repositoryRows := func(ctx context.Context, listed []dockhand.GetGitRepositoriesResponse, reveal bool) ([]gitRepositoryRow, int, error) {
		credentials, err := r.gitCredentials(ctx)
		if err != nil {
			return nil, 0, err
		}
		stacks, err := r.gitStacks(ctx)
		if err != nil {
			return nil, 0, err
		}
		envs, err := r.environments(ctx)
		if err != nil {
			return nil, 0, err
		}
		rows, hidden := make([]gitRepositoryRow, 0, len(listed)), 0
		for i := range listed {
			row, masked := gitRepositoryRowOf(&listed[i], credentials, stacks, envs, reveal)
			if masked {
				hidden++
			}
			rows = append(rows, row)
		}
		slices.SortFunc(rows, repositoriesByName)

		return rows, hidden, nil
	}
	add(r, readTool, &mcp.Tool{
		Name:        "git_repository_list",
		Description: "List the git repositories the Dockhand keeps, or one: where each is fetched from, the branch followed, the credential it is fetched with, and the git stacks deployed from it. A repository is only an address and a way in: what is deployed from it, and where, is a git stack's, which git_stack_list gives.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in gitRepositoryListIn) (*mcp.CallToolResult, gitRepositoryListOut, error) {
		listed, err := r.gitRepositories(ctx)
		if err != nil {
			return nil, gitRepositoryListOut{}, err
		}
		if in.Repository != nil {
			one, unknown := oneOf(listed, in.Repository, "repository", "git repository", gitRepositoryID, gitRepositoryName)
			if unknown != nil {
				return nil, gitRepositoryListOut{}, unknown
			}
			listed = []dockhand.GetGitRepositoriesResponse{*one}
		}
		out := gitRepositoryListOut{Repositories: []gitRepositoryRow{}}
		if len(listed) == 0 {
			out.Note = "the Dockhand keeps no git repository: none of its stacks is deployed from git"

			return nil, out, nil
		}
		if out.Repositories, out.Hidden, err = repositoryRows(ctx, listed, in.RevealSecrets); err != nil {
			return nil, gitRepositoryListOut{Repositories: []gitRepositoryRow{}}, err
		}
		out.Total = len(out.Repositories)
		if out.Hidden > 0 {
			out.Note = hiddenNote(out.Hidden, "written into an address")
		}

		return nil, out, nil
	})

	type gitBranch struct {
		Name   string `json:"name"`
		Commit string `json:"commit" jsonschema:"its newest commit, shortened"`
	}
	type gitRepositoryTestIn struct {
		gitRepositoryArg

		URL        string `json:"url,omitempty"        jsonschema:"to try a repository that is not saved: its address, as Dockhand's own container reaches it"`
		Branch     string `json:"branch,omitempty"     jsonschema:"with url: the branch to look for; main unless said"`
		Credential any    `json:"credential,omitempty" jsonschema:"with url: a git credential the Dockhand keeps to sign in with, by name or number"`
	}
	type gitRepositoryTestOut struct {
		Repository string      `json:"repository,omitempty" jsonschema:"the repository that was tried; absent for one that is not saved"`
		Reached    bool        `json:"reached"              jsonschema:"Dockhand reached the repository, was let in, and found the branch"`
		Branch     string      `json:"branch,omitempty"     jsonschema:"the branch that was looked for"`
		Commit     string      `json:"commit,omitempty"     jsonschema:"the newest commit on it, shortened"`
		Branches   []gitBranch `json:"branches,omitempty"   jsonschema:"every branch the repository has, when it could be asked"`
		Error      string      `json:"error,omitempty"      jsonschema:"what Dockhand says went wrong, when it did not"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "git_repository_test",
		Description: "Have the Dockhand reach for a git repository and say whether it could: one it keeps, by repository, as it is saved; or one that is not saved yet, by url with a branch and a credential. Says the branch and its newest commit, and every branch the repository has. Nothing is saved, fetched or changed. Dockhand makes the call itself, so the address is one its own container can reach. A git stack whose repository cannot be reached is not deployed, so this is the first thing to ask when one fails.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in gitRepositoryTestIn) (*mcp.CallToolResult, gitRepositoryTestOut, error) {
		address := strings.TrimSpace(in.URL)
		if in.Repository == nil {
			if address == "" {
				return nil, gitRepositoryTestOut{}, errors.New("repository or url is required: a repository the Dockhand keeps, or the address of one that is not saved")
			}
			credential := 0
			if in.Credential != nil {
				listed, err := r.gitCredentials(ctx)
				if err != nil {
					return nil, gitRepositoryTestOut{}, err
				}
				one, err := oneOf(listed, in.Credential, "credential", "git credential", gitCredentialID, gitCredentialName)
				if err != nil {
					return nil, gitRepositoryTestOut{}, err
				}
				credential = one.Id
			}
			tried, err := r.tryRepository(ctx, address, strings.TrimSpace(in.Branch), credential)
			if err != nil {
				return nil, gitRepositoryTestOut{}, fmt.Errorf("the repository could not be tried: %w", err)
			}
			out := gitRepositoryTestOut{Reached: tried.Reached, Branch: cmp.Or(tried.Branch, strings.TrimSpace(in.Branch), mainBranch), Commit: tried.Commit, Error: gitSaid(tried.Error)}
			if tried.Reached {
				out.Error = ""
			}
			if branches, err := r.dock.PostGitBranches(ctx, dockhand.PostGitBranchesRequest{Url: address, CredentialId: credential}); err == nil {
				for _, b := range branches.Model.Branches {
					out.Branches = append(out.Branches, gitBranch{Name: b.Name, Commit: b.Sha})
				}
			}

			return nil, out, nil
		}
		if address != "" || in.Credential != nil || strings.TrimSpace(in.Branch) != "" {
			return nil, gitRepositoryTestOut{}, errors.New("url, branch and credential are for a repository that is not saved: one the Dockhand keeps is tried as it is saved")
		}
		listed, err := r.gitRepositories(ctx)
		if err != nil {
			return nil, gitRepositoryTestOut{}, err
		}
		repository, err := oneOf(listed, in.Repository, "repository", "git repository", gitRepositoryID, gitRepositoryName)
		if err != nil {
			return nil, gitRepositoryTestOut{}, err
		}
		tried, err := r.dock.PostGitRepositoriesByIdTest(ctx, repository.Id)
		if err != nil {
			return nil, gitRepositoryTestOut{}, fmt.Errorf("the repository %s could not be tried: %w", repository.Name, refusal(err))
		}
		out := gitRepositoryTestOut{Repository: repository.Name, Reached: yes(tried.Model.Success), Branch: cmp.Or(tried.Model.Branch, repository.Branch), Commit: tried.Model.LastCommit}
		if !out.Reached {
			out.Error = gitSaid(tried.Model.Error)
		}
		if branches, err := r.dock.PostGitBranches(ctx, dockhand.PostGitBranchesRequest{RepositoryId: repository.Id}); err == nil {
			for _, b := range branches.Model.Branches {
				out.Branches = append(out.Branches, gitBranch{Name: b.Name, Commit: b.Sha})
			}
		}

		return nil, out, nil
	})

	type gitVariable struct {
		Name  string `json:"name"`
		Value string `json:"value"          jsonschema:"its value, hidden when its name is a secret's"`
		From  string `json:"from,omitempty" jsonschema:"the file it is from: .env for the one beside the compose file, envFile for the one named with env_file"`
	}
	type gitRepositoryVariablesIn struct {
		gitRepositoryArg
		revealArg

		ComposePath string `json:"compose_path,omitempty" jsonschema:"the compose file's path in the repository, whose folder the .env file is looked for in; compose.yaml unless said"`
		EnvFile     string `json:"env_file,omitempty"     jsonschema:"one more .env file in the repository to read, by its path there"`
	}
	type gitRepositoryVariablesOut struct {
		Repository string        `json:"repository"`
		Variables  []gitVariable `json:"variables"`
		Total      int           `json:"total"`
		Hidden     int           `json:"hidden,omitempty" jsonschema:"how many values are hidden for having a secret's name"`
		Note       string        `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "git_repository_variables",
		Description: "Read the variables a git repository the Dockhand keeps would fill a compose file in with: the ones in the .env file beside the compose file, and in one more .env file when it is named. Dockhand fetches the repository into a folder of its own to read them and removes the folder again; nothing is saved or deployed. It is how to see what a git stack made from the repository starts with, and which of its variables are left for Dockhand to be given. A compose file that is not there is not noticed: only the .env files are read.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in gitRepositoryVariablesIn) (*mcp.CallToolResult, gitRepositoryVariablesOut, error) {
		listed, err := r.gitRepositories(ctx)
		if err != nil {
			return nil, gitRepositoryVariablesOut{}, err
		}
		repository, err := oneOf(listed, in.Repository, "repository", "git repository", gitRepositoryID, gitRepositoryName)
		if err != nil {
			return nil, gitRepositoryVariablesOut{}, err
		}
		out := gitRepositoryVariablesOut{Repository: repository.Name, Variables: []gitVariable{}}
		read, err := r.dock.PostGitPreviewEnv(ctx, dockhand.PostGitPreviewEnvRequest{RepositoryId: repository.Id, ComposePath: cmp.Or(strings.TrimSpace(in.ComposePath), defaultComposePath), EnvFilePath: strings.TrimSpace(in.EnvFile)})
		if err != nil {
			shown, _ := maskText(refusal(err).Error())

			return nil, out, fmt.Errorf("the variables of %s could not be read: %s", repository.Name, shown)
		}
		for _, name := range slices.Sorted(maps.Keys(read.Model.Vars)) {
			value, hidden := read.Model.Vars[name], false
			if !in.RevealSecrets {
				value, hidden = maskValue(name, value)
			}
			if hidden {
				out.Hidden++
			}
			out.Variables = append(out.Variables, gitVariable{Name: name, Value: value, From: read.Model.Sources[name]})
		}
		out.Total = len(out.Variables)
		switch {
		case out.Total == 0:
			out.Note = "the repository has no .env file there, or an empty one: a compose file there is filled in only with what Dockhand is given for the stack"
		case out.Hidden > 0:
			out.Note = hiddenNote(out.Hidden, "told by the names beside them")
		default:
		}

		return nil, out, nil
	})

	type gitRepositoryOut struct {
		Repository *gitRepositoryRow `json:"repository,omitempty" jsonschema:"the repository as it is now, read back; absent for a dry run of making one"`
		Commit     string            `json:"commit,omitempty"     jsonschema:"the newest commit on its branch, found when it was tried"`
		Changes    []string          `json:"changes"              jsonschema:"what was done, in words; empty when nothing needed doing"`
		DryRun     bool              `json:"dry_run,omitempty"    jsonschema:"nothing was done: changes says what would be"`
		Note       string            `json:"note,omitempty"`
	}
	// repositoryBack is a repository as it stands after a change, with the note that it could not be read when it could not
	repositoryBack := func(ctx context.Context, out *gitRepositoryOut, id int) {
		got, err := r.dock.GetGitRepositoriesById(ctx, id)
		if err != nil {
			out.Note = noted(out.Note, "it was done, and could not be read back: "+refusal(err).Error())

			return
		}
		rows, _, err := repositoryRows(ctx, []dockhand.GetGitRepositoriesResponse{{Id: got.Model.Id, Name: got.Model.Name, Url: got.Model.Url, Branch: got.Model.Branch, CredentialId: got.Model.CredentialId, CreatedAt: got.Model.CreatedAt, UpdatedAt: got.Model.UpdatedAt}}, false)
		if err != nil {
			out.Note = noted(out.Note, "it was done, and could not be read back: "+err.Error())

			return
		}
		out.Repository = &rows[0]
	}

	type gitRepositoryCreateIn struct {
		dryRunArg

		Name            string `json:"name"                       jsonschema:"what to call the repository: the name the tools then take, which no other repository has letter for letter"`
		URL             string `json:"url"                        jsonschema:"where to fetch it from, as Dockhand's own container reaches it: https://host/owner/name.git, or git@host:owner/name.git for ssh"`
		Branch          string `json:"branch,omitempty"           jsonschema:"the branch to follow; main unless said"`
		Credential      any    `json:"credential,omitempty"       jsonschema:"a git credential the Dockhand keeps to fetch it with, by name or number; left out for a repository anyone may read"`
		SaveUnreachable bool   `json:"save_unreachable,omitempty" jsonschema:"save it though Dockhand cannot reach it now, is not let in, or does not find the branch. Without this such a repository is not saved"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "git_repository_create",
		Description: "Save a git repository on the Dockhand: an address, the branch to follow and the credential to fetch it with, for git stacks to be deployed from. It is tried first, and one Dockhand cannot reach, is not let in to, or does not find the branch of is not saved unless save_unreachable says so: Dockhand left to itself saves any, and a git stack made from such a repository is never deployed. A password or token belongs in a credential rather than in the address, which Dockhand gives back whole. Nothing is fetched or deployed until a git stack is made from it.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in gitRepositoryCreateIn) (*mcp.CallToolResult, gitRepositoryOut, error) {
		name, address, branch := strings.TrimSpace(in.Name), strings.TrimSpace(in.URL), cmp.Or(strings.TrimSpace(in.Branch), mainBranch)
		switch {
		case name == "":
			return nil, gitRepositoryOut{}, errors.New("name is required: what to call the repository")
		case address == "":
			return nil, gitRepositoryOut{}, errors.New("url is required: where the repository is fetched from")
		default:
		}
		listed, err := r.gitRepositories(ctx)
		if err != nil {
			return nil, gitRepositoryOut{}, err
		}
		if slices.ContainsFunc(listed, func(p dockhand.GetGitRepositoriesResponse) bool { return strings.TrimSpace(p.Name) == name }) {
			return nil, gitRepositoryOut{}, fmt.Errorf("a git repository is already named %s: git_repository_edit changes it, and a new one takes a name of its own", name)
		}
		credential, with := 0, ""
		if in.Credential != nil {
			credentials, unlisted := r.gitCredentials(ctx)
			if unlisted != nil {
				return nil, gitRepositoryOut{}, unlisted
			}
			one, unknown := oneOf(credentials, in.Credential, "credential", "git credential", gitCredentialID, gitCredentialName)
			if unknown != nil {
				return nil, gitRepositoryOut{}, unknown
			}
			credential, with = one.Id, ", fetched with the credential "+one.Name
		}
		out := gitRepositoryOut{Changes: []string{}, DryRun: in.DryRun, Note: embedsSecret(address)}

		tried, err := r.tryRepository(ctx, address, branch, credential)
		if err != nil {
			return nil, out, fmt.Errorf("the repository could not be tried, so nothing was saved: %w", err)
		}
		out.Commit = tried.Commit
		if !tried.Reached {
			if !in.SaveUnreachable {
				return nil, out, fmt.Errorf("Dockhand could not fetch the repository, so nothing was saved: %s. save_unreachable saves it all the same", gitSaid(tried.Error)) //nolint:staticcheck // ST1005: the sentence begins with the server's name
			}
			out.Note = noted(out.Note, map[bool]string{true: "it would be saved", false: "it was saved"}[in.DryRun]+" as save_unreachable asks though Dockhand cannot fetch it: "+gitSaid(tried.Error))
		}
		shown, _ := gitAddress(address, false)
		if in.DryRun {
			out.Changes = append(out.Changes, fmt.Sprintf("would save the git repository %s, the branch %s of %s%s", name, branch, shown, with))

			return nil, out, nil
		}

		made, err := r.dock.PostGitRepositories(ctx, dockhand.PostGitRepositoriesRequest{Name: name, Url: address, Branch: branch, CredentialId: credential})
		if err != nil {
			return nil, out, fmt.Errorf("the git repository %s was not saved: %w", name, refusal(err))
		}
		out.Changes = append(out.Changes, fmt.Sprintf("saved the git repository %s, the branch %s of %s%s", name, branch, shown, with))
		repositoryBack(ctx, &out, made.Model.Id)

		return nil, out, nil
	})

	type gitRepositoryEditIn struct {
		gitRepositoryArg
		dryRunArg

		Name            string `json:"name,omitempty"             jsonschema:"another name for the repository"`
		URL             string `json:"url,omitempty"              jsonschema:"another address to fetch it from"`
		Branch          string `json:"branch,omitempty"           jsonschema:"another branch to follow"`
		Credential      any    `json:"credential,omitempty"       jsonschema:"another git credential to fetch it with, by name or number; an empty text has it fetched with none"`
		SaveUnreachable bool   `json:"save_unreachable,omitempty" jsonschema:"save the change though Dockhand cannot fetch the repository with it. Without this it is not saved"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "git_repository_edit",
		Description: "Change a git repository the Dockhand keeps: its name, its address, the branch followed, and the credential it is fetched with. What is left out is kept. A new address, branch or credential is tried before it is saved, and one Dockhand cannot fetch the repository with is refused unless save_unreachable says otherwise: every git stack deployed from the repository is fetched that way from then on, and is not deployed when it fails. Nothing running changes until such a stack is deployed again.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in gitRepositoryEditIn) (*mcp.CallToolResult, gitRepositoryOut, error) {
		name, address, branch := strings.TrimSpace(in.Name), strings.TrimSpace(in.URL), strings.TrimSpace(in.Branch)
		if name == "" && address == "" && branch == "" && in.Credential == nil {
			return nil, gitRepositoryOut{}, errors.New("nothing to change was given: name, url, branch or credential")
		}
		listed, err := r.gitRepositories(ctx)
		if err != nil {
			return nil, gitRepositoryOut{}, err
		}
		repository, err := oneOf(listed, in.Repository, "repository", "git repository", gitRepositoryID, gitRepositoryName)
		if err != nil {
			return nil, gitRepositoryOut{}, err
		}
		if name != "" && name != repository.Name && slices.ContainsFunc(listed, func(p dockhand.GetGitRepositoriesResponse) bool { return strings.TrimSpace(p.Name) == name }) {
			return nil, gitRepositoryOut{}, fmt.Errorf("a git repository is already named %s", name)
		}
		out := gitRepositoryOut{Changes: []string{}, DryRun: in.DryRun}
		does := func(did, would string) string {
			if in.DryRun {
				return would
			}

			return did
		}

		// the credential as it is to be: the one named, none for an empty text, and the one it has when nothing is said
		credential, off := repository.CredentialId, false
		if text, is := in.Credential.(string); is && strings.TrimSpace(text) == "" {
			credential, off = 0, repository.CredentialId != 0
		} else if in.Credential != nil {
			credentials, err := r.gitCredentials(ctx)
			if err != nil {
				return nil, out, err
			}
			one, err := oneOf(credentials, in.Credential, "credential", "git credential", gitCredentialID, gitCredentialName)
			if err != nil {
				return nil, out, err
			}
			if one.Id != repository.CredentialId {
				credential = one.Id
				out.Changes = append(out.Changes, fmt.Sprintf("%s it fetched with the credential %s", does("had", "would have"), one.Name))
			}
		}
		if off {
			out.Changes = append(out.Changes, does("had", "would have")+" it fetched with no credential")
		}

		request := dockhand.PutGitRepositoriesByIdRequest{}
		if credential != 0 && credential != repository.CredentialId {
			request.CredentialId = credential
		}
		if name != "" && name != repository.Name {
			request.Name = name
			out.Changes = append(out.Changes, fmt.Sprintf("%s it from %s to %s", does("renamed", "would rename"), repository.Name, name))
		}
		if address != "" && address != repository.Url {
			request.Url = address
			shown, _ := gitAddress(address, false)
			out.Changes = append(out.Changes, fmt.Sprintf("%s its address to %s", does("changed", "would change"), shown))
			out.Note = embedsSecret(address)
		}
		if branch != "" && branch != repository.Branch {
			request.Branch = branch
			out.Changes = append(out.Changes, fmt.Sprintf("%s the branch it follows from %s to %s", does("changed", "would change"), repository.Branch, branch))
		}
		if len(out.Changes) == 0 {
			out.Note = "nothing to do: it is that way already"
			repositoryBack(ctx, &out, repository.Id)

			return nil, out, nil
		}

		// only a change to how it is fetched is tried: a new name fetches nothing differently
		if request.Url != "" || request.Branch != "" || credential != repository.CredentialId {
			tried, err := r.tryRepository(ctx, cmp.Or(request.Url, repository.Url), cmp.Or(request.Branch, repository.Branch), credential)
			if err != nil {
				return nil, gitRepositoryOut{Changes: []string{}, DryRun: in.DryRun}, fmt.Errorf("the change could not be tried, so nothing was changed: %w", err)
			}
			out.Commit = tried.Commit
			if !tried.Reached {
				if !in.SaveUnreachable {
					return nil, gitRepositoryOut{Changes: []string{}, DryRun: in.DryRun}, fmt.Errorf("Dockhand could not fetch %s that way, so nothing was changed: %s. save_unreachable saves it all the same", repository.Name, gitSaid(tried.Error)) //nolint:staticcheck // ST1005: the sentence begins with the server's name
				}
				out.Note = noted(out.Note, does("it was saved", "it would be saved")+" as save_unreachable asks though Dockhand cannot fetch the repository that way: "+gitSaid(tried.Error))
			}
		}
		if in.DryRun {
			repositoryBack(ctx, &out, repository.Id)

			return nil, out, nil
		}

		if request != (dockhand.PutGitRepositoriesByIdRequest{}) {
			if _, err := r.dock.PutGitRepositoriesById(ctx, repository.Id, request); err != nil {
				return nil, gitRepositoryOut{Changes: []string{}}, fmt.Errorf("the git repository %s was not changed: %w", repository.Name, refusal(err))
			}
		}
		if off {
			if _, err := r.dock.ForgetGitRepositoryCredential(ctx, repository.Id); err != nil {
				out.Changes = slices.DeleteFunc(out.Changes, func(change string) bool { return strings.HasSuffix(change, "with no credential") })
				repositoryBack(ctx, &out, repository.Id)

				return nil, out, fmt.Errorf("the credential of %s was not taken away: %w", repository.Name, refusal(err))
			}
		}
		repositoryBack(ctx, &out, repository.Id)
		if out.Repository != nil && len(out.Repository.Stacks) > 0 {
			out.Note = noted(out.Note, "the next deploy of each git stack made from it fetches it this way: "+stacksSaid(out.Repository.Stacks))
		}

		return nil, out, nil
	})

	type gitRepositoryDeleteIn struct {
		gitRepositoryArg

		Confirm  bool `json:"confirm,omitempty"   jsonschema:"remove it: without this the answer says what would go and nothing is removed"`
		EvenUsed bool `json:"even_used,omitempty" jsonschema:"remove it though git stacks are deployed from it. Dockhand then forgets each of those git stacks as well and leaves its containers running, with nothing deploying them any more"`
	}
	type gitRepositoryDeleteOut struct {
		Repository string        `json:"repository"`
		Stacks     []gitStackUse `json:"stacks,omitempty"   jsonschema:"the git stacks deployed from it"`
		Removed    bool          `json:"removed"            jsonschema:"the repository was removed, and is read back as gone"`
		WouldGo    []string      `json:"would_go,omitempty" jsonschema:"what removing it takes away, said before anything is removed"`
		Note       string        `json:"note,omitempty"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "git_repository_delete",
		Description: "Remove a git repository from the Dockhand. Without confirm the answer says what would go and removes nothing. Dockhand removes one that git stacks are deployed from without a word, and with it each of those git stacks: its schedule, its webhook and its copy of the repository go, and its containers are left running with nothing deploying them any more. So a repository in use is removed only with even_used, after the stacks are named. The credential it was fetched with is kept, and nothing on the git server is touched.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in gitRepositoryDeleteIn) (*mcp.CallToolResult, gitRepositoryDeleteOut, error) {
		listed, err := r.gitRepositories(ctx)
		if err != nil {
			return nil, gitRepositoryDeleteOut{}, err
		}
		repository, err := oneOf(listed, in.Repository, "repository", "git repository", gitRepositoryID, gitRepositoryName)
		if err != nil {
			return nil, gitRepositoryDeleteOut{}, err
		}
		rows, _, err := repositoryRows(ctx, []dockhand.GetGitRepositoriesResponse{*repository}, false)
		if err != nil {
			return nil, gitRepositoryDeleteOut{}, fmt.Errorf("%w, so nothing was removed", err)
		}
		uses := rows[0].Stacks
		out := gitRepositoryDeleteOut{Repository: repository.Name, Stacks: uses}
		out.WouldGo = append(out.WouldGo, fmt.Sprintf("the git repository %s, as Dockhand keeps it: its address, its branch and which credential it is fetched with", repository.Name))
		if len(uses) > 0 {
			out.WouldGo = append(out.WouldGo, fmt.Sprintf("%s deployed from it: %s. Dockhand forgets each, with its schedule and its webhook, and leaves its containers running", several(len(uses), "git stack"), stacksSaid(uses)))
		}
		if !in.Confirm {
			out.Note = "nothing was removed: confirm removes it"
			if len(uses) > 0 {
				out.Note = "nothing was removed: confirm with even_used removes it"
			}

			return nil, out, nil
		}
		if len(uses) > 0 && !in.EvenUsed {
			return nil, out, fmt.Errorf("%s has %s deployed from it: %s. Dockhand would forget each without a word and leave its containers running with nothing deploying them: remove the git stacks first, or even_used removes it all the same", repository.Name, several(len(uses), "git stack"), stacksSaid(uses))
		}

		if _, err := r.dock.DeleteGitRepositoriesById(ctx, repository.Id); err != nil {
			return nil, out, fmt.Errorf("the git repository %s was not removed: %w", repository.Name, refusal(err))
		}
		after, err := r.gitRepositories(ctx)
		switch {
		case err != nil:
			out.Removed, out.WouldGo = true, nil
			out.Note = "it was removed, and the repositories could not be listed afterwards: " + err.Error()
		case slices.ContainsFunc(after, func(p dockhand.GetGitRepositoriesResponse) bool { return p.Id == repository.Id }):
			return nil, out, fmt.Errorf("Dockhand answered that %s was removed, and still lists it", repository.Name) //nolint:staticcheck // ST1005: the sentence begins with the server's name
		default:
			out.Removed, out.WouldGo = true, nil
			if len(uses) > 0 {
				out.Note = fmt.Sprintf("%s forgotten with it, their containers left running with nothing deploying them: %s", several(len(uses), "git stack"), stacksSaid(uses))
			}
		}

		return nil, out, nil
	})
}
