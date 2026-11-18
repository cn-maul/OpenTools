# 工具开发指南

面向要在 `opentool-kit` 里写一个新工具的人。本文所有结论都对应框架源码，
示例代码可以原样编译。

阅读顺序建议：先看「最小可用工具」跑通一次，再按需查「输入 Schema」「结果
布局」「宿主服务」；最后用「本地自测」和「发布检查清单」收尾。架构与协议
演进规则见 [`plugin-architecture.md`](plugin-architecture.md)。

## 1. 一次运行发生了什么

```
下载 ZIP → 校验 SHA256 → 解压到 .install-* → rename 到位
  ↓
宿主发现 manifest.json
  ↓
宿主启动 <tool>.exe rpc（stdin/stdout 上的 JSON-RPC 2.0）
  ↓
initialize（工作目录、数据目录、授予的能力、限额）
  ↓
tool/run（已校验并补全默认值的输入）
  ↓
工具发 tool/log、tool/progress、tool/section 通知
  ↓
返回 Result（摘要 + 区块 + 产物 + 动作）
  ↓
宿主校验结果、结算产物路径与大小、落库
```

工具运行在**独立进程**里：拿不到宿主的 Go 运行时，也不能导入 `internal/*`。
唯一的通信面是 `pkg/tool` 的类型、`pkg/tool/pluginproto` 的协议，以及
`pkg/tool/host` 对宿主服务的类型化封装。

## 2. 最小可用工具

目录结构：

```text
opentool-kit/
    tools/hello/tool.go
    cmd/plugins/hello/main.go
    tools/plugins.json      # 加一条登记
```

`tools/hello/tool.go`：

```go
// Package hello 是示例工具。
package hello

import (
	"context"
	"strings"

	"github.com/cn-maul/OpenTools/pkg/tool"
)

const version = "1.0.0"

// Tool 是示例工具的实现。
type Tool struct{}

// New 返回示例工具。
func New() Tool { return Tool{} }

// Definition 描述工具的稳定元数据。宿主每次启动都会调用它。
func (Tool) Definition() tool.Definition {
	return tool.Definition{
		ID:       "sample.hello",
		Name:     "打个招呼",
		Summary:  "把输入的名字拼成一句问候。",
		Category: "示例",
		Icon:     "svg:M4 5h16v14H4z|M8 10h8",
		Keywords: []string{"示例", "hello"},
		Version:  version,
		Input: tool.Object(map[string]*tool.Schema{
			"name": tool.String("名字", "会被写进问候语。").WithDefault("世界"),
		}, "name"),
	}
}

// Run 执行一次调用。必须在 ctx 取消时尽快返回。
func (Tool) Run(
	ctx context.Context,
	input tool.Input,
	env tool.Environment,
	report tool.Reporter,
) (tool.Result, error) {
	var params struct {
		Name string `json:"name"`
	}
	if err := input.Decode(&params); err != nil {
		return tool.Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}

	greeting := "你好，" + strings.TrimSpace(params.Name) + "！"
	report.Log("info", "已生成问候语")

	return tool.Result{
		Summary: greeting,
		Blocks: []tool.Block{{
			Kind: tool.BlockText,
			Text: greeting,
		}},
	}, nil
}

var _ tool.Tool = Tool{}
```

`cmd/plugins/hello/main.go`：

```go
// Command hello 是「打个招呼」的插件入口。
package main

import (
	"github.com/cn-maul/OpenTools/pkg/tool/pluginproto"
	"github.com/cn-maul/opentool-kit/tools/hello"
)

func main() {
	pluginproto.Serve(hello.New())
}
```

`tools/plugins.json` 追加：

```json
{
  "name": "hello",
  "output": "hello",
  "author": "opentool-kit",
  "homepage": "https://github.com/cn-maul/opentool-kit"
}
```

`name` 对应 `cmd/plugins/<name>` 目录，`output` 是编译出的文件名和
`<output>-<version>.zip`。两者可以不同（现有工具就不同），但必须唯一。

打包与验证：

```powershell
cd C:\path\to\opentool-kit
.\publish-store.ps1 -SkipUpload
```

脚本会编译每个插件、执行 `<exe> describe` 生成清单、打 ZIP、写
`store/catalog.json`。`describe` 失败或没有 `version` 字段会直接中止。

ZIP 根目录只有两个文件，顺序和层级都不能改：

```text
hello-1.0.0.zip
├── manifest.json
└── hello.exe
```

`manifest.json` 由脚本生成，其中 `schemaVersion` 固定 1，`protocolVersion`
固定 2，`platform` 必须与目标系统一致。手工改 ZIP 很容易打破 SHA256 与
目录条目的对应关系，升级一律重新跑脚本。

Windows 安装器只接受 `.exe`，可执行文件必须在 1 字节到 256 MiB 之间。
清单里的 `executable` 只能是包内相对路径。

`output` 决定包名，`Definition.ID` 决定安装目录。两者不要混用：包名可以改，
`ID` 不能改。工具改名时只动 `output` 和显示名，老用户仍按原 `ID` 升级。

