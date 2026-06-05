package config

import (
	"strings"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// Load — 加载 JSON 配置文件，同时支持命令行 --model 覆盖。
// 还会读 LAGENT_* 环境变量（用 _ 代替 .），优先级：命令行 > 环境变量 > 配置文件。
func Load(cfgFile string, args []string) (*Config, error) {
	v := viper.New()

	v.SetConfigFile(cfgFile)
	v.SetConfigType("json") // 明确指定，不然 viper 会根据扩展名猜
	if err := v.ReadInConfig(); err != nil {
		return nil, err
	}

	// 环境变量：LAGENT_PROVIDERS_OPENAI_API_KEY 这种格式
	v.SetEnvPrefix("LAGENT")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// 命令行 flag，--model 会覆盖路由选择
	flags := pflag.NewFlagSet("lagent", pflag.ContinueOnError)
	flags.String("model", "", "强制指定模型")
	flags.Parse(args)
	v.BindPFlag("cli.model", flags.Lookup("model"))

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	// viper 的 Unmarshal 不会自动把 flag 值写进嵌套 struct，手动处理
	if flags.Lookup("model").Changed {
		cfg.CLI = &CLIConfig{
			ForceModel: v.GetString("cli.model"),
		}
	}

	return &cfg, nil
}
