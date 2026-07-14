package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"unicode"
)

// Calculator 手写递归下降解析器求值四则运算表达式,支持括号、小数、一元负号。
// 除零/非法表达式返回 error,由 agent 回填给 LLM 自行修正。
type Calculator struct{}

func (Calculator) Name() string { return "calculator" }

func (Calculator) Description() string {
	return "计算算术表达式,支持 + - * / 和括号,例如 (3+5)*2 或 10/4"
}

func (Calculator) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"expression": map[string]any{
				"type":        "string",
				"description": "要计算的算术表达式,如 (3+5)*2",
			},
		},
		"required": []string{"expression"},
	}
}

type calcArgs struct {
	Expression string `json:"expression"`
}

func (Calculator) Execute(_ context.Context, _ State, args json.RawMessage) (string, error) {
	var a calcArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}
	if a.Expression == "" {
		return "", errors.New("expression 不能为空")
	}
	v, err := evalExpr(a.Expression)
	if err != nil {
		return "", err
	}
	return strconv.FormatFloat(v, 'f', -1, 64), nil
}

// ---- 递归下降解析:expr := term (('+'|'-') term)* ; term := factor (('*'|'/') factor)* ;
// factor := number | '(' expr ')' | '-' factor ----

type calcParser struct {
	s   []rune
	pos int
}

func evalExpr(s string) (float64, error) {
	p := &calcParser{s: []rune(s)}
	v, err := p.parseExpr()
	if err != nil {
		return 0, err
	}
	p.skipSpace()
	if p.pos < len(p.s) {
		return 0, fmt.Errorf("表达式在位置 %d 存在多余字符 %q", p.pos, string(p.s[p.pos]))
	}
	return v, nil
}

func (p *calcParser) parseExpr() (float64, error) {
	v, err := p.parseTerm()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpace()
		if p.pos >= len(p.s) {
			return v, nil
		}
		switch p.s[p.pos] {
		case '+':
			p.pos++
			r, err := p.parseTerm()
			if err != nil {
				return 0, err
			}
			v += r
		case '-':
			p.pos++
			r, err := p.parseTerm()
			if err != nil {
				return 0, err
			}
			v -= r
		default:
			return v, nil
		}
	}
}

func (p *calcParser) parseTerm() (float64, error) {
	v, err := p.parseFactor()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpace()
		if p.pos >= len(p.s) {
			return v, nil
		}
		switch p.s[p.pos] {
		case '*':
			p.pos++
			r, err := p.parseFactor()
			if err != nil {
				return 0, err
			}
			v *= r
		case '/':
			p.pos++
			r, err := p.parseFactor()
			if err != nil {
				return 0, err
			}
			if r == 0 {
				return 0, errors.New("除数为零")
			}
			v /= r
		default:
			return v, nil
		}
	}
}

func (p *calcParser) parseFactor() (float64, error) {
	p.skipSpace()
	if p.pos >= len(p.s) {
		return 0, errors.New("表达式意外结束")
	}
	c := p.s[p.pos]
	switch {
	case c == '(':
		p.pos++
		v, err := p.parseExpr()
		if err != nil {
			return 0, err
		}
		p.skipSpace()
		if p.pos >= len(p.s) || p.s[p.pos] != ')' {
			return 0, errors.New("缺少右括号")
		}
		p.pos++
		return v, nil
	case c == '-':
		p.pos++
		v, err := p.parseFactor()
		if err != nil {
			return 0, err
		}
		return -v, nil
	case unicode.IsDigit(c) || c == '.':
		return p.parseNumber()
	default:
		return 0, fmt.Errorf("位置 %d 存在非法字符 %q", p.pos, string(c))
	}
}

func (p *calcParser) parseNumber() (float64, error) {
	start := p.pos
	for p.pos < len(p.s) && (unicode.IsDigit(p.s[p.pos]) || p.s[p.pos] == '.') {
		p.pos++
	}
	v, err := strconv.ParseFloat(string(p.s[start:p.pos]), 64)
	if err != nil {
		return 0, fmt.Errorf("非法数字 %q", string(p.s[start:p.pos]))
	}
	return v, nil
}

func (p *calcParser) skipSpace() {
	for p.pos < len(p.s) && unicode.IsSpace(p.s[p.pos]) {
		p.pos++
	}
}