## 3. Definition 字段

| 字段 | 必填 | 说明 |
|---|---|---|
| `ID` | 是 | 全局唯一，建议 `分类.名称`，安装后作为目录名 |
| `Name` | 是 | 界面显示名 |
| `Summary` | 是 | 一行说明，用于列表和搜索 |
| `Description` | 否 | 详情页长文，可换行 |
| `Category` | 是 | 左侧分组，同名归为一类 |
| `Icon` | 否 | `svg:<path>`，24×24 的 `viewBox`，多个 path 用竖线分隔；其它值退化为首字标记 |
| `Keywords` | 否 | 参与搜索匹配（名称、摘要、分类、关键词） |
| `Version` | 是 | `1.2.3` 形式，商店用它判断升级 |
| `Effects` | 否 | 对外声明的影响面，见下 |
| `Layout` | 否 | `"tabs"` 把多个结果分区放进标签页 |
| `Requires` | 否 | 需要宿主授予的能力，见「宿主服务」 |
| `Input` | 是 | 输入表单，见下 |

`Effects` 是**声明**，宿主目前不会据此拦截任何操作。它的用途是让人在安装前
看清工具会做什么，所以要和实际行为一致：

- `read-files` 读取用户文件
- `write-files` 写出文件
- `delete-files` 删除文件
- `network` 访问网络
- `secrets` 处理用户密钥
- `system-settings` 修改系统设置（提权、注册表、服务等）

`ID` 一旦发布就不要改：用户的已安装目录、任务历史都按它关联。

版本比较按数字段进行，`v1.2.0` 与 `1.2.0` 等价，`1.2.10` 高于 `1.2.9`。
后缀只取其中开头的数字：`1.2.0-beta` 与 `1.2.0` 相等，安装过的正式版不会
因为换成 `-beta` 而触发升级。发布版本只用三段数字最稳妥。

## 4. 输入 Schema

只支持 JSON Schema 的一个小子集，够用且表单能完整渲染。

### 类型

| 构造 | 类型 | 渲染 |
|---|---|---|
| `tool.String(title, desc)` | string | 单行输入框 |
| `tool.Text(title, desc, rows)` | string | 多行文本域 |
| `tool.Number(title, desc)` | number | 数字输入框 |
| `tool.Boolean(title, desc)` | boolean | 开关 |
| `tool.Select(title, desc, values...)` | string + enum | 下拉框 |
| `tool.Object(props, required...)` | object | 字段组 |

数组没有构造快捷函数，直接写字面量即可；界面会用一个 JSON 文本框编辑：

```go
&tool.Schema{
    Type:  "array",
    Title: "端口",
    Items: tool.Number("端口号", ""),
}
```

`integer` 也只在手写字面量时出现，`tool.Number` 生成的是 `number`。

### 约束

```go
tool.String("端口", "").WithPattern(`^\d+$`)
tool.Number("并发", "").WithMin(1).WithMax(64)
tool.String("名称", "").WithMin(2).WithMax(32)   // 对 string 是长度
```

- `WithMin` / `WithMax` 在 `string` 上表示**字符数**（按 rune 计），在
  `number` 上表示数值范围。
- 必填字段不允许空字符串：`required` 里的字段传 `""` 会以 `required` 报错。
- `AdditionalProperties` 默认关闭，传未知字段会被拒绝。

### 展示提示

```go
tool.String("源文件", "").
    WithWidget("file", "选择一个文件").   // "file" / "directory" / "code" / "textarea"
    WithAccept(".docx", ".txt", ".md").   // 只影响文件选择器
    WithVisibleWhen("mode", "custom")     // 仅在 mode 取这些值时显示

tool.String("API Key", "").
    WithWidget("", "sk-…").
    WithSecret()                          // 密码框，并且落库前脱敏
```

可用 widget：

| widget | 效果 |
|---|---|
| `file` | 右侧「选择」按钮打开原生文件选择器 |
| `directory` | 打开原生文件夹选择器 |
| `code` | 等宽大文本域，可从文件载入 |
| `textarea` | 多行文本域 |
| `hidden` | 渲染为隐藏域 |
| 其它 / 空 | 普通输入框 |

`code` widget 除了编辑文本，还会显示「从文件载入」按钮；载入的是**文件内容**，
不是路径。`file` widget 写入的才是路径。

`WithEnumLabels` 可以给枚举值配显示名，序列化值保持不变：

```go
tool.Select("协议", "", "openai-chat", "anthropic").
    WithEnumLabels(map[string]string{"openai-chat": "OpenAI Chat", "anthropic": "Anthropic"})
```

`WithVisibleWhen` 只控制**显示**，被隐藏的字段仍保留当前值并参与校验。因此
不要用它来关闭一个必填字段——用户切换选项后，旧值仍会提交。需要真正互斥
时，让工具根据模式忽略无关字段，并把必填约束放在工具自己的逻辑里。

注意：`WithGroup` 目前只会写进 schema，界面还没有分组渲染。需要分组时用
`tool.Object` 嵌一层，它会渲染成带标题的字段组。

