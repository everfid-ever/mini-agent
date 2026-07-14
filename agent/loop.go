// Package agent 实现核心 ReAct 主循环
// 不依赖任何 agent 框架:messages 累积 → 调 chat API → 有 tool_calls 就执行并回填,
// 没有就把 content 作为最终答案返回。
//
// 分层:本包是 domain 层,Run 只产生事件流(Event),不做打印/持久化;
// application 层(main)消费事件完成展示与落盘 —— 对齐 eino ADK 的
// AsyncIterator[AgentEvent] 与"domain 只产事件"的分层理念。
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"mini-agent/ctxmgr"
	"mini-agent/llm"
	"mini-agent/session"
	"mini-agent/tool"
	"mini-agent/trace"
)

var (
	// ErrMaxIterations 单条用户消息内工具循环超上限(防死循环)。
	ErrMaxIterations = errors.New("已达到单轮最大工具调用次数,任务中断")
	// ErrMaxTurns 对话总轮次超上限,与 ErrMaxIterations 是两个不同维度。
	ErrMaxTurns = errors.New("对话轮次已达上限,请新开一个 session 继续")
)

// LLM 抽象模型调用,测试时注入 mock,生产用 llm.Client。
type LLM interface {
	Chat(ctx context.Context, msgs []llm.Message, tools []llm.ToolDef) (llm.Message, llm.Usage, error)
}

type Agent struct {
	LLM           LLM
	Registry      *tool.Registry
	Builder       *ctxmgr.Builder
	TextMode      bool // true 时走文本协议(Thought/Action/Final Answer)而非 function calling
	MaxIterations int  // 单次 Run 内工具循环上限
}

// Run 处理一条用户输入,返回事件流;循环结束后 channel 关闭。
// 正常情况下事件流以恰好一个 EventAnswer 或 EventError 结束;
// ctx 取消时事件被丢弃、channel 直接关闭,消费方必须 range 到关闭或取消 ctx。
// LLM 调用失败时回滚本轮追加的所有消息,保证 session 历史干净可重试。
func (a *Agent) Run(ctx context.Context, sess *session.Session, input string) <-chan Event {
	ch := make(chan Event, 8)
	go func() {
		defer close(ch)
		emit := func(ev Event) {
			select {
			case ch <- ev:
			case <-ctx.Done():
			}
		}
		a.run(ctx, sess, input, emit)
	}()
	return ch
}

