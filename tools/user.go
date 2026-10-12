package tools

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/pandorest/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// shortestPassword is the fewest characters Dockhand takes for a password
	shortestPassword = 8
	// sessionShortest and sessionLongest bound how long a signed-in session may be set to last, in seconds: Dockhand makes a time outside them the
	// nearer of the two without a word, and takes 0 for one that never ends
	sessionShortest = 3600
	sessionLongest  = 604800
	// loginIsOff is what Dockhand says of its tokens while its login is switched off
	loginIsOff = "Authentication is not enabled"
	// openToAll is what a Dockhand with its login off is
	openToAll = "the login is switched off: the Dockhand asks nobody who they are, and anyone who can reach its address can do everything it can, on every Docker host it manages"
	// madeAnAdmin is what Dockhand does with every user it is asked to make
	madeAnAdmin = "Dockhand made it an administrator, as it does every user it is asked to make: it can do everything this Dockhand can"
	// adminHoldsNothing is what taking the administrator's mark from a user was seen to do on a Dockhand with no licence
	adminHoldsNothing = "on a Dockhand with no licence a user that is no administrator was not seen to be refused anything, making itself one again included: this is no way to hold a user to less"
	// lastAdministrator is why the only user that is both switched on and an administrator is left as it is, which Dockhand itself does not see to
	// unless it is the only user there is
	lastAdministrator = "%s is the only user that is both switched on and an administrator, so this is not done. Dockhand would do it: with no other user it switches its login off, which leaves it open to anyone who can reach it, and with any other user it goes ahead, and can leave the login on with nobody able to sign in. Make another administrator first (user_create), or switch the login off knowingly with login_set"
	// shutOut is what this server is once the Dockhand stops taking what it signs in with
	shutOut = "it was done, and the Dockhand now turns this server away: what it signs in with is no longer taken, and every tool is refused until it is started with an API token of a user that is switched on. A token is made signed in to Dockhand's web interface, under Profile, API tokens, and never with a tool"
)

// named is a thing an argument names, as text: a name as it came, a number as its digits, and nothing for an argument left out.
func named(given any, what string) (string, error) {
	switch given := given.(type) {
	case nil:
		return "", nil
	case string:
		return strings.TrimSpace(given), nil
	case float64:
		if given != float64(int(given)) {
			return "", fmt.Errorf("%s is %v: its number is a whole one", what, given)
		}

		return strconv.Itoa(int(given)), nil
	default:
		return "", fmt.Errorf("%s is %v: it is a name or a number", what, given)
	}
}

// userArg names one user of the Dockhand.
type userArg struct {
	User any `json:"user,omitempty" jsonschema:"the user, by its name or its number as user_list gives them"`
}

// userRow is one user of the Dockhand.
type userRow struct {
	ID          int    `json:"id"`
	Username    string `json:"username"               jsonschema:"the name it signs in with"`
	DisplayName string `json:"display_name,omitempty" jsonschema:"the name Dockhand shows for it"`
	Email       string `json:"email,omitempty"`
	Admin       bool   `json:"admin"                  jsonschema:"marked an administrator, as every user is when it is made"`
	Active      bool   `json:"active"                 jsonschema:"switched on: one that is not can neither sign in nor use its API tokens"`
	Provider    string `json:"provider,omitempty"     jsonschema:"what it signs in through: local for a name and password Dockhand keeps itself"`
	MFA         bool   `json:"mfa,omitempty"          jsonschema:"it has a second step set up for signing in"`
	LastLogin   string `json:"last_login,omitempty"   jsonschema:"when it last signed in; absent for one that never has"`
	Created     string `json:"created,omitempty"`
}

func userRowOf(u *dockhand.GetUsersAnswer) userRow {
	return userRow{ID: int(u.Id), Username: u.Username, DisplayName: u.DisplayName, Email: u.Email, Admin: u.IsAdmin, Active: u.IsActive, Provider: u.AuthProvider, MFA: u.MfaEnabled, LastLogin: when(u.LastLogin), Created: when(u.CreatedAt)}
}

// users is every user the Dockhand has.
func (r *registry) users(ctx context.Context) ([]dockhand.GetUsersAnswer, error) {
	listed, err := r.dock.GetUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("the users could not be listed: %w", refusal(err))
	}

	return listed.Answer, nil
}

