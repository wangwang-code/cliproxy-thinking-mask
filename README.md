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

## CPA 抢先思考补丁（本仓库另一个产物）

### 它解决什么

原始 CPA 对 `/v1/chat/completions` 流式请求采用“先窥探第一个完整上游帧，
成功后才提交 SSE 头”。在等待上游首 token 期间，客户端收不到任何字节：
转圈/空白、或显示“无响应”。本补丁在 `streaming.keepalive-seconds > 0` 时
改变该行为：

1. 请求进来后**立即**提交 `text/event-stream` 响应头；
2. 立刻发送一个只有 `delta.reasoning_content`（无 `content`）的
   `chat.completion.chunk` 帧——客户端会先渲染“思考中”；
3. 之后保持原有的 Keepalive 心跳，直到上游真实 chunk 到达，再原样转发；
4. 真实首帧到达后，插件（如上所述）会在该帧上再补一个
   `reasoning_content`，因此整个流看起来像“先思考、后正式回答”。

`streaming.keepalive-seconds` 未设置或为 0 时保持原始行为（仍先窥探首帧，
上游早错会按 JSON 错误返回）。

### 配置（只需在原有 streaming 段下加一行可选文案）

```yaml
streaming:
  keepalive-seconds: 5      # > 0 才启用“开流即发思考”，同时保持原有心跳
  fake-thinking-text: "我先理清一下需求，然后给出准确回答。"  # 可选，缺省用内置英文文案
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
├── _cpa-overlay/              # CPA 抢先思考补丁：覆盖到 CLIProxyAPI 源码根目录
│   ├── internal/config/sdk_config.go
│   └── sdk/api/handlers/openai/openai_handlers.go
│       └── openai_handlers_early_test.go
├── README.md
├── internal/mask/
│   ├── mask.go                # 纯逻辑：首帧 thinking 改写 + 请求级跟踪
│   ├── config.go              # 解析插件自身 YAML 配置
│   └── mask_test.go           # 纯逻辑单元测试
└── rpc_test.go                # RPC 信封/拦截分发测试
```