package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed web
var webFS embed.FS

const listenAddr = "0.0.0.0:21012"

// qBittorrent 与窗口壳同机运行，所以地址固定 127.0.0.1，
// 端口则可自动探测或由用户手填（见 probe.go）。地址不出现在界面上——
// 同 NAS 上装的 MSGO 壳也是这么做的。
//
// 端口优先级：QBVIEW_URL（完整 URL，调试用）> QBVIEW_PORT（调试用）
// > 配置文件里用户保存的端口 > 启动时自动探测 > 默认 8899 兜底。
func upstreamURL(port int) string {
	if v := strings.TrimSpace(os.Getenv("QBVIEW_URL")); v != "" {
		return v
	}
	return urlForPort(port)
}

// resolvePort 决定启动时用哪个端口，以及这个值的来源。
func resolvePort(saved int) (int, string) {
	if strings.TrimSpace(os.Getenv("QBVIEW_URL")) != "" {
		return saved, "env"
	}
	if v := strings.TrimSpace(os.Getenv("QBVIEW_PORT")); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 && p <= 65535 {
			return p, "env"
		}
	}
	if saved > 0 && saved <= 65535 {
		return saved, "saved"
	}
	return 0, "" // 交给自动探测
}

// 运行期配置。存在 <UGAPP_DATA_DIR>/config.json，权限 0600。
type appConfig struct {
	Port       int    `json:"port,omitempty"`
	PortSource string `json:"portSource,omitempty"`
	URL        string `json:"url"`
	User       string `json:"user"`
	Pass       string `json:"pass"`
}

type app struct {
	cfgPath string

	mu  sync.RWMutex
	cfg appConfig
	qb  *qbClient
}

func main() {
	log.SetFlags(log.LstdFlags)

	// Go 的 mime 表里没有 .webmanifest，不注册会返回 text/plain，
	// Chrome 会因此拒绝安装 PWA（清单必须 application/manifest+json）。
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
	_ = mime.AddExtensionType(".webmanifest.json", "application/manifest+json")

	dataDir := os.Getenv("UGAPP_DATA_DIR")
	if dataDir == "" {
		// 直接跑二进制调试时的兜底，避免必须设置环境变量。
		dataDir = "./data"
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Printf("创建数据目录失败: %v", err)
	}

	a := &app{cfgPath: filepath.Join(dataDir, "config.json")}
	a.load()

	port, source := resolvePort(a.cfg.Port)
	autoProbed := false
	if source == "" {
		// 没配过端口：启动时自动找一次。结果落盘，下次启动不必再扫。
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		found := findQBPorts(ctx)
		cancel()
		if len(found) > 0 {
			port, source = found[0], "auto"
			log.Printf("自动探测到 qBittorrent WebUI: %s", urlForPort(port))
		} else {
			port, source = defaultQBPort, "default"
			log.Printf("未自动探测到 qBittorrent，回退默认端口 %d（可在设置里修改）", port)
		}
		autoProbed = true
	}
	a.cfg.Port, a.cfg.PortSource = port, source
	a.cfg.URL = upstreamURL(port)
	if autoProbed {
		if err := a.save(a.cfg); err != nil {
			log.Printf("保存探测结果失败（不影响本次运行）: %v", err)
		}
	}

	// 环境变量优先级高于配置文件，方便临时覆盖排查问题。
	if v := strings.TrimSpace(os.Getenv("QBVIEW_USER")); v != "" {
		a.cfg.User = v
	}
	if v := strings.TrimSpace(os.Getenv("QBVIEW_PASS")); v != "" {
		a.cfg.Pass = v
	}
	if a.cfg.Pass != "" {
		a.rebuild()
	}

	// 端口自愈：已保存的端口可能已经失效（qB 容器换端口、旧配置残留，
	// 或 1.0.2 把某个返回 403 的网关端口误判成 qB）。后台异步校验，
	// 失效就重新探测并自动切换，不让用户卡在一个永远连不上的端口上。
	go a.healPort()

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("内嵌资源异常: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/config", a.handleConfig)
	mux.HandleFunc("/api/setup", a.handleSetup)
	mux.HandleFunc("/api/probe", a.handleProbe)
	mux.HandleFunc("/api/state", a.handleState)
	mux.HandleFunc("/api/t/", a.handleTorrentAction)
	mux.HandleFunc("/api/global/", a.handleGlobalAction)
	mux.Handle("/", http.FileServer(http.FS(sub)))

	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           noCacheAPI(mux),
		ReadHeaderTimeout: 10 * time.Second,
		// 不设 WriteTimeout：手机端可能长时间挂着页面轮询，写超时反而添乱。
	}
	log.Printf("qbview 监听 %s，配置 %s", listenAddr, a.cfgPath)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("监听失败: %v", err)
	}
}