func (a *Agent) run(ctx context.Context, sess *session.Session, input string, emit func(Event)) {
	fail := func(err error) {
		emit(Event{Type: EventError, Content: err.Error(), Err: err})
	}

	if sess.UserTurns() >= ctxmgr.MaxTurns {
		fail(ErrMaxTurns)
		return
	}
	if err := a.Builder.MaybeCompact(ctx, sess); err != nil {
		// 压缩失败降级:本轮继续用原文,只记日志
		fmt.Fprintf(trace.Out, "[TRACE] session=%s compact failed: %v\n", sess.ID, err)
	}

	startLen := len(sess.Messages)
	sess.Append(llm.Message{Role: "user", Content: input})
	sess.IncrementUserTurn()
	// 快照工具状态,LLM 失败时回滚本轮副作用
	savedTodos := append([]tool.TodoItem{}, sess.Todos...)
	rollback := func() {
		sess.Truncate(startLen)
		sess.SetTodoItems(savedTodos)
	}

	maxIter := a.MaxIterations
	if maxIter <= 0 {
		maxIter = 10
	}
	for i := 0; i < maxIter; i++ {
		if err := ctx.Err(); err != nil {
			rollback()
			fail(err)
			return
		}

		msgs := a.Builder.Build(sess)
		var tools []llm.ToolDef
		if !a.TextMode {
			tools = a.Registry.ToolDefs()
		}

		llmStart := time.Now()
		resp, usage, err := a.LLM.Chat(ctx, msgs, tools)
		trace.LLM(sess.ID, i, len(msgs), len(resp.ToolCalls), usage.PromptTokens, usage.CompletionTokens, time.Since(llmStart), err)
		if err != nil {
			rollback()
			fail(fmt.Errorf("LLM 调用失败: %w", err))
			return
		}
		sess.Append(resp) // assistant 消息(含 tool_calls)必须入史,否则下一轮 LLM 失忆

		if a.TextMode {
			done, answer := a.stepText(ctx, sess, i, resp, emit)
			if done {
				emit(Event{Type: EventAnswer, Content: answer})
				return
			}
			continue
		}

		// Step 2: 无 tool_calls 即最终答案
		if len(resp.ToolCalls) == 0 {
			emit(Event{Type: EventAnswer, Content: resp.Content})
			return
		}
		// fc 模式下 tool_calls 伴随的 content 即模型的"思考"
		if resp.Content != "" {
			emit(Event{Type: EventThought, Content: resp.Content})
		}
		// Step 3: 逐个执行工具,结果以 role=tool 回填(tool_call_id 必须配对)
		for _, tc := range resp.ToolCalls {
			emit(Event{Type: EventToolCall, Tool: tc.Function.Name, Args: tc.Function.Arguments})
			result := a.execTool(ctx, sess, i, tc.Function.Name, tc.Function.Arguments)
			sess.Append(llm.Message{Role: "tool", ToolCallID: tc.ID, Content: result})
			emit(Event{Type: EventToolResult, Tool: tc.Function.Name, Content: result})
		}
		// Step 4: 带着工具结果继续 loop
	}
	fail(ErrMaxIterations)
}

// execTool 执行单个工具调用。所有失败(未知工具/参数非法/执行报错)都转成
// 错误文本回填给 LLM,让其自行修正,而不是中断循环。
// session 以 tool.State 窄接口传给工具:工具看不到历史与摘要。
func (a *Agent) execTool(ctx context.Context, sess *session.Session, iter int, name, args string) string {
	start := time.Now()
	t, ok := a.Registry.Get(name)
	if !ok {
		err := fmt.Errorf("unknown tool %q", name)
		trace.Tool(sess.ID, iter, name, args, "", err, time.Since(start))
		return "error: " + err.Error()
	}
	if !json.Valid([]byte(args)) {
		err := errors.New("arguments 不是合法 JSON")
		trace.Tool(sess.ID, iter, name, args, "", err, time.Since(start))
		return "error: " + err.Error()
	}
	result, err := t.Execute(ctx, sess, json.RawMessage(args))
	trace.Tool(sess.ID, iter, name, args, result, err, time.Since(start))
	if err != nil {
		return "error: " + err.Error()
	}
	return result
}

// stepText 处理文本协议模式的一步:解析 Thought/Action/Final Answer。
// 返回 (是否结束, 最终答案)。工具结果以 "Observation:" 用户消息回填。
func (a *Agent) stepText(ctx context.Context, sess *session.Session, iter int, resp llm.Message, emit func(Event)) (bool, string) {
	out, err := ParseReAct(resp.Content)
	if err != nil {
		sess.Append(llm.Message{
			Role:    "user",
			Content: "你的输出不符合约定格式。请严格输出 Thought/Action/Action Input,或 Thought/Final Answer。",
		})
		return false, ""
	}
	if out.Thought != "" {
		emit(Event{Type: EventThought, Content: out.Thought})
	}
	if out.FinalAnswer != "" {
		return true, out.FinalAnswer
	}
	emit(Event{Type: EventToolCall, Tool: out.Action, Args: out.ActionInput})
	result := a.execTool(ctx, sess, iter, out.Action, out.ActionInput)
	sess.Append(llm.Message{Role: "user", Content: "Observation: " + result})
	emit(Event{Type: EventToolResult, Tool: out.Action, Content: result})
	return false, ""
}
