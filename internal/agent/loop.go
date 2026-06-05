// Package agent 实现 LAgent 的核心 Agent 循环。
//
// Agent 是与 LLM 交互的核心调度单元，负责：
//   - 构建 system prompt（包含工具列表和调用格式说明）
//   - 管理对话历史（滑动窗口裁剪）
//   - 解析 LLM 返回的工具调用指令（<tool_call> XML 标签格式）
//   - 执行工具并将结果反馈给 LLM，形成多轮工具调用循环
//   - 当工具超过 5 个时，使用 TF-IDF Rerank 筛选 Top-3 相关工具以节省 token
//
// 【架构说明】当前 Agent 每次 Run 都会重建 system prompt 并做 Rerank，
// 这意味着同一对话中不同轮次的工具列表可能不同。这是刻意设计：
// 每次根据用户最新输入动态选择最相关的工具，避免无关工具干扰 LLM。
//
// 【工具调用格式】使用自定义 XML 标签而非 OpenAI Function Calling：
//
//	<tool_call>{"name":"calculator","args":{"expression":"2+3"}}</tool_call>
//
// 这是为了跨 Provider 兼容（DeepSeek 等对 native function calling 支持程度不同）。
// 缺点是需要 LLM 生成严格格式的 JSON，可能解析失败；优点是简单、可调试。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"lagent/internal/adapter"
	"lagent/internal/mcp"
	"lagent/internal/tool"
)

// Agent 是 AI Agent 的核心结构体，封装了 LLM 调用和工具执行逻辑。
//
// 字段说明:
//   - provider: LLM 服务提供者（OpenAI/DeepSeek 等），通过接口解耦
//   - registry: 工具注册表，包含所有可用工具（内置 + MCP 扩展）
//   - systemPrompt: 基础系统提示词，由调用方（main）注入，Agent 会在此基础上追加工具描述
type Agent struct {
	provider     adapter.LLMProvider
	registry     *tool.Registry
	systemPrompt string
}

// NewAgent 创建一个新的 Agent 实例。
//
// 参数:
//   - provider: 已初始化的 LLM Provider（必须非 nil）
//   - registry: 工具注册表（必须非 nil，可以空注册表）
//   - systemPrompt: 基础角色设定，Agent 会自动追加工具列表和调用格式说明
func NewAgent(provider adapter.LLMProvider, registry *tool.Registry, systemPrompt string) *Agent {
	return &Agent{
		provider:     provider,
		registry:     registry,
		systemPrompt: systemPrompt,
	}
}

// maxIter 限制工具调用的最大轮次，防止 LLM 陷入死循环（如工具反复出错）。
const maxIter = 5

// maxHistoryMessages 限制注入给 LLM 的历史消息条数。
//
// 滑动窗口裁剪的原因：
//  1. LLM 上下文窗口有限（虽然现在很多支持 128K+，但 token 消耗与历史长度正相关）
//  2. 长历史会稀释 system prompt 的指令约束力
//  3. 工具调用的中间消息会快速增长，需限制回溯范围
//
// 【注意】这个值可根据模型上下文窗口大小调整。20 条对于大多数场景够用，
// 但对于需要长程推理的任务可能不够，未来可改为可配置参数。
const maxHistoryMessages = 20

