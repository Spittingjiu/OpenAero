# OpenAero (v2.6.0)

> 基于 sing-box 原生内核的高性能轻量级 OpenWrt / iStoreOS 全栈代理路由客户端与 0-RTT 极速预热系统。

[![Release](https://img.shields.io/badge/version-v2.6.0-blue.svg)](https://github.com/Spittingjiu/OpenAero)
[![Go Version](https://img.shields.io/badge/go-1.22+-00ADD8.svg)](https://golang.org)
[![Platform](https://img.shields.io/badge/platform-OpenWrt%2023.05+%20%7C%20iStoreOS%2024.10-orange.svg)](https://openwrt.org)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

---

## 🌟 核心架构与技术亮点

### 4. 全链路原生 Clash 订阅规则解析与 Fake-IP 穿透防死锁 (v2.6.0)
- **原生提取免人工配置**：深度解析机场 Clash YAML 中的 `rules`、`dns.fake-ip-filter` 与 `dns.proxy-server-nameserver-policy`，自动转换为 sing-box `direct_rule_set_tags` 与 `custom_direct_domains`，彻底告别人工写入直连域名的繁琐操作。
- **DNS 直连 + 路由直连双轨闭环**：国内域名（如明日方舟 `hypergryph.com`、B站、腾讯、阿里等）在 DNS 解析阶段即由 `direct-dns` 捕获并解析为真实物理国内 IP，绝不落入末端 Fake-IP 池（`198.18.x.x`），实现真正的国内 0 延迟秒开与 GeoIP 直连生效。
- **MetaCubeX 每日活跃规则集生态**：上游全套切换至 MetaCubeX 17 个最新编译二进制 `.srs` 规则集（涵盖 440KB 超全国内域名库、游戏、流媒体等），支持代理与 ghproxy 双重加速镜像容灾。
- **真实 WAN 物理延迟测速（绝无 5ms 假延迟）**：严禁在 TUN 接管下使用本地 TCP 握手，全面采用 Clash API 真实代理 RTT 与 ORTT UDP Ping WAN 物理往返估算，测速公正真实。
- **大规则轻量化与防死锁保护**：自动识别 `cncidr` 9600+ 国内网段直连，29 万行广告列表二进制卸载；订阅域名与节点域名自动进入直接放行白名单，彻底消除更新死锁。

### 1. 全栈 Go 语言原生重构（Zero Overhead）
- **极简资源开销**：完全摒弃臃肿的 Python/Node.js/Java 运行时，守护进程 `openaero-core` 纯 Go 编写编译为静态二进制，运行时仅占用 **约 6~8MB** 内存。
- **高并发零频 GC**：主更新循环与多节点测速采用预分配通道与对象池，避免频繁内存分配和 GC 停顿，保障嵌入式路由器（MT7981/MT7986/MT7621/RK3568/N1 等）长期 7×24 小时稳定满帧运行。

### 2. Nftables 原子接管与 Linux TUN 透明代理
- **原子化规则注入**：全面适配 OpenWrt 23.05+ / 24.10 及 iStoreOS `fw4` / `nftables` 体系，支持 DNS 劫持（53 -> 1053）、局域网设备按 IP/MAC 旁路直连分流、IPv6 防泄漏熔断保护。
- **停机自愈与优雅卸载**：当服务停止或切换节点时，自动原子化清理 `table-post` / `chain-pre` 中的关联规则并重载防火墙，绝不残留脏规则污染宿主路由表。

### 3. ORTT v2.4 (0-RTT Prewarming Protocol) 全链路预热引擎
- **全节点常驻保活与多机并发建池**：支持对多达 10 台活跃 VPS 节点保持热池保活，全员常驻 38~77 条 Anycast 边缘热连接（Google / Cloudflare / Apple / iCloud CDN）。
- **HTTPS 443 真实热池穿透测速**：测速探针使用 `https://www.gstatic.com/generate_204` 主测速地址（走 HTTPS 443 端口，精准穿透 ORTT 热连接池），备用测速设为 `https://cp.cloudflare.com/generate_204`，支持双 URL 自动重试与 TCP 握手回退，彻底消除节点假死超时（`-1`）。
- **UDP 源端口动态轮换**：探活探测包在重试时自动更换全新随机 UDP 本地源端口，彻底免疫运营商跨洋链路对单一 UDP 五元组的 QoS 丢包。
- **三段式公正检测体系**：
  1. 控制面 UDP :39999 + HMAC-SHA256 签名鉴权；
  2. 远端数据面 39998 热池挂载状态探活；
  3. 代理隧道真实流量穿透核验与状态回填。

### 4. iStoreOS 24.10 / Argon 原生顶部导航栏深度融合
- **原生顶栏无缝契合**：通过 ucode 动态模板注入与 LuCI 菜单映射，将「运行概览」、「节点与分流」、「规则与设备」、「系统与订阅」四大核心面板完美融入 iStoreOS 原生顶栏，彻底消除双重 Tab 冗余。
- **纯原生 JavaScript 视图**：完全基于 LuCI 现代化 JS API 构建，原生实现东八区（CST UTC+8）时间对齐、`⚡ 最快` 节点推荐徽章、`🏆 优选最低延迟节点` 智能一键直达。
- **生产级日志降噪**：20 秒周期性心跳在日志中保持静默零输出，仅在节点唤醒（`0x01`）与睡眠（`0x03`）时记入审计日志，彻底根治 syslog 内存缓冲区冲刷问题。

---

## 📁 仓库目录结构

```text
OpenAero/
├── bin/
│   └── openaero-core-arm64       # 针对 ARM64 / aarch64 交叉编译的生产发布二进制
├── luci-app-openaero/            # LuCI 前端管理应用
│   ├── htdocs/                   # LuCI 原生 JavaScript 视图 (overview.js)
│   ├── luasrc/                   # 兼容传统 LuCI 控制器与 RPC 代理 (openaero.lua)
│   ├── root/                     # 系统配置文件 (UCI / etc / init.d / ACL / nftables)
│   └── Makefile                  # OpenWrt 编译打包配置
├── openaero-core/                # 守护进程核心源码 (Go 1.22+)
│   ├── main.go                   # 入口、东八区时区初始化、自愈看门狗
│   ├── pkg/
│   │   ├── api/                  # RESTful API、SSE 实时流、Token 鉴权
│   │   ├── config/               # sing-box 1.13+ 配置编译器、UCI 解析器
│   │   ├── diagnose/             # 路由分流诊断追踪引擎
│   │   ├── nodes/                # 节点测速（HTTPS 443 穿透与主备重试）
│   │   ├── prewarm/              # ORTT v2.4 预热中继、三段式探活探针
│   │   ├── subscription/         # 多格式订阅抓取、异步解析与缓存
│   │   ├── traffic/              # TUN 网卡实时带宽统计
│   │   ├── updater/              # 节点与核心动态升级管理
│   │   └── watchdog/             # 内核防死锁与断网自愈看门狗
│   ├── go.mod
│   └── go.sum
└── README.md
```

---

## 🛠️ 构建与部署

### 交叉编译 ARM64 二进制
```bash
cd openaero-core
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o ../bin/openaero-core-arm64 .
```

### 安装与部署至 OpenWrt / iStoreOS
1. 将 `bin/openaero-core-arm64` 复制至路由器 `/usr/bin/openaero-core` 并赋予执行权限：
   ```bash
   chmod +x /usr/bin/openaero-core
   ```
2. 部署 LuCI 前端文件至 `/usr/lib/lua/luci/` 与 `/www/luci-static/resources/view/openaero/`。
3. 启动并启用服务：
   ```bash
   /etc/init.d/openaero enable
   /etc/init.d/openaero restart
   ```

---

## 🔒 安全与合规说明
- 本开源仓库中所有核心代码均已完成严格安全脱敏处理，移除任何私有密钥、预置订阅或生产 VPS 凭证。
- 生产环境中使用的大规模 ORTT 鉴权密码通过 OpenWrt `/etc/openaero/config.json` 或 `ORTT_SECRET` 环境变量安全加载，确保公开代码不含任何敏感信息。

---

## 📄 开源许可证
本项目遵循 [MIT License](LICENSE)。
