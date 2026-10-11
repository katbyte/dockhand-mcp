package tools

import "testing"

func TestIsSecretName(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]bool{
		"POSTGRES_PASSWORD":    true,
		"DB_PASS":              true,
		"db-pwd":               true,
		"API_KEY":              true,
		"apiKey":               true,
		"APIKEY":               true,
		"SSH_PRIVATE_KEY":      true,
		"KEY":                  true,
		"JWT_SECRET":           true,
		"GITHUB_TOKEN":         true,
		"token":                true,
		"AUTH":                 true,
		"BASIC_AUTH_USERS":     true,
		"REGISTRY_CREDENTIALS": true,
		"sshPassphrase":        true,

		"PATH":            false,
		"MODE":            false,
		"KEYBOARD_LAYOUT": false,
		"PASSIVE_MODE":    false,
		"AUTHOR":          false,
		"TZ":              false,
		"MONKEY":          false,
		"":                false,
	} {
		if got := isSecretName(name); got != want {
			t.Errorf("isSecretName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestMaskValue(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, value, want string
		hidden            bool
	}{
		{"POSTGRES_PASSWORD", "hunter2", "***", true},
		{"MODE", "demo", "demo", false},
		{"DATABASE_URL", "postgres://app:hunter2@db:5432/app", "postgres://app:***@db:5432/app", true},
		{"UPSTREAM", "http://backend:8080/path", "http://backend:8080/path", false},
		{"API_KEY", "", "", false},
	} {
		got, hidden := maskValue(tc.name, tc.value)
		if got != tc.want || hidden != tc.hidden {
			t.Errorf("maskValue(%q, %q) = %q, %v; want %q, %v", tc.name, tc.value, got, hidden, tc.want, tc.hidden)
		}
	}
}

func TestMaskText(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		in, want string
		hidden   int
	}{
		{"2026-10-10T06:04:47Z tick 21", "2026-10-10T06:04:47Z tick 21", 0},
		{"connecting with password=hunter2 to db", "connecting with password=*** to db", 1},
		{`config: {"api_key": "abc123", "mode": "demo"}`, `config: {"api_key": ***, "mode": "demo"}`, 1},
		{"DB_PASSWORD: 'two words'", "DB_PASSWORD: ***", 1},
		{"token=aaa secret=bbb mode=ccc", "token=*** secret=*** mode=ccc", 2},
		{"GET /v1/items Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.e30.abc", "GET /v1/items Authorization: Bearer ***", 1},
		{"using postgres://app:hunter2@db:5432/app", "using postgres://app:***@db:5432/app", 1},
		{"minted dh_0123456789abcdefghijABCDEFGHIJ0123456789abc for ci", "minted dh_*** for ci", 1},
		{"the keyboard and the passive monkey", "the keyboard and the passive monkey", 0},
		{"fetching https://example.org/a?page=2", "fetching https://example.org/a?page=2", 0},
		{"ACCESS_TOKEN=abc&next=1", "ACCESS_TOKEN=***&next=1", 1},
		// a variable to be filled in is not what fills it in, and only a fallback written into one is hidden
		{"      DB_PASSWORD: ${DB_PASSWORD}", "      DB_PASSWORD: ${DB_PASSWORD}", 0},
		{`API_KEY="$API_KEY" TOKEN=${TOKEN?must be set}`, `API_KEY="$API_KEY" TOKEN=${TOKEN?must be set}`, 0},
		{"      API_KEY: ${API_KEY:-changeme}", "      API_KEY: ${API_KEY:-***}", 1},
		{"      DATABASE_URL: postgres://app:${DB_PASSWORD}@db:5432/app", "      DATABASE_URL: postgres://app:${DB_PASSWORD}@db:5432/app", 0},
		{"      DATABASE_URL: postgres://app:${DB_PASSWORD:-hunter2}@db/app", "      DATABASE_URL: postgres://app:***@db/app", 1},
		{"SECRET_KEY: $notavariable!", "SECRET_KEY: ***", 1},
	} {
		got, hidden := maskText(tc.in)
		if got != tc.want || hidden != tc.hidden {
			t.Errorf("maskText(%q)\n got %q, %d\nwant %q, %d", tc.in, got, hidden, tc.want, tc.hidden)
		}
	}
}