// Run 是 Agent 的主入口方法，接收用户输入并返回 LLM 的最终回复。
//
// 处理流程:
//  1. Rerank 工具 → 筛选最相关的 Top-3 工具描述
//  2. 构建完整 system prompt（基础设定 + 工具描述 + 调用格式说明）
//  3. 裁剪对话历史（滑动窗口，保留最近 maxHistoryMessages 条）
//  4. 调用 LLM，检查回复中是否包含 <tool_call> 标签
//  5. 如果有工具调用 → 解析 → 执行 → 将结果反馈给 LLM → 回到步骤 4（最多 maxIter 轮）
//  6. 如果没有工具调用 → 直接返回回复内容
//
// 【注意】工具执行错误不会被静默吞掉，而是以 "工具执行错误: xxx" 的形式反馈给 LLM，
// 让 LLM 有机会修正参数后重试。但如果 maxIter 耗尽仍未得到最终回复，会返回错误。
//
// 【注意】history 中的消息顺序必须为: [system, user, assistant, user, assistant, ...]
// 但 Agent 内部会重新构建消息列表，确保 system 在最前面。
func (a *Agent) Run(ctx context.Context, userInput string, history []adapter.Message) (string, error) {
	// ---- 步骤 1: Rerank 工具，获取与用户输入最相关的工具描述 ----
	toolsDesc := a.getRelevantTools(userInput)

	// ---- 步骤 2: 构建完整的 system prompt ----
	systemContent := a.buildSystemPrompt(toolsDesc)

	// ---- 步骤 3: 裁剪对话历史（滑动窗口） ----
	if len(history) > maxHistoryMessages {
		history = history[len(history)-maxHistoryMessages:]
	}

	// ---- 步骤 4: 组装完整消息列表 ----
	// 【重要】system 消息必须放在最前面，这符合所有 LLM API 的规范
	messages := make([]adapter.Message, 0, len(history)+2)
	messages = append(messages, adapter.Message{Role: "system", Content: systemContent})
	messages = append(messages, history...)
	messages = append(messages, adapter.Message{Role: "user", Content: userInput})

	// ---- 步骤 5: 工具调用循环 ----
	for i := 0; i < maxIter; i++ {
		resp, err := a.provider.Chat(ctx, &adapter.ChatRequest{Messages: messages})
		if err != nil {
			return "", fmt.Errorf("LLM 调用失败 (第 %d 轮): %w", i+1, err)
		}
		content := resp.Content

		// 检查 LLM 回复中是否有工具调用标记
		if strings.Contains(content, "<tool_call>") {
			// 解析工具调用 JSON
			toolCall, err := parseToolCall(content)
			if err != nil {
				// 解析失败时，将错误反馈给 LLM 让其重新生成
				// 【注意】这不会消耗 maxIter 之外的轮次，因为我们已经 append 了消息
				messages = append(messages, adapter.Message{Role: "assistant", Content: content})
				messages = append(messages, adapter.Message{Role: "user", Content: "解析工具调用时出错: " + err.Error()})
				continue
			}

			// 查找并执行工具
			t, ok := a.registry.Get(toolCall.Name)
			if !ok {
				// 工具不存在，反馈给 LLM（可能是 LLM 幻觉出了不存在的工具名）
				messages = append(messages, adapter.Message{Role: "assistant", Content: content})
				messages = append(messages, adapter.Message{Role: "user", Content: fmt.Sprintf("未找到工具: %s", toolCall.Name)})
				continue
			}

			// 执行工具 — 这是唯一会产生副作用的地方
			toolResult, err := t.Execute(ctx, toolCall.Args)
			if err != nil {
				// 执行失败也反馈给 LLM，让它尝试修正参数
				toolResult = fmt.Sprintf("工具执行错误: %v", err)
			}

			// 将工具调用（LLM 原始输出）和工具执行结果一起追加到消息列表
			// 【注意】这里追加的是 assistant role 的原始输出和 user role 的工具结果，
			// 这种 pattern 被大多数 LLM 良好支持
			messages = append(messages, adapter.Message{Role: "assistant", Content: content})
			messages = append(messages, adapter.Message{Role: "user", Content: fmt.Sprintf("工具结果: %s", toolResult)})
		} else {
			// 无工具调用 — 这就是最终回复
			return content, nil
		}
	}

	// maxIter 耗尽，LLM 始终在请求工具调用而未给出最终回复
	return "", fmt.Errorf("超过最大工具调用轮次 (%d)，Agent 未能给出最终回复", maxIter)
}

