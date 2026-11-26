# phoneMic

手机浏览器当电脑无线麦克风：手机网页采集 → WSS 推 PCM → 电脑端 Go 单二进制写入虚拟声卡（macOS BlackHole / Windows VB-Cable），任何 App 把它选作麦克风即可收音。无 wheel 依赖的独立跨平台仓库，不适用 keke 系框架先例。

## 构建与验证

- **本机全局 `go env CGO_ENABLED=0`**：所有 go build/test/vet 必须显式前缀 `CGO_ENABLED=1`，否则 malgo 符号全部 undefined。
- 测试与 mac 构建：`CGO_ENABLED=1 go test ./...`、`CGO_ENABLED=1 go build -o phone_mic ./cmd/phone_mic`（malgo 走 cgo，需 Xcode CLT）。
- Windows 交叉编译（本机验证用）：`CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc CXX=x86_64-w64-mingw32-g++ go build -ldflags '-H windowsgui' -o phone_mic.exe ./cmd/phone_mic`；**CXX 必须显式指定**——只设 CC 时 cgo 用宿主 clang++ 编 webview.cc，报 `shlobj.h file not found`（GCC16/mingw-w64 14 实测）；mingw-w64 由 brew 安装（免 sudo；ghcr.io 被限速/断流时用 `HOMEBREW_BOTTLE_DOMAIN=https://mirrors.ustc.edu.cn/homebrew-bottles`）。**`-H windowsgui` 必须带**（2026-11-26 用户要求启动不弹控制台）：GUI 子系统双击/Start-Process 不再分配控制台，日志只走文件；CLI 参数（--list-devices/--install-driver/--uninstall-driver）经 attachParentConsole 挂回父终端输出；父进程无控制台后 powershell 子进程须 CREATE_NO_WINDOW（driver_windows.go 已内置）。
- 运行：`./phone_mic`（HTTPS :8443 手机页面/WS，HTTP :8080 仅 CA 下载与跳转）；`--list-devices` 列输出设备；`--device <substr>` 指定输出设备；`--install-driver` / `--uninstall-driver` 安装/卸载虚拟声卡（都会弹系统管理员授权）。
- 首次使用顺序：`--install-driver`（mac 装 BlackHole2ch，win 装 VB-Cable）→ **mac 需重启电脑**（见下）→ 启动 → 手机浏览器装页面引导里的 CA 证书 → 打开 `https://<局域网IP>:8443` 开始推流 → 会议 App 麦克风选虚拟声卡。

## 已踩过的坑（验证过的事实）

- **SIP 禁止重启 coreaudiod**（`launchctl kickstart -k system/com.apple.audio.coreaudiod` 报 error 150）：BlackHole pkg 装完文件在 `/Library/Audio/Plug-Ins/HAL/` 但设备列表不出现，**必须重启电脑**；卸载同理。代码已按此设计：装后轮询设备枚举，未加载则明确提示重启，不尝试 kickstart。
- **existential.audio 拦 Go 默认 UA 返回 406**：下载驱动资源必须带浏览器 User-Agent（internal/driver 已内置）。
- BlackHole 的 GitHub release 无 assets，官方 pkg 只能从 brew cask API json 拿 URL+sha256。
- 集成浏览器/Chrome 不信任自签 CA（ERR_CERT_AUTHORITY_INVALID，无"继续"入口）。**无声全链路 UI 验证法**：临时把 player.go 两处 `malgo.InitContext(nil,…)` 改成 `[]malgo.Backend{malgo.BackendNull}` → 构建 /tmp 测试二进制（设备名 "NULL Playback Device"）→ **立刻还原源码再构建正式二进制**；用 playwright-core 驱动系统 Chrome（`ignoreHTTPSErrors: true` + `--use-fake-device-for-media-stream --use-fake-ui-for-media-stream --mute-audio`）点 `#micBtn`、断言 `/status` 的 sessionFrames/writtenBytes/bufferedMs。
- 手机页面（web/static/index.html）元素 id：`micBtn/btnLabel/pill/state/dur/viz/db/device/buffered/sent/rate/setup`（details 手风琴）。

## 手机页 UI 设计系统（ui-ux-pro-max 推荐 + 用户确认重做）

