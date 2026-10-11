package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// The canned answers below are a 1.0.51 server's own, with invented names.
const (
	sessionLoginOn  = `{"authenticated":false,"authEnabled":true}`
	sessionLoginOff = `{"authenticated":false,"authEnabled":false}`
	changelog       = `[{"version":"1.0.51","date":"2026-10-03","changes":[{"type":"feature","text":"drag to reorder environments"}]},{"version":"1.0.50","date":"2026-09-30","changes":[]}]`
	databaseHealth  = `{"healthy":true,"database":"sqlite","connection":"/app/data/db/dockhand.db","migrationsTable":true,"appliedMigrations":17,"pendingMigrations":0,"schemaVersion":"0016_add_tags","tables":{"expected":27,"found":27,"missing":[]},"timestamp":"2026-10-10T06:34:16.503Z"}`
	twoEnvironments = `[{"id":1,"name":"alpha","connectionType":"socket","socketPath":"/var/run/docker.sock","host":null,"port":2375,"protocol":"http"},{"id":2,"name":"beta","connectionType":"hawser-standard","host":"agent-beta","port":2376,"protocol":"http","hasHawserToken":true}]`
)

// answering is a canned Dockhand that answers everything server_info asks.
func answering(t *testing.T) *dockhandtest.Server {
	t.Helper()

	dock := dockhandtest.New(t)
	dock.Answer("GET /api/auth/session", sessionLoginOn)
	dock.Answer("GET /api/environments", twoEnvironments)
	dock.Answer("GET /api/changelog", changelog)
	dock.Answer("GET /api/health/database", databaseHealth)

	return dock
}

func TestServerInfo(t *testing.T) {
	t.Parallel()

	dock := answering(t)
	out := session(t, dock, Options{}).Call(t, "server_info", nil)

	if out["url"] != dock.URL || out["version"] != "1.0.51" || out["released"] != "2026-10-03" || out["sdk_api_version"] != dockhand.APIVersion {
		t.Errorf("the server = %v", out)
	}
	if !acctest.IsBool(out["login"], true) || out["database"] != "sqlite" || !acctest.IsBool(out["database_healthy"], true) || out["schema_version"] != "0016_add_tags" {
		t.Errorf("its login and database = %v", out)
	}
	if acctest.Num(t, out["environments"], "environments") != 2 {
		t.Errorf("environments = %v, want the two it manages", out["environments"])
	}
	if out["dockhand_mcp_version"] == "" || out["note"] != nil {
		t.Errorf("this build's version and no note = %v", out)
	}
}

// A Dockhand with its login off is asked with no token, and says so: nothing stands between whoever can reach it and what these tools can do.
func TestServerInfoWithoutLogin(t *testing.T) {
	t.Parallel()

	dock := dockhandtest.NewOpen(t)
	dock.Answer("GET /api/auth/session", sessionLoginOff)
	dock.Answer("GET /api/environments", `[]`)
	dock.Answer("GET /api/changelog", changelog)
	dock.Answer("GET /api/health/database", databaseHealth)

	out := session(t, dock, Options{}).Call(t, "server_info", nil)
	said := acctest.Str(out["note"])
	if !acctest.IsBool(out["login"], false) || !strings.Contains(said, "login is switched off") || !strings.Contains(said, "manages no Docker host yet") {
		t.Errorf("a Dockhand with no login and no hosts = %v", out)
	}
}

// What cannot be read of a server that answers is said beside what can.
func TestServerInfoPartly(t *testing.T) {
	t.Parallel()

	dock := answering(t)
	dock.Fail("GET /api/changelog", http.StatusInternalServerError, "Failed to read changelog")
	dock.Fail("GET /api/health/database", http.StatusForbidden, "Permission denied")

	out := session(t, dock, Options{}).Call(t, "server_info", nil)
	said := acctest.Str(out["note"])
	if out["version"] != nil || !strings.Contains(said, "its version could not be read") || !strings.Contains(said, "Failed to read changelog") || !strings.Contains(said, "its database's health could not be read") {
		t.Errorf("a server that keeps two things back = %v", out)
	}
	if acctest.Num(t, out["environments"], "environments") != 2 {
		t.Errorf("what it did give is lost: %v", out)
	}
}

func TestServerInfoRefused(t *testing.T) {
	t.Parallel()

	// the token is not one the server takes: said as that, with what to set
	dock := dockhandtest.New(t)
	dock.Answer("GET /api/auth/session", sessionLoginOn)
	dock.Fail("GET /api/environments", http.StatusUnauthorized, "Unauthorized")
	msg := session(t, dock, Options{}).CallErr(t, "server_info", nil)
	if !strings.Contains(msg, "would not list its environments") || !strings.Contains(msg, "the API token was refused") || !strings.Contains(msg, "DOCKHAND_TOKEN") {
		t.Errorf("a token refused = %q", msg)
	}
	if strings.Contains(msg, dockhandtest.Token) {
		t.Errorf("the refusal repeats the token: %q", msg)
	}

	// the account may not: said as that
	dock.Fail("GET /api/environments", http.StatusForbidden, "Permission denied")
	msg = session(t, dock, Options{}).CallErr(t, "server_info", nil)
	if !strings.Contains(msg, "Permission denied") || !strings.Contains(msg, "the role of the user") {
		t.Errorf("an account that may not = %q", msg)
	}

	// nothing there at all
	gone := dockhandtest.New(t)
	gone.Fail("GET /api/auth/session", http.StatusInternalServerError, "Internal Error")
	msg = session(t, gone, Options{}).CallErr(t, "server_info", nil)
	if !strings.Contains(msg, "could not be asked") || !strings.Contains(msg, "500") {
		t.Errorf("a server that is not there = %q", msg)
	}
}
