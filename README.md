# OpenTools

OpenTools 是一个用 Go 编写的桌面工具框架和插件管理器。界面使用 Vue 3，通过 MyGo WebView 渲染；桌面应用和 CLI 共享同一套应用服务。

桌面窗口使用自绘标题栏，包含最小化、最大化/还原和关闭按钮。主程序版本号显示在应用名旁边；每个工具在自己的标题栏中带有独立版本号。

核心不内置任何业务工具。下载安装的工具是独立的可执行文件，宿主从插件目录发现它们；安装或卸载工具不需要重新编译宿主。

## 架构

```text
MyGo / CLI
    -> internal/application      用例层，不依赖 GUI 或数据库包
        -> internal/catalog      已安装工具注册表
        -> internal/plugins      工具商店与 JSON-RPC 进程适配器
        -> internal/tasks        后台任务、取消、事件流
            -> tasks.Store       持久化接口
                -> SQLite
    -> pkg/tool                  仅依赖标准库的工具契约
        -> pkg/tool/pluginproto  带版本号的 JSON-RPC 2.0 进程协议
    -> tools/*                   独立编译的插件二进制源码
```

关键边界：

- `pkg/tool` 永远不导入 Vue、MyGo、SQLite 或应用状态。
- `internal/appkit` 不导入任何业务工具包，只扫描插件目录并注册轻量进程适配器。
- `internal/application` 由桌面端和 CLI 共享。
- `internal/desktop` 是唯一知道 MyGo 的包。
- `internal/tasks` 负责任务生命周期与取消；工具只上报进度并观察 `context.Context`。
- 任务比窗口活得久。只有当进程停止刷新持久心跳时任务才被标记为"已中断"，因此 GUI 和 CLI 实例可以安全共存。
- 工具结果是结构化区块加上任务工作目录下的文件；宿主在暴露产物路径前会做校验。
- 插件是独立的可执行文件。宿主通过 stdin/stdout 上的行分隔 JSON-RPC 2.0 与其通信，插件无法导入宿主内部实现，也无法替换宿主的 Go 运行时。

每个已安装工具的目录结构：

```text
data\plugins\<tool-id>\
    manifest.json
    <工具可执行文件>
```

安装包是 ZIP 文件，根目录包含同样的两个文件。宿主会校验清单版本、协议版本、平台、工具 ID、可执行文件路径、压缩包路径和 SHA256，全部通过后原子化启用新包。下载或校验失败不会影响已安装的旧版本。

## 插件协议

以 `describe` 参数启动可执行文件会打印工具定义；以 `rpc` 参数启动则在标准输入输出上提供 JSON-RPC 服务。宿主先发送 `initialize`，再发送 `tool/run`。插件可以在最终结果之前发送 `tool/log` 和 `tool/progress` 通知。取消任务会终止子进程，因此工具不会在取消后留下孤儿进程。

## 开发一个可下载工具

1. 创建 `tools/<name>/tool.go` 并实现 `tool.Tool` 接口。
2. 使用稳定的工具 ID，并声明所有对外可见的能力（effects）。
3. 在执行耗时或有副作用的操作前后检查 `ctx`。
4. 返回非空摘要，且只使用支持的结果区块类型。
5. 在 `cmd/plugins/<name>` 下添加 `pluginproto.Serve(tool)` 可执行入口。
6. 发布包含 `manifest.json` 和可执行文件的 ZIP，然后将其 SHA256 和相对下载地址写入 `store/catalog.json`。

`build.ps1` 会构建全部插件并在输出目录生成完整的本地商店。该目录可以由任意静态 HTTP 服务器托管，开发时也可以直接使用。

输入 Schema 有意只支持 JSON Schema 的一个小子集：对象、字符串、数字、整数、布尔、数组、默认值、枚举、取值范围和 UI 提示。这保证了表单生成和 CLI 解析的确定性；将来如果扩展 Schema，工具也可以自行解码更复杂的值。

## 开发

