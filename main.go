package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/joho/godotenv"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/sashabaranov/go-openai"
)

type MCPConfig struct {
	MCPServers map[string]MCPServer `json:"mcpServers"`
}

type MCPServer struct {
	Type    string   `json:"type" validate:"required"`
	Command string   `json:"command" validate:"required"`
	Args    []string `json:"args,omitempty"`
}

type ChatClient struct {
	mcpClients   []*client.Client
	openaiClient *openai.Client
	model        string
	messages     []openai.ChatCompletionMessage // 用于存储历史消息，实现多轮对话
}

// 创建客户端实例，连接 MCP 服务端
func LoadMCPClients(configPath string, ctx context.Context) ([]*client.Client, []error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, []error{err}
	}

	var mcpConfig MCPConfig
	err = json.Unmarshal(data, &mcpConfig)
	if err != nil {
		return nil, []error{err}
	}

	if err := validator.New().Struct(mcpConfig); err != nil {
		return nil, []error{err}
	}

	var mcpClients []*client.Client
	var errors []error

	for name, mcpServer := range mcpConfig.MCPServers {
		var mcpClient *client.Client
		var err error

		switch strings.ToLower(mcpServer.Type) {
		case "stdio":
			mcpClient, err = client.NewStdioMCPClient(mcpServer.Command, mcpServer.Args)
		case "http":
			mcpClient, err = client.NewStreamableHttpClient(mcpServer.Command)
		case "sse":
			mcpClient, err = client.NewSSEMCPClient(mcpServer.Command)
		default:
			err = fmt.Errorf("未知服务类型: %s (%s)", name, mcpServer.Type)
		}

		if err != nil {
			errors = append(errors, fmt.Errorf("[%s] 创建客户端失败: %v", name, err))
			continue
		}

		// 初始化 MCP 客户端
		fmt.Println("Initializing client...")
		initRequest := mcp.InitializeRequest{}
		initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
		initRequest.Params.ClientInfo = mcp.Implementation{
			Name:    name, // 使用配置中的名称作为客户端名
			Version: "1.0.0",
		}
		initResult, err := mcpClient.Initialize(ctx, initRequest)
		if err != nil {
			errors = append(errors, fmt.Errorf("[%s] 初始化失败: %v", name, err))
			continue
		}

		fmt.Printf("[%s] Connected to server: %s %s\n", name, initResult.ServerInfo.Name, initResult.ServerInfo.Version)

		mcpClients = append(mcpClients, mcpClient)
	}

	return mcpClients, errors
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	mcpClients, errs := LoadMCPClients("config.json", ctx)
	if len(errs) > 0 {
		for _, err := range errs {
			log.Println(err)
		}
	}
	defer func() {
		for _, mcpClient := range mcpClients {
			mcpClient.Close()
		}
	}()

	_ = godotenv.Load()

	apiKey := os.Getenv("OPENAI_API_KEY")
	baseURL := os.Getenv("OPENAI_API_BASE")
	model := os.Getenv("OPENAI_API_MODEL")
	if apiKey == "" || baseURL == "" || model == "" {
		fmt.Println("检查环境变量设置")
		return
	}

	config := openai.DefaultConfig(apiKey)
	config.BaseURL = baseURL
	openaiClient := openai.NewClientWithConfig(config)

	cc := &ChatClient{
		mcpClients:   mcpClients,
		openaiClient: openaiClient,
		model:        model,
		messages:     make([]openai.ChatCompletionMessage, 0),
	}
	cc.ChatLoop()
}

func (cc *ChatClient) ChatLoop() {
	fmt.Print("Type your queries or 'quit' to exit.")
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\nUser: ")
		if !scanner.Scan() {
			break
		}
		userInput := strings.TrimSpace(scanner.Text())
		if strings.ToLower(userInput) == "quit" {
			break
		}
		if userInput == "" {
			continue
		}

		response, err := cc.ProcessQuery(userInput)
		if err != nil {
			fmt.Printf("请求失败: %v\n", err)
			continue
		}

		fmt.Printf("Assistant: %s\n", response)
	}
}

