// openai.go — OpenAI（及兼容接口）的 Provider 实现。
// Chat 内部调 ChatStream 攒完整回复再返回，所以实现重点在 ChatStream。
package adapter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const openaiDefaultBase = "https://api.openai.com/v1"

func init() {
	Register("openai", func(cfg ProviderConfig) (LLMProvider, error) {
		if cfg.BaseURL == "" {
			cfg.BaseURL = openaiDefaultBase
		}
		return &openAIProvider{
			apiKey:  cfg.APIKey,
			baseURL: cfg.BaseURL,
			model:   cfg.Model,
			client:  &http.Client{Timeout: 60 * time.Second},
		}, nil
	})
}

type openAIProvider struct {
	apiKey  string
	baseURL string
	model   string
	client  *http.Client
}

func (p *openAIProvider) ModelName() string { return p.model }

// Chat — 同步调用，攒 ChatStream 的 delta 拼成完整回复。
func (p *openAIProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	stream, err := p.ChatStream(ctx, req)
	if err != nil {
		return nil, err
	}
	var full string
	for chunk := range stream {
		if chunk.Error != nil {
			return nil, chunk.Error
		}
		full += chunk.Delta
	}
	return &ChatResponse{Content: full}, nil
}

// ChatStream — 核心方法，发送 streaming 请求，解析 SSE 返回的逐 token delta。
// 返回的 channel 在流结束或出错时会被 close，调用方只管 for range。
func (p *openAIProvider) ChatStream(ctx context.Context, req *ChatRequest) (<-chan StreamChunk, error) {
	body := map[string]interface{}{
		"model":    p.model,
		"messages": convertMessages(req.Messages),
		"stream":   true,
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("API error: %s", resp.Status)
	}

	ch := make(chan StreamChunk, 10) // 带缓冲，避免消费者慢时阻塞 SSE 读取
	go func() {
		defer resp.Body.Close()
		defer close(ch) // 不管成功失败，最后一定 close
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				ch <- StreamChunk{Done: true}
				return
			}
			var streamResp struct {
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
				} `json:"choices"`
			}
			if err := json.Unmarshal([]byte(data), &streamResp); err != nil {
				ch <- StreamChunk{Error: err}
				return
			}
			if len(streamResp.Choices) > 0 {
				delta := streamResp.Choices[0].Delta.Content
				if delta != "" {
					ch <- StreamChunk{Delta: delta}
				}
			}
		}
		if err := scanner.Err(); err != nil {
			ch <- StreamChunk{Error: err}
		} else {
			ch <- StreamChunk{Done: true}
		}
	}()
	return ch, nil
}

// convertMessages — 把内部的 []Message 转成 API 需要的 []map[string]string。
func convertMessages(msgs []Message) []map[string]string {
	res := make([]map[string]string, 0, len(msgs))
	for _, m := range msgs {
		res = append(res, map[string]string{"role": m.Role, "content": m.Content})
	}
	return res
}