// userIn is the user of a list a call names, by its name whatever its case or by its number. A name that is no user's is refused with the ones there
// are.
func userIn(listed []dockhand.GetUsersAnswer, a userArg) (*dockhand.GetUsersAnswer, error) {
	wanted, err := named(a.User, "user")
	if err != nil {
		return nil, err
	}
	if wanted == "" {
		return nil, errors.New("user is required: the user's name or number, as user_list gives them")
	}
	names := make([]string, 0, len(listed))
	for i := range listed {
		if strings.EqualFold(listed[i].Username, wanted) || strconv.Itoa(int(listed[i].Id)) == wanted {
			return &listed[i], nil
		}
		names = append(names, listed[i].Username)
	}

	return nil, fmt.Errorf("no user is named or numbered %q: the Dockhand has %s", wanted, cmp.Or(strings.Join(names, ", "), "none"))
}

// signsIn reports whether a user is one a Dockhand with its login on would let in and let do everything: switched on, and an administrator.
func signsIn(u *dockhand.GetUsersAnswer) bool { return u.IsActive && u.IsAdmin }

// onlyAdministrator reports whether a user is the only one that is both switched on and an administrator: the one the tools leave as it is.
// Dockhand's own guard is narrower. It refuses to switch off, unmark or remove the only user there is, and with a second user of any kind it does all
// three, a last administrator switched off with the login left on among them.
func onlyAdministrator(listed []dockhand.GetUsersAnswer, id int) bool {
	is := false
	for i := range listed {
		switch {
		case int(listed[i].Id) == id:
			is = signsIn(&listed[i])
		case signsIn(&listed[i]):
			return false
		default:
		}
	}

	return is
}

// loginOn reports whether the Dockhand has its login switched on.
func (r *registry) loginOn(ctx context.Context) (bool, error) {
	settings, err := r.dock.GetAuthSettings(ctx)
	if err != nil {
		return false, fmt.Errorf("whether the login is on could not be read: %w", refusal(err))
	}

	return settings.Answer.AuthEnabled, nil
}

// passwordOf is a password checked against the one rule Dockhand has for one, without a word of it going into what is said.
func passwordOf(given string) error {
	if n := utf8.RuneCountInString(given); n < shortestPassword {
		return fmt.Errorf("password is %d characters: Dockhand takes none shorter than %d", n, shortestPassword)
	}

	return nil
}

// emailOf is an email address looked over: Dockhand saves whatever text it is given for one.
func emailOf(given string) (string, error) {
	email := strings.TrimSpace(given)
	if at := strings.Index(email, "@"); at < 1 || at == len(email)-1 || strings.ContainsAny(email, " \t\n") {
		return "", fmt.Errorf("email is %q: it is an address, as someone@example.com", given)
	}

	return email, nil
}

// userChangeOut is what making or changing a user answers.
type userChangeOut struct {
	User    *userRow `json:"user,omitempty"    jsonschema:"the user as Dockhand has it now, read back; absent for a dry run of making one"`
	Changes []string `json:"changes"           jsonschema:"what was done, in words; empty when nothing needed doing"`
	DryRun  bool     `json:"dry_run,omitempty" jsonschema:"nothing was done: changes says what would be"`
	Note    string   `json:"note,omitempty"`
}

// loginState is how the Dockhand is signed in to.
type loginState struct {
	Enabled         bool   `json:"enabled"                    jsonschema:"whether the login is switched on. Off, the Dockhand asks nobody who they are: anyone who can reach it can do everything it can"`
	SessionTimeout  int    `json:"session_timeout"            jsonschema:"how long a signed-in session lasts, in seconds; 0 for one that never ends"`
	DefaultProvider string `json:"default_provider,omitempty" jsonschema:"the provider marked as the default, by its id among providers"`
}

func loginStateOf(enabled bool, timeout int, provider string) loginState {
	return loginState{Enabled: enabled, SessionTimeout: timeout, DefaultProvider: provider}
}

