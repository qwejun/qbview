# qBittorrent（com.wbj.nas.qb）

qBittorrent 的任务面板，桌面窗口与手机页面共用一份界面。不做原版 WebUI 的复制品，
只保留看进度、暂停、删除这几件事。视觉语言借鉴 mihomo 面板（浅灰底、白卡片、
彩色图标、明暗主题），信息架构按下载器自己的需求设计，不做它的布局复制品。

产物：`amd64_nasync_com.wbj.nas.qb_1.0.3.0001.upk`（约 2.7 MB）

---

## 架构

```
UGOS 桌面窗口 ──┐
                ├── qbview (Go, 0.0.0.0:21012) ──► qBittorrent Web API (127.0.0.1:<端口>)
绿联云 App ─────┘        │
                         ├─ 内嵌前端（响应式，桌面/手机同一份）
                         ├─ 自动探测本机 qB 端口 / 允许手填
                         ├─ 聚合 /api/state（把 40+ 字段压成前端要的那几个）
                         └─ 持有 SID，前端永远拿不到 qB 的凭证
```

一个静态二进制，前端用 `go:embed` 打进去，没有外部依赖、没有 Node 构建链。

**为什么前端不直接调 qBittorrent**：qB 的 API 需要 SID cookie，把凭证发到浏览器等于
把整个下载器的控制权交给页面；而且 `torrents/info` 一次返回 40 多个字段，手机端没必要
解析。所以壳做代理 + 聚合，前端只认自己的 `/api/state`。

**为什么不用填地址，端口却可以改**：qBittorrent 和这个壳跑在同一台 NAS 上，
所以主机永远固定 `127.0.0.1`，界面上不出现地址栏（和 MSGO 壳一致）。
但端口不写死——不同人装的 qB 可能用 8899、8080 或任意自定义端口。

端口由 `probe.go` 决定，优先级从高到低：

1. 配置文件里**用户手动保存**的端口（设置面板第一项）
2. 上次**自动探测**到并落盘的端口
3. 首次启动自动探测：先验证常见端口 + `/proc/net/tcp` 里正在监听的端口，
   都没命中再对 `127.0.0.1` 做全范围并发扫描（未监听的端口会立刻拒绝连接，1~3 秒扫完）
4. 全都找不到才回退默认 8899，并在设置面板提示手动修改

识别方式不是"端口能连上就算"，而是**三道指纹**，缺一不可：

1. `GET /` 返回 200 且页面含 `qBittorrent`（登录页 description）
2. `/api/v2/app/version` 未登录回 403，或回纯版本号；404 / 302 / 整页 HTML 直接排除
3. `POST /api/v2/auth/login` 用固定假账密打一次，必须是 qB 特有的
   `Fails.` / SID cookie（localhost 免认证）/ 403

第 3 道是 1.0.3 补的。UGOS 网关、NAS 自带服务、各类反代对未知 `/api/v2/` 路径
普遍回 403，只靠前两道会把 `8080` 这类泛用端口误判成 qB —— 用户拿真账号去登录
就卡在「登录未返回 SID」，因为那个端口返回的是 200 空 body，不是 qB 的 API。
第 3 道只在自动探测和启动自检时发一次，不会在轮询里累积失败次数去触发封禁。

命中多个端口时按 `commonQBPorts` 的专属程度排序（8899 优先于 8080），
不再按数字大小取最小。

**端口自愈**：启动时后台异步校验已保存的端口是否仍然是 qB；容器换端口、旧配置
残留、或 1.0.2 误判留下的 8080，都会自动重探并落盘切换，不用手动改。
日志里会有 `端口 X 已不是 qBittorrent WebUI，自动切换到 Y`。

调试用环境变量：`QBVIEW_PORT` 指定端口，`QBVIEW_URL` 直接覆盖完整 URL，
`QBVIEW_USER` / `QBVIEW_PASS` 覆盖账号密码。正常安装都不会设置。

## 安装

应用中心 → 左下角「手动安装」→ 选 upk → 出现「qBittorrent」图标。

装完点开窗口，会弹出配置面板：

