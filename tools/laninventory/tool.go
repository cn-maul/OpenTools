// Package laninventory discovers usable IPv4 addresses on local networks.
// It is deliberately independent from networkconfig: the two tools share the
// plugin contract, not each other's internal implementation.
package laninventory

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"opentools/pkg/tool"
)

const version = "1.3.0"

// Tool scans an IPv4 subnet and reports online and unused host addresses.
type Tool struct{}

// New returns the LAN inventory tool.
func New() Tool { return Tool{} }

// Definition describes the downloadable LAN scanner.
func (Tool) Definition() tool.Definition {
	return tool.Definition{
		ID:          "network.lan-addresses",
		Name:        "局域网可用 IP",
		Summary:     "识别当前局域网，扫描在线主机和空闲 IPv4 地址。",
		Description: "自动选择正在联网的物理网卡与私有 IPv4 网段，也可手动输入 CIDR。扫描使用 ARP 邻居表和并发 TCP 探测，只读、不修改网络配置；输出在线主机、空闲地址和 CSV 报告。",
		Category:    "网络工具",
		Icon:        "lan",
		Keywords:    []string{"局域网", "LAN", "IP", "网段", "在线主机", "空闲地址", "ARP", "扫描"},
		Version:     version,
		Effects:     []tool.Effect{tool.EffectReadFiles, tool.EffectNetwork, tool.EffectWriteFiles},
		Input: tool.Object(map[string]*tool.Schema{
			"network": tool.String("扫描范围", "留空时自动识别当前联网的主网卡与网段；也可填写 192.168.1.0/24。"),
			"mode": tool.Select("扫描模式", "快速扫描常见端口；彻底扫描更多 TCP 端口，耗时更长。", "quick", "thorough").
				WithEnumLabels(map[string]string{
					"quick":    "快速扫描",
					"thorough": "彻底扫描",
				}).
				WithDefault("quick"),
			"timeout": tool.Number("单机超时", "每个地址的探测超时时间，单位毫秒。").
				WithDefault(350).
				WithMin(100).
				WithMax(2000).
				WithVisibleWhen("mode", "quick", "thorough"),
		}),
	}
}

type params struct {
	Network string  `json:"network"`
	Mode    string  `json:"mode"`
	Timeout float64 `json:"timeout"`
}

type scanTarget struct {
	Interface string `json:"interface"`
	Address   string `json:"address"`
	Prefix    int    `json:"prefix"`
	CIDR      string `json:"cidr"`
	Network   string `json:"network"`
	Gateway   string `json:"gateway"`
	MAC       string `json:"mac"`
}

type hostResult struct {
	Address     string
	RTT         time.Duration
	Method      string
	MAC         string
	OpenPorts   []int
	Fingerprint deviceFingerprint
}

