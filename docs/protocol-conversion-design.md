# Peer 协议转换系统设计文档

## 1. 项目概述

### 1.1 背景

llama-swap 当前通过 peers 配置连接远程 API，但仅支持透传转发，不支持协议转换。用户希望客户端可以用任意格式（OpenAI 或 Anthropic）访问配置为 Anthropic 格式的远程 peer。

### 1.2 目标

实现双向协议转换功能：

1. **自动检测**：根据请求路径自动识别客户端使用的格式
2. **按需转换**：根据配置的 upstreamFormat 决定是否需要转换
3. **透明代理**：对客户端和上游都保持透明
4. **渐进交付**：先非流式，后流式

### 1.3 术语

- **客户端格式**：客户端实际发送的格式（OpenAI/Anthropic）
- **上游格式**：远程 peer 期望的格式（通过配置指定）
- **转换**：请求体和响应体在不同格式间的映射

## 2. 架构设计

### 2.1 整体架构

```
┌─────────────────────────────────────────────────────────────────┐
│                        Client Request                            │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│  [1] Auto-Detect Format                                          │
│  Path: /v1/chat/completions → OpenAI                             │
│  Path: /v1/messages → Anthropic                                  │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│  [2] Check if Conversion Needed                                  │
│  clientFormat != upstreamFormat?                                 │
└─────────────────────────────────────────────────────────────────┘
              │                           │
       Yes (need)                    No (passthrough)
              │                           │
              ▼                           ▼
┌────────────────────┐          ┌────────────────────┐
│ [3a] Transform     │          │ [3b] Direct Proxy  │
│ - Path rewrite     │          │ - No modification  │
│ - Body conversion  │          │ - Transparent      │
└────────────────────┘          └────────────────────┘
              │                           │
              └───────────┬───────────────┘
                          ▼
┌─────────────────────────────────────────────────────────────────┐
│  [4] Send to Upstream                                            │
│  (with proper headers)                                           │
└─────────────────────────────────────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────────────┐
│  [5] Transform Response (if needed)                              │
│  upstreamFormat → clientFormat                                   │
└─────────────────────────────────────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────────────┐
│  [6] Return to Client                                            │
│  (in client's expected format)                                   │
└─────────────────────────────────────────────────────────────────┘
```

### 2.2 核心组件

| 组件                  | 职责                          | 位置                         |
|----------------------|-------------------------------|-----------------------------|
| `FormatDetector`     | 自动检测客户端格式             | `proxy/protocol/detector.go` |
| `RequestConverter`   | 请求转换 (OpenAI↔Anthropic)   | `proxy/protocol/converter.go`|
| `ResponseConverter`  | 响应转换                      | `proxy/protocol/converter.go`|
| `TransformingWriter` | 包装 ResponseWriter 进行转换   | `proxy/protocol/writer.go`   |
| `PeerConfig`         | 新增 upstreamFormat 配置字段   | `proxy/config/peer.go`       |

## 3. 详细设计

### 3.1 配置方案

```yaml
peers:
  anthropic-api:
    proxy: "https://api.anthropic.com"
    apiKey: "sk-ant-xxx"
    models: [claude-3-opus, claude-3-sonnet]
    
    # 新增：指定上游期望的格式
    upstreamFormat: "anthropic"  # 可选值: "anthropic", "openai"
                                  # 默认: "openai"（与现有行为兼容）
    
    # 新增：是否启用协议转换
    convert: true                # true = 自动检测并转换
                                  # false = 透传（默认）
```

**设计原则**：
- 向后兼容：`convert` 默认为 `false`，现有配置无需修改
- 显式配置：用户必须显式启用转换，避免意外行为
- 单向上游格式：只需要指定上游格式，客户端格式自动检测

### 3.2 自动格式检测

```go
// detector.go
func DetectClientFormat(path string) ProtocolFormat {
    switch {
    case strings.HasSuffix(path, "/v1/messages"):
        return FormatAnthropic
    case strings.HasSuffix(path, "/v1/chat/completions"):
        return FormatOpenAI
    default:
        return FormatUnknown
    }
}
```

