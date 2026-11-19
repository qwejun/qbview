package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// qbClient 是 qBittorrent Web API v2 的最小客户端。
// 只做三件事：登录换 SID、拉任务列表、下发操作。
// 设计前提：qB 与本进程在同一台机器上（127.0.0.1），不跨网络，因此不做 TLS。
type qbClient struct {
	base  string // 例如 http://127.0.0.1:8899
	user  string
	pass  string
	http    *http.Client
	mu      sync.Mutex
	loginMu sync.Mutex // 串行化登录，避免并发请求同时打 /auth/login 触发 IP 封禁
	sid     string
	sidAt   time.Time
	freeVal int64
	freeAt  time.Time
}

// qB 的 SID 默认有效期 1 小时，这里保守按 50 分钟刷新。
const sidTTL = 50 * time.Minute

func newQBClient(base, user, pass string) *qbClient {
	tr := &http.Transport{
		// 上游固定在本机，任何情况下都不该走系统代理。
		// NAS 上若有 http_proxy 环境变量，登录请求会被塞进代理导致 403/502。
		Proxy:                 nil,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     false,
	}
	return &qbClient{
		base: strings.TrimRight(base, "/"),
		user: user,
		pass: pass,
		http: &http.Client{Transport: tr, Timeout: 20 * time.Second},
	}
}

// login 换取 SID。qB 成功返回 "Ok."，失败返回 "Fails."（HTTP 仍是 200）。
func (c *qbClient) login(ctx context.Context) error {
	form := url.Values{}
	form.Set("username", c.user)
	form.Set("password", c.pass)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.base+"/api/v2/auth/login", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// CSRFProtection=false 时不需要 Referer，但部分版本仍会校验 Origin/Host 一致性，
	// 这里带上同源 Referer，成本为零。
	req.Header.Set("Referer", c.base+"/")
	req.Header.Set("Origin", c.base)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("连接 qBittorrent 失败: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	if resp.StatusCode == http.StatusForbidden {
		return errors.New("qBittorrent 拒绝登录（403）：可能是 IP 被临时封禁，或密码错误次数过多")
	}

	for _, ck := range resp.Cookies() {
		if ck.Name == "SID" && ck.Value != "" {
			c.mu.Lock()
			c.sid = ck.Value
			c.sidAt = time.Now()
			c.mu.Unlock()
			return nil
		}
	}

	s := snippet(string(body), 120)
	if strings.Contains(s, "Fails") {
		return errors.New("账号或密码错误")
	}
	if s == "" {
		// 网关 / 反向代理占了端口时最典型的表现：HTTP 200 但响应体是空的。
		return fmt.Errorf("%s 不是 qBittorrent 的 Web API（登录响应为空）。这个端口可能被网关或反向代理占用，改端口或点「自动探测」", c.base)
	}
	if strings.Contains(s, "<") {
		return fmt.Errorf("%s 返回的是网页而不是 qB 的登录接口。检查端口，或点「自动探测」", c.base)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s 登录返回 HTTP %d：%s", c.base, resp.StatusCode, s)
	}
	return fmt.Errorf("登录未返回 SID：%s（确认端口是 qBittorrent WebUI，而不是反向代理）", s)
}

// snippet 把上游响应截短，避免把一整页 HTML 塞进错误提示。
func snippet(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return string(r)
}

func (c *qbClient) currentSID() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sid == "" || time.Since(c.sidAt) > sidTTL {
		return "", false
	}
	return c.sid, true
}

func (c *qbClient) resetSID() {
	c.mu.Lock()
	c.sid = ""
	c.mu.Unlock()
}

// ensure 返回可用的 SID，必要时登录。
// 用 loginMu 而不是 mu 来串行化，因为 login() 内部要拿 mu 写 sid，同一把锁会自锁。
func (c *qbClient) ensure(ctx context.Context) (string, error) {
	if sid, ok := c.currentSID(); ok {
		return sid, nil
	}
	c.loginMu.Lock()
	defer c.loginMu.Unlock()
	// 等锁期间可能已被别的请求登录过，双重检查。
	if sid, ok := c.currentSID(); ok {
		return sid, nil
	}
	if err := c.login(ctx); err != nil {
		return "", err
	}
	sid, ok := c.currentSID()
	if !ok {
		return "", errors.New("登录后仍未取得 SID")
	}
	return sid, nil
}

// do 发请求；遇到 403 说明 SID 失效，重登后重试一次。
func (c *qbClient) do(ctx context.Context, method, path string, form url.Values) ([]byte, error) {
	for attempt := 0; attempt < 2; attempt++ {
		sid, err := c.ensure(ctx)
		if err != nil {
			return nil, err
		}

		var body io.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
		if err != nil {
			return nil, err
		}
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		req.AddCookie(&http.Cookie{Name: "SID", Value: sid})
		req.Header.Set("Referer", c.base+"/")

		resp, err := c.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("请求 qBittorrent 失败: %v", err)
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()

		if resp.StatusCode == http.StatusForbidden && attempt == 0 {
			c.resetSID()
			continue
		}
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("qBittorrent 返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
		}
		if readErr != nil {
			return nil, readErr
		}
		return data, nil
	}
	return nil, errors.New("重新登录后仍被拒绝，请检查 qBittorrent 的 IP 封禁列表")
}

// rawTorrent 是 /api/v2/torrents/info 的返回项，只保留前端要用的字段。
// qB 返回 40+ 个字段，全量透传只会让前端变笨。
type rawTorrent struct {
	Hash       string  `json:"hash"`
	Name       string  `json:"name"`
	Size       int64   `json:"size"`
	Progress   float64 `json:"progress"`
	DlSpeed    int64   `json:"dlspeed"`
	UpSpeed    int64   `json:"upspeed"`
	Eta        int64   `json:"eta"`
	State      string  `json:"state"`
	NumSeeds   int     `json:"num_seeds"`
	NumLeechs  int     `json:"num_leechs"`
	Downloaded int64   `json:"downloaded"`
	Uploaded   int64   `json:"uploaded"`
	Ratio      float64 `json:"ratio"`
	Category   string  `json:"category"`
	AddedOn    int64   `json:"added_on"`
	SavePath   string  `json:"save_path"`
	AmountLeft int64   `json:"amount_left"`
	NumComplete int    `json:"num_complete"`
	NumIncomplete int  `json:"num_incomplete"`
}

type rawTransfer struct {
	DlInfoSpeed  int64 `json:"dl_info_speed"`
	UpInfoSpeed  int64 `json:"up_info_speed"`
	DlInfoData   int64 `json:"dl_info_data"`
	UpInfoData   int64 `json:"up_info_data"`
}

func (c *qbClient) torrents(ctx context.Context) ([]rawTorrent, error) {
	data, err := c.do(ctx, http.MethodGet, "/api/v2/torrents/info", nil)
	if err != nil {
		return nil, err
	}
	var out []rawTorrent
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("解析任务列表失败: %v", err)
	}
	return out, nil
}

func (c *qbClient) transfer(ctx context.Context) (rawTransfer, error) {
	var t rawTransfer
	data, err := c.do(ctx, http.MethodGet, "/api/v2/transfer/info", nil)
	if err != nil {
		return t, err
	}
	err = json.Unmarshal(data, &t)
	return t, err
}

// freeSpace 拿磁盘可用空间。sync/maindata 会顺带返回全部种子，
// 几百个种子时响应可能几百 KB，所以这里缓存 60 秒，不要每次轮询都拉。
func (c *qbClient) freeSpace(ctx context.Context) int64 {
	c.mu.Lock()
	if time.Since(c.freeAt) < 60*time.Second {
		v := c.freeVal
		c.mu.Unlock()
		return v
	}
	c.mu.Unlock()

	data, err := c.do(ctx, http.MethodGet, "/api/v2/sync/maindata?rid=1", nil)
	if err != nil {
		return 0
	}
	var m struct {
		ServerState struct {
			FreeSpaceOnDisk int64 `json:"free_space_on_disk"`
		} `json:"server_state"`
	}
	if json.Unmarshal(data, &m) != nil {
		return 0
	}
	v := m.ServerState.FreeSpaceOnDisk
	if v > 0 {
		c.mu.Lock()
		c.freeVal, c.freeAt = v, time.Now()
		c.mu.Unlock()
	}
	return v
}

func (c *qbClient) action(ctx context.Context, endpoint string, hashes string, extra url.Values) error {
	f := url.Values{}
	f.Set("hashes", hashes)
	for k, v := range extra {
		f[k] = v
	}
	_, err := c.do(ctx, http.MethodPost, "/api/v2/torrents/"+endpoint, f)
	return err
}

func (c *qbClient) appVersion(ctx context.Context) (string, error) {
	data, err := c.do(ctx, http.MethodGet, "/api/v2/app/version", nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
