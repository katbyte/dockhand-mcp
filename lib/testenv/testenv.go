// Package testenv is what the two live suites share: the Dockhand and the Docker hosts .scripts/testenv.sh started for them, and which of the two
// ways a Dockhand is run they stand for - open, as it comes, with no login, or with its login switched on and asked with an API token. The machinery
// for reading the environment is go-kt's (test/env).
//
// The SDK suite (../../acceptance/dockhand) drives the client in sdk/dockhand and the tool suite (../../acceptance/tools) drives the tools over MCP.
// Each runs twice on the same server, in order: open, and then with the login on - which this package switches on, through the client, as part of
// opening the second: the first user, the login, and a token for that user.
//
// Opening a world also registers the two hosts as environments, through the client, each the way Dockhand reaches it: alpha by its socket and beta by
// the Hawser agent in front of it.
//
// It is also where something is started on a host from outside Dockhand (Run, Compose): what Dockhand did not make is half of what it manages, and no
// API of Dockhand's makes it.
package testenv

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http/cookiejar"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/go-kt/test/env"
	"github.com/katbyte/pandorest/client"
)

// Stage is one of the ways the Dockhand under test is run.
type Stage string

const (
	// StageOpen is a Dockhand as it comes: no login, and nothing sent to say who is asking.
	StageOpen Stage = "open"
	// StageLogin is a Dockhand with its login switched on, asked with an API token.
	StageLogin Stage = "login"
)

const (
	// Alpha and Beta are the two Docker hosts .scripts/testenv.sh starts, by the names the suites register them under: alpha is reached by its
	// socket, and beta by the Hawser agent in front of it.
	Alpha = "alpha"
	Beta  = "beta"

	// NotConfigured is why a live test skips when the environment names no server.
	NotConfigured = `DOCKHAND_SERVER is not set: run "make testacc", or eval "$(.scripts/testenv.sh up)" first`

	// mintedAs is what the token a run mints is called on the server
	mintedAs = "dockhand-mcp-test"
	// execPatience is how long something started on a host is given
	execPatience = 2 * time.Minute
)

// The ways Dockhand reaches a host, as it names them.
const (
	ConnectionSocket = "socket"
	ConnectionDirect = "direct"
	ConnectionAgent  = "hawser-standard"
	ConnectionCaller = "hawser-edge"
)

// environment is what .scripts/testenv.sh writes, every variable of which begins DOCKHAND_.
var environment = env.New("DOCKHAND")

// Configured reports whether the environment names a server and the hosts it is to manage. It names no token: a fresh Dockhand has none to give, and
// the suites make their own.
func Configured() bool {
	return environment.Server() != "" && environment.Get("TEST_HOSTS") != ""
}

// World is the Dockhand a live suite runs against, how it is run, and the hosts it manages.
type World struct {
	Stage Stage
	// Server is the address the Dockhand answers on
	Server string
	// Token is the API token this run minted, "" in the open stage
	Token string
	// Dock is a client for the server as this stage asks it: with nothing, or with the token
	Dock *dockhand.Client

	// User and Password are the administrator the login stage makes
	User     string
	Password string

	// Socket is where Dockhand finds alpha's Docker socket, Direct the address alpha's Docker also listens on, and Agent the address of the Hawser
	// agent in front of beta, with the token it was started with
	Socket     string
	Direct     string
	Agent      string
	AgentToken string
	// Image is what the suites run on the hosts
	Image string
	// Scanned is an image old enough that a scanner finds plenty in it, and Scanner the image the scanner is run from: both where a host can pull
	// them without asking Docker Hub
	Scanned string
	Scanner string
	// Registry is where the hosts and the Dockhand find the registry the suites push to, which speaks plain HTTP and asks for the administrator's
	// name and password
	Registry string
	// Vault is where the Dockhand finds the secret store the suites save as a secret provider, which speaks Vault's API, and VaultToken the token it
	// takes: made up, and good for nothing but that store
	Vault      string
	VaultToken string
	// VaultByName is the same secret store by another address, its container's own name: for showing what Dockhand does when a provider's address
	// changes
	VaultByName string
	// Git is where the Dockhand finds the suites' git server, which speaks plain HTTP: a repository is at /open/<name>.git for anyone and at
	// /private/<name>.git for GitUser and GitPassword, a login made up for that server alone
	Git         string
	GitUser     string
	GitPassword string
	// Backups is where the Dockhand and the hosts find the server the suites keep backups on, as restic names a repository there: a suite adds a
	// name of its own to the end. It is by the server's address and not its name, since a backup is run by a container on a host, which finds no
	// name of the network the hosts share
	Backups string

	// Environments are the numbers Dockhand gave alpha and beta
	Environments map[string]dockhand.EnvironmentID

	hosts map[string]string
	// container is the container the Dockhand itself runs in, whose name leads the name of everything started beside it
	container string
	// network is the network the Dockhand and the hosts share, and agentImage what a Hawser agent is run from
	network    string
	agentImage string
}

