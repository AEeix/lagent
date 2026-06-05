// tool 包 — 工具接口和注册表。
// 所有工具都得实现 Tool 接口（Name + Description + InputSchema + Execute），
// 然后 Register 到 Registry 里，Agent 就能发现和调用了。
package tool

import (
	"context"
	"fmt"
	"sort"
)

// Tool — 工具接口，所有内置工具和 MCP 工具都要实现。
// Execute 接收 args，返回字符串结果。错误也通过返回字符串表达即可（除非要中断流程）。
type Tool interface {
	Name() string
	Description() string                          // 用于拼 prompt 和 TF-IDF 匹配
	InputSchema() map[string]interface{}          // 目前预留，未实际使用
	Execute(ctx context.Context, args map[string]interface{}) (string, error)
}

// Registry — 工具注册表，线程不安全的（目前全是启动时注册，没并发问题）。
type Registry struct {
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

func (r *Registry) Register(t Tool) {
	r.tools[t.Name()] = t
}

func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

func (r *Registry) List() []Tool {
	res := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		res = append(res, t)
	}
	return res
}

// GetAllDescriptions — 把所有工具的"名字: 描述"拼成一段文本，塞进 system prompt。
func (r *Registry) GetAllDescriptions() string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names) // 按字母序排，保证输出稳定
	res := ""
	for _, name := range names {
		t := r.tools[name]
		res += fmt.Sprintf("- %s: %s\n", name, t.Description())
	}
	return res
}
