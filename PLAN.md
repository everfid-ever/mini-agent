# mini-agent 实现方案(PLAN)

> 本文档在写任何代码之前产出并经人工 review 定稿,是后续所有实现 Prompt 的共享锚点。
> 代码与本方案的偏差需回写本文档(见文末修订记录)。

## 1. 目标与硬约束

做一个最小可用 Agent(CLI + 真实 LLM API),硬约束:

- 禁止任何 agent 框架(langgraph/langchain/eino 等);LLM 调用用 `net/http` 裸调 OpenAI 兼容 chat/completions,不用 SDK;除 Go 标准库外零依赖。
- 必须实现:ReAct 循环、≥3 个工具 + 注册机制、LLM 输出解析、多 session 隔离、context 管理(轮次上限 + 基础压缩)、异常处理、执行 trace、测试用例。

## 2. 架构与分层原则

```
mini-agent/
├── main.go        # application 层:CLI,消费事件流做展示/错误处理/落盘
├── llm/           # OpenAI 兼容 HTTP 客户端(超时+重试)、增量摘要
├── agent/         # domain 层:ReAct 循环(loop.go)、事件定义(events.go)、文本协议解析(parse.go)
├── tool/          # Tool 接口 + State 窄接口 + 注册表 + calculator/search/todo
├── session/       # 事实层:全量历史 + JSON 落盘 Store
├── ctxmgr/        # 视图层:context 组装 + 压缩 + 上限常量
└── trace/         # 结构化执行日志(stderr)
```

三条分层原则(全部体现为依赖方向或接口签名,可被 review 检查):

1. **domain 只产事件流**:`agent.Run(ctx, sess, input) → <-chan Event`,事件类型
   `thought / tool_call / tool_result / answer / error`;打印、持久化全在 main 消费侧,
   agent 包内不允许任何面向用户的 IO。
2. **session 是事实层,context 是视图层**:session 全量历史只增不删;每轮由 ctxmgr.Builder
   重建"这轮给 LLM 看什么";压缩只推进边界下标,不删消息。
3. **工具窄接口 + 依赖反转**:`tool.State` 接口定义在消费方(tool 包),session 实现它;
   依赖方向 session→tool,tool 包禁止 import session;工具看不到历史与摘要。

## 3. 核心循环(agent/loop.go)

伪代码:

```
Run:
  UserTurns >= MaxTurns → emit error(ErrMaxTurns), return
  MaybeCompact(失败降级:记日志,本轮用原文)
  startLen = len(history);append user 消息
  for i < MaxIterations:
    ctx 取消 → 回滚到 startLen,emit error
    msgs = Builder.Build(sess);fc 模式附带 registry.ToolDefs()
    resp = LLM.Chat(msgs)          # 失败 → 回滚到 startLen,emit error
    append resp                     # assistant(含 tool_calls)必须入史
    无 tool_calls → emit answer, return
    有 → 逐个:emit tool_call → execTool → append role=tool(配 tool_call_id)→ emit tool_result
  emit error(ErrMaxIterations)
```

必须满足的不变量(每条配测试):

| # | 不变量 |
|---|---|
| I1 | assistant(tool_calls) 先入史再执行工具;工具结果按 `tool_call_id` 逐个配对回填 |
| I2 | 工具报错 / 未知工具名 / arguments 非法 JSON → 转 `error: ...` 文本回填,循环不中断、不 panic |
| I3 | MaxIterations(单条消息内工具循环,默认10)与 MaxTurns(会话总轮次,默认20)是两个维度 |
| I4 | LLM 调用失败(重试后)→ 回滚本轮追加的全部消息,历史干净可重试 |
| I5 | 事件流以恰好一个 answer/error 收尾后关闭;ctx 取消时丢弃事件直接关闭,emit 用 select ctx.Done 兜底,无 goroutine 泄漏 |

### 双解析模式

- **fc(默认)**:function calling,`tool_calls` 字段天然区分工具调用与最终答案。
- **text(--mode text)**:不传 tools,system prompt 约定
  `Thought:/Action:/Action Input:/Final Answer:`,手写逐行解析;解析失败回填格式纠正消息
  (消耗一次循环额度);工具结果以 `Observation:` 回填。两模式共用同一循环体。

## 4. 工具层(tool/)

```go
type Tool interface {
    Name() string; Description() string
    Parameters() map[string]any                      // JSON Schema,进 tools 数组
    Execute(ctx, st State, args json.RawMessage) (string, error)
}
type State interface {                               // 窄接口,session 实现
    TodoItems() []TodoItem; SetTodoItems([]TodoItem)
}
```

- registry 维护注册顺序,`ToolDefs()` 输出稳定有序。
- `calculator`:手写递归下降(+ - * / 括号 小数 一元负号),除零/非法表达式返回 error。
- `search`:mock,内置城市天气与知识条目,结果带 `[mock 搜索结果]` 前缀。
- `todo`:add/list/done,状态经 State 存进 session(验证多 session 隔离)。

