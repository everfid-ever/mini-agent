package agent

import "testing"

func TestParseReActAction(t *testing.T) {
	out, err := ParseReAct("Thought: 需要算一下\nAction: calculator\nAction Input: {\"expression\":\"3*7\"}")
	if err != nil {
		t.Fatal(err)
	}
	if out.Action != "calculator" || out.ActionInput != `{"expression":"3*7"}` {
		t.Fatalf("解析结果不符: %+v", out)
	}
	if out.Thought != "需要算一下" {
		t.Fatalf("thought 不符: %q", out.Thought)
	}
}

func TestParseReActFinalAnswer(t *testing.T) {
	out, err := ParseReAct("Thought: 已经有答案了\nFinal Answer: 结果是 21\n第二行补充")
	if err != nil {
		t.Fatal(err)
	}
	if out.FinalAnswer != "结果是 21\n第二行补充" {
		t.Fatalf("final answer 不符: %q", out.FinalAnswer)
	}
	if out.Action != "" {
		t.Fatalf("不应有 action: %q", out.Action)
	}
}

func TestParseReActMalformed(t *testing.T) {
	if _, err := ParseReAct("随便说点什么,没有任何标记"); err == nil {
		t.Fatal("无标记输出期望报错")
	}
}

func TestParseReActActionWithoutInput(t *testing.T) {
	out, err := ParseReAct("Thought: 查看待办\nAction: todo\nAction Input:")
	if err != nil {
		t.Fatal(err)
	}
	if out.ActionInput != "{}" {
		t.Fatalf("空 Action Input 应默认为 {},得到 %q", out.ActionInput)
	}
}
