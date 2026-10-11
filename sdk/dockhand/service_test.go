package dockhand

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/katbyte/pandorest/client"
)

// This file is written by hand, beside the generated ones: it tests what is Dockhand's own about being talked to (service.go).

// aToken is an API token in the shape Dockhand mints them, and no real one.
const aToken = "dh_0000000000000000000000000000000000000000000"

// answering is a server that answers every request with fn, closed when the test ends.
func answering(t *testing.T, fn http.HandlerFunc) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(fn)
	t.Cleanup(srv.Close)

	return srv
}

func TestNewWithoutLogin(t *testing.T) {
	t.Parallel()

	var sent []http.Header
	var mu sync.Mutex
	srv := answering(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sent = append(sent, r.Header.Clone())
		mu.Unlock()
		_, _ = w.Write([]byte(`{"authenticated":false,"authEnabled":false}`))
	})

	open, err := NewWithoutLogin(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := open.GetAuthSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	withToken, err := New(srv.URL, aToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := withToken.GetAuthSession(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(sent) != 2 || sent[0].Get("Authorization") != "" || sent[1].Get("Authorization") != "Bearer "+aToken {
		t.Errorf("Authorization was %q and %q: nothing with no login, and the token as a bearer header with one", sent[0].Get("Authorization"), sent[1].Get("Authorization"))
	}
	if !strings.HasPrefix(sent[0].Get("User-Agent"), "dockhand-mcp/") {
		t.Errorf("User-Agent = %q, want this client's name and version", sent[0].Get("User-Agent"))
	}

	if _, err := NewWithoutLogin("dockhand:3000"); err == nil || !strings.Contains(err.Error(), "http://dockhand:3000") {
		t.Errorf("an address that is none = %v, want it refused with an example of one", err)
	}
}

func TestForgetEnvironmentPublicIP(t *testing.T) {
	t.Parallel()

	var method, path, body string
	srv := answering(t, func(w http.ResponseWriter, r *http.Request) {
		sent, _ := io.ReadAll(r.Body)
		method, path, body = r.Method, r.URL.Path, string(sent)
		if strings.HasSuffix(r.URL.Path, "/9") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"Environment not found"}`))

			return
		}
		_, _ = w.Write([]byte(`{"id":2,"name":"beta","publicIp":null}`))
	})
	c, err := NewWithoutLogin(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	// the one thing sent is the address, empty, which the generated call leaves out
	forgot, err := c.ForgetEnvironmentPublicIP(context.Background(), 2)
	if err != nil || method != http.MethodPut || path != "/api/environments/2" || body != `{"publicIp":""}` || forgot.Model.Name != "beta" || forgot.Model.PublicIp != "" {
		t.Errorf("forgetting sent %s %s %s and answered %+v, %v", method, path, body, forgot.Model, err)
	}
	if _, err := c.ForgetEnvironmentPublicIP(context.Background(), 9); !client.IsNotFound(err) {
		t.Errorf("forgetting the address of an environment there is none of = %v", err)
	}
}

func TestErrorMessages(t *testing.T) {
	t.Parallel()

	for body, want := range map[string]string{
		`{"error":"Environment not found"}`:                                "Environment not found",
		`{"error":"Unauthorized","message":"Authentication required"}`:     "Unauthorized|Authentication required",
		`{"error":"Failed to deploy","details":"compose file is missing"}`: "Failed to deploy|compose file is missing",
		`{"error":"Not found","message":"not found"}`:                      "Not found",
		`{"error":"  "}`: "",
		`Not found`:      "",
		`[]`:             "",
		``:               "",
	} {
		if got := strings.Join(errorMessages([]byte(body)), "|"); got != want {
			t.Errorf("errorMessages(%s) = %q, want %q", body, got, want)
		}
	}
}

func TestNote(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		failure client.Failure
		want    string
	}{
		"a token refused":           {client.Failure{StatusCode: http.StatusUnauthorized, Credentialed: true}, "the API token was refused"},
		"no token where one is due": {client.Failure{StatusCode: http.StatusUnauthorized}, "was sent no API token"},
		"a call kept for a session": {client.Failure{StatusCode: http.StatusUnauthorized, Credentialed: true, Body: []byte(`{"error":"Not authenticated"}`)}, "answers a logged-in session and no API token"},
		"the free edition":          {client.Failure{StatusCode: http.StatusForbidden, Credentialed: true, Body: []byte(`{"error":"Enterprise license required","status":403}`)}, "Enterprise edition"},
		"not allowed":               {client.Failure{StatusCode: http.StatusForbidden, Credentialed: true, Body: []byte(`{"error":"Permission denied"}`)}, "may not do this"},
		"a call that is not there":  {client.Failure{StatusCode: http.StatusNotFound, Body: []byte("Not found")}, "does not answer this call"},
		"a thing that is not there": {client.Failure{StatusCode: http.StatusNotFound, Body: []byte(`{"error":"Environment not found"}`)}, ""},
		"the server's own failure":  {client.Failure{StatusCode: http.StatusInternalServerError, Body: []byte(`{"error":"Failed to restart container"}`)}, ""},
	} {
		got := note(tc.failure)
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("%s: note = %q, want %q", name, got, tc.want)
		}
	}
}

// A refusal reaches its reader as what Dockhand said, with what is known of it here, and is recognised for what it is.
func TestRefusals(t *testing.T) {
	t.Parallel()

	srv := answering(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/environments":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"Unauthorized","message":"Authentication required"}`))
		case "/api/audit":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"Enterprise license required","status":403}`))
		default:
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("Not found"))
		}
	})

	withToken, err := New(srv.URL, aToken)
	if err != nil {
		t.Fatal(err)
	}
	open, err := NewWithoutLogin(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	_, err = withToken.GetEnvironments(context.Background())
	if !client.IsUnauthorized(err) || !strings.Contains(err.Error(), "Authentication required") || !strings.Contains(err.Error(), "the API token was refused") || strings.Contains(err.Error(), aToken) {
		t.Errorf("a token refused = %v", err)
	}
	_, err = open.GetEnvironments(context.Background())
	if !client.IsUnauthorized(err) || !strings.Contains(err.Error(), "login switched on") || !strings.Contains(err.Error(), "Profile, API tokens") {
		t.Errorf("no token where one is due = %v", err)
	}
	_, err = withToken.GetAudit(context.Background(), GetAuditOperationOptions{})
	if !client.IsForbidden(err) || !strings.Contains(err.Error(), "Enterprise license required") || !strings.Contains(err.Error(), "Enterprise edition") {
		t.Errorf("a call of the Enterprise edition = %v", err)
	}
	_, err = withToken.GetBackupConfigs(context.Background(), GetBackupConfigsOperationOptions{})
	if !client.IsNotFound(err) || !strings.Contains(err.Error(), "does not answer this call") {
		t.Errorf("a call the server does not answer = %v", err)
	}
}

// traced collects what a client says it sent and was sent.
type traced struct {
	mu    sync.Mutex
	lines []string
}

func (l *traced) Debugf(format string, args ...any) { l.add(format, args...) }
func (l *traced) Tracef(format string, args ...any) { l.add(format, args...) }

func (l *traced) add(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *traced) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return strings.Join(l.lines, "\n")
}

// Nothing that lets someone in is ever in a trace: not the API token, not a session's cookie, not a password, and not the keys and tokens Dockhand is
// given to reach a host or a repository with.
func TestTheTraceHoldsNoSecret(t *testing.T) {
	t.Parallel()

	srv := answering(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "dockhand_session", Value: "a-session-cookie", Path: "/"}) //nolint:gosec // G124: a canned server's cookie, over the plain HTTP a test is served on
			_, _ = w.Write([]byte(`{"success":true}`))
		case "/api/auth/tokens":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":1,"token":"dh_1111111111111111111111111111111111111111111","tokenPrefix":"11111111"}`))
		default:
			_, _ = w.Write([]byte(`{"id":7,"name":"kept-name","hasHawserToken":true}`))
		}
	})

	log := &traced{}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	session, err := NewWithoutLogin(srv.URL, client.WithLog(log), client.WithCookies(jar))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.PostAuthLogin(context.Background(), PostAuthLoginRequest{Username: "sent-name", Password: "a-login-password", Provider: "local"}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.PostAuthTokens(context.Background(), PostAuthTokensRequest{Name: "a-token-name", Password: "a-login-password"}); err != nil {
		t.Fatal(err)
	}

	withToken, err := New(srv.URL, aToken, client.WithLog(log))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := withToken.PostEnvironments(context.Background(), PostEnvironmentsRequest{Name: "sent-environment", ConnectionType: "hawser-standard", Host: "agent", HawserToken: "an-agent-token", TlsKey: "a-tls-key", TlsCert: "a-certificate"}); err != nil {
		t.Fatal(err)
	}

	said := log.String()
	for _, shown := range []string{"/api/auth/login", "/api/environments", "sent-name", "a-token-name", "sent-environment", "kept-name", "a-certificate"} {
		if !strings.Contains(said, shown) {
			t.Errorf("the trace does not show %q, so it is not the exchanges that were traced:\n%s", shown, said)
		}
	}
	for _, secret := range []string{aToken, "dh_1111111111111111111111111111111111111111111", "a-session-cookie", "a-login-password", "an-agent-token", "a-tls-key"} {
		if strings.Contains(said, secret) {
			t.Errorf("the trace shows %q:\n%s", secret, said)
		}
	}
}

