package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Todo 是有状态工具:通过 State 窄接口读写所属会话的待办列表,
// 用于验证多 session 隔离(窗口1记的待办,窗口2看不到)。
type Todo struct{}

func (Todo) Name() string { return "todo" }

func (Todo) Description() string {
	return "管理当前会话的待办事项:add 添加、list 查看、done 完成(item 传序号或原文)。"
}

func (Todo) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"add", "list", "done"},
				"description": "操作类型",
			},
			"item": map[string]any{
				"type":        "string",
				"description": "add 时为待办内容;done 时为待办序号(从1开始)或待办原文;list 时省略",
			},
		},
		"required": []string{"action"},
	}
}

type todoArgs struct {
	Action string `json:"action"`
	Item   string `json:"item"`
}

func (Todo) Execute(_ context.Context, st State, args json.RawMessage) (string, error) {
	if st == nil {
		return "", errors.New("todo 工具需要会话状态")
	}
	var a todoArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}
	todos := st.TodoItems()
	switch a.Action {
	case "add":
		if strings.TrimSpace(a.Item) == "" {
			return "", errors.New("add 需要提供 item")
		}
		todos = append(todos, TodoItem{Text: a.Item})
		st.SetTodoItems(todos)
		return fmt.Sprintf("已添加待办:%s(当前共 %d 条)", a.Item, len(todos)), nil
	case "list":
		return renderTodos(todos), nil
	case "done":
		idx, err := findTodo(todos, a.Item)
		if err != nil {
			return "", err
		}
		todos[idx].Done = true
		st.SetTodoItems(todos)
		return fmt.Sprintf("已完成待办:%s\n%s", todos[idx].Text, renderTodos(todos)), nil
	default:
		return "", fmt.Errorf("未知 action %q,支持 add/list/done", a.Action)
	}
}

func renderTodos(todos []TodoItem) string {
	if len(todos) == 0 {
		return "当前没有待办事项"
	}
	var sb strings.Builder
	sb.WriteString("当前待办:\n")
	for i, t := range todos {
		mark := "[ ]"
		if t.Done {
			mark = "[x]"
		}
		fmt.Fprintf(&sb, "%d. %s %s\n", i+1, mark, t.Text)
	}
	return strings.TrimRight(sb.String(), "\n")
}

func findTodo(todos []TodoItem, item string) (int, error) {
	if strings.TrimSpace(item) == "" {
		return 0, errors.New("done 需要提供 item(序号或原文)")
	}
	if n, err := strconv.Atoi(strings.TrimSpace(item)); err == nil {
		if n < 1 || n > len(todos) {
			return 0, fmt.Errorf("序号 %d 超出范围(共 %d 条)", n, len(todos))
		}
		return n - 1, nil
	}
	for i, t := range todos {
		if t.Text == item {
			return i, nil
		}
	}
	return 0, fmt.Errorf("未找到待办 %q", item)
}