| 字段 | 填什么 |
|---|---|
| WebUI 端口 | 首次打开会自动探测并填好；不对就自己改，或点「自动探测」重扫 |
| 用户名 | 你 qBittorrent WebUI 的用户名（默认 `admin`，或自己设的） |
| 密码 | 你 qBittorrent WebUI 的密码（不是 NAS 账号） |

保存前会真实登录一次，密码错了当场报错，不会存下一份用不了的配置。
端口上没检测到 qB 的 WebUI 也会当场拦下，不会静默存一个连不上的值。
凭据和端口写在 `/var/packages/com.wbj.nas.qb/data/config.json`，权限 600。

## 手机

装好之后不用做任何事：绿联云 App 的应用列表里会出现「qBittorrent」，点开就是全屏面板。
走的还是同一个 21012 端口，`supports: ["app", "pc"]` 已经声明，客户端自己会开窗口。

外网能不能用取决于 NAS 本身开没开外网访问，跟这个包无关。

调试时也可以直接用浏览器打开 `http://<NAS 局域网 IP>:21012`。页面带了 manifest，
iOS/Android 可以「添加到主屏幕」，之后按独立应用启动，地址栏不显示。

## 界面

- **左栏**（手机上变成顶部横排标签）：全部 / 下载中 / 做种 / 已暂停 / 异常，各带数量
- **顶栏**：大标题跟当前筛选走（全部 / 下载中…），旁边连接状态胶囊，右侧全部暂停 / 全部继续
- **统计四格**：下载速度、上传速度、任务总数、剩余空间——分类数量不进这排，
  左栏筛选徽标已经有了，放两遍是重复信息
- **任务行**：状态标签 + 进度条（蓝=下载、绿=做种、橙=校验、红=出错、灰=暂停）、
  速度、剩余、ETA、大小、种子/连接数、分享率，右侧暂停/继续 + 删除
- **删除**：不用原生 confirm 弹窗（和卡片风格割裂），点「删除」原地展开
  「取消 / 仅移除 / 连文件删」三个选项，选了才执行
- **主题**：右下角太阳图标切换明暗，跟随系统默认，选择记在 localStorage
- **刷新**：2 秒轮询，页面切到后台自动停（手机省电），回前台立刻刷一次

## 重新构建

```bash
# 编译（Linux 静态二进制）
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" \
  -o pkg/rootfs_amd64/bin/qbview ./app

# 打包
cd pkg && ugcli check && ugcli pack --build 1 --arch amd64
```

改了 `app/web/` 下的前端资源必须重新编译——它们通过 `go:embed` 编进二进制。

### 端口探测回归

```bash
python runtest/mockqb.py   # 18999：真 qB 行为（Fails. / SID / 403）
python runtest/fakeqb.py   # 8080：假 qB（登录页 + 通用 403 + 200 空 body）
```

空配置启动应自动选中 **18999** 而不是 8080；预置 `config.json` 里
`"port": 8080` 再启动，日志应出现 `端口 8080 已不是 qBittorrent WebUI，自动切换到 18999`。

### 图标

用 qBittorrent 4.0+ 官方 logo（`src/icons/qbittorrent-tray.svg` 的 1280px 透明 PNG，GPLv3），
和 mihomo 那个用官方猫 logo 的做法一致：透明底、品牌图形居中，不套深色方板、不自绘箭头。

```bash
python assets/make_icons.py   # 源图 assets/qbittorrent_logo.png
```

一次生成 `pkg/rootfs_common/icon.png`(256)、`app/web/icon.png`(512)、
`app/web/icon-maskable.png`(512，满幅官方渐变底 + 中心 62% 安全区内的 logo)。
改完记得把 `app/web/sw.js` 的 `CACHE` 版本号 +1，否则手机 PWA 还是缓存里的旧图标。

## 关键实现点

