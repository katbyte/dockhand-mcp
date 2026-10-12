package tools

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/pandorest/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// secretPathKey is the variable in a stack's .env file that names the path in the stack's secret provider whose every key is loaded as a variable.
const secretPathKey = "DOCKHAND_SECRET_SELECTOR"

// secretProviderTypes are the kinds of secret store a 1.0.51 Dockhand knows, as it names them. It refuses any other.
const secretProviderTypes = "vault (HashiCorp Vault, or anything that speaks its API), infisical, doppler, op-connect and op-service-account (1Password), azure-kv, bitwarden or keepass" //nolint:gosec // G101: the kinds of secret store, and no secret

// secretProviderArg names one secret provider the Dockhand keeps.
type secretProviderArg struct {
	Provider any `json:"provider,omitempty" jsonschema:"the secret provider, by its name or its number as secret_provider_list gives them"`
}

// secretProviderUse is one stack whose secrets are loaded from a provider.
type secretProviderUse struct {
	Env   string `json:"env"`
	Stack string `json:"stack"`
}

// secretProviderRow is a secret provider as the tools give it: never with what it signs in with, which Dockhand does not give back.
type secretProviderRow struct {
	ID       int                 `json:"id"`
	Name     string              `json:"name"`
	Type     string              `json:"type"               jsonschema:"what kind of secret store it is, as Dockhand names it: vault, infisical, doppler, op-connect, op-service-account, azure-kv, bitwarden or keepass"`
	Default  bool                `json:"default,omitempty"  jsonschema:"the provider Dockhand's web interface offers first for a new stack; a stack made any other way has none unless one is named"`
	Settings map[string]string   `json:"settings,omitempty" jsonschema:"where it is and what in it is read, as Dockhand gives them back: for vault its address, its mount and its namespace. The token or key it signs in with is kept and never given back"`
	Stacks   []secretProviderUse `json:"stacks,omitempty"   jsonschema:"the stacks whose secrets are loaded from it each time they are deployed"`
	Created  string              `json:"created,omitempty"`
	Changed  string              `json:"changed,omitempty"`
}

// secretProviders is every secret provider the Dockhand keeps.
func (r *registry) secretProviders(ctx context.Context) ([]dockhand.GetSecretProvidersAnswer, error) {
	listed, err := r.dock.GetSecretProviders(ctx)
	if err != nil {
		return nil, fmt.Errorf("the secret providers could not be listed: %w", refusal(err))
	}

	return listed.Answer, nil
}

// secretProviderIn is the secret provider of a list a call names, by its number or by its name. Dockhand keeps two whose names differ only by case,
// so a name is matched letter for letter first, and whatever its case only when that is one provider alone.
func secretProviderIn(listed []dockhand.GetSecretProvidersAnswer, given any, what string) (*dockhand.GetSecretProvidersAnswer, error) {
	wanted, err := named(given, what)
	if err != nil {
		return nil, err
	}
	if wanted == "" {
		return nil, fmt.Errorf("%s is required: the secret provider's name or number", what)
	}
	var near []*dockhand.GetSecretProvidersAnswer
	names := make([]string, 0, len(listed))
	for i := range listed {
		if strconv.Itoa(int(listed[i].Id)) == wanted || listed[i].Name == wanted {
			return &listed[i], nil
		}
		if strings.EqualFold(listed[i].Name, wanted) {
			near = append(near, &listed[i])
		}
		names = append(names, listed[i].Name)
	}
	switch len(near) {
	case 0:
		return nil, fmt.Errorf("no secret provider is named or numbered %q: the Dockhand keeps %s", wanted, cmp.Or(strings.Join(names, ", "), "none"))
	case 1:
		return near[0], nil
	default:
		numbers := make([]string, 0, len(near))
		for _, p := range near {
			numbers = append(numbers, p.Name+" ("+strconv.Itoa(int(p.Id))+")")
		}

		return nil, fmt.Errorf("no secret provider is named %q letter for letter, and %d are but for their case: give the number of one, %s", wanted, len(near), strings.Join(numbers, " or "))
	}
}

// byEnvThenStack orders the stacks that use a provider, and byNameThenNumber the providers themselves, whatever the case of their names.
func byEnvThenStack(a, b secretProviderUse) int {
	return cmp.Or(cmp.Compare(a.Env, b.Env), cmp.Compare(a.Stack, b.Stack))
}