- OLED 暗夜风：bg `#0F0F23`、card `#1B1B30`、primary 靛蓝 `#4338CA`、推流绿 `#22C55E`、danger `#EF4444`、fg `#F8FAFC`、muted `#94A3B8`。
- **零外链**：不用 CDN 字体（局域网产品必须离线可用，Google Fonts 国内不可达且阻塞渲染），字体走系统栈。页面内功能图标一律内联 SVG，禁 emoji；favicon/apple-touch-icon 是 web/static 下的 PNG 文件（见「品牌图标」）。
- 电平链路：worklet 每帧 postMessage `{pcm, level(RMS)}` → 页面 `pushLevel` 指数平滑（0.7/0.3）驱动 `--level` CSS 变量（按钮光环）、canvas 64 条滚动电平条、dB 读数；**dB 必须用平滑值**（瞬时值会在静音帧闪 `--`，与光环不一致）。
- 触控 ≥44px、focus-visible、prefers-reduced-motion、320px 无横向溢出（均已 Playwright 验证）。

## 品牌图标（2026-11-25 用户逐轮确认：像素风手机角色 + V1 渐变边框）

- 唯一源图 `assets/logo.png`（1024，透明圆角外框）；角色 = 像素手机（绿屏笑脸+电平条+无线电波），最外圈靛蓝→绿渐变像素边框（用户为解决「暗底图标在 dock 里显小」加的）。合成参数：边框 inset 5.9%、暗面板圆角 16.6%、内容占宽 76%、画内底色 `#07071E`（AI 原图底色，非页面 bg #0F0F23，勿混用）。
- Android：`mipmap-{m,h,xh,xxh,xxxh}dpi/ic_launcher(.png/_round.png)` 全密度 + `mipmap-anydpi-v26` 自适应。**自适应分层是刻意的**：边框=背景层（`drawable-nodpi/ic_launcher_background.png` 渐变），暗面板+角色=前景层（角色内容≤56% 画幅，适配 66/108 安全区）——这样任何 launcher 蒙版下边框都贴图标外缘；若把带边框整图当前景，边框会被蒙版裁掉。monochrome 用 `drawable/ic_launcher_monochrome.xml`（108 viewport + group translate 22 收进安全区）；通知小图标是独立的白色线稿 `drawable/ic_notification.xml`（全彩 launcher 图不可当 smallIcon）。
- 网页：`web/static/favicon.png`(64) + `apple-touch-icon.png`(180)，由既有 FileServer 直出，index.html 用 `<link>` 引用。
- 图像处理环境：`~/.copilot/tools/imgenv/bin/python`（venv + pillow，系统 python3 无 PIL）。改图标一律从 assets/logo.png 派生各尺寸，勿再逐尺寸手改。
- 纯静态/favicon 验证不必碰音频：BlackHole 未加载时正式二进制起不来（先开设备后开 HTTP），可在会话目录建临时 go module（replace 指向本仓库）只 serve `web.StaticFS()` 后 curl。

## 版本与交付惯例（2026-11-26 用户拍板：每次构建版本号 +1，任务完成后自动重启桌面+部署手机）

- **每次构建版本号 +1**：仓库根 `build.ps1` 以 gradle `versionName` 为唯一源，自动末位 +1、`versionCode` +1，并同步 `internal/version/version.go`（Go 侧唯一版本常量，ui 与 server 都读它）。不要再手动双写版本号。
- **三端右下角版本号**（fixed/overlay 常显，2026-11-26 用户拍板三端都加）：桌面 `desktop.html` 的 `.ver`（fixed 右下，数据 GetState.version）；手机网页 `index.html` 的 `#ver`（数据来自 `/status` 的 `version` 字段）；Android `main_activity.kt` 用 `BuildConfig.VERSION_NAME`（gradle 已开 `buildConfig = true`），以 fillMaxSize+wrapContentSize(BottomEnd) 的兄弟 overlay 盖在屏幕右下角（与 ScanScreen 同模式）。
- **每次任务完成后（本轮有改动要提交时）自动走完全流程，不逐步询问**：最小验证（`CGO_ENABLED=1 go test ./...` + Android 单测）→ `.\build.ps1`（bump → 双端构建 → 拷 Downloads → 重启桌面 → 装手机）→ 验证部署结果（桌面 `/status.version`、手机 `dumpsys package` versionName + 截图右下角）→ commit → push。验证/构建失败不提交不 push，先报告。只读任务不触发。
- commit message 沿用 `<versionName>: 中文描述`（取 bump 后的新版本号）。
- 产物拷 `~/Downloads` 统一命名：`phoneMic_<ver>_android_debug.apk` / `phoneMic_<ver>_mac_arm64` / `phoneMic_<ver>_win_x64.exe`；仓库根 phone_mic / phone_mic.exe / *.exe / *.apk 已被 .gitignore（本地构建产物，不入库）。
- mac 端暂无脚本：按同样口径手动 bump（gradle + internal/version/version.go 两处同步）后构建双端、重启桌面；部署目标同样只装小米13。

