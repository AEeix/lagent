package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"lagent/internal/tool"
)

// ClientConfig — 启动 MCP Server 需要的参数。
// Command 是可执行文件名，Args 是参数，通过 stdio 通信。
type ClientConfig struct {
	Name    string
	Command string
	Args    []string
}

// Client — MCP 客户端，包装了一个子进程。
// 通过 stdin/stdout 走 JSON-RPC 和 MCP Server 对话，每次调用加锁防并发。
type Client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	mu     sync.Mutex
	id     int // JSON-RPC 请求 ID 自增
}

// NewClient — 启动子进程，发 initialize 握手，返回就绪的客户端。
func NewClient(config ClientConfig) (*Client, error) {
	cmd := exec.Command(config.Command, config.Args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &Client{
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
	}
	// MCP 协议要求先握手
	if err := c.initialize(); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// initialize — 发送 MCP initialize 请求，完成协议握手。
func (c *Client) initialize() error {
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  "initialize",
		Params:  map[string]interface{}{},
		ID:      c.nextID(),
	}
	var resp jsonrpcResponse
	if err := c.call(req, &resp); err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("init error: %v", resp.Error)
	}
	return nil
}

// ListTools — 问 MCP Server 它有哪些工具，包装成 tool.Tool 接口返回。
func (c *Client) ListTools() ([]tool.Tool, error) {
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  "tools/list",
		ID:      c.nextID(),
	}
	var resp jsonrpcResponse
	if err := c.call(req, &resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("list tools error: %v", resp.Error)
	}
	var result struct {
		Tools []mcpTool `json:"tools"`
	}
	if err := convertMapToStruct(resp.Result, &result); err != nil {
		return nil, err
	}
	// 每个 MCP 工具包一层 wrapper，调用时走 RPC
	tools := make([]tool.Tool, len(result.Tools))
	for i, mt := range result.Tools {
		tools[i] = &mcpToolWrapper{client: c, tool: mt}
	}
	return tools, nil
}

// CallTool — 远程调用 MCP Server 上的工具，返回 JSON 序列化后的结果。
func (c *Client) CallTool(ctx context.Context, toolName string, args map[string]interface{}) (string, error) {
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name":      toolName,
			"arguments": args,
		},
		ID: c.nextID(),
	}
	var resp jsonrpcResponse
	if err := c.call(req, &resp); err != nil {
		return "", err
	}
	if resp.Error != nil {
		return "", fmt.Errorf("call tool error: %v", resp.Error)
	}
	content, _ := json.Marshal(resp.Result)
	return string(content), nil
}

// call — 发一行 JSON 到 stdin，从 stdout 读一行回来，解析成 response。
// 全程加锁，因为 stdin/stdout 不能并发读写。
func (c *Client) call(req jsonrpcRequest, resp *jsonrpcResponse) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	data, _ := json.Marshal(req)
	_, err := fmt.Fprintf(c.stdin, "%s\n", data)
	if err != nil {
		return err
	}
	reader := bufio.NewReader(c.stdout)
	line, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(line), resp)
}

func (c *Client) nextID() int {
	c.id++
	return c.id
}

// Close — 关掉 stdin 等一小会儿，然后杀了子进程。
// 先关 stdin 是让子进程知道该退出了，给 100ms 缓冲。
func (c *Client) Close() error {
	if c.stdin != nil {
		c.stdin.Close()
	}
	time.Sleep(100 * time.Millisecond)
	return c.cmd.Process.Kill()
}

// ---- MCP 工具适配层 ----

// mcpToolWrapper — 把 MCP 协议的工具描述包成 tool.Tool 接口。
// Execute 实际走的是 CallTool RPC。
type mcpToolWrapper struct {
	client *Client
	tool   mcpTool
}

func (w *mcpToolWrapper) Name() string        { return w.tool.Name }
func (w *mcpToolWrapper) Description() string { return w.tool.Description }
func (w *mcpToolWrapper) InputSchema() map[string]interface{} {
	return w.tool.InputSchema
}
func (w *mcpToolWrapper) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	return w.client.CallTool(ctx, w.tool.Name, args)
}

// ---- JSON-RPC 2.0 协议结构 ----

type jsonrpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
	ID      int         `json:"id"`
}

type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
	ID      int             `json:"id"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// mcpTool — MCP 协议返回的工具描述，对应 tools/list 的返回格式。
type mcpTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// convertMapToStruct — json.RawMessage → 目标结构体，顺手小封装。
func convertMapToStruct(data json.RawMessage, target interface{}) error {
	return json.Unmarshal(data, target)
}
