package ctxmgr

import (
	"context"
	"fmt"
	"testing"

	"mini-agent/llm"
	"mini-agent/session"
	"mini-agent/trace"
)

func TestMain(m *testing.M) {
	trace.Enabled = false
	m.Run()
}

type mockSummarizer struct {
	calls   int
	summary string
}

func (m *mockSummarizer) Summarize(_ context.Context, _ string, _ []llm.Message) (string, error) {
	m.calls++
	return m.summary, nil
}

func fillMessages(sess *session.Session, n int) {
	for i := 0; i < n; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		sess.Append(llm.Message{Role: role, Content: fmt.Sprintf("msg %d", i)})
	}
}

// 消息超阈值触发压缩:Summary 生成、SummaryUpto 前移、Build 输出被裁剪。
func TestCompactTrigger(t *testing.T) {
	sess := session.New("c1")
	fillMessages(sess, 36)
	ms := &mockSummarizer{summary: "SUMMARY"}
	b := &Builder{SystemPrompt: "sys", Summarizer: ms}

	if err := b.MaybeCompact(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	if sess.Summary != "SUMMARY" {
		t.Fatalf("摘要未生成: %q", sess.Summary)
	}
	wantCut := 36 - KeepRecent
	if sess.SummaryUpto != wantCut {
		t.Fatalf("SummaryUpto=%d, 期望 %d", sess.SummaryUpto, wantCut)
	}
	msgs := b.Build(sess)
	// 1 system + 1 摘要 + KeepRecent 条原文
	if len(msgs) != 2+KeepRecent {
		t.Fatalf("Build 输出 %d 条,期望 %d", len(msgs), 2+KeepRecent)
	}
	if msgs[1].Role != "system" || msgs[1].Content == "" {
		t.Fatalf("第二条应为摘要 system 消息: %+v", msgs[1])
	}
}

// 切点落在 tool 配对中间时向后对齐,保留区不出现孤儿 tool 消息。
func TestCompactBoundaryAlign(t *testing.T) {
	sess := session.New("c2")
	fillMessages(sess, 25)
	// 下标25: assistant(tool_calls), 26/27: tool 结果 —— 默认切点 36-10=26 会落在 tool 上
	sess.Append(llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{
		{ID: "a", Type: "function", Function: llm.FunctionCall{Name: "search", Arguments: "{}"}},
		{ID: "b", Type: "function", Function: llm.FunctionCall{Name: "todo", Arguments: "{}"}},
	}})
	sess.Append(llm.Message{Role: "tool", ToolCallID: "a", Content: "r1"})
	sess.Append(llm.Message{Role: "tool", ToolCallID: "b", Content: "r2"})
	fillMessages(sess, 8) // 总计36条

	ms := &mockSummarizer{summary: "S"}
	b := &Builder{SystemPrompt: "sys", Summarizer: ms}
	if err := b.MaybeCompact(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	if sess.SummaryUpto != 28 {
		t.Fatalf("切点应对齐到28(跳过两条 tool),实际 %d", sess.SummaryUpto)
	}
	if sess.Messages[sess.SummaryUpto].Role == "tool" {
		t.Fatal("保留区首条不能是孤儿 tool 消息")
	}
}

// 压缩幂等:边界持久化后第二次调用不重复压缩,避免每轮重建 context 时反复压缩同一段历史。
func TestCompactIdempotent(t *testing.T) {
	sess := session.New("c3")
	fillMessages(sess, 36)
	ms := &mockSummarizer{summary: "S"}
	b := &Builder{SystemPrompt: "sys", Summarizer: ms}

	_ = b.MaybeCompact(context.Background(), sess)
	_ = b.MaybeCompact(context.Background(), sess)
	if ms.calls != 1 {
		t.Fatalf("应只压缩一次,实际 %d 次", ms.calls)
	}
}

// 未达阈值不压缩。
func TestNoCompactBelowThreshold(t *testing.T) {
	sess := session.New("c4")
	fillMessages(sess, CompactThreshold)
	ms := &mockSummarizer{summary: "S"}
	b := &Builder{SystemPrompt: "sys", Summarizer: ms}

	_ = b.MaybeCompact(context.Background(), sess)
	if ms.calls != 0 || sess.SummaryUpto != 0 {
		t.Fatalf("不应触发压缩: calls=%d upto=%d", ms.calls, sess.SummaryUpto)
	}
}
