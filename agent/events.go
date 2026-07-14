package agent

// EventType 是 agent 事件流的事件类型。
// domain 层(本包)只产生事件,不做打印/持久化 —— 那是 application 层(main)消费事件的职责。
type EventType string

const (
	EventThought    EventType = "thought"     // LLM 的思考(text 模式的 Thought,或 fc 模式 tool_calls 伴随的 content)
	EventToolCall   EventType = "tool_call"   // 决定调用某个工具
	EventToolResult EventType = "tool_result" // 工具执行结果(含错误文本)
	EventAnswer     EventType = "answer"      // 最终答案,终止事件
	EventError      EventType = "error"       // 循环失败,终止事件
)

// Event 是一次 Run 过程中对外可见的最小事实单元。
// 每次 Run 的事件流以恰好一个 EventAnswer 或 EventError 结束(ctx 取消时可能直接关闭)。
type Event struct {
	Type    EventType
	Content string // thought / answer / tool 结果 / 错误文本
	Tool    string // tool_call & tool_result:工具名
	Args    string // tool_call:参数 JSON
	Err     error  // error 事件:原始错误,供 errors.Is 判断
}
