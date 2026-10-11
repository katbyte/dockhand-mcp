package tools

import (
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
	mcpregistry "github.com/katbyte/go-kt/mcp/registry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The tools end to end against a canned Dockhand: the requests a handler makes and the answer it projects, without a container. The canned server
// holds every request against the API's definitions; the acceptance suite proves the canned answers match a server that is running.

// everything is the options that register every tool: the ones that change something, and the ones that delete.
var everything = Options{EnableWrite: true, EnableDelete: true}

// session connects an in-memory MCP client to a server registering every tool against a canned Dockhand, with the given options.
func session(t *testing.T, dock *dockhandtest.Server, opts Options) *acctest.Suite {
	t.Helper()

	srv := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "0"}, nil)
	opts.Toolsets = []string{mcpregistry.All}
	if _, err := RegisterAll(srv, dock.Client(), opts); err != nil {
		t.Fatal(err)
	}

	return connect(t, srv)
}

// unreachable is a client for a Dockhand that is never called, for the tests of which tools are registered.
func unreachable(t *testing.T) *dockhand.Client {
	t.Helper()

	dock, err := dockhand.New("http://127.0.0.1:1", dockhandtest.Token)
	if err != nil {
		t.Fatal(err)
	}

	return dock
}

// register is the names RegisterAll registers for the options.
func register(t *testing.T, opts Options) []string {
	t.Helper()

	names, err := RegisterAll(mcp.NewServer(&mcp.Implementation{Name: "t", Version: "0"}, nil), unreachable(t), opts)
	if err != nil {
		t.Fatal(err)
	}

	return names
}

// refused is what RegisterAll refuses the options with.
func refused(t *testing.T, opts Options) string {
	t.Helper()

	_, err := RegisterAll(mcp.NewServer(&mcp.Implementation{Name: "t", Version: "0"}, nil), unreachable(t), opts)
	if err == nil {
		t.Errorf("%+v was accepted", opts)

		return ""
	}

	return err.Error()
}

// hostRegistry registers the tools queued on a registry the test built by hand, for what RegisterAll does not reach: a tool of the test's own.
func hostRegistry(t *testing.T, r *registry) *acctest.Suite {
	t.Helper()

	srv := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "0"}, nil)
	if _, err := r.queued().Register(srv, mcpregistry.Selection{EnableDelete: true}); err != nil {
		t.Fatal(err)
	}

	return connect(t, srv)
}

// connect opens an in-memory MCP client on a server, as go-kt's suite: every call goes through the protocol, as a client's does.
func connect(t *testing.T, srv *mcp.Server) *acctest.Suite {
	t.Helper()

	suite, err := acctest.Connect(t.Context(), srv)
	if err != nil {
		t.Fatal(err)
	}
	suite.Ready = true
	t.Cleanup(func() { _ = suite.Session.Close() })

	return suite
}

// listed is every tool a session was given, as a client sees them.
func listed(t *testing.T, suite *acctest.Suite) []*mcp.Tool {
	t.Helper()

	res, err := suite.Session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}

	return res.Tools
}