// Open finds the Dockhand under test, runs it the way the stage asks - which for the login stage is switching its login on and minting a token - and
// registers the two hosts as environments.
func Open(ctx context.Context) (*World, error) {
	w, err := OpenBare(ctx)
	if err != nil {
		return nil, err
	}
	if err := w.register(ctx); err != nil {
		return nil, err
	}

	return w, nil
}

// OpenBare is Open without the hosts: the Dockhand is run the way the stage asks and is left managing whatever it already manages, which on a fresh
// server is nothing. It is for a suite that adds the hosts itself, through what it tests, and then says which it added (Manages).
func OpenBare(ctx context.Context) (*World, error) {
	w := &World{
		Stage:        Stage(environment.Get("TEST_STAGE")),
		Server:       environment.Server(),
		User:         environment.Get("TEST_USER"),
		Password:     environment.Get("TEST_PASSWORD"),
		Socket:       environment.Get("TEST_SOCKET"),
		Direct:       environment.Get("TEST_DIRECT"),
		Agent:        environment.Get("TEST_AGENT"),
		AgentToken:   environment.Get("TEST_AGENT_TOKEN"),
		Image:        environment.Get("TEST_FIXTURE_IMAGE"),
		Scanned:      environment.Get("TEST_SCANNED_IMAGE"),
		Scanner:      environment.Get("TEST_SCANNER_IMAGE"),
		Registry:     environment.Get("TEST_REGISTRY"),
		Vault:        environment.Get("TEST_VAULT"),
		VaultToken:   environment.Get("TEST_VAULT_TOKEN"),
		Git:          environment.Get("TEST_GIT"),
		GitUser:      environment.Get("TEST_GIT_USER"),
		GitPassword:  environment.Get("TEST_GIT_PASSWORD"),
		Backups:      environment.Get("TEST_BACKUP"),
		Environments: map[string]dockhand.EnvironmentID{},
		hosts:        map[string]string{},
		container:    environment.Get("TEST_CONTAINER"),
		network:      environment.Get("TEST_NETWORK"),
		agentImage:   environment.Get("TEST_AGENT_IMAGE"),
	}
	if w.Stage == "" {
		w.Stage = StageOpen
	}
	if w.Vault != "" {
		w.VaultByName = "http://" + w.container + "-vault:8200"
	}
	if w.Stage != StageOpen && w.Stage != StageLogin {
		return nil, fmt.Errorf("%s is %q: the stages are %s and %s", environment.Var("TEST_STAGE"), w.Stage, StageOpen, StageLogin)
	}
	for entry := range strings.SplitSeq(environment.Get("TEST_HOSTS"), ",") {
		name, container, ok := strings.Cut(entry, "=")
		if !ok {
			return nil, fmt.Errorf("%s holds %q: each host is name=container", environment.Var("TEST_HOSTS"), entry)
		}
		w.hosts[name] = container
	}

	open, err := dockhand.NewWithoutLogin(w.Server)
	if err != nil {
		return nil, err
	}
	session, err := open.GetAuthSession(ctx)
	if err != nil {
		return nil, fmt.Errorf("the Dockhand under test could not be asked: %w", err)
	}
	loginOn := session.Answer.AuthEnabled

	switch {
	case w.Stage == StageOpen && loginOn:
		return nil, errors.New("the Dockhand under test already has its login on: the open stage comes first, on a fresh server")
	case w.Stage == StageOpen:
		w.Dock = open
	default:
		if err := w.logIn(ctx, open, loginOn); err != nil {
			return nil, err
		}
	}

	return w, nil
}

