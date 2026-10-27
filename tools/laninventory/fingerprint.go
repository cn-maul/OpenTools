package laninventory

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Device categories drive the shared status-grid colors.
const (
	categoryUnknown = "unknown"
	categoryWindows = "windows"
	categoryApple   = "apple"
	categoryPrinter = "printer"
	categoryNetwork = "network"
	categoryPhone   = "phone"
	categoryLinux   = "linux"
	categoryLocal   = "local"
)

// ouiPrefixes maps MAC prefixes (lowercase, no separators) to a vendor hint.
// The table intentionally covers common consumer/office vendors only; an
// unknown MAC must not block port- and HTTP-based classification.
var ouiPrefixes = map[string]string{
	// Apple
	"f01898": "Apple", "acde48": "Apple", "a4d18c": "Apple", "d0e140": "Apple",
	"80e650": "Apple", "3c15c2": "Apple", "f0f61c": "Apple", "60f81d": "Apple",
	"0021e9": "Apple", "0026b0": "Apple", "044246": "Apple", "2cf05d": "Apple",
	// Microsoft (Surface and phones)
	"00155d": "Microsoft", "002248": "Microsoft", "00264a": "Microsoft", "7c1e52": "Microsoft",
	// Popular Android vendors
	"8c34fd": "Huawei", "781d2a": "Huawei", "04332c": "Huawei", "346b46": "Huawei",
	"64a651": "Huawei", "98e7f5": "Huawei", "001ff2": "Huawei", "00259b": "Huawei",
	"74ac5f": "Xiaomi", "7811dc": "Xiaomi", "64cc2e": "Xiaomi", "f40b93": "Xiaomi",
	"14f6d8": "Xiaomi", "34ce00": "Xiaomi", "286b35": "Xiaomi", "64b310": "Xiaomi",
	"84d111": "OPPO", "d83ab9": "OPPO", "0c3e9f": "OPPO", "404e36": "OPPO",
	"3c2ef0": "vivo", "b4e1c4": "vivo", "38a418": "vivo", "c08e14": "vivo",
	"9cd21e": "Samsung", "c4a376": "Samsung", "5085a2": "Samsung", "88329b": "Samsung",
	"2c5a29": "OnePlus", "bc6cec": "OnePlus", "5cf938": "OnePlus", "a45e60": "OnePlus",
	"e8abfa": "Asus", "04d4c4": "Asus", "04d9f5": "Asus",
	"001e64": "Lenovo", "54e1ad": "Lenovo", "8c1645": "Lenovo", "60d446": "ZTE",
	"f8a45f": "ZTE", "002559": "ZTE", "9c2a70": "Realme",
	"8866a5": "Honor", "18e29f": "Meizu", "fc5cfc": "Meizu",
	// Router / network equipment
	"c83a35": "TP-Link", "50c7bf": "TP-Link", "1cfa68": "TP-Link", "1458d0": "TP-Link",
	"0031f4": "TP-Link", "ec086b": "TP-Link", "60e727": "TP-Link", "f4f26d": "TP-Link",
	"94d9b3": "Xiaomi Router", "28d127": "Xiaomi Router", "508719": "Xiaomi Router",
	"0022b0": "Xiaomi Router", "186af6": "Xiaomi Router",
	"e8cd2d": "Huawei Router", "4846fb": "Huawei Router", "1c1d67": "Huawei Router",
	"34680e": "Huawei Router", "78f5fd": "Huawei Router",
	"843838": "ASUS Router", "ac9e17": "ASUS Router", "bcee7b": "ASUS Router",
	"001dd9": "D-Link", "1cb72c": "D-Link", "0024b2": "D-Link", "2cfda1": "D-Link",
	"000f66": "Netgear", "a00460": "Netgear", "20e52a": "Netgear", "b0b98a": "Netgear", "9c3dcf": "Netgear",
	"640980": "H3C", "d46a6a": "H3C",
	"001a2b": "Cisco-Linksys", "002172": "Cisco", "000a8a": "Cisco", "58971e": "Cisco",
	"002185": "Cisco", "e02f6d": "Cisco",
	"b827eb": "Raspberry Pi", "dc7054": "Raspberry Pi", "e45f01": "Raspberry Pi", "28cdc1": "Raspberry Pi",
	"002372": "Raspberry Pi",
	"001132": "Synology", "90197e": "Synology", "000c29": "VMware", "001c14": "VMware",
	"005056": "VMware", "080027": "VirtualBox", "525400": "QEMU/KVM", "00163e": "Xen",
	// Printers
	"00806a": "HP Printer", "002481": "HP Printer", "9cb654": "HP Printer", "d4c9ef": "HP Printer",
	"000379": "Ricoh Printer", "002673": "Epson Printer", "5074f2": "Epson Printer", "80ce62": "Epson Printer",
	"0011d8": "Canon Printer", "0060b0": "Canon Printer", "b4ba9d": "Canon Printer", "34e6d7": "Canon Printer",
	"0025ae": "Brother Printer", "3c2af4": "Brother Printer", "30cd9d": "Brother Printer", "8c332b": "Kyocera",
	"0010a4": "Samsung Printer", "00e04c": "Zebra Printer", "080019": "OKI",
	// IoT / misc
	"001a11": "Google", "54eadd": "Google", "b4e62d": "Google", "f4f5e8": "Google Home",
	"0015f2": "Amazon", "74c63b": "Amazon Echo", "441ca1": "Amazon Echo", "fc65de": "Amazon Echo",
	"00126f": "Espressif ESP", "5ccf7f": "Espressif ESP", "24dfe2": "Espressif ESP", "bcddc2": "Espressif ESP",
	"ecfabc": "Espressif ESP", "84f3eb": "Espressif ESP",
	"98dab4": "Tuya IoT", "d8f15b": "Tuya IoT", "7cf666": "Tuya IoT", "10521c": "Tuya IoT",
	"50ec50": "Hikvision", "4c86ee": "Hikvision", "c0672c": "Hikvision", "44944d": "Dahua",
	"e0508b": "Dahua", "90ea5f": "Hikvision", "bcadf8": "Hikvision",
}

