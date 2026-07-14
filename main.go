package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"mini-agent/agent"
	"mini-agent/ctxmgr"
	"mini-agent/llm"
	"mini-agent/session"
	"mini-agent/tool"
	"mini-agent/trace"
)

const fcSystemPrompt = `你是一个乐于助人的中文 AI 助手,可以调用提供的工具完成任务。
规则:
- 需要计算、搜索、管理待办时调用对应工具;闲聊或已有足够信息时直接回答。
- 工具返回错误时,分析原因并修正参数重试,或换一种方式完成任务。
- 回答简洁,不要复述工具的原始输出格式。`

func textSystemPrompt(reg *tool.Registry) string {
	var sb strings.Builder
	sb.WriteString("你是一个中文 AI 助手,通过以下工具完成任务:\n\n")
	for _, t := range reg.All() {
		fmt.Fprintf(&sb, "- %s: %s\n", t.Name(), t.Description())
	}
	sb.WriteString(`
你必须严格按以下两种格式之一输出,不要输出其他内容:

需要调用工具时:
Thought: <你的思考>
Action: <工具名>
Action Input: <JSON 格式的参数>

可以给出最终答案时:
Thought: <你的思考>
Final Answer: <给用户的答案>

工具结果会以 "Observation: ..." 的形式反馈给你。`)
	return sb.String()
}

func main() {
	sessionID := flag.String("session", "default", "session id(不同窗口用不同 id,彼此隔离)")
	mode := flag.String("mode", "fc", "fc=function calling(默认) / text=文本协议解析")
	dataDir := flag.String("data", "sessions", "session 持久化目录")
	flag.Parse()

	client, err := llm.NewFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "启动失败:", err)
		fmt.Fprintln(os.Stderr, `请设置环境变量,例如:
  $env:LLM_API_KEY = "sk-xxx"
  $env:LLM_BASE_URL = "https://api.deepseek.com/v1"   # 可选,默认 DeepSeek
  $env:LLM_MODEL = "deepseek-chat"                    # 可选`)
		os.Exit(1)
	}

	reg := tool.NewRegistry()
	reg.Register(tool.Calculator{})
	reg.Register(tool.Search{})
	reg.Register(tool.Todo{})

	sysPrompt := fcSystemPrompt
	if *mode == "text" {
		sysPrompt = textSystemPrompt(reg)
	}
	ag := &agent.Agent{
		LLM:           client,
		Registry:      reg,
		Builder:       &ctxmgr.Builder{SystemPrompt: sysPrompt, Summarizer: client},
		TextMode:      *mode == "text",
		MaxIterations: 10,
	}

	store, err := session.NewStore(*dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "启动失败:", err)
		os.Exit(1)
	}
	sess, err := store.GetOrCreate(*sessionID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "启动失败:", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	fmt.Printf("mini-agent 已启动 model=%s mode=%s(/switch <id> 切换会话,/history 查看状态,/exit 退出)\n", client.Model(), *mode)
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Printf("mini-agent [session=%s] > ", sess.ID)
		if !scanner.Scan() {
			fmt.Println()
			return
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		switch {
		case input == "/exit":
			return
		case input == "/history":
			fmt.Printf("消息数=%d 已压缩到下标=%d 摘要长度=%d 待办数=%d\n",
				len(sess.Messages), sess.SummaryUpto, len([]rune(sess.Summary)), len(sess.Todos))
			continue
		case strings.HasPrefix(input, "/switch "):
			id := strings.TrimSpace(strings.TrimPrefix(input, "/switch "))
			next, err := store.GetOrCreate(id)
			if err != nil {
				fmt.Println("切换失败:", err)
				continue
			}
			sess = next
			fmt.Println("已切换到 session:", id)
			continue
		}

		// application 层消费 domain 事件流:实时展示过程,结束后统一处理错误与落盘
		var runErr error
		for ev := range ag.Run(ctx, sess, input) {
			switch ev.Type {
			case agent.EventThought:
				fmt.Println("· 思考:", clip(ev.Content, 200))
			case agent.EventToolCall:
				fmt.Printf("→ 调用工具 %s %s\n", ev.Tool, clip(ev.Args, 200))
			case agent.EventToolResult:
				fmt.Printf("← %s\n", clip(ev.Content, 200))
			case agent.EventAnswer:
				fmt.Println(ev.Content)
			case agent.EventError:
				runErr = ev.Err
			}
		}
		if runErr != nil {
			switch {
			case errors.Is(runErr, context.Canceled):
				fmt.Println("\n已中断,再见")
				return
			case errors.Is(runErr, agent.ErrMaxTurns), errors.Is(runErr, agent.ErrMaxIterations):
				fmt.Println("⚠ ", runErr)
			default:
				fmt.Println("⚠ 出错了:", runErr, "(本轮输入已回滚,可直接重试)")
			}
		}
		if err := store.Save(sess); err != nil {
			fmt.Fprintf(trace.Out, "[TRACE] session=%s save failed: %v\n", sess.ID, err)
		}
	}
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