// Manages records the number Dockhand gave a host, for a suite that added the hosts itself.
func (w *World) Manages(host string, id dockhand.EnvironmentID) { w.Environments[host] = id }

// logIn switches the server's login on, when it is not on already, and mints a token for the administrator: the first user is made with no login, the
// login is switched on, and a token is made by a session, which is a user name and a password answered with a cookie.
func (w *World) logIn(ctx context.Context, open *dockhand.Client, loginOn bool) error {
	if !loginOn {
		if _, err := open.PostUsers(ctx, dockhand.PostUsersRequest{Username: w.User, Password: w.Password, DisplayName: "dockhand-mcp test"}); err != nil {
			return fmt.Errorf("making the first user: %w", err)
		}
		if _, err := open.PutAuthSettings(ctx, dockhand.PutAuthSettingsRequest{AuthEnabled: new(true)}); err != nil {
			return fmt.Errorf("switching the login on: %w", err)
		}
	}

	// a session is a cookie the server sets on login, so the client that logs in keeps cookies
	jar, err := cookiejar.New(nil)
	if err != nil {
		return err
	}
	session, err := dockhand.NewWithoutLogin(w.Server, client.WithCookies(jar))
	if err != nil {
		return err
	}
	if _, err := session.PostAuthLogin(ctx, dockhand.PostAuthLoginRequest{Username: w.User, Password: w.Password, Provider: "local"}); err != nil {
		return fmt.Errorf("logging in as %s: %w", w.User, err)
	}
	mint := dockhand.PostAuthTokensRequest{Name: mintedAs + "-" + strconv.FormatInt(time.Now().Unix(), 36), Password: w.Password}
	minted, err := session.PostAuthTokens(ctx, mint)
	// a user holds 25 tokens at most, and a server kept from run to run fills up with the ones earlier runs minted: those are revoked, and it is
	// asked once more
	if err != nil && strings.Contains(err.Error(), "Maximum of") {
		if err = revokeMinted(ctx, session); err == nil {
			minted, err = session.PostAuthTokens(ctx, mint)
		}
	}
	if err != nil {
		return fmt.Errorf("minting a token for %s: %w", w.User, err)
	}
	if minted.Answer.Token == "" {
		return errors.New("the server said it made a token and sent none")
	}

	w.Token = minted.Answer.Token
	if w.Dock, err = dockhand.Connect(w.Server, w.Token); err != nil {
		return err
	}

	return nil
}

// revokeMinted revokes the tokens earlier runs minted for the user a session is signed in as, told by their name, and no other token of that user's.
func revokeMinted(ctx context.Context, session *dockhand.Client) error {
	held, err := session.GetAuthTokens(ctx)
	if err != nil {
		return err
	}
	for i := range held.Answer {
		if !strings.HasPrefix(held.Answer[i].Name, mintedAs+"-") {
			continue
		}
		if _, err := session.DeleteAuthTokensById(ctx, held.Answer[i].Id); err != nil {
			return err
		}
	}

	return nil
}

