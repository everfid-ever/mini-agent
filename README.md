# mini-agent

从零手写的最小可用 Agent:不依赖任何 agent 框架(无 langgraph / eino / langchain),
核心 ReAct 循环、工具注册、多 session、context 压缩全部自行实现,LLM 调用为裸 HTTP(OpenAI 兼容接口)。

配套文档:[PLAN.md](PLAN.md) 实现方案(写码前定稿,含设计不变量与修订记录)、
[PROMPTS.md](PROMPTS.md) AI Prompt 与问题解决记录(PLAN 先行的开发工作流)。

## 运行方式

需要 Go 1.21+ 和一个 OpenAI 兼容的 LLM API Key(默认 DeepSeek,便宜)。

```powershell
# PowerShell
$env:LLM_API_KEY  = "sk-xxx"
$env:LLM_BASE_URL = "https://api.deepseek.com/v1"   # 可选,默认此值
$env:LLM_MODEL    = "deepseek-chat"                 # 可选,默认此值

go run . --session w1            # 窗口1
go run . --session w2            # 窗口2(另开终端,与 w1 完全隔离)
go run . --mode text             # 文本协议模式(不依赖 function calling)
```

```bash
# bash
export LLM_API_KEY=sk-xxx
go run . --session w1
```

REPL 内置命令:`/switch <id>` 切换会话、`/history` 查看消息数/压缩状态/待办数、`/exit` 退出。
会话自动持久化到 `sessions/<id>.json`,重启后接着聊。

运行测试(mock LLM,不消耗 API):

```bash
go test ./...
```

### 演示脚本(录屏用)

```
窗口1> 帮我查下北京天气,如果下雨记个待办:明天带伞
       # 观察 stderr 的 [TRACE]:search → todo 两次工具调用
窗口1> 我刚才记了什么待办?          # 带工具的追问
窗口2> 我有哪些待办?               # 返回"没有待办" —— session 隔离
窗口1> 帮我算 (3+5)*2/0            # 除零错误回填,LLM 自行解释/修正
```

## 系统设计

分层原则:**domain 层(agent)只产生事件流,application 层(main)消费事件做展示与持久化**
—— 与 eino ADK 的 `Run → AsyncIterator[AgentEvent]` 同构。

```
用户输入 ──> Agent.Run ──> <-chan Event(thought / tool_call / tool_result / answer / error)
                │                            ▲
                ├─> ctxmgr.Builder.Build     │ main 消费事件:实时打印过程、
                │   (system+摘要+边界后原文)  │ 处理错误、store.Save 落盘
                ▼                            │
         LLM chat/completions(带工具 Schema) │
                │                            │
     ┌── 无 tool_calls ──> emit(answer),结束┘
     │
     └── 有 tool_calls ──> emit(tool_call) ──> registry 查找 ──> Execute
                │              (错误转文本回填,不中断)
                └──── emit(tool_result),结果入历史,继续循环(≤ MaxIterations)
```

| 模块 | 职责 |
|---|---|
| `agent/loop.go` | ReAct 主循环(本项目"从零"的核心),产出事件流;`agent/events.go` 事件定义;`agent/parse.go` 文本协议解析(Thought/Action/Final Answer) |
| `tool/` | Tool 接口(名称/描述/JSON Schema/Execute)+ 注册表;工具通过 `tool.State` 窄接口访问会话状态(接口定义在消费方,session 是实现之一,测试注入 fake);calculator(手写递归下降求值)、search(mock)、todo(有状态) |
| `session/` | 全量历史(只增不删)+ 内存 Store + JSON 文件持久化;session_id 之间完全隔离;实现 `tool.State` |
| `ctxmgr/` | 每轮重建 context 视图;超阈值压缩;轮次上限 |
| `llm/` | 裸 HTTP 的 OpenAI 兼容客户端,超时 60s + 5xx/网络错误重试一次 |
| `trace/` | 工具调用与 LLM 调用的结构化日志(stderr) |

事件流契约:每次 `Run` 的事件流以恰好一个 `answer` 或 `error` 事件结束,消费方 `range` 到
channel 关闭即可;ctx 取消时事件被丢弃、channel 直接关闭。这个设计让 CLI 能实时展示
工具调用过程,也是后续接 SSE/streaming 的天然接缝。

### 两种 LLM 输出解析模式