## Windows 本机工具链与一键构建（2026-11-26 实测）

- 工具链都不在 PATH，`build.ps1` 已内置：Go=`~\tools\go\bin`（go1.27.3，全局 CGO_ENABLED=0 必须显式置 1）、mingw64=`~\tools\mingw64\bin`（gcc16 ucrt，CC/CXX 用默认 gcc/g++ 即可）、JDK21=`%LOCALAPPDATA%\Programs\jdk-21.0.13.1+2`、ANDROID_HOME=`%LOCALAPPDATA%\Android\Sdk`、adb=`%ANDROID_HOME%\platform-tools\adb.exe`。
- Gradle 用本机 `%LOCALAPPDATA%\Programs\gradle-8.14.3\bin\gradle.bat`（wrapper dist 未缓存、services.gradle.org 国内慢）；不存在时脚本回退 gradlew.bat。
- **Windows 不能覆盖运行中的 exe**：脚本先构建 `phone_mic.new.exe` → `Stop-Process -Id` 停旧 `phone_mic` 进程并 WaitForExit → Move 替换 → `Start-Process` 启动（独立进程，会话结束不随之退出，已实测）。其它名字的旧实例（phoneMic_*）由 `single.TakeOver` 自动接管踢掉，脚本不用管。
- **自动部署只装小米13（2211133C，Android 16）**（用户拍板）：遍历 `adb devices` 在线设备按 `getprop ro.product.model` 匹配，不写死无线 adb serial（形如 `adb-<serial>-<token>._adb-tls-connect._tcp`，token 会变）；离线跳过并说明。小米15（M154FF）不自动装。
- MIUI 可能拒装（INSTALL_FAILED_USER_RESTRICTED）：需手机上确认或开发者选项开 USB 安装；脚本会原样报错。

## Android app（android/，Kotlin + Compose，与网页同契约）