// register makes the two hosts environments, each the way Dockhand reaches it, and leaves one that is there already as it is.
func (w *World) register(ctx context.Context) error {
	listed, err := w.Dock.GetEnvironments(ctx)
	if err != nil {
		return fmt.Errorf("listing the environments: %w", err)
	}
	for i := range listed.Answer {
		w.Environments[listed.Answer[i].Name] = listed.Answer[i].Id
	}

	agentHost, agentPort, err := HostPort(w.Agent)
	if err != nil {
		return fmt.Errorf("%s: %w", environment.Var("TEST_AGENT"), err)
	}
	for _, want := range []dockhand.PostEnvironmentsRequest{
		{Name: Alpha, ConnectionType: ConnectionSocket, SocketPath: w.Socket},
		{Name: Beta, ConnectionType: ConnectionAgent, Host: agentHost, Port: agentPort, Protocol: "http", HawserToken: w.AgentToken},
	} {
		if _, there := w.Environments[want.Name]; there {
			continue
		}
		made, err := w.Dock.PostEnvironments(ctx, want)
		if err != nil {
			return fmt.Errorf("registering %s: %w", want.Name, err)
		}
		w.Environments[want.Name] = dockhand.EnvironmentID(int(made.Answer.Id))
	}

	return nil
}

// HostPort splits an address as the script gives one, host:port.
func HostPort(address string) (host string, port int, err error) {
	host, number, err := net.SplitHostPort(address)
	if err != nil {
		return "", 0, err
	}
	if port, err = strconv.Atoi(number); err != nil {
		return "", 0, err
	}

	return host, port, nil
}

// Env is the number Dockhand gave a host.
func (w *World) Env(host string) dockhand.EnvironmentID { return w.Environments[host] }

// Docker runs a docker command on one of the hosts, inside the container the host is, and returns what it printed: how something is started on a host
// that Dockhand had no part in.
func (w *World) Docker(ctx context.Context, host string, args ...string) (string, error) {
	container, ok := w.hosts[host]
	if !ok {
		return "", fmt.Errorf("no host is named %q", host)
	}

	ctx, cancel := context.WithTimeout(ctx, execPatience)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", append([]string{"exec", container, "docker"}, args...)...) //nolint:gosec // the test's own containers and arguments
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("docker %s on %s: %w: %s", strings.Join(args, " "), host, err, strings.TrimSpace(string(out)))
	}

	return string(out), nil
}

// Secrets runs a command of the secret store's own in its container, signed in with its token, and returns what it printed: how a suite puts a secret
// there for Dockhand to load, and takes it away again, which nothing in Dockhand can do. The first argument is the command, as kv.
func (w *World) Secrets(ctx context.Context, args ...string) (string, error) {
	if w.container == "" || w.VaultToken == "" {
		return "", fmt.Errorf("%s or %s is not set, so the secret store is not known", environment.Var("TEST_CONTAINER"), environment.Var("TEST_VAULT_TOKEN"))
	}

	ctx, cancel := context.WithTimeout(ctx, execPatience)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", append([]string{"exec", "-e", "BAO_ADDR=http://127.0.0.1:8200", "-e", "BAO_TOKEN=" + w.VaultToken, w.container + "-vault", "bao"}, args...)...) //nolint:gosec // the test's own container and arguments
	out, err := cmd.CombinedOutput()
	if err != nil {
		// only the command is said, never what it was given: that is where a secret's value goes
		return string(out), fmt.Errorf("bao %s in the secret store: %w: %s", strings.Join(args[:min(2, len(args))], " "), err, strings.TrimSpace(string(out)))
	}

	return string(out), nil
}

// inGit runs a shell command inside the container the suites' git server runs in, with something to read when it is given, and returns what it
// printed.
func (w *World) inGit(ctx context.Context, script, stdin string, args ...string) (string, error) {
	if w.container == "" || w.Git == "" {
		return "", fmt.Errorf("%s or %s is not set, so the git server is not known", environment.Var("TEST_CONTAINER"), environment.Var("TEST_GIT"))
	}

	ctx, cancel := context.WithTimeout(ctx, execPatience)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", append([]string{"exec", "-i", w.container + "-git", "sh", "-c", script, "sh"}, args...)...) //nolint:gosec // the test's own container and arguments
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%q in the git server: %w: %s", script, err, strings.TrimSpace(string(out)))
	}

	return string(out), nil
}

