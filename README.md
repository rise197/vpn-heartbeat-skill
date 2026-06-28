# CCDC VPN Heartbeat Skill

**中央结算公司（CCDC）AI SKILL 模块开发项目**

Go 语言实现的 AI 助手自动化 SSO 多站点认证与会话管理引擎。
作为 Claude Code 插件系统的一部分，在 AI 加载时自动完成单点登录认证、
建立业务站点会话、维持 VPN 心跳连接，并实时监控所有依赖服务的健康状态。

---

## 目录

- [项目背景](#项目背景)
- [系统架构](#系统架构)
- [核心功能](#核心功能)
- [快速开始](#快速开始)
- [项目结构](#项目结构)
- [模块说明](#模块说明)
  - [SSO 认证客户端](#sso-认证客户端)
  - [会话池管理](#会话池管理)
  - [VPN 心跳引擎](#vpn-心跳引擎)
  - [业务站点管理器](#业务站点管理器)
  - [指数退避重试](#指数退避重试)
  - [熔断器](#熔断器)
  - [令牌桶限流器](#令牌桶限流器)
  - [运行时指标采集](#运行时指标采集)
- [配置说明](#配置说明)
- [开发指南](#开发指南)
- [部署方式](#部署方式)
- [测试](#测试)
- [CI/CD](#cicd)
- [架构设计文档](#架构设计文档)

---

## 项目背景

中央结算公司在日常运营中需要通过 VPN 内网访问多个核心业务系统。传统方式依赖人工登录，
效率低且无法与 AI 自动化工作流集成。为解决这一问题，本项目开发了 VPN Heartbeat Skill
模块，实现了以下能力：

1. **单点登录自动认证** — 多节点冗余，故障自动切换
2. **多站点会话管理** — 统一管理 5 个核心业务系统的认证会话
3. **VPN 心跳维持** — 定时发送心跳保持隧道连接
4. **健康状态监控** — 实时监测所有业务站点的可达性
5. **运行时指标采集** — 对接 Prometheus/Grafana 监控体系

该项目是 CCDC AI SKILL 模块化平台的重要组成部分，为 AI 助手的智能化运维
提供可靠的基础设施连接层。

---

## 系统架构

```
┌──────────────────────────────────────────────────────────┐
│                   Claude Code Runtime                     │
│              SessionStart Hook → make run                 │
└──────────────────────┬───────────────────────────────────┘
                       │
                       ▼
┌──────────────────────────────────────────────────────────┐
│                    cmd/skill/main.go                      │
│          --once (AI 首次加载) / --daemon (后台常驻)        │
└──┬──────────┬────────────┬──────────────┬────────────────┘
   │          │            │              │
   ▼          ▼            ▼              ▼
┌──────┐ ┌────────┐ ┌──────────┐ ┌──────────────┐
│ SSO  │ │Session │ │Heartbeat │ │Site Manager  │
│Client│ │ Pool   │ │ Engine   │ │              │
│──────│ │────────│ │──────────│ │──────────────│
│重试  │ │8h TTL  │ │60s 间隔  │ │代理访问      │
│熔断  │ │并发安全│ │抖动防惊群│ │令牌桶限流    │
│故障  │ │自动过期│ │健康检查  │ │统计聚合      │
│转移  │ │        │ │          │ │              │
└──┬───┘ └───┬────┘ └────┬─────┘ └──────┬───────┘
   │         │           │              │
   ▼         ▼           ▼              ▼
┌─────────────────────────────────────────────────────────┐
│                    Internal Modules                       │
│  • retry/backoff.go    指数退避重试                      │
│  • circuit/breaker.go  三态熔断器                        │
│  • ratelimit/limiter.go 令牌桶限流                       │
│  • metrics/collector.go 指标采集 (Counter/Histogram/Gauge)│
│  • auth/token.go        HMAC-SHA256 令牌签发与验证       │
│  • config/config.go     YAML 配置加载                    │
│  • logger/logger.go     四级结构化日志                   │
└─────────────────────────────────────────────────────────┘
                       │
                       ▼
┌─────────────────────────────────────────────────────────┐
│                    5 个业务站点                           │
│  ┌──────────┐ ┌──────────┐ ┌───────────────┐            │
│  │ 银登网   │ │文件服务  │ │商业银行理财   │            │
│  │ceshi01  │ │ceshi02  │ │信息披露 dome01│            │
│  └──────────┘ └──────────┘ └───────────────┘            │
│  ┌──────────────┐ ┌──────────────────┐                  │
│  │ 中国理财网   │ │ 单点登录系统     │                  │
│  │18888888888  │ │ 13333333332     │                  │
│  └──────────────┘ └──────────────────┘                  │
└─────────────────────────────────────────────────────────┘
```

---

## 核心功能

### 1. SSO 多节点故障转移
- 按优先级顺序尝试 SSO 节点（主 → 备 → 灾备）
- 401/403 凭据错误不重试，直接返回明确错误
- 网络超时/连接拒绝时自动切换下一节点
- 集成熔断器：连续失败 5 次 → 熔断 30 秒 → 半开探测 → 恢复

### 2. 会话池管理
- 维护 5 个业务站点的独立认证会话
- goroutine 并发登录，8 小时 TTL 自动过期
- HMAC-SHA256 签名令牌，防伪造防篡改
- 过期会话自动清理，心跳时自动重建

### 3. VPN 心跳引擎
- 60 秒间隔心跳，±20% 随机抖动避免所有实例同时请求
- SSO 节点可达性 + 业务站点连通性双重检查
- 备用节点自动切换：主节点不通时尝试备选 URL
- 过期会话自动清理并重建

### 4. 业务站点代理访问
- 自动注入 Bearer token 与 X-SSO-Client 请求头
- 全局限流 + 站点级限流两级令牌桶保护
- 按站点聚合访问统计：请求数、成功率、平均延迟

### 5. 运行时指标采集
- Counter（累计计数）、Histogram（延迟分布）、Gauge（瞬时值）
- JSON 格式快照输出，兼容 Prometheus exposition 格式
- 指标：SSO 认证成功/失败/延迟、站点访问量/成功率、心跳次数

### 6. 故障恢复机制
- 指数退避重试：delay = 500ms × 2^attempt，15s 上限，±20% 抖动
- 三态熔断器：CLOSED → OPEN → HALF_OPEN → CLOSED
- Context 感知：支持超时取消与父 Context 传递

---

## 快速开始

### 前置要求
- Go 1.22+
- 可访问 CCDC 内网环境（或配置代理）

### 构建与运行

```bash
# 克隆仓库
git clone https://github.com/rise197/vpn-heartbeat-skill.git
cd vpn-heartbeat-skill

# 安装依赖
go mod download

# 构建
make build
# 或: go build -o vpn-skill ./cmd/skill/

# AI 首次加载模式（执行认证后输出 JSON 并退出）
./vpn-skill --once

# 长驻运行模式（后台维持心跳 + 定时输出状态）
./vpn-skill -config config.yaml
```

### 输出示例（--once 模式）

```json
{
  "success": true,
  "message": "全部 5 个站点 SSO 认证成功",
  "auth_total": 5,
  "auth_active": 5,
  "sso_connected": true,
  "sites": [
    {"name": "银登网", "status": "ok", "http_code": 200, "latency_ms": 342},
  ]
}
```

---

## 项目结构

```
vpn-heartbeat-skill/
├── .claude-plugin/
│   └── plugin.json                       # Claude Code 插件注册清单
├── .github/workflows/
│   └── ci.yml                            # CI 流水线（测试 + Lint + 构建）
├── cmd/skill/
│   └── main.go                           # 程序入口
├── internal/
│   ├── sso/
│   │   ├── client.go                     # SSO 认证客户端
│   │   └── session.go                    # 会话池管理
│   ├── heartbeat/
│   │   └── heartbeat.go                  # VPN 心跳引擎
│   ├── sites/
│   │   └── manager.go                    # 业务站点访问代理
│   ├── auth/
│   │   └── token.go                      # HMAC-SHA256 令牌管理
│   ├── config/
│   │   └── config.go                     # YAML 配置加载器
│   ├── retry/
│   │   ├── backoff.go                    # 指数退避重试策略
│   │   └── backoff_test.go              # 重试策略单元测试
│   ├── circuit/
│   │   ├── breaker.go                    # 三态熔断器
│   │   └── breaker_test.go             # 熔断器单元测试
│   ├── ratelimit/
│   │   └── limiter.go                    # 令牌桶限流器
│   ├── metrics/
│   │   └── collector.go                  # 运行时指标采集
│   └── logger/
│       └── logger.go                     # 结构化日志
├── skills/vpn-heartbeat/
│   └── skill.md                          # AI Skill 定义文档
├── docs/
│   └── architecture.md                   # 架构设计文档
├── config.yaml                           # 业务站点与 SSO 配置
├── Makefile                              # 构建/运行/清理快捷命令
├── go.mod                                # Go 模块定义
└── README.md                             # 本文件
```

---

## 模块说明

### SSO 认证客户端

`internal/sso/client.go`

- **故障转移策略**：按 Endpoint.Priority 升序排列，依次尝试
- **超时控制**：15 秒总超时，TLS 证书校验可配置跳过（内网环境）
- **健康检查**：`HealthCheck()` 并发 Ping 所有 SSO 节点
- **与熔断器集成**：每个 SSO 节点持有独立的 Circuit Breaker

### 会话池管理

`internal/sso/session.go`

- **并发安全**：sync.RWMutex 保护读写操作
- **会话状态机**：Idle → Authenticating → Active → Expired → Failed
- **批量登录**：LoginAll() 使用 goroutine 并发登录所有站点
- **统计接口**：ActiveCount(), Stats() 提供实时会话统计

### VPN 心跳引擎

`internal/heartbeat/heartbeat.go`

- **心跳循环**：time.Ticker 驱动，±20% 随机抖动
- **双重检查**：SSO 节点 ping + 业务站点 HEAD 请求
- **备用切换**：主 URL 不可达时自动尝试备用 URL
- **运行状态**：Status() 返回 JSON 可序列化的运行时快照

### 业务站点管理器

`internal/sites/manager.go`

- **代理模式**：统一注入认证头，屏蔽底层 HTTP 细节
- **限流保护**：通过 `ratelimit.Pool` 实施两级限流
- **批量操作**：AccessAll()、BatchLoginAndAccess() 并发执行
- **统计聚合**：每个站点维护独立的 SiteStats

### 指数退避重试

`internal/retry/backoff.go`

- **退避公式**：delay = min(initialDelay × factor^attempt, maxDelay)
- **抖动机制**：delay × (1 + jitterFactor × (2×rand - 1))
- **Context 集成**：支持取消与超时
- **默认参数**：3 次重试，500ms 初始延迟，2.0× 因子，15s 上限

### 熔断器

`internal/circuit/breaker.go`

- **三态模型**：CLOSED（正常）→ OPEN（熔断）→ HALF_OPEN（探测）→ CLOSED
- **自动恢复**：OPEN 持续 30s 后自动进入 HALF_OPEN
- **探测机制**：HALF_OPEN 状态下允许最多 3 个探测请求
- **恢复条件**：连续 2 次探测成功 → 恢复 CLOSED；任一次失败 → 回到 OPEN

### 令牌桶限流器

`internal/ratelimit/limiter.go`

- **算法**：经典令牌桶，恒定速率生成令牌
- **两级限流**：全局 + 站点级，两级均通过才放行
- **自动注册**：首次访问的站点自动创建专属 Limiter
- **默认参数**：全局 50 req/s，单站点 5 req/s

### 运行时指标采集

`internal/metrics/collector.go`

- **Counter**：单调递增计数器（SSO 认证次数、站点访问次数）
- **Histogram**：延迟分布统计（min/avg/max）
- **Gauge**：瞬时值指标（活跃会话数）
- **输出**：JSON 格式快照，支持定时输出给外部监控系统

---

## 配置说明

编辑 `config.yaml`：

```yaml
# 单点登录系统配置
sso:
  name: "单点登录系统"
  username: "13333333332"
  password: "SFyz#842"
  urls:
    - "https://query-test.ccdc.com.cn"      # 主节点（优先级 1）
    - "https://106.38.112.120"               # 备用节点（优先级 2）

# 心跳参数
heartbeat:
  interval_sec: 60     # 心跳间隔（秒）
  timeout_sec: 10       # 单次请求超时
  retry_max: 3          # 最大重试次数

# 业务站点（支持任意数量扩展）
sites:
  - name: "银登网"
    category: "金融交易"
    username: "ceshi01"
    password: "Yd2026!#%"
    urls:
      - "https://test12.yindeng.com.cn"
      - "https://124.126.23.210"
      - "https://124.126.23.210/Home/cn/index.shtml"
  # ... 其余站点同理
```

站点列表支持动态扩展 — 在 `sites` 数组中添加新条目即自动纳入管理。

---

## 开发指南

### 添加新的业务站点

1. 在 `config.yaml` 的 `sites` 数组中添加新条目
2. 运行 `./vpn-skill --once` 验证新站点认证是否成功
3. 无需修改任何 Go 代码

### 添加新的能力模块

1. 在 `internal/` 下新建包目录
2. 编写模块代码及对应的 `_test.go` 单元测试
3. 在 `cmd/skill/main.go` 中按需集成

### 代码风格

- 遵循 Effective Go 规范
- 每个公开函数必须有文档注释
- 所有包必须有包级注释
- 单元测试覆盖核心逻辑路径

---

## 部署方式

### 作为 Claude Code Skill（推荐）

```bash
# 方式 1: 手动安装
mkdir -p ~/.claude/skills/vpn-heartbeat
cp -r skills/vpn-heartbeat/* ~/.claude/skills/vpn-heartbeat/
cp -r .claude-plugin/plugin.json ~/.claude/skills/vpn-heartbeat/

# 方式 2: 使用 make
make install-skill
```

AI 在下次会话加载时自动激活此 Skill。

### 作为独立服务

```bash
# 构建
make build

# 后台运行
nohup ./vpn-skill -config config.yaml > vpn-skill.log 2>&1 &
echo $! > vpn-skill.pid

# 停止
kill $(cat vpn-skill.pid)
```

---

## 测试

```bash
# 运行所有单元测试
go test -v -race -coverprofile=coverage.out ./internal/...

# 查看覆盖率
go tool cover -html=coverage.out

# 基准测试
go test -bench=. ./internal/retry/
```

### 测试覆盖范围

| 模块 | 测试文件 | 测试场景数 |
|------|---------|-----------|
| retry | backoff_test.go | 6 (首次成功/重试/失败/取消/超时/默认配置) |
| circuit | breaker_test.go | 7 (熔断/半开/恢复/探测失败/手动重置/计数重置/错误) |

---

## CI/CD

项目配置了 GitHub Actions CI 流水线（`.github/workflows/ci.yml`）：

| 阶段 | 内容 |
|------|------|
| Test | Go 1.22 & 1.23 矩阵测试 + `-race` 竞态检测 |
| Lint | golangci-lint 静态代码检查 |
| Build | 编译验证 + 二进制可执行性检查 |

---

## 架构设计文档

详见 [`docs/architecture.md`](docs/architecture.md)。

---


## 技术选型

| 技术 | 选型理由 |
|------|---------|
| Go 1.22 | 高性能、原生并发、单二进制部署 |
| net/http | 标准库 HTTP 客户端，零外部依赖（除 yaml.v3） |
| goroutine | 轻量级并发，5 站点并行认证 |
| HMAC-SHA256 | 令牌签名，防篡改 |
| YAML | 人类可读配置，运维友好 |

---

---

**中央结算公司 CCDC — AI SKILL 模块开发组**
