package tool

import (
	"context"
	"encoding/json"
	"testing"
)

func TestEvalExpr(t *testing.T) {
	cases := []struct {
		expr    string
		want    float64
		wantErr bool
	}{
		{"(3+5)*2", 16, false},
		{"3*7", 21, false},
		{"10/4", 2.5, false},
		{"-3+5", 2, false},
		{"2*(3+(4-1))/3", 4, false},
		{" 1.5 + 2.5 ", 4, false},
		{"10/0", 0, true},
		{"abc", 0, true},
		{"1+", 0, true},
		{"(1+2", 0, true},
		{"1+2)", 0, true},
	}
	for _, c := range cases {
		got, err := evalExpr(c.expr)
		if c.wantErr {
			if err == nil {
				t.Errorf("evalExpr(%q) 期望报错,实际得到 %v", c.expr, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("evalExpr(%q) 报错: %v", c.expr, err)
		} else if got != c.want {
			t.Errorf("evalExpr(%q) = %v, 期望 %v", c.expr, got, c.want)
		}
	}
}

func TestCalculatorExecute(t *testing.T) {
	c := Calculator{}
	out, err := c.Execute(context.Background(), nil, json.RawMessage(`{"expression":"3*7"}`))
	if err != nil || out != "21" {
		t.Fatalf("期望 21, 得到 %q err=%v", out, err)
	}
	if _, err := c.Execute(context.Background(), nil, json.RawMessage(`{"expression":"1/0"}`)); err == nil {
		t.Fatal("除零期望报错")
	}
	if _, err := c.Execute(context.Background(), nil, json.RawMessage(`{}`)); err == nil {
		t.Fatal("空表达式期望报错")
	}
}
