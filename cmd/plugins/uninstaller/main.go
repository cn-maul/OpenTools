// Command uninstaller is the downloadable OpenTools uninstaller plugin.
package main

import (
	"opentools/pkg/tool/pluginproto"
	"opentools/tools/uninstaller"
)

func main() {
	pluginproto.Serve(uninstaller.New())
}
