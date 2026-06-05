// deepseek.go — DeepSeek Provider。
// DeepSeek API 兼容 OpenAI 接口格式，直接复用 openAIProvider，只改默认 BaseURL。
package adapter

import (
	"net/http"
	"time"
)

const deepseekDefaultBase = "https://api.deepseek.com/v1"

func init() {
	Register("deepseek", func(cfg ProviderConfig) (LLMProvider, error) {
		if cfg.BaseURL == "" {
			cfg.BaseURL = deepseekDefaultBase
		}
		return &openAIProvider{
			apiKey:  cfg.APIKey,
			baseURL: cfg.BaseURL,
			model:   cfg.Model,
			client:  &http.Client{Timeout: 60 * time.Second},
		}, nil
	})
}