// API 响应禁止缓存，否则手机会拿着旧的种子列表不放。
func noCacheAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func (a *app) load() {
	b, err := os.ReadFile(a.cfgPath)
	if err != nil {
		return
	}
	var c appConfig
	if json.Unmarshal(b, &c) == nil {
		a.cfg = c
	}
}

func (a *app) save(c appConfig) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := a.cfgPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	// 先写临时文件再原子替换，避免写一半断电留下坏配置。
	return os.Rename(tmp, a.cfgPath)
}

func (a *app) rebuild() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.qb = newQBClient(a.cfg.URL, a.cfg.User, a.cfg.Pass)
}

// healPort 启动时校验当前端口是否仍然是 qB WebUI；不是就重新探测并自动切换。
// 只跑一次，不做定时轮询——避免反复打 qB 的登录端点累积失败次数。
func (a *app) healPort() {
	a.mu.RLock()
	cur := a.cfg.Port
	a.mu.RUnlock()
	if cur <= 0 || cur > 65535 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if isQBAt(ctx, cur) {
		return
	}

	found := findQBPorts(ctx)
	if len(found) == 0 || found[0] == cur {
		log.Printf("端口 %d 已不是 qBittorrent WebUI，也没找到替代端口，保持原配置", cur)
		return
	}

	a.mu.Lock()
	a.cfg.Port, a.cfg.PortSource = found[0], "auto"
	a.cfg.URL = upstreamURL(found[0])
	c := a.cfg
	if c.Pass != "" {
		a.qb = newQBClient(c.URL, c.User, c.Pass)
	}
	a.mu.Unlock()

	if err := a.save(c); err != nil {
		log.Printf("保存自愈后的端口失败: %v", err)
		return
	}
	log.Printf("端口 %d 已不是 qBittorrent WebUI，自动切换到 %d", cur, found[0])
}

func (a *app) client() *qbClient {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.qb
}

func (a *app) configured() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.qb != nil
}

// ---------- 配置接口 ----------

func (a *app) handleConfig(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	c := a.cfg
	a.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"configured": a.configured(),
		"url":        c.URL,
		"port":       c.Port,
		"portSource": c.PortSource,
		"user":       c.User,
		// 密码不回传，只告诉前端有没有设过。
		"hasPass": c.Pass != "",
	})
}

// handleProbe 手动触发一次端口扫描，供设置面板的「自动探测」按钮调用。
func (a *app) handleProbe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只接受 POST")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 9*time.Second)
	defer cancel()
	found := findQBPorts(ctx)
	if len(found) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":    false,
			"error": "没找到 qBittorrent WebUI。确认容器已启动，或直接手动填端口。",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"port":  found[0],
		"ports": found,
		"url":   upstreamURL(found[0]),
	})
}

// setupInput 是 /api/setup 的请求体。URL 不接受前端输入，只收端口。
type setupInput struct {
	User string `json:"user"`
	Pass string `json:"pass"`
	Port int    `json:"port"`
}

