package tool

import "mini-agent/llm"

// Registry 是工具注册表:agent 只依赖 registry,不 import 任何具体工具。
type Registry struct {
	order []string
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

func (r *Registry) Register(t Tool) {
	if _, ok := r.tools[t.Name()]; !ok {
		r.order = append(r.order, t.Name())
	}
	r.tools[t.Name()] = t
}

func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

func (r *Registry) All() []Tool {
	out := make([]Tool, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.tools[name])
	}
	return out
}

// ToolDefs 生成 chat/completions 请求的 tools 数组。
func (r *Registry) ToolDefs() []llm.ToolDef {
	out := make([]llm.ToolDef, 0, len(r.order))
	for _, t := range r.All() {
		out = append(out, llm.ToolDef{
			Type: "function",
			Function: llm.FuncDef{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.Parameters(),
			},
		})
	}
	return out
}
