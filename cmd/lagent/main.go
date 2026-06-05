// Package main 是 LAgent 的入口包。
// LAgent 是一个基于 Go 语言开发的个人 AI Agent 助手，支持：
//   - 多模型集成（OpenAI、DeepSeek 等，通过统一接口接入）
//   - 工具调用（内置计算器、Web 搜索，支持 MCP 协议扩展）
//   - 分层记忆（短期对话上下文 + 长期 SQLite 记忆）
//   - 多轮会话管理（创建、切换、恢复、持久化）
//
// 启动方式: lagent <config.json> [--model=xxx]
// 交互命令:
//
//	/session new|list|switch <id>|resume <id>  — 会话管理
//	/mem add <key>:<value>                      — 添加长期记忆
//	/bye                                        — 退出
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"lagent/internal/adapter"
	"lagent/internal/agent"
	"lagent/internal/config"
	"lagent/internal/mcp"
	"lagent/internal/memory"
	"lagent/internal/router"
	"lagent/internal/session"
	"lagent/internal/tool"
)

// main 是程序入口，负责以下初始化流程和主循环：
//
//	配置加载 → Provider 初始化 → 路由器 → 工具注册 → MCP → 记忆存储 → 会话管理 → CLI 交互循环
//
// 【注意】初始化顺序有依赖关系：Provider 和 Router 必须在 Agent 之前初始化；
// 记忆存储和会话管理器是独立的，可以在 Agent 之前或之后初始化，但必须在交互循环之前完成。
func main() {
	// ---- 参数解析 ----
	// 第一个参数是配置文件路径，后续可选参数（如 --model=xxx）由 config.Load 内部处理
	// 没传配置文件时，自动找同目录下的 config/lagent.json（方便双击运行）
	// --save：把启动时输入的 API Key 写回配置文件
	configPath := "config/lagent.json"
	saveMode := false
	extraArgs := os.Args[1:]
	if len(os.Args) >= 2 {
		configPath = os.Args[1]
		extraArgs = os.Args[2:]
	}
	// 从 extraArgs 里过滤掉 --save，config.Load 不认识它
	filtered := make([]string, 0, len(extraArgs))
	for _, a := range extraArgs {
		if a == "--save" {
			saveMode = true
		} else {
			filtered = append(filtered, a)
		}
	}
	extraArgs = filtered

	// ---- 1. 加载配置 ----
	// config.Load 使用 Viper 读取 JSON 配置文件，并允许命令行覆盖部分字段
	cfg, err := config.Load(configPath, extraArgs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		os.Exit(1)
	}
	// ---- 启动引言 ----
	printbanner()
	// ---- 1.5 交互式输入 API Key ----
	// 配置里没填 api_key 的 provider，启动时让用户手动输入
	// 如果带 --save，输入完会写回 JSON 文件，下次不用再输
	reader := bufio.NewReader(os.Stdin)
	anyInput := false             // 标记用户有没有输入新 key
	defName := cfg.Router.Default // 默认模型优先问

	// 第一步：先问默认模型（deepseek）
	if pCfg, ok := cfg.Providers[defName]; ok && pCfg.APIKey == "" {
		fmt.Printf("请输入 %s 的 API Key（%s / %s）: ", defName, pCfg.BaseURL, pCfg.Model)
		key, err := reader.ReadString('\n')
		if err != nil {
			fmt.Fprintf(os.Stderr, "读取失败: %v\n", err)
			os.Exit(1)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			fmt.Printf("未输入 Key，跳过 %s\n", defName)
		} else {
			pCfg.APIKey = key
			cfg.Providers[defName] = pCfg
			anyInput = true
		}
	}

	// 第二步：再问剩下的
	for name, pCfg := range cfg.Providers {
		if name == defName {
			continue // 默认的已经问过了
		}
		if pCfg.APIKey == "" {
			fmt.Printf("请输入 %s 的 API Key（%s / %s）: ", name, pCfg.BaseURL, pCfg.Model)
			key, err := reader.ReadString('\n')
			if err != nil {
				fmt.Fprintf(os.Stderr, "读取失败: %v\n", err)
				os.Exit(1)
			}
			key = strings.TrimSpace(key)
			if key == "" {
				fmt.Printf("未输入 Key，跳过 %s\n", name)
				continue
			}
			pCfg.APIKey = key
			cfg.Providers[name] = pCfg
			anyInput = true
		}
	}
	// 用户要求回写
	if saveMode && anyInput {
		data, err := json.MarshalIndent(map[string]interface{}{
			"providers": cfg.Providers,
			"router":    cfg.Router,
			"mcp":       cfg.MCP,
		}, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "序列化配置失败: %v\n", err)
		} else {
			if err := os.WriteFile(configPath, append(data, '\n'), 0644); err != nil {
				fmt.Fprintf(os.Stderr, "写回配置文件失败: %v\n", err)
			} else {
				fmt.Printf("已保存 API Key 到 %s\n", configPath)
			}
		}
	}
	fmt.Println()

	// ---- 2. 初始化 LLM Provider ----
	// 遍历配置中所有 provider，通过工厂模式创建对应的 LLMProvider 实例
	// 每个 provider 由 name 标识（如 "openai"、"deepseek"），在配置文件中定义
	providers := make(map[string]adapter.LLMProvider)
	for name, pCfg := range cfg.Providers {
		prov, err := adapter.Create(name, adapter.ProviderConfig{
			APIKey:  pCfg.APIKey,
			BaseURL: pCfg.BaseURL,
			Model:   pCfg.Model,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "初始化 provider %s 失败: %v\n", name, err)
			os.Exit(1)
		}
		providers[name] = prov
	}

	// ---- 3. 初始化路由器 ----
	// 路由器根据用户输入的正则匹配，动态选择使用哪个模型
	// 例如：数学问题 → deepseek，代码问题 → openai
	r, err := router.New(cfg.Router)
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化路由失败: %v\n", err)
		os.Exit(1)
	}

	// 如果 CLI 配置中指定了强制模型，则所有请求都使用该模型，跳过路由器
	forceModel := ""
	if cfg.CLI != nil {
		forceModel = cfg.CLI.ForceModel
	}

	// ---- 4. 初始化工具注册表 ----
	// 注册表是 Agent 获知可用工具的唯一途径，所有工具必须实现 tool.Tool 接口
	toolRegistry := tool.NewRegistry()
	toolRegistry.Register(&tool.CalculatorTool{})                       // 算术表达式求值工具
	toolRegistry.Register(&tool.WebSearchTool{})                        // Web 搜索工具（DuckDuckGo）
	toolRegistry.Register(tool.NewDummyTool("dummy_0", "dummy tool 0")) // 占位测试工具
	toolRegistry.Register(tool.NewDummyTool("dummy_1", "dummy tool 1"))
	// 【注意】当工具总数超过 5 个时，Agent 会触发 TF-IDF Rerank，只将 Top-3 相关工具注入 prompt
	// 这是为了节省 token 并避免 LLM 被过多工具描述干扰

	// ---- 4.1 MCP 工具扩展（可选） ----
	// MCP (Model Context Protocol) 允许通过外部进程动态扩展工具能力
	// 每个 MCP Server 通过 stdio 与主进程通信，Agent 自动发现并注册其提供的工具
	if len(cfg.MCP) > 0 {
		for _, mcpCfg := range cfg.MCP {
			client, err := mcp.NewClient(mcp.ClientConfig{
				Name:    mcpCfg.Name,
				Command: mcpCfg.Command,
				Args:    mcpCfg.Args,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "启动 MCP Server %s 失败: %v\n", mcpCfg.Name, err)
				continue // 【注意】单个 MCP Server 失败不影响其他 Server 和主程序运行
			}
			tools, err := client.ListTools()
			if err != nil {
				fmt.Fprintf(os.Stderr, "获取 MCP 工具列表失败 (%s): %v\n", mcpCfg.Name, err)
				client.Close()
				continue
			}
			for _, t := range tools {
				toolRegistry.Register(t)
				fmt.Printf("已注册 MCP 工具: %s (来自 %s)\n", t.Name(), mcpCfg.Name)
			}
			// 【注意】MCP client 未显式 Close()，随进程退出由 OS 回收。
			// 如果需要优雅关闭，应在信号处理中调用 client.Close()
		}
	}

	// ---- 5. 记忆存储（SQLite） ----
	// 长期记忆按 userID + project 维度隔离，通过 LIKE 查询检索相关记忆注入到 LLM 上下文
	memStore, err := memory.NewStore("lagent_mem.db")
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化记忆存储失败: %v\n", err)
		os.Exit(1)
	}
	defer memStore.Close()

	// ---- 6. 会话管理器 ----
	// 会话保存完整的对话历史（[]adapter.Message），序列化为 JSON 存储在 SQLite 中
	sessMgr, err := session.NewManager("lagent_sessions.db")
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化会话管理器失败: %v\n", err)
		os.Exit(1)
	}
	defer sessMgr.Close()

	// ---- 7. 创建初始会话 ----
	// 每次启动默认创建新会话；用户可通过 /session resume <id> 恢复历史会话
	currentSession, err := sessMgr.NewSession("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "创建初始会话失败: %v\n", err)
		os.Exit(1)
	}
	userID := "default_user"     // 【注意】当前为硬编码，多用户场景应从配置或环境变量读取
	project := "default_project" // 【注意】当前为硬编码，多项目场景应从配置或环境变量读取

	fmt.Println("LAgent 已启动。命令: /session new|list|switch <id>|resume <id> ; /mem add <key>:<value> ; /bye 退出。")

	// ---- 8. CLI 交互主循环 ----
	scanner := bufio.NewScanner(os.Stdin)
	history := currentSession.Messages

	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break // EOF（Ctrl+D）时退出
		}
		input := scanner.Text()
		if strings.TrimSpace(input) == "" {
			continue
		}

		// ---- 处理斜杠命令（/session, /mem, /bye） ----
		// 【注意】命令处理可能修改 currentSession 指针和 history 切片引用，
		// 因此传递的是指针的指针（**session.Session）和切片的指针（*[]adapter.Message）
		if strings.HasPrefix(input, "/") {
			if input == "/bye" {
				break
			}
			if handleCommand(input, &currentSession, sessMgr, memStore, userID, project, &history) {
				continue // 已处理，跳过 LLM 调用
			}
		}

		// ---- 检索长期记忆 ----
		// 将用户输入作为查询，从记忆库中搜索最相关的 Top-3 条记忆
		// 这些记忆会被拼接到 system prompt 中，供 LLM 参考
		memories, err := memStore.Search(userID, project, input, 3)
		if err != nil {
			fmt.Println(os.Stderr, "记忆检索失败: %v\n", err)
		}
		memContext := ""
		for _, m := range memories {
			memContext += fmt.Sprintf("- %s: %s\n", m.Key, m.Value)
		}

		// ---- 动态选择模型 ----
		// 优先级: CLI 强制指定 > 路由器正则匹配 > 默认模型
		modelName := forceModel
		if modelName == "" {
			modelName = r.Route(input)
		}
		prov, ok := providers[modelName]
		if !ok {
			fmt.Printf("模型 %s 未找到，使用默认模型\n", modelName)
			prov = providers[cfg.Router.Default] // 兜底到默认模型
		}

		// ---- 构建 System Prompt ----
		// 系统提示词 = 基础角色设定 + 长期记忆上下文
		// Agent 内部还会追加工具列表和工具调用格式说明
		systemPrompt := "你是一个有帮助的助手。"
		if memContext != "" {
			systemPrompt += "\n\n相关记忆：\n" + memContext
		}
		ag := agent.NewAgent(prov, toolRegistry, systemPrompt)

		// ---- 调用 Agent（流式输出） ----
		fmt.Print("AI: ")
		reply, err := ag.RunStream(context.Background(), input, history, os.Stdout)
		fmt.Println() // 回复结束后换行
		if err != nil {
			fmt.Fprintf(os.Stderr, "\n错误: %v\n", err)
			continue
		}

		// ---- 更新对话历史并持久化 ----
		// 【注意】每条消息都立即持久化到 SQLite，避免崩溃丢失上下文；
		// 但这也意味着每次交互都有一次写盘操作，高并发场景需考虑批量写入优化
		history = append(history, adapter.Message{Role: "user", Content: input})
		history = append(history, adapter.Message{Role: "assistant", Content: reply})
		currentSession.Messages = history
		sessMgr.UpdateMessages(currentSession.ID, history)
	}

	// 退出前最后一次保存，确保所有历史已持久化
	sessMgr.UpdateMessages(currentSession.ID, history)
}

