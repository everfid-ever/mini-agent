package agent

import (
	"errors"
	"strings"
)

// ReActOutput 是文本协议模式下从 LLM 输出中解析出的结构。
// 该模式不依赖 function calling,演示手写"思考/工具调用/最终答案"解析。
type ReActOutput struct {
	Thought     string
	Action      string // 工具名,空表示无工具调用
	ActionInput string // 工具参数 JSON
	FinalAnswer string
}

// ParseReAct 按约定格式逐行解析:
//
//	Thought: <思考>
//	Action: <工具名>
//	Action Input: <JSON参数>
//	--- 或 ---
//	Thought: <思考>
//	Final Answer: <答案>
//
// Action Input 与 Final Answer 支持多行,取到下一个标记或结尾为止。
func ParseReAct(content string) (ReActOutput, error) {
	var out ReActOutput
	var cur *string
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case hasMarker(trimmed, "Thought:"):
			out.Thought = afterMarker(trimmed, "Thought:")
			cur = &out.Thought
		case hasMarker(trimmed, "Action:"):
			out.Action = afterMarker(trimmed, "Action:")
			cur = &out.Action
		case hasMarker(trimmed, "Action Input:"):
			out.ActionInput = afterMarker(trimmed, "Action Input:")
			cur = &out.ActionInput
		case hasMarker(trimmed, "Final Answer:"):
			out.FinalAnswer = afterMarker(trimmed, "Final Answer:")
			cur = &out.FinalAnswer
		default:
			if cur != nil && trimmed != "" {
				if *cur != "" {
					*cur += "\n"
				}
				*cur += line
			}
		}
	}
	out.Action = strings.TrimSpace(out.Action)
	out.ActionInput = strings.TrimSpace(out.ActionInput)
	out.FinalAnswer = strings.TrimSpace(out.FinalAnswer)

	if out.Action == "" && out.FinalAnswer == "" {
		return out, errors.New("输出中既没有 Action 也没有 Final Answer")
	}
	if out.Action != "" && out.ActionInput == "" {
		out.ActionInput = "{}"
	}
	return out, nil
}

func hasMarker(line, marker string) bool {
	return strings.HasPrefix(line, marker)
}

func afterMarker(line, marker string) string {
	return strings.TrimSpace(strings.TrimPrefix(line, marker))
}