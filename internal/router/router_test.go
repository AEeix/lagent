package router_test

import (
	"testing"

	"lagent/internal/config"
	"lagent/internal/router"
)

func TestRoute(t *testing.T) {
	cfg := config.RouterConfig{
		Default: "deepseek",
		Rules: []config.RouterRule{
			{Pattern: "代码", Model: "openai"},
		},
	}
	r, _ := router.New(cfg)
	if r.Route("写代码") != "openai" {
		t.Error("expected openai")
	}
	if r.Route("你好") != "deepseek" {
		t.Error("expected deepseek")
	}
}