// Commit writes files into a repository of the suites' git server and commits them to its main branch, making the repository when there is none of
// that name, and takes out the files named as gone. It answers the new commit, shortened as Dockhand shortens one, and there always is one: a
// repository outlives a stage of the suites, so the next finds the files it writes there already and commits all the same. It is how a repository
// comes to hold something for Dockhand to deploy, and how it changes under a stack already deployed, which nothing in Dockhand can do.
func (w *World) Commit(ctx context.Context, repository string, files map[string]string, gone ...string) (string, error) {
	if _, err := w.inGit(ctx, `set -e; [ -d "/srv/git/$1.git" ] || git init -q --bare "/srv/git/$1.git"; [ -d "/srv/work/$1" ] || git clone -q "/srv/git/$1.git" "/srv/work/$1" 2>/dev/null`, "", repository); err != nil {
		return "", err
	}
	for _, path := range slices.Sorted(maps.Keys(files)) {
		if _, err := w.inGit(ctx, `set -e; mkdir -p "$(dirname "/srv/work/$1/$2")"; cat > "/srv/work/$1/$2"`, files[path], repository, path); err != nil {
			return "", err
		}
	}
	for _, path := range gone {
		if _, err := w.inGit(ctx, `rm -f "/srv/work/$1/$2"`, "", repository, path); err != nil {
			return "", err
		}
	}
	commit, err := w.inGit(ctx, `set -e; cd "/srv/work/$1"; git add -A; git commit -q --allow-empty -m "the suite's change"; git push -q origin HEAD:main; git rev-parse --short HEAD`, "", repository)

	return strings.TrimSpace(commit), err
}

// Branch gives a repository of the suites' git server another branch, at the commit its main branch is at.
func (w *World) Branch(ctx context.Context, repository, branch string) error {
	_, err := w.inGit(ctx, `set -e; cd "/srv/work/$1"; git push -q origin "HEAD:refs/heads/$2"`, "", repository, branch)

	return err
}

// Repository is the address Dockhand fetches a repository of the suites' git server from: the one anyone may read, or with private the one that asks
// for the server's login.
func (w *World) Repository(name string, private bool) string {
	if private {
		return w.Git + "/private/" + name + ".git"
	}

	return w.Git + "/open/" + name + ".git"
}

// servedOn is the port a host serves a test's file on, which nothing the suites run on a host publishes.
const servedOn = "18099"

// Serve has one of the hosts serve one file over HTTP, from a small container started on it from outside Dockhand, and returns the address Dockhand
// fetches it at: how a test gives Dockhand something to fetch without either of them asking the internet. One a run that was cut short left under the
// same name is taken away first.
func (w *World) Serve(ctx context.Context, host, name, file, content string) (string, error) {
	container, ok := w.hosts[host]
	if !ok {
		return "", fmt.Errorf("no host is named %q", host)
	}
	if err := w.Unserve(ctx, host, name); err != nil {
		return "", err
	}

	// the file is handed over as a variable, which wants no quoting, and written by the container itself before it serves
	if _, err := w.Docker(ctx, host, "run", "-d", "--name", name, "-p", servedOn+":80", "-e", "SERVED="+content, w.Image, "sh", "-c", `mkdir -p /www && printf %s "$SERVED" > /www/`+file+` && exec httpd -f -p 80 -h /www`); err != nil {
		return "", err
	}

	// Dockhand fetches a source the moment it is added and keeps what it got, an answer of nothing included, so the file is waited for before anyone
	// is told of it
	ctx, cancel := context.WithTimeout(ctx, execPatience)
	defer cancel()
	for {
		fetched := exec.CommandContext(ctx, "docker", "exec", container, "wget", "-q", "-O", "/dev/null", "http://127.0.0.1:"+servedOn+"/"+file) //nolint:gosec // the test's own containers and names
		if fetched.Run() == nil {
			break
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("%s on %s did not come to serve %s in %s", name, host, file, execPatience)
		case <-time.After(200 * time.Millisecond):
		}
	}

	return "http://" + container + ":" + servedOn + "/" + file, nil
}