// fingerprintPorts extends the scan port list with identification-oriented
// ports; they are probed the same way as availability ports.
var fingerprintHTTPPorts = []int{80, 8080, 443, 8443}

type deviceFingerprint struct {
	Category string
	Vendor   string
	OSGuess  string
	Detail   string
}

func classifyHost(host hostResult, openPorts []int, httpServer, htmlHead string) deviceFingerprint {
	vendor := ""
	macKey := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(host.MAC, ":", ""), "-", ""))
	if len(macKey) >= 6 {
		vendor = ouiPrefixes[macKey[:6]]
	}
	portSet := map[int]bool{}
	for _, port := range openPorts {
		portSet[port] = true
	}
	fp := deviceFingerprint{Vendor: vendor, Category: categoryUnknown}
	switch {
	case portSet[9100] || portSet[631] || portSet[515]:
		fp.Category = categoryPrinter
		fp.OSGuess = "打印机"
	case portSet[445] && (portSet[3389] || portSet[139]):
		fp.Category = categoryWindows
		fp.OSGuess = "Windows 主机"
	case portSet[62078]:
		fp.Category = categoryApple
		fp.OSGuess = "iPhone / iPad"
	case portSet[548]:
		fp.Category = categoryApple
		fp.OSGuess = "macOS 主机"
	case portSet[53] || portSet[161] || portSet[179]:
		fp.Category = categoryNetwork
		fp.OSGuess = "网络设备（路由器/交换机）"
	case portSet[5000] || portSet[5001] || portSet[445]:
		fp.Category = categoryLinux
		fp.OSGuess = "NAS / 文件服务"
	case portSet[3389]:
		fp.Category = categoryWindows
		fp.OSGuess = "Windows 远程桌面"
	}
	if fp.Category == categoryUnknown && vendor != "" {
		fp.Category = vendorCategory(vendor)
	}
	if fp.OSGuess == "" {
		switch {
		case strings.Contains(httpServer, "IIS"):
			fp.Category = categoryWindows
			fp.OSGuess = "Windows（IIS）"
		case strings.Contains(httpServer, "HP-Print"), strings.Contains(httpServer, "Epson"),
			strings.Contains(httpServer, "Canon"), strings.Contains(httpServer, "Brother"),
			strings.Contains(httpServer, "Ricoh"):
			fp.Category = categoryPrinter
			fp.OSGuess = "打印机"
		case strings.Contains(httpServer, "lighttpd"), strings.Contains(httpServer, "Boa"),
			strings.Contains(httpServer, "uhttpd"), strings.Contains(httpServer, "Allegro"):
			fp.Category = categoryNetwork
			fp.OSGuess = "嵌入式 Web 管理（路由器/摄像头）"
		case strings.Contains(httpServer, "nginx"), strings.Contains(httpServer, "Apache"),
			strings.Contains(httpServer, "caddy"):
			fp.OSGuess = "Web 服务（" + strings.TrimSpace(httpServer) + "）"
			if fp.Category == categoryUnknown {
				fp.Category = categoryLinux
			}
		}
	}
	if fp.OSGuess == "" && htmlHead != "" {
		lower := strings.ToLower(htmlHead)
		switch {
		case strings.Contains(lower, "product.js"), strings.Contains(lower, "wemo"),
			strings.Contains(lower, "belkin"):
			fp.Category = categoryNetwork
			fp.OSGuess = "智能设备（Belkin/嵌入式 Web）"
		case strings.Contains(lower, "huawei"), strings.Contains(lower, "hilink"):
			fp.Category = categoryNetwork
			fp.OSGuess = "华为设备 Web 管理"
		case strings.Contains(lower, "h3c"), strings.Contains(lower, "tp-link"),
			strings.Contains(lower, "zte"), strings.Contains(lower, "ip camera"),
			strings.Contains(lower, "webext"), strings.Contains(lower, "cgi-bin/luci"):
			fp.Category = categoryNetwork
			if strings.Contains(lower, "cgi-bin/luci") {
				fp.OSGuess = "路由器（LuCI 管理）"
			} else {
				fp.OSGuess = "网络设备 Web 管理"
			}
		case strings.Contains(httpServer, "bsa"), strings.Contains(lower, "bsa/"):
			fp.Category = categoryNetwork
			fp.OSGuess = "嵌入式设备（bsa）"
		}
	}
	if fp.Category == categoryUnknown && vendor != "" {
		fp.OSGuess = vendor + " 设备"
	} else if fp.Category == categoryUnknown {
		fp.OSGuess = "未知在线设备"
	}
	var hints []string
	if vendor != "" {
		hints = append(hints, vendor)
	}
	if len(openPorts) > 0 {
		ports := make([]string, 0, len(openPorts))
		for _, port := range openPorts {
			ports = append(ports, fmt.Sprint(port))
		}
		hints = append(hints, "端口 "+strings.Join(ports, "/"))
	}
	if httpServer != "" {
		hints = append(hints, "Server: "+httpServer)
	}
	fp.Detail = strings.Join(hints, "，")
	return fp
}

