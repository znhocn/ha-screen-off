# HA Screen Off (scroff)

> [English](README.md) | [中文](README.zh-CN.md)

一个零依赖、跨平台、由 **Home Assistant 驱动**的**显示器开关控制器**，带输入看门狗——**只要一碰鼠标或键盘就自动点亮屏幕**。编译为单个 `scroff` 可执行文件，支持 **Windows、macOS、Linux**。通过 HomeKit Bridge 集成还可以把项目暴露给 HomeKit，支持 Siri 语音控制。

## 特性

- **由 Home Assistant 驱动** —— 切换一个实体（entity），屏幕就跟随动作。
- **毫秒级响应** —— 订阅 HA `/api/stream`（Server-Sent Events 事件流），切开关后**毫秒级**执行；事件流之下始终有轮询兜底，即使反向代理缓冲或拦截事件流也能照常工作。
- **输入自动唤醒** —— 息屏期间任何鼠标/键盘操作立即退出息屏并点亮屏幕（只认**息屏之后**发生的输入）。
- **对抗意外亮屏** —— 无输入时保持息屏：每隔 `force_off_interval_s` 重新断言关机，抵御通知、后台程序把显示器又弄亮。
- **单个零依赖二进制** —— 仅用 Go 标准库，无运行时/无 C 工具链，任意主机交叉编译。
- **后台运行** —— `serve -d`：PID 文件、日志文件，附 `stop` / `logs` / `status` 诊断命令。

## 工作原理

```
Home Assistant (input_boolean.screen_power)  <-- 通过 REST + 事件流读取状态
        │  "off" =====//===========>           ┌────────────────┐
        │                                      │  scroff        │
        │         "on" ==============>         │  ─ 读状态       │
        │                                      │  ─ 控制显示器   │
        └── input_boolean.turn_on (唤醒时) <───│                │
                                               └───────┬────────┘
                         鼠标 / 键盘活动 ────────────────┘
                                (持续监听)
```

- **实体为 `off`** → 息屏，进入 *息屏模式（off-mode）*。
- **息屏模式下** 任何鼠标/键盘活动退出息屏、点亮屏幕，并把 Home Assistant 实体拨回 `on`，让 HA 状态保持同步。
- **否则**（无输入）屏幕保持**熄灭**：每隔 `force_off_interval_s` 重新断言关机，对抗聊天通知、后台程序等把显示器又弄亮。
- **实体为 `on`** → 点亮屏幕。

一句话：*只要你有操作，屏幕就点亮；否则保持黑暗。*

### 状态获取：事件流 + 轮询兜底

工具订阅 Home Assistant 的 `/api/stream`（**Server-Sent Events**），切换开关在**毫秒级**生效，而不是等下轮轮询。**事件流之下始终有一个每 `ha.poll_interval_s` 一次的轮询兜底**——即使反向代理静默缓冲/拦截了事件流（`mode=stream` 已 200 连接成功但事件不进来），切换也仍会在一个轮询周期内被捕获。当前事件流状态见日志：
`msg="home assistant monitoring" mode=stream|polling`。

## 编译

唯一的依赖是 Go ≥ 1.22。全部用标准库——无需 `go mod download`，无需 C 工具链，所有平台都能从一台机器交叉编译：

```sh
# Linux（X11 用 xset，或 Wayland 用 wlopm）
GOOS=linux  GOARCH=amd64 go build -o bin/scroff-linux  .

# Windows / macOS（Intel / Apple Silicon）——普通控制台程序
GOOS=windows GOARCH=amd64 go build -o bin/scroff.exe  .
GOOS=darwin  GOARCH=amd64 go build -o bin/scroff-mac   .
GOOS=darwin  GOARCH=arm64 go build -o bin/scroff-mac-m1 .
```

也可以直接把预编译好的二进制放到 `bin/`。

## 用法

```sh
scroff setup              # 交互式生成配置 -> ~/.config/scroff/config.json
scroff serve              # 前台运行看门狗（默认命令）
scroff serve -d           # 后台运行看门狗（守护进程模式）
scroff stop               # 停止后台看门狗
scroff status             # 打印配置/daemon/后端/HA/输入 诊断信息
scroff logs               # 查看后台 daemon 的日志
scroff off                # 关闭一次屏幕
scroff on                 # 点亮一次屏幕
scroff help               # 显示本帮助
scroff version            # 打印版本号（等价 -v 或 -version）
```

参数：`-config PATH` 指定配置文件，`-url`/`-token`/`-entity` 覆盖配置里的对应值，`-verbose` 开启调试日志，`-d` 让 `serve` 后台运行，`-v`/`-version` 打印版本号，`-hide-console`（仅 Windows）强制隐藏控制台窗口并静默所有输出（通常自动，见"自动运行"章节）。

直接运行 `scroff`（不带子命令）会显示帮助信息；如果配置文件还不存在，则提示你去跑 `scroff setup`。