func TestForgetRegistryCredentials(t *testing.T) {
	t.Parallel()

	var method, path, body string
	srv := answering(t, func(w http.ResponseWriter, r *http.Request) {
		sent, _ := io.ReadAll(r.Body)
		method, path, body = r.Method, r.URL.Path, string(sent)
		if strings.HasSuffix(r.URL.Path, "/9") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"Registry not found"}`))

			return
		}
		_, _ = w.Write([]byte(`{"id":2,"name":"mirror","url":"https://mirror.example","username":null,"isDefault":false,"hasCredentials":false}`))
	})
	c, err := NewWithoutLogin(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	// the name and password go empty, which the generated call leaves out, beside the name and address a change must carry
	forgot, err := c.ForgetRegistryCredentials(context.Background(), 2, "mirror", "https://mirror.example")
	if err != nil || method != http.MethodPut || path != "/api/registries/2" || body != `{"name":"mirror","password":"","url":"https://mirror.example","username":""}` || forgot.Model.Name != "mirror" || forgot.Model.Username != "" || forgot.Model.HasCredentials == nil || *forgot.Model.HasCredentials {
		t.Errorf("forgetting sent %s %s %s and answered %+v, %v", method, path, body, forgot.Model, err)
	}
	if _, err := c.ForgetRegistryCredentials(context.Background(), 9, "mirror", "https://mirror.example"); !client.IsNotFound(err) {
		t.Errorf("forgetting the credentials of a registry there is none of = %v", err)
	}
}

func TestSaveGeneralSettings(t *testing.T) {
	t.Parallel()

	var method, path, body string
	fail := false
	srv := answering(t, func(w http.ResponseWriter, r *http.Request) {
		sent, _ := io.ReadAll(r.Body)
		method, path, body = r.Method, r.URL.Path, string(sent)
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"Failed to save general settings"}`))

			return
		}
		_, _ = w.Write([]byte(`{"scanRetentionGraceDays":0,"primaryStackLocation":null,"externalStackPaths":["/srv/stacks"],"showWhatsNew":false,"defaultTimezone":"UTC"}`))
	})
	c, err := NewWithoutLogin(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	// a number that is 0, a text that is empty and a switch that is off are each sent as they are, which the generated call leaves out
	saved, err := c.SaveGeneralSettings(context.Background(), map[string]any{"scanRetentionGraceDays": 0, "primaryStackLocation": "", "externalStackPaths": []string{"/srv/stacks"}, "showWhatsNew": false})
	if err != nil || method != http.MethodPost || path != "/api/settings/general" || body != `{"externalStackPaths":["/srv/stacks"],"primaryStackLocation":"","scanRetentionGraceDays":0,"showWhatsNew":false}` {
		t.Fatalf("saving sent %s %s %s, %v", method, path, body, err)
	}
	if saved.Model.ScanRetentionGraceDays != 0 || saved.Model.PrimaryStackLocation != "" || len(saved.Model.ExternalStackPaths) != 1 || saved.Model.ShowWhatsNew == nil || *saved.Model.ShowWhatsNew || saved.Model.DefaultTimezone != "UTC" {
		t.Errorf("saving answered %+v", saved.Model)
	}

	// a save Dockhand says failed is an error, whatever it kept of it
	fail = true
	if _, err := c.SaveGeneralSettings(context.Background(), map[string]any{"defaultTimezone": "Nowhere/Atall"}); err == nil || !strings.Contains(err.Error(), "Failed to save general settings") {
		t.Errorf("a save that failed = %v", err)
	}
}

