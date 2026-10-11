package tools

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ssoArg names one single sign-on provider the Dockhand keeps.
type ssoArg struct {
	Provider any `json:"provider,omitempty" jsonschema:"the single sign-on provider, by its name or its number as sso_list gives them"`
}

// ssoRow is one single sign-on provider the Dockhand keeps: everything of its record but the secret, which Dockhand never gives back.
type ssoRow struct {
	ID                int    `json:"id"`
	Name              string `json:"name"`
	Enabled           bool   `json:"enabled"                       jsonschema:"switched on: offered to whoever signs in"`
	LoginID           string `json:"login_id"                      jsonschema:"what login_set takes to mark it the default provider"`
	IssuerURL         string `json:"issuer_url"                    jsonschema:"the address of the provider"`
	ClientID          string `json:"client_id"                     jsonschema:"what Dockhand is known to the provider as"`
	RedirectURI       string `json:"redirect_uri,omitempty"        jsonschema:"where the provider sends a user back to"`
	Scopes            string `json:"scopes,omitempty"              jsonschema:"the scopes the record names, with spaces between"`
	UsernameClaim     string `json:"username_claim,omitempty"      jsonschema:"the claim the record names for a user's name"`
	EmailClaim        string `json:"email_claim,omitempty"`
	DisplayNameClaim  string `json:"display_name_claim,omitempty"`
	AdminClaim        string `json:"admin_claim,omitempty"         jsonschema:"the claim the record names for an administrator; absent when none is set"`
	AdminValue        string `json:"admin_value,omitempty"         jsonschema:"the value of that claim the record names for an administrator"`
	RoleMappingsClaim string `json:"role_mappings_claim,omitempty" jsonschema:"the claim the record names for roles, which Dockhand has only with a licence"`
}

func ssoRowOf(p *dockhand.GetAuthOidcResponse) ssoRow {
	return ssoRow{ID: p.Id, Name: p.Name, Enabled: yes(p.Enabled), LoginID: "oidc:" + strconv.Itoa(p.Id), IssuerURL: p.IssuerUrl, ClientID: p.ClientId, RedirectURI: p.RedirectUri, Scopes: p.Scopes, UsernameClaim: p.UsernameClaim, EmailClaim: p.EmailClaim, DisplayNameClaim: p.DisplayNameClaim, AdminClaim: p.AdminClaim, AdminValue: p.AdminValue, RoleMappingsClaim: p.RoleMappingsClaim}
}

// ssoProviders is every single sign-on provider the Dockhand keeps.
func (r *registry) ssoProviders(ctx context.Context) ([]dockhand.GetAuthOidcResponse, error) {
	listed, err := r.dock.GetAuthOidc(ctx)
	if err != nil {
		return nil, fmt.Errorf("the single sign-on providers could not be listed: %w", refusal(err))
	}

	return listed.Model, nil
}

// ssoIn is the provider of a list a call names, by its name whatever its case or by its number. Dockhand lets two share a name, and a name two share
// is refused with their numbers.
func ssoIn(listed []dockhand.GetAuthOidcResponse, a ssoArg) (*dockhand.GetAuthOidcResponse, error) {
	wanted, err := named(a.Provider, "provider")
	if err != nil {
		return nil, err
	}
	if wanted == "" {
		return nil, errors.New("provider is required: the single sign-on provider's name or number, as sso_list gives them")
	}
	var found []*dockhand.GetAuthOidcResponse
	names := make([]string, 0, len(listed))
	for i := range listed {
		if strconv.Itoa(listed[i].Id) == wanted {
			return &listed[i], nil
		}
		if strings.EqualFold(listed[i].Name, wanted) {
			found = append(found, &listed[i])
		}
		names = append(names, listed[i].Name)
	}
	switch len(found) {
	case 0:
		return nil, fmt.Errorf("no single sign-on provider is named or numbered %q: the Dockhand keeps %s", wanted, cmp.Or(strings.Join(names, ", "), "none"))
	case 1:
		return found[0], nil
	default:
		numbers := make([]string, 0, len(found))
		for _, p := range found {
			numbers = append(numbers, strconv.Itoa(p.Id))
		}

		return nil, fmt.Errorf("%d single sign-on providers are named %q: give the number of one, %s", len(found), wanted, strings.Join(numbers, " or "))
	}
}

