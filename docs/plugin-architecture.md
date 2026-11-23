# OpenTools 插件架构

本文是插件体系的契约与演进计划。代码是唯一事实来源，本文记录「为什么这样设计」
和「改协议时必须遵守的规则」。

## 分层

```
cmd/opentools            桌面壳 + CLI，只做组装
internal/desktop         原生界面：主壳、工具表单、结果区块、商店、设置、主题；
                         也是唯一导入 MyGo 的包
internal/appkit          与 GUI 无关的组装：SQLite → 插件 → 任务 → 应用服务
internal/application     用例层，桌面与 CLI 共用
internal/plugins         已安装插件发现、安装/升级/卸载、进程适配
internal/tasks           后台任务、事件流、取消、产物
internal/hostservice     宿主服务分发：secrets / config / dialog / notify / elevate
sdk/tool                 工具契约：Definition、Schema、Result、Environment
sdk/tool/pluginproto     进程协议：双向 JSON-RPC 2.0、能力协商、取消
sdk/tool/host            工具侧宿主服务的类型化封装
sdk/registry             商店分发包与目录快照（仅宿主/发布工具使用）
```

主仓库只有框架，不含任何业务工具。工具在 `cn-maul/opentool-kit`，通过
`sdk/tool` 依赖契约，以独立 exe 的形式被下载、校验、安装、运行。

### 框架与工具的兼容边界

`sdk/` 是独立的 Go 模块（`github.com/cn-maul/OpenTools/sdk`），主框架用
`replace` 指向本地目录，工具仓库则依赖它的版本号。这条边界是硬约束：

- 修改 `internal/*`、桌面渲染、设置页、商店、任务调度，**不会**要求工具改代码；
- 修改 `sdk/tool` 的导出字段、删除字段或改变语义，才需要升
  `pluginproto.ContractVersion`，并让工具按需升级；
- 只给 `sdk/tool` 新增可选字段或新增能力常量不升契约版本，已有工具继续运行；
  但 `ContractVersion` 一旦上调，低于 `MinimumContractVersion` 的工具会被直接
  拒绝，当前最低为 3，不接受契约 2；
- 工具不导入 `internal/*`、`sdk/registry`，也不依赖 `sdk/tool/pluginproto`
  之外的框架包。

发布新框架版本时，工具二进制不需要重编译；只有主动提高
`MinimumContractVersion` 才要求工具重建发布。当前最低契约是 3，因此契约 2
的旧二进制必须先重装新版本才能运行。

## 进程协议

插件是独立可执行文件，宿主以 `rpc` 参数启动它，双方在 stdio 上使用行分隔的
JSON-RPC 2.0。这是**双向**连接：宿主可以调用工具（`initialize`、`tool/run`），
工具也可以调用宿主（`host/*`）。

```
宿主                                   插件
 │  initialize(protocol, contract,      │
 │             workDir, dataDir,        │
 │             grantedCapabilities,     │
 │             limits)  ───────────────▶│
 │◀──────────── InitializeResult ───────│
 │  tool/run(input)  ──────────────────▶│
 │◀──── tool/section / tool/progress ───│   通知（无 id）
 │◀──── host/secrets/get ───────────────│   工具发起的请求
 │──────────── {value} ────────────────▶│
 │  $/cancel  ─────────────────────────▶│   通知，工具自行取消 ctx
 │◀──────────── result ─────────────────│
```

### 版本

- `ProtocolVersion`：进程协议的线格式。当前 2，宿主兼容 1–2。
- `ContractVersion`：字段与语义的契约版本，当前 3。**只增字段不改语义**；新增服务、
  新增可选字段都不升版本，只有字段改语义或被删除时才升。3 收紧了
  `env.DataDir` 的含义（从共享数据根目录变为每工具私有目录），并删除了
  未被宿主消费的结果字段。
- `MinimumContractVersion`：当前 3。工具在 `initialize` 里报告低于该值的契约时，
  宿主直接拒绝启动并提示重新安装，避免旧结果块在运行到一半才报“未知类型”。

工具声明 `Definition.Requires`，宿主在 `initialize` 里回传实际授予的能力。
未声明的能力，调用会被拒绝（`CodeCapabilityDenied`），传输层和分发层各校验一次。
`Requires` 的类型是 `[]tool.Capability`，常量也定义在 `sdk/tool`；协议包只保留
同名别名，避免工具和宿主在字符串拼写上漂移。
`InitializeResult.ContractVersion` 不在
[`MinimumContractVersion`, `ContractVersion`] 区间内时，宿主拒绝启动该工具。
当前区间是 3–3：契约 2 及更旧的工具必须重新构建发布，宿主不做字段兼容。

### 兼容规则

- 未知通知：忽略并记日志，不中断会话。
- 未知请求：回 `CodeMethodNotFound`，不中断会话。
- 单条消息上限默认 16 MiB，超限即断开并报错。
- `Definition.TimeoutSeconds` 大于零时，宿主用 `context.WithTimeout` 约束一次
  运行；超时按失败结算，用户取消仍按取消结算。
- 工具结果必须通过 `tool.ValidateResult`，非法结果按协议错误返回。

### 取消

三段式：

1. 宿主发 `$/cancel`（带原因）。
2. 工具在宽限期（默认 3 秒，`Limits.CancelGraceMS`）内取消 `Run` 的 ctx。
3. 仍未退出则 `TerminateProcess`。

工具因此有机会落盘、回滚、清理半完成状态。宿主不再直接杀进程。

## 宿主服务

工具能调用的宿主能力只有这几个命名空间。新增服务不需要改进程协议。

| 方法 | 能力 | 用途 |
|---|---|---|
| `host/dialog/openFile` | `dialogs` | 原生打开文件对话框 |
| `host/dialog/saveFile` | `dialogs` | 原生保存对话框 |
| `host/dialog/pickDirectory` | `dialogs` | 原生选择文件夹 |
| `host/secrets/get` `set` `delete` | `secrets` | 每工具独立密钥库 |
| `host/config/get` `set` | `config` | 每工具独立配置 |
| `host/notify` | `notify` | 系统通知 |
| `host/elevate` | `elevate` | 宿主统一 UAC 提权 |

密钥落盘用 Windows DPAPI（绑定当前用户）；其它平台退化为仅文件权限保护，
调用方必须知道这一点。密钥**不会**进入任务快照或数据库。

`host/tools.invoke`（工具调工具）明确不做：它引入循环调用、权限传递和资源
放大，等前面的服务稳定后单独设计。

## 任务与数据

- 任务在后台 goroutine 中执行，窗口关闭不影响；任务目录 `data/jobs/<id>`。
- 输入在落库前用 `tool.Redact` 脱敏，`secret` 字段变成 `<redacted>`；
  工具执行时拿到的仍是原值。
- 产物在任务结束时校验路径、补 MIME 与大小，并把目录权限收紧到当前用户。
- 桌面端退出时清理历史与任务目录，避免密钥或内网扫描结果残留。CLI 的
  `list`/`jobs`/`store` 只读取同一份数据库，退出时**不清**历史；否则一次查询
  就会抹掉桌面端正在展示的结果。清理策略由 `appkit.Options.ClearHistoryOnClose`
  显式决定，只有桌面宿主打开它。

## 插件安装

- 安装是事务性的：先解压到 `.install-*`，再把旧目录 rename 成 `.old-*`，
  最后把新目录 rename 到位。启用失败会把旧版本 rename 回来。
- 扫描时跳过 `.install-*`、`.old-*`、`.remove-*`，它们不是可安装版本。
- 旧目录清理失败只留下无害的备份目录，后台重试删除，不阻塞安装。

## 演进阶段

| 阶段 | 内容 | 状态 |
|---|---|---|
| 0 | 止血：密钥脱敏、升级回滚、产物权限时机与大小、临时目录扫描、`list` 空结果 | 已完成 |
| 1 | 协议骨架：契约版本、限额、能力协商、未知消息容错、`$/cancel`、双向路由 | 已完成 |
| 2 | 首批宿主服务：`dialog` → `secrets` → `config` → `notify` → `elevate` | 已完成 |
| 3 | 开发体验：`toolkit` 脚手架、`examples/echo` 协议基准 | 未开始 |
| 4 | 收口：超大结果分块、`Sections` 与 `Blocks` 去重 | 未开始（历史保留策略已按宿主/CLI 拆分） |

### 阶段 2 的验收标准

每个服务必须满足：

1. 工具未在 `Definition.Requires` 里声明时，宿主返回 `CodeCapabilityDenied`。
2. 独立运行（无宿主）时，工具侧封装返回 `host.ErrNoHost`，工具能降级。
3. 至少有一个真实工具在用，而不是只有测试调用。

`elevate` 服务已实现（宿主统一 UAC）。当前随仓库发布的
`network.dns-switcher` 仍自己起隐藏 PowerShell 完成提权，迁移到 `elevate`
属于后续工作。

## 写新工具时

面向作者的完整步骤、可编译示例与自测命令在
[`plugin-development.md`](plugin-development.md)；本节只列架构约束。

1. 在 `opentool-kit/tools/<name>` 建包，`tool.go` 里给出 `Definition`。
2. 需要宿主能力就在 `Requires` 里声明，用 `sdk/tool/host` 的类型化封装调用。
3. `main.go` 调 `pluginproto.Serve(...)`。`ServeWithHost` 只用于测试或把
   工具嵌进自带宿主客户端的程序；普通插件不要传自定义 client，否则会绕过
   宿主的 stdio 服务路由。
4. `tools/plugins.json` 登记后用 `publish-store.ps1` 打包发布。
