package tool

import (
	"context"
	"fmt"
)

// WebSearchTool — Web 搜索工具。当前是假实现，返回固定模板文本。
// 想用真的？换 SerpAPI、Bing API 或者 DuckDuckGo 的 HTML 抓取都行，改 Execute 就行。
type WebSearchTool struct{}

func (w *WebSearchTool) Name() string { return "web_search" }
func (w *WebSearchTool) Description() string {
	return "在互联网上搜索信息，返回相关摘要。"
}
func (w *WebSearchTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "搜索关键词",
			},
		},
		"required": []string{"query"},
	}
}

// Execute — 返回一段假搜索结果。query 会嵌入模板里，至少看起来像那么回事。
func (w *WebSearchTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	query, ok := args["query"].(string)
	if !ok {
		return "", fmt.Errorf("缺少 query 参数")
	}
	// 假的，别当真
	return fmt.Sprintf("关于 '%s' 的搜索结果：这是一段模拟的摘要，显示相关网页内容。实际部署时可替换为 SerpAPI 等真实搜索。", query), nil
}