### 默认值

默认值由宿主在 `tool.Prepare` 里补齐，工具拿到的输入一定包含全部默认值。
界面也会用默认值初始化表单，所以 `tool.Input` 里读到的字段可以直接使用。

`Prepare` 还会做三件容易忽略的事：

- 递归补全嵌套对象和数组元素的默认值；
- 校验必填、类型、长度、范围、正则和枚举；
- 把数字统一成 JSON 数字，工具侧用 `float64` / `json.Number` 接收。

表单端只做展示和即时提示，**真正的校验在宿主**。因此同一个工具被 CLI、
动作按钮或 API 调用时行为一致。

### 输入里不要放“运行时才知道”的东西

Schema 是安装前就能展示的静态契约。不要把当前机器上才存在的路径、昨天
生成的临时文件、上一次运行的结果写进 `Default`。默认值只能是所有用户都
成立的值；需要动态推荐时，留空并在工具里给可读的提示。

## 5. Run 的约定

```go
func (Tool) Run(ctx context.Context, input tool.Input, env tool.Environment, report tool.Reporter) (tool.Result, error)
```

### 取消

`ctx` 会在用户点「取消」、宿主退出、或超出 `Limits.MaxJobDuration` 时结束。
必须把 `ctx` 透传给 `exec.CommandContext`、HTTP 请求等，并在长循环里检查：

```go
for _, item := range items {
    if err := ctx.Err(); err != nil {
        return tool.Result{}, err
    }
    // ...
}
```

返回 `ctx.Err()` 后任务状态是 `canceled`，不是 `failed`，不会弹错误。

宿主给工具 3 秒宽限期：先发 `$/cancel`，再关闭 stdin，仍不退出才强杀。
所以**不要**忽略 `ctx`——被强杀时来不及落盘或回滚。

`ctx` 的截止时间来自宿主的 `Limits.MaxJobDuration`。当前桌面端不设置总时长
上限（值为 0），所以工具自己的网络请求和子进程超时仍要显式设置，不能指望
宿主兜底。

### 环境

| 字段 | 用途 |
|---|---|
| `env.WorkDir` | 本次任务私有目录，产物必须写在这里 |
| `env.DataDir` | 每位用户共享的数据目录，只放必须在任务之间保留的小状态 |
| `env.Host` | 宿主服务入口，可能为 `nil` |

`WorkDir` 是 `data\jobs\<job-id>`，任务历史清除时整个目录一起删除，不能当
持久存储用。需要长期保存的配置或密钥走宿主服务（`config` / `secrets`），
不要自己往 `DataDir` 写 JSON 文件——那样既拿不到加密，也会和宿主的清理策略
打架。

桌面端退出时会清空任务历史，产物目录也在其中。用户确实需要留存文件时，
要么在任务结束前通过结果里的产物入口另存，要么由工具用 `dialogs.SaveFile`
直接写到用户选择的位置。

### 产物

```go
path := filepath.Join(env.WorkDir, "报告.xlsx")
if err := os.WriteFile(path, data, 0o644); err != nil {
    return tool.Result{}, err
}
absolute, size, err := tool.ResolveArtifact(env, path)
if err != nil {
    return tool.Result{}, err
}
return tool.Result{
    Summary: "已生成报告",
    Artifacts: []tool.Artifact{{
        Name: "报告.xlsx",
        Path: absolute,
        MIME: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
    }},
}, nil
```

- 路径必须在 `WorkDir` 之下，且是普通文件；`ResolveArtifact` 会校验并返回大小。
- 不填 `Name` 时宿主用文件名；不填 `MIME` 时按扩展名猜。
- 产物校验失败会让整个任务变成 `failed`，所以写完再返回。

### 日志与进度

```go
report.Log("info", "正在解析文档")     // level 只影响样式：error 变红，其余灰色
report.Progress(2, 5)                 // (已完成, 总数)，宿主据此画进度条
```

日志会按行持久化（上限 2000 行）。不要把密钥写进日志——日志会落库。
工具进程写到 stderr 的内容也会被宿主按行收进日志，用作崩溃诊断。

### Windows 中文与 PowerShell

工具给 `report.Log` 和 `Result` 传的是 Go 字符串，宿主按 UTF-8 读写，不需要
额外设置代码页。真正容易乱码的是**子进程**：

```go
script := "$OutputEncoding = [Text.UTF8Encoding]::new($false)\n" +
    "[Console]::OutputEncoding = $OutputEncoding\n" + body

encoded := base64.StdEncoding.EncodeToString(utf16LE(script))
cmd := exec.CommandContext(ctx, powershell,
    "-NoLogo", "-NoProfile", "-NonInteractive",
    "-ExecutionPolicy", "Bypass", "-EncodedCommand", encoded,
)
```

`utf16LE` 就是 `utf16.Encode([]rune(text))` 后按小端序写入字节：

