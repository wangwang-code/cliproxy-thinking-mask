# cliproxy-thinking-mask（CLIProxyAPI 插件 + CPA 抢先思考补丁）

> 源码托管：<https://github.com/wangwang-code/cliproxy-thinking-mask>
> Actions 自动编译 linux/amd64 版 `cliproxy-thinking-mask.so`（见下文）。

一个针对 **CLIProxyAPI** 的 C-ABI 动态库插件（Go + cgo，`c-shared`）。
作用是**伪装思考**：当插件启用时，拦截 OpenAI 兼容的 `/v1/chat/completions`
SSE 流式响应，把**首个携带真实内容（content）的 data 帧**改写成同时带
`delta.reasoning_content`（思考文本）的帧——真实回答内容原样保留，后续帧
照常透传。这样面向的 CLI/聊天前端会先展示一段“思考中”，看起来像推理模型。

- 每个请求只注入一次（按请求 ID 跟踪）。
- 如果上游本来就返回了真实 `reasoning_content`，插件完全不改动该流。
- 非 `chat.completion.chunk` 形状（如 Claude / Gemini / OpenAI responses
  事件等）一律透传。
- 你可以在 CPA 配置里写 `keepalive-seconds`；想“仅在 keepalive 生效时才伪装”
  时，只需要同时把本插件 enable/disable（插件 ABI 只能拿到自身配置，读不到
  CPA 全局 `streaming.keepalive-seconds`，所以“启用即伪装”由插件开关控制）。

> ⚠️ **只靠插件无法“在首 token 前抢先发 thinking”**。插件只能在上游 chunk
> 到达后改写，等待上游首 token 期间 CPA 并不会调用它。要想客户端在空窗期就
> 看到 thinking，必须使用本仓库 `_cpa-overlay/` 的 CPA 核心补丁（见下文
> “CPA 抢先思考补丁”）。

## 产物与放置

### 方式一：GitHub Actions 自动编译（推荐）

仓库每次 push / PR 都会由 Actions（`.github/workflows/build.yml`）在
`ubuntu-latest` 上构建两个 artifact：

1. `cliproxy-thinking-mask-linux-amd64`：插件 `.so`。
2. `cliproxy-thinking-mask-cpa-linux-amd64`：打过抢先思考补丁的
   CLIProxyAPI linux/amd64 可执行文件（`cli-proxy-api-linux-amd64`）。

### 方式二：本地在 linux/amd64 上构建

在 linux/amd64 机器上（或 linux/amd64 构建容器内）构建：

```bash
bash build.sh
```

得到 `cliproxy-thinking-mask.so`。把它放到 CPA 的插件目录（二选一）：

- `plugins/cliproxy-thinking-mask.so`
- `plugins/linux/amd64/cliproxy-thinking-mask.so`（推荐，按 goos/goarch 分目录）

> 文件名去掉 `.so` 后的名字就是插件 ID（`cliproxy-thinking-mask`），配置里
> `plugins.configs.cliproxy-thinking-mask` 必须与之对应。

## 启用（CPA config.yaml）

```yaml
plugins:
  enabled: true            # 打开整个插件宿主
  # dir: plugins           # 默认 plugins；如换了目录在这里指定
  configs:
    cliproxy-thinking-mask:
      enabled: true        # 插件启用即开始伪装思考
      priority: 100        # 优先级（数值越大越先执行）
      thinking-text: "Let me think through this step by step before giving the final answer."  # 可选，思考文本
```

配置项：

| 键 | 说明 |
| --- | --- |
| `enabled` | 宿主字段，必须 `true` 插件才会被加载并拦截 |
| `priority` | 宿主字段，决定插件在拦截链中的顺序 |
| `thinking-text` | 可选，注入到首个内容帧 `delta.reasoning_content` 的文本；缺省用内置默认文案 |

示例（等价配置放 CPA 配置里即可）：

```yaml
plugins:
  configs:
    cliproxy-thinking-mask:
      enabled: true
      priority: 100
      thinking-text: "我先理清一下需求，然后给出准确回答。"
```

## CPA codex OAuth 登录按请求走代理补丁（第三个 overlay 产物）

