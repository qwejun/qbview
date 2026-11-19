package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// qB WebUI 端口不写死：既能自动找，也允许用户在设置面板里手填。
// 地址始终锁在 127.0.0.1 —— 只放开端口，不让窗口变成能连外网的通用 qB 客户端。
const defaultQBPort = 8899

// 常见端口先试一遍，命中率极高，能避免每次启动都做全范围扫描。
// linuxserver/qbittorrent 默认 8899，官方默认 8080，各类整合包常用 8085/18080/9090。
var commonQBPorts = []int{
	8899, 8080, 8085, 8081, 8082, 8083, 8084, 8086, 8087, 8088, 8089, 8090,
	18080, 9090, 8888, 8000, 9000, 10000, 9999, 808, 888, 6880, 6881, 6767,
}

// probeHTTP 只服务端口探测：短超时、不走系统代理、不留 keep-alive。
// NAS 上若有 http_proxy 环境变量，普通 http.Client 会把 127.0.0.1 的请求塞进代理，
// 结果全部误判，所以这里必须 Proxy:nil。
var probeHTTP = &http.Client{
	Transport: &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 250 * time.Millisecond, KeepAlive: 0}).DialContext,
		DisableKeepAlives:     true,
		MaxIdleConnsPerHost:   4,
		ResponseHeaderTimeout: 900 * time.Millisecond,
	},
	Timeout: 1500 * time.Millisecond,
}

func urlForPort(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

// confirmQBAPI 用一次必然失败的匿名登录确认这个端口真的是 qB 的 Web API。
//
// 真 qBittorrent 对 /api/v2/auth/login 只有三种反应：
//   200 + "Fails."（账密错）、200 + SID cookie（开启 localhost 免认证）、403（IP 被封禁）。
// 而 UGOS 网关 / 反向代理 / 登录页伪装通常是 200 空 body、200 一整页 HTML、
// 404 或 302 到自己的登录页 —— 全部剔除。
//
// 只在前两道指纹通过后才发这一个请求，且只在自动探测或启动自检时发，
// 不会在每次轮询里累积失败次数去触发 qB 的封禁阈值。
func confirmQBAPI(ctx context.Context, port int) bool {
	base := urlForPort(port)
	form := url.Values{}
	form.Set("username", "__qbview_probe__")
	form.Set("password", "__qbview_probe__")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+"/api/v2/auth/login", strings.NewReader(form.Encode()))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", base+"/")
	req.Header.Set("Origin", base)

	resp, err := probeHTTP.Do(req)
	if err != nil {
		return false
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	resp.Body.Close()

	for _, ck := range resp.Cookies() {
		if ck.Name == "SID" && ck.Value != "" {
			return true
		}
	}
	if resp.StatusCode == http.StatusForbidden {
		return true
	}
	if resp.StatusCode != http.StatusOK {
		return false
	}
	s := strings.TrimSpace(string(body))
	return strings.EqualFold(s, "Fails.") || strings.EqualFold(s, "Ok.")
}

// isQBAt 判断某个端口上是不是 qBittorrent WebUI。三道校验缺一不可：
//  1. GET / 返回 200 且页面含 "qBittorrent"（登录页 description）。
//  2. /api/v2/app/version 未登录回 403，或回纯版本号；404/302/整页 HTML 直接排除。
//  3. /api/v2/auth/login 返回 qB 特有响应（见 confirmQBAPI）。
//
// 第 3 道是 1.0.3 补的：UGOS 网关、NAS 自带服务、各类反代对未知 /api/v2/ 路径
// 普遍回 403，只靠前两道会把 8080 这类泛用端口误判成 qB，
// 用户拿真账号去登录就卡在「登录未返回 SID」。
func isQBAt(ctx context.Context, port int) bool {
	base := urlForPort(port)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/", nil)
	if err != nil {
		return false
	}
	resp, err := probeHTTP.Do(req)
	if err != nil {
		return false
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK ||
		!strings.Contains(strings.ToLower(string(body)), "qbittorrent") {
		return false
	}

	req2, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v2/app/version", nil)
	if err != nil {
		return false
	}
	resp2, err := probeHTTP.Do(req2)
	if err != nil {
		return false
	}
	b2, _ := io.ReadAll(io.LimitReader(resp2.Body, 128))
	resp2.Body.Close()
	switch resp2.StatusCode {
	case http.StatusForbidden:
		// 可能是 qB，也可能只是网关的通用 403，交给第三道裁决。
	case http.StatusOK:
		s := strings.TrimSpace(string(b2))
		// 版本号形如 4.6.7 / v5.0.0；HTML 里带 '<' 的一律不算
		if !(len(s) < 32 && !strings.Contains(s, "<") && strings.ContainsAny(s, "0123456789")) {
			return false
		}
	default:
		return false
	}
	return confirmQBAPI(ctx, port)
}

// portRank 给命中端口排优先级：常见端口表里靠前的（专属 qB 端口）优先，
// 不按数字大小排，免得 8080 这种泛用端口抢在 8899 前面。
func portRank(p int) int {
	for i, c := range commonQBPorts {
		if p == c {
			return i
		}
	}
	return len(commonQBPorts)
}

// sortByPriority 按 qB 专属程度排序，同权重再按端口号。
func sortByPriority(ports []int) {
	sort.Slice(ports, func(i, j int) bool {
		ri, rj := portRank(ports[i]), portRank(ports[j])
		if ri != rj {
			return ri < rj
		}
		return ports[i] < ports[j]
	})
}

// listeningPorts 从 /proc/net/tcp* 读本机 LISTEN 端口。
// 这是零噪声的探测方式：只对真正在听的端口发 HTTP 请求。
// 若 UPK 沙箱有独立 netns 或系统不给读，返回空，由 scanAll 兜底。
func listeningPorts() []int {
	seen := map[int]bool{}
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		fh, err := os.Open(path)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(fh)
		sc.Scan() // 跳过表头
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			// 第 4 列是状态，0A = TCP_LISTEN；第 2 列是本地地址 00000000:22CB
			if len(fields) < 4 || fields[3] != "0A" {
				continue
			}
			i := strings.LastIndexByte(fields[1], ':')
			if i < 0 {
				continue
			}
			p, err := strconv.ParseInt(fields[1][i+1:], 16, 32)
			if err == nil && p > 0 && p <= 65535 {
				seen[int(p)] = true
			}
		}
		fh.Close()
	}
	out := make([]int, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sortByPriority(out)
	return out
}

// checkPorts 并发验证一组候选端口，返回按端口号排序的命中列表。
func checkPorts(ctx context.Context, ports []int) []int {
	type result struct {
		port int
		ok   bool
	}
	ch := make(chan result, len(ports))
	sem := make(chan struct{}, 32)
	var wg sync.WaitGroup
	for _, p := range ports {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			if isQBAt(ctx, p) {
				ch <- result{p, true}
			}
		}(p)
	}
	wg.Wait()
	close(ch)

	var out []int
	for r := range ch {
		if r.ok {
			out = append(out, r.port)
		}
	}
	sortByPriority(out)
	return out
}

