package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"mini-agent/ctxmgr"
	"mini-agent/llm"
	"mini-agent/session"
	"mini-agent/tool"
	"mini-agent/trace"
)

func TestMain(m *testing.M) {
	trace.Enabled = false
	m.Run()
}

// mockLLM 按脚本依次返回预设响应;脚本耗尽后重复最后一条(用于死循环测试)。
type mockLLM struct {
	responses []llm.Message
	errs      []error
	calls     [][]llm.Message
	i         int
}

func (m *mockLLM) Chat(_ context.Context, msgs []llm.Message, _ []llm.ToolDef) (llm.Message, llm.Usage, error) {
	m.calls = append(m.calls, append([]llm.Message(nil), msgs...))
	idx := m.i
	m.i++
	if idx < len(m.errs) && m.errs[idx] != nil {
		return llm.Message{}, llm.Usage{}, m.errs[idx]
	}
	if idx >= len(m.responses) {
		return m.responses[len(m.responses)-1], llm.Usage{}, nil
	}
	return m.responses[idx], llm.Usage{}, nil
}

func answerMsg(content string) llm.Message {
	return llm.Message{Role: "assistant", Content: content}
}

func toolCallMsg(id, name, args string) llm.Message {
	return llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{
		ID: id, Type: "function",
		Function: llm.FunctionCall{Name: name, Arguments: args},
	}}}
}

func newTestAgent(m *mockLLM) *Agent {
	reg := tool.NewRegistry()
	reg.Register(tool.Calculator{})
	reg.Register(tool.Search{})
	reg.Register(tool.Todo{})
	return &Agent{
		LLM:           m,
		Registry:      reg,
		Builder:       &ctxmgr.Builder{SystemPrompt: "test"},
		MaxIterations: 5,
	}
}

// runCollect 消费整条事件流,返回全部事件、最终答案与终止错误。
func runCollect(a *Agent, sess *session.Session, input string) (evs []Event, answer string, err error) {
	for ev := range a.Run(context.Background(), sess, input) {
		evs = append(evs, ev)
		switch ev.Type {
		case EventAnswer:
			answer = ev.Content
		case EventError:
			err = ev.Err
		}
	}
	return evs, answer, err
}

// eventTypes 提取事件类型序列,便于断言。
func eventTypes(evs []Event) []EventType {
	out := make([]EventType, len(evs))
	for i, ev := range evs {
		out[i] = ev.Type
	}
	return out
}

func assertEventTypes(t *testing.T, evs []Event, want ...EventType) {
	t.Helper()
	got := eventTypes(evs)
	if len(got) != len(want) {
		t.Fatalf("事件序列不符: got=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("事件序列不符: got=%v want=%v", got, want)
		}
	}
}

// 用例1:纯对话,无工具调用,事件流 = [answer]。
func TestPureChat(t *testing.T) {
	m := &mockLLM{responses: []llm.Message{answerMsg("你好!")}}
	a := newTestAgent(m)
	sess := session.New("t1")

	evs, ans, err := runCollect(a, sess, "你好")
	if err != nil || ans != "你好!" {
		t.Fatalf("ans=%q err=%v", ans, err)
	}
	assertEventTypes(t, evs, EventAnswer)
	if len(sess.Messages) != 2 {
		t.Fatalf("历史应为 user+assistant 共2条,实际 %d", len(sess.Messages))
	}
}

// 用例2:单工具调用,验证事件序列与 tool 消息 role/ID 配对。
func TestSingleToolCall(t *testing.T) {
	m := &mockLLM{responses: []llm.Message{
		toolCallMsg("call_1", "calculator", `{"expression":"3*7"}`),
		answerMsg("3乘7等于21"),
	}}
	a := newTestAgent(m)
	sess := session.New("t2")

	evs, ans, err := runCollect(a, sess, "3*7等于几")
	if err != nil || ans != "3乘7等于21" {
		t.Fatalf("ans=%q err=%v", ans, err)
	}
	assertEventTypes(t, evs, EventToolCall, EventToolResult, EventAnswer)
	if evs[0].Tool != "calculator" || evs[1].Content != "21" {
		t.Fatalf("工具事件不符: %+v", evs[:2])
	}
	// 历史: user, assistant(tool_calls), tool, assistant
	if len(sess.Messages) != 4 {
		t.Fatalf("历史应为4条,实际 %d", len(sess.Messages))
	}
	toolMsg := sess.Messages[2]
	if toolMsg.Role != "tool" || toolMsg.ToolCallID != "call_1" || toolMsg.Content != "21" {
		t.Fatalf("tool 消息不符: %+v", toolMsg)
	}
	// 第二次 LLM 调用的 context 必须包含工具结果
	last := m.calls[1]
	if last[len(last)-1].Content != "21" {
		t.Fatalf("第二次调用应携带工具结果,实际末条: %+v", last[len(last)-1])
	}
}

