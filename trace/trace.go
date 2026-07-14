// Package trace 输出工具调用与 LLM 调用的结构化执行日志(stderr,不干扰对话输出)。
package trace

import (
	"fmt"
	"io"
	"os"
	"time"
)

var (
	Enabled       = true
	Out io.Writer = os.Stderr
)

// Tool 记录一次工具调用:入参、结果/错误、耗时。
func Tool(sessID string, iter int, name, args, result string, err error, cost time.Duration) {
	if !Enabled {
		return
	}
	status := "ok"
	if err != nil {
		status = "err=" + err.Error()
	}
	fmt.Fprintf(Out, "[TRACE] session=%s iter=%d tool=%s args=%s result=%q %s cost=%s\n",
		sessID, iter, name, clip(args, 200), clip(result, 200), status, cost.Round(time.Microsecond))
}

// LLM 记录一次模型调用:消息数、是否触发工具调用、token 用量、耗时。
func LLM(sessID string, iter, msgCount, toolCalls, promptTokens, completionTokens int, cost time.Duration, err error) {
	if !Enabled {
		return
	}
	status := "ok"
	if err != nil {
		status = "err=" + err.Error()
	}
	fmt.Fprintf(Out, "[TRACE] session=%s iter=%d llm msgs=%d tool_calls=%d tokens=%d/%d %s cost=%s\n",
		sessID, iter, msgCount, toolCalls, promptTokens, completionTokens, status, cost.Round(time.Millisecond))
}

// Compact 记录一次上下文压缩。
func Compact(sessID string, from, to int, summaryLen int) {
	if !Enabled {
		return
	}
	fmt.Fprintf(Out, "[TRACE] session=%s compact messages[%d:%d) -> summary(%d chars)\n", sessID, from, to, summaryLen)
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