func TestForgetUserDetails(t *testing.T) {
	t.Parallel()

	var method, path, body string
	srv := answering(t, func(w http.ResponseWriter, r *http.Request) {
		sent, _ := io.ReadAll(r.Body)
		method, path, body = r.Method, r.URL.Path, string(sent)
		if strings.HasSuffix(r.URL.Path, "/9") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"User not found"}`))

			return
		}
		_, _ = w.Write([]byte(`{"id":2,"username":"second","email":null,"displayName":null,"mfaEnabled":false,"isAdmin":true,"isActive":true,"lastLogin":null,"createdAt":"2026-01-02 03:04:05","updatedAt":"2026-01-02T03:04:06.000Z"}`))
	})
	c, err := NewWithoutLogin(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	// each goes empty, which the generated call leaves out, and only the one asked for is sent
	forgot, err := c.ForgetUserDetails(context.Background(), 2, true, true)
	if err != nil || method != http.MethodPut || path != "/api/users/2" || body != `{"displayName":"","email":""}` || forgot.Model.Username != "second" || forgot.Model.Email != "" || forgot.Model.DisplayName != "" {
		t.Errorf("forgetting both sent %s %s %s and answered %+v, %v", method, path, body, forgot.Model, err)
	}
	if _, err := c.ForgetUserDetails(context.Background(), 2, true, false); err != nil || body != `{"email":""}` {
		t.Errorf("forgetting the email alone sent %s, %v", body, err)
	}
	if _, err := c.ForgetUserDetails(context.Background(), 2, false, true); err != nil || body != `{"displayName":""}` {
		t.Errorf("forgetting the display name alone sent %s, %v", body, err)
	}
	if _, err := c.ForgetUserDetails(context.Background(), 9, true, false); !client.IsNotFound(err) {
		t.Errorf("forgetting the details of a user there is none of = %v", err)
	}
}

func TestNeverEndSessionsAndForgetOidcAdminClaim(t *testing.T) {
	t.Parallel()

	var method, path, body string
	srv := answering(t, func(w http.ResponseWriter, r *http.Request) {
		sent, _ := io.ReadAll(r.Body)
		method, path, body = r.Method, r.URL.Path, string(sent)
		switch {
		case strings.HasSuffix(r.URL.Path, "/9"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"OIDC configuration not found"}`))
		case strings.Contains(r.URL.Path, "/oidc/"):
			_, _ = w.Write([]byte(`{"id":2,"name":"company","enabled":true,"issuerUrl":"https://sso.example","clientId":"dockhand","clientSecret":"********","adminClaim":null,"adminValue":null,"roleMappingsClaim":"groups"}`))
		default:
			_, _ = w.Write([]byte(`{"id":1,"authEnabled":true,"defaultProvider":"local","sessionTimeout":0}`))
		}
	})
	c, err := NewWithoutLogin(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	// a time of 0 is sent as it is, which the generated call leaves out
	never, err := c.NeverEndSessions(context.Background())
	if err != nil || method != http.MethodPut || path != "/api/auth/settings" || body != `{"sessionTimeout":0}` || never.Model.SessionTimeout != 0 || never.Model.AuthEnabled == nil || !*never.Model.AuthEnabled {
		t.Errorf("never ending a session sent %s %s %s and answered %+v, %v", method, path, body, never.Model, err)
	}

	// the claim and its value go empty together
	forgot, err := c.ForgetOidcAdminClaim(context.Background(), 2)
	if err != nil || method != http.MethodPut || path != "/api/auth/oidc/2" || body != `{"adminClaim":"","adminValue":""}` || forgot.Model.Name != "company" || forgot.Model.AdminClaim != "" || forgot.Model.RoleMappingsClaim != "groups" {
		t.Errorf("forgetting the claim sent %s %s %s and answered %+v, %v", method, path, body, forgot.Model, err)
	}
	if _, err := c.ForgetOidcAdminClaim(context.Background(), 9); !client.IsNotFound(err) {
		t.Errorf("forgetting the claim of a provider there is none of = %v", err)
	}
}