func byNameThenNumber(a, b secretProviderRow) int {
	return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.ID, b.ID))
}

// providerSettings is the settings of a provider as texts, whatever JSON carried each as.
func providerSettings(config map[string]any) map[string]string {
	settings := make(map[string]string, len(config))
	for name, value := range config {
		if text, is := value.(string); is {
			settings[name] = text

			continue
		}
		settings[name] = fmt.Sprint(value)
	}

	return settings
}

// secretProviderRowOf reads one provider in full: its settings, with a value whose name says it is a secret hidden unless asked for, and the stacks
// that use it by the names of their environments.
func (r *registry) secretProviderRowOf(ctx context.Context, id, standard int, envs []environment, reveal bool) (secretProviderRow, int, error) {
	got, err := r.dock.GetSecretProvidersById(ctx, dockhand.SecretProviderID(id))
	if err != nil {
		return secretProviderRow{}, 0, refusal(err)
	}
	row := secretProviderRow{ID: int(got.Answer.Id), Name: got.Answer.Name, Type: string(got.Answer.Type), Default: int(got.Answer.Id) == standard, Created: when(got.Answer.CreatedAt), Changed: when(got.Answer.UpdatedAt)}
	settings, hidden := maskedLabels(providerSettings(got.Answer.Config), reveal)
	if len(settings) > 0 {
		row.Settings = settings
	}
	for _, use := range got.Answer.StacksUsing {
		i := slices.IndexFunc(envs, func(e environment) bool { return e.ID == use.EnvironmentId })
		row.Stacks = append(row.Stacks, secretProviderUse{Env: nameOr(i, int(use.EnvironmentId), func(i int) string { return envs[i].Name }), Stack: use.StackName})
	}
	slices.SortFunc(row.Stacks, byEnvThenStack)

	return row, hidden, nil
}

// standardProvider is the number of the provider Dockhand's web interface offers first, 0 for none.
func (r *registry) standardProvider(ctx context.Context) (int, error) {
	standard, err := r.dock.GetSecretProvidersDefault(ctx)
	if err != nil {
		return 0, fmt.Errorf("which secret provider is the default could not be read: %w", refusal(err))
	}

	return int(standard.Answer.ProviderId), nil
}

// reasonOf is why Dockhand could not use a provider, as it says it, to go in the middle of a sentence.
func reasonOf(said string) string {
	return cmp.Or(strings.TrimRight(strings.TrimSpace(said), "."), "it gives no reason")
}

// usedBy is the stacks that use a provider in words: web on alpha, mail on beta.
func usedBy(stacks []secretProviderUse) string {
	said := make([]string, 0, len(stacks))
	for _, use := range stacks {
		said = append(said, use.Stack+" on "+use.Env)
	}

	return strings.Join(said, ", ")
}

