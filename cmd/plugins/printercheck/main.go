// Command printercheck is the downloadable OpenTools printer inspection plugin.
package main

import (
	"opentools/pkg/tool/pluginproto"
	"opentools/tools/printercheck"
)

func main() {
	pluginproto.Serve(printercheck.New())
}