**检测规则**：
- `/v1/messages` → Anthropic
- `/v1/chat/completions` → OpenAI
- 其他 → 根据 upstreamFormat 推断（或报错）

### 3.3 请求转换

#### OpenAI → Anthropic

| OpenAI 字段              | Anthropic 字段                | 转换逻辑                |
|-------------------------|------------------------------|------------------------|
| `model`                 | `model`                      | 保留（peer 配置中的模型） |
| `messages[]`            | `messages[]`                 | 结构相似，直接映射       |
| `max_tokens`            | `max_tokens`                 | 直接映射                |
| `temperature`           | `temperature`                | 直接映射                |
| `stream`                | `stream`                     | 直接映射                |
| `top_p`                 | `top_p`                      | Anthropic 支持，可选     |
| `stop`                  | `stop_sequences`             | 字段名转换               |
| `presence_penalty`      | -                            | 移除（Anthropic 不支持） |
| `frequency_penalty`     | -                            | 移除                     |
| `tools`                 | `tools`                      | Phase 2 支持             |

**路径重写**：`/v1/chat/completions` → `/v1/messages`

**Headers**：
- 添加 `anthropic-version: 2023-06-01`
- 保留 `x-api-key`（由 apiKey 配置自动添加）

#### Anthropic → OpenAI

| Anthropic 字段          | OpenAI 字段                  | 转换逻辑                |
|------------------------|-----------------------------|------------------------|
| `model`                | `model`                     | 保留                    |
| `messages[]`           | `messages[]`                | 结构相似，直接映射       |
| `max_tokens`           | `max_tokens`                | 直接映射                |
| `temperature`          | `temperature`               | 直接映射                |
| `stream`               | `stream`                    | 直接映射                |
| `stop_sequences`       | `stop`                      | 字段名转换               |
| `tools`                | `tools`                     | Phase 2 支持             |

**路径重写**：`/v1/messages` → `/v1/chat/completions`

### 3.4 响应转换

#### Anthropic → OpenAI (非流式)

Anthropic 响应结构：
```json
{
  "id": "msg_01...",
  "type": "message",
  "role": "assistant",
  "content": [
    {"type": "text", "text": "Hello"}
  ],
  "model": "claude-3-opus",
  "usage": {
    "input_tokens": 10,
    "output_tokens": 5
  }
}
```

转换为 OpenAI：
```json
{
  "id": "msg_01...",
  "object": "chat.completion",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "Hello"
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 10,
    "completion_tokens": 5,
    "total_tokens": 15
  }
}
```

#### OpenAI → Anthropic (非流式)

反向转换，字段映射见上表。

### 3.5 错误响应转换

Anthropic 错误格式：
```json
{
  "type": "error",
  "error": {
    "type": "invalid_request_error",
    "message": "..."
  }
}
```

转换为 OpenAI：
```json
{
  "error": {
    "message": "...",
    "type": "invalid_request_error"
  }
}
```

## 4. 流式响应处理 (Phase 2)

### 4.1 SSE 格式差异

**OpenAI SSE**：
```
data: {"choices": [{"delta": {"content": "Hello"}}]}

data: {"choices": [{"finish_reason": "stop"}]}

data: [DONE]
```

**Anthropic SSE**：
```
event: content_block_delta
data: {"delta": {"type": "text_delta", "text": "Hello"}}

event: message_stop
data: {"type": "message_stop"}
```

### 4.2 转换策略

需要实时逐行解析和转换 SSE 事件：

1. **检测 SSE**：通过 `Content-Type: text/event-stream` 识别
2. **逐行处理**：读取上游响应的每一行
3. **事件类型映射**：
   - `content_block_delta` → `data:`
   - `message_stop` → `data: [DONE]`
4. **数据转换**：转换 JSON 结构后发送给客户端

## 5. 实现计划

### Phase 1: MVP - 非流式支持 (3-4 天)

**Day 1: 基础设施**
- [ ] 创建 `proxy/protocol/` 包
- [ ] 实现 `FormatDetector`
- [ ] 定义转换接口 `ProtocolConverter`

