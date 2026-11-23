// Package serve is the entry point for downloadable tool executables.
//
// A tool's main function calls Serve with its implementation; everything
// else (the describe/rpc command-line contract, the JSON-RPC session,
// capability grants and cancellation) is handled here. Tools never need the
// lower-level protocol package directly.
package serve

import (
	"github.com/cn-maul/OpenTools/sdk/tool"
	"github.com/cn-maul/OpenTools/sdk/tool/pluginproto"
)

// Serve runs the command-line contract for a downloadable tool executable.
//
// Supported modes:
//   - describe: print the tool definition as JSON
//   - rpc: serve line-delimited JSON-RPC 2.0 requests on stdio
func Serve(implementation tool.Tool) {
	pluginproto.Serve(implementation)
}

// ServeWithHost runs the command-line contract with host services available
// to the tool. A nil client keeps the tool self-contained: it can still
// describe itself and run, but every host call fails.
//
// Only embedders that bring their own host client need this; ordinary
// plugins must use Serve so the host's stdio service routing stays intact.
func ServeWithHost(implementation tool.Tool, services pluginproto.HostClient) {
	pluginproto.ServeWithHost(implementation, services)
}
