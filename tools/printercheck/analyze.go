package printercheck

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"opentools/pkg/tool"
)

const (
	driverInstalled = "已安装"
	driverMissing   = "缺失"
	driverBroken    = "文件缺失"
	driverUnknown   = "无法验证"
)

type analysis struct {
	snapshot snapshot
	printers []printerAnalysis
	ready    int
	warning  int
	danger   int
	virtual  int
	physical int
}

type printerAnalysis struct {
	Info             printerInfo
	Port             *portInfo
	Driver           *driverInfo
	Device           *deviceInfo
	DriverState      string
	Network          bool
	NetworkChecked   bool
	NetworkReachable bool
	Virtual          bool
	Tone             tool.Tone
	State            string
	Detail           string
}

func analyze(ctx context.Context, value snapshot) analysis {
	out := analysis{snapshot: value}
	ports := make(map[string]portInfo, len(value.Ports))
	for _, item := range value.Ports {
		ports[strings.ToLower(strings.TrimSpace(item.Name))] = item
	}
	drivers := make(map[string]driverInfo, len(value.Drivers))
	for _, item := range value.Drivers {
		if key := strings.ToLower(strings.TrimSpace(item.Name)); key != "" {
			drivers[key] = item
		}
	}
	devices := make(map[string]deviceInfo, len(value.Devices))
	for _, item := range value.Devices {
		if key := strings.ToLower(strings.TrimSpace(item.Name)); key != "" {
			devices[key] = item
		}
	}

	for _, info := range value.Printers {
		item := printerAnalysis{Info: info}
		if port, ok := ports[strings.ToLower(strings.TrimSpace(info.PortName))]; ok {
			item.Port = &port
		} else if strings.TrimSpace(info.PortName) != "" {
			item.Port = &portInfo{
				Name:         info.PortName,
				Description:  "打印机注册表",
				Source:       "打印机注册表",
				UserRegistry: true,
			}
		}
		if driver, ok := drivers[strings.ToLower(strings.TrimSpace(info.DriverName))]; ok {
			item.Driver = &driver
			if driver.FilesChecked && !driver.FilesOK {
				item.DriverState = driverBroken
			} else {
				item.DriverState = driverInstalled
			}
		} else if strings.TrimSpace(info.DriverName) == "" {
			item.DriverState = driverUnknown
		} else if len(drivers) > 0 {
			item.DriverState = driverMissing
		} else {
			item.DriverState = driverUnknown
		}
		if device, ok := devices[strings.ToLower(strings.TrimSpace(info.Name))]; ok {
			item.Device = &device
		}
		item.Virtual = isVirtualPrinter(info)
		item.Network = info.Network || (item.Port != nil && strings.TrimSpace(item.Port.Host) != "")
		if item.Port != nil && strings.TrimSpace(item.Port.Host) != "" && !item.Virtual {
			item.Network = true
			item.NetworkChecked = true
			item.NetworkReachable = portReachable(ctx, item.Port.Host, item.Port.Port)
		}
		item.Tone, item.State, item.Detail = printerState(out.snapshot, item)
		if item.Virtual {
			out.virtual++
		} else {
			switch item.Tone {
			case tool.ToneSuccess:
				out.ready++
			case tool.ToneWarning:
				out.warning++
			case tool.ToneDanger:
				out.danger++
			}
			out.physical++
		}
		out.printers = append(out.printers, item)
	}
	return out
}

