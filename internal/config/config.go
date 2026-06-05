// config 包 — 配置结构体定义和加载。
// 用 viper 读 JSON 文件 + 命令行 flag 覆盖。
package config

// Config — 顶层配置结构。
type Config struct {
	Providers map[string]ProviderConfig `mapstructure:"providers"` // 多个 LLM 厂商配置
	Router    RouterConfig              `mapstructure:"router"`    // 模型路由规则
	MCP       []MCPConfig               `mapstructure:"mcp"`       // 外部 MCP Server 列表
	CLI       *CLIConfig                `mapstructure:"cli"`       // CLI 可选参数
}

// ProviderConfig — 单个 LLM 厂商的配置。
type ProviderConfig struct {
	APIKey  string `mapstructure:"api_key"`
	BaseURL string `mapstructure:"base_url"` // 空就用该厂商默认地址
	Model   string `mapstructure:"model"`
}

// RouterConfig — 路由配置：一个默认模型 + 多条正则匹配规则。
type RouterConfig struct {
	Default string       `mapstructure:"default"`
	Rules   []RouterRule `mapstructure:"rules"`
}

// RouterRule — 一条路由规则，正则匹配 → 指定模型。
type RouterRule struct {
	Pattern string `mapstructure:"pattern"`
	Model   string `mapstructure:"model"`
}

// CLIConfig — 命令行相关配置，--model 会覆盖路由选择。
type CLIConfig struct {
	ForceModel string `mapstructure:"model"`
}

// MCPConfig — 一个外部 MCP Server 的启动参数。
// Command 是启动命令，Args 是参数列表，通过 stdio 通信。
type MCPConfig struct {
	Name    string   `mapstructure:"name"`
	Command string   `mapstructure:"command"`
	Args    []string `mapstructure:"args"`
}