> 背景：管理面板做 codex OAuth 登录时，CPA 后端在“开始登录 → 提交回调 →
> code 换 token → 后续认证出站”这条链路上会向 auth.openai.com 发请求。默认
> 走全局 `proxy-url`，换机器/多代理时常要反复改全局代理，且可能漏出 CPA 本机
> 直连出口。
>
> 本补丁让**单次登录请求自带代理**：页面顶部的代理地址随发起请求传给 CPA，
> 这次登录的全部 CPA 出站都走它，不碰全局配置。

### 后端接口约定（已实现）

`GET /v0/management/codex-auth-url` 现在接受：

- query：`?proxy-url=socks5://user:pass@host:1080`
- 或请求头：`X-Proxy-URL: socks5://user:pass@host:1080`

当 `proxy-url` 非空时，CPA 用
`codex.NewCodexAuthWithProxyURL(cfg, proxyURL)` 构造本次登录的 auth 服务，
使该次登录里 code→token 交换等 CPA 出站全部走这个代理；为空则回落到全局
`proxy-url` 原行为。

> 覆盖的是 **CPA 本机向 provider 发出的请求**。浏览器打开授权页、以及本地
> 回调收码这两段不经过 CPA（浏览器直连 auth.openai.com），不受此开关影响——
> 这是“避免漏出 CPA 本机出口”的语义范围。

### 前端/插件要做的

在发起 codex OAuth 登录的页面顶部提供“代理地址”输入，调用
`/v0/management/codex-auth-url` 时把该值作为 `proxy-url` query（或
`X-Proxy-URL` 头）带上即可；登录页面轮询/回调逻辑无需其它改动。

## CPA 抢先思考补丁（本仓库另一个产物）

### 它解决什么

原始 CPA 对 `/v1/chat/completions` 流式请求采用“先窥探第一个完整上游帧，
成功后才提交 SSE 头”。而且 `ExecuteStreamWithAuthManager` 会**同步阻塞读上游
直到首个 payload 到达才返回**——所以若在它返回之后才发 thinking 帧，那帧和
真实正文几乎同时到，快模型上根本看不出“思考”。

本补丁在 `streaming.keepalive-seconds > 0` 时改成**抢先模式**：

1. 请求一进来（t=0）就**立即**提交 `text/event-stream` 响应头，并立刻发一个
   只有 `delta.reasoning_content`（无 `content`）的 `chat.completion.chunk`
   帧——客户端马上进入“思考中”；
2. 上游执行（含其同步 bootstrap）放到后台 goroutine；
3. 在等上游首个真实 chunk 期间，按 keepalive 周期持续发 `: keep-alive` 心跳；
   如果配置了 `streaming.fake-thinking-texts`，每个 keepalive 还会**按列表顺序**
   额外发一个 `delta.reasoning_content` 假 thinking 帧（第 1 次 Keepalive 用
   第 1 条、第 2 次用第 2 条……列表耗尽后恢复纯心跳）；
4. 上游首 chunk 一就绪，立即接上 `handleStreamResult` 原样转发真实流；随后
   插件（如上）会在真实首内容帧上再补一个 `reasoning_content`，形成
   “思考中 → 正式回答”两段式。

`streaming.keepalive-seconds` 未设置或为 0 时保持原始行为（仍先窥探首帧，
上游早错会按 JSON 错误返回）。

> ⚠️ 抢先模式的取舍（仅 keepalive>0 时生效）：
> - 上游响应头**无法透传**——它们只在上游首字节后才存在，而此刻响应已提交；
>   因此启用抢先模式时请关掉 `passthrough-headers`（否则这些流会拿不到
>   响应头，属于预期行为，不是 bug）。
> - 上游若在首字节前报错，会以 SSE 错误帧呈现，而**不是** HTTP 4xx JSON
>   （这是“抢先”的必然代价）。

### 配置

```yaml
streaming:
  keepalive-seconds: 5      # > 0 才启用“开流即抢先发 thinking”，同时保持心跳
  fake-thinking-text: "我先理清一下需求，然后给出准确回答。"  # 可选，开流首帧，缺省用内置英文文案
  fake-thinking-texts:      # 可选：每次 Keepalive 额外发的假 thinking 文案，按顺序取用
    - "第一次Keepalive的文案"
    - "第二次"
    - "第三次"
    - "第四次"

passthrough-headers: false  # 建议关掉：抢先模式下上游响应头无法透传（见上）
```

### 直接使用 Actions 产物