func TestForgetTagIcon(t *testing.T) {
	t.Parallel()

	var method, path, body string
	srv := answering(t, func(w http.ResponseWriter, r *http.Request) {
		sent, _ := io.ReadAll(r.Body)
		method, path, body = r.Method, r.URL.Path, string(sent)
		if strings.HasSuffix(r.URL.Path, "/abc") || strings.HasSuffix(r.URL.Path, "/0") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"Invalid tag id"}`))

			return
		}
		_, _ = w.Write([]byte(`{"success":true}`))
	})
	c, err := NewWithoutLogin(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	// the icon is sent empty, which the generated call leaves out
	forgot, err := c.ForgetTagIcon(context.Background(), 7)
	if err != nil || method != http.MethodPut || path != "/api/tags/7" || body != `{"icon":""}` || forgot.Model.Success == nil || !*forgot.Model.Success || forgot.HttpResponse == nil {
		t.Errorf("forgetting an icon sent %s %s %s and answered %+v, %v", method, path, body, forgot.Model, err)
	}
	if refused, err := c.ForgetTagIcon(context.Background(), 0); client.StatusCode(err) != http.StatusBadRequest || refused.Model != nil || refused.HttpResponse == nil {
		t.Errorf("forgetting the icon of a tag there is none of = %+v, %v", refused, err)
	}
}

func TestForgetConfigSetDescription(t *testing.T) {
	t.Parallel()

	var method, path, body string
	srv := answering(t, func(w http.ResponseWriter, r *http.Request) {
		sent, _ := io.ReadAll(r.Body)
		method, path, body = r.Method, r.URL.Path, string(sent)
		if strings.HasSuffix(r.URL.Path, "/9") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"Config set not found"}`))

			return
		}
		_, _ = w.Write([]byte(`{"id":4,"name":"web","description":null,"envVars":[{"key":"MODE","value":"demo"}],"labels":null,"ports":[],"volumes":[],"networkMode":"bridge","restartPolicy":"no","createdAt":"2026-10-10 20:01:30","updatedAt":"2026-10-10T20:01:31.160Z"}`))
	})
	c, err := NewWithoutLogin(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	// the description is sent empty, which the generated call leaves out
	forgot, err := c.ForgetConfigSetDescription(context.Background(), 4)
	if err != nil || method != http.MethodPut || path != "/api/config-sets/4" || body != `{"description":""}` || forgot.Model.Name != "web" || forgot.Model.Description != "" || len(forgot.Model.EnvVars) != 1 || forgot.Model.Labels != nil || forgot.HttpResponse == nil {
		t.Errorf("forgetting a description sent %s %s %s and answered %+v, %v", method, path, body, forgot.Model, err)
	}
	if refused, err := c.ForgetConfigSetDescription(context.Background(), 9); !client.IsNotFound(err) || refused.Model != nil || refused.HttpResponse == nil {
		t.Errorf("forgetting the description of a set there is none of = %+v, %v", refused, err)
	}
}