如果不指定配置文件（`-config`），工具自动使用 `~/.config/scroff/config.json`——也就是 `setup` 写入的那个文件。典型流程：

```sh
scroff setup      # 回答问题（默认值显示在 [方括号] 里）
scroff serve -d   # 后台运行
scroff logs       # 查看是否正常启动 / 调试
scroff status     # 出问题时一键查看概览
scroff stop       # 停止
```

后台模式会写 PID 文件和日志到 `~/.config/scroff/scroff.log`；前台运行（包括计划任务启动的）也会把进程 pid 记在那里，因此 `stop` 两种方式都能结束——Linux/macOS 会通过 SIGTERM 处理器把屏幕恢复为点亮；Windows 上是强制终止（`taskkill /F`），屏幕恢复以 Home Assistant 中的实体状态为准。

## Home Assistant 配置

1. 在 Home Assistant 里创建一个 **helper**（`设置 → 设备与服务 → 辅助元素 → 添加辅助元素 → 开关`）。例如命名为 *Screen Power* → 实体 `input_boolean.screen_power`。
2. 创建**长期访问令牌**（点击你的用户名 → 安全 → 长期访问令牌）。
3. 把地址和令牌写进配置文件（参考 `examples/config.json`）：

```json
{
  "ha": {
    "url": "http://homeassistant.local:8123",
    "token": "LONG_LIVED_ACCESS_TOKEN",
    "entity_id": "input_boolean.screen_power"
  }
}
```

4. 运行 `scroff setup` 回答问题（或自己写好 `examples/config.json`），然后 `scroff serve` —— 用 `serve -d` 后台运行。

在 HA 里把开关切到**关**（仪表盘 / 自动化 / 语音），屏幕就熄了。你一动鼠标，工具就唤醒屏幕，**并自动把开关拨回开**。

> 实体可以是任意二值实体（`switch.*`、`light.*`、`input_boolean.*`……）。工具从实体 ID 推导域（domain）并调用对应的 `turn_on`/`turn_off` 服务。只有 `on`/`off` 状态有意义——其余一律忽略。

## 平台支持与要求

| 平台 | 息屏/点亮 | 空闲检测 | 依赖 |
|---|---|---|---|
| Windows | `SC_MONITORPOWER` + `SetThreadExecutionState` | `GetLastInputInfo` | 无（系统自带） |
| macOS | `pmset displaysleepnow` / `caffeinate -u` | `ioreg` `HIDIdleTime` | 无（系统自带） |
| Linux X11 | `xset dpms force off/on` | evdev `/dev/input`（或 `xprintidle`） | `xset` |
| Linux Wayland | `wlopm --off/--on` | evdev `/dev/input` | `wlopm` |
| Linux DDC/CI | `ddcutil setvcp 0xD6 ...`（选装） | evdev `/dev/input` | `ddcutil` |

平台说明：

- **Windows** 的屏幕控制要求**交互式桌面会话**。通过 SSH、或以服务/NSSM/非交互终端方式运行时，进程落在没有你桌面的会话里，息屏/点亮和输入检测都无效。请用任务计划程序的"登录时"任务（见"开机自启"）。
- **Linux evdev** 检测在 X11 和 Wayland 上都可用，不依赖显示服务器，但要能读取 `/dev/input/event*`——把用户加入 `input` 组（`sudo usermod -aG input $USER`）或以 root 运行。若设备打不开且 X11 下装了 `xprintidle`，会自动作为回退方案。
- Linux 后端按会话自动选择（`auto`），依据 `XDG_SESSION_TYPE`（Wayland 会话一律用 Wayland 后端——`xset` 只能控制 XWayland 窗口，控制不了真实输出）；也可用 `"linux_backend": "x11"`、`"wayland"` 或 `"ddc"` 强制指定。DDC/CI 是选装，因为它直接关的是显示器自身电源。
- X11/macOS 上工具可以*验证*屏幕是否真的熄灭（`xset q`、`ioreg`）；Windows 和 DDC 上不查询，而是周期性重新断言关机。
- **多显示器**下是所有屏幕**一起控制**——息屏/点亮是全局的（Windows 广播会发给每块显示器，`wlopm` 用 `*` 匹配所有输出，`xset dpms` 覆盖整个 X server），任意一块屏上有输入就全部点亮。不支持单独指定某块屏。

## 配置项参考

| 键 | 默认值 | 含义 |
|---|---|---|
| `ha.url` | *(必填)* | Home Assistant 地址 |
| `ha.token` | *(必填)* | 长期访问令牌 |
| `ha.entity_id` | `input_boolean.screen_power` | 控制屏幕的实体 |
| `ha.poll_interval_s` | `1` | 轮询兜底间隔（走事件流时用不到） |
| `ha.timeout_s` | `10` | HA 请求的 HTTP 超时 |
| `ha.insecure_tls` | `false` | HA 使用 https 且证书是自签发时设为 `true` |
| `screen.linux_backend` | `auto` | `auto` \| `x11` \| `wayland` \| `ddc` |
| `screen.force_off_interval_s` | `30` | 等待期间重新断言关机的间隔 |
| `screen.active_threshold_ms` | `2000` | 空闲时间低于此值 = 用户活跃 → 唤醒 |
| `screen.watch_interval_ms` | `250` | 息屏模式下的看门狗分辨率 |
| `log.level` | `info` | `debug` \| `info` \| `warn` \| `error` |