// Unserve takes away what Serve started on a host; one that is not there is no error.
func (w *World) Unserve(ctx context.Context, host, name string) error {
	// docker rm -f answers a container that is not there with no error
	_, err := w.Docker(ctx, host, "rm", "-f", "-v", name)

	return err
}

// Calls is what a Hawser agent that calls the Dockhand is told to call: the Dockhand by the name it has on the network the hosts share, where nothing
// outside that network hears what is said.
const Calls = "ws://dockhand:3000/api/hawser/connect" //nolint:revive // unsecure-url-scheme: the throwaway Dockhand is served without TLS, on a network of its own

// caller is the name of the container an agent that calls in for a host runs in, which .scripts/testenv.sh down takes away with the rest.
func (w *World) caller(host string) string { return w.container + "-caller-" + host }

// CallIn starts a Hawser agent before one of the hosts that calls the Dockhand with a token, as an agent on a host Dockhand cannot reach does. One a
// run that was cut short left is taken away first.
func (w *World) CallIn(ctx context.Context, host, token string) error {
	if _, ok := w.hosts[host]; !ok {
		return fmt.Errorf("no host is named %q", host)
	}
	if w.container == "" || w.network == "" || w.agentImage == "" {
		return fmt.Errorf("%s, %s and %s say where an agent is started and from what, and one of them is not set", environment.Var("TEST_CONTAINER"), environment.Var("TEST_NETWORK"), environment.Var("TEST_AGENT_IMAGE"))
	}
	if err := w.HangUp(ctx, host); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, execPatience)
	defer cancel()

	// the host keeps its socket on a volume, which is how the agent before it is handed it
	run := exec.CommandContext(ctx, "docker", "run", "-d", "--name", w.caller(host), "--hostname", "caller-"+host, "--network", w.network, "-e", "DOCKHAND_SERVER_URL="+Calls, "-e", "TOKEN="+token, "-e", "AGENT_NAME=caller-"+host, "-v", w.container+"-run-"+host+":/var/run", w.agentImage) //nolint:gosec // the test's own containers, names and token
	if out, err := run.CombinedOutput(); err != nil {
		return fmt.Errorf("starting an agent that calls in for %s: %w: %s", host, err, strings.TrimSpace(string(out)))
	}

	return nil
}

// Freeze holds the agent CallIn started for a host still, so that it neither answers nor calls again. An agent whose token stops working calls again
// with it a second later, is turned away, and Dockhand then turns away every call from the agent's address for five minutes, the next agent's with
// the right token among them, since a container that is removed leaves its address to the next. So a test that ends an agent's token holds the agent
// still first, and takes it away after.
func (w *World) Freeze(ctx context.Context, host string) error {
	ctx, cancel := context.WithTimeout(ctx, execPatience)
	defer cancel()

	if out, err := exec.CommandContext(ctx, "docker", "pause", w.caller(host)).CombinedOutput(); err != nil { //nolint:gosec // the test's own container
		return fmt.Errorf("holding still the agent that calls in for %s: %w: %s", host, err, strings.TrimSpace(string(out)))
	}

	return nil
}

// HangUp takes away the agent CallIn started for a host; one that is not there is no error.
func (w *World) HangUp(ctx context.Context, host string) error {
	if w.container == "" {
		return fmt.Errorf("%s is not set, so the agent's container is not known", environment.Var("TEST_CONTAINER"))
	}

	ctx, cancel := context.WithTimeout(ctx, execPatience)
	defer cancel()

	// docker rm -f answers a container that is not there with no error
	if out, err := exec.CommandContext(ctx, "docker", "rm", "-f", "-v", w.caller(host)).CombinedOutput(); err != nil { //nolint:gosec // the test's own container
		return fmt.Errorf("taking away the agent that calls in for %s: %w: %s", host, err, strings.TrimSpace(string(out)))
	}

	return nil
}

