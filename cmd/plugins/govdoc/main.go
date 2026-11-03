// Command govdoc is the downloadable OpenTools 公文格式规范化 plugin.
package main

import (
	"opentools/pkg/tool/pluginproto"
	"opentools/tools/govdoc"
)

func main() {
	pluginproto.Serve(govdoc.New())
}
