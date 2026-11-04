// Command diskhealth is the downloadable OpenTools disk health plugin.
package main

import (
	"opentools/pkg/tool/pluginproto"
	"opentools/tools/diskhealth"
)

func main() {
	pluginproto.Serve(diskhealth.New())
}
