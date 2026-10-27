// Command laninventory is the downloadable OpenTools LAN address scanner.
package main

import (
	"opentools/pkg/tool/pluginproto"
	"opentools/tools/laninventory"
)

func main() {
	pluginproto.Serve(laninventory.New())
}