环境要求：Go 1.27+、Node.js 22+，以及 MyGo 所需的平台依赖。

```powershell
npm install
npm run generate
npm run typecheck
npm run build:web
go test ./...
go vet ./...
go tool mygo build -platform windows/amd64
```

使用 `go tool mygo dev` 或编译出的可执行文件运行桌面应用。

发布构建请使用 PowerShell 7（构建脚本本身是 `pwsh`）。构建出的应用在系统自带的 Windows PowerShell 5.1 上即可运行，不要求安装 PowerShell 7。

```powershell
./build.ps1
```

脚本会执行检查、编译核心与全部插件、生成插件 ZIP 和 `store/catalog.json`，然后把核心程序和安装包复制到 `release/<platform>-<version>/` 下带时间戳的新目录。`BUILD.txt` 记录平台、构建时间和可执行文件 SHA256。迭代时可用 `-SkipChecks` 跳过检查，用 `-Platform` 选择其他目标平台。

核心 `OpenTools.exe` 不包含业务工具。参考插件位于 `store/packages/`，按需下载安装。

## CLI

```powershell
go run ./cmd/opentools list
go run ./cmd/opentools store --refresh
go run ./cmd/opentools install system.inventory
go run ./cmd/opentools run system.inventory --json
go run ./cmd/opentools uninstall system.inventory
go run ./cmd/opentools jobs --limit 10
go run ./cmd/opentools job <job-id>
```

数据目录默认是 exe 旁边的 `data` 文件夹（便携模式）；该目录不可写时回退到用户配置目录。设置 `OPENTOOLS_DATA_DIR` 可指定任意便携或测试目录。CLI 输出对脚本友好，加 `--json` 可打印最终任务快照。

## 网络与 DNS

可下载插件 `network.dns-switcher` 管理 Windows 主网卡的 IPv4 与 DNS 设置。它通过系统自带的 Windows PowerShell 5.1 运行，支持：

- 识别主网卡，排除虚拟网卡、断开链路和 APIPA 地址；
- 按物理链路、可用 IPv4、默认路由、Internet 连通性和路由/接口跃点排序候选网卡；优先级相同的歧义候选会被报告而不是被静默选择；
- 备份 DHCP 模式、IPv4 地址与前缀、默认网关、自动/显式接口跃点、默认路由跃点以及 DNS 模式和地址；
- 通过直连 UDP 查询测速 9 个公共 DNS 服务，按成功数、中位 RTT 和抖动排序；
- 切换 DNS 前自动做一次全新备份并弹出确认提示；
- 恢复备份前核对网卡 GUID 和 MAC 地址，操作后验证并在失败时自动回滚。

备份使用 Schema 版本 2，存放在数据目录的 `network\backups` 下；旧版版本 1 备份仍可读取。切换 DNS 或恢复备份会在单独的隐藏 Windows PowerShell 5.1 进程中请求 UAC 授权。恢复范围仅限所选物理网卡的 IPv4 和 DNS；IPv6、Wi-Fi 凭据、代理设置和 Windows 防火墙规则不会被修改。

## 系统配置

`system.inventory` 工具是只读的本机配置检测。报告内容包括计算机与主板、固件与启动模式、CPU、内存条、活动显示适配器、物理磁盘与卷、已连接的网络适配器、操作系统运行时长、电池，以及安全状态（安全启动、TPM、Defender、防火墙、BitLocker 和硬件虚拟化）。

Windows 数据采集使用系统自带的 PowerShell 5.1，不依赖 PowerShell 7。该工具拥有自己的平台适配器，不与网络工具共享运行时代码，因此任何一个工具都可以独立做成下载插件而不影响另一个。

## 打印机检测

`hardware.printers` 是只读的打印机与打印驱动检查工具。它分别采集 Windows 打印后台处理程序、打印管理接口、WMI、用户打印机注册表、打印驱动注册表、打印端口和 PnP 设备，因此某个接口不可用时不会连带隐藏其他信息。即使 Spooler 已经停止，工具也会尽量从用户注册表列出已配置的打印机和默认打印机。