// Run implements tool.Tool.
func (Tool) Run(ctx context.Context, input tool.Input, env tool.Environment, report tool.Reporter) (tool.Result, error) {
	if runtime.GOOS != "windows" {
		return tool.Result{}, errors.New("局域网可用 IP 工具当前仅支持 Windows")
	}
	var p params
	if err := input.Decode(&p); err != nil {
		return tool.Result{}, err
	}
	if p.Timeout == 0 {
		p.Timeout = 350
	}
	if p.Mode == "" {
		p.Mode = "quick"
	}

	report.Section(tool.Section{ID: "map", Title: "地址分布", State: tool.SectionPending})
	report.Section(tool.Section{ID: "map", Title: "地址分布", State: tool.SectionRunning})
	target, err := selectTarget(ctx, strings.TrimSpace(p.Network))
	if err != nil {
		return tool.Result{}, err
	}
	report.Log("info", fmt.Sprintf("扫描网卡 %s，网段 %s", target.Interface, target.CIDR))

	addresses, err := hostAddresses(target)
	if err != nil {
		return tool.Result{}, err
	}
	if len(addresses) > 4094 {
		return tool.Result{}, fmt.Errorf("网段 %s 包含 %d 个可用地址，请缩小到 /20 或更小的 IPv4 网段", target.CIDR, len(addresses))
	}

	report.Log("info", fmt.Sprintf("开始扫描 %d 个地址（%s）", len(addresses), modeLabel(p.Mode)))
	timeout := time.Duration(p.Timeout) * time.Millisecond
	hosts := scanHosts(ctx, addresses, neighbors(ctx), p.Mode, timeout, report)

	report.Log("info", fmt.Sprintf("开始识别 %d 台在线设备的类型", len(hosts)))
	fingerprintHosts(ctx, hosts, timeout)

	sort.Slice(hosts, func(i, j int) bool {
		left, right := netip.MustParseAddr(hosts[i].Address), netip.MustParseAddr(hosts[j].Address)
		return left.Less(right)
	})
	online := make(map[string]bool, len(hosts))
	for _, host := range hosts {
		online[host.Address] = true
	}
	hostDetail := make(map[string]hostResult, len(hosts))
	for _, host := range hosts {
		hostDetail[host.Address] = host
	}
	free := make([]string, 0, len(addresses)-len(hosts))
	for _, address := range addresses {
		if !online[address.String()] {
			free = append(free, address.String())
		}
	}
	gridCells := make([]tool.StatusCell, 0, len(addresses))
	for _, address := range addresses {
		key := address.String()
		if key == strings.TrimSpace(target.Address) {
			gridCells = append(gridCells, tool.StatusCell{Label: key, Tone: tool.ToneNeutral, Category: categoryLocal, Detail: "本机地址"})
			continue
		}
		if host, ok := hostDetail[key]; ok {
			fp := host.Fingerprint
			device := fp.OSGuess
			if device == "" {
				device = "未知在线设备"
			}
			detail := fmt.Sprintf("%s，%s，%s", device, formatRTT(host.RTT), host.Method)
			if host.MAC != "" {
				detail += "，MAC " + host.MAC
			}
			if fp.Detail != "" {
				detail += "，" + fp.Detail
			}
			gridCells = append(gridCells, tool.StatusCell{Label: key, Tone: tool.ToneSuccess, Category: fp.Category, Detail: detail})
			continue
		}
		gridCells = append(gridCells, tool.StatusCell{Label: key, Tone: "", Detail: "空闲地址"})
	}
	grid := tool.Block{
		Kind:  tool.BlockGrid,
		Title: target.CIDR,
		Data: map[string]any{
			"cells": gridCells,
			"legend": []tool.StatusLegendItem{
				{Label: "Windows 主机", Category: categoryWindows},
				{Label: "Apple 设备", Category: categoryApple},
				{Label: "安卓/手机", Category: categoryPhone},
				{Label: "打印机", Category: categoryPrinter},
				{Label: "网络设备", Category: categoryNetwork},
				{Label: "Linux/NAS", Category: categoryLinux},
				{Label: "未知在线", Category: categoryUnknown},
				{Label: "本机地址", Category: categoryLocal},
				{Label: "空闲地址", Tone: ""},
			},
		},
	}
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	report.Section(tool.Section{
		ID:     "map",
		Title:  "地址分布",
		State:  tool.SectionReady,
		Blocks: []tool.Block{grid},
	})
	return tool.Result{
		Summary: fmt.Sprintf("%s：在线 %d，空闲 %d", target.CIDR, len(hosts), len(free)),
		Blocks:  []tool.Block{grid},
	}, nil
}

func selectTarget(ctx context.Context, raw string) (scanTarget, error) {
	state, err := readWindowsNetwork(ctx)
	if err != nil {
		return scanTarget{}, err
	}
	if strings.TrimSpace(raw) != "" {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil || !prefix.Addr().Is4() {
			return scanTarget{}, fmt.Errorf("扫描范围 %q 不是有效的 IPv4 CIDR，例如 192.168.1.0/24", raw)
		}
		prefix = prefix.Masked()
		best := state.Primary
		localAddress := ""
		for _, item := range state.Adapters {
			for _, address := range item.IPv4 {
				addr, parseErr := netip.ParseAddr(address.Address)
				if parseErr != nil || !addr.Is4() || !prefix.Contains(addr) {
					continue
				}
				best = item
				localAddress = addr.String()
			}
		}
		return scanTarget{
			Interface: fallback(best.Name, "手动指定"),
			Address:   localAddress,
			Prefix:    prefix.Bits(),
			CIDR:      prefix.String(),
			Network:   prefix.Masked().Addr().String(),
			Gateway:   first(best.Gateways),
			MAC:       best.MACAddress,
		}, nil
	}
	if state.Primary.Name == "" || len(state.Primary.IPv4) == 0 {
		return scanTarget{}, errors.New("未找到正在联网的物理网卡，请手动填写扫描范围")
	}
	address, err := netip.ParseAddr(firstAddress(state.Primary.IPv4))
	if err != nil || !address.Is4() {
		return scanTarget{}, errors.New("主网卡没有有效的 IPv4 地址")
	}
	prefix, err := netip.ParsePrefix(fmt.Sprintf("%s/%d", address, state.Primary.PrefixLength))
	if err != nil {
		return scanTarget{}, err
	}
	prefix = prefix.Masked()
	return scanTarget{
		Interface: state.Primary.Name,
		Address:   address.String(),
		Prefix:    prefix.Bits(),
		CIDR:      prefix.String(),
		Network:   prefix.Addr().String(),
		Gateway:   first(state.Primary.Gateways),
		MAC:       state.Primary.MACAddress,
	}, nil
}