// Compose starts a compose project on a host from outside Dockhand, as a person at the host would: its file is written on the host and brought up.
func (w *World) Compose(ctx context.Context, host, project, file string) error {
	container, ok := w.hosts[host]
	if !ok {
		return fmt.Errorf("no host is named %q", host)
	}

	ctx, cancel := context.WithTimeout(ctx, execPatience)
	defer cancel()

	dir := "/stacks/" + project
	write := exec.CommandContext(ctx, "docker", "exec", "-i", container, "sh", "-c", "mkdir -p "+dir+" && cat > "+dir+"/compose.yaml") //nolint:gosec // the test's own containers and names
	write.Stdin = strings.NewReader(file)
	if out, err := write.CombinedOutput(); err != nil {
		return fmt.Errorf("writing %s's compose file on %s: %w: %s", project, host, err, strings.TrimSpace(string(out)))
	}

	up := exec.CommandContext(ctx, "docker", "exec", container, "docker", "compose", "--project-directory", dir, "up", "-d", "--quiet-pull") //nolint:gosec // the test's own containers and names
	if out, err := up.CombinedOutput(); err != nil {
		return fmt.Errorf("bringing %s up on %s: %w: %s", project, host, err, strings.TrimSpace(string(out)))
	}

	return nil
}

// inDockhand runs a shell command inside the container the Dockhand itself runs in, with something to read when it is given, and returns what it
// printed.
func (w *World) inDockhand(ctx context.Context, script, stdin string) (string, error) {
	if w.container == "" {
		return "", fmt.Errorf("%s is not set, so the Dockhand's own container is not known", environment.Var("TEST_CONTAINER"))
	}

	ctx, cancel := context.WithTimeout(ctx, execPatience)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", w.container, "sh", "-c", script) //nolint:gosec // the test's own container and paths
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%q in the Dockhand's own container: %w: %s", script, err, strings.TrimSpace(string(out)))
	}

	return string(out), nil
}

// Lay writes a file inside the Dockhand's own container, as a folder mounted into it would hold one: where Dockhand looks for a compose file it is
// shown rather than given.
func (w *World) Lay(ctx context.Context, path, content string) error {
	dir := path[:strings.LastIndex(path, "/")+1]
	_, err := w.inDockhand(ctx, "mkdir -p '"+dir+"' && cat > '"+path+"'", content)

	return err
}

// Laid reads a file inside the Dockhand's own container.
func (w *World) Laid(ctx context.Context, path string) (string, error) {
	return w.inDockhand(ctx, "cat '"+path+"'", "")
}

// Unlay removes a directory a test laid inside the Dockhand's own container.
func (w *World) Unlay(ctx context.Context, dir string) error {
	_, err := w.inDockhand(ctx, "rm -rf '"+dir+"'", "")

	return err
}

// BackupsByName is the backup server by its name on the network, which the Dockhand finds and a container on a host does not: for showing what
// becomes of a backup to somewhere only Dockhand reaches.
func (w *World) BackupsByName() string { return "rest:http://" + w.container + "-backups:8000" }

// MirrorBackupImage has the Dockhand run its backups from the mirror's copy of the helper image where a host can pull that, as the suites do with
// every image a host pulls for itself: Docker Hub turns a CI runner away for what its shared address has pulled. A mirror that does not have the
// image yet, as it may not for a day after a release, leaves the setting as it was.
func (w *World) MirrorBackupImage(ctx context.Context) error {
	const mirror = "mirror.gcr.io/"

	settings, err := w.Dock.GetSettingsGeneral(ctx)
	if err != nil {
		return fmt.Errorf("the image backups are run from could not be read: %w", err)
	}
	image := settings.Answer.DefaultBackupImage
	if image == "" || strings.HasPrefix(image, mirror) {
		return nil
	}
	for host := range w.hosts {
		if _, err := w.Docker(ctx, host, "pull", "-q", mirror+image); err != nil {
			return nil //nolint:nilerr // the mirror is behind: the image is pulled from where Dockhand names it
		}
	}
	if _, err := w.Dock.SaveGeneralSettings(ctx, map[string]any{"defaultBackupImage": mirror + image}); err != nil {
		return fmt.Errorf("the image backups are run from could not be set to the mirror's: %w", err)
	}

	return nil
}