// 用例3:多工具链 search → todo → 答案,断言完整事件序列。
func TestMultiToolChain(t *testing.T) {
	m := &mockLLM{responses: []llm.Message{
		toolCallMsg("c1", "search", `{"query":"北京天气"}`),
		toolCallMsg("c2", "todo", `{"action":"add","item":"明天带伞"}`),
		answerMsg("北京明天有雨,已记待办:明天带伞"),
	}}
	a := newTestAgent(m)
	sess := session.New("t3")

	evs, ans, err := runCollect(a, sess, "查北京天气,下雨的话记个待办带伞")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ans, "待办") {
		t.Fatalf("答案不符: %q", ans)
	}
	assertEventTypes(t, evs,
		EventToolCall, EventToolResult, // search
		EventToolCall, EventToolResult, // todo
		EventAnswer)
	if evs[0].Tool != "search" || evs[2].Tool != "todo" {
		t.Fatalf("工具顺序不符: %v %v", evs[0].Tool, evs[2].Tool)
	}
	if len(sess.Todos) != 1 || sess.Todos[0].Text != "明天带伞" {
		t.Fatalf("todo 状态不符: %+v", sess.Todos)
	}
}

// 用例4:工具报错自愈 —— 除零错误回填后 LLM 修正参数,循环继续。
func TestToolErrorSelfHeal(t *testing.T) {
	m := &mockLLM{responses: []llm.Message{
		toolCallMsg("c1", "calculator", `{"expression":"1/0"}`),
		toolCallMsg("c2", "calculator", `{"expression":"1/2"}`),
		answerMsg("结果是 0.5"),
	}}
	a := newTestAgent(m)
	sess := session.New("t4")

	evs, ans, err := runCollect(a, sess, "算 1/0,不行就算 1/2")
	if err != nil || ans != "结果是 0.5" {
		t.Fatalf("ans=%q err=%v", ans, err)
	}
	// 第一次 tool_result 事件应携带错误文本
	if evs[1].Type != EventToolResult || !strings.Contains(evs[1].Content, "error") {
		t.Fatalf("除零错误应作为 tool_result 事件: %+v", evs[1])
	}
	if !strings.Contains(sess.Messages[2].Content, "error") {
		t.Fatalf("除零错误应作为 tool result 回填: %+v", sess.Messages[2])
	}
}

// 用例5:LLM 幻觉出不存在的工具,回填 unknown tool 不 panic。
func TestUnknownTool(t *testing.T) {
	m := &mockLLM{responses: []llm.Message{
		toolCallMsg("c1", "no_such_tool", `{}`),
		answerMsg("抱歉,换个方式"),
	}}
	a := newTestAgent(m)
	sess := session.New("t5")

	if _, _, err := runCollect(a, sess, "test"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sess.Messages[2].Content, "unknown tool") {
		t.Fatalf("应回填 unknown tool: %+v", sess.Messages[2])
	}
}

// 用例6:LLM 永远要求调工具 → maxIterations 中断,终止事件为 error。
func TestMaxIterations(t *testing.T) {
	m := &mockLLM{responses: []llm.Message{
		toolCallMsg("c1", "calculator", `{"expression":"1+1"}`),
	}}
	a := newTestAgent(m)
	sess := session.New("t6")

	evs, _, err := runCollect(a, sess, "test")
	if !errors.Is(err, ErrMaxIterations) {
		t.Fatalf("期望 ErrMaxIterations,得到 %v", err)
	}
	if evs[len(evs)-1].Type != EventError {
		t.Fatalf("终止事件应为 error: %+v", evs[len(evs)-1])
	}
	if len(m.calls) != 5 {
		t.Fatalf("应恰好调用 LLM %d 次,实际 %d", 5, len(m.calls))
	}
}