**Day 2: 请求转换**
- [ ] 实现 OpenAI→Anthropic 请求转换
- [ ] 实现 Anthropic→OpenAI 请求转换
- [ ] 配置解析增强（upstreamFormat, convert 字段）

**Day 3: 响应转换**
- [ ] 实现非流式响应转换（双向）
- [ ] 实现 `TransformingWriter` 包装器
- [ ] 集成到 `PeerProxy.ProxyRequest()`

**Day 4: 测试与修复**
- [ ] 单元测试（请求/响应转换）
- [ ] 集成测试（端到端）
- [ ] 错误处理测试
- [ ] Bug 修复

### Phase 2: 流式支持 (3-4 天)

- [ ] SSE 流式检测与解析
- [ ] 实时事件转换
- [ ] 流式错误处理
- [ ] 流式测试

### Phase 3: 高级功能 (可选)

- [ ] 工具调用转换
- [ ] 图片输入转换
- [ ] 系统消息特殊处理
- [ ] 性能优化（零拷贝、池化）

## 6. 接口定义

```go
// proxy/protocol/types.go

type ProtocolFormat string

const (
    FormatOpenAI    ProtocolFormat = "openai"
    FormatAnthropic ProtocolFormat = "anthropic"
    FormatUnknown   ProtocolFormat = "unknown"
)

// Converter 协议转换器接口
type Converter interface {
    // 转换请求（path 和 body）
    TransformRequest(path string, body []byte) (newPath string, newBody []byte, err error)
    
    // 转换非流式响应
    TransformResponse(body []byte) ([]byte, error)
    
    // 是否为流式响应
    IsStreaming(headers http.Header) bool
    
    // 获取流式转换器（Phase 2）
    GetStreamConverter() StreamConverter
}

// StreamConverter 流式转换器（Phase 2）
type StreamConverter interface {
    // 转换 SSE 数据行
    TransformStreamLine(line []byte) ([]byte, error)
}

// TransformingWriter 包装 http.ResponseWriter 进行响应转换
type TransformingWriter struct {
    http.ResponseWriter
    converter   Converter
    buf         *bytes.Buffer
    wroteHeader bool
}
```

## 7. 代码变更点

### 7.1 新增文件

```
proxy/protocol/
├── types.go          # 类型定义
├── detector.go       # 格式检测
├── converter.go      # 转换器接口与工厂
├── openai_to_anthropic.go  # O→A 转换实现
├── anthropic_to_openai.go  # A→O 转换实现
└── writer.go         # TransformingWriter
```

### 7.2 修改文件

```
proxy/config/peer.go       # 新增 upstreamFormat, convert 字段
proxy/peerproxy.go         # 集成转换逻辑
```

## 8. 风险评估

| 风险                     | 可能性 | 影响 | 缓解措施                           |
|-------------------------|--------|------|-----------------------------------|
| 流式转换性能问题          | 中     | 中   | Phase 1 不做流式，Phase 2 充分测试 |
| 字段映射遗漏              | 中     | 高   | 完整测试覆盖，快速迭代              |
| 与现有功能冲突            | 低     | 高   | 默认关闭，显式启用                  |
| 错误处理不一致            | 中     | 中   | 统一错误转换逻辑                    |

## 9. 测试策略

### 9.1 单元测试

- 每种字段的转换正确性
- 边界情况（空字段、缺失字段）
- 错误响应转换

### 9.2 集成测试

- OpenAI 客户端 → Anthropic 上游（非流式）
- Anthropic 客户端 → OpenAI 上游（非流式）
- 路径保留（不需要转换时）

### 9.3 手动测试

使用 curl 测试真实场景：

```bash
# 测试 OpenAI 格式访问 Anthropic peer
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model": "claude-3-opus", "messages": [{"role": "user", "content": "hi"}]}'
```

## 10. 文档更新

- [ ] 配置文档更新（添加 upstreamFormat, convert 说明）
- [ ] README 更新（功能介绍）
- [ ] 示例配置（anthropic-openai.yaml）

---

**版本**: 1.0  
**日期**: 2025-03-18  
**状态**: 设计完成，待实现
