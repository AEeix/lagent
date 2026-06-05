# LAgent

基于 Go 语言开发的个人 AI Agent 助手，可在终端中与 LLM 对话，支持工具调用、记忆系统和会话管理。

## 项目结构

```
cmd/lagent/main.go          # 入口，初始化 + CLI 交互循环
internal/
  adapter/                  # LLM 厂商抽象层（工厂注册，当前支持 OpenAI、DeepSeek）
    provider.go             #   LLMProvider 接口定义 + 工厂注册表
    openai.go               #   OpenAI 实现（含流式 SSE 解析）
    deepseek.go             #   DeepSeek 实现（兼容 OpenAI 协议，仅改 BaseURL）
  agent/loop.go             # Agent 核心：Rerank → 构建 Prompt → 工具调用循环
  router/router.go          # 基于正则的模型路由（如：代码问题→OpenAI，翻译→DeepSeek）
  tool/                     # 工具系统
    registry.go             #   工具注册表（Agent 通过它发现可用工具）
    calculator.go           #   计算器（中缀→后缀→求值，支持 +-*/ 和括号）
    websearch.go            #   Web 搜索（当前为假实现，返回模板文本）
    dummy.go                #   占位测试工具
  mcp/                      # MCP 协议支持（Model Context Protocol）
    client.go               #   MCP 客户端（JSON-RPC over stdio）
    rerank.go               #   TF-IDF 工具筛选（>5 个工具时挑 Top-3 注入 prompt）
  memory/store.go           # 长期记忆（SQLite，双向子串匹配）
  session/manager.go        # 会话管理（SQLite + JSON 序列化，支持切换/恢复）
  config/                   # 配置管理
    config.go               #   配置结构体定义
    loader.go               #   Viper 加载（支持配置文件 + 环境变量 + 命令行覆盖）
```

## 快速开始

### 环境要求

- Go 1.21+
- SQLite 由 `modernc.org/sqlite` 纯 Go 实现，无需额外安装

### 安装与运行

```bash
# 编译
go build -o lagent.exe ./cmd/lagent

# 运行（指定配置文件）
./lagent.exe config/lagent.json

# 双击 lagent.exe 也可以直接启动
```

### 配置

编辑 `config/lagent.json`：

```json
{
  "providers": {
    "openai": {
      "api_key": "sk-xxx",
      "base_url": "https://api.openai.com/v1",
      "model": "gpt-4o-mini"
    },
    "deepseek": {
      "api_key": "sk-xxx",
      "base_url": "https://api.deepseek.com/v1",
      "model": "deepseek-chat"
    }
  },
  "router": {
    "default": "deepseek",
    "rules": [
      { "pattern": "代码|编程|debug", "model": "openai" },
      { "pattern": "翻译", "model": "deepseek" }
    ]
  },
  "mcp": []
}
```

也支持环境变量覆盖，格式为 `LAGENT_` + 路径（用 `_` 代替 `.`）：
```bash
export LAGENT_PROVIDERS_OPENAI_API_KEY=sk-xxx
```

`--model` 命令行参数可强制指定模型，跳过路由：
```bash
./lagent.exe config/lagent.json --model=openai
```

### 交互命令

| 命令 | 说明 |
|------|------|
| `/session new [名称]` | 创建新会话 |
| `/session list` | 列出所有会话 |
| `/session switch <id>` | 切换到指定会话 |
| `/mem add <key>:<value>` | 添加长期记忆 |
| `/bye` | 退出 |

直接输入文本即可对话。AI 会自动调用工具并在需要时搜索长期记忆。

## 工作流程

1. 用户输入 → 路由器根据正则匹配选择模型
2. 从长期记忆中检索相关条目（Top-3），拼入 system prompt
3. Agent 将系统提示词 + 工具列表 + 对话历史发给 LLM
4. LLM 返回文本回复，或通过 `<tool_call>` 标签请求调用工具
5. 工具执行结果返回给 LLM 继续推理，直到 LLM 不再请求工具
6. 流式输出到终端，对话历史持久化到 SQLite

## 设计特点

- **统一 LLM 接口**：所有厂商实现同一个 `LLMProvider` 接口，加新厂商只需写一个文件并在 `init()` 里注册
- **流式输出**：支持 SSE 流式响应，终端实时打字效果
- **模型路由**：基于正则匹配用户意图，自动分发到不同模型
- **MCP 可扩展**：通过 MCP 协议可接入外部工具，不修改主程序代码
- **分层记忆**：短期记忆（对话历史）+ 长期记忆（SQLite 按 key-value 存储，双向匹配检索）

## 已知限制

- 计算器不支持负数表达式
- Web 搜索为假实现，需接入真实 API
- 长期记忆用 LIKE 匹配（非 FTS5 全文索引），数据量大时性能会下降
- userID / project 当前硬编码为默认值
- MCP 子进程未优雅关闭

## 运行测试

```bash
go test ./internal/...
```

## 依赖

- [viper](https://github.com/spf13/viper) — 配置管理
- [pflag](https://github.com/spf13/pflag) — 命令行解析
- [uuid](https://github.com/google/uuid) — 会话 ID 生成
- [modernc.org/sqlite](https://modernc.org/sqlite/) — 纯 Go SQLite 驱动