// ssoChangeOut is what saving or changing a single sign-on provider answers.
type ssoChangeOut struct {
	Provider *ssoRow  `json:"provider,omitempty" jsonschema:"the provider as Dockhand has it now, read back; absent for a dry run of saving one"`
	Changes  []string `json:"changes"            jsonschema:"what was done, in words; empty when nothing needed doing"`
	DryRun   bool     `json:"dry_run,omitempty"  jsonschema:"nothing was done: changes says what would be"`
	Note     string   `json:"note,omitempty"`
}

// neverTried is what no tool here can say of a single sign-on provider.
const neverTried = "Dockhand checks nothing of what it is given here, and lets only a signed-in session try a provider, in its web interface: whether anyone can sign in through this one, and what its claims then do, is told there and not by a tool"

//nolint:gocyclo // four tools registered in one function, each a handler of its own: the count is theirs added up
func registerSSOTools(r *registry) {
	type ssoListOut struct {
		Providers []ssoRow `json:"providers"      jsonschema:"the single sign-on providers the Dockhand keeps, by name"`
		Total     int      `json:"total"`
		Note      string   `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "sso_list",
		Description: "List the single sign-on (OpenID Connect) providers the Dockhand keeps, switched on or not: each one's name, the provider's address, what Dockhand is known to it as, where it sends a user back to, its scopes, and the claims its record names for a user's name, email and display name, for an administrator and for roles. One that is switched on is offered to whoever signs in, beside the name and password Dockhand keeps itself. The secret Dockhand signs in to the provider with is never given back.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, ssoListOut, error) {
		listed, err := r.ssoProviders(ctx)
		if err != nil {
			return nil, ssoListOut{}, err
		}
		out := ssoListOut{Providers: make([]ssoRow, 0, len(listed)), Total: len(listed)}
		for i := range listed {
			out.Providers = append(out.Providers, ssoRowOf(&listed[i]))
		}
		slices.SortStableFunc(out.Providers, func(a, b ssoRow) int {
			return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.ID, b.ID))
		})
		if out.Total == 0 {
			out.Note = "the Dockhand keeps no single sign-on provider: its users sign in with a name and password it keeps itself"
		}

		return nil, out, nil
	})

	type ssoCreateIn struct {
		dryRunArg

		Name              string `json:"name"                          jsonschema:"what to call the provider: what whoever signs in sees, and the name the sso tools then take"`
		IssuerURL         string `json:"issuer_url"                    jsonschema:"the address of the provider, with its scheme: https://sso.example.com/realms/main"`
		ClientID          string `json:"client_id"                     jsonschema:"what Dockhand is known to the provider as"`
		ClientSecret      string `json:"client_secret"                 jsonschema:"the secret the provider gave for that client. It is sent to Dockhand and kept there, and is never given back"`
		RedirectURI       string `json:"redirect_uri"                  jsonschema:"where the provider sends a user back to: this Dockhand's own address as its users reach it, then /api/auth/oidc/callback"`
		Enabled           bool   `json:"enabled,omitempty"             jsonschema:"switch it on, so that it is offered to whoever signs in. Left out, it is saved switched off"`
		Scopes            string `json:"scopes,omitempty"              jsonschema:"the scopes to name, with spaces between; openid profile email unless given"`
		UsernameClaim     string `json:"username_claim,omitempty"      jsonschema:"the claim to name for a user's name; preferred_username unless given"`
		EmailClaim        string `json:"email_claim,omitempty"         jsonschema:"email unless given"`
		DisplayNameClaim  string `json:"display_name_claim,omitempty"  jsonschema:"name unless given"`
		AdminClaim        string `json:"admin_claim,omitempty"         jsonschema:"the claim to name in the record for an administrator, with admin_value; none unless given"`
		AdminValue        string `json:"admin_value,omitempty"         jsonschema:"the value of admin_claim to name for an administrator"`
		RoleMappingsClaim string `json:"role_mappings_claim,omitempty" jsonschema:"the claim to name in the record for roles; groups unless given"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "sso_create",
		Description: "Save a single sign-on (OpenID Connect) provider on the Dockhand: its name, the provider's address, the client id and secret the provider gave for Dockhand, and where the provider sends a user back to. It is saved switched off unless enabled says otherwise; switched on, it is offered to whoever signs in: treat it as a way in to this Dockhand for anyone that provider vouches for. " + neverTried + ". A name already in use is refused. The secret is kept by Dockhand and never given back.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ssoCreateIn) (*mcp.CallToolResult, ssoChangeOut, error) {
		name, issuer, id, redirect := strings.TrimSpace(in.Name), strings.TrimSpace(in.IssuerURL), strings.TrimSpace(in.ClientID), strings.TrimSpace(in.RedirectURI)
		if name == "" || id == "" || in.ClientSecret == "" {
			return nil, ssoChangeOut{}, errors.New("name, issuer_url, client_id, client_secret and redirect_uri are all required: Dockhand saves no provider without each")
		}
		for what, address := range map[string]string{"issuer_url": issuer, "redirect_uri": redirect} {
			if !strings.HasPrefix(address, "https://") && !strings.HasPrefix(address, "http://") {
				return nil, ssoChangeOut{}, fmt.Errorf("%s is %q: it is an address with its scheme, as https://sso.example.com. Dockhand would save it all the same", what, address)
			}
		}
		if (strings.TrimSpace(in.AdminClaim) == "") != (strings.TrimSpace(in.AdminValue) == "") {
			return nil, ssoChangeOut{}, errors.New("admin_claim and admin_value go together: give both, or neither")
		}
		listed, err := r.ssoProviders(ctx)
		if err != nil {
			return nil, ssoChangeOut{}, err
		}
		if slices.ContainsFunc(listed, func(p dockhand.GetAuthOidcResponse) bool { return strings.EqualFold(p.Name, name) }) {
			return nil, ssoChangeOut{}, fmt.Errorf("a single sign-on provider is already named %q: sso_list gives it, and another name is needed for another", name)
		}

		out := ssoChangeOut{Changes: []string{}, DryRun: in.DryRun}
		what := fmt.Sprintf("the single sign-on provider %s at %s, switched %s", name, issuer, map[bool]string{true: "on", false: "off"}[in.Enabled])
		if in.DryRun {
			out.Changes = append(out.Changes, "would save "+what)

			return nil, out, nil
		}

		made, err := r.dock.PostAuthOidc(ctx, dockhand.PostAuthOidcRequest{Name: name, IssuerUrl: issuer, ClientId: id, ClientSecret: in.ClientSecret, RedirectUri: redirect, Enabled: &in.Enabled, Scopes: strings.TrimSpace(in.Scopes), UsernameClaim: strings.TrimSpace(in.UsernameClaim), EmailClaim: strings.TrimSpace(in.EmailClaim), DisplayNameClaim: strings.TrimSpace(in.DisplayNameClaim), AdminClaim: strings.TrimSpace(in.AdminClaim), AdminValue: strings.TrimSpace(in.AdminValue), RoleMappingsClaim: strings.TrimSpace(in.RoleMappingsClaim)})
		if err != nil {
			return nil, out, fmt.Errorf("%s was not saved: %w", name, refusal(err))
		}
		out.Changes = append(out.Changes, "saved "+what)
		if back, err := r.dock.GetAuthOidcById(ctx, made.Model.Id); err != nil {
			out.Note = "it was saved, and could not be read back: " + refusal(err).Error()
		} else {
			row := ssoRowOf(new(dockhand.GetAuthOidcResponse(*back.Model)))
			out.Provider = &row
		}
		if in.Enabled {
			out.Note = note(out.Note, "it is offered to whoever signs in from now on; "+neverTried)
		}

		return nil, out, nil
	})

	type ssoEditIn struct {
		ssoArg
		dryRunArg

		Name              string  `json:"name,omitempty"                jsonschema:"another name for the provider"`
		IssuerURL         string  `json:"issuer_url,omitempty"          jsonschema:"another address for the provider, with its scheme"`
		ClientID          string  `json:"client_id,omitempty"`
		ClientSecret      string  `json:"client_secret,omitempty"       jsonschema:"another secret. It is sent to Dockhand and kept there, and is never given back"`
		RedirectURI       string  `json:"redirect_uri,omitempty"`
		Enabled           *bool   `json:"enabled,omitempty"             jsonschema:"true switches it on, so that it is offered to whoever signs in; false switches it off"`
		Scopes            string  `json:"scopes,omitempty"`
		UsernameClaim     string  `json:"username_claim,omitempty"`
		EmailClaim        string  `json:"email_claim,omitempty"`
		DisplayNameClaim  string  `json:"display_name_claim,omitempty"`
		AdminClaim        *string `json:"admin_claim,omitempty"         jsonschema:"another claim to name for an administrator; empty to forget it and its value"`
		AdminValue        string  `json:"admin_value,omitempty"`
		RoleMappingsClaim string  `json:"role_mappings_claim,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "sso_edit",
		Description: "Change a single sign-on (OpenID Connect) provider the Dockhand keeps: its name, the provider's address, the client id and secret, where a user is sent back to, its scopes, the claims its record names, and whether it is switched on. What is not given is kept, the secret included. Switching one on offers it to whoever signs in, and switching it off takes it away at once; an empty admin_claim has the claim and value named for an administrator forgotten. " + neverTried + ".",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ssoEditIn) (*mcp.CallToolResult, ssoChangeOut, error) {
		for what, address := range map[string]string{"issuer_url": strings.TrimSpace(in.IssuerURL), "redirect_uri": strings.TrimSpace(in.RedirectURI)} {
			if address != "" && !strings.HasPrefix(address, "https://") && !strings.HasPrefix(address, "http://") {
				return nil, ssoChangeOut{}, fmt.Errorf("%s is %q: it is an address with its scheme, as https://sso.example.com. Dockhand would save it all the same", what, address)
			}
		}
		forget := in.AdminClaim != nil && strings.TrimSpace(*in.AdminClaim) == ""
		if forget && strings.TrimSpace(in.AdminValue) != "" {
			return nil, ssoChangeOut{}, errors.New("an empty admin_claim forgets the claim and its value, so it is not given with a value to keep")
		}
		listed, err := r.ssoProviders(ctx)
		if err != nil {
			return nil, ssoChangeOut{}, err
		}
		saved, err := ssoIn(listed, in.ssoArg)
		if err != nil {
			return nil, ssoChangeOut{}, err
		}
		row := ssoRowOf(saved)
		out := ssoChangeOut{Provider: &row, Changes: []string{}, DryRun: in.DryRun}

		done := func(did, would string) string {
			if in.DryRun {
				return would
			}

			return did
		}
		request := dockhand.PutAuthOidcByIdRequest{}
		given := 0
		// each text that is given and is not what is kept, with where it goes and what it is called
		texts := []struct {
			given, kept string
			into        *string
			what        string
		}{
			{in.IssuerURL, saved.IssuerUrl, &request.IssuerUrl, "the provider's address"},
			{in.ClientID, saved.ClientId, &request.ClientId, "its client id"},
			{in.RedirectURI, saved.RedirectUri, &request.RedirectUri, "where a user is sent back to"},
			{in.Scopes, saved.Scopes, &request.Scopes, "its scopes"},
			{in.UsernameClaim, saved.UsernameClaim, &request.UsernameClaim, "the claim named for a user's name"},
			{in.EmailClaim, saved.EmailClaim, &request.EmailClaim, "the claim named for a user's email"},
			{in.DisplayNameClaim, saved.DisplayNameClaim, &request.DisplayNameClaim, "the claim named for a user's display name"},
			{in.AdminValue, saved.AdminValue, &request.AdminValue, "the value named for an administrator"},
			{in.RoleMappingsClaim, saved.RoleMappingsClaim, &request.RoleMappingsClaim, "the claim named for roles"},
		}
		if in.AdminClaim != nil {
			texts = append(texts, struct {
				given, kept string
				into        *string
				what        string
			}{*in.AdminClaim, saved.AdminClaim, &request.AdminClaim, "the claim named for an administrator"})
		}
		if name := strings.TrimSpace(in.Name); name != "" {
			given++
			if name != saved.Name {
				if slices.ContainsFunc(listed, func(p dockhand.GetAuthOidcResponse) bool { return p.Id != saved.Id && strings.EqualFold(p.Name, name) }) {
					return nil, out, fmt.Errorf("another single sign-on provider is named %q, so %s is not renamed to it", name, saved.Name)
				}
				request.Name = name
				out.Changes = append(out.Changes, fmt.Sprintf("%s it from %s to %s", done("renamed", "would rename"), saved.Name, name))
			}
		}
		for _, text := range texts {
			value := strings.TrimSpace(text.given)
			if value == "" {
				continue
			}
			given++
			if value != text.kept {
				*text.into = value
				out.Changes = append(out.Changes, fmt.Sprintf("%s %s to %s", done("changed", "would change"), text.what, value))
			}
		}
		if in.ClientSecret != "" {
			given++
			request.ClientSecret = in.ClientSecret
			out.Changes = append(out.Changes, done("changed", "would change")+" its secret to the one given")
		}
		if in.Enabled != nil {
			given++
			if *in.Enabled != yes(saved.Enabled) {
				request.Enabled = in.Enabled
				out.Changes = append(out.Changes, map[bool]string{true: done("switched", "would switch") + " it on", false: done("switched", "would switch") + " it off"}[*in.Enabled])
			}
		}
		sends := len(out.Changes) > 0
		forget = forget && (saved.AdminClaim != "" || saved.AdminValue != "")
		if forget {
			out.Changes = append(out.Changes, done("forgot", "would forget")+" the claim and value named for an administrator")
		}
		if len(out.Changes) == 0 {
			if given == 0 && in.AdminClaim == nil {
				return nil, out, errors.New("nothing to change was given: name, issuer_url, client_id, client_secret, redirect_uri, enabled, scopes or one of the claims")
			}
			out.Note = "nothing to do: it is that way already"

			return nil, out, nil
		}
		if in.DryRun {
			return nil, out, nil
		}

		var back *dockhand.PutAuthOidcByIdResponse
		if sends {
			changed, failed := r.dock.PutAuthOidcById(ctx, saved.Id, request)
			if failed != nil {
				out.Changes = []string{}

				return nil, out, fmt.Errorf("%s was not changed: %w", saved.Name, refusal(failed))
			}
			back = changed.Model
		}
		if forget {
			forgot, failed := r.dock.ForgetOidcAdminClaim(ctx, saved.Id)
			if failed != nil {
				return nil, out, fmt.Errorf("%s was changed in part, and the claim named for an administrator was not forgotten: %w", saved.Name, refusal(failed))
			}
			back = forgot.Model
		}
		row = ssoRowOf(new(dockhand.GetAuthOidcResponse(*back)))
		if request.Enabled != nil && *request.Enabled {
			out.Note = "it is offered to whoever signs in from now on; " + neverTried
		}

		return nil, out, nil
	})

	type ssoDeleteIn struct {
		ssoArg

		Confirm bool `json:"confirm,omitempty" jsonschema:"remove it: without this the answer says what would go and nothing is removed"`
	}
	type ssoDeleteOut struct {
		Provider string   `json:"provider"`
		Removed  bool     `json:"removed"            jsonschema:"the provider was removed, and is read back as gone"`
		WouldGo  []string `json:"would_go,omitempty" jsonschema:"what removing it takes away, said before anything is removed"`
		Note     string   `json:"note,omitempty"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "sso_delete",
		Description: "Remove a single sign-on (OpenID Connect) provider from the Dockhand: it forgets the provider, its address and the client id and secret kept for it, and no longer offers it to whoever signs in. Nothing at the provider is touched. To keep one and stop offering it, sso_edit switches it off. Without confirm the answer says what would go and removes nothing.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ssoDeleteIn) (*mcp.CallToolResult, ssoDeleteOut, error) {
		listed, err := r.ssoProviders(ctx)
		if err != nil {
			return nil, ssoDeleteOut{}, err
		}
		saved, err := ssoIn(listed, in.ssoArg)
		if err != nil {
			return nil, ssoDeleteOut{}, err
		}
		out := ssoDeleteOut{Provider: saved.Name}
		isDefault := false
		if settings, err := r.dock.GetAuthSettings(ctx); err == nil {
			isDefault = settings.Model.DefaultProvider == "oidc:"+strconv.Itoa(saved.Id)
		}
		if !in.Confirm {
			out.WouldGo = []string{fmt.Sprintf("Dockhand's record of %s at %s, with the client id and secret kept for it", saved.Name, saved.IssuerUrl)}
			out.Note = "nothing was removed: call again with confirm to remove it. Nothing at the provider is touched either way"
			if isDefault {
				out.Note = note(out.Note, "it is marked the default provider, and Dockhand goes on naming it as that once it is removed: login_set marks another")
			}

			return nil, out, nil
		}

		gone, err := r.dock.DeleteAuthOidcById(ctx, saved.Id)
		if err != nil {
			return nil, out, fmt.Errorf("%s was not removed: %w", saved.Name, refusal(err))
		}
		if !yes(gone.Model.Success) {
			return nil, out, fmt.Errorf("the removal of %s was answered without its being said to have succeeded: sso_list says whether it is still there", saved.Name)
		}
		left, err := r.ssoProviders(ctx)
		if err != nil {
			out.Note = "it was removed, and the providers could not be listed afterwards: " + err.Error()

			return nil, out, nil //nolint:nilerr // the removal went through: that it could not be read back is said in the answer
		}
		if slices.ContainsFunc(left, func(p dockhand.GetAuthOidcResponse) bool { return p.Id == saved.Id }) {
			return nil, out, fmt.Errorf("the removal of %s was answered as done, and it is still listed", saved.Name)
		}
		out.Removed = true
		if isDefault {
			out.Note = "it was marked the default provider, and Dockhand goes on naming it as that: login_set marks another"
		}

		return nil, out, nil
	})
}
