package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Search 是 mock 搜索:内置天气数据和少量知识条目,未命中返回"未找到"。
type Search struct{}

func (Search) Name() string { return "search" }

func (Search) Description() string {
	return "搜索互联网信息(含天气查询)。输入自然语言查询词,返回搜索结果文本。"
}

func (Search) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "搜索查询词,如 '北京明天天气' 或 'Go 语言是什么'",
			},
		},
		"required": []string{"query"},
	}
}

var mockWeather = map[string]string{
	"北京": "北京:今天晴,26°C;明天小雨,22°C,建议带伞",
	"上海": "上海:今天多云,28°C;明天雷阵雨,25°C,建议带伞",
	"广州": "广州:今天阵雨,30°C;明天多云,31°C",
	"深圳": "深圳:今天晴,31°C;明天晴,32°C,注意防晒",
}

var mockKnowledge = map[string]string{
	"go":     "Go(Golang)是 Google 开发的静态类型编译型语言,以并发原语(goroutine/channel)和简洁著称。",
	"golang": "Go(Golang)是 Google 开发的静态类型编译型语言,以并发原语(goroutine/channel)和简洁著称。",
	"agent":  "AI Agent 指以 LLM 为决策核心、能自主调用工具完成任务的系统,核心是 ReAct 循环:思考→行动→观察。",
	"react":  "ReAct 是 Reasoning + Acting 的缩写,LLM 交替输出思考和工具调用,根据工具结果迭代直至给出最终答案。",
}

type searchArgs struct {
	Query string `json:"query"`
}

func (Search) Execute(_ context.Context, _ State, args json.RawMessage) (string, error) {
	var a searchArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}
	if strings.TrimSpace(a.Query) == "" {
		return "", errors.New("query 不能为空")
	}
	q := strings.ToLower(a.Query)

	if strings.Contains(a.Query, "天气") {
		for city, w := range mockWeather {
			if strings.Contains(a.Query, city) {
				return "[mock 搜索结果] " + w, nil
			}
		}
		return "[mock 搜索结果] 未找到该城市的天气(mock 数据仅支持:北京/上海/广州/深圳)", nil
	}
	for k, v := range mockKnowledge {
		if strings.Contains(q, k) {
			return "[mock 搜索结果] " + v, nil
		}
	}
	return "[mock 搜索结果] 未找到相关结果", nil
}
