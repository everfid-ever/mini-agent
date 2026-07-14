package session

import (
	"sync"

	"mini-agent/llm"
	"mini-agent/tool"
)

// Session 是"事实层":全量历史只增不删。
// 压缩不删除消息,只推进 SummaryUpto 边界 —— 边界随 session 一起持久化,
// 避免重启/每轮重建 context 时重复压缩同一段历史。
type Session struct {
	ID          string          `json:"id"`
	Messages    []llm.Message   `json:"messages"`
	Summary     string          `json:"summary"`      // 压缩摘要
	SummaryUpto int             `json:"summary_upto"` // 摘要已覆盖到的消息下标(压缩边界)
	Todos       []tool.TodoItem `json:"todos"`        // todo 工具的状态,归属于单个 session

	mu sync.Mutex `json:"-"`
}

func New(id string) *Session {
	return &Session{ID: id}
}

func (s *Session) Append(m llm.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Messages = append(s.Messages, m)
}

// Truncate 把消息裁回到长度 n,用于 LLM 调用失败时回滚本轮追加的消息。
func (s *Session) Truncate(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n >= 0 && n <= len(s.Messages) {
		s.Messages = s.Messages[:n]
	}
}

// UserTurns 返回用户消息条数,用于对话总轮次上限判断。
func (s *Session) UserTurns() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, m := range s.Messages {
		if m.Role == "user" {
			n++
		}
	}
	return n
}

// TodoItems / SetTodoItems 实现 tool.State 窄接口:
// 工具只看到待办状态,看不到历史与摘要。
func (s *Session) TodoItems() []tool.TodoItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Todos
}

func (s *Session) SetTodoItems(items []tool.TodoItem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Todos = items
}