func TestForgetNavigationPages(t *testing.T) {
	t.Parallel()

	var method, path, query, body string
	srv := answering(t, func(w http.ResponseWriter, r *http.Request) {
		sent, _ := io.ReadAll(r.Body)
		method, path, query, body = r.Method, r.URL.Path, r.URL.RawQuery, string(sent)
		_, _ = w.Write([]byte(`{"success":true,"global":{"landingPage":null,"envClickPage":"logs"}}`))
	})
	c, err := NewWithoutLogin(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	// each page named is sent as nothing, which the generated call leaves out, and only for everyone: a token may change no user's own
	forgot, err := c.ForgetNavigationPages(context.Background(), true, false)
	if err != nil || method != http.MethodPut || path != "/api/settings/navigation" || query != "scope=global" || body != `{"landingPage":null}` || forgot.Model.Success == nil || !*forgot.Model.Success || forgot.Model.Global == nil || forgot.Model.Global.LandingPage != "" || forgot.Model.Global.EnvClickPage != "logs" {
		t.Errorf("forgetting the landing page sent %s %s?%s %s and answered %+v, %v", method, path, query, body, forgot.Model, err)
	}
	if _, err = c.ForgetNavigationPages(context.Background(), true, true); err != nil || body != `{"envClickPage":null,"landingPage":null}` {
		t.Errorf("forgetting both pages sent %s, %v", body, err)
	}
}

func TestSetLogFavorites(t *testing.T) {
	t.Parallel()

	var method, path, body string
	srv := answering(t, func(w http.ResponseWriter, r *http.Request) {
		sent, _ := io.ReadAll(r.Body)
		method, path, body = r.Method, r.URL.Path, string(sent)
		if strings.Contains(body, `"environmentId":9`) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"Failed to update favorites"}`))

			return
		}
		_, _ = w.Write([]byte(`{"favorites":["loose",{"name":"front","containers":["web-1"]}]}`))
	})
	c, err := NewWithoutLogin(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	// names and groups go together in the one list, and none at all is sent as an empty list
	set, err := c.SetLogFavorites(context.Background(), 1, []any{"loose", map[string]any{"name": "front", "containers": []string{"web-1"}}})
	if err != nil || method != http.MethodPost || path != "/api/preferences/favorites" || body != `{"action":"reorder","environmentId":1,"favorites":["loose",{"containers":["web-1"],"name":"front"}]}` || len(set.Model.Favorites) != 2 || set.Model.Favorites[0] != "loose" {
		t.Errorf("setting the favourites sent %s %s %s and answered %+v, %v", method, path, body, set.Model, err)
	}
	if _, err = c.SetLogFavorites(context.Background(), 1, nil); err != nil || body != `{"action":"reorder","environmentId":1,"favorites":[]}` {
		t.Errorf("setting no favourites sent %s, %v", body, err)
	}
	if refused, err := c.SetLogFavorites(context.Background(), 9, nil); client.StatusCode(err) != http.StatusInternalServerError || refused.Model != nil {
		t.Errorf("the favourites of an environment there is none of = %+v, %v", refused, err)
	}
}

func TestForgetStackSecretProvider(t *testing.T) {
	t.Parallel()

	var method, path, query, body string
	srv := answering(t, func(w http.ResponseWriter, r *http.Request) {
		sent, _ := io.ReadAll(r.Body)
		method, path, query, body = r.Method, r.URL.EscapedPath(), r.URL.RawQuery, string(sent)
		if strings.Contains(path, "nope") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"Stack not found"}`))

			return
		}
		_, _ = w.Write([]byte(`{"success":true}`))
	})
	c, err := NewWithoutLogin(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	// the provider is sent as nothing, which the generated call leaves out, with the file Dockhand will not save a stack without
	saved, err := c.ForgetStackSecretProvider(context.Background(), "a web", "services: {}\n", 2)
	if err != nil || method != http.MethodPut || path != "/api/stacks/a%20web/compose" || query != "env=2" || body != `{"content":"services: {}\n","secretProviderId":null}` || saved.Model.Success == nil || !*saved.Model.Success {
		t.Errorf("taking a stack off its provider sent %s %s?%s %s and answered %+v, %v", method, path, query, body, saved.Model, err)
	}
	if _, err = c.ForgetStackSecretProvider(context.Background(), "nope", "x", 1); err == nil || !strings.Contains(err.Error(), "Stack not found") {
		t.Errorf("a stack there is none of = %v", err)
	}
}