// 用例7:追问 —— 第二轮的 context 必须携带第一轮内容。
func TestFollowUp(t *testing.T) {
	m := &mockLLM{responses: []llm.Message{
		toolCallMsg("c1", "todo", `{"action":"add","item":"写周报"}`),
		answerMsg("已记待办:写周报"),
		answerMsg("你刚才记了:写周报"),
	}}
	a := newTestAgent(m)
	sess := session.New("t7")

	if _, _, err := runCollect(a, sess, "记个待办:写周报"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runCollect(a, sess, "我刚才记了什么?"); err != nil {
		t.Fatal(err)
	}
	// 第三次 LLM 调用(第二轮的第一次)应包含第一轮的 user 消息
	ctx3 := m.calls[2]
	found := false
	for _, msg := range ctx3 {
		if strings.Contains(msg.Content, "记个待办:写周报") {
			found = true
		}
	}
	if !found {
		t.Fatal("追问的 context 未携带第一轮内容")
	}
}

// 用例8:LLM 调用失败 → 回滚本轮 user 消息,历史保持干净可重试。
func TestLLMErrorRollback(t *testing.T) {
	m := &mockLLM{
		responses: []llm.Message{answerMsg("unused")},
		errs:      []error{errors.New("api down")},
	}
	a := newTestAgent(m)
	sess := session.New("t8")

	if _, _, err := runCollect(a, sess, "你好"); err == nil {
		t.Fatal("期望报错")
	}
	if len(sess.Messages) != 0 {
		t.Fatalf("失败后历史应回滚为空,实际 %d 条", len(sess.Messages))
	}
}

// 用例9:对话总轮次超上限 → 拒绝新输入。
func TestMaxTurns(t *testing.T) {
	m := &mockLLM{responses: []llm.Message{answerMsg("ok")}}
	a := newTestAgent(m)
	sess := session.New("t9")
	for i := 0; i < ctxmgr.MaxTurns; i++ {
		sess.IncrementUserTurn()
		sess.Append(llm.Message{Role: "user", Content: fmt.Sprintf("msg %d", i)})
		sess.Append(answerMsg("ok"))
	}

	_, _, err := runCollect(a, sess, "再问一句")
	if !errors.Is(err, ErrMaxTurns) {
		t.Fatalf("期望 ErrMaxTurns,得到 %v", err)
	}
}

// 用例10:arguments 非法 JSON → 回填 parse error 文本。
func TestInvalidToolArgs(t *testing.T) {
	m := &mockLLM{responses: []llm.Message{
		toolCallMsg("c1", "calculator", `{not json`),
		answerMsg("参数有误,已修正"),
	}}
	a := newTestAgent(m)
	sess := session.New("t10")

	if _, _, err := runCollect(a, sess, "test"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sess.Messages[2].Content, "error") {
		t.Fatalf("非法 JSON 应回填 error: %+v", sess.Messages[2])
	}
}

// 用例11:文本协议模式 —— thought 事件 + Observation 回填,共用同一循环。
func TestTextMode(t *testing.T) {
	m := &mockLLM{responses: []llm.Message{
		answerMsg("Thought: 需要计算\nAction: calculator\nAction Input: {\"expression\":\"(3+5)*2\"}"),
		answerMsg("Thought: 有结果了\nFinal Answer: 答案是 16"),
	}}
	a := newTestAgent(m)
	a.TextMode = true
	sess := session.New("t11")

	evs, ans, err := runCollect(a, sess, "(3+5)*2 等于几")
	if err != nil || ans != "答案是 16" {
		t.Fatalf("ans=%q err=%v", ans, err)
	}
	assertEventTypes(t, evs,
		EventThought, EventToolCall, EventToolResult, // 第一步
		EventThought, EventAnswer) // 第二步
	if evs[0].Content != "需要计算" || evs[2].Content != "16" {
		t.Fatalf("事件内容不符: %+v", evs)
	}
	// Observation 应以 user 消息回填
	obs := sess.Messages[2]
	if obs.Role != "user" || !strings.Contains(obs.Content, "Observation: 16") {
		t.Fatalf("observation 回填不符: %+v", obs)
	}
}