```go
func utf16LE(value string) []byte {
    units := utf16.Encode([]rune(value))
    out := make([]byte, 0, len(units)*2)
    for _, unit := range units {
        out = append(out, byte(unit), byte(unit>>8))
    }
    return out
}
```

要点：

- 用 `-EncodedCommand`（UTF-16LE + Base64）传脚本，避开中文和引号的转义；
- 脚本开头显式把 `$OutputEncoding` 和 `[Console]::OutputEncoding` 设为 UTF-8；
- 固定用系统自带的 Windows PowerShell 5.1，不依赖 PowerShell 7；
- 读子进程输出前不要用本地代码页解码，直接按字节接收再按 UTF-8 转字符串。

需要多次调用时，优先把逻辑合并进一个脚本；每次 `exec` 都会启动一个新的
PowerShell 进程，扫描类工具很容易因此慢一个数量级。

## 6. 结果怎么显示

结果有三层：摘要、区块、分区。摘要来自 `Result.Summary`；区块可以放在
`Result.Blocks` 里，也可以随分区逐个上报。

### 两种模式，不能混用

- **不调用 `report.Section`**：界面显示 `Result.Summary` + `Result.Blocks`。
- **调用过 `report.Section`**：界面仍显示 `Result.Summary`，但只渲染分区，
  `Result.Blocks` 不再出现。

第二种模式适合有阶段的工具：先上报所有分区的 `pending` 占位，再逐个推进。

```go
for _, section := range []tool.Section{
    {ID: "source", Title: "源文档", State: tool.SectionPending},
    {ID: "result", Title: "处理结果", State: tool.SectionPending},
} {
    report.Section(section)
}

report.Section(tool.Section{ID: "source", Title: "源文档", State: tool.SectionRunning})
// ...干活...
report.Section(tool.Section{
    ID:    "source",
    Title: "源文档",
    State: tool.SectionReady,
    Blocks: []tool.Block{{Kind: tool.BlockFacts, Data: []tool.Fact{
        {Label: "文件", Value: "通报.docx"},
        {Label: "段落", Value: "42"},
    }}},
})

report.Section(tool.Section{
    ID:     "result",
    Title:  "处理结果",
    State:  tool.SectionReady,
    Blocks: []tool.Block{{Kind: tool.BlockText, Text: "已完成规范化。"}},
})
```

规则：

- `ID` 和 `Title` 必填；同一个 `ID` 重复上报会原位替换。
- `state` 只能是 `pending` / `running` / `ready` / `skipped`。
- `ready` 必须有内容（区块或 `Text`），否则整条上报被丢弃。
- 区块非法时整条上报被静默丢弃，工具不会收到错误——本地自测时要用
  `tool.ValidateSection` 提前查。
- `Layout: "tabs"` 且分区不少于两个时变成标签页；否则一律纵向排列。
  分区多时用 `tabs`，不要指望界面自动换布局。

### 区块类型

| `Kind` | 主要字段 | 用途 |
|---|---|---|
| `text` | `Text` | 一段正文 |
| `notice` | `Text`、`Tone` | 带颜色的提示条 |
| `code` | `Text`、`Language` | 等宽代码块，带复制按钮；`Language` 只作标注，不做高亮 |
| `json` | `Data` | 任意结构，格式化成 JSON 显示 |
| `diff` | `Text` | 等宽差异文本 |
| `table` | `Columns`、`Rows`、`ColumnWidths`、`RowIcons`、`RowActions`、`BatchAction` | 主结果表 |
| `status-grid` | `Data` 里的 `cells` / `legend` / `labeled` | 主机或设备状态矩阵 |
| `meter` | `Data` 里的 `items` / `facts` | 环形或条形指标 |
| `facts` | `Data` 为 `[]Fact` | 标签值列表 |

`Tone` 取 `neutral` / `success` / `warning` / `danger`。

表格另有两个可选的排版字段：

- `ColumnWidths` 与 `Columns` 等长，值是渲染器的 DIP 宽度，写成 `"120px"`
  这样的像素数字（`px` 只是单位写法，不是浏览器的 CSS 百分比）。留空或写
  `"0px"` 表示按内容自动分配；不支持 `"30%"`、`"auto"` 这类 CSS 表达式。
- `RowIcons` 与 `Rows` 等长，值是 `data:` URI。空字符串会退化为首字标记，
  不要留空数组，否则整列图标都不显示。

`status-grid` 与 `meter` 的 `Data` 用 map，因为渲染器读取固定键：

```go
tool.Block{
    Kind:  tool.BlockGrid,
    Title: "192.168.1.0/24",
    Data: map[string]any{
        "cells": []tool.StatusCell{
            {Label: "192.168.1.10", Tone: tool.ToneSuccess, Category: "windows", Detail: "DESKTOP-A"},
        },
        "legend":  []tool.StatusLegendItem{{Label: "Windows 主机", Category: "windows"}},
        "labeled": true,
    },
}

tool.Block{
    Kind:  tool.BlockMeter,
    Title: "磁盘占用",
    Data: map[string]any{
        "items": []tool.MeterItem{
            {Label: "C:", Value: 78, Unit: "%", Variant: "bar", Segments: []tool.MeterSegment{
                {Label: "系统", Value: 30},
                {Label: "数据", Value: 48},
            }},
        },
        "facts": []tool.Fact{{Label: "剩余", Value: "120 GB"}},
    },
}
```