- **fc(默认)**:走原生 function calling,`tool_calls` 字段天然区分"调工具"与"最终答案",可靠性高。
- **text(`--mode text`)**:system prompt 约定 `Thought:/Action:/Action Input:/Final Answer:` 文本协议,
  手写逐行解析器提取思考、工具调用、最终答案;工具结果以 `Observation:` 回填。
  两种模式共用同一个循环,只是判断分支的来源不同。格式不符时回填纠正提示,让 LLM 重新输出。

## memory 的召回时机与放置方式

**召回时机**:每轮 `Run` 开始时,由 `ctxmgr.Builder` 从 session 全量历史**全量重建**本轮 context
(不做增量缓存,保证与持久化状态一致,重启后行为相同)。

**放置方式**(按顺序):

1. system prompt(角色 + 工具使用规范)恒在首位;
2. 压缩摘要作为第二条 system 消息(仅当存在);
3. `SummaryUpto` 边界之后的原文消息,按时间序。

**哪些信息进 context**:

| 信息 | 是否进 | 理由 |
|---|---|---|
| user / assistant 消息 | ✅ | 对话本体;assistant 的 tool_calls 不入史则下一轮 LLM"失忆" |
| tool 结果消息 | ✅ | 支持带工具的追问("刚才查的天气是多少来着") |
| 思考过程 | 不单独注入 | fc 模式下思考在 assistant content 里自然保留;单独存储再注入会撑大 context,得不偿失 |

**压缩策略**(基础版):未压缩消息数 > 30 时,把最近 10 条之外的旧消息交给 LLM 做增量摘要
(旧摘要 + 新增段 → 新摘要),然后推进 `SummaryUpto` 边界。两个关键设计:

1. **压缩边界持久化**:`SummaryUpto` 随 session 一起落盘。否则每轮全量重建 context 时会
   反复压缩同一段历史,浪费 token 且最终超窗。
2. **切点对齐 tool 配对**:切点落在 `assistant(tool_calls)` 与其 `tool` 结果之间时向后推进,
   避免保留区出现孤儿 tool 消息导致 API 报错。
3. **压缩失败降级**:本轮继续用原文,下轮再试,不阻塞对话。

**轮次限制(两个维度)**:单条用户消息内工具循环 ≤ 10 次(防死循环);
对话总轮次 ≤ 20(超过拒绝并提示新开 session)。

## 异常处理

| 异常 | 处理 | 测试 |
|---|---|---|
| LLM 超时/5xx/网络错误 | 60s 超时 + 重试 1 次;仍失败则**回滚本轮追加的消息**,提示可直接重试 | `TestLLMErrorRollback` |
| 工具执行报错(除零等) | error 文本作为 tool result 回填,LLM 自行修正 | `TestToolErrorSelfHeal` |
| LLM 幻觉不存在的工具 | 回填 `unknown tool`,不 panic | `TestUnknownTool` |
| arguments 非法 JSON | 回填 parse error 文本 | `TestInvalidToolArgs` |
| 工具循环超上限 | 中断返回 `ErrMaxIterations` | `TestMaxIterations` |
| 对话轮次超上限 | 拒绝新输入,返回 `ErrMaxTurns` | `TestMaxTurns` |
| Ctrl+C | ctx 透传,循环检查后干净退出 | — |
| session 文件损坏 / 非法 id | 启动报错 / 拒绝(防路径穿越) | `TestInvalidSessionID` |

## 测试用例总览

`go test ./...` 共 24 个用例,LLM 全部 mock(脚本化响应),关键覆盖:

- 循环:纯对话、单工具、多工具链(**断言完整事件序列**)、报错自愈、maxIterations、maxTurns、失败回滚
- 追问:第二轮 context 携带第一轮内容(`TestFollowUp`)
- 解析:文本协议四种形态 + 畸形输出(`parse_test.go`、`TestTextMode`)
- session:双窗口隔离、持久化 roundtrip、非法 id
- 压缩:触发条件、边界对齐、**幂等性**(不重复压缩)、未达阈值不触发
- 工具:calculator 四则/括号/负号/除零/非法表达式;todo 用 fake state 测(不依赖 session,验证解耦)

## trace 示例

```
[TRACE] session=w1 iter=0 llm msgs=2 tool_calls=1 tokens=612/24 ok cost=1.2s
[TRACE] session=w1 iter=0 tool=search args={"query":"北京天气"} result="[mock 搜索结果] 北京:..." ok cost=12µs
[TRACE] session=w1 iter=1 tool=todo args={"action":"add","item":"明天带伞"} result="已添加待办:..." ok cost=8µs
[TRACE] session=w1 compact messages[0:26) -> summary(183 chars)
```
