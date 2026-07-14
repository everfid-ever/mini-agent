package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// fakeState 证明工具已与 session 解耦:测试无需真实 Session,注入 fake 即可。
type fakeState struct{ todos []TodoItem }

func (f *fakeState) TodoItems() []TodoItem         { return f.todos }
func (f *fakeState) SetTodoItems(items []TodoItem) { f.todos = items }

func TestTodoAddListDone(t *testing.T) {
	td := Todo{}
	st := &fakeState{}
	ctx := context.Background()

	out, err := td.Execute(ctx, st, json.RawMessage(`{"action":"add","item":"写周报"}`))
	if err != nil || !strings.Contains(out, "写周报") {
		t.Fatalf("add 失败: out=%q err=%v", out, err)
	}
	if len(st.todos) != 1 {
		t.Fatalf("状态未写入: %+v", st.todos)
	}

	out, _ = td.Execute(ctx, st, json.RawMessage(`{"action":"list"}`))
	if !strings.Contains(out, "1. [ ] 写周报") {
		t.Fatalf("list 输出不符: %q", out)
	}

	out, err = td.Execute(ctx, st, json.RawMessage(`{"action":"done","item":"1"}`))
	if err != nil || !strings.Contains(out, "[x] 写周报") {
		t.Fatalf("done 失败: out=%q err=%v", out, err)
	}
}

func TestTodoErrors(t *testing.T) {
	td := Todo{}
	st := &fakeState{}
	ctx := context.Background()

	if _, err := td.Execute(ctx, st, json.RawMessage(`{"action":"add"}`)); err == nil {
		t.Fatal("add 无 item 应报错")
	}
	if _, err := td.Execute(ctx, st, json.RawMessage(`{"action":"done","item":"9"}`)); err == nil {
		t.Fatal("done 序号越界应报错")
	}
	if _, err := td.Execute(ctx, st, json.RawMessage(`{"action":"nope"}`)); err == nil {
		t.Fatal("未知 action 应报错")
	}
	if _, err := td.Execute(ctx, nil, json.RawMessage(`{"action":"list"}`)); err == nil {
		t.Fatal("nil state 应报错")
	}
}