`meter` 的 `Variant` 为 `"ring"`（默认）或 `"bar"`；`Value` 与 `Max` 决定比例，
`Unit` 决定后缀，`Max` 为 100 时默认显示 `%`。

`facts` 的 `Group` 相同会归到一个小组标题下，适合放两组以上标签值。

### 原生界面实际怎么画

桌面端完全由 Go + MyGo 原生控件绘制，没有网页层，因此下面这些**渲染约定**
就是工具作者能依赖的全部外观契约：

- 摘要是一行说明，显示在结果顶部；`Description` 只在工具页头部出现。
- 区块按上报顺序纵向排列。`Title` 非空时先画一行小标题，再画区块本体。
- `notice` 用右侧留白的两行提示条；`Tone` 决定颜色，`neutral` 用正文色。
- `code` / `json` / `diff` 共用一个等宽块：右上角有「复制」按钮，文本自动换行，
  `Language` 目前不参与语法高亮，只作为语义标注保留。
- `table` 的行高约 30 DIP，表格整体高度限制在 360 DIP 以内（约 10 行），
  超出后内部滚动，表头固定。列宽用 `ColumnWidths` 的 DIP 数值，最小值
  72 DIP；列多时优先保证前两列可读。
- `status-grid` 不传 `labeled` 时是 18×18 的色块矩阵，悬停显示 `Label + Detail`；
  `labeled: true` 时每个单元格放大成 72×34 的卡片，适合设备名很短、需要直接
  读出来的场景。
- `meter` 统一渲染成横向进度条，`Variant: "ring"` 目前与 `"bar"` 同形；
  `Segments` 在条下方按彩色方块列出，而不是叠加在进度条里。
- `facts` 是两列布局：左列 150 DIP 宽放 `Label`，右列换行放 `Value`。

分区的 `Layout: "tabs"` 在桌面端是一个横向分段控件，标签过多时会挤在一起，
因此分区**标题尽量控制在 6 个汉字以内**；超过 4 个分区建议改用堆叠布局。

复制与导出会把 `Sections` 里的内容一起带上：依次输出摘要、每个分区的标题与
正文/区块、最后是 `Result.Blocks`。因此不要为了「导出好看」把同一份数据
同时放进分区和 `Result.Blocks`，界面上不会重复，导出文本里会。

### 表格里的操作

行内按钮：

```go
tool.Block{
    Kind:    tool.BlockTable,
    Columns: []string{"服务", "延迟"},
    Rows:    [][]string{{"阿里 DNS", "12 ms"}},
    RowActions: [][]tool.RowAction{{
        {
            Label:   "切换",
            Tone:    tool.ToneSuccess,
            Confirm: "确认切换到这个 DNS？",   // 非空会先弹确认框
            Input:   map[string]any{"provider": "ali"},
        },
    }},
}
```

- `RowActions` 的行数必须与 `Rows` 一致，没有操作的行走 `nil`。
- 点击后会用 `action.Input` **覆盖**当前表单值再去执行，未覆盖的字段沿用
  表单里的当前值。
- `Variant: "switch"` 渲染成开关（用 `On` 表示当前状态）；`Variant: "select"`
  渲染成下拉菜单（需要 `Options`，`Value` 必须是其中之一）。

批量操作：

```go
BatchAction: &tool.BatchAction{
    Label:    "批量启动",
    Confirm:  "确认启动选中的 {count} 项？",
    Input:    map[string]any{"action": "start"},
    ItemKey:  "service",   // 从每行 RowActions[0].Input 里取这个键
    ItemsKey: "services",  // 汇总后放到这个输入字段
}
```

选中的值会序列化成 **JSON 字符串**放进 `ItemsKey`，工具侧要自己二次解析：

```go
raw, _ := input.Values["services"].(string)
var names []string
if err := json.Unmarshal([]byte(raw), &names); err != nil {
    return tool.Result{}, fmt.Errorf("批量输入无效：%w", err)
}
```

批量模式会隐藏行内按钮，改为勾选框加一个底部按钮。

### 下一步动作

`Result.Actions` 会在结果底部渲染成按钮：

```go
Actions: []tool.Action{{
    Label:   "重新扫描",
    Tone:    tool.ToneNeutral,
    Confirm: "重新扫描会覆盖当前结果。",
    Input:   map[string]any{"refresh": true},
}}
```

动作只能调用**本工具**，输入同样要过 schema 校验。不能借它调用别的工具。

`Result.Warnings` 会渲染成黄色提示条，适合“结果可用，但有部分数据缺失”的
场景。它不影响任务状态；真正失败请返回 error，而不是把错误塞进 warnings。

## 7. 宿主服务

工具进程不能弹原生对话框、不能提权、也不该自己造密钥存储。这些能力由宿主
提供，工具通过 `env.Host` 调用。