func hostAddresses(target scanTarget) ([]netip.Addr, error) {
	prefix, err := netip.ParsePrefix(target.CIDR)
	if err != nil {
		return nil, err
	}
	if prefix.Bits() < 20 {
		return nil, fmt.Errorf("为避免扫描过多地址，扫描范围不能大于 /20（当前 %s）", target.CIDR)
	}
	if prefix.Bits() > 30 {
		return nil, fmt.Errorf("扫描范围 %s 不包含可用的主机地址，请使用 /30 或更大的 IPv4 网段", target.CIDR)
	}
	total := 1 << (32 - prefix.Bits())
	out := make([]netip.Addr, 0, max(0, total-2))
	for address := prefix.Masked().Addr().Next(); prefix.Contains(address) && address != prefix.Masked().Addr().Prev(); address = address.Next() {
		// The network and broadcast addresses are not usable hosts.
		if address == prefix.Masked().Addr() || address == lastAddr(prefix) {
			continue
		}
		out = append(out, address)
	}
	return out, nil
}

func lastAddr(prefix netip.Prefix) netip.Addr {
	bits := 32 - prefix.Bits()
	value := prefix.Masked().Addr().As4()
	number := uint32(value[0])<<24 | uint32(value[1])<<16 | uint32(value[2])<<8 | uint32(value[3])
	number |= (1 << bits) - 1
	return netip.AddrFrom4([4]byte{byte(number >> 24), byte(number >> 16), byte(number >> 8), byte(number)})
}

func (Tool) scanWorkers(addressCount int) int {
	return min(96, max(16, addressCount/4))
}

func scanHosts(ctx context.Context, addresses []netip.Addr, arp map[string]string, mode string, timeout time.Duration, report tool.Reporter) []hostResult {
	ports := []int{445, 139, 135, 80, 443, 22, 3389, 9100, 631, 62078}
	if mode == "thorough" {
		ports = append(ports, 21, 23, 53, 88, 110, 143, 389, 636, 8080, 8443, 8000, 9100, 62078)
	}
	workers := Tool{}.scanWorkers(len(addresses))
	queue := make(chan netip.Addr)
	results := make(chan hostResult)
	var total atomic.Int64
	total.Store(int64(len(addresses)))
	var done atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for address := range queue {
				if ctx.Err() != nil {
					continue
				}
				if mac, ok := arp[address.String()]; ok {
					host := hostResult{Address: address.String(), Method: "ARP 邻居", MAC: mac}
					// ARP 命中的设备同样要探测端口，否则指纹识别缺少依据。
					if probed, ok := probeHost(ctx, address, ports, timeout); ok {
						probed.MAC = mac
						probed.Method = "ARP 邻居"
						host = probed
					}
					results <- host
					done.Add(1)
					report.Progress(done.Load(), total.Load())
					continue
				}
				if host, ok := probeHost(ctx, address, ports, timeout); ok {
					results <- host
				}
				done.Add(1)
				report.Progress(done.Load(), total.Load())
			}
		}()
	}
	go func() {
		defer close(queue)
		for _, address := range addresses {
			select {
			case <-ctx.Done():
				return
			case queue <- address:
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()
	hosts := make([]hostResult, 0)
	for host := range results {
		hosts = append(hosts, host)
	}
	return hosts
}

func probeHost(ctx context.Context, address netip.Addr, ports []int, timeout time.Duration) (hostResult, bool) {
	type probe struct {
		port     int
		duration time.Duration
	}
	probes := make(chan probe, len(ports))
	open := make(chan int, len(ports))
	var wg sync.WaitGroup
	for _, port := range ports {
		wg.Add(1)
		go func(port int) {
			defer wg.Done()
			start := time.Now()
			dialer := net.Dialer{Timeout: timeout}
			conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(address.String(), fmt.Sprint(port)))
			if err != nil {
				return
			}
			_ = conn.Close()
			open <- port
			probes <- probe{port: port, duration: time.Since(start)}
		}(port)
	}
	wg.Wait()
	close(probes)
	close(open)
	openPorts := make([]int, 0, len(open))
	for port := range open {
		openPorts = append(openPorts, port)
	}
	sort.Ints(openPorts)
	best := probe{}
	for item := range probes {
		if best.port == 0 || item.duration < best.duration {
			best = item
		}
	}
	if best.port == 0 {
		return hostResult{}, false
	}
	return hostResult{
		Address:   address.String(),
		RTT:       best.duration,
		Method:    fmt.Sprintf("TCP %d", best.port),
		OpenPorts: openPorts,
	}, true
}

func formatRTT(value time.Duration) string {
	if value <= 0 {
		return "未测量"
	}
	return fmt.Sprintf("%.0f ms", float64(value.Microseconds())/1000)
}

func modeLabel(mode string) string {
	if mode == "thorough" {
		return "彻底扫描"
	}
	return "快速扫描"
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func firstAddress(values []ipAddress) string {
	if len(values) == 0 {
		return ""
	}
	return values[0].Address
}

func fallback(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
