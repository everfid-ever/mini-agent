# AI Prompt 与问题解决记录

本项目使用 AI(Claude)辅助开发,工作流是 **PLAN 先行**:

```
需求 + 硬约束 ──Prompt 1──> PLAN.md(实现方案) ──人工 review 修订──> 定稿
定稿的 PLAN 作为共享上下文 ──Prompt 2~6──> 分模块生成代码 ──测试验收──> 偏差回写 PLAN
```

不直接让 AI 从需求生成代码——那样每个模块 prompt 各自理解需求,接口在跨 prompt 之间漂移,产出不稳定。先花一个 prompt 生成 PLAN(见 `PLAN.md`),人工把分层原则、接口契约、设计不变量在 PLAN 上审定,之后每个实现 prompt 都以"PLAN 全文 + 本模块章节"为上下文,AI 只做"按规格施工",人工用测试验收。

---

## Prompt 1:生成 PLAN(不写代码)

> 我要用 Go 从零实现一个最小可用 Agent,先不要写任何代码,输出一份实现方案(PLAN)给我 review。
>
> 需求:ReAct 循环、≥3 个工具(calculator/search-mock/todo)+ 注册机制(名称/描述/JSON Schema,LLM 基于 Schema 自主决策)、LLM 输出解析(思考/工具调用/最终答案)、多 session 隔离且可随时接着聊、context 管理(最大轮次 + 支持纯对话和带工具的追问 + 基础压缩)、异常处理、工具调用 trace、测试用例。
>
> 硬约束,PLAN 必须体现:
> 1. 禁止任何 agent 框架;LLM 用 net/http 裸调 OpenAI 兼容接口,不用 SDK,除标准库外零依赖;
> 2. 分层:domain 层只产生事件流(thought/tool_call/tool_result/answer/error),展示与持久化由 application 层消费事件完成;
> 3. session 是事实层(全量历史只增不删),context 是视图层(每轮重建),压缩只推进边界不删消息,且压缩边界必须持久化;
> 4. 工具经注册表注入,有状态工具通过定义在 tool 包的窄接口访问会话状态,依赖方向 session→tool。
>
> PLAN 必须包含:模块划分与依赖方向、核心循环伪代码 + 编号的设计不变量(每条可测)、双解析模式(function calling 为主 + 文本协议备选)、压缩策略与切点规则、**CLI 运行形态(flags、REPL 命令、多窗口如何各带 session、env 配置)与 llm 客户端策略(超时/重试/增量摘要)**、异常处理表、测试清单、里程碑。特别注意:"随时接着窗口 1/2 继续聊"要落到 CLI 形态 + 持久化时机上,不能只是一个 Store 接口。

**产出**:PLAN 初稿(v1)。

**人工 review 修订(v1 → v2,这一步是整个流程价值最大的环节)**:

1. v1 的 `Run` 签名是同步返回 `(string, error)`——违反了 PLAN 自己写的"domain 只产事件流"原则(中间过程成了黑盒,只能靠日志偷看)。改为 `Run → <-chan Event`,并补充事件流契约:恰好一个 answer/error 收尾、ctx 取消不泄漏 goroutine。
2. v1 的工具签名是 `Execute(ctx, *session.Session, args)`——工具耦死在 session 具体类型上,与依赖反转原则冲突。改为 `Execute(ctx, tool.State, args)`,State 窄接口定义在 tool 包,session 实现它。
3. 确认两个容易混的上限拆开:MaxIterations(单条消息内防死循环)/ MaxTurns(会话总轮次),各配测试。

定稿见 `PLAN.md`(修订记录在文末)。

---

## Prompt 2:主循环(PLAN §3)

> 以 PLAN.md 为上下文,实现 agent 包:loop.go(Run 事件流版)、events.go、parse.go。严格满足 §3 的不变量 I1~I5,逐条自查后在回复里说明每条不变量对应代码的哪一行。文本协议解析器手写逐行扫描,Action Input 和 Final Answer 支持多行;解析失败回填格式纠正消息,不中断循环。

**要点**:要求 AI"逐条自查并指出落点"能显著减少不变量被静默遗漏;I1(配对)和 I4(回滚)初稿即正确,I5 的 emit 初稿直接 `ch <- ev` 会在消费方退出时泄漏,review 时要求补 `select ctx.Done` 兜底。

## Prompt 3:工具层(PLAN §4)

> 以 PLAN.md 为上下文实现 tool 包。注意:tool 包禁止 import session(依赖方向是 session→tool);calculator 禁止第三方表达式库,手写递归下降;三个工具的错误(除零、序号越界、未知 action)都返回 error 而不是 panic,由循环转文本回填。

**踩坑**:todo 工具的测试若 import session 来构造状态,会形成 import cycle(session 已依赖 tool)。解决:注入 fakeState——这个坑反而验证了窄接口的收益,工具测试完全不需要真实 Session。

## Prompt 4:session 与 context(PLAN §5)

> 以 PLAN.md 为上下文实现 session 包与 ctxmgr 包。重点检查三处:SummaryUpto 必须是 JSON 持久化字段;压缩切点的 alignToCleanBoundary(切点指向 tool 消息就向后推进);session id 白名单防路径穿越。压缩失败返回 error 但调用方降级处理。