### 声明与授予

```go
Requires: []string{
    pluginproto.CapabilityDialogs,
    pluginproto.CapabilitySecrets,
},
```

类型化封装在 `pkg/tool/host`，能力常量在 `pkg/tool/pluginproto`：

```go
import (
    "github.com/cn-maul/OpenTools/pkg/tool"
    "github.com/cn-maul/OpenTools/pkg/tool/host"
    "github.com/cn-maul/OpenTools/pkg/tool/pluginproto"
)
```

宿主回传的是「工具声明 ∩ 宿主实现」。桌面端实现了全部五项；CLI 没有原生
界面，只授予 `secrets` 和 `config`。

**未授予不等于崩溃**：工具要能降级。标准写法是：

```go
secrets, err := host.OpenSecrets(env)
if err != nil {
    // 没有宿主（单元测试）或宿主没授予，走不需要密钥的分支
    report.Log("warn", "密钥库不可用："+err.Error())
}
```

### 服务清单

| 入口 | 能力 | 用途 |
|---|---|---|
| `host.OpenDialogs(env)` | `dialogs` | `OpenFile` / `SaveFile` / `PickDirectory` |
| `host.OpenSecrets(env)` | `secrets` | `Get` / `Set` / `Delete`，每个工具独立 |
| `host.OpenConfig(env)` | `config` | `Get` / `Set` 结构化配置 |
| `host.Notify(ctx, env, title, body)` | `notify` | 系统通知 |
| `host.Elevate(ctx, env, request)` | `elevate` | 宿主统一 UAC |

所有调用都要传 `ctx`：用户在对话框里停留、任务被取消时，调用会随之返回。

### 密钥

```go
secrets, err := host.OpenSecrets(env)
if err == nil {
    if params.APIKey == "" {
        if stored, _ := secrets.Get(ctx, "llm.api_key"); stored != "" {
            params.APIKey = stored
        }
    } else if params.Remember {
        if err := secrets.Set(ctx, "llm.api_key", params.APIKey); err != nil {
            report.Log("warn", "未能写入密钥库："+err.Error())
        }
    }
}
```

Windows 上用 DPAPI 加密，绑定当前用户；其它平台只有文件权限保护。密钥不会
进入任务快照，也不会出现在数据库里。落盘位置是
`data\tools\<tool-id>\secrets.bin`，与 `config.json` 同目录。

密钥名会先被规范化：只保留字母、数字和 `.` `-` `_` `:`，最长 64 个字符；
空值等价于删除。因此不要在名字里放路径或中文。

如果字段标了 `WithSecret()`，宿主落库前还会把它替换成 `<redacted>`，工具
执行时拿到的仍是原值。

### 对话框

```go
dialogs, err := host.OpenDialogs(env)
if err != nil {
    return tool.Result{}, errors.New("当前环境无法选择文件，请直接填写路径")
}
path, err := dialogs.SaveFile(ctx, "保存报告", "报告.xlsx", []string{".xlsx"})
if err != nil {
    return tool.Result{}, err
}
if path == "" {
    return tool.Result{}, errors.New("已取消保存")
}
```

`OpenFile` 和 `PickDirectory` 在用户取消时同样返回空路径、无错误。

对话框返回的是用户选择的**绝对路径**，可能在任何位置。工具要自己校验
扩展名和可读性，不能假设它一定在 `WorkDir` 里；若要在结果里返回该文件，
先复制到 `WorkDir` 再走 `Artifacts`。

长时间任务不要依赖对话框——CLI 与后台任务里没有可交互的窗口。

### 提权

```go
result, err := host.Elevate(ctx, env, tool.ElevateRequest{
    Title:   "修改 DNS",
    Program: `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`,
    Script:  script,        // 宿主写入临时 .ps1（带 BOM）再执行
    Args:    []string{"-InputPath", inputPath},
})
```

宿主负责弹 UAC、等待、回收退出码和输出。用户取消会返回错误，工具按普通失败
处理即可。`Script` 适合较长的中文脚本，能避开命令行转义问题。

`Program` 建议写成绝对路径；`Start-Process` 虽然能按 `PATH` 解析，但换一台
机器可能就找不到了。提权进程的 stdout/stderr 会被捕获到临时文件，因此脚本
里不要依赖交互式输入，也不要把密码写在命令行参数里——它们会进入进程列表。

### 配置

```go
config, err := host.OpenConfig(env)
if err == nil {
    var prefs struct {
        Theme string `json:"theme"`
    }
    found, _ := config.Get(ctx, "prefs", &prefs)
    if !found {
        prefs.Theme = "light"
    }
    _ = config.Set(ctx, "prefs", prefs)
}
```

值按 JSON 存，可以放结构体。不要往里放密钥，用 `secrets`。

### 通知

```go
if err := host.Notify(ctx, env, "扫描完成", "发现 3 台离线设备"); err != nil {
    report.Log("warn", "通知不可用："+err.Error())
}
```

