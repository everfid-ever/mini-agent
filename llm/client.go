package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Client 是 OpenAI 兼容 chat/completions 接口的裸 HTTP 封装,不依赖任何 SDK。
type Client struct {
	baseURL string
	apiKey  string
	model   string
	httpc   *http.Client
}

func NewFromEnv() (*Client, error) {
	key := os.Getenv("LLM_API_KEY")
	if key == "" {
		return nil, errors.New("环境变量 LLM_API_KEY 未设置")
	}
	base := os.Getenv("LLM_BASE_URL")
	if base == "" {
		base = "https://api.deepseek.com/v1"
	}
	model := os.Getenv("LLM_MODEL")
	if model == "" {
		model = "deepseek-chat"
	}
	return &Client{
		baseURL: strings.TrimRight(base, "/"),
		apiKey:  key,
		model:   model,
		httpc:   &http.Client{Timeout: 60 * time.Second},
	}, nil
}

func (c *Client) Model() string { return c.model }

type chatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Tools    []ToolDef `json:"tools,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Chat 调一次 chat/completions。网络错误或 5xx 自动重试一次。
func (c *Client) Chat(ctx context.Context, msgs []Message, tools []ToolDef) (Message, Usage, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(time.Second):
			case <-ctx.Done():
				return Message{}, Usage{}, ctx.Err()
			}
		}
		msg, usage, retryable, err := c.chatOnce(ctx, msgs, tools)
		if err == nil {
			return msg, usage, nil
		}
		lastErr = err
		if !retryable || ctx.Err() != nil {
			break
		}
	}
	return Message{}, Usage{}, lastErr
}

func (c *Client) chatOnce(ctx context.Context, msgs []Message, tools []ToolDef) (Message, Usage, bool, error) {
	body, err := json.Marshal(chatRequest{Model: c.model, Messages: msgs, Tools: tools})
	if err != nil {
		return Message{}, Usage{}, false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Message{}, Usage{}, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpc.Do(req)
	if err != nil {
		return Message{}, Usage{}, true, fmt.Errorf("llm 请求失败: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return Message{}, Usage{}, true, err
	}
	if resp.StatusCode >= 500 {
		return Message{}, Usage{}, true, fmt.Errorf("llm 服务端错误 %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var out chatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return Message{}, Usage{}, false, fmt.Errorf("llm 响应解析失败: %w, body=%s", err, truncate(string(raw), 200))
	}
	if resp.StatusCode != http.StatusOK {
		msg := truncate(string(raw), 200)
		if out.Error != nil {
			msg = out.Error.Message
		}
		return Message{}, Usage{}, false, fmt.Errorf("llm 返回 %d: %s", resp.StatusCode, msg)
	}
	if len(out.Choices) == 0 {
		return Message{}, Usage{}, false, errors.New("llm 响应无 choices")
	}
	m := out.Choices[0].Message
	if m.Role == "" {
		m.Role = "assistant"
	}
	return m, out.Usage, false, nil
}

// Summarize 用同一个模型把一段历史消息压缩成摘要,prev 为已有摘要(增量压缩)。
func (c *Client) Summarize(ctx context.Context, prev string, msgs []Message) (string, error) {
	var sb strings.Builder
	if prev != "" {
		sb.WriteString("已有摘要:\n" + prev + "\n\n新增对话:\n")
	}
	for _, m := range msgs {
		sb.WriteString(m.Role)
		sb.WriteString(": ")
		if len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&sb, "[调用工具 %s(%s)] ", tc.Function.Name, tc.Function.Arguments)
			}
		}
		sb.WriteString(truncate(m.Content, 500))
		sb.WriteString("\n")
	}
	prompt := []Message{
		{Role: "system", Content: "你是对话摘要助手。将给定对话压缩成简洁摘要,必须保留:用户的核心诉求、关键事实、工具调用及其结果、待办事项状态。用中文,直接输出摘要正文,不要任何前缀。"},
		{Role: "user", Content: sb.String()},
	}
	m, _, err := c.Chat(ctx, prompt, nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(m.Content), nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
