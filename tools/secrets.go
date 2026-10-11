package tools

import (
	"regexp"
	"strconv"
	"strings"
)

// What a container is given to run with, and what it writes to its log, is where its secrets are: a database's password in its environment, a token
// in a line it logged at start. A tool that reads either hides them unless it is told not to, because what a tool answers is kept in a conversation
// long after it was asked for.

// masked stands where a secret was.
const masked = "***"

// revealArg is the argument a tool that hides secrets takes to show them.
type revealArg struct {
	RevealSecrets bool `json:"reveal_secrets,omitempty" jsonschema:"show the values that are hidden unless this is set: passwords, tokens and keys, wherever they are told by their names. Ask for it only when the value itself is what is needed"`
}

// secretName tells a name that holds a secret: one with password, secret, token or credential anywhere in it, or with pass, pwd, key or auth as a
// word of its own, so that API_KEY and DB_PASS are and KEYBOARD and PASSIVE are not.
var secretName = regexp.MustCompile(`(?i)(password|passwd|passphrase|secret|token|credential|apikey|privatekey|(^|[^a-z0-9])(pass|pwd|key|auth)([^a-z0-9]|$))`)

// urlPassword finds the password in an address that carries one, scheme://user:password@host.
var urlPassword = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^\s:/@]+):([^\s@/]+)@`)

// isSecretName reports whether a name is one whose value is hidden.
func isSecretName(name string) bool {
	return secretName.MatchString(name)
}

// maskValue is a value as it is shown for its name: hidden whole when the name is a secret's, and otherwise with any password in an address it holds
// hidden. It reports whether anything was hidden.
func maskValue(name, value string) (string, bool) {
	if value == "" {
		return value, false
	}
	if isSecretName(name) {
		return masked, true
	}
	if urlPassword.MatchString(value) {
		return urlPassword.ReplaceAllString(value, "${1}:"+masked+"@"), true
	}

	return value, false
}

// The secrets a line of text can hold, each with what is kept of it: a name and its value, where the name is a secret's; a bearer token; a token of
// Dockhand's own, by its shape.
var (
	assignment  = regexp.MustCompile(`(?i)([a-z0-9_.-]*(?:password|passwd|passphrase|secret|token|credential|api[_-]?key|private[_-]?key|authorization)[a-z0-9_.-]*)("?\s*[=:]\s*)("[^"]*"|'[^']*'|[^\s,;&]+)`)
	bearer      = regexp.MustCompile(`(?i)\b(bearer)\s+[a-z0-9._~+/=-]{8,}`)
	dockhandKey = regexp.MustCompile(`\bdh_[A-Za-z0-9_-]{20,}`)
)

// reference tells a value that is a variable to be filled in rather than a value, as a compose file writes one: $NAME, ${NAME}, or ${NAME:-fallback}
// with the fallback it carries. What is kept apart is all of it but the fallback.
var reference = regexp.MustCompile(`^(["']?\$\{?[A-Za-z_][A-Za-z0-9_]*)(?:(:?[-+?])([^}]*))?(\}?["']?)$`)

// maskText hides the secrets in free text, a line of a log say, and reports how many it hid. What is hidden is told by what is beside it - a name
// that is a secret's, the word Bearer, an address with a password in it - so a secret that is written with nothing to say it is one is not found. A
// variable to be filled in is no secret, whatever it is named: the name of a password is not the password.
func maskText(text string) (shown string, hidden int) {
	replace := func(re *regexp.Regexp, with string) {
		text = re.ReplaceAllStringFunc(text, func(found string) string {
			out := re.ReplaceAllString(found, with)
			if out != found {
				hidden++
			}

			return out
		})
	}

	text = urlPassword.ReplaceAllStringFunc(text, func(found string) string {
		parts := urlPassword.FindStringSubmatch(found)
		if m := reference.FindStringSubmatch(parts[2]); len(m) > 3 && m[3] == "" {
			return found
		}
		hidden++

		return parts[1] + ":" + masked + "@"
	})
	replace(bearer, "${1} "+masked)
	replace(dockhandKey, "dh_"+masked)
	// an assignment whose value is already hidden is one of the above, and is not counted twice
	text = assignment.ReplaceAllStringFunc(text, func(found string) string {
		parts := assignment.FindStringSubmatch(found)
		if strings.Contains(parts[3], masked) || strings.EqualFold(parts[3], "bearer") {
			return found
		}
		if m := reference.FindStringSubmatch(parts[3]); m != nil {
			// only a fallback written into the reference can be a secret: what follows a ? is what to say when the variable is not set
			if m[3] == "" || strings.HasSuffix(m[2], "?") {
				return found
			}
			hidden++

			return parts[1] + parts[2] + m[1] + m[2] + masked + m[4]
		}
		hidden++

		return parts[1] + parts[2] + masked
	})

	return text, hidden
}

// hiddenNote says how many secrets a tool hid, how they were told for what they are, and how to see them.
func hiddenNote(hidden int, toldBy string) string {
	if hidden == 1 {
		return "1 secret is hidden, " + toldBy + ": reveal_secrets shows it"
	}

	return strconv.Itoa(hidden) + " secrets are hidden, " + toldBy + ": reveal_secrets shows them"
}
