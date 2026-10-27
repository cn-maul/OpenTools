// Command systemconfig is the downloadable OpenTools system inventory plugin.
package main

import (
	"opentools/pkg/tool/pluginproto"
	"opentools/tools/systemconfig"
)

func main() {
	pluginproto.Serve(systemconfig.New())
}
