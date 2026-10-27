package printercheck

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
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
	snapshot  snapshot
	printers  []printerAnalysis
	ignored   []string
	ready     int
	warning   int
	danger    int
	physical  int
	reachable int
}

type printerAnalysis struct {
	Info             printerInfo
	Port             *portInfo
	Driver           *driverInfo
	Device           *deviceInfo
	DriverState      string
	DriverDetail     string
	Network          bool
	NetworkChecked   bool
	NetworkReachable bool
	NetworkHost      string
	NetworkIP        string
	NeighborMAC      string
	USBPresent       bool
	AltPort          *portInfo
	AltReachable     bool
	Tone             tool.Tone
	State            string
	Detail           string
	Causes           []string
	Advice           []string
	ConnectionKind   string
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
		if isVirtualPrinter(info) {
			out.ignored = append(out.ignored, info.Name)
			continue
		}
		item := printerAnalysis{Info: info}
		if port, ok := ports[strings.ToLower(strings.TrimSpace(info.PortName))]; ok {
			item.Port = &port
		} else if strings.TrimSpace(info.PortName) != "" {
			item.Port = &portInfo{
				Name:         info.PortName,
				Description:  "打印机注册表",
				Monitor:      "打印机注册表",
				Source:       "打印机注册表",
				UserRegistry: true,
			}
		}
		item.Driver, item.DriverState, item.DriverDetail = resolveDriver(info, drivers, value.Drivers)
		if device, ok := devices[strings.ToLower(strings.TrimSpace(info.Name))]; ok {
			item.Device = &device
		}
		item.Network, item.ConnectionKind = classifyConnection(info, item.Port, item.Driver)
		// A queue bound to a local port can still be a network printer: the
		// system keeps a TCP/IP port for it. Find that port so the report
		// can name the real address and check whether it answers.
		if !item.Network {
			if alt := findNetworkPort(info, value.Ports, item.Driver); alt != nil {
				item.AltPort = alt
				host := resolvePortHost(alt)
				item.NetworkHost = host
				if ip := resolveIP(host); ip != "" {
					item.NetworkIP = ip
				}
				if host != "" {
					item.AltReachable = portReachable(ctx, host, alt.Port)
					if item.AltReachable {
						out.reachable++
					}
				}
			}
		}
		if item.Network && item.Port != nil {
			host := resolvePortHost(item.Port)
			item.NetworkHost = host
			if ip := resolveIP(host); ip != "" {
				item.NetworkIP = ip
			}
			if host != "" {
				item.NetworkChecked = true
				item.NetworkReachable = portReachable(ctx, host, item.Port.Port)
				if item.NetworkReachable {
					out.reachable++
				}
			}
		}
		item.USBPresent = usbPrinterPresent(item, value.Devices)
		if item.NetworkIP != "" {
			for _, neighbor := range value.Neighbors {
				if neighbor.IP == item.NetworkIP {
					item.NeighborMAC = neighbor.MAC
				}
			}
		}
		item.Tone, item.State, item.Detail, item.Causes, item.Advice = diagnose(out.snapshot, item, value)
		switch item.Tone {
		case tool.ToneSuccess:
			out.ready++
		case tool.ToneWarning:
			out.warning++
		case tool.ToneDanger:
			out.danger++
		}
		out.physical++
		out.printers = append(out.printers, item)
	}
	return out
}

