// Command networkconfig is the downloadable OpenTools network/DNS plugin.
package main

import (
	"opentools/pkg/tool/pluginproto"
	"opentools/tools/networkconfig"
)

func main() {
	pluginproto.Serve(networkconfig.New())
}