不用自己打补丁：每次 Actions 的 **build-patched-cpa** job 会：

1. checkout CLIProxyAPI（固定到补丁基线 commit）；
2. 用本仓库 `_cpa-overlay/` 覆盖 3 个文件；
3. 执行 `go test`（新增早发帧构建测试）；并
4. 用 `GOOS=linux GOARCH=amd64 CGO_ENABLED=1 go build` 产出
   `cli-proxy-api-linux-amd64` 可执行文件，作为 artifact 上传。

把该 artifact 解压出的二进制替换线上 CPA 可执行文件（注意先备份原文件），
再配合 `streaming.keepalive-seconds > 0` 即可看到效果。

> `_cpa-overlay/` 只包含补丁涉及的文件，完整 CPA 源码在
> <https://github.com/router-for-me/CLIProxyAPI>（本补丁基线 commit
> `d198db54d4c4886c99b21488d54fc576933019a3`）。后续 CPA 上游更新后若打不上
> 覆盖，需要重新基于新基线做小改。

## CPA codex 过载静默降级补丁（第二个 overlay 产物，与 thinking 共存）

> 背景：生产以 **codex(OAuth)** 池为主力，OpenAI 过载时 CPA 会收到
> `server_is_overloaded`（502/503），内置 failover 只会在同一批 codex 凭据里
> 轮换；全部 codex 账号过载时 502 直出到客户端，**不会**尝试 grok 等
> OpenAI 兼容端点。
>
> 纯 `.so` 插件做不到“观察到 502 后换 provider 重发”（插件 ABI 无此回调，
> 且 codex OAuth 只能由 CPA 宿主执行），所以这也是 CPA overlay，与抢先思考
> 补丁在同一个 `_cpa-overlay/` / 同一份 Actions 产物里，互不影响。

### 行为

`/v1/chat/completions`（payload 带 `messages`）在主 provider 返回
overload/502/503（bootstrap 阶段）时：

1. 依次尝试 `failover.endpoints` 里列出的 **OpenAI-compatible 端点**（按
   priority 升序；每个必须已在 `openai-compatibility` 里 `disabled:false`
   启用，否则不可用）；
2. 任一成功 → 透明接管（流式下客户端只会看到思考/心跳变长，然后接该端点
   的真实内容；非流式直接返回其响应）；
3. 全部失败 → 返回合成错误（`terminal-*`，默认）：
   ```json
   {"error":{"type":"service_unavailable_error","code":"server_is_overloaded","message":"云翻译服务暂不可用，请稍后重试","param":null}}
   ```
   HTTP 状态默认 502。

> 端点可选 `model` 覆盖（发给该端点的模型名，不依赖 `openai-compatibility`
> 里填的 alias）；留空则用原请求模型、由该端点的 alias 映射解析。

### 配置示例

```yaml
failover:
  enabled: true
  primary-providers:
    - codex
  endpoints:
    - name: "grok web"     # 必须匹配一个已启用的 openai-compatibility 名
      priority: 1
      model: ""            # 可选覆盖模型；留空用原模型
  terminal-status: 502
  terminal-type: "service_unavailable_error"
  terminal-code: "server_is_overloaded"
  terminal-message: "云翻译服务暂不可用，请稍后重试"
```

### 生效前提

- 使用本仓库 Actions 产物里的 **patched CPA**（同时含 thinking + failover）；
- `failover.enabled: true`，且想用的降级端点在 `openai-compatibility` 里
  `disabled: false`（例如把 `grok web` 打开）；
- 只作用于 `/v1/chat/completions`，其它入口不受影响。

## CPA 上游错误消息改写补丁（同 overlay，新增）

> 背景：上游 429/502/503 等错误原来会把上游原始 error body、请求头以及
> 上游身份直接透给客户端；不利于隐藏“云翻译”背后到底打到哪家。此补丁在
> CPA 把执行错误转成客户端响应前，按配置的状态码替换成自定义 message，并
> 丢弃上游 body/headers。

### 配置示例

```yaml
error-rewrite:
  enabled: true
  default-message: "[云翻译]上游服务暂时不可用，请稍后重试"
  status-messages:
    "429": "[云翻译]被上游限流，请稍微再试"
    "502": "[云翻译]上游服务暂时不可用，请稍后重试"
    "503": "[云翻译]上游服务暂时不可用，请稍后重试"
```

