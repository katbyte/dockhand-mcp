//go:build integration

package acceptance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/dockhand-mcp/tools"
	"github.com/katbyte/go-kt/mcp/acctest"
	mcpregistry "github.com/katbyte/go-kt/mcp/registry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The fixtures, each named so that nothing else on a host is taken for one: a compose stack brought up at the first host, as a person there would,
// and containers started on their own.
const (
	// webStack has two services: front, which is healthy, publishes two ports, keeps a volume, is given a key and a password and logs one, and back,
	// whose health check always fails and says why
	webStack = "acc-web"
	webFront = webStack + "-front-1"
	webBack  = webStack + "-back-1"
	// loose runs on its own, crashed exited with an error and looping exits at once and is started again by Docker, over and over
	loose   = "acc-loose"
	crashed = "acc-crashed"
	looping = "acc-looping"
	// solo is the one container on the second host
	solo = "acc-solo"
)

// webCompose is the stack's compose file. Its ports are published on the host's own addresses, which nothing outside the host's container reaches.
const webCompose = `name: ` + webStack + `
services:
  front:
    image: IMAGE
    command: ["sh", "-c", "echo front up; echo connecting with password=not-a-real-password; while true; do echo tick; sleep 2; done"]
    environment:
      MODE: demo
      API_KEY: not-a-real-key
      DATABASE_URL: postgres://app:not-a-real-password@db:5432/app
    mem_limit: 64m
    restart: unless-stopped
    ports:
      - "18081:80"
      - "127.0.0.1:18082:81/udp"
    volumes:
      - data:/data
    healthcheck:
      test: ["CMD", "true"]
      interval: 2s
  back:
    image: IMAGE
    command: ["sh", "-c", "echo back up; sleep 100000"]
    healthcheck:
      test: ["CMD", "sh", "-c", "echo not ready; exit 1"]
      interval: 2s
      retries: 1
volumes:
  data:
`

var (
	// world is the Dockhand under test and how it is run
	world *testenv.World
	// suite is the one client session every test calls the tools through, and the record of what was called
	suite = &acctest.Suite{NotReady: testenv.NotConfigured}
)

// runSuite runs the Dockhand the way the environment asks, lays the fixtures out on its hosts, runs the tests, takes the fixtures away again and
// holds the run to having called every tool, returning the code to exit with.
func runSuite(m *testing.M) int {
	if !testenv.Configured() {
		return m.Run() // every test skips
	}

	ctx := context.Background()
	var err error
	if world, err = testenv.OpenBare(ctx); err == nil {
		err = start(ctx)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "acceptance setup:", err)

		return 1
	}

	code := m.Run()
	removeBinary()
	if err := removeFixtures(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "acceptance teardown:", err)
		code = 1
	}

	// every registered tool must have answered something above, not only refused it. Only a whole-suite run can say that, so a -run or -skip filter
	// skips the check.
	if acctest.WholeRun() {
		report, err := suite.CoverageReport()
		if err != nil {
			fmt.Fprintln(os.Stderr, "\n"+err.Error())
			code = 1
		}
		if report != "" {
			fmt.Fprint(os.Stderr, report)
			code = 1
		}
	}

	return code
}

// start connects the suite to an MCP server registering every tool for the Dockhand, has the Dockhand take on its two hosts through those tools, and
// lays the fixtures out on them.
func start(ctx context.Context) error {
	srv := mcp.NewServer(&mcp.Implementation{Name: "dockhand-mcp-acceptance", Version: "test"}, nil)
	if _, err := tools.RegisterAll(srv, world.Dock, tools.Options{Toolsets: []string{mcpregistry.All}, EnableWrite: true, EnableDelete: true}); err != nil {
		return err
	}
	connected, err := acctest.Connect(ctx, srv)
	if err != nil {
		return err
	}
	connected.Ready = true
	suite = connected

	if err := addHosts(); err != nil {
		return err
	}
	if err := removeFixtures(ctx); err != nil {
		return err
	}

	return layFixtures(ctx)
}

// added is what adding each host through the tools answered, by the host's name, for the journey that looks at it: nothing for a host that was there
// already, as both are on the second stage of a run.
var added = map[string]map[string]any{}

// addHosts has a Dockhand that manages nothing take on the two hosts, each the way it is reached, through the tools a person would use: the first
// journey of a fresh server, on which every other test stands. A host the Dockhand already manages is left as it is.
func addHosts() error {
	agentHost, agentPort, err := testenv.HostPort(world.Agent)
	if err != nil {
		return err
	}
	listed, err := suite.Invoke("env_list", nil)
	if err != nil {
		return err
	}
	there := named(acctest.RowsOf(listed["environments"]), "env")

	for _, host := range []struct {
		name string
		args map[string]any
	}{
		{testenv.Alpha, map[string]any{"connection": testenv.ConnectionSocket, "socket_path": world.Socket}},
		{testenv.Beta, map[string]any{"connection": testenv.ConnectionAgent, "host": agentHost, "port": agentPort, "agent_token": world.AgentToken}},
	} {
		if row, ok := there[host.name]; ok {
			world.Manages(host.name, acctest.NumOr0(row["id"]))

			continue
		}

		// tried before it is saved, as a person would, and saved only once it answers
		tried, err := suite.Invoke("env_test", host.args)
		if err != nil {
			return fmt.Errorf("trying %s: %w", host.name, err)
		}
		if !acctest.BoolOf(tried["reachable"]) {
			return fmt.Errorf("the Dockhand cannot reach %s: %v", host.name, tried["error"])
		}
		host.args["name"] = host.name
		made, err := suite.Invoke("env_create", host.args)
		if err != nil {
			return fmt.Errorf("adding %s: %w", host.name, err)
		}
		row, _ := made["environment"].(map[string]any)
		world.Manages(host.name, acctest.NumOr0(row["id"]))
		added[host.name] = made
	}

	return nil
}

// layFixtures starts what the tests read on the two hosts, from outside Dockhand: what it did not make is what it is mostly asked about.
func layFixtures(ctx context.Context) error {
	if err := world.Compose(ctx, testenv.Alpha, webStack, strings.ReplaceAll(webCompose, "IMAGE", world.Image)); err != nil {
		return err
	}
	for _, run := range []struct {
		host string
		args []string
	}{
		{testenv.Alpha, []string{"run", "-d", "--name", loose, world.Image, "sleep", "100000"}},
		{testenv.Alpha, []string{"run", "-d", "--name", looping, "--restart", "always", world.Image, "sh", "-c", "echo again; exit 1"}},
		{testenv.Beta, []string{"run", "-d", "--name", solo, world.Image, "sleep", "100000"}},
	} {
		if _, err := world.Docker(ctx, run.host, run.args...); err != nil {
			return err
		}
	}
	// a container that exits with an error is one docker run reports as failed, which is the point of it
	if out, err := world.Docker(ctx, testenv.Alpha, "run", "--name", crashed, world.Image, "sh", "-c", "echo dying; exit 3"); err == nil {
		return fmt.Errorf("%s was to exit with an error and did not: %s", crashed, out)
	}

	return nil
}

// removeFixtures takes away what layFixtures started, and what a run cut short may have left: the containers by name, and the stack with its volume.
func removeFixtures(ctx context.Context) error {
	var problems []error
	for host, names := range map[string][]string{testenv.Alpha: {loose, crashed, looping, webFront, webBack}, testenv.Beta: {solo}} {
		// removing a container that is not there is no failure of this
		if out, err := world.Docker(ctx, host, append([]string{"rm", "-f", "-v"}, names...)...); err != nil && !strings.Contains(out, "No such container") {
			problems = append(problems, err)
		}
	}
	_, _ = world.Docker(ctx, testenv.Alpha, "volume", "rm", "-f", webStack+"_data")
	_, _ = world.Docker(ctx, testenv.Alpha, "network", "rm", "-f", webStack+"_default")

	return errors.Join(problems...)
}

// started runs a container on a host for the length of a test, from outside Dockhand, and returns its name: something a journey may stop and start
// without another test reading it.
func started(t *testing.T, host, name string) string {
	t.Helper()

	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	// a run that was cut short may have left it behind
	_, _ = world.Docker(t.Context(), host, "rm", "-f", name)
	if _, err := world.Docker(t.Context(), host, "run", "-d", "--name", name, world.Image, "sh", "-c", "echo up; while true; do echo beat; sleep 1; done"); err != nil {
		t.Fatalf("starting %s on %s: %v", name, host, err)
	}
	ctx := context.WithoutCancel(t.Context())
	t.Cleanup(func() {
		if _, err := world.Docker(ctx, host, "rm", "-f", name); err != nil {
			t.Errorf("removing %s from %s: %v", name, host, err)
		}
	})

	return name
}

// call invokes a tool that is to answer.
func call(t *testing.T, tool string, args map[string]any) map[string]any {
	t.Helper()

	return suite.Call(t, tool, args)
}

// refused invokes a tool that is to refuse, and gives what it said.
func refused(t *testing.T, tool string, args map[string]any) string {
	t.Helper()

	return suite.CallErr(t, tool, args)
}

// flat is an answer as one text, to look for something anywhere in it.
func flat(answer map[string]any) string {
	return fmt.Sprint(answer)
}

// named is the rows of a list by the name each carries under a key.
func named(rows []map[string]any, key string) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, row := range rows {
		out[acctest.Str(row[key])] = row
	}

	return out
}