func (cc *ChatClient) ProcessQuery(userInput string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 维护toolName到mcpClient的映射
	toolNameMap := make(map[string]*client.Client)

	// 列出所有可用工具
	availableTools := []openai.Tool{}

	for _, mcpClient := range cc.mcpClients {
		toolsResp, err := mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
		if err != nil {
			log.Printf("Failed to list tools: %v", err)
		}
		for _, tool := range toolsResp.Tools {
			// fmt.Println("name:", tool.Name)
			// fmt.Println("description:", tool.Description)
			// fmt.Println("parameters:", tool.InputSchema)
			availableTools = append(availableTools, openai.Tool{
				Type: openai.ToolTypeFunction,
				Function: &openai.FunctionDefinition{
					Name:        tool.Name,
					Description: tool.Description,
					Parameters:  tool.InputSchema,
				},
			})

			toolNameMap[tool.Name] = mcpClient
		}
	}

	// 存储助理回复的消息
	finalText := []string{}

	// 首轮交互
	cc.messages = append(cc.messages, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: userInput,
	})

	// 遍历每个mcpClient读取其对应的mcpServer上的工具告诉大模型
	resp, err := cc.openaiClient.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:    cc.model,
		Messages: cc.messages,
		Tools:    availableTools,
	})
	if err != nil {
		return "", err
	}
	// fmt.Println(resp)

	// OpenAI的API设计上支持一次请求返回多个候选回答（choices）默认为1
	for _, choice := range resp.Choices {

		// message.Content和message.ToolCalls二选一的关系
		// 如果用户输入涉及需要调用工具，模型一般会返回 ToolCalls
		// 否则直接返回 Content 作为文本回答
		message := choice.Message

		if message.Content != "" { // 若直接生成文本
			finalText = append(finalText, message.Content)

		} else if len(message.ToolCalls) > 0 { // 若调用工具
			// 这个代码len(message.ToolCalls)永远为1
			// 但如果一个MCP Server里注册了两个工具get_temperature和get_humidity
			// 我问大模型: “我想调用xxx工具看一下今天的温度和湿度分别是多少?”message.ToolCalls就变2了
			// 如果多个mcp server 一个注册get_temperature, 一个注册get_humidity
			// 就要把ChatClient的mcpClient改成数组了 通过for循环每个mcpClient来列出所有可用工具给大模型
			toolCallMessages := []openai.ChatCompletionMessage{}

			for _, toolCall := range message.ToolCalls {
				toolName := toolCall.Function.Name
				toolArgsRaw := toolCall.Function.Arguments
				// fmt.Println("=====toolCall.Function.Arguments:", toolArgsRaw)
				var toolArgs map[string]any
				_ = json.Unmarshal([]byte(toolArgsRaw), &toolArgs)

				// 调用工具
				req := mcp.CallToolRequest{}
				req.Params.Name = toolName
				req.Params.Arguments = toolArgs
				//resp, err := cc.mcpClient.CallTool(ctx, req)
				mcpClient := toolNameMap[toolName]
				resp, err := mcpClient.CallTool(ctx, req)
				if err != nil {
					log.Printf("工具调用失败: %v", err)
					continue
				}

				// 构造 tool message
				// 把工具返回的答案记录下来，作为后续模型推理的输入
				toolCallMessages = append(toolCallMessages, openai.ChatCompletionMessage{
					Role:       openai.ChatMessageRoleTool, // 说明是工具的响应
					ToolCallID: toolCall.ID,                // 绑定之前模型说要调用的那个 tool_call.id
					Content:    fmt.Sprintf("%s", resp.Content),
				})
			}

			// 下面这个顺序模拟了人机对话流程
			// 助理说：“我已经调用了这些工具（toolCalls）”
			// 然后工具返回了结果（toolCallMessages）

			// 添加 assistant tool call 信息
			cc.messages = append(cc.messages, openai.ChatCompletionMessage{
				Role:      openai.ChatMessageRoleAssistant,
				Content:   "",
				ToolCalls: message.ToolCalls,
			})

			// 添加 tool 响应
			cc.messages = append(cc.messages, toolCallMessages...)

			// debug
			// b, _ := json.MarshalIndent(cc.messages, "", "  ")
			// fmt.Println("Sending messages to OpenAI:\n", string(b))

			// 再次发送给模型
			// 把助理声明调用了哪些工具（toolCalls）和这些工具的返回结果（toolCallMessages）一起发送给模型，
			// 让模型基于工具的响应继续生成下一步的回复
			nextResponse, err := cc.openaiClient.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
				Model:    cc.model,
				Messages: cc.messages,
			})
			if err != nil {
				return "", err
			}

			for _, nextChoice := range nextResponse.Choices {
				if nextChoice.Message.Content != "" {
					finalText = append(finalText, nextChoice.Message.Content)
				}
			}
		}
	}

	// 把助理的所有回答合并成一个字符串，方便下一次调用时使用完整的对话上下文
	response := strings.Join(finalText, "\n")
	cc.messages = append(cc.messages, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleAssistant,
		Content: response,
	})
	return response, nil
}
