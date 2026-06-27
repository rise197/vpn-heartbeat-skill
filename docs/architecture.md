# CCDC VPN Heartbeat Skill — 架构设计文档

## 1. 项目背景

中央结算公司（CCDC）在日常运营中需要通过 VPN 内网访问多个核心业务系统，
包括银登网、文件服务上传管理系统、商业银行理财业务信息披露平台、中国理财网等。
为实现 AI 助手的自动化运维能力，开发了本 VPN Heartbeat Skill 模块，
作为 Claude Code 插件系统的一部分，在 AI 加载时自动完成 SSO 认证与多站点会话管理。

## 2. 设计目标

- **自动化**: AI 加载 Skill 时无需人工干预，自动完成认证
- **高可用**: 多节点故障转移、熔断保护、指数退避重试
- **可观测**: 结构化日志、运行时指标采集、JSON 状态快照
- **防抖与限流**: 令牌桶限流防止对业务站点造成压力
- **优雅降级**: 部分站点失败不影响整体服务

## 3. 系统架构

```
                    ┌──────────────────────────┐
                    │    Claude Code Runtime    │
                    │  (SessionStart Hook 触发) │
                    └────────────┬─────────────┘
                                 │
                                 ▼
                    ┌──────────────────────────┐
                    │   cmd/skill/main.go       │
                    │   --once / --daemon       │
                    └────────────┬─────────────┘
                                 │
          ┌──────────────────────┼──────────────────────┐
          │                      │                      │
    ┌─────▼──────┐      ┌───────▼──────┐      ┌───────▼──────┐
    │ SSO Client │      │ Session Pool │      │  Heartbeat   │
    │  (retry +  │──────▶  (8h TTL)   │◀─────│   Engine     │
    │  circuit)  │      └───────┬──────┘      │  (60s tick)  │
    └────────────┘              │             └──────────────┘
                                │
                        ┌───────▼──────┐
                        │ Site Manager │
                        │  (ratelimit) │
                        └───────┬──────┘
                                │
          ┌─────────────────────┼─────────────────────┐
          │                     │                     │
    ┌─────▼──────┐      ┌──────▼─────┐      ┌───────▼──────┐
    │  银登网    │      │ 文件上传   │      │  中国理财网  │  ...
    └────────────┘      └────────────┘      └──────────────┘
```

## 4. 核心模块

### 4.1 SSO Client (`internal/sso/client.go`)

- 多节点故障转移：按优先级依次尝试，401/403 直接失败
- 集成指数退避重试 (`internal/retry/backoff.go`)
- 集成熔断器保护 (`internal/circuit/breaker.go`)
- 每个节点维护独立的健康状态

### 4.2 会话池 (`internal/sso/session.go`)

- 维护 5 个业务站点的认证会话
- 并发安全（sync.RWMutex）
- 8 小时 TTL 自动过期
- 支持并发登录（goroutine pool）

### 4.3 心跳引擎 (`internal/heartbeat/heartbeat.go`)

- 60 秒间隔心跳，±20% 随机抖动防惊群
- SSO 节点可达性检查
- 业务站点连通性探测（HEAD 请求）
- 过期会话自动清理

### 4.4 站点管理器 (`internal/sites/manager.go`)

- 代理访问：自动注入 Bearer token
- 令牌桶限流（`internal/ratelimit/limiter.go`）
- 按站点聚合访问统计

### 4.5 重试策略 (`internal/retry/backoff.go`)

- 指数退避：delay = initialDelay × backoffFactor^attempt
- 随机抖动：±20% 扰动避免重试风暴
- Context 感知：支持取消和超时

### 4.6 熔断器 (`internal/circuit/breaker.go`)

- 三态状态机：CLOSED → OPEN → HALF_OPEN → CLOSED
- 自动恢复：冷却后进入半开探测
- 手动重置接口

### 4.7 限流器 (`internal/ratelimit/limiter.go`)

- 令牌桶算法，支持全局 + 站点级两级限流
- 首次访问站点自动创建专属限流器

### 4.8 指标采集 (`internal/metrics/collector.go`)

- Counter / Histogram / Gauge 三类指标
- JSON 快照输出，可对接 Prometheus

## 5. 数据流

```
1. AI 加载 SKILL
2. SessionStart Hook → make build && ./vpn-skill --once
3. 读取 config.yaml
4. SSO Client 按优先级认证 (retry + circuit breaker)
5. 认证成功 → Session Pool 并发登录所有站点
6. Site Manager 对各站点发起探测请求
7. 输出 JSON 结果给 AI 解析
8. (长驻模式) Heartbeat Engine 启动，定时心跳
```

## 6. 状态码规范

| 状态 | 含义 |
|------|------|
| 200 | 认证/访问成功 |
| 401 | 凭据无效 |
| 403 | 权限不足 |
| 503 | SSO 全部节点不可达 |
| 429 | 触发限流 |

## 7. 性能指标

| 指标 | 目标值 |
|------|--------|
| SSO 认证 P50 延迟 | < 500ms |
| 站点探测 P50 延迟 | < 200ms |
| 会话建立并发数 | 5 (goroutine) |
| 心跳间隔 | 60s ± 20% |
| 熔断恢复时间 | 30s |
| 限流默认速率 | 5 req/s/站点 |