通知是尽力而为，失败不应让任务失败。CLI 没有 `notify` 能力，调用会返回
能力错误。

## 8. 本地自测

不需要先发布，也不需要启动桌面窗口。编译出的 exe 本身就是一个完整的协议
端点。

### 8.1 看元数据

```powershell
go build -o .\build\hello.exe .\cmd\plugins\hello
.\build\hello.exe describe
```

输出是一行 JSON。重点检查：

- `id`、`version`、`category` 非空；
- `input` 是你期望的表单结构；
- `requires` 只列出真正调用的能力。

解析失败时先确认没有往 stdout 打日志。**stdout 只留给协议**，`fmt.Println`
会把 `describe` 的输出和 JSON-RPC 流都污染掉；调试信息一律走 stderr 或
`report.Log`。

### 8.2 跑一次 rpc

协议是行分隔 JSON，但**必须等 `initialize` 的响应回来再发 `tool/run`**。
一次性把两行喂给进程，两个请求会被并发处理，可能先执行 `tool/run` 并得到
`initialize must be called first`。

用下面的脚本顺序收发；`Read-Response` 会跳过 `tool/log` 这类通知，直到拿到
匹配的 `id`：

```powershell
$exe = ".\build\hello.exe"
$psi = [Diagnostics.ProcessStartInfo]::new((Resolve-Path $exe))
$psi.Arguments = "rpc"
$psi.UseShellExecute = $false
$psi.RedirectStandardInput = $true
$psi.RedirectStandardOutput = $true
$psi.CreateNoWindow = $true
$p = [Diagnostics.Process]::Start($psi)

function Send($message) {
    $p.StandardInput.WriteLine(($message | ConvertTo-Json -Depth 20 -Compress))
    $p.StandardInput.Flush()
}
function Read-Response([int]$id) {
    while ($true) {
        $line = $p.StandardOutput.ReadLine()
        if ($null -eq $line) { throw "进程提前退出" }
        $message = $line | ConvertFrom-Json
        if ($message.id -eq $id) { return $message }
    }
}

Send @{
    jsonrpc = "2.0"; id = 1; method = "initialize"
    params = @{
        protocolVersion = 2
        toolId = "sample.hello"
        workDir = "C:\Temp\hello"
        dataDir = "C:\Temp\hello-data"
        grantedCapabilities = @()
        limits = @{ maxMessageBytes = 16777216; cancelGraceMs = 3000 }
    }
}
Read-Response 1

Send @{
    jsonrpc = "2.0"; id = 2; method = "tool/run"
    params = @{ input = @{ name = "OpenTools" } }
}
$result = Read-Response 2
$result.result.summary

$p.StandardInput.Close()
$p.WaitForExit(5000) | Out-Null
```

预期最后打印 `你好，OpenTools！`。`workDir` 必须事先存在，否则写产物的工具
会失败；脚本里用 `New-Item -ItemType Directory -Force` 先建好即可。

开发时也可以直接运行 `rpc` 后手工粘贴单行 JSON，但同样要一次只发一条并
等待响应。

### 8.3 不用写客户端也能跑：CLI

如果只是想验证“输入 → 结果”，把工具装进一个隔离数据目录，用 CLI 跑一遍
最省事。CLI 会替你完成 `initialize`、`tool/run`、收集日志和产物：

```powershell
cd C:\path\to\OpenTools
go build -o "$env:TEMP\opentools.exe" .\cmd\opentools

$env:OPENTOOLS_DATA_DIR = "$env:TEMP\opentools-dev"
$env:OPENTOOLS_CATALOG_URL = "C:\path\to\opentool-kit\store\catalog.json"
& "$env:TEMP\opentools.exe" install sample.hello
& "$env:TEMP\opentools.exe" run sample.hello --name OpenTools --json
```

`--json` 打印完整任务快照（包括 `sections`、`artifacts` 和 `logs`），适合
对照协议字段。普通输出只打印摘要和结果 JSON，日志走 stderr。

### 8.4 取消

用一个会跑上一分钟的工具，在它运行期间写入取消通知，验证能否及时退出：

```powershell
Send @{ jsonrpc = "2.0"; method = '$/cancel'; params = @{ reason = "test" } }
Read-Response 2
```

工具应返回 `-32800`（`CodeCanceled`），而不是 `-32000`。返回普通错误说明
`Run` 把 `ctx.Err()` 包装成了别的错误；完全不返回说明忽略了 `ctx`。宿主在
3 秒宽限期后会强杀进程，届时看到的是连接断开而不是取消响应。

### 8.5 通过宿主安装

真实链路要验证三件事：商店能读到 ZIP、安装目录结构正确、CLI 能带着输入
执行。

```powershell
cd C:\path\to\opentool-kit
.\publish-store.ps1 -SkipUpload

cd C:\path\to\OpenTools
go build -o "$env:TEMP\opentools.exe" .\cmd\opentools

$env:OPENTOOLS_DATA_DIR = "C:\Temp\opentools-test"
$env:OPENTOOLS_CATALOG_URL = "C:\path\to\opentool-kit\store\catalog.json"

& "$env:TEMP\opentools.exe" store --refresh
& "$env:TEMP\opentools.exe" install sample.hello
& "$env:TEMP\opentools.exe" run sample.hello --name OpenTools
```