// RunStream 和 Run 逻辑一致，区别是：最终回复通过 ChatStream 流式输出到 w，
// 而中间的工具调用回合只在终端打印简短的状态提示，不展示原始 XML。
// w 通常是 os.Stdout，调用方也可以传其他 io.Writer。
func (a *Agent) RunStream(ctx context.Context, userInput string, history []adapter.Message, w io.Writer) (string, error) {
	toolsDesc := a.getRelevantTools(userInput)
	systemContent := a.buildSystemPrompt(toolsDesc)

	if len(history) > maxHistoryMessages {
		history = history[len(history)-maxHistoryMessages:]
	}

	messages := make([]adapter.Message, 0, len(history)+2)
	messages = append(messages, adapter.Message{Role: "system", Content: systemContent})
	messages = append(messages, history...)
	messages = append(messages, adapter.Message{Role: "user", Content: userInput})

	for i := 0; i < maxIter; i++ {
		// 用流式接口逐 token 收，攒到 builder 里
		stream, err := a.provider.ChatStream(ctx, &adapter.ChatRequest{Messages: messages})
		if err != nil {
			return "", fmt.Errorf("LLM 调用失败 (第 %d 轮): %w", i+1, err)
		}
		var full strings.Builder
		for chunk := range stream {
			if chunk.Error != nil {
				return "", chunk.Error
			}
			full.WriteString(chunk.Delta)
		}
		content := full.String()

		if strings.Contains(content, "<tool_call>") {
			// 工具调用 — 不在终端展示原始 XML，只打一行状态
			toolCall, err := parseToolCall(content)
			if err != nil {
				messages = append(messages, adapter.Message{Role: "assistant", Content: content})
				messages = append(messages, adapter.Message{Role: "user", Content: "解析工具调用时出错: " + err.Error()})
				continue
			}
			t, ok := a.registry.Get(toolCall.Name)
			if !ok {
				messages = append(messages, adapter.Message{Role: "assistant", Content: content})
				messages = append(messages, adapter.Message{Role: "user", Content: fmt.Sprintf("未找到工具: %s", toolCall.Name)})
				continue
			}
			fmt.Fprintf(w, "  [调用工具: %s...]\n", toolCall.Name)
			toolResult, err := t.Execute(ctx, toolCall.Args)
			if err != nil {
				toolResult = fmt.Sprintf("工具执行错误: %v", err)
			}
			messages = append(messages, adapter.Message{Role: "assistant", Content: content})
			messages = append(messages, adapter.Message{Role: "user", Content: fmt.Sprintf("工具结果: %s", toolResult)})
		} else {
			// 最终回复 — 一次性输出（已攒完，直接写）
			fmt.Fprint(w, content)
			return content, nil
		}
	}
	return "", fmt.Errorf("超过最大工具调用轮次 (%d)，Agent 未能给出最终回复", maxIter)
}

// getRelevantTools 根据用户输入，返回最相关工具的格式化描述。
//
// 策略:
//   - 工具总数 ≤ 5: 返回全部工具描述（无需筛选，全量注入 token 开销可接受）
//   - 工具总数 > 5: 使用 TF-IDF 相似度计算，返回 Top-3 最相关的工具描述
//
// 【设计决策】阈值 5 是基于经验：大多数 LLM 在 5 个工具描述（约 200-500 token）
// 时能正确处理；超过则需要筛选以避免：
//  1. 上下文被无关工具描述占满
//  2. LLM 选择困难（工具间的功能重叠导致选错工具）
//
// 【注意】TF-IDF Rerank 是在用户输入和工具描述文本之间做相似度匹配，
// 因此工具的描述质量直接影响 Rerank 准确性。
func (a *Agent) getRelevantTools(userInput string) string {
	tools := a.registry.List()
	if len(tools) <= 5 {
		return a.registry.GetAllDescriptions()
	}

	// 为每个工具描述构建 TF-IDF 索引
	docs := make([]string, len(tools))
	for i, t := range tools {
		docs[i] = t.Description()
	}

	// 使用简单的 TF-IDF 计算每个工具描述与用户输入的相似度
	reranker := mcp.NewSimpleTFIDF(docs)
	scores := make([]float64, len(tools))
	for i := range tools {
		scores[i] = reranker.Score(userInput, i)
	}

	// 取 Top-3 最高分的工具
	indices := topK(scores, 3)
	desc := ""
	for _, idx := range indices {
		desc += fmt.Sprintf("- %s: %s\n", tools[idx].Name(), tools[idx].Description())
	}
	return desc
}