| 位置 | 说明 |
|---|---|
| `qb.go: do()` | 403 视为 SID 失效，重登后重试一次；`loginMu` 串行化登录，避免并发打 `/auth/login` 触发 qB 的 IP 封禁 |
| `qb.go: newQBClient()` | 反代 Transport 强制 `Proxy: nil`，NAS 上有 `http_proxy` 环境变量时不会把本机请求塞进代理 |
| `qb.go: freeSpace()` | `sync/maindata` 会顺带返回全部种子，几百个种子就是几百 KB，因此缓存 60 秒 |
| `probe.go: findQBPorts()` | 先查常见端口 + `/proc/net/tcp` 的 LISTEN 端口，未命中才全范围扫描；结果按 qB 专属程度排序 |
| `probe.go: isQBAt()` | 三道指纹：登录页 + `app/version` + `auth/login` 的 `Fails.`/SID/403，手填端口也走这套验证 |
| `probe.go: confirmQBAPI()` | 第三道。用假账密打一次 login，剔除「返回 403 但其实不是 qB」的网关/反代 |
| `main.go: healPort()` | 启动时异步校验已保存端口，失效则自动重探切换并落盘 |
| `main.go: resolvePort()` | 端口优先级 `QBVIEW_URL` > `QBVIEW_PORT` > 用户保存 > 自动探测 > 默认 8899 |
| `main.go: handleSetup()` | 只收端口不收 URL，主机锁死 `127.0.0.1`，窗口不会被当成通用 qB 客户端连外网 |
| `main.go: handleState()` | `transfer/info` 拿不到时用任务速度求和兜底，否则头部一直显示 0 |
| `main.go: main()` | 注册 `.webmanifest` 的 MIME，否则 Go 返回 `text/plain`，Chrome 拒绝安装 PWA |
| `style.css: .row-name` | 单行 `ellipsis`。两行 clamp 配 `word-break` 会把中英混排的名字断成「沙丘 / 2.Dune…」 |
| `style.css: .sidebar`(860) | 必须 `align-content:flex-start` + `align-self:start`，否则 `min-height:100vh` 多出的高度会摊到行上，导航被拉成两行巨型按钮、顶部空一大块 |
| `index.html: 内联脚本` | 在样式生效前定好 `data-theme`，否则暗色用户会看到一帧白闪 |
| `sw.js` | 网络优先。用 cache-first 会导致改完前端、刷新还是旧界面 |

## 排错

| 现象 | 处理 |
|---|---|
| 窗口显示"后端未运行" | `docker ps` 确认 `qbittorrent` 容器在跑 |
| 保存配置报"账号或密码错误" | 密码是 WebUI 登录密码，不是 NAS 账号 |
| 保存配置报"端口 X 上没检测到 qBittorrent" | 点设置里的「自动探测」重扫，或确认 qB 容器的 `WEBUI_PORT` 后手动填 |
| 保存配置报"不是 qBittorrent 的 Web API（登录响应为空）" | 这个端口被网关/反向代理占了，不是 qB 的 API。改端口或点「自动探测」；1.0.3 起自动探测会直接剔除它 |
| 自动探测一直找不到 | 确认 `qbittorrent` 容器在跑；qB 开了 HTTPS 或改了监听地址时自动探测不适用，手动填端口 |
| 保存配置报"连接失败" | 在 NAS 上 `curl 127.0.0.1:<端口>` 试；不通说明容器没起或端口不对 |
| 之前能用，qB 换端口后一直连不上 | 1.0.3 启动会自愈；旧版需点「自动探测」或手填新端口 |
| 任务列表空但 qB 里有任务 | 看 `/api/state` 返回体；可能是 SID 失效后 qB 返回了空数组 |
| 顶部速度一直是 0 | 看日志有没有 `取全局速度失败`，兜底求和也没数据说明任务本身没在跑 |
| 改了前端不生效 | 重新编译，并确认浏览器里 sw 已更新（`qbview-v8`） |

日志：`/var/packages/com.wbj.nas.qb/log/`
数据：`/var/packages/com.wbj.nas.qb/data/`

## 端口

占用 **21012**（避开 mihomo 的 21010、Media Saber 的 21011）。监听 `0.0.0.0`，
因为手机要直连；如果只想让窗口用，把 `main.go` 里的 `listenAddr` 改成 `127.0.0.1:21012`。