## 开机自启

**Linux (systemd)**

```ini
# /etc/systemd/system/scroff.service
[Unit]
Description=scroff (HA Screen Off) 显示器控制器
After=network-online.target

[Service]
ExecStart=/usr/local/bin/scroff serve -config /etc/scroff/config.json
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
```

**macOS (launchd)** —— 一个指向该二进制和配置文件的 `KeepAlive` LaunchAgent。

**Windows** —— 用**任务计划程序**创建"**登录时**"触发的任务，让 `scroff` 跑在交互式桌面会话里：

```powershell
schtasks /Create /F /TN "HA Screen Off" ^
  /TR "\"C:\Program Files\scroff\scroff.exe\" serve -config \"C:\Users\yourname\.config\scroff\config.json\"" ^
  /SC ONLOGON /RL LIMITED
```

Windows 二进制是普通的控制台程序。当任务计划段（或双击）为 scroff 创建控制台时，scroff 会用 `GetConsoleProcessList` 判断该控制台是否只有自己，如果是就在启动时自动把窗口隐藏并把 stdout/stderr 重定向到 NUL（**完全静默**）、日志改写到 `~/.config/scroff/scroff.log`（所以 `scroff logs` 依然可用），因此**桌面上不会残留黑窗口**。`-hide-console` 参数只在特殊场景下强制隐藏用（同样会静默输出）；在终端里（共享控制台）绝不会隐藏，命令行输出完全原生。

这就得到了 `serve -d` 式的看门狗体验，却又**没有孤儿进程问题**：**前台运行**（不要加 `-d`）。`serve -d` 会重新把自己变成脱离任务的独立守护进程，任务计划程序会一直显示"正在运行"且 "End" 结束不掉那个孤儿进程。而静默的前台任务进程始终是任务计划程序的直接子进程——任务计划里的"结束"、`schtasks /End /TN "HA Screen Off"`、任务管理器或 `scroff stop` 都能正常结束它，正常退出时还会恢复屏幕点亮。

> **Windows 服务（NSSM、`sc.exe` 等）无法控制屏幕也无法检测输入**：服务运行在 **Session 0**，与登录用户的桌面隔离。`SC_MONITORPOWER` 广播到不了交互会话，`SetThreadExecutionState` 唤醒那边也没有显示器，`GetLastInputInfo` 只反映服务会话的（空）输入。Vista 之后"允许服务与桌面交互"这个选项就已失效。

## 故障排查

先运行 `scroff status`——它会一次性报告配置路径、后台 pid、屏幕后端、HA 实体实时状态和输入监视器。

- **切换 HA 没反应** —— 查看 `scroff logs` 里有没有 `home assistant state changed`；没有的话说明你切的实体和配置里的 `ha.entity_id` 对不上。加 `-verbose` 还能看到逐条 `ha event` 日志。
- **日志里是 `mode=polling`** —— HA 的反向代理不支持 `/api/stream` SSE（被缓冲/拦截）。轮询仍可用；放行流式响应（GET `/api/stream`）即恢复毫秒模式。另外工具还需要 GET `/api/states/*` 和 POST `/api/services/*`（后者用于自动唤醒后把 HA 开关同步回开；若被拦截会看到 `failed to sync` 告警，但屏幕唤醒不受影响）。
- **Windows 上屏幕始终不变** —— 确认它在交互式桌面会话里运行（任务计划程序"登录时"任务），不是 SSH/服务/NSSM。
- **Linux 上动鼠标点不亮** —— 输入监视器被禁用了（见启动告警）。把用户加入 `input` 组，或安装 `xprintidle`。
- **https + 自签名证书** —— 配置里设 `"insecure_tls": true`。

## 注意事项

- 停止进程（`SIGINT`/`SIGTERM`）会把屏幕恢复为**点亮**。
- **单实例** —— 同一时刻只允许一个看门狗运行。已有实例在跑时，再次 `serve`（前台或 `-d`）会拒绝启动，因此任务计划程序多次触发（登录、解锁、重连会话……）也不会拉起多个进程互相抢屏幕。
- HA 失联时工具保留最后已知行为并（限频）记录日志，直到 HA 恢复。
- 输入监视器无法启动时（例如 `/dev/input` 不可读），工具仍会运行，但自动唤醒被禁用——启动时告警一次。
- 启动时会做依赖检查——接线前先跑 `scroff status` 诊断机器；它还会报告 HA 实体实时状态，URL/令牌/实体写错或自签名 https 证书都能立刻看出来。