func (a *app) handleSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只接受 POST")
		return
	}
	var in setupInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	in.User = strings.TrimSpace(in.User)

	if in.Port < 0 || in.Port > 65535 {
		writeErr(w, http.StatusBadRequest, "端口需在 1~65535 之间")
		return
	}

	// 端口决定地址。留空 / 0 交给自动探测；手填则先确认那真的是 qB。
	var portSource string
	if in.Port == 0 {
		ctx, cancel := context.WithTimeout(r.Context(), 9*time.Second)
		found := findQBPorts(ctx)
		cancel()
		if len(found) == 0 {
			writeErr(w, http.StatusBadRequest,
				"没找到 qBittorrent WebUI。确认容器已启动，或手动填写端口。")
			return
		}
		in.Port, portSource = found[0], "auto"
	} else {
		ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
		ok := isQBAt(ctx, in.Port)
		cancel()
		if !ok {
			writeErr(w, http.StatusBadRequest,
				fmt.Sprintf("端口 %d 上没检测到 qBittorrent WebUI，请检查端口或点「自动探测」", in.Port))
			return
		}
		portSource = "manual"
	}

	// 允许只改账号时留空密码，沿用旧密码。
	if in.Pass == "" {
		a.mu.RLock()
		in.Pass = a.cfg.Pass
		a.mu.RUnlock()
	}
	if in.User == "" {
		writeErr(w, http.StatusBadRequest, "用户名不能为空")
		return
	}
	if in.Pass == "" {
		writeErr(w, http.StatusBadRequest, "密码不能为空")
		return
	}

	target := upstreamURL(in.Port)
	// 先验证能真的登录，再落盘——否则用户会得到一个「保存成功但用不了」的配置。
	probe := newQBClient(target, in.User, in.Pass)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := probe.login(ctx); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	c := appConfig{
		Port: in.Port, PortSource: portSource, URL: target,
		User: in.User, Pass: in.Pass,
	}
	if err := a.save(c); err != nil {
		writeErr(w, http.StatusInternalServerError, "写入配置失败: "+err.Error())
		return
	}
	a.mu.Lock()
	a.cfg = c
	a.qb = probe
	a.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "port": in.Port})
}

// ---------- 状态聚合 ----------

type torrentView struct {
	Hash       string  `json:"hash"`
	Name       string  `json:"name"`
	Size       int64   `json:"size"`
	Progress   float64 `json:"progress"`
	DlSpeed    int64   `json:"dlspeed"`
	UpSpeed    int64   `json:"upspeed"`
	Eta        int64   `json:"eta"`
	State      string  `json:"state"`
	StateText  string  `json:"stateText"`
	Kind       string  `json:"kind"`
	Seeds      int     `json:"seeds"`
	Peers      int     `json:"peers"`
	Downloaded int64   `json:"downloaded"`
	Ratio      float64 `json:"ratio"`
	Category   string  `json:"category"`
	AddedOn    int64   `json:"addedOn"`
	Left       int64   `json:"left"`
}

func (a *app) handleState(w http.ResponseWriter, r *http.Request) {
	qb := a.client()
	if qb == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":         false,
			"configured": false,
			"error":      "尚未配置 qBittorrent 连接",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	var (
		raws     []rawTorrent
		tr       rawTransfer
		free     int64
		errTask  error
		errXfer  error
		wg       sync.WaitGroup
	)
	wg.Add(3)
	go func() { defer wg.Done(); raws, errTask = qb.torrents(ctx) }()
	go func() { defer wg.Done(); tr, errXfer = qb.transfer(ctx) }()
	go func() { defer wg.Done(); free = qb.freeSpace(ctx) }()
	wg.Wait()

	if errTask != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":         false,
			"configured": true,
			"error":      errTask.Error(),
		})
		return
	}
	if errXfer != nil {
		// 全局速度拿不到不算致命，任务列表还能用。
		log.Printf("取全局速度失败: %v", errXfer)
	}

	counts := map[string]int{"all": len(raws), "down": 0, "seed": 0, "pause": 0, "error": 0}
	views := make([]torrentView, 0, len(raws))
	for _, t := range raws {
		kind, text := classify(t.State)
		switch kind {
		case "down":
			counts["down"]++
		case "seed":
			counts["seed"]++
		case "pause":
			counts["pause"]++
		case "error":
			counts["error"]++
		}
		views = append(views, torrentView{
			Hash: t.Hash, Name: t.Name, Size: t.Size, Progress: t.Progress,
			DlSpeed: t.DlSpeed, UpSpeed: t.UpSpeed, Eta: t.Eta,
			State: t.State, StateText: text, Kind: kind,
			Seeds: t.NumSeeds, Peers: t.NumLeechs,
			Downloaded: t.Downloaded, Ratio: t.Ratio,
			Category: t.Category, AddedOn: t.AddedOn, Left: t.AmountLeft,
		})
	}

	// transfer/info 万一取不到（旧版 qB 或接口被限），用任务速度求和兜底，
	// 免得头部一直显示 0，看着像没在下载。
	dlSum, upSum := tr.DlInfoSpeed, tr.UpInfoSpeed
	if dlSum == 0 && upSum == 0 {
		for _, v := range views {
			dlSum += v.DlSpeed
			upSum += v.UpSpeed
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"configured": true,
		"ts":         time.Now().Unix(),
		"global": map[string]any{
			"dlSpeed": dlSum,
			"upSpeed": upSum,
			"dlTotal": tr.DlInfoData,
			"upTotal": tr.UpInfoData,
			"free":    free,
			"counts":  counts,
		},
		"torrents": views,
	})
}

// classify 把 qB 的 state 归一成前端只需要判断的四五类。
// qB 4.x 用 pausedDL/pausedUP，5.x 改名 stoppedDL/stoppedUP，两边都要认。
func classify(state string) (kind, text string) {
	switch state {
	case "downloading":
		return "down", "下载中"
	case "forcedDL":
		return "down", "下载中·强制"
	case "metaDL":
		return "down", "获取元数据"
	case "forcedMetaDL":
		return "down", "获取元数据·强制"
	case "stalledDL":
		return "down", "等待中"
	case "queuedDL":
		return "down", "排队中"
	case "allocating":
		return "down", "分配空间"
	case "uploading":
		return "seed", "做种中"
	case "forcedUP":
		return "seed", "做种中·强制"
	case "stalledUP":
		return "seed", "做种中·空闲"
	case "queuedUP":
		return "seed", "排队做种"
	case "pausedDL", "stoppedDL":
		return "pause", "已暂停"
	case "pausedUP", "stoppedUP":
		return "pause", "已完成"
	case "checkingDL", "checkingUP":
		return "check", "校验中"
	case "checkingResumeData":
		return "check", "恢复数据"
	case "moving":
		return "check", "移动中"
	case "error":
		return "error", "出错"
	case "missingFiles":
		return "error", "文件丢失"
	default:
		return "other", state
	}
}

// ---------- 操作接口 ----------

func (a *app) handleTorrentAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只接受 POST")
		return
	}
	qb := a.client()
	if qb == nil {
		writeErr(w, http.StatusConflict, "尚未配置 qBittorrent 连接")
		return
	}

	// /api/t/<hash>/<action>
	rest := strings.TrimPrefix(r.URL.Path, "/api/t/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" {
		writeErr(w, http.StatusBadRequest, "路径应为 /api/t/<hash>/<action>")
		return
	}
	hash, act := parts[0], parts[1]

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	var err error
	switch act {
	case "pause":
		err = qb.action(ctx, "pause", hash, nil)
	case "resume":
		err = qb.action(ctx, "resume", hash, nil)
	case "delete":
		var extra url.Values
		if r.URL.Query().Get("files") == "1" {
			extra = url.Values{"deleteFiles": {"true"}}
		}
		err = qb.action(ctx, "delete", hash, extra)
	default:
		writeErr(w, http.StatusBadRequest, "未知操作: "+act)
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *app) handleGlobalAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只接受 POST")
		return
	}
	qb := a.client()
	if qb == nil {
		writeErr(w, http.StatusConflict, "尚未配置 qBittorrent 连接")
		return
	}
	act := strings.TrimPrefix(r.URL.Path, "/api/global/")
	if act != "pause" && act != "resume" {
		writeErr(w, http.StatusBadRequest, "未知操作: "+act)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := qb.action(ctx, act, "all", nil); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------- 工具 ----------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"ok": false, "error": msg})
}