`install` 会先刷新商店，再用工具 ID 匹配 `catalog.json`。安装后的目录是
`$env:OPENTOOLS_DATA_DIR\plugins\sample.hello\`，里面应有 `manifest.json`
和 exe。

为什么不在 `opentool-kit` 里直接 `go run` 框架的 CLI：那会把工具仓库当成主
模块，框架的 GUI、SQLite 依赖不在它的 `go.sum` 里，会报 `missing go.sum
entry`。宿主始终从框架仓库构建。

测试目录用完可以整体删掉；它和真实用户的 `data` 完全隔离。

### 8.6 CLI 的输入限制

CLI 只把顶层 `string` / `number` / `boolean` 字段映射成参数：

```powershell
& "$env:TEMP\opentools.exe" run sample.hello --name OpenTools
& "$env:TEMP\opentools.exe" run mytool --count 3 --verbose
```

对象、数组和带复杂结构的字段没有对应参数，也无法从命令行传入。需要脚本
调用时，把工具输入设计成平铺的简单字段；确实需要结构时，在工具里接受一个
JSON 字符串字段并自行解析。

校验错误会以 `code|field|message` 的形式打印，例如：

```text
opentools: required|name|此项为必填项。
```

等号两边不要加空格地写 `--name=OpenTools` 也可以；布尔值用 `--verbose`
或 `--verbose=false`。

## 9. 常见坑

| 现象 | 原因 | 处理 |
|---|---|---|
| `describe` 输出不是合法 JSON | 往 stdout 打了日志 | 所有调试输出改走 stderr |
| 点击取消后任务仍是 `failed` | `Run` 返回了包装后的错误 | 直接返回 `ctx.Err()`，或让 `errors.Is(err, context.Canceled)` 成立 |
| 取消后进程被强杀 | 长循环或 `exec` 没透传 `ctx` | 用 `exec.CommandContext`，循环里检查 `ctx.Err()` |
| 界面不显示 `Blocks` | 调用过 `report.Section` | 二选一：要么只用 Blocks，要么把内容放进分区 |
| 某个分区整条消失 | `ready` 没有内容，或区块校验失败 | 本地用 `tool.ValidateSection` 检查 |
| 表格右侧按钮错位 | `RowActions` 行数与 `Rows` 不一致 | 每一行都给一个切片，没有动作填 `nil` |
| 批量操作拿到 `"[...]"` | 宿主把选中项序列化成了 JSON 字符串 | 在工具里 `json.Unmarshal` 到 `[]string` |
| 安装时报清单版本/平台错误 | ZIP 里的 `manifest.json` 过期 | 重新跑 `publish-store.ps1`，不要手改 ZIP |
| 本地商店刷新后没有条目 | `catalog.json` 的 `tools` 为空或下载路径不对 | 先跑 `publish-store.ps1 -SkipUpload` 生成完整目录 |
| 升级时提示文件占用 | 旧进程还没退出 | 等任务结束后重试；安装失败不会破坏旧版本 |
| 密钥出现在任务详情里 | 字段没标 `WithSecret()` | 给字段加 `.WithSecret()`，并确认走宿主 `secrets` |
| CLI 里对话框直接失败 | CLI 只授予 `secrets` / `config` | 设计一个“直接填路径”的降级分支 |
| 工具拿到空字符串但 schema 说必填 | required 对空白字符串同样报错 | 先 `strings.TrimSpace`，或在默认值里给非空值 |
| 大结果被拒绝 | 超过单条消息上限（默认 16 MiB） | 拆成多个分区，或用产物文件代替内联数据 |

## 10. 发布检查清单

发布前逐项过一遍：

- [ ] `go build ./...` 与 `go vet ./...` 通过；
- [ ] `describe` 输出的 `version` 已递增，`id` 与上次发布一致；
- [ ] `Effects` 与真实行为一致，`Requires` 只包含实际调用的服务；
- [ ] 至少用管道跑过一次 `rpc`，并验证了取消；
- [ ] `publish-store.ps1 -SkipUpload` 能生成 `store/catalog.json` 和 ZIP；
- [ ] 用 `OPENTOOLS_CATALOG_URL` 指向本地商店，`install` + `run` 成功；
- [ ] 卸载后重新安装一次，确认没有依赖旧目录里的残留文件；
- [ ] 需要密钥的工具，在“未授予 secrets”的环境下能给出可读的降级提示；
- [ ] 没有把 `work/`、测试产物或本地路径写进提交。

发布命令：

```powershell
cd C:\path\to\opentool-kit
$env:OPENTOOLS_GITEE_TOKEN = "<token>"
.\publish-store.ps1
```

脚本先编译全部插件，再逐个执行 `describe`。任何一个工具构建失败或没有
`version`，整批都会中止，不会产生半套商店目录。
