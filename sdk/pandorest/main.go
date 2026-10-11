// Command pandorest generates sdk/dockhand from the Dockhand API document, with github.com/katbyte/pandorest: the document is imported into
// definitions under api-defs, fixing what it gets wrong on the way (see workarounds), and the client is written from those. The document is
// Dockhand's own and is not kept in this repository: api-defs/get.sh fetches the one pinned in api-defs/dockhand-openapi.pin, and make does that
// before anything here reads it.
//
// It is run from the repository root, as the make targets do:
//
//	go run ./sdk/pandorest import     # the document -> api-defs/dockhand-<version>/
//	go run ./sdk/pandorest generate   # the definitions -> sdk/dockhand
//	go run ./sdk/pandorest diff       # what a refreshed document changes
//	go run ./sdk/pandorest check      # the definitions match the document, and every operation has a method
package main

import (
	"github.com/katbyte/dockhand-mcp/sdk/pandorest/workarounds"
	"github.com/katbyte/pandorest"
)

func main() {
	pandorest.Main(config())
}

// config is what dockhand-mcp generates its client from.
func config() pandorest.Config {
	return pandorest.Config{Services: workarounds.Services, Workarounds: workarounds.All}
}
