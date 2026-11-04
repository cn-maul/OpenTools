// Command startupmgr is the downloadable OpenTools startup manager plugin.
package main

import (
	"opentools/pkg/tool/pluginproto"
	"opentools/tools/startupmgr"
)

func main() {
	pluginproto.Serve(startupmgr.New())
}
