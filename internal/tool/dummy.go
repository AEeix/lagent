package tool

import "context"

// DummyTool 是一个用于测试的假工具
type DummyTool struct {
	name        string
	description string
}

// NewDummyTool 创建一个假工具实例
func NewDummyTool(name, description string) *DummyTool {
	return &DummyTool{name: name, description: description}
}

func (d *DummyTool) Name() string        { return d.name }
func (d *DummyTool) Description() string { return d.description }
func (d *DummyTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{}
}
func (d *DummyTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	return "dummy", nil
}