// resolveDriver links a queue to its driver. The print management API is the
// primary source, but a stopped spooler leaves the queue without a driver
// name, so the driver registry is matched by name and by hardware id.
func resolveDriver(
	info printerInfo,
	drivers map[string]driverInfo,
	all []driverInfo,
) (*driverInfo, string, string) {
	name := strings.TrimSpace(info.DriverName)
	if name != "" {
		if driver, ok := drivers[strings.ToLower(name)]; ok {
			return &driver, driverState(driver), driverSummaryLine(driver)
		}
	}
	// A queue read from the user registry carries no driver name. The queue
	// name is usually the driver name plus a location suffix, for example
	// "Pantum MS6000NW Series 0001" for the "Pantum MS6000NW Series"
	// driver, so the longest matching driver name wins.
	if match, ok := matchDriverByName(info.Name, all); ok {
		return match, driverState(*match), driverSummaryLine(*match)
	}
	// Fall back to the only real driver on the machine.
	candidates := make([]driverInfo, 0, len(all))
	for _, driver := range all {
		if isClassDriver(driver.Name) {
			continue
		}
		candidates = append(candidates, driver)
	}
	if len(candidates) == 1 {
		copied := candidates[0]
		return &copied, driverState(copied), driverSummaryLine(copied)
	}
	// Otherwise pick the driver whose hardware id fits a USB printer.
	for _, driver := range candidates {
		if strings.Contains(strings.ToLower(driver.HardwareID), "usbprint") {
			copied := driver
			return &copied, driverState(copied), driverSummaryLine(copied)
		}
	}
	if name == "" {
		return nil, driverUnknown, "队列未登记驱动名称"
	}
	if len(all) == 0 {
		return nil, driverUnknown, "未能读取驱动列表"
	}
	return nil, driverMissing, "系统中未找到名为「" + name + "」的驱动"
}

// matchDriverByName finds the driver whose name best matches the queue
// name. The queue name is the driver name with a trailing location or
// instance suffix, so a prefix match is the reliable signal.
func matchDriverByName(queueName string, all []driverInfo) (*driverInfo, bool) {
	queue := strings.ToLower(strings.TrimSpace(queueName))
	if queue == "" {
		return nil, false
	}
	best := ""
	var matched *driverInfo
	for i := range all {
		driver := all[i]
		if isClassDriver(driver.Name) {
			continue
		}
		candidate := strings.ToLower(strings.TrimSpace(driver.Name))
		if candidate == "" {
			continue
		}
		if queue == candidate ||
			strings.HasPrefix(queue, candidate) ||
			strings.HasPrefix(candidate, queue) {
			if len(candidate) > len(best) {
				best = candidate
				copied := driver
				matched = &copied
			}
		}
	}
	return matched, matched != nil
}

func driverState(driver driverInfo) string {
	if driver.InfPath != "" && !fileExists(driver.InfPath) {
		return driverBroken
	}
	if driver.DriverFile != "" && !driverFileExists(driver) {
		return driverBroken
	}
	return driverInstalled
}

// fileExists checks an absolute path.
func fileExists(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// driverFileExists resolves a driver file, which Windows stores as a bare
// file name. The real file lives in the driver store next to the INF, so
// checking System32 alone would report every third-party driver as broken.
func driverFileExists(driver driverInfo) bool {
	name := strings.TrimSpace(driver.DriverFile)
	if name == "" {
		return true
	}
	if strings.Contains(name, `\`) || strings.Contains(name, "/") {
		return fileExists(name)
	}
	candidates := []string{
		filepath.Join(systemDirectory(), name),
		filepath.Join(systemDirectory(), "spool", "drivers", name),
	}
	if inf := strings.TrimSpace(driver.InfPath); inf != "" {
		store := filepath.Dir(inf)
		candidates = append(candidates,
			filepath.Join(store, name),
			filepath.Join(store, "x64", name),
			filepath.Join(store, "amd64", name),
			filepath.Join(store, "Amd64", name),
		)
		// Driver packages keep their payload in architecture and language
		// subdirectories, so fall back to a bounded recursive search.
		if found := findFileBelow(store, name, 3); found != "" {
			candidates = append(candidates, found)
		}
	}
	for _, candidate := range candidates {
		if fileExists(candidate) {
			return true
		}
	}
	return false
}

func systemDirectory() string {
	if root := strings.TrimSpace(os.Getenv("SystemRoot")); root != "" {
		return filepath.Join(root, "System32")
	}
	return filepath.Join(os.Getenv("WINDIR"), "System32")
}

// findFileBelow looks for a file name within a few directory levels.
func findFileBelow(root, name string, depth int) string {
	if depth < 0 {
		return ""
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(entry.Name(), name) {
			return filepath.Join(root, entry.Name())
		}
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if found := findFileBelow(filepath.Join(root, entry.Name()), name, depth-1); found != "" {
			return found
		}
	}
	return ""
}

func driverSummaryLine(driver driverInfo) string {
	parts := []string{driver.Manufacturer}
	if driver.Version != "" {
		parts = append(parts, "v"+driver.Version)
	}
	if driver.DriverDate != "" {
		parts = append(parts, driver.DriverDate)
	}
	parts = append(parts, driver.Source)
	return joinNonEmpty(" · ", parts...)
}

// classifyConnection decides how a queue reaches its printer.
func classifyConnection(info printerInfo, port *portInfo, driver *driverInfo) (bool, string) {
	if port != nil {
		switch strings.ToLower(strings.TrimSpace(port.Monitor)) {
		case "standard tcp/ip port", "wsd port", "打印管理":
			return true, "网络"
		case "usb monitor":
			return false, "USB"
		case "local port", "打印机注册表":
			return false, "本地端口"
		}
		if strings.TrimSpace(port.Host) != "" {
			return true, "网络"
		}
	}
	if info.Network {
		return true, "网络"
	}
	if driver != nil && strings.Contains(strings.ToLower(driver.HardwareID), "usbprint") {
		return false, "USB"
	}
	return false, "未知"
}

func isClassDriver(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "class driver") ||
		strings.Contains(lower, "microsoft ipp") ||
		strings.Contains(lower, "virtual print")
}

// findNetworkPort looks for a TCP/IP or WSD port that belongs to the same
// printer. Windows keeps such a port even when the queue itself was bound
// to a local port, and that port holds the printer's real address.
func findNetworkPort(info printerInfo, ports []portInfo, driver *driverInfo) *portInfo {
	queue := strings.ToLower(strings.TrimSpace(info.Name))
	driverName := strings.ToLower(strings.TrimSpace(info.DriverName))
	if driverName == "" && driver != nil {
		driverName = strings.ToLower(strings.TrimSpace(driver.Name))
	}
	var best *portInfo
	bestScore := -1
	for i := range ports {
		port := ports[i]
		monitor := strings.ToLower(strings.TrimSpace(port.Monitor))
		if monitor != "standard tcp/ip port" && monitor != "wsd port" && monitor != "打印管理" {
			continue
		}
		host := strings.ToLower(strings.TrimSpace(port.Host))
		if host == "" {
			continue
		}
		// The port name usually embeds the printer or driver name, for
		// example "IPP_Pantum-3B27D2_1".
		portName := strings.ToLower(strings.TrimSpace(port.Name))
		score := 0
		if driverName != "" && strings.Contains(portName, driverName) {
			score += 2
		}
		if queue != "" && strings.Contains(portName, queue) {
			score += 2
		}
		for _, token := range driverTokens(driverName) {
			if len(token) >= 4 && strings.Contains(portName, token) {
				score++
			}
		}
		if score > bestScore {
			bestScore = score
			copied := port
			best = &copied
		}
	}
	return best
}

// driverTokens splits a driver name into comparable fragments.
func driverTokens(driverName string) []string {
	fields := strings.FieldsFunc(strings.ToLower(driverName), func(r rune) bool {
		return r == ' ' || r == '-' || r == '_' || r == '.'
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if len(field) >= 4 {
			out = append(out, field)
		}
	}
	return out
}

// resolveIP turns a host name into an IPv4 address so the report can show
// the printer's actual address and match it against the neighbour table.
func resolveIP(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	addresses, err := net.LookupHost(host)
	if err != nil {
		return ""
	}
	for _, address := range addresses {
		parsed := net.ParseIP(address)
		if parsed == nil {
			continue
		}
		if v4 := parsed.To4(); v4 != nil {
			return v4.String()
		}
	}
	return ""
}

// usbPrinterPresent reports whether a USB printer device is currently
// attached. A driver that declares a USB hardware id does not mean the
// printer is connected over USB.
func usbPrinterPresent(item printerAnalysis, devices []deviceInfo) bool {
	for _, device := range devices {
		if strings.EqualFold(device.Class, "Printer") &&
			!strings.EqualFold(device.Status, "Unknown") &&
			!strings.Contains(device.Problem, "PHANTOM") {
			return true
		}
	}
	return false
}

// resolvePortHost turns a port target into something dialable. Windows
// stores either an IP or a host name, and the host name may need resolving.
func resolvePortHost(port *portInfo) string {
	if port == nil {
		return ""
	}
	if ip := strings.TrimSpace(port.IPAddress); ip != "" {
		return ip
	}
	host := strings.TrimSpace(port.Host)
	if host == "" {
		return ""
	}
	if net.ParseIP(host) != nil {
		return host
	}
	return host
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

// diagnose turns the raw snapshot into a verdict plus the concrete reasons
// behind it. Every cause is one thing the user can act on.
func diagnose(value snapshot, item printerAnalysis, all snapshot) (tool.Tone, string, string, []string, []string) {
	causes := make([]string, 0, 4)
	advice := make([]string, 0, 4)
	driver := driverLabel(item)

	spoolerDown := !value.Spooler.Found || !strings.EqualFold(value.Spooler.Status, "Running")
	if spoolerDown {
		causes = append(causes, "打印服务 Spooler 未运行，系统不会尝试连接打印机")
		advice = append(advice, "启动打印服务：services.msc 中启动「Print Spooler」")
	}

	switch item.DriverState {
	case driverMissing:
		causes = append(causes, "系统缺少该队列对应的打印驱动")
		advice = append(advice, "在「设备和打印机」中重新添加打印机并安装厂商驱动")
	case driverBroken:
		causes = append(causes, "驱动已注册但驱动文件缺失，驱动无法加载")
		advice = append(advice, "从厂商官网或 Windows Update 重新安装该打印机驱动")
	case driverUnknown:
		causes = append(causes, "驱动状态无法验证："+item.DriverDetail)
	}

	if item.Info.WorkOffline || strings.Contains(strings.ToLower(item.Info.PrinterStatus), "offline") {
		causes = append(causes, "打印机报告为离线状态")
		advice = append(advice, "取消「脱机使用打印机」，并确认打印机已开机联网")
	}

	// Connection diagnosis. A queue bound to a local port can never reach a
	// network printer, even when the printer itself is online.
	switch {
	case item.ConnectionKind == "网络":
		host := resolvePortHost(item.Port)
		target := host
		if item.Port != nil && item.Port.Port > 0 {
			target = net.JoinHostPort(host, strconv.Itoa(item.Port.Port))
		}
		switch {
		case item.NetworkChecked && item.NetworkReachable:
			causes = append(causes, "网络打印端口 "+target+" 可达")
		case item.NetworkChecked && !item.NetworkReachable:
			causes = append(causes, "网络打印端口 "+target+" 无法建立连接")
			if ip := item.Port.IPAddress; ip != "" {
				advice = append(advice, "确认打印机 IP 未变更，或在打印机端口设置中更新 IP")
			}
			advice = append(advice, "确认本机与打印机处于同一网段，且防火墙未拦截打印端口")
		default:
			causes = append(causes, "网络打印端口未配置可用的主机地址")
			advice = append(advice, "在打印机端口设置中填写打印机 IP 地址")
		}
	case item.ConnectionKind == "本地端口":
		// The queue points at a local port. If the machine also knows a
		// network port for this printer, the printer is a network device
		// that was never re-bound, which is the usual cause.
		if item.AltPort != nil {
			target := item.NetworkIP
			if target == "" {
				target = item.NetworkHost
			}
			port := item.AltPort.Port
			if port <= 0 {
				port = 9100
			}
			if item.AltReachable {
				causes = append(causes, "队列绑定在本地端口（"+item.Info.PortName+
					"），但系统中的网络端口 "+net.JoinHostPort(target, strconv.Itoa(port))+
					" 可以连通")
				advice = append(advice, "打印队列端口配置错误：将其改为 TCP/IP 端口并指向 "+
					target)
			} else {
				causes = append(causes, "队列绑定在本地端口（"+item.Info.PortName+
					"），网络端口 "+net.JoinHostPort(target, strconv.Itoa(port))+" 无法连通")
				advice = append(advice, "确认打印机已开机并与本机处于同一网段，然后把队列端口改为 TCP/IP 端口 "+
					target)
			}
		} else if item.Driver != nil &&
			strings.Contains(strings.ToLower(item.Driver.HardwareID), "usbprint") {
			causes = append(causes, "队列绑定在本地端口，驱动声明为 USB 设备")
			advice = append(advice, "重新添加打印机并选择正确的 USB 端口")
		} else {
			causes = append(causes, "队列绑定在本地端口（"+item.Info.PortName+"），无法通过网络寻址")
			advice = append(advice, "若打印机为网络机型，请改用 TCP/IP 端口并填写打印机 IP")
		}
	}

	if item.Device != nil {
		problem := strings.TrimSpace(item.Device.Problem)
		if problem != "" && problem != "0" && !strings.EqualFold(problem, "CM_PROB_PHANTOM") {
			causes = append(causes, "设备状态异常："+problem)
			advice = append(advice, "在设备管理器中检查该打印机设备并重新扫描硬件改动")
		}
	}

	if item.Info.QueuedJobs > 0 {
		causes = append(causes, fmt.Sprintf("队列中有 %d 个待处理任务（%s）", item.Info.QueuedJobs, item.Info.JobSummary))
		advice = append(advice, "在打印队列中清空卡住的任务后重试")
	}

	// Verdict.
	tone := tool.ToneSuccess
	state := "连接正常"
	switch {
	case len(causes) == 0:
		causes = append(causes, "未发现异常")
	case spoolerDown:
		tone = tool.ToneDanger
		state = "打印服务未运行"
	case item.DriverState == driverMissing || item.DriverState == driverBroken:
		tone = tool.ToneDanger
		state = driverStateLabel(item.DriverState)
	case item.Info.WorkOffline || strings.Contains(strings.ToLower(item.Info.PrinterStatus), "offline"):
		tone = tool.ToneDanger
		state = "打印机离线"
	case item.NetworkChecked && !item.NetworkReachable:
		tone = tool.ToneDanger
		state = "网络不可达"
	case item.ConnectionKind == "本地端口":
		if item.AltPort != nil {
			tone = tool.ToneDanger
			state = "打印队列端口配置错误"
		} else {
			tone = tool.ToneWarning
			state = "端口配置需调整"
		}
	case item.DriverState == driverUnknown:
		tone = tool.ToneWarning
		state = "驱动无法验证"
	case len(causes) > 0:
		tone = tool.ToneWarning
		state = "需检查"
	}

	detail := driver + "；" + connectionLabel(item)
	if item.NetworkIP != "" {
		detail += "；" + item.NetworkIP
	}
	return tone, state, detail, causes, advice
}

func driverStateLabel(state string) string {
	switch state {
	case driverMissing:
		return "驱动缺失"
	case driverBroken:
		return "驱动文件缺失"
	default:
		return state
	}
}

func driverLabel(item printerAnalysis) string {
	name := strings.TrimSpace(item.Info.DriverName)
	if name == "" && item.Driver != nil {
		name = item.Driver.Name
	}
	if name == "" {
		name = "未登记驱动"
	}
	switch item.DriverState {
	case driverInstalled:
		parts := []string{"驱动已安装"}
		if item.Driver != nil {
			if strings.TrimSpace(item.Driver.Version) != "" {
				parts = append(parts, "v"+item.Driver.Version)
			}
			if strings.TrimSpace(item.Driver.Manufacturer) != "" {
				parts = append(parts, item.Driver.Manufacturer)
			}
		}
		return strings.Join(parts, " ") + "（" + name + "）"
	case driverMissing:
		return "驱动缺失（" + name + "）"
	case driverBroken:
		return "驱动文件缺失（" + name + "）"
	default:
		if item.DriverDetail != "" {
			return "驱动无法验证（" + name + "，" + item.DriverDetail + "）"
		}
		return "驱动无法验证（" + name + "）"
	}
}

func connectionLabel(item printerAnalysis) string {
	switch item.ConnectionKind {
	case "网络":
		if item.NetworkChecked {
			if item.NetworkReachable {
				return "TCP 端口可达"
			}
			return "TCP 端口不可达"
		}
		return "网络地址未验证"
	case "USB":
		return "USB 连接"
	case "本地端口":
		return "本地端口"
	}
	if item.Device != nil {
		return "PnP " + fallback(item.Device.Status, "状态未知")
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
	for _, marker := range []string{
		"print to pdf",
		"onenote",
		"microsoft print to",
		"microsoft xps",
		"fax",
		"send to",
		"portprompt",
		"nul:",
		"虚拟打印机",
		"virtual printer",
		"local port",
		"nul",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	// A queue with no driver and no device is a leftover software queue
	// rather than a physical printer.
	if strings.TrimSpace(item.DriverName) == "" && !item.Network && !item.Local {
		return true
	}
	return false
}

func overviewBlocks(value analysis) []tool.Block {
	printerFacts := []tool.Fact{
		{Label: "真实打印机", Value: fmt.Sprintf("%d 台", value.physical)},
		{Label: "连接正常", Value: fmt.Sprintf("%d 台", value.ready)},
		{Label: "需检查", Value: fmt.Sprintf("%d 台", value.warning)},
		{Label: "异常", Value: fmt.Sprintf("%d 台", value.danger)},
		{Label: "网络可达", Value: fmt.Sprintf("%d 台", value.reachable)},
	}
	systemFacts := []tool.Fact{
		{Label: "默认打印机", Value: fallback(value.snapshot.DefaultPrinter, "未设置")},
		{Label: "打印服务", Value: serviceLabel(value.snapshot.Spooler)},
	}
	for i := range printerFacts {
		printerFacts[i].Group = "打印机"
	}
	for i := range systemFacts {
		systemFacts[i].Group = "系统"
	}
	facts := append(printerFacts, systemFacts...)
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
			{Label: "未知", Tone: tool.ToneNeutral},
		}),
	}
	if value.physical > 0 {
		ratio := float64(value.ready) * 100 / float64(value.physical)
		blocks = append([]tool.Block{{
			Kind:  tool.BlockMeter,
			Title: "打印机健康度",
			Data: map[string]any{"items": []tool.MeterItem{{
				Label:   "连接正常",
				Value:   ratio,
				Max:     100,
				Unit:    "%",
				Detail:  fmt.Sprintf("%d / %d 台", value.ready, value.physical),
				Tone:    ratioTone(ratio),
				Variant: "bar",
			}}},
		}}, blocks...)
	}
	return blocks
}

func printersBlocks(value analysis) []tool.Block {
	if len(value.printers) == 0 {
		text := "没有检测到已安装的实体打印机。"
		if len(value.ignored) > 0 {
			text += "已忽略 " + fmt.Sprintf("%d", len(value.ignored)) + " 个虚拟打印机（" +
				strings.Join(value.ignored, "、") + "）。"
		}
		return []tool.Block{{
			Kind:  tool.BlockNotice,
			Tone:  tool.ToneWarning,
			Title: "打印机",
			Text:  text,
		}}
	}
	cells := make([]tool.StatusCell, 0, len(value.printers))
	facts := make([]tool.Fact, 0, len(value.printers)*6)
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
			tool.Fact{Label: "连接方式", Value: item.ConnectionKind, Group: group},
		)
		if label := connectionLabel(item); label != item.ConnectionKind {
			facts = append(facts, tool.Fact{Label: "连接", Value: label, Group: group})
		}
		if item.NetworkIP != "" {
			facts = append(facts, tool.Fact{
				Label: "网络地址",
				Value: joinNonEmpty(" · ", item.NetworkIP, item.NetworkHost),
				Group: group,
			})
		}
		if item.AltPort != nil {
			facts = append(facts, tool.Fact{
				Label: "系统网络端口",
				Value: joinNonEmpty(" · ", item.AltPort.Name, item.NetworkIP),
				Group: group,
			})
		}
		if item.NeighborMAC != "" {
			facts = append(facts, tool.Fact{
				Label: "网卡 MAC",
				Value: item.NeighborMAC,
				Group: group,
			})
		}
		if item.Info.Location != "" {
			facts = append(facts, tool.Fact{Label: "位置", Value: item.Info.Location, Group: group})
		}
		if item.Info.Shared || strings.TrimSpace(item.Info.ShareName) != "" {
			facts = append(facts, tool.Fact{Label: "共享", Value: fallback(item.Info.ShareName, "是"), Group: group})
		}
		if item.Info.QueuedJobs > 0 {
			facts = append(facts, tool.Fact{
				Label: "待处理任务",
				Value: fmt.Sprintf("%d 个（%s）", item.Info.QueuedJobs, item.Info.JobSummary),
				Group: group,
			})
		}
	}
	blocks := []tool.Block{
		statusGridBlock("打印机状态", cells, []tool.StatusLegendItem{
			{Label: "连接正常", Tone: tool.ToneSuccess},
			{Label: "需检查", Tone: tool.ToneWarning},
			{Label: "异常", Tone: tool.ToneDanger},
		}),
		factsBlock("打印机详情", facts),
	}
	// The diagnosis is the part the user actually needs when a printer fails.
	causes := make([]tool.Fact, 0)
	advice := make([]tool.Fact, 0)
	for _, item := range value.printers {
		for _, cause := range item.Causes {
			causes = append(causes, tool.Fact{Label: "原因", Value: cause, Group: item.Info.Name})
		}
		for _, hint := range item.Advice {
			advice = append(advice, tool.Fact{Label: "建议", Value: hint, Group: item.Info.Name})
		}
	}
	if len(causes) > 0 {
		blocks = append(blocks, factsBlock("问题原因", causes))
	}
	if len(advice) > 0 {
		blocks = append(blocks, factsBlock("处理建议", advice))
	}
	return blocks
}

func driversBlocks(value analysis) []tool.Block {
	facts := make([]tool.Fact, 0)
	usedDrivers := make(map[string]bool)
	for _, item := range value.printers {
		if item.Driver == nil {
			continue
		}
		key := strings.ToLower(item.Driver.Name)
		if usedDrivers[key] {
			continue
		}
		usedDrivers[key] = true
		facts = append(facts, tool.Fact{
			Label: item.Driver.Name,
			Group: "打印驱动",
			Value: driverSummaryLine(*item.Driver),
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
		if !isPrintQueueDevice(item) {
			continue
		}
		if isVirtualDevice(item) {
			continue
		}
		facts = append(facts, tool.Fact{
			Label: fallback(item.Name, "未命名设备"),
			Group: "硬件设备",
			Value: joinNonEmpty(" · ", deviceStatus(item), deviceClass(item)),
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

// isPrintQueueDevice keeps the printer queues and drops unrelated devices
// such as the phantom root queue.
func isPrintQueueDevice(item deviceInfo) bool {
	if strings.EqualFold(item.Class, "Printer") {
		return true
	}
	return strings.EqualFold(item.Class, "PrintQueue") &&
		!strings.Contains(strings.ToLower(item.Name), "根打印队列")
}

// isVirtualDevice drops the software-only printers so the hardware list
// only shows queues that map to a physical device.
func isVirtualDevice(item deviceInfo) bool {
	text := strings.ToLower(item.Name + " " + item.InstanceID)
	for _, marker := range []string{
		"print to pdf",
		"onenote",
		"microsoft print to",
		"microsoft xps",
		"fax",
		"send to",
		"portprompt",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// deviceClass names the PnP device role. The instance id itself looks like
// "SWD\PRINTENUM\{GUID}" and carries nothing the user can act on.
func deviceClass(item deviceInfo) string {
	if strings.EqualFold(item.Class, "PrintQueue") {
		return "打印队列"
	}
	return "打印机设备"
}

// deviceStatus turns the PnP status into readable Chinese. A stopped spooler
// makes every queue report "Unknown", which tells the user nothing.
func deviceStatus(item deviceInfo) string {
	status := strings.TrimSpace(item.Status)
	switch strings.ToLower(status) {
	case "":
		return "状态未知"
	case "unknown":
		return "状态未知"
	case "ok":
		return "正常"
	case "error":
		return "异常"
	case "degraded":
		return "性能下降"
	case "warning":
		return "有警告"
	}
	return status
}
func driverDetail(item printerAnalysis) string {
	name := fallback(item.Info.DriverName, "未登记")
	if item.Driver != nil {
		name = item.Driver.Name
	}
	parts := []string{item.DriverState, name}
	if item.Driver != nil {
		parts = append(parts, driverVersion(item.Driver))
		if item.Driver.Manufacturer != "" {
			parts = append(parts, item.Driver.Manufacturer)
		}
	}
	return joinNonEmpty(" · ", parts...)
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
	return value.printers
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
		warnings = append(warnings, "未找到 Windows 打印后台处理程序 Spooler，打印功能不可用。")
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
