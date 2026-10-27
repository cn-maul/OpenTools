package networkconfig

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

var probeDomains = []string{"www.baidu.com", "www.qq.com"}

func probeProvider(ctx context.Context, p provider, rounds int, step func()) probeResult {
	type sample struct {
		ms  float64
		err error
	}
	attempts := rounds * len(probeDomains) * min(2, len(p.Servers))
	samples := make([]sample, 0, attempts)
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 3)

	for _, server := range p.Servers {
		for _, domain := range probeDomains {
			for round := 0; round < rounds; round++ {
				if err := ctx.Err(); err != nil {
					break
				}
				wg.Add(1)
				sem <- struct{}{}
				go func(server, domain string) {
					defer wg.Done()
					defer func() { <-sem }()
					ms, err := probeOnce(ctx, server, domain, 1200*time.Millisecond)
					mu.Lock()
					samples = append(samples, sample{ms: ms, err: err})
					mu.Unlock()
					step()
				}(server, domain)
			}
		}
	}
	wg.Wait()

	result := probeResult{Provider: p, Attempts: len(samples)}
	var successful []float64
	for _, item := range samples {
		if item.err == nil {
			result.Successes++
			successful = append(successful, item.ms)
		} else if result.LastError == "" {
			result.LastError = item.err.Error()
		}
	}
	if len(successful) == 0 {
		return result
	}
	sort.Float64s(successful)
	result.Successful = true
	result.MedianMS = percentile(successful, 0.5)
	if len(successful) > 1 {
		result.JitterMS = percentile(successful, 0.9) - percentile(successful, 0.1)
	}
	return result
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	index := int(float64(len(values)-1) * p)
	return values[index]
}

func probeOnce(ctx context.Context, server, domain string, timeout time.Duration) (float64, error) {
	if strings.Contains(server, ":") {
		return 0, errors.New("当前仅使用 IPv4 DNS 地址")
	}
	name, err := dnsmessage.NewName(strings.TrimSuffix(domain, ".") + ".")
	if err != nil {
		return 0, err
	}
	id := uint16(rand.Uint32())
	message := dnsmessage.Message{
		Header: dnsmessage.Header{
			ID:               id,
			RecursionDesired: true,
			CheckingDisabled: true,
		},
		Questions: []dnsmessage.Question{{
			Name:  name,
			Type:  dnsmessage.TypeA,
			Class: dnsmessage.ClassINET,
		}},
	}
	packed, err := message.Pack()
	if err != nil {
		return 0, err
	}
	dialer := net.Dialer{Timeout: timeout}
	start := time.Now()
	conn, err := dialer.DialContext(ctx, "udp", net.JoinHostPort(server, "53"))
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(start.Add(timeout))
	if _, err := conn.Write(packed); err != nil {
		return 0, err
	}
	buffer := make([]byte, 2048)
	n, err := conn.Read(buffer)
	elapsed := float64(time.Since(start).Microseconds()) / 1000
	if err != nil {
		return elapsed, err
	}
	var response dnsmessage.Message
	if err := response.Unpack(buffer[:n]); err != nil {
		return elapsed, err
	}
	if response.Header.ID != id {
		return elapsed, errors.New("DNS 响应 ID 不匹配")
	}
	if !response.Header.Response {
		return elapsed, errors.New("DNS 响应格式错误")
	}
	if response.Header.RCode != dnsmessage.RCodeSuccess {
		return elapsed, fmt.Errorf("DNS 返回错误：%s", response.Header.RCode)
	}
	if len(response.Answers) == 0 {
		return elapsed, errors.New("DNS 响应没有答案")
	}
	for _, answer := range response.Answers {
		if answer.Header.Type == dnsmessage.TypeA || answer.Header.Type == dnsmessage.TypeAAAA {
			return elapsed, nil
		}
	}
	return elapsed, nil
}