**踩坑**:上下文管理包最初命名 `context`,与标准库冲突,改名 `ctxmgr`。

## Prompt 5:llm 客户端、trace 与 CLI(PLAN §6)

> 以 PLAN.md §6 为上下文实现 llm 包、trace 包和 main.go,这是把"库"变成"可运行提交物"的一环:
>
> 1. `llm.NewFromEnv`:读 LLM_API_KEY / LLM_BASE_URL / LLM_MODEL,缺 key 打印配置示例退出;`Chat` 60s 超时,网络错误/5xx 重试一次(间隔 1s,尊重 ctx),返回 (Message, Usage, error);`Summarize(prev, msgs)` 增量摘要,输出纯正文。
> 2. `trace`:工具调用与 LLM 调用各一行结构化日志到 stderr(长字段截断,不污染 stdout),可全局关闭(测试用)。
> 3. `main`:flags --session/--mode/--data;REPL 命令 /switch /history /exit;消费事件流实时打印 thought/tool_call/tool_result,error 事件按 errors.Is 分支(MaxTurns、MaxIterations、ctx 取消、其他);**每轮事件流结束后落盘**;Ctrl+C 干净退出。多窗口演示 = 两个终端各带 --session w1 / w2。

**产出**:`llm/`、`trace/`、`main.go`。至此题目提交要求里的"真实 LLM API + 终端操作录屏"才有了载体。

## Prompt 6:测试(PLAN §8)

> 以 PLAN.md §8 清单为准生成全部测试。约束:mockLLM 按脚本返回预设响应(脚本耗尽重复最后一条,用于死循环用例);断言事件序列而不是只断最终答案,如多工具链必须断言恰好 [tool_call(search), tool_result, tool_call(todo), tool_result, answer];压缩幂等用例必须证明连续两次 MaybeCompact 只执行一次摘要。

**产出**:24 个用例,`go test ./... -count=1` 全绿,零 API 消耗。

---

## 问题解决记录汇总

| 问题 | 发现环节 | 解决 | 落点 |
|---|---|---|---|
| Run 同步返回违反自定的分层原则 | PLAN review | 改事件流 `<-chan Event` | PLAN 修订 v1→v2 |
| 工具耦合 session 具体类型 | PLAN review | tool.State 窄接口 + 依赖反转 | PLAN 修订 v1→v2 |
| emit 直接发 channel,消费方退出会泄漏 goroutine | 代码 review | select ctx.Done 兜底,契约写进注释 | `Run` |
| 包名 `context` 撞标准库 | 实现期 | 改名 `ctxmgr`,回写 PLAN | PLAN 修订记录 |
| tool 测试 import session 成环 | 实现期 | fakeState 注入 | `todo_test.go` |
| tool 结果与 tool_calls 配对易漏(API 400) | PLAN 阶段预判 | 写成不变量 I1 + 测试断言 ToolCallID | `TestSingleToolCall` |
| 压缩每轮重复执行 | PLAN 阶段预判 | SummaryUpto 持久化(硬约束3) | `TestCompactIdempotent` |
| 压缩切点拆开工具配对产生孤儿 tool 消息 | PLAN 阶段预判 | alignToCleanBoundary | `TestCompactBoundaryAlign` |
| 文本协议下 LLM 偶发不按格式输出 | 实现期 | 解析失败回填纠正消息,不中断 | `stepText` |
| session id 路径穿越 | PLAN review | 白名单正则 | `TestInvalidSessionID` |
| prompt 链遗漏 llm 客户端/trace/CLI 的施工 prompt,按链复现会得到一个没有入口、调不了真实 API 的库 | prompt 链复盘(对照题目逐条审) | PLAN 补 §6 运行形态,新增 Prompt 5;Prompt 1 补"CLI 形态必须进 PLAN"的要求 | PLAN §6、Prompt 5 |

## 经验总结

1. **PLAN 先行比 prompt 写得多细都重要。** PLAN 是所有模块 prompt 的共享锚点:接口签名、依赖方向、不变量编号都定在一处,跨 prompt 不漂移;实现期的偏差(如包名冲突)回写 PLAN,文档与代码不脱节。
2. **PLAN review 是人的主战场。** 两个最重要的设计修正(事件流、依赖反转)都发生在 review v1 的时候——在 PLAN 上改一行字,比在五个包的代码里返工便宜两个数量级。
3. **把设计不变量编号并要求"每条配测试、逐条指出落点"。** "实现一个 agent 循环"和"实现循环并满足 I1~I5"产出质量完全是两个量级;编号让 review、测试、问题记录能互相引用。
4. **测试是对 AI 生成代码唯一可靠的验收手段。** mock 掉 LLM、断言事件序列和状态边界,离线秒级跑完,比人肉 review 更能兜住回归。
5. **交付前用"只给这组 prompt 能否复现项目"的标准反向审一遍 prompt 链。** 本项目复盘时就查出 prompt 链只覆盖了核心库,漏了 llm 客户端和 CLI 入口——恰恰是题目"真实 LLM API + 终端录屏"的载体。核心逻辑最受关注,胶水层(入口、配置、客户端)最容易在分模块 prompt 里被遗漏。
