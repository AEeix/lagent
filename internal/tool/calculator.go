package tool

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// CalculatorTool — 算术表达式求值工具，支持 + - * / 和括号。
// 实现用的是经典的中缀→后缀→求值三件套，没依赖任何第三方库。
type CalculatorTool struct{}

func (c *CalculatorTool) Name() string { return "calculator" }
func (c *CalculatorTool) Description() string {
	return "执行基本数学运算，例如 3+5*2。支持的运算符：+、-、*、/。"
}
func (c *CalculatorTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"expression": map[string]interface{}{
				"type":        "string",
				"description": "数学表达式",
			},
		},
		"required": []string{"expression"},
	}
}
func (c *CalculatorTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	expr, ok := args["expression"].(string)
	if !ok {
		return "", fmt.Errorf("缺少 expression 参数")
	}
	res, err := evaluate(expr)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%g", res), nil
}

// evaluate — 表达式求值入口：词法分析 → 中缀转后缀 → 后缀求值。
// 这只是一个玩具实现，不处理负数、函数等高级语法。
func evaluate(expr string) (float64, error) {
	tokens := tokenizeExpr(expr)
	postfix, err := infixToPostfix(tokens)
	if err != nil {
		return 0, err
	}
	return evalPostfix(postfix)
}

// tokenizeExpr — 把表达式字符串切成一堆 token。
// 数字连在一起算一个 token，运算符和括号各自一个。
// 注意：不支持负数，所以 "-3+5" 这种会崩。
func tokenizeExpr(expr string) []string {
	expr = strings.ReplaceAll(expr, " ", "") // 先去空格
	var tokens []string
	var cur string
	for _, ch := range expr {
		if ch == '+' || ch == '-' || ch == '*' || ch == '/' || ch == '(' || ch == ')' {
			if cur != "" {
				tokens = append(tokens, cur)
				cur = ""
			}
			tokens = append(tokens, string(ch))
		} else {
			cur += string(ch)
		}
	}
	if cur != "" {
		tokens = append(tokens, cur)
	}
	return tokens
}

// infixToPostfix — 中缀表达式转后缀（逆波兰），用调度场算法。
// 核心逻辑：数字直接输出；运算符按优先级压栈/弹栈；括号特殊处理。
func infixToPostfix(tokens []string) ([]string, error) {
	precedence := map[string]int{"+": 1, "-": 1, "*": 2, "/": 2}
	var output []string
	var ops []string // 运算符栈
	for _, t := range tokens {
		if isNumber(t) {
			output = append(output, t)
		} else if t == "(" {
			ops = append(ops, t)
		} else if t == ")" {
			// 弹栈直到遇到左括号
			for len(ops) > 0 && ops[len(ops)-1] != "(" {
				output = append(output, ops[len(ops)-1])
				ops = ops[:len(ops)-1]
			}
			if len(ops) == 0 {
				return nil, fmt.Errorf("括号不匹配")
			}
			ops = ops[:len(ops)-1] // 弹出 '('
		} else { // 运算符
			// 栈顶优先级 >= 当前就弹出来，保证运算顺序
			for len(ops) > 0 && ops[len(ops)-1] != "(" && precedence[ops[len(ops)-1]] >= precedence[t] {
				output = append(output, ops[len(ops)-1])
				ops = ops[:len(ops)-1]
			}
			ops = append(ops, t)
		}
	}
	// 把剩余的运算符全弹出
	for len(ops) > 0 {
		if ops[len(ops)-1] == "(" {
			return nil, fmt.Errorf("括号不匹配")
		}
		output = append(output, ops[len(ops)-1])
		ops = ops[:len(ops)-1]
	}
	return output, nil
}

// evalPostfix — 后缀表达式求值，用一个栈就够了。
// 遇到数字就压栈，遇到运算符就弹两个出来算完再压回去。
func evalPostfix(postfix []string) (float64, error) {
	var stack []float64
	for _, t := range postfix {
		if isNumber(t) {
			v, _ := strconv.ParseFloat(t, 64)
			stack = append(stack, v)
		} else {
			if len(stack) < 2 {
				return 0, fmt.Errorf("表达式不合法")
			}
			b := stack[len(stack)-1]
			a := stack[len(stack)-2]
			stack = stack[:len(stack)-2]
			switch t {
			case "+":
				stack = append(stack, a+b)
			case "-":
				stack = append(stack, a-b)
			case "*":
				stack = append(stack, a*b)
			case "/":
				if b == 0 {
					return 0, fmt.Errorf("除数为零")
				}
				stack = append(stack, a/b)
			}
		}
	}
	if len(stack) != 1 {
		return 0, fmt.Errorf("表达式不合法")
	}
	return stack[0], nil
}

// isNumber — 判断一个字符串能不能转成数字。
func isNumber(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}