- 命中的响应保留原 HTTP 状态码，body 改为标准 OpenAI error JSON，
  `message` 用配置值；
- 未配置的状态：若 `default-message` 非空则用默认，否则原样透出；
- 覆盖普通 HTTP、流式错误，以及 `/v1/responses` WebSocket 的 upstream
  disconnect error。

## 工作原理（首帧改写插件）

对每个流式 payload 帧做如下判断：

1. 不是 OpenAI `chat.completion.chunk` JSON → 透传。
2. 该帧任一 choice 的 `delta.reasoning_content` 已有非空文本 → 视为原生推理
   流，透传，且本请求之后不再尝试注入。
3. 否则找到**首个** `delta.content` 为非空字符串的 choice，给它的
   `delta.reasoning_content` 写入 thinking-text，`content` 原样保留，其余字段
   不动（用 JSON 对象级修改，不会丢 tool_calls 等扩展字段）。
4. 同一请求后续内容帧透传（只注入一次）。

示例改写前/后的首个 data 帧：

```jsonc
// 前
{"id":"chatcmpl-..","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"好的，我来帮你。"},"finish_reason":null}]}

// 后（content 保留，新增 reasoning_content）
{"id":"chatcmpl-..","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"好的，我来帮你。","reasoning_content":"我先理清一下需求，然后给出准确回答。"},"finish_reason":null}]}
```

## CPA 抢先 thinking 帧示例（补丁新增）

```jsonc
// 开流后立即发出的第一帧（只有 reasoning_content，没有 content）
{"id":"chatcmpl-thinking-...","object":"chat.completion.chunk","created":...,"model":"...","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"我先理清一下需求，然后给出准确回答。"},"finish_reason":null}]}
```

## 本地测试 / 其它平台

插件主体依赖 cgo，只能在启用 cgo 的环境里编译成 `.so`；纯逻辑放在
`internal/mask` 子包，不依赖 cgo，可在任意平台跑测试：

```bash
go test ./internal/mask
```

## 目录结构

```
cliproxy-thinking-mask/
├── go.mod
├── main.go                    # cgo 薄入口：导出 C-ABI 函数（仅 cgo 构建时编译）
├── rpc.go                     # 宿主 RPC 分发：plugin.register/reconfigure + 流拦截
├── main_cgo_disabled.go       # 非 cgo 编译占位（便于无 C 工具链机器跑测试/构建）
├── build.sh                   # linux/amd64 c-shared 构建
├── config.example.yaml        # CPA 配置片段
├── _cpa-overlay/              # CPA 补丁（thinking + codex 过载降级 + 上游错误消息改写）：覆盖到 CLIProxyAPI 源码根目录
│   ├── internal/config/sdk_config.go        # 新增 failover/error-rewrite 字段
│   ├── internal/config/sdk_failover.go      # failover 配置类型
│   ├── internal/config/sdk_error_rewrite.go # 上游错误消息改写配置类型
│   ├── internal/config/sdk_error_rewrite_test.go
│   ├── sdk/api/handlers/handlers_failover.go        # 降级判定/终端错误构造
│   ├── sdk/api/handlers/handlers_failover_test.go
│   ├── sdk/api/handlers/handlers_error_rewrite.go   # 错误消息改写逻辑
│   ├── sdk/api/handlers/handlers_error_rewrite_test.go
│   ├── sdk/api/handlers/handlers_execution.go       # 非流式降级接入 + 错误改写
│   ├── sdk/api/handlers/handlers_stream.go          # 流式降级接入 + 错误改写
│   ├── sdk/api/handlers/openai/openai_handlers.go   # 抢先 thinking + 按序 Keepalive 假 thinking
│   ├── sdk/api/handlers/openai/openai_handlers_early_test.go
│   ├── sdk/api/handlers/openai/openai_handlers_keepalive_test.go
│   └── sdk/api/handlers/openai/openai_responses_websocket.go # websocket 错误改写
├── README.md
├── internal/mask/
│   ├── mask.go                # 纯逻辑：首帧 thinking 改写 + 请求级跟踪
│   ├── config.go              # 解析插件自身 YAML 配置
│   └── mask_test.go           # 纯逻辑单元测试
└── rpc_test.go                # RPC 信封/拦截分发测试
```