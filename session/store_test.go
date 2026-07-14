package session

import (
	"testing"

	"mini-agent/llm"
	"mini-agent/tool"
)

// 双 session 隔离:w1 的历史与状态,w2 完全看不到。
func TestSessionIsolation(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w1, _ := store.GetOrCreate("w1")
	w2, _ := store.GetOrCreate("w2")

	w1.Append(llm.Message{Role: "user", Content: "查天气记待办"})
	w1.Todos = append(w1.Todos, tool.TodoItem{Text: "明天带伞"})

	if len(w2.Messages) != 0 || len(w2.Todos) != 0 {
		t.Fatalf("w2 不应看到 w1 的数据: msgs=%d todos=%d", len(w2.Messages), len(w2.Todos))
	}
	again, _ := store.GetOrCreate("w1")
	if again != w1 {
		t.Fatal("同 id 应返回同一 session 实例")
	}
}

// 持久化 roundtrip:落盘后新 Store 能完整恢复历史、摘要边界、待办。
func TestPersistenceRoundtrip(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)
	s, _ := store.GetOrCreate("w1")
	s.Append(llm.Message{Role: "user", Content: "hello"})
	s.Append(llm.Message{Role: "assistant", Content: "hi"})
	s.Summary = "早前摘要"
	s.SummaryUpto = 1
	s.Todos = []tool.TodoItem{{Text: "写周报", Done: true}}
	if err := store.Save(s); err != nil {
		t.Fatal(err)
	}

	store2, _ := NewStore(dir)
	restored, err := store2.GetOrCreate("w1")
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Messages) != 2 || restored.Summary != "早前摘要" ||
		restored.SummaryUpto != 1 || len(restored.Todos) != 1 || !restored.Todos[0].Done {
		t.Fatalf("恢复不完整: %+v", restored)
	}
}

// 非法 session id 拒绝(防路径穿越)。
func TestInvalidSessionID(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	for _, id := range []string{"../evil", "a/b", "", "带中文"} {
		if _, err := store.GetOrCreate(id); err == nil {
			t.Errorf("id %q 应被拒绝", id)
		}
	}
}