检查内容包括：

- 打印服务 Spooler 的运行状态和启动类型；
- 打印机名称、默认打印机、共享名、位置、离线状态与驱动名称；
- 驱动是否在系统中注册，以及可读取时驱动文件是否仍然存在；
- 打印端口类型、主机地址、端口号和协议；
- 打印机的 PnP 硬件状态；
- 网络打印机 TCP 端口的短超时可达性检查。

工具不会启动或停止打印服务，不会安装、更新或删除驱动，也不会修改任何打印机设置。网络探测只连接打印机的标准端口，超时固定且不会进行端口扫描。停服或权限限制导致无法验证的驱动会显示为“无法验证”，不会误报成“驱动缺失”。

## 工具商店来源

宿主按以下顺序确定目录来源：

1. 用户在**设置**中保存的地址（`data\plugin-sources.json`，多个地址用英文分号分隔，支持 HTTP、HTTPS 和本地路径）；
2. `OPENTOOLS_CATALOG_URL` 环境变量；
3. exe 旁边的 `store\catalog.json`（离线便携模式）；
4. 线上默认源 `https://gitee.com/dogge2333/opentools-store/raw/master/catalog.json`。

设置面板在 URL 输入框下方固定展示 Gitee 源，点**使用**即可把输入框切换成该地址并立即保存刷新，无需手工粘贴。

所有来源都不可达时，已安装的工具照常可用；商店列表会带上警告但不影响本地工具运行。

## 发布工具到 Gitee 商店

线上商店仓库为 [dogge2333/opentools-store](https://gitee.com/dogge2333/opentools-store)，目录结构：

```text
catalog.json
packages\network-config-1.2.0.zip
packages\lan-inventory-1.3.0.zip
packages\system-inventory-1.3.0.zip
packages\printer-check-1.0.0.zip
```

`catalog.json` 里的 `downloadUrl` 用**相对路径**，宿主会相对目录地址解析；`sha256` 由发布脚本自动计算。

### 升级某个工具

只需要改插件源码里的版本常量，然后重新发布：

1. 修改 `tools\<name>\tool.go` 中的 `const version = "x.y.z"`；
2. 运行发布脚本：

```powershell
$env:OPENTOOLS_GITEE_TOKEN = "<你的 Gitee token>"
.\publish-store.ps1
```

脚本会依次完成：编译全部插件 → 用 `describe` 读取新版本号生成 `manifest.json` → 打 ZIP → 重算 SHA256 → 生成 `catalog.json` → 上传到 Gitee（新文件用 POST，已存在的文件自动取 blob SHA 后 PUT 覆盖）。

老版本 ZIP 不会被删除，用户端只要刷新商店就会看到「更新」按钮（宿主按版本号比较，`catalog.json` 里只保留每个工具的最新条目）。

### 新增工具

1. 在 `tools\<name>` 实现 `tool.Tool`，在 `cmd\plugins\<name>` 加入 `pluginproto.Serve(...)` 入口；
2. 在 `publish-store.ps1` 的 `$toolSpecs` 里加一行（`Name` + `Executable`）；
3. 跑 `publish-store.ps1`。

### 本地预览

不想上传时加 `-SkipUpload`，脚本只在仓库根目录生成 `store\`，可以直接用 `build.ps1` 或任意静态服务器验证。加 `-SkipBuild` 可以复用上一次编译出的 `build\plugins` 可执行文件。

发布前建议先做一次离线验证：

```powershell
.\publish-store.ps1 -SkipUpload
go run ./cmd/opentools store --refresh
go run ./cmd/opentools install <tool-id>
```

### 校验发布结果

```powershell
curl.exe -L "https://gitee.com/dogge2333/opentools-store/raw/master/catalog.json"
```

注意 Gitee 的 raw 地址会 302 跳转到 `raw.giteeusercontent.com`，必须跟随重定向；宿主的 HTTP 客户端默认跟随，直接用浏览器或 `curl` 时记得加 `-L`。
