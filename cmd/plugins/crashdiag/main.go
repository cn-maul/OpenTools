// Command crashdiag is the downloadable OpenTools crash diagnostics plugin.
package main

import (
	"opentools/pkg/tool/pluginproto"
	"opentools/tools/crashdiag"
)

func main() {
	pluginproto.Serve(crashdiag.New())
}
