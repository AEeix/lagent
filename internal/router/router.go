// router 包 — 根据用户输入正则匹配，动态选择用哪个模型。
// 比如：数学问题→deepseek，代码问题→openai。
// 一个都没匹配上就用 default。
package router

import (
	"regexp"

	"lagent/internal/config"
)

type Router struct {
	defaultModel string
	rules        []compiledRule
}

type compiledRule struct {
	re    *regexp.Regexp
	model string
}

func New(cfg config.RouterConfig) (*Router, error) {
	r := &Router{defaultModel: cfg.Default}
	for _, rule := range cfg.Rules {
		re, err := regexp.Compile(rule.Pattern)
		if err != nil {
			return nil, err
		}
		r.rules = append(r.rules, compiledRule{re: re, model: rule.Model})
	}
	return r, nil
}

// Route — 遍历规则，第一个匹配到的就返回对应模型名。都不匹配返回默认。
func (r *Router) Route(input string) string {
	for _, rule := range r.rules {
		if rule.re.MatchString(input) {
			return rule.model
		}
	}
	return r.defaultModel
}