func printbanner() {
	fmt.Println("========================================")
	fmt.Println("  LAgent — 个人 AI Agent 助手")
	fmt.Println("  Go 语言课程项目")
	fmt.Println("========================================")
}

// handleCommand 处理以 "/" 开头的内置命令。
//
// 参数说明:
//   - sessPtr: 指向当前会话指针的指针 — 允许在命令中切换会话
//   - history: 指向当前对话历史切片的指针 — 切换会话时需要同步替换
//
// 返回值: true 表示命令已处理（跳过 LLM 调用），false 表示不是已知命令
//
// 支持的命令:
//
//	/session new [name]           创建新会话并切换
//	/session list                 列出所有会话
//	/session switch <id>          切换到指定会话
//	/session resume <id>          恢复指定会话（同 switch）
//	/mem add <key>:<value>        添加长期记忆
//
// 【注意】切换会话时务必先保存当前会话的消息，再替换指针和切片引用，
// 否则会导致当前会话的消息丢失。
func handleCommand(input string, sessPtr **session.Session, sessMgr *session.Manager,
	memStore *memory.Store, userID, project string, history *[]adapter.Message) bool {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return true
	}
	switch parts[0] {
	case "/session":
		if len(parts) < 2 {
			fmt.Println("用法: /session new|list|switch <id>|resume <id>")
			return true
		}
		switch parts[1] {
		case "new":
			// 创建新会话的流程：
			// 1) 保存当前会话的消息 → 2) 创建新会话 → 3) 切换指针和 history
			name := ""
			if len(parts) > 2 {
				name = strings.Join(parts[2:], " ")
			}
			newSess, err := sessMgr.NewSession(name)
			if err != nil {
				fmt.Println("创建会话失败:", err)
				return true
			}
			// 【关键】先保存当前会话，再切换
			(*sessPtr).Messages = *history
			sessMgr.UpdateMessages((*sessPtr).ID, (*sessPtr).Messages)
			*sessPtr = newSess
			*history = nil // 新会话无历史
			fmt.Printf("已切换到新会话 %s\n", newSess.ID)
			return true
		case "list":
			sessions, err := sessMgr.ListSessions()
			if err != nil {
				fmt.Println("列出会话失败:", err)
				return true
			}
			fmt.Println("会话列表:")
			for _, s := range sessions {
				mark := ""
				if s.ID == (*sessPtr).ID {
					mark = " (当前)"
				}
				fmt.Printf("  %s %s%s\n", s.ID[:8], s.Name, mark)
			}
			return true
		case "switch", "resume":
			// 切换/恢复会话的流程与 new 类似，但目标是已有会话
			if len(parts) < 3 {
				fmt.Println("请提供会话 ID")
				return true
			}
			id := parts[2]
			sess, err := sessMgr.GetSession(id)
			if err != nil {
				fmt.Println("会话不存在:", err)
				return true
			}
			// 【关键】保存当前会话 → 切换到目标会话 → 恢复其历史
			(*sessPtr).Messages = *history
			sessMgr.UpdateMessages((*sessPtr).ID, (*sessPtr).Messages)
			*sessPtr = sess
			*history = sess.Messages
			fmt.Printf("已切换到会话 %s\n", id)
			return true
		}
	case "/mem":
		// /mem add key:value — key:value 格式的分隔符是第一个冒号
		// 【注意】value 部分可以包含冒号，因此使用 SplitN(..., 2) 而不是 Split
		if len(parts) < 3 || parts[1] != "add" {
			fmt.Println("用法: /mem add <key>:<value>")
			return true
		}
		payload := strings.Join(parts[2:], " ")
		kv := strings.SplitN(payload, ":", 2)
		if len(kv) != 2 {
			fmt.Println("格式错误，应为 key:value")
			return true
		}
		key := strings.TrimSpace(kv[0])
		value := strings.TrimSpace(kv[1])
		// importance 固定为 0.7，未来可扩展为可配置参数
		if err := memStore.Add(userID, project, key, value, 0.7); err != nil {
			fmt.Println("添加记忆失败:", err)
		} else {
			fmt.Printf("已添加记忆: %s = %s\n", key, value)
		}
		return true
	}
	return false
}