//nolint:gocyclo,maintidx // the secret provider tools registered in one function, each a handler of its own: the count is theirs added up
func registerSecretProviderTools(r *registry) {
	type secretProviderListIn struct {
		secretProviderArg
		revealArg
	}
	type secretProviderListOut struct {
		Providers []secretProviderRow `json:"providers"`
		Total     int                 `json:"total"`
		Hidden    int                 `json:"hidden,omitempty" jsonschema:"how many settings are hidden for having a secret's name"`
		Note      string              `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "secret_provider_list",
		Description: "List the secret providers the Dockhand keeps, or one: the stores outside it, such as a Vault, that it loads a stack's secrets from each time the stack is deployed, so that they are kept in no file. Each is given with its kind, where it is, which stacks use it, and whether it is the one the web interface offers first. What a provider signs in with is never given: Dockhand keeps it and does not give it back.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in secretProviderListIn) (*mcp.CallToolResult, secretProviderListOut, error) {
		listed, err := r.secretProviders(ctx)
		if err != nil {
			return nil, secretProviderListOut{}, err
		}
		if in.Provider != nil {
			one, unknown := secretProviderIn(listed, in.Provider, "provider")
			if unknown != nil {
				return nil, secretProviderListOut{}, unknown
			}
			listed = []dockhand.GetSecretProvidersAnswer{*one}
		}
		out := secretProviderListOut{Providers: make([]secretProviderRow, 0, len(listed))}
		if len(listed) == 0 {
			out.Note = "the Dockhand keeps no secret provider: every stack's secrets are the ones Dockhand keeps itself"

			return nil, out, nil
		}
		standard, err := r.standardProvider(ctx)
		if err != nil {
			return nil, out, err
		}
		envs, err := r.environments(ctx)
		if err != nil {
			return nil, out, err
		}
		for i := range listed {
			row, hidden, err := r.secretProviderRowOf(ctx, int(listed[i].Id), standard, envs, in.RevealSecrets)
			if err != nil {
				return nil, out, fmt.Errorf("the secret provider %s could not be read: %w", listed[i].Name, err)
			}
			out.Providers, out.Hidden = append(out.Providers, row), out.Hidden+hidden
		}
		slices.SortFunc(out.Providers, byNameThenNumber)
		out.Total = len(out.Providers)
		if out.Hidden > 0 {
			out.Note = hiddenNote(out.Hidden, "told by the names beside them")
		}

		return nil, out, nil
	})

	type secretProviderTestIn struct {
		secretProviderArg

		Type     string         `json:"type,omitempty"     jsonschema:"to try a provider that is not saved: its kind, vault, infisical, doppler, op-connect, op-service-account, azure-kv, bitwarden or keepass"`
		Settings map[string]any `json:"settings,omitempty" jsonschema:"with type, everything the provider would be saved with: for vault its address, its token, and a mount and a namespace when it has them. With provider, the whole of the settings to try in place of the saved ones; the token Dockhand keeps is used when none is given and the address is the saved one. What is given is sent to Dockhand to try and is not kept"`
	}
	type secretProviderTestOut struct {
		Provider string `json:"provider,omitempty" jsonschema:"the provider that was tried; absent for one that is not saved"`
		Type     string `json:"type"`
		Reached  bool   `json:"reached"            jsonschema:"Dockhand reached the provider and it took what Dockhand signed in with"`
		Error    string `json:"error,omitempty"    jsonschema:"what Dockhand says went wrong, when it did not"`
		Note     string `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "secret_provider_test",
		Description: "Have the Dockhand try a secret provider and say whether it could reach it and sign in: one it keeps, by provider, as it is saved or with other settings; or one that is not saved yet, by type and settings. Nothing is saved or changed. Dockhand makes the call itself, so the address is one Dockhand's own container can reach. A stack whose provider cannot be reached is not deployed, so this is the first thing to ask when a deploy fails for a stack that has one.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in secretProviderTestIn) (*mcp.CallToolResult, secretProviderTestOut, error) {
		kind := strings.TrimSpace(in.Type)
		if in.Provider == nil {
			switch {
			case kind == "":
				return nil, secretProviderTestOut{}, errors.New("provider or type is required: a provider the Dockhand keeps, or the kind of one that is not saved, with its settings")
			case len(in.Settings) == 0:
				return nil, secretProviderTestOut{}, errors.New("settings is required with type: everything the provider would be saved with, as its address and its token")
			default:
			}
			tried, err := r.dock.PostSecretProvidersTest(ctx, dockhand.PostSecretProvidersTestRequest{Type: dockhand.SecretProviderType(kind), Config: in.Settings})
			if err != nil {
				return nil, secretProviderTestOut{}, fmt.Errorf("the provider could not be tried: %w", refusal(err))
			}

			return nil, secretProviderTestOut{Type: kind, Reached: tried.Answer.Ok, Error: tried.Answer.Error}, nil
		}
		if kind != "" {
			return nil, secretProviderTestOut{}, errors.New("type is for a provider that is not saved: one the Dockhand keeps is tried as the kind it is")
		}
		listed, err := r.secretProviders(ctx)
		if err != nil {
			return nil, secretProviderTestOut{}, err
		}
		provider, err := secretProviderIn(listed, in.Provider, "provider")
		if err != nil {
			return nil, secretProviderTestOut{}, err
		}
		tried, err := r.dock.PostSecretProvidersByIdTest(ctx, provider.Id, dockhand.PostSecretProvidersByIdTestRequest{Config: in.Settings})
		if err != nil {
			return nil, secretProviderTestOut{}, fmt.Errorf("the provider %s could not be tried: %w", provider.Name, refusal(err))
		}
		out := secretProviderTestOut{Provider: provider.Name, Type: string(provider.Type), Reached: tried.Answer.Ok, Error: tried.Answer.Error}
		if len(in.Settings) > 0 {
			out.Note = "tried with the settings given in place of the saved ones, which are as they were"
		}

		return nil, out, nil
	})

	type secretProviderKeysIn struct {
		secretProviderArg

		Path string `json:"path" jsonschema:"where in the provider to look: for vault a path under its mount, as prod/db. It is what a stack is given as secret_path to have every key there loaded"`
	}
	type secretProviderKeysOut struct {
		Provider string   `json:"provider"`
		Path     string   `json:"path"`
		Keys     []string `json:"keys"           jsonschema:"the names of the secrets there, each of which a stack given this path gets as a variable of that name. Their values never leave Dockhand"`
		Total    int      `json:"total"`
		Note     string   `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "secret_provider_keys",
		Description: "List the names of the secrets a provider the Dockhand keeps has at one path: names only, never their values, which do not leave Dockhand. A stack whose secrets are loaded from that path gets each as a variable of that name when it is deployed, so this says what a compose file may be filled in with before the stack is bound to the path.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in secretProviderKeysIn) (*mcp.CallToolResult, secretProviderKeysOut, error) {
		path := strings.TrimSpace(in.Path)
		if path == "" {
			return nil, secretProviderKeysOut{}, errors.New("path is required: where in the provider to look, as prod/db")
		}
		listed, err := r.secretProviders(ctx)
		if err != nil {
			return nil, secretProviderKeysOut{}, err
		}
		provider, err := secretProviderIn(listed, in.Provider, "provider")
		if err != nil {
			return nil, secretProviderKeysOut{}, err
		}
		keys, err := r.secretKeysAt(ctx, provider, path)
		if err != nil {
			return nil, secretProviderKeysOut{Provider: provider.Name, Path: path, Keys: []string{}}, err
		}
		out := secretProviderKeysOut{Provider: provider.Name, Path: path, Keys: keys, Total: len(keys)}
		if len(keys) == 0 {
			out.Note = "the provider answered and has no secret there"
		}

		return nil, out, nil
	})

	type secretProviderOut struct {
		Provider *secretProviderRow `json:"provider,omitempty" jsonschema:"the provider as it is now, read back; absent for a dry run of making one"`
		Changes  []string           `json:"changes"            jsonschema:"what was done, in words; empty when nothing needed doing"`
		DryRun   bool               `json:"dry_run,omitempty"  jsonschema:"nothing was done: changes says what would be"`
		Note     string             `json:"note,omitempty"`
	}
	// readBack is a provider as it stands after a change, with the note that it could not be read when it could not
	readBack := func(ctx context.Context, out *secretProviderOut, id int) {
		standard, err := r.standardProvider(ctx)
		if err != nil {
			out.Note = noted(out.Note, "it was done, and could not be read back: "+err.Error())

			return
		}
		envs, err := r.environments(ctx)
		if err != nil {
			out.Note = noted(out.Note, "it was done, and could not be read back: "+err.Error())

			return
		}
		row, _, err := r.secretProviderRowOf(ctx, id, standard, envs, false)
		if err != nil {
			out.Note = noted(out.Note, "it was done, and could not be read back: "+err.Error())

			return
		}
		out.Provider = &row
	}

	type secretProviderCreateIn struct {
		dryRunArg

		Name            string         `json:"name"                       jsonschema:"what to call the provider: the name the tools then take, which no other provider has letter for letter"`
		Type            string         `json:"type"                       jsonschema:"its kind: vault, infisical, doppler, op-connect, op-service-account, azure-kv, bitwarden or keepass"`
		Settings        map[string]any `json:"settings"                   jsonschema:"where it is and what Dockhand signs in with: for vault its address (as Dockhand's own container reaches it) and its token, with a mount when the secrets are not under secret, and a namespace when it has them. The token is sent to Dockhand and kept there, and is never given back"`
		Default         bool           `json:"default,omitempty"          jsonschema:"make it the provider the web interface offers first for a new stack, in place of the one that is"`
		SaveUnreachable bool           `json:"save_unreachable,omitempty" jsonschema:"save it though Dockhand cannot reach it now, or it turns the token away. Without this such a provider is not saved"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "secret_provider_create",
		Description: "Save a secret provider on the Dockhand: a store outside it, of the kinds " + secretProviderTypes + ", that a stack's secrets are then loaded from each time the stack is deployed. It is tried first, and one Dockhand cannot reach or sign in to is not saved unless save_unreachable says so: Dockhand left to itself saves any, and a stack bound to one that cannot be reached is not deployed. Nothing uses it until a stack is given it.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in secretProviderCreateIn) (*mcp.CallToolResult, secretProviderOut, error) {
		name, kind := strings.TrimSpace(in.Name), strings.TrimSpace(in.Type)
		switch {
		case name == "":
			return nil, secretProviderOut{}, errors.New("name is required: what to call the provider")
		case kind == "":
			return nil, secretProviderOut{}, errors.New("type is required: the provider's kind, " + secretProviderTypes)
		case len(in.Settings) == 0:
			return nil, secretProviderOut{}, errors.New("settings is required: where the provider is and what Dockhand signs in with, as its address and its token")
		default:
		}
		listed, err := r.secretProviders(ctx)
		if err != nil {
			return nil, secretProviderOut{}, err
		}
		if slices.ContainsFunc(listed, func(p dockhand.GetSecretProvidersAnswer) bool { return p.Name == name }) {
			return nil, secretProviderOut{}, fmt.Errorf("a secret provider is already named %s: secret_provider_edit changes it, and a new one takes a name of its own", name)
		}
		out := secretProviderOut{Changes: []string{}, DryRun: in.DryRun}

		tried, err := r.dock.PostSecretProvidersTest(ctx, dockhand.PostSecretProvidersTestRequest{Type: dockhand.SecretProviderType(kind), Config: in.Settings})
		if err != nil {
			return nil, out, fmt.Errorf("the provider could not be tried, so nothing was saved: %w", refusal(err))
		}
		if !tried.Answer.Ok {
			if !in.SaveUnreachable {
				return nil, out, fmt.Errorf("Dockhand could not use the provider, so nothing was saved: %s. save_unreachable saves it all the same", reasonOf(tried.Answer.Error)) //nolint:staticcheck // ST1005: the sentence begins with the server's name
			}
			out.Note = "it was saved as save_unreachable asked though Dockhand could not use it: " + reasonOf(tried.Answer.Error)
		}
		if in.DryRun {
			out.Changes = append(out.Changes, fmt.Sprintf("would save the %s provider %s", kind, name))
			if in.Default {
				out.Changes = append(out.Changes, "would make it the default")
			}
			if !tried.Answer.Ok {
				out.Note = "it would be saved as save_unreachable asks though Dockhand cannot use it: " + reasonOf(tried.Answer.Error)
			}

			return nil, out, nil
		}

		made, err := r.dock.PostSecretProviders(ctx, dockhand.PostSecretProvidersRequest{Name: name, Type: dockhand.SecretProviderType(kind), Config: in.Settings})
		if err != nil {
			return nil, out, fmt.Errorf("the provider %s was not saved: %w", name, refusal(err))
		}
		out.Changes = append(out.Changes, fmt.Sprintf("saved the %s provider %s", kind, name))
		if in.Default {
			if _, err := r.dock.PutSecretProvidersDefault(ctx, dockhand.PutSecretProvidersDefaultRequest{ProviderId: made.Answer.Id}); err != nil {
				readBack(ctx, &out, int(made.Answer.Id))

				return nil, out, fmt.Errorf("the provider %s was saved and could not be made the default: %w", name, refusal(err))
			}
			out.Changes = append(out.Changes, "made it the default")
		}
		readBack(ctx, &out, int(made.Answer.Id))

		return nil, out, nil
	})

	type secretProviderEditIn struct {
		secretProviderArg
		dryRunArg

		Name            string         `json:"name,omitempty"             jsonschema:"another name for the provider"`
		Type            string         `json:"type,omitempty"             jsonschema:"another kind for it, which is for a provider saved as the wrong one: its settings are kept as they are"`
		Settings        map[string]any `json:"settings,omitempty"         jsonschema:"settings to set, each name with its value; the others are kept. A new token is given here. An address is changed only with the token given again: Dockhand would keep the saved one and send it to the new address"`
		RemoveSettings  []string       `json:"remove_settings,omitempty"  jsonschema:"settings to take out, by name"`
		Default         *bool          `json:"default,omitempty"          jsonschema:"true makes it the provider the web interface offers first for a new stack; false has it be that no longer, and none is"`
		SaveUnreachable bool           `json:"save_unreachable,omitempty" jsonschema:"save new settings though Dockhand cannot use the provider with them. Without this they are not saved"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "secret_provider_edit",
		Description: "Change a secret provider the Dockhand keeps: its name, its settings a name at a time, the token it signs in with, and whether it is the one the web interface offers first. New settings are tried before they are saved, and ones Dockhand cannot use the provider with are refused unless save_unreachable says otherwise: every stack bound to the provider takes its secrets through them at its next deploy, and is not deployed when they fail. A token left out is kept, which is why an address is not changed without it: Dockhand would send the token it keeps to the new address.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in secretProviderEditIn) (*mcp.CallToolResult, secretProviderOut, error) {
		name, kind := strings.TrimSpace(in.Name), strings.TrimSpace(in.Type)
		if name == "" && kind == "" && len(in.Settings) == 0 && len(in.RemoveSettings) == 0 && in.Default == nil {
			return nil, secretProviderOut{}, errors.New("nothing to change was given: name, type, settings, remove_settings or default")
		}
		for _, gone := range in.RemoveSettings {
			if _, set := in.Settings[gone]; set {
				return nil, secretProviderOut{}, fmt.Errorf("the setting %s is in both settings and remove_settings", gone)
			}
		}
		listed, err := r.secretProviders(ctx)
		if err != nil {
			return nil, secretProviderOut{}, err
		}
		provider, err := secretProviderIn(listed, in.Provider, "provider")
		if err != nil {
			return nil, secretProviderOut{}, err
		}
		if name != provider.Name && slices.ContainsFunc(listed, func(p dockhand.GetSecretProvidersAnswer) bool { return p.Name == name }) {
			return nil, secretProviderOut{}, fmt.Errorf("a secret provider is already named %s", name)
		}
		got, err := r.dock.GetSecretProvidersById(ctx, provider.Id)
		if err != nil {
			return nil, secretProviderOut{}, fmt.Errorf("the provider %s could not be read, so nothing was changed: %w", provider.Name, refusal(err))
		}
		standard, err := r.standardProvider(ctx)
		if err != nil {
			return nil, secretProviderOut{}, err
		}
		out := secretProviderOut{Changes: []string{}, DryRun: in.DryRun}
		does := func(did, would string) string {
			if in.DryRun {
				return would
			}

			return did
		}

		// the settings as they would be: what Dockhand gives back of the saved ones, with what is given over it and what is named taken out. A value
		// given that Dockhand does not give back is a token or a key, which cannot be told from the one it keeps and so is always saved
		saved := providerSettings(got.Answer.Config)
		whole := maps.Clone(got.Answer.Config)
		if whole == nil {
			whole = map[string]any{}
		}
		var set, moved []string
		credential := false
		for _, setting := range slices.Sorted(maps.Keys(in.Settings)) {
			was, had := saved[setting]
			value := in.Settings[setting]
			if text, is := value.(string); is && had && text == was {
				continue
			}
			whole[setting] = value
			set = append(set, setting)
			if isSecretName(setting) {
				credential = true
			}
			if had && (strings.Contains(was, "://") || setting == "address" || setting == "host") {
				moved = append(moved, setting)
			}
		}
		var removed []string
		for _, gone := range in.RemoveSettings {
			if _, had := saved[gone]; had {
				delete(whole, gone)
				removed = append(removed, gone)
			}
		}
		if len(moved) > 0 && !credential {
			return nil, out, fmt.Errorf("the %s of %s is changed and no token is given: Dockhand would keep the token it has and send it to the new address. Give the token for the new address in settings as well", strings.Join(moved, " and "), provider.Name)
		}

		request := dockhand.PutSecretProvidersByIdRequest{}
		if name != "" && name != provider.Name {
			request.Name = name
			out.Changes = append(out.Changes, fmt.Sprintf("%s it from %s to %s", does("renamed", "would rename"), provider.Name, name))
		}
		if kind != "" && kind != string(provider.Type) {
			request.Type = dockhand.SecretProviderType(kind)
			out.Changes = append(out.Changes, fmt.Sprintf("%s its kind from %s to %s", does("changed", "would change"), provider.Type, kind))
		}
		if len(set) > 0 || len(removed) > 0 {
			request.Config = whole
			if len(set) > 0 {
				out.Changes = append(out.Changes, does("set", "would set")+" its "+strings.Join(set, ", "))
			}
			if len(removed) > 0 {
				out.Changes = append(out.Changes, does("took out", "would take out")+" its "+strings.Join(removed, ", "))
			}
			tried, err := r.dock.PostSecretProvidersByIdTest(ctx, provider.Id, dockhand.PostSecretProvidersByIdTestRequest{Config: whole})
			if err != nil {
				return nil, secretProviderOut{Changes: []string{}, DryRun: in.DryRun}, fmt.Errorf("the new settings could not be tried, so nothing was changed: %w", refusal(err))
			}
			if !tried.Answer.Ok {
				if !in.SaveUnreachable {
					return nil, secretProviderOut{Changes: []string{}, DryRun: in.DryRun}, fmt.Errorf("Dockhand could not use %s with the new settings, so nothing was changed: %s. save_unreachable saves them all the same", provider.Name, reasonOf(tried.Answer.Error)) //nolint:staticcheck // ST1005: the sentence begins with the server's name
				}
				out.Note = does("the settings were saved", "the settings would be saved") + " as save_unreachable asks though Dockhand cannot use the provider with them: " + reasonOf(tried.Answer.Error)
			}
		}
		changing := len(out.Changes) > 0
		isStandard := standard == int(provider.Id)
		if in.Default != nil && *in.Default != isStandard {
			out.Changes = append(out.Changes, map[bool]string{true: does("made", "would make") + " it the default", false: does("had", "would have") + " it be the default no longer"}[*in.Default])
		}

		if len(out.Changes) == 0 {
			out.Note = "nothing to do: it is that way already"
			readBack(ctx, &out, int(provider.Id))

			return nil, out, nil
		}
		if in.DryRun {
			readBack(ctx, &out, int(provider.Id))

			return nil, out, nil
		}
		if changing {
			if _, err := r.dock.PutSecretProvidersById(ctx, provider.Id, request); err != nil {
				return nil, secretProviderOut{Changes: []string{}}, fmt.Errorf("the provider %s was not changed: %w", provider.Name, refusal(err))
			}
		}
		if in.Default != nil && *in.Default != isStandard {
			to := dockhand.PutSecretProvidersDefaultRequest{}
			if *in.Default {
				to.ProviderId = provider.Id
			}
			if _, err := r.dock.PutSecretProvidersDefault(ctx, to); err != nil {
				out.Changes = out.Changes[:len(out.Changes)-1]
				readBack(ctx, &out, int(provider.Id))

				return nil, out, fmt.Errorf("which provider is the default was not changed: %w", refusal(err))
			}
		}
		readBack(ctx, &out, int(provider.Id))

		return nil, out, nil
	})

	type secretProviderDeleteIn struct {
		secretProviderArg

		Confirm  bool `json:"confirm,omitempty"   jsonschema:"remove it: without this the answer says what would go and nothing is removed"`
		EvenUsed bool `json:"even_used,omitempty" jsonschema:"remove it though stacks have their secrets loaded from it. Each is then deployed with those variables empty until it is given another provider"`
	}
	type secretProviderDeleteOut struct {
		Provider string              `json:"provider"`
		Stacks   []secretProviderUse `json:"stacks,omitempty"   jsonschema:"the stacks whose secrets are loaded from it"`
		Removed  bool                `json:"removed"            jsonschema:"the provider was removed, and is read back as gone"`
		WouldGo  []string            `json:"would_go,omitempty" jsonschema:"what removing it takes away, said before anything is removed"`
		Note     string              `json:"note,omitempty"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "secret_provider_delete",
		Description: "Remove a secret provider from the Dockhand. Without confirm the answer says what would go and removes nothing. Dockhand removes one that stacks use without a word and takes each stack off it, and the next deploy of such a stack succeeds with every variable the provider gave left empty: so a provider stacks use is removed only with even_used, after the stacks are named. Nothing in the secret store itself is touched.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in secretProviderDeleteIn) (*mcp.CallToolResult, secretProviderDeleteOut, error) {
		listed, err := r.secretProviders(ctx)
		if err != nil {
			return nil, secretProviderDeleteOut{}, err
		}
		provider, err := secretProviderIn(listed, in.Provider, "provider")
		if err != nil {
			return nil, secretProviderDeleteOut{}, err
		}
		standard, err := r.standardProvider(ctx)
		if err != nil {
			return nil, secretProviderDeleteOut{}, err
		}
		envs, err := r.environments(ctx)
		if err != nil {
			return nil, secretProviderDeleteOut{}, err
		}
		row, _, err := r.secretProviderRowOf(ctx, int(provider.Id), standard, envs, false)
		if err != nil {
			return nil, secretProviderDeleteOut{}, fmt.Errorf("the provider %s could not be read, so nothing was removed: %w", provider.Name, err)
		}
		out := secretProviderDeleteOut{Provider: provider.Name, Stacks: row.Stacks}
		out.WouldGo = append(out.WouldGo, fmt.Sprintf("the %s provider %s, with the token Dockhand keeps for it", provider.Type, provider.Name))
		if len(row.Stacks) > 0 {
			out.WouldGo = append(out.WouldGo, fmt.Sprintf("where the secrets of %s are loaded from: %s. Each is taken off it, and is deployed with those variables empty from then on", several(len(row.Stacks), "stack"), usedBy(row.Stacks)))
		}
		if row.Default {
			out.WouldGo = append(out.WouldGo, "the default the web interface offers for a new stack, which is then none")
		}
		if !in.Confirm {
			out.Note = "nothing was removed: confirm removes it"
			if len(row.Stacks) > 0 {
				out.Note = "nothing was removed: confirm with even_used removes it"
			}

			return nil, out, nil
		}
		if len(row.Stacks) > 0 && !in.EvenUsed {
			return nil, out, fmt.Errorf("the secrets of %s are loaded from %s: %s. Dockhand would take each off it without a word and deploy it with those variables empty: give each another provider first, or even_used removes it all the same", several(len(row.Stacks), "stack"), provider.Name, usedBy(row.Stacks))
		}

		if _, err := r.dock.DeleteSecretProvidersById(ctx, provider.Id); err != nil {
			return nil, out, fmt.Errorf("the provider %s was not removed: %w", provider.Name, refusal(err))
		}
		after, err := r.secretProviders(ctx)
		switch {
		case err != nil:
			out.Removed, out.WouldGo = true, nil
			out.Note = "it was removed, and the providers could not be listed afterwards: " + err.Error()
		case slices.ContainsFunc(after, func(p dockhand.GetSecretProvidersAnswer) bool { return p.Id == provider.Id }):
			return nil, out, fmt.Errorf("Dockhand answered that %s was removed, and still lists it", provider.Name) //nolint:staticcheck // ST1005: the sentence begins with the server's name
		default:
			out.Removed, out.WouldGo = true, nil
			if len(row.Stacks) > 0 {
				out.Note = fmt.Sprintf("%s left with no secret provider, to be deployed with those variables empty: %s", several(len(row.Stacks), "stack"), usedBy(row.Stacks))
			}
		}

		return nil, out, nil
	})
}

// secretKeysAt is the names of the secrets a provider has at a path, in order, as Dockhand finds them now. A path the provider has nothing at, and a
// provider that cannot be reached, are errors that say which.
func (r *registry) secretKeysAt(ctx context.Context, provider *dockhand.GetSecretProvidersAnswer, path string) ([]string, error) {
	found, err := r.dock.PostSecretProvidersByIdProbe(ctx, provider.Id, dockhand.PostSecretProvidersByIdProbeRequest{Selector: path})
	switch {
	case client.IsNotFound(err):
		return nil, fmt.Errorf("the provider %s is gone", provider.Name)
	case err != nil:
		return nil, fmt.Errorf("the provider %s could not be asked what it has at %s: %w", provider.Name, path, refusal(err))
	case !found.Answer.Ok:
		return nil, fmt.Errorf("the provider %s gave nothing for the path %s: Dockhand says %s", provider.Name, path, cmp.Or(found.Answer.Error, "it could not look, and gives no reason"))
	default:
		keys := append([]string{}, found.Answer.BulkKeys...)
		slices.Sort(keys)

		return keys, nil
	}
}