// scanAll 对 1..65535 做连通性扫描，是 /proc 读不到时的兜底。
// 127.0.0.1 上未监听的端口会立刻 ECONNREFUSED，不必等超时，
// 所以 6 万次 dial 在 128 并发下通常 1~3 秒跑完；
// 只有真正 LISTEN 的端口才进入 HTTP 指纹验证，请求量极小。
func scanAll(ctx context.Context, skip map[int]bool) []int {
	type result struct {
		port int
		ok   bool
	}
	jobs := make(chan int, 512)
	ch := make(chan result, 512)
	var wg sync.WaitGroup
	const workers = 128

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d := &net.Dialer{Timeout: 200 * time.Millisecond, KeepAlive: 0}
			for p := range jobs {
				if ctx.Err() != nil {
					return
				}
				conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)))
				if err != nil {
					continue
				}
				conn.Close()
				if isQBAt(ctx, p) {
					ch <- result{p, true}
				}
			}
		}()
	}

	go func() {
	feed:
		for p := 1; p <= 65535; p++ {
			if skip[p] {
				continue
			}
			select {
			case jobs <- p:
			case <-ctx.Done():
				break feed
			}
		}
		close(jobs)
		wg.Wait()
		close(ch)
	}()

	var out []int
	for r := range ch {
		if r.ok {
			out = append(out, r.port)
		}
	}
	sortByPriority(out)
	return out
}

// findQBPorts 返回所有识别为 qB WebUI 的端口。
// 先查「本机正在监听的端口 + 常见端口」，没命中才做全范围扫描。
func findQBPorts(ctx context.Context) []int {
	candidates := map[int]bool{}
	for _, p := range commonQBPorts {
		candidates[p] = true
	}
	for _, p := range listeningPorts() {
		candidates[p] = true
	}

	ordered := make([]int, 0, len(candidates))
	for p := range candidates {
		ordered = append(ordered, p)
	}
	sort.Ints(ordered)

	if found := checkPorts(ctx, ordered); len(found) > 0 {
		return found
	}
	return scanAll(ctx, candidates)
}
