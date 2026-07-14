package tool

import (
	"context"
	"encoding/json"
)

// Tool 是所有工具的统一接口。名称/描述/参数 Schema 会注入 LLM 请求,
// 由 LLM 基于 Schema 自主决策是否调用、如何传参。
type Tool interface {
	Name() string
	Description() string
	// Parameters 返回 JSON Schema(object),直接序列化进 tools 参数。
	Parameters() map[string]any
	// Execute 执行工具。args 是 LLM 生成的 arguments JSON;
	// st 是会话状态的窄接口,无状态工具直接忽略。
	// 返回 error 时由 agent 把错误文本回填给 LLM,而不是中断循环。
	Execute(ctx context.Context, st State, args json.RawMessage) (string, error)
}

// State 是工具可见的会话状态窄接口:接口定义在消费方(tool 包),
// session.Session 是它的一个实现。工具不依赖 session 具体类型,
// 也看不到历史/摘要等无关字段;测试可注入 fake。
type State interface {
	TodoItems() []TodoItem
	SetTodoItems([]TodoItem)
}

// TodoItem 归属 tool 包(契约随接口走),session 只负责存储。
type TodoItem struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}