//nolint:gocyclo,maintidx // seven tools registered in one function, each a handler of its own: the count is theirs added up
func registerUserTools(r *registry) {
	type userListOut struct {
		Users []userRow `json:"users"          jsonschema:"the users, by name"`
		Total int       `json:"total"`
		Login bool      `json:"login"          jsonschema:"whether the Dockhand's login is switched on: off, nobody is asked who they are and these users are not asked for"`
		Note  string    `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "user_list",
		Description: "List the users of the Dockhand: each one's name, email and display name, whether it is marked an administrator, whether it is switched on, what it signs in through and when it last did. A user that is switched off can neither sign in nor use its API tokens. Dockhand makes every user an administrator, and has roles to hold one to less only with a licence, which login_get tells. A password is never given back. While the login is switched off nobody is asked who they are, and the users matter once it is on.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, userListOut, error) {
		listed, err := r.users(ctx)
		if err != nil {
			return nil, userListOut{}, err
		}
		out := userListOut{Users: make([]userRow, 0, len(listed)), Total: len(listed)}
		for i := range listed {
			out.Users = append(out.Users, userRowOf(&listed[i]))
		}
		slices.SortStableFunc(out.Users, func(a, b userRow) int { return cmp.Compare(strings.ToLower(a.Username), strings.ToLower(b.Username)) })

		if out.Login, err = r.loginOn(ctx); err != nil {
			out.Note = err.Error()
		} else if !out.Login {
			out.Note = openToAll
		}

		return nil, out, nil
	})

	type userCreateIn struct {
		dryRunArg

		Username    string `json:"username"               jsonschema:"the name the user signs in with, with no spaces in it"`
		Password    string `json:"password"               jsonschema:"the password the user signs in with, of 8 characters or more. It is sent to Dockhand and kept there, and is never given back"`
		Email       string `json:"email,omitempty"`
		DisplayName string `json:"display_name,omitempty" jsonschema:"the name Dockhand shows for the user"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "user_create",
		Description: "Make a user of the Dockhand, who signs in with a name and a password. Dockhand makes every user an administrator: the new user can do everything this Dockhand can, on every Docker host it manages, and with no licence there are no roles to hold it to less. A name already in use is refused, and a password under 8 characters. While the login is switched off the user is made and nobody is asked for it: login_set switches the login on, which needs a user to exist. The password is kept by Dockhand and never given back.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in userCreateIn) (*mcp.CallToolResult, userChangeOut, error) {
		name, display := strings.TrimSpace(in.Username), strings.TrimSpace(in.DisplayName)
		if name == "" || strings.ContainsAny(name, " \t\n") {
			return nil, userChangeOut{}, fmt.Errorf("username is %q: it is the name the user signs in with, with no spaces in it", in.Username)
		}
		if err := passwordOf(in.Password); err != nil {
			return nil, userChangeOut{}, err
		}
		email := ""
		if strings.TrimSpace(in.Email) != "" {
			var err error
			if email, err = emailOf(in.Email); err != nil {
				return nil, userChangeOut{}, err
			}
		}
		listed, err := r.users(ctx)
		if err != nil {
			return nil, userChangeOut{}, err
		}
		if slices.ContainsFunc(listed, func(u dockhand.GetUsersAnswer) bool { return strings.EqualFold(u.Username, name) }) {
			return nil, userChangeOut{}, fmt.Errorf("a user is already named %q: user_list gives it, and another name is needed for another", name)
		}

		out := userChangeOut{Changes: []string{}, DryRun: in.DryRun}
		if in.DryRun {
			out.Changes = append(out.Changes, fmt.Sprintf("would make the user %s, an administrator as Dockhand makes every user", name))

			return nil, out, nil
		}

		made, err := r.dock.PostUsers(ctx, dockhand.PostUsersRequest{Username: name, Password: in.Password, Email: email, DisplayName: display})
		if err != nil {
			return nil, out, fmt.Errorf("%s was not made: %w", name, refusal(err))
		}
		out.Changes = append(out.Changes, "made the user "+name)
		if made.Answer.IsAdmin {
			out.Note = madeAnAdmin
		}

		after, err := r.users(ctx)
		if err != nil {
			out.Note = note(out.Note, "it was made, and could not be read back: "+err.Error())

			return nil, out, nil //nolint:nilerr // the user was made: that it could not be read back is said in the answer
		}
		i := slices.IndexFunc(after, func(u dockhand.GetUsersAnswer) bool { return u.Id == made.Answer.Id })
		if i < 0 {
			return nil, out, fmt.Errorf("the making of %s was answered as done, and it is not listed", name)
		}
		row := userRowOf(&after[i])
		out.User = &row
		if on, err := r.loginOn(ctx); err == nil && !on {
			out.Note = note(out.Note, "the login is switched off, so nobody is asked for this user until login_set switches it on")
		}

		return nil, out, nil
	})

	type userEditIn struct {
		userArg
		dryRunArg

		Username    string  `json:"username,omitempty"     jsonschema:"another name for the user to sign in with"`
		Password    string  `json:"password,omitempty"     jsonschema:"another password, of 8 characters or more. It is sent to Dockhand and kept there, and is never given back"`
		Email       *string `json:"email,omitempty"        jsonschema:"another email address; empty to forget it"`
		DisplayName *string `json:"display_name,omitempty" jsonschema:"another name for Dockhand to show for the user; empty to forget it"`
		Active      *bool   `json:"active,omitempty"       jsonschema:"false switches the user off, so that it can neither sign in nor use its API tokens; true switches it on again"`
		Admin       *bool   `json:"admin,omitempty"        jsonschema:"whether the user is marked an administrator"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "user_edit",
		Description: "Change a user of the Dockhand: the name it signs in with, its password, its email and display name, whether it is switched on and whether it is marked an administrator. What is not given is kept. A new password ends the user's signed-in sessions and leaves its API tokens working. Switching a user off stops its sign-in and its tokens at once, and switching it on again brings both back: if it is the user whose token this server signs in with, this server is turned away until then. The only user that is both switched on and an administrator is neither switched off nor unmarked here. Dockhand itself would do it: with no other user it switches its login off, and with any other it goes ahead, which can leave the login on and nobody able to sign in. With the login off Dockhand changes neither mark for anyone, so both are refused then. " + "Taking the administrator's mark away does not hold a user to less: " + adminHoldsNothing + ".",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in userEditIn) (*mcp.CallToolResult, userChangeOut, error) {
		name := strings.TrimSpace(in.Username)
		if strings.ContainsAny(name, " \t\n") {
			return nil, userChangeOut{}, fmt.Errorf("username is %q: it is the name the user signs in with, with no spaces in it", in.Username)
		}
		if in.Password != "" {
			if err := passwordOf(in.Password); err != nil {
				return nil, userChangeOut{}, err
			}
		}
		email, forgetEmail := "", in.Email != nil && strings.TrimSpace(*in.Email) == ""
		if in.Email != nil && !forgetEmail {
			var err error
			if email, err = emailOf(*in.Email); err != nil {
				return nil, userChangeOut{}, err
			}
		}
		display, forgetDisplay := "", in.DisplayName != nil && strings.TrimSpace(*in.DisplayName) == ""
		if in.DisplayName != nil {
			display = strings.TrimSpace(*in.DisplayName)
		}
		listed, err := r.users(ctx)
		if err != nil {
			return nil, userChangeOut{}, err
		}
		saved, err := userIn(listed, in.userArg)
		if err != nil {
			return nil, userChangeOut{}, err
		}
		row := userRowOf(saved)
		out := userChangeOut{User: &row, Changes: []string{}, DryRun: in.DryRun}

		done := func(did, would string) string {
			if in.DryRun {
				return would
			}

			return did
		}
		request := dockhand.PutUsersByIdRequest{}
		sends := false
		if name != "" && name != saved.Username {
			if slices.ContainsFunc(listed, func(u dockhand.GetUsersAnswer) bool { return u.Id != saved.Id && strings.EqualFold(u.Username, name) }) {
				return nil, out, fmt.Errorf("another user is named %q, so %s is not renamed to it", name, saved.Username)
			}
			request.Username, sends = name, true
			out.Changes = append(out.Changes, fmt.Sprintf("%s it from %s to %s", done("renamed", "would rename"), saved.Username, name))
		}
		if in.Password != "" {
			request.Password, sends = in.Password, true
			out.Changes = append(out.Changes, done("changed", "would change")+" its password to the one given")
		}
		if email != "" && email != saved.Email {
			request.Email, sends = email, true
			out.Changes = append(out.Changes, fmt.Sprintf("%s its email to %s", done("changed", "would change"), email))
		}
		forgetEmail = forgetEmail && saved.Email != ""
		if forgetEmail {
			out.Changes = append(out.Changes, done("forgot", "would forget")+" its email")
		}
		if display != "" && display != saved.DisplayName {
			request.DisplayName, sends = display, true
			out.Changes = append(out.Changes, fmt.Sprintf("%s its display name to %s", done("changed", "would change"), display))
		}
		forgetDisplay = forgetDisplay && saved.DisplayName != ""
		if forgetDisplay {
			out.Changes = append(out.Changes, done("forgot", "would forget")+" its display name")
		}
		marks := false
		if in.Active != nil && *in.Active != saved.IsActive {
			request.IsActive, sends, marks = in.Active, true, true
			out.Changes = append(out.Changes, map[bool]string{true: done("switched", "would switch") + " it on", false: done("switched", "would switch") + " it off"}[*in.Active])
		}
		if in.Admin != nil && *in.Admin != saved.IsAdmin {
			request.IsAdmin, sends, marks = in.Admin, true, true
			out.Changes = append(out.Changes, map[bool]string{true: done("marked", "would mark") + " it an administrator", false: done("took", "would take") + " the administrator's mark from it"}[*in.Admin])
		}
		if len(out.Changes) == 0 {
			if name == "" && in.Password == "" && in.Email == nil && in.DisplayName == nil && in.Active == nil && in.Admin == nil {
				return nil, out, errors.New("nothing to change was given: username, password, email, display_name, active or admin")
			}
			out.Note = "nothing to do: it is that way already"

			return nil, out, nil
		}

		if marks {
			on, failed := r.loginOn(ctx)
			if failed != nil {
				return nil, userChangeOut{}, failed
			}
			if !on {
				return nil, userChangeOut{}, errors.New("the login is switched off, and with it off Dockhand answers a change to who is switched on or an administrator as done and does not make it: switch the login on first (login_set), or leave active and admin out")
			}
		}
		lessens := (request.IsActive != nil && !*request.IsActive) || (request.IsAdmin != nil && !*request.IsAdmin)
		if lessens && onlyAdministrator(listed, int(saved.Id)) {
			return nil, userChangeOut{}, fmt.Errorf(lastAdministrator, saved.Username)
		}
		if in.DryRun {
			return nil, out, nil
		}

		if sends {
			if _, err := r.dock.PutUsersById(ctx, saved.Id, request); err != nil {
				out.Changes = []string{}

				return nil, out, fmt.Errorf("%s was not changed: %w", saved.Username, refusal(err))
			}
		}
		if forgetEmail || forgetDisplay {
			if _, err := r.dock.ForgetUserDetails(ctx, int(saved.Id), forgetEmail, forgetDisplay); err != nil {
				return nil, out, fmt.Errorf("%s was changed in part, and what was to be forgotten was not: %w", saved.Username, refusal(err))
			}
		}

		after, err := r.users(ctx)
		if client.IsUnauthorized(err) {
			out.User, out.Note = nil, shutOut

			return nil, out, nil
		}
		if err != nil {
			out.Note = "it was changed, and could not be read back: " + err.Error()

			return nil, out, nil //nolint:nilerr // the change went through: that it could not be read back is said in the answer
		}
		i := slices.IndexFunc(after, func(u dockhand.GetUsersAnswer) bool { return u.Id == saved.Id })
		if i < 0 {
			return nil, out, fmt.Errorf("%s was changed, and is no longer listed", saved.Username)
		}
		row = userRowOf(&after[i])
		if (request.IsActive != nil && *request.IsActive != row.Active) || (request.IsAdmin != nil && *request.IsAdmin != row.Admin) {
			return nil, out, fmt.Errorf("Dockhand answered the change to %s as done and did not make it: it is switched on %t and an administrator %t", row.Username, row.Active, row.Admin) //nolint:staticcheck // ST1005: the sentence begins with a name
		}
		if in.Password != "" {
			out.Note = note(out.Note, "a new password ends the user's signed-in sessions, and its API tokens go on working")
		}
		if request.IsActive != nil && !*request.IsActive {
			out.Note = note(out.Note, "it can no longer sign in, and its API tokens are refused until it is switched on again")
		}
		if request.IsAdmin != nil && !*request.IsAdmin {
			out.Note = note(out.Note, adminHoldsNothing)
		}

		return nil, out, nil
	})

	type userDeleteIn struct {
		userArg

		Confirm bool `json:"confirm,omitempty" jsonschema:"remove the user: without this the answer says what would go and nothing is removed"`
	}
	type userDeleteOut struct {
		User    string   `json:"user"`
		Removed bool     `json:"removed"            jsonschema:"the user was removed, and is read back as gone"`
		WouldGo []string `json:"would_go,omitempty" jsonschema:"what removing it takes away, said before anything is removed"`
		Note    string   `json:"note,omitempty"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "user_delete",
		Description: "Remove a user of the Dockhand, for good: it can no longer sign in, and every API token it made stops working. If it is the user whose token this server signs in with, this server is turned away from then on. The only user that is both switched on and an administrator is not removed while the login is on. Dockhand itself would remove it: with no other user it switches its login off, and with any other it goes ahead, whoever is left. To keep a user and stop it signing in, user_edit switches it off. Without confirm the answer says what would go and removes nothing.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in userDeleteIn) (*mcp.CallToolResult, userDeleteOut, error) {
		listed, err := r.users(ctx)
		if err != nil {
			return nil, userDeleteOut{}, err
		}
		saved, err := userIn(listed, in.userArg)
		if err != nil {
			return nil, userDeleteOut{}, err
		}
		out := userDeleteOut{User: saved.Username}
		on, err := r.loginOn(ctx)
		if err != nil {
			return nil, out, err
		}
		if on && onlyAdministrator(listed, int(saved.Id)) {
			return nil, out, fmt.Errorf(lastAdministrator, saved.Username)
		}
		if !in.Confirm {
			out.WouldGo = []string{fmt.Sprintf("the user %s, its password and every API token it made", saved.Username)}
			out.Note = "nothing was removed: call again with confirm to remove it"
			if on {
				out.Note = note(out.Note, "if this is the user whose token this server signs in with, this server is turned away once it is removed")
			}

			return nil, out, nil
		}

		gone, err := r.dock.DeleteUsersById(ctx, saved.Id, dockhand.DeleteUsersByIdOptions{})
		if err != nil {
			return nil, out, fmt.Errorf("%s was not removed: %w", saved.Username, refusal(err))
		}
		if !gone.Answer.Success {
			return nil, out, fmt.Errorf("the removal of %s was answered without its being said to have succeeded: user_list says whether it is still there", saved.Username)
		}
		left, err := r.users(ctx)
		if client.IsUnauthorized(err) {
			out.Removed, out.Note = true, shutOut

			return nil, out, nil
		}
		if err != nil {
			out.Note = "it was removed, and the users could not be listed afterwards: " + err.Error()

			return nil, out, nil //nolint:nilerr // the removal went through: that it could not be read back is said in the answer
		}
		if slices.ContainsFunc(left, func(u dockhand.GetUsersAnswer) bool { return u.Id == saved.Id }) {
			return nil, out, fmt.Errorf("the removal of %s was answered as done, and it is still listed", saved.Username)
		}
		out.Removed = true

		return nil, out, nil
	})

	type loginProvider struct {
		ID   string `json:"id"   jsonschema:"what login_set takes to make it the default: local, or oidc:3"`
		Name string `json:"name"`
		Type string `json:"type" jsonschema:"local for a name and password Dockhand keeps itself, oidc for a single sign-on provider"`
	}
	type loginGetOut struct {
		loginState

		Providers []loginProvider `json:"providers"      jsonschema:"what whoever signs in is offered: local, and each single sign-on provider that is switched on"`
		Licensed  bool            `json:"licensed"       jsonschema:"whether a licence is in use. Roles, which hold a user to less than everything, are refused without one"`
		Note      string          `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "login_get",
		Description: "Say how the Dockhand is signed in to: whether its login is switched on at all, how long a signed-in session lasts, what whoever signs in is offered (a name and password Dockhand keeps, and each single sign-on provider that is switched on) and which of those is marked the default, and whether the Dockhand has a licence in use. With the login off the Dockhand asks nobody who they are. user_list gives the users, sso_list the single sign-on providers whether switched on or not, and token_list the API tokens of the user this server signs in as.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, loginGetOut, error) {
		settings, err := r.dock.GetAuthSettings(ctx)
		if err != nil {
			return nil, loginGetOut{}, fmt.Errorf("how the Dockhand is signed in to could not be read: %w", refusal(err))
		}
		out := loginGetOut{loginState: loginStateOf(settings.Answer.AuthEnabled, settings.Answer.SessionTimeout, settings.Answer.DefaultProvider), Providers: []loginProvider{}}
		if !out.Enabled {
			out.Note = openToAll
		}

		offered, err := r.dock.GetAuthProviders(ctx)
		if err != nil {
			out.Note = note(out.Note, "what is offered at sign-in could not be read: "+refusal(err).Error())
		} else {
			for i := range offered.Answer.Providers {
				out.Providers = append(out.Providers, loginProvider{ID: offered.Answer.Providers[i].Id, Name: offered.Answer.Providers[i].Name, Type: string(offered.Answer.Providers[i].Type)})
			}
		}
		if licence, err := r.dock.GetLicense(ctx); err != nil {
			out.Note = note(out.Note, "whether there is a licence could not be read: "+refusal(err).Error())
		} else {
			out.Licensed = licence.Answer.Active && licence.Answer.Valid
		}

		return nil, out, nil
	})

	type loginSetIn struct {
		Enabled         *bool  `json:"enabled,omitempty"          jsonschema:"true switches the login on and false switches it off. Either needs confirm"`
		SessionTimeout  *int   `json:"session_timeout,omitempty"  jsonschema:"how long a signed-in session lasts, in seconds: from 3600, an hour, to 604800, a week, or 0 for one that never ends"`
		DefaultProvider string `json:"default_provider,omitempty" jsonschema:"the provider to mark as the default, by its id or name as login_get gives them: local, or oidc:3"`
		Confirm         bool   `json:"confirm,omitempty"          jsonschema:"switch the login on or off: without this a call that would do either changes nothing at all, and the answer says what it would mean"`
	}
	type loginSetOut struct {
		loginState

		Changes    []string `json:"changes"               jsonschema:"what was done, in words; empty when nothing was"`
		Would      []string `json:"would,omitempty"       jsonschema:"what switching the login would mean, said before it is done: nothing was changed"`
		TurnedAway bool     `json:"turned_away,omitempty" jsonschema:"the login is on and the Dockhand no longer takes what this server signs in with: every tool is refused until it is started with an API token"`
		Note       string   `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "login_set",
		Description: "Switch the Dockhand's login on or off, and set how long a signed-in session lasts and which provider is marked the default. Switching it off leaves the Dockhand open: it asks nobody who they are, and anyone who can reach its address can do everything it can, on every Docker host it manages; users and their API tokens are kept, and work again once it is back on. Switching it on shuts out whoever cannot sign in, this server included: from then on Dockhand takes only a signed-in session or an API token of a user that is switched on, and a token is made signed in to its web interface, never with a tool. So it is not switched on unless a user that is switched on and an administrator exists, which Dockhand itself does not ask for, and the answer says whether this server was turned away. Either way needs confirm, and without it nothing at all is changed.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in loginSetIn) (*mcp.CallToolResult, loginSetOut, error) {
		provider := strings.TrimSpace(in.DefaultProvider)
		if in.Enabled == nil && in.SessionTimeout == nil && provider == "" {
			return nil, loginSetOut{}, errors.New("nothing to set was given: enabled, session_timeout or default_provider")
		}
		if t := in.SessionTimeout; t != nil && *t != 0 && (*t < sessionShortest || *t > sessionLongest) {
			return nil, loginSetOut{}, fmt.Errorf("session_timeout is %d: it is from %d seconds, an hour, to %d, a week, or 0 for a session that never ends. Dockhand would make it the nearer of the two without a word", *t, sessionShortest, sessionLongest)
		}
		before, err := r.dock.GetAuthSettings(ctx)
		if err != nil {
			return nil, loginSetOut{}, fmt.Errorf("how the Dockhand is signed in to could not be read: %w", refusal(err))
		}
		out := loginSetOut{loginState: loginStateOf(before.Answer.AuthEnabled, before.Answer.SessionTimeout, before.Answer.DefaultProvider), Changes: []string{}}

		if provider != "" {
			offered, failed := r.dock.GetAuthProviders(ctx)
			if failed != nil {
				return nil, out, fmt.Errorf("what is offered at sign-in could not be read: %w", refusal(failed))
			}
			ids := make([]string, 0, len(offered.Answer.Providers))
			found := ""
			for i := range offered.Answer.Providers {
				if p := offered.Answer.Providers[i]; strings.EqualFold(p.Id, provider) || strings.EqualFold(p.Name, provider) {
					found = p.Id
				}
				ids = append(ids, offered.Answer.Providers[i].Id)
			}
			if found == "" {
				return nil, out, fmt.Errorf("default_provider is %q, which is not offered at sign-in: there is %s. Dockhand would save it all the same", provider, strings.Join(ids, ", "))
			}
			provider = found
		}

		switches := in.Enabled != nil && *in.Enabled != out.Enabled
		if switches && *in.Enabled {
			listed, failed := r.users(ctx)
			if failed != nil {
				return nil, out, failed
			}
			able := []string{}
			for i := range listed {
				if signsIn(&listed[i]) {
					able = append(able, listed[i].Username)
				}
			}
			if len(able) == 0 {
				return nil, out, errors.New("the login is not switched on: no user is both switched on and an administrator, so nobody could sign in to switch it off again. Dockhand would switch it on all the same, with any user at all. user_create makes one")
			}
			out.Would = []string{"switch the login on: the Dockhand then takes only a signed-in session or an API token of a user that is switched on. " + strings.Join(able, ", ") + " could sign in", "this server is let in only if it was started with such a token, which Dockhand does not tell while its login is off: without one every tool is refused from then on, and a token is made signed in to the web interface, under Profile, API tokens, never with a tool"}
		}
		if switches && !*in.Enabled {
			out.Would = []string{"switch the login off: the Dockhand then asks nobody who they are, and anyone who can reach its address can do everything it can, on every Docker host it manages", "users and their API tokens are kept, and work again once the login is switched back on"}
		}
		if switches && !in.Confirm {
			out.Note = "nothing was changed: call again with confirm to switch the login " + map[bool]string{true: "on", false: "off"}[*in.Enabled]

			return nil, out, nil
		}
		out.Would = nil

		// a session that never ends is a call of its own, and goes first: once the login is on this server may not be let in to make it
		request := dockhand.PutAuthSettingsRequest{}
		sends := false
		if t := in.SessionTimeout; t != nil && *t != out.SessionTimeout {
			if *t == 0 {
				if _, err := r.dock.NeverEndSessions(ctx); err != nil {
					return nil, out, fmt.Errorf("how long a session lasts was not changed: %w", refusal(err))
				}
				out.SessionTimeout = 0
				out.Changes = append(out.Changes, "set a signed-in session never to end")
			} else {
				request.SessionTimeout, sends = *t, true
			}
		}
		if provider != "" && provider != out.DefaultProvider {
			request.DefaultProvider, sends = provider, true
		}
		if switches {
			request.AuthEnabled, sends = in.Enabled, true
		}
		if !sends {
			if len(out.Changes) == 0 {
				out.Note = "nothing to do: it is that way already"
			}

			return nil, out, nil
		}

		saved, err := r.dock.PutAuthSettings(ctx, request)
		if err != nil {
			return nil, out, fmt.Errorf("how the Dockhand is signed in to was not changed: %w", refusal(err))
		}
		if request.SessionTimeout != 0 {
			out.Changes = append(out.Changes, fmt.Sprintf("set a signed-in session to last %d seconds", request.SessionTimeout))
		}
		if request.DefaultProvider != "" {
			out.Changes = append(out.Changes, "marked "+request.DefaultProvider+" the default provider")
		}
		if switches {
			out.Changes = append(out.Changes, "switched the login "+map[bool]string{true: "on", false: "off"}[*in.Enabled])
		}
		out.loginState = loginStateOf(saved.Answer.AuthEnabled, saved.Answer.SessionTimeout, saved.Answer.DefaultProvider)

		// read back, which is also how this server learns whether it is still let in
		after, err := r.dock.GetAuthSettings(ctx)
		switch {
		case client.IsUnauthorized(err):
			out.TurnedAway, out.Note = true, shutOut
		case err != nil:
			out.Note = "it was changed, and could not be read back: " + refusal(err).Error()
		default:
			out.loginState = loginStateOf(after.Answer.AuthEnabled, after.Answer.SessionTimeout, after.Answer.DefaultProvider)
			if switches && out.Enabled != *in.Enabled {
				return nil, out, fmt.Errorf("the change was answered as done, and the login is read back as %s", map[bool]string{true: "on", false: "off"}[out.Enabled])
			}
			if !out.Enabled {
				out.Note = openToAll
			} else if switches {
				out.Note = "the login is on, and this server is still let in"
			}
		}

		return nil, out, nil
	})

	type tokenRow struct {
		ID       int    `json:"id"`
		Name     string `json:"name"`
		Prefix   string `json:"prefix"              jsonschema:"the characters the token starts with after dh_, to tell it by: never the token itself"`
		Created  string `json:"created"`
		LastUsed string `json:"last_used,omitempty" jsonschema:"when it was last used; absent for one that never was"`
		Expires  string `json:"expires,omitempty"   jsonschema:"when it runs out; absent for one that never does"`
	}
	type tokenListOut struct {
		Tokens []tokenRow `json:"tokens"         jsonschema:"the API tokens of the user this server signs in as, the newest first"`
		Total  int        `json:"total"`
		Note   string     `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "token_list",
		Description: "List the API tokens of the user this server signs in to the Dockhand as, the one it uses among them: each one's name, the characters it starts with, when it was made, when it was last used and when it runs out. It is that one user's tokens and nobody else's, and never a token itself. Dockhand lets a token be made or revoked only by a signed-in session, in its web interface under Profile, API tokens, so no tool does either; removing or switching off a user (user_delete, user_edit) stops all of its tokens. While the login is switched off there are none to list and none is needed.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, tokenListOut, error) {
		out := tokenListOut{Tokens: []tokenRow{}}
		listed, err := r.dock.GetAuthTokens(ctx)
		if err != nil && strings.Contains(err.Error(), loginIsOff) {
			out.Note = "the login is switched off: Dockhand lists no tokens then, and asks for none"

			return nil, out, nil
		}
		if err != nil {
			return nil, out, fmt.Errorf("the tokens could not be listed: %w", refusal(err))
		}
		for i := range listed.Answer {
			t := &listed.Answer[i]
			out.Tokens = append(out.Tokens, tokenRow{ID: t.Id, Name: t.Name, Prefix: t.TokenPrefix, Created: when(t.CreatedAt), LastUsed: when(t.LastUsed), Expires: when(t.ExpiresAt)})
		}
		slices.SortStableFunc(out.Tokens, func(a, b tokenRow) int { return cmp.Compare(b.ID, a.ID) })
		out.Total = len(out.Tokens)

		return nil, out, nil
	})
}
