// Command perfcheckup is the downloadable OpenTools performance plugin.
package main

import (
	"opentools/pkg/tool/pluginproto"
	"opentools/tools/perfcheckup"
)

func main() {
	pluginproto.Serve(perfcheckup.New())
}