// buildSystemPrompt 在基础 system prompt 上追加工具列表和调用格式说明。
//
// 输出格式示例:
//
//	你是一个有帮助的助手。
//
//	可用工具列表：
//	- calculator: 计算数学表达式，输入如 "2+3*4"
//	- web_search: 在互联网上搜索信息
//
//	如果需要使用工具，请严格返回 <tool_call>{"name":"工具名", "args":{...}}</tool_call> 格式的 JSON 调用。
//
// 【关键】工具调用格式说明必须清晰、精确，因为 LLM 需要生成严格可解析的 JSON。
// 不正确的格式会导致 parseToolCall 失败，浪费一轮对话。
func (a *Agent) buildSystemPrompt(toolsDesc string) string {
	prompt := a.systemPrompt
	if toolsDesc != "" {
		prompt += "\n\n可用工具列表：\n" + toolsDesc
		prompt += "\n如果需要使用工具，请严格返回 <tool_call>{\"name\":\"工具名\", \"args\":{...}}</tool_call> 格式的 JSON 调用。"
	}
	return prompt
}

// toolCall 表示 LLM 请求的一次工具调用。
//
// JSON 格式:
//
//	{"name": "calculator", "args": {"expression": "2+3"}}
//
// 【注意】Args 使用 map[string]interface{} 以兼容任意工具的参数结构，
// 具体类型的校验由各个工具的 Execute 方法自行处理。
type toolCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}

// parseToolCall 从 LLM 回复文本中提取并解析工具调用 JSON。
//
// 期望格式:
//
//	<tool_call>{"name":"calculator","args":{"expression":"2+3"}}</tool_call>
//
// 解析步骤:
//  1. 定位 <tool_call> 和 </tool_call> 标签
//  2. 提取标签内的 JSON 文本
//  3. 清理可能的额外引号/反引号
//  4. JSON 反序列化
//
// 【注意】当前实现只提取第一个 <tool_call> 块，如果 LLM 返回多个工具调用，
// 后续的会被忽略。未来可扩展为返回 []toolCall 支持并行工具调用。
//
// 【鲁棒性】LLM 可能在 JSON 周围添加 ```json 代码块标记或额外的引号，
// 当前处理较简单，对于复杂的嵌套 JSON（如 args 中包含特殊字符）可能解析失败。
func parseToolCall(content string) (*toolCall, error) {
	start := strings.Index(content, "<tool_call>")
	end := strings.Index(content, "</tool_call>")
	if start == -1 || end == -1 {
		return nil, fmt.Errorf("未找到 tool_call 标签")
	}
	jsonStr := content[start+len("<tool_call>") : end]

	// 清理可能的包围字符（引号、反引号、空白）
	jsonStr = strings.Trim(jsonStr, "`\" \t\n")

	var tc toolCall
	if err := json.Unmarshal([]byte(jsonStr), &tc); err != nil {
		return nil, fmt.Errorf("解析 JSON 出错: %w", err)
	}
	return &tc, nil
}

// topK 返回分数最高的 k 个索引，按分数降序排列。
//
// 这是一个简单的 O(n log n) 实现，适合 n 较小时（工具数量通常 < 100）。
// 如果 n 很大（> 10000），可考虑使用堆来优化到 O(n log k)。
func topK(scores []float64, k int) []int {
	type pair struct {
		idx   int
		score float64
	}
	pairs := make([]pair, len(scores))
	for i, s := range scores {
		pairs[i] = pair{i, s}
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].score > pairs[j].score })
	res := make([]int, k)
	for i := 0; i < k && i < len(pairs); i++ {
		res[i] = pairs[i].idx
	}
	return res
}
