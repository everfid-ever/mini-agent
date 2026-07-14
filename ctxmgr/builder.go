// Package ctxmgr 负责"这一轮给 LLM 看什么":session 存全量历史(事实),
// Builder 每轮从 SummaryUpto 边界之后重建视图,并在历史过长时触发压缩。
package ctxmgr

import (
	"context"

	"mini-agent/llm"
	"mini-agent/session"
	"mini-agent/trace"
)

const (
	// MaxTurns 对话总轮次上限(按 user 消息计),超过拒绝新输入。
	MaxTurns = 20
	// CompactThreshold 未压缩消息数超过此值触发压缩。
	CompactThreshold = 30
	// KeepRecent 压缩时保留最近 N 条消息原文。
	KeepRecent = 10
)

// Summarizer 由 llm.Client 实现;测试时可注入 mock。
type Summarizer interface {
	Summarize(ctx context.Context, prev string, msgs []llm.Message) (string, error)
}

type Builder struct {
	SystemPrompt string
	Summarizer   Summarizer
}

// Build 组装本轮 context:system prompt → 压缩摘要 → 边界之后的原文消息。
func (b *Builder) Build(sess *session.Session) []llm.Message {
	msgs := make([]llm.Message, 0, len(sess.Messages)-sess.SummaryUpto+2)
	msgs = append(msgs, llm.Message{Role: "system", Content: b.SystemPrompt})
	if sess.Summary != "" {
		msgs = append(msgs, llm.Message{
			Role:    "system",
			Content: "以下是本会话早前对话的摘要(更早的原始消息已压缩):\n" + sess.Summary,
		})
	}
	msgs = append(msgs, sess.Messages[sess.SummaryUpto:]...)
	return msgs
}

// MaybeCompact 在未压缩消息超阈值时,把旧消息压成摘要并推进 SummaryUpto。
// 压缩失败不致命:返回 error 供调用方记日志,本轮继续用原文,下轮再试。
func (b *Builder) MaybeCompact(ctx context.Context, sess *session.Session) error {
	if b.Summarizer == nil {
		return nil
	}
	if len(sess.Messages)-sess.SummaryUpto <= CompactThreshold {
		return nil
	}
	cut := alignToCleanBoundary(sess.Messages, len(sess.Messages)-KeepRecent)
	if cut <= sess.SummaryUpto {
		return nil
	}
	summary, err := b.Summarizer.Summarize(ctx, sess.Summary, sess.Messages[sess.SummaryUpto:cut])
	if err != nil {
		return err
	}
	trace.Compact(sess.ID, sess.SummaryUpto, cut, len([]rune(summary)))
	sess.Summary = summary
	sess.SummaryUpto = cut // 压缩边界持久化在 session 里,避免重复压缩
	return nil
}

// alignToCleanBoundary 保证切点不落在 assistant(tool_calls) 与其 tool 结果之间:
// 若切点指向 tool 消息,向后推进,让整组配对进入摘要区,保留区首条必为干净消息。
func alignToCleanBoundary(msgs []llm.Message, cut int) int {
	if cut < 0 {
		cut = 0
	}
	for cut < len(msgs) && msgs[cut].Role == "tool" {
		cut++
	}
	return cut
}