func portReachable(ctx context.Context, host string, port int) bool {
	if ctx.Err() != nil {
		return false
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	if port <= 0 {
		port = 9100
	}
	dialer := net.Dialer{Timeout: 700 * time.Millisecond}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func printerState(value snapshot, item printerAnalysis) (tool.Tone, string, string) {
	service := serviceLabel(value.Spooler)
	driver := driverLabel(item)
	connection := connectionLabel(item)
	if item.Virtual {
		return tool.ToneNeutral, "虚拟打印机", driver + "；" + connection
	}
	if !value.Spooler.Found {
		return tool.ToneDanger, "无法读取打印服务", service + "；" + driver + "；" + connection
	}
	if !strings.EqualFold(value.Spooler.Status, "Running") {
		return tool.ToneDanger, "打印服务未运行", service + "；" + driver + "；" + connection
	}
	if item.Info.WorkOffline || strings.Contains(strings.ToLower(item.Info.PrinterStatus), "offline") {
		return tool.ToneDanger, "打印机离线", "设备报告离线；" + driver + "；" + connection
	}
	if item.DriverState == driverMissing {
		return tool.ToneDanger, "驱动缺失", driver + "；" + connection
	}
	if item.DriverState == driverBroken {
		return tool.ToneDanger, "驱动文件缺失", driver + "；" + connection
	}
	if item.DriverState != driverInstalled {
		return tool.ToneWarning, "驱动无法验证", driver + "；" + connection
	}
	if item.NetworkChecked && item.NetworkReachable {
		return tool.ToneSuccess, "网络连接正常", driver + "；" + connection
	}
	if item.NetworkChecked && !item.NetworkReachable {
		return tool.ToneWarning, "网络不可达", driver + "；" + connection
	}
	if item.Device != nil && strings.EqualFold(item.Device.Status, "OK") {
		return tool.ToneSuccess, "设备已连接", driver + "；" + connection
	}
	if item.Port != nil {
		return tool.ToneWarning, "已配置，连接未知", driver + "；" + connection
	}
	return tool.ToneWarning, "配置不完整", driver + "；" + connection
}

func driverLabel(item printerAnalysis) string {
	name := strings.TrimSpace(item.Info.DriverName)
	if name == "" {
		name = "未登记驱动"
	}
	switch item.DriverState {
	case driverInstalled:
		if item.Driver != nil && strings.TrimSpace(item.Driver.Version) != "" {
			return "驱动已安装（" + name + "，v" + item.Driver.Version + "）"
		}
		return "驱动已安装（" + name + "）"
	case driverMissing:
		return "驱动缺失（" + name + "）"
	case driverBroken:
		return "驱动文件缺失（" + name + "）"
	default:
		return "驱动无法验证（" + name + "）"
	}
}

func connectionLabel(item printerAnalysis) string {
	if item.Virtual {
		return "虚拟设备"
	}
	if item.NetworkChecked {
		if item.NetworkReachable {
			return "TCP 端口可达"
		}
		return "TCP 端口不可达"
	}
	if item.Device != nil {
		return "PnP " + fallback(item.Device.Status, "状态未知")
	}
	if item.Info.Local {
		return "本地已配置"
	}
	return "连接状态未知"
}

func serviceLabel(value spoolerInfo) string {
	if !value.Found {
		return "打印服务未找到"
	}
	status := fallback(value.Status, "状态未知")
	switch strings.ToLower(status) {
	case "running":
		status = "运行中"
	case "stopped":
		status = "已停止"
	}
	if strings.TrimSpace(value.StartType) != "" {
		return status + "（启动类型 " + value.StartType + "）"
	}
	return status
}

func isVirtualPrinter(item printerInfo) bool {
	text := strings.ToLower(strings.Join([]string{
		item.Name,
		item.DriverName,
		item.PortName,
	}, " "))
	for _, marker := range []string{"pdf", "onenote", "fax", "xps", "send to", "microsoft print", "portprompt", "nul:", "虚拟"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func overviewBlocks(value analysis) []tool.Block {
	facts := []tool.Fact{
		{Label: "已配置打印机", Value: fmt.Sprintf("%d 台", len(value.printers))},
		{Label: "物理打印机", Value: fmt.Sprintf("%d 台", value.physical)},
		{Label: "连接正常", Value: fmt.Sprintf("%d 台", value.ready)},
		{Label: "物理需检查", Value: fmt.Sprintf("%d 台", value.warning)},
		{Label: "物理异常", Value: fmt.Sprintf("%d 台", value.danger)},
		{Label: "虚拟打印机", Value: fmt.Sprintf("%d 台", value.virtual)},
		{Label: "默认打印机", Value: fallback(value.snapshot.DefaultPrinter, "未设置")},
		{Label: "打印服务", Value: serviceLabel(value.snapshot.Spooler)},
	}
	cells := []tool.StatusCell{
		{Label: "打印服务", Tone: serviceTone(value.snapshot.Spooler), Detail: serviceLabel(value.snapshot.Spooler)},
		{Label: "驱动", Tone: driverSummaryTone(value), Detail: driverSummary(value)},
		{Label: "端口", Tone: portSummaryTone(value), Detail: portSummary(value)},
		{Label: "设备连接", Tone: connectionSummaryTone(value), Detail: connectionSummary(value)},
		{Label: "默认打印机", Tone: defaultTone(value), Detail: fallback(value.snapshot.DefaultPrinter, "未设置默认打印机")},
	}
	blocks := []tool.Block{
		factsBlock("检测概要", facts),
		statusGridBlock("关键状态", cells, []tool.StatusLegendItem{
			{Label: "正常", Tone: tool.ToneSuccess},
			{Label: "需检查", Tone: tool.ToneWarning},
			{Label: "异常", Tone: tool.ToneDanger},
			{Label: "未知/虚拟", Tone: tool.ToneNeutral},
		}),
	}
	if value.physical > 0 {
		ratio := float64(value.ready) * 100 / float64(value.physical)
		blocks = append([]tool.Block{{
			Kind:  tool.BlockMeter,
			Title: "物理打印机健康度",
			Data: map[string]any{"items": []tool.MeterItem{{
				Label:   "连接正常",
				Value:   ratio,
				Max:     100,
				Unit:    "%",
				Detail:  fmt.Sprintf("%d / %d 台", value.ready, value.physical),
				Tone:    ratioTone(ratio),
				Variant: "ring",
			}}},
		}}, blocks...)
	}
	return blocks
}

func printersBlocks(value analysis) []tool.Block {
	if len(value.printers) == 0 {
		return []tool.Block{{
			Kind:  tool.BlockNotice,
			Tone:  tool.ToneWarning,
			Title: "打印机",
			Text:  "没有从打印管理接口或用户注册表读取到已配置打印机。",
		}}
	}
	cells := make([]tool.StatusCell, 0, len(value.printers))
	facts := make([]tool.Fact, 0, len(value.printers)*5)
	for _, item := range value.printers {
		cells = append(cells, tool.StatusCell{
			Label:  item.Info.Name,
			Tone:   item.Tone,
			Detail: item.State + "；" + item.Detail,
		})
		group := item.Info.Name
		facts = append(facts,
			tool.Fact{Label: "状态", Value: item.State, Group: group},
			tool.Fact{Label: "驱动", Value: driverDetail(item), Group: group},
			tool.Fact{Label: "端口", Value: portDetail(item), Group: group},
			tool.Fact{Label: "连接", Value: connectionLabel(item), Group: group},
		)
		if item.Info.Shared || strings.TrimSpace(item.Info.ShareName) != "" {
			facts = append(facts, tool.Fact{Label: "共享", Value: fallback(item.Info.ShareName, "是"), Group: group})
		}
		if item.Info.Location != "" {
			facts = append(facts, tool.Fact{Label: "位置", Value: item.Info.Location, Group: group})
		}
	}
	return []tool.Block{
		statusGridBlock("打印机状态", cells, []tool.StatusLegendItem{
			{Label: "连接正常", Tone: tool.ToneSuccess},
			{Label: "需检查", Tone: tool.ToneWarning},
			{Label: "异常", Tone: tool.ToneDanger},
			{Label: "虚拟打印机", Tone: tool.ToneNeutral},
		}),
		factsBlock("打印机详情", facts),
	}
}

func driversBlocks(value analysis) []tool.Block {
	facts := make([]tool.Fact, 0)
	usedDrivers := make(map[string]bool)
	for _, item := range value.printers {
		if item.Driver == nil || usedDrivers[strings.ToLower(item.Driver.Name)] {
			continue
		}
		usedDrivers[strings.ToLower(item.Driver.Name)] = true
		facts = append(facts, tool.Fact{
			Label: item.Driver.Name,
			Group: "打印驱动",
			Value: joinNonEmpty(" · ", item.Driver.Manufacturer, driverVersion(item.Driver), item.Driver.Environment),
		})
	}
	usedPorts := make(map[string]bool)
	for _, item := range value.printers {
		if item.Port == nil || usedPorts[strings.ToLower(item.Port.Name)] {
			continue
		}
		usedPorts[strings.ToLower(item.Port.Name)] = true
		host := fallback(item.Port.Host, "本地端口")
		number := ""
		if item.Port.Port > 0 {
			number = " · TCP " + strconv.Itoa(item.Port.Port)
		}
		protocol := ""
		if item.Port.Protocol != "" {
			protocol = " · " + item.Port.Protocol
		}
		facts = append(facts, tool.Fact{
			Label: item.Port.Name,
			Group: "打印端口",
			Value: host + number + protocol,
		})
	}
	for _, item := range value.snapshot.Devices {
		facts = append(facts, tool.Fact{
			Label: fallback(item.Name, "未命名设备"),
			Group: "硬件设备",
			Value: joinNonEmpty(" · ", fallback(item.Status, "状态未知"), item.InstanceID),
		})
	}
	if len(facts) == 0 {
		return []tool.Block{{
			Kind:  tool.BlockNotice,
			Tone:  tool.ToneWarning,
			Title: "驱动与端口",
			Text:  "没有读取到可关联到打印机的驱动、端口或硬件设备信息。请检查打印服务状态。",
		}}
	}
	return []tool.Block{factsBlock("驱动、端口与硬件", facts)}
}

func driverDetail(item printerAnalysis) string {
	name := fallback(item.Info.DriverName, "未登记")
	if item.Driver == nil {
		return item.DriverState + " · " + name
	}
	return joinNonEmpty(" · ", item.DriverState, name, driverVersion(item.Driver), item.Driver.Environment)
}

func driverVersion(value *driverInfo) string {
	if value == nil || strings.TrimSpace(value.Version) == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(value.Version)), "v") {
		return value.Version
	}
	return "v" + value.Version
}

func portDetail(item printerAnalysis) string {
	if item.Port == nil {
		return "缺少端口 · " + fallback(item.Info.PortName, "未登记")
	}
	parts := []string{item.Port.Name}
	if item.Port.Host != "" {
		parts = append(parts, item.Port.Host)
	}
	if item.Port.Port > 0 {
		parts = append(parts, "TCP "+strconv.Itoa(item.Port.Port))
	}
	if item.Port.Protocol != "" {
		parts = append(parts, item.Port.Protocol)
	}
	return strings.Join(parts, " · ")
}

func serviceTone(value spoolerInfo) tool.Tone {
	if !value.Found || !strings.EqualFold(value.Status, "Running") {
		return tool.ToneDanger
	}
	return tool.ToneSuccess
}

func driverSummaryTone(value analysis) tool.Tone {
	physical := physicalPrinters(value)
	if len(physical) == 0 {
		return tool.ToneNeutral
	}
	unknown := false
	for _, item := range physical {
		switch item.DriverState {
		case driverMissing:
			return tool.ToneDanger
		case driverBroken:
			return tool.ToneDanger
		case driverUnknown:
			unknown = true
		}
	}
	if unknown {
		return tool.ToneWarning
	}
	return tool.ToneSuccess
}

func driverSummary(value analysis) string {
	physical := physicalPrinters(value)
	if len(physical) == 0 {
		return "没有可校验的打印机"
	}
	count := make(map[string]int)
	for _, item := range physical {
		count[item.DriverState]++
	}
	return fmt.Sprintf("已安装 %d · 缺失 %d · 文件缺失 %d · 无法验证 %d",
		count[driverInstalled], count[driverMissing], count[driverBroken], count[driverUnknown])
}

func portSummaryTone(value analysis) tool.Tone {
	if len(physicalPrinters(value)) == 0 {
		return tool.ToneNeutral
	}
	missing := 0
	known := 0
	for _, item := range physicalPrinters(value) {
		if item.Port == nil {
			missing++
		} else {
			known++
		}
	}
	if missing > 0 {
		return tool.ToneWarning
	}
	if known > 0 {
		return tool.ToneSuccess
	}
	return tool.ToneWarning
}

func portSummary(value analysis) string {
	known := 0
	physical := physicalPrinters(value)
	for _, item := range physical {
		if item.Port != nil {
			known++
		}
	}
	return fmt.Sprintf("%d 台有端口信息 · %d 台缺少端口", known, len(physical)-known)
}

func connectionSummaryTone(value analysis) tool.Tone {
	if !value.snapshot.Spooler.Found || !strings.EqualFold(value.snapshot.Spooler.Status, "Running") {
		return tool.ToneDanger
	}
	if len(physicalPrinters(value)) == 0 {
		return tool.ToneNeutral
	}
	warning := false
	for _, item := range value.printers {
		switch item.Tone {
		case tool.ToneDanger:
			return tool.ToneDanger
		case tool.ToneWarning:
			warning = true
		}
	}
	if warning {
		return tool.ToneWarning
	}
	return tool.ToneSuccess
}

func connectionSummary(value analysis) string {
	if len(physicalPrinters(value)) == 0 {
		return "没有已配置打印机"
	}
	return fmt.Sprintf("正常 %d · 需检查 %d · 异常 %d", value.ready, value.warning, value.danger)
}

func physicalPrinters(value analysis) []printerAnalysis {
	out := make([]printerAnalysis, 0, value.physical)
	for _, item := range value.printers {
		if !item.Virtual {
			out = append(out, item)
		}
	}
	return out
}

func defaultTone(value analysis) tool.Tone {
	if strings.TrimSpace(value.snapshot.DefaultPrinter) == "" {
		return tool.ToneWarning
	}
	return tool.ToneSuccess
}

func ratioTone(value float64) tool.Tone {
	switch {
	case value >= 80:
		return tool.ToneSuccess
	case value >= 50:
		return tool.ToneWarning
	default:
		return tool.ToneDanger
	}
}

func statusGridBlock(title string, cells []tool.StatusCell, legend []tool.StatusLegendItem) tool.Block {
	return tool.Block{
		Kind:  tool.BlockGrid,
		Title: title,
		Data:  map[string]any{"cells": cells, "legend": legend, "labeled": true},
	}
}

func factsBlock(title string, facts []tool.Fact) tool.Block {
	return tool.Block{
		Kind:  tool.BlockFacts,
		Title: title,
		Data:  map[string]any{"items": facts},
	}
}

func resultWarnings(value analysis) []string {
	spoolerDown := !value.snapshot.Spooler.Found ||
		!strings.EqualFold(value.snapshot.Spooler.Status, "Running")
	warnings := make([]string, 0, len(value.snapshot.Warnings)+3)
	for _, warning := range value.snapshot.Warnings {
		if spoolerDown && (strings.Contains(warning, "后台打印程序服务") ||
			strings.Contains(warning, "常规故障")) {
			continue
		}
		warnings = append(warnings, warning)
	}
	if !value.snapshot.Spooler.Found {
		warnings = append(warnings, "未找到 Windows 打印后台处理程序 Spooler。")
	} else if spoolerDown {
		warnings = append(warnings, "打印后台处理程序 Spooler 未运行；打印管理接口和部分驱动、端口信息可能不可用。")
	}
	problems := make([]string, 0)
	for _, item := range value.printers {
		if item.Virtual {
			continue
		}
		switch item.Tone {
		case tool.ToneDanger, tool.ToneWarning:
			problems = append(problems, item.Info.Name)
		}
	}
	if len(problems) > 0 {
		warnings = append(warnings, "以下打印机需要处理："+strings.Join(problems, "、")+"。")
	}
	return uniqueStrings(warnings, 8)
}

func uniqueStrings(values []string, limit int) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func fallback(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func joinNonEmpty(separator string, values ...string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			parts = append(parts, strings.TrimSpace(value))
		}
	}
	return strings.Join(parts, separator)
}

func sortPrinters(items []printerInfo) {
	sort.SliceStable(items, func(i, j int) bool {
		left, right := strings.ToLower(items[i].Name), strings.ToLower(items[j].Name)
		if left != right {
			return left < right
		}
		return items[i].Source < items[j].Source
	})
}