func TestForgetGitStackSettings(t *testing.T) {
	t.Parallel()

	var method, path, body string
	srv := answering(t, func(w http.ResponseWriter, r *http.Request) {
		sent, _ := io.ReadAll(r.Body)
		method, path, body = r.Method, r.URL.EscapedPath(), string(sent)
		if strings.HasSuffix(path, "/9") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"Git stack not found"}`))

			return
		}
		_, _ = w.Write([]byte(`{"id":4,"stackName":"web","branch":null,"webhookEnabled":false}`))
	})
	c, err := NewWithoutLogin(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	// each setting named is sent as nothing, which the generated call leaves out, and the ones not named are not sent at all
	saved, err := c.ForgetGitStackSettings(context.Background(), 4, GitStackSettings{Branch: true, SecretProvider: true})
	if err != nil || method != http.MethodPut || path != "/api/git/stacks/4" || body != `{"branch":null,"secretProviderId":null}` || saved.Model.StackName != "web" {
		t.Errorf("taking a git stack's branch and provider away sent %s %s %s and answered %+v, %v", method, path, body, saved.Model, err)
	}
	if _, err = c.ForgetGitStackSettings(context.Background(), 4, GitStackSettings{EnvFilePath: true, ContextDir: true}); err != nil || body != `{"contextDir":null,"envFilePath":null}` {
		t.Errorf("taking a git stack's two paths away sent %s, %v", body, err)
	}

	// a webhook's secret goes only with the webhook switched off, which Dockhand refuses otherwise
	if _, err = c.ForgetGitStackSettings(context.Background(), 4, GitStackSettings{Webhook: true}); err != nil || body != `{"webhookEnabled":false,"webhookSecret":null}` {
		t.Errorf("taking a git stack's webhook away sent %s, %v", body, err)
	}
	if _, err = c.ForgetGitStackSettings(context.Background(), 9, GitStackSettings{Branch: true}); err == nil || !strings.Contains(err.Error(), "Git stack not found") {
		t.Errorf("a git stack there is none of = %v", err)
	}
}

func TestForgetGitRepositoryCredential(t *testing.T) {
	t.Parallel()

	var method, path, body string
	srv := answering(t, func(w http.ResponseWriter, r *http.Request) {
		sent, _ := io.ReadAll(r.Body)
		method, path, body = r.Method, r.URL.EscapedPath(), string(sent)
		if strings.HasSuffix(path, "/9") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"Repository not found"}`))

			return
		}
		_, _ = w.Write([]byte(`{"id":2,"name":"app","url":"http://git.example/app.git","branch":"main","credentialId":null}`))
	})
	c, err := NewWithoutLogin(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	// the credential is sent as nothing, which the generated call leaves out
	saved, err := c.ForgetGitRepositoryCredential(context.Background(), 2)
	if err != nil || method != http.MethodPut || path != "/api/git/repositories/2" || body != `{"credentialId":null}` || saved.Model.Name != "app" || saved.Model.CredentialId != 0 {
		t.Errorf("taking a repository's credential away sent %s %s %s and answered %+v, %v", method, path, body, saved.Model, err)
	}
	if _, err = c.ForgetGitRepositoryCredential(context.Background(), 9); err == nil || !strings.Contains(err.Error(), "Repository not found") {
		t.Errorf("a repository there is none of = %v", err)
	}
}