// vendorCategory maps a vendor hint to a coarse category. Vendor strings not
// listed here leave classification to ports/HTTP.
func vendorCategory(vendor string) string {
	switch {
	case strings.Contains(vendor, "Apple"):
		return categoryApple
	case strings.Contains(vendor, "Printer"), strings.Contains(vendor, "Ricoh"),
		strings.Contains(vendor, "Kyocera"), strings.Contains(vendor, "Zebra"), strings.Contains(vendor, "OKI"):
		return categoryPrinter
	case strings.Contains(vendor, "Router"), strings.Contains(vendor, "TP-Link"),
		strings.Contains(vendor, "D-Link"), strings.Contains(vendor, "Netgear"),
		strings.Contains(vendor, "Cisco"), strings.Contains(vendor, "H3C"):
		return categoryNetwork
	case strings.Contains(vendor, "Synology"):
		return categoryLinux
	case strings.Contains(vendor, "Huawei"), strings.Contains(vendor, "Xiaomi"),
		strings.Contains(vendor, "OPPO"), strings.Contains(vendor, "vivo"),
		strings.Contains(vendor, "Samsung"), strings.Contains(vendor, "OnePlus"),
		strings.Contains(vendor, "ZTE"), strings.Contains(vendor, "Realme"),
		strings.Contains(vendor, "Honor"), strings.Contains(vendor, "Meizu"), strings.Contains(vendor, "Lenovo"):
		return categoryPhone
	case strings.Contains(vendor, "Espressif"), strings.Contains(vendor, "Tuya"),
		strings.Contains(vendor, "Hikvision"), strings.Contains(vendor, "Dahua"),
		strings.Contains(vendor, "Google"):
		return categoryNetwork
	default:
		return categoryUnknown
	}
}

// probeHTTPBanner issues one GET to an open HTTP port and returns the Server
// header. It never follows redirects and always closes the body immediately.
func probeHTTPBanner(ctx context.Context, address string, port int, timeout time.Duration) (string, string) {
	scheme := "http"
	if port == 443 || port == 8443 {
		scheme = "https"
	}
	url := fmt.Sprintf("%s://%s/", scheme, net.JoinHostPort(address, fmt.Sprint(port)))
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", ""
	}
	req.Header.Set("User-Agent", "OpenTools-LAN-Scanner/1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()
	head, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return strings.TrimSpace(resp.Header.Get("Server")), string(head)
}

// fingerprintHosts enriches every online host with a device fingerprint using
// the open ports recorded during the scan plus one lightweight HTTP probe.
func fingerprintHosts(ctx context.Context, hosts []hostResult, timeout time.Duration) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 32)
	for i := range hosts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ports := hosts[i].OpenPorts
			server, htmlHead := "", ""
			if len(ports) > 0 {
				for _, port := range fingerprintHTTPPorts {
					if !containsPort(ports, port) {
						continue
					}
					server, htmlHead = probeHTTPBanner(ctx, hosts[i].Address, port, timeout)
					if server != "" || htmlHead != "" {
						break
					}
				}
			}
			// 没有开放端口也按 MAC 厂商分类，而不是留在未知。
			hosts[i].Fingerprint = classifyHost(hosts[i], ports, server, htmlHead)
		}(i)
	}
	wg.Wait()
}

func containsPort(ports []int, target int) bool {
	for _, port := range ports {
		if port == target {
			return true
		}
	}
	return false
}