## 5. session 与 context 管理

- Session 字段:ID / Messages / Summary / **SummaryUpto(压缩边界,持久化字段)** / Todos。
  边界必须落盘:否则每轮全量重建 context 会反复压缩同一段历史。
- Store:内存 map + `sessions/<id>.json` 每轮落盘;id 白名单 `^[A-Za-z0-9_-]{1,64}$` 防路径穿越。
- Build 顺序:system prompt → 摘要(第二条 system,仅当存在)→ SummaryUpto 之后原文。
- 压缩:未压缩消息 > 30 触发,保留最近 10 条,旧摘要+旧消息 → LLM 增量摘要;
  **切点对齐**:落在 assistant(tool_calls) 与 tool 结果之间时向后推进,保留区首条不得是孤儿 tool 消息;
  压缩失败降级不阻塞。
- 进 context 的取舍:user/assistant/tool 消息全量进;思考不单独注入(fc 模式下在 assistant content 里自然保留)。

## 6. CLI 与 llm 客户端(运行形态)

题目要求"用户 A 可以随时接着窗口 1/2 继续聊",落在 CLI 形态上:

- **配置**:环境变量 `LLM_API_KEY`(必填,缺失时打印配置示例退出)、`LLM_BASE_URL`
  (默认 `https://api.deepseek.com/v1`)、`LLM_MODEL`(默认 `deepseek-chat`)。
- **llm.Client**:`net/http` 裸调 `POST {base}/chat/completions`;60s 超时;网络错误/5xx
  重试一次(间隔 1s,尊重 ctx);解析 `choices[0].message` 与 `usage`;非 200 提取 error.message。
  另提供 `Summarize(prev, msgs)`:旧摘要 + 新增消息渲染成文本交给同一模型,输出纯摘要正文
  (供 ctxmgr 增量压缩,以 Summarizer 接口注入)。
- **CLI(main)**:flags `--session`(默认 default)/ `--mode`(fc|text)/ `--data`(session 目录);
  REPL 命令 `/switch <id>` 切换会话、`/history` 查看消息数/压缩边界/待办数、`/exit`;
  多窗口 = 多个进程各带自己的 `--session` id。
- **事件消费**:application 层 range 事件流——thought/tool_call/tool_result 实时打印(长文本截断),
  answer 输出正文,error 按 sentinel(`errors.Is`:MaxTurns / MaxIterations / ctx 取消 / 其他)分支提示;
  **每轮事件流结束后 `store.Save` 落盘**;Ctrl+C 经 `signal.NotifyContext` 取消 ctx 干净退出。
- **trace**:工具调用(入参/结果/错误/耗时)与 LLM 调用(消息数/tool_calls 数/token/耗时)
  各打一行结构化日志到 stderr(不污染 stdout 对话流),可全局开关。

## 7. 异常处理

| 异常 | 处理 |
|---|---|
| LLM 超时/5xx/网络错误 | 60s 超时 + 重试1次;仍失败回滚并提示可重试 |
| 工具报错/未知工具/非法参数 | 错误文本回填(I2) |
| 循环/轮次超限 | ErrMaxIterations / ErrMaxTurns(I3) |
| Ctrl+C | ctx 透传,干净退出 |
| session 文件损坏/非法 id | 启动报错/拒绝 |

## 8. 测试方案

LLM 全 mock(接口注入、脚本化响应),离线运行;**断言事件序列而非只断最终答案**。
覆盖:纯对话 / 单工具(I1 配对)/ 多工具链(完整事件序列)/ 除零自愈 / 未知工具 / 非法 JSON /
MaxIterations / MaxTurns / 失败回滚 / 追问携带前轮 context / 文本协议各形态 /
压缩触发+切点对齐+**幂等**+未达阈值 / session 隔离+落盘 roundtrip+非法 id /
calculator 边界 / todo(fakeState)。

## 9. 里程碑

M1 llm+裸循环端到端 → M2 registry+三工具+trace+错误回填 → M3 store+CLI 多 session →
M4 ctxmgr+上限+压缩 → M5 测试+README+录屏。

## 修订记录

- **v1 → v2(实现前,review 定稿)**:`Run` 由同步返回 `(string, error)` 改为事件流
  `<-chan Event`(对齐"domain 只产事件"原则,原 v1 违反了自己定的分层);工具签名由
  `Execute(ctx, *session.Session, args)` 改为 `Execute(ctx, tool.State, args)`(v1 让工具
  依赖 session 具体类型,与依赖反转原则冲突)。
- **实现期偏差回写**:上下文管理包 `context` 与标准库冲突,改名 `ctxmgr`。
