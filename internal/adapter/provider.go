// adapter 包 — LLM 厂商的统一抽象层。
// 不管后端是 OpenAI 还是 DeepSeek，对上层都是同一个 LLMProvider 接口。
// 加新厂商只需要新建一个文件，在 init() 里 Register 就行，不用改任何旧代码。
package adapter

import (
	"context"
	"fmt"
	"sync"
)

// LLMProvider 是所有 LLM 服务必须实现的接口。
// Chat 返回完整回复（内部调 ChatStream 攒完再返回），
// ChatStream 返回一个 channel，适合终端实时打字输出。
// ModelName 告诉你当前用的是什么模型。
type LLMProvider interface {
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
	ChatStream(ctx context.Context, req *ChatRequest) (<-chan StreamChunk, error)
	ModelName() string
}

// ChatRequest — 一次 LLM 调用的入参。
// Temperature/MaxTokens 为 0 表示用 Provider 默认值。
type ChatRequest struct {
	Messages    []Message
	Temperature float32
	MaxTokens   int
}

// Message — 对话中的一条消息。Role 是 system/user/assistant。
type Message struct {
	Role    string
	Content string
}

// ChatResponse — LLM 返回结果。
type ChatResponse struct {
	Content string
	Usage   TokenUsage // token 用量，目前没用到，预留
}

// StreamChunk — 流式响应的一个片段。
// Delta 是增量文本，Done 表示流结束，Error 非 nil 说明中间出了错。
type StreamChunk struct {
	Delta string
	Done  bool
	Error error
}

// TokenUsage — 每次请求的 token 统计。
type TokenUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// ProviderFactory — 工厂函数签名，接收配置返回一个 Provider。
type ProviderFactory func(cfg ProviderConfig) (LLMProvider, error)

var (
	registryMu sync.Mutex
	registry   = map[string]ProviderFactory{}
)

// ProviderConfig — 创建 Provider 所需的参数。
// 在 adapter 包里自己定义一份，避免和 config 包循环依赖。
type ProviderConfig struct {
	APIKey  string
	BaseURL string // 空就用默认地址
	Model   string
}

// Register — 注册一个 Provider 工厂。通常在 init() 里调用。
func Register(name string, factory ProviderFactory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[name] = factory
}

// Create — 按名字创建 Provider 实例。
func Create(name string, cfg ProviderConfig) (LLMProvider, error) {
	registryMu.Lock()
	factory, ok := registry[name]
	registryMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unknown provider: %s", name)
	}
	return factory(cfg)
}