- 构建必须 JDK21：`cd android && export JAVA_HOME=/Users/afreto/Library/Java/JavaVirtualMachines/corretto-21.0.3/Contents/Home && ./gradlew :app:assembleDebug :app:testDebugUnitTest`（系统 Gradle 9.2.1 默认 JVM25 与 wrapper 8.14.3 不兼容）。APK 在 `android/app/build/outputs/apk/debug/app-debug.apk`；Compose 依赖本机未缓存，settings.gradle.kts 已配阿里云镜像优先。
- 信任模型 TOFU：首连 https 探测失败 → 从 mDNS TXT 的 http 端口取 `/ca.crt` → `PinnedCaTrustManager` 校验后存 prefs `ca_store`（key `ca_<host>`）→ 重建 client。**已固定 CA 时探测失败分流**：SSLException（证书换了）才重走 TOFU；连接类失败（ConnectException 等）直接用固定 client 让 WS 自然重试，不重取 CA。
- 重连：退避 `1s shl (attempt-1)` 封顶 30s；被踢 code 4001 是终态不重连；连接成功 attempt 归零。
- 扫码与发现刷新（2026-11-25 用户拍板）：内置扫码页 CameraX 1.4.1 + MLKit barcode-scanning 17.3.0（离线模型，scan_screen.kt）；扫到**仅选中不连接**（复用 parseManual，host[:port] 正则校验、端口缺省 8443）；未推流时每 30 秒重启 mDNS（不清列表），ServerCard「刷新」清列表重搜、「扫码」走 CAMERA 权限流程。MuMu 假相机能开扫码页但扫不到真码，识别效果需真机。
- 选中项自动跟随（2026-11-25 用户拍板）：mDNS 解析到**同名**电脑且 host/port/httpPort 有变 → 自动更新选中项并持久化（discovery.kt `shouldFollowSelected`，单测 follow_update_test.kt）；扫码/手输的选中项 name=IP，与 mDNS 实例名不同名，不会被覆盖。
- **Resampler 必须用整数相位累加**（posUnits 以 1/48000 采样为单位，`floorDiv`/`Math.floorMod`）：浮点 pos 跨 chunk 边界会因累加量级不同漂移出 1 LSB 差异，"任意分块输出逐位一致"是单测守住的不变量（resampler_test.kt）。
- **MuMu 模拟器特性（adb 127.0.0.1:16384/16448，端口以 `adb devices` 实测为准；Android 12，已实测）**：
  - 默认平台 TLS 信任被放宽：自签证书直接过、`handshake.peerCertificates` 为空 → 全新安装不触发 TOFU 属正常；**自定义 TrustManager（pinning）不受影响**，三条信任路径（固定拒绝/TOFU 固定/固定接受）已用 prefs 注入法全部真机验证。
  - NsdManager 发现启动失败 code 0（NAT 网络），用手动输 IP 兜底；Go 端 mDNS 广播本身正常（Mac `dns-sd -B _phonemic._tcp local.` 可见）。
  - adb UI 自动化：每次 tap 前必须 `exec-out screencap -p` 核对坐标（按钮位置随页面状态/滚动变化）；`pm grant com.afret0.phonemic android.permission.RECORD_AUDIO` 免弹窗；日志 `logcat -d -s CaStore:* MicEngine:* Discovery:* Scan:*`；debug 包可 `run-as` 读写 shared_prefs（注入 ca_store.xml 可模拟证书更换）。
  - 模拟器麦克风采零（电平恒 0）属正常，音质/息屏推流需真机验证。

## 契约（改动前必读，均已与用户确认）

- 音频线上格式：48000Hz 单声道 Int16 LE 裸 PCM，WSS 二进制帧；无协议头。
- 单路推流：新 WS 连接进来即踢旧连接（close 4001）；抖动缓冲目标 100ms、上限 300ms 超限丢最旧。
- 证书：程序内置自签 CA（certs/ 目录持久化，SAN 含全部本机非回环 IP），HTTP :8080/ca.crt 供手机安装；不依赖 mkcert/caddy。
- 桌面对外地址（2026-11-25 用户拍板，两轮）：地址列表/二维码/CA 链接一律走 `certs.ReachableIPv4s()`——剔除 169.254 链路本地后按档稳定排序：出口 IP（`net.Dial("udp","8.8.8.8:80")` 取 LocalAddr，不发包；非私网如 Clash TUN 198.18 段则弃用）> 192.168/16 > 其它私网 > 其余；二维码取第一个。证书 SAN 仍用 `LocalIPv4s()` 含全部非回环 IP，不受影响。**注意 `net.ParseIP` 返回 16 字节表示，按字节判网段必须先 `To4()`**。
- mDNS 广播（2026-11-25 用户拍板）：`zeroconf.RegisterProxy` 只广播 `ReachableIPv4s()` 首位**一个** IP（所有网卡仍应答，TXT 契约不变，旧 app 也修好）——默认 `Register` 会把 Docker/WSL 等全部网卡 IP 塞进 A 记录，Android 随机挑一个导致连 172.17.0.1。**「按收包网卡回地址」方案不可行**：x/net ipv4 的 Windows `setControlMessage` 是空实现（errNotImplemented），收包 IfIndex 恒 0，会让 Windows 一条 A 记录都不回。host 仍用 `os.Hostname()`（与旧版一致，中文主机名实测可解析）。
- 虚拟声卡名匹配：mac 找 "BlackHole"，win 找 "VB-Cable"/"CABLE Output"；找不到报错并提示 --install-driver，绝不静默落到扬声器（会啸叫）。
- 驱动安装源：mac 用 brew cask API（formulae.brew.sh/api/cask/blackhole-2ch.json）取官方 pkg URL+sha256 校验后 installer 安装（卸载=删 /Library/Audio/Plug-Ins/HAL/BlackHole2ch.driver；装/卸后均需重启电脑生效）；win 用 vb-audio 官方 VBCABLE_Driver_Pack zip，UAC 提权跑官方 setup。