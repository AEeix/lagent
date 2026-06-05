package tool_test

import (
	"context"
	"testing"

	"lagent/internal/tool"
)

func TestCalculator(t *testing.T) {
	calc := &tool.CalculatorTool{}
	res, err := calc.Execute(context.Background(), map[string]interface{}{"expression": "3+5*2"})
	if err != nil {
		t.Fatal(err)
	}
	if res != "13" { // 注意运算符优先级：5*2=10，3+10=13
		t.Errorf("expected 13, got %s", res)
	}
}
