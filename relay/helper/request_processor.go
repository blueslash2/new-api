package helper

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

// ========== 递归处理properties字段的辅助函数 ==========
// ProcessProperties 递归处理工具参数中的properties字段，为缺失type的参数补全string类型
func ProcessProperties(schema map[string]interface{}, toolName string) {
	if properties, hasProps := schema["properties"].(map[string]interface{}); hasProps && properties != nil {
		for propName, propValue := range properties {
			if propMap, isMap := propValue.(map[string]interface{}); isMap {
				// 如果参数定义中没有type字段，默认设置为string
				if _, hasType := propMap["type"]; !hasType {
					propMap["type"] = "string"
					common.SysLog(fmt.Sprintf("工具 %s 的参数 %s 缺少type字段，已默认设置为string类型", toolName, propName))
				}

				// 递归处理嵌套的properties（处理对象类型的嵌套）
				if propMap["type"] == "object" {
					ProcessProperties(propMap, toolName)
				}
			}
		}
	}
}

// ========== Moonshot kimi-k2.5 模型 temperature 处理 ==========
// ProcessMoonshotTemperature 如果渠道类型是 Moonshot 且模型名称包含 "kimi-k2.5"，将非1.0的 temperature 改为1.0
func ProcessMoonshotTemperature(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) {
	if info.ChannelType == constant.ChannelTypeMoonshot {
		if strings.Contains(request.Model, "kimi-k2.5") {
			if request.Temperature != nil {
				tempValue := *request.Temperature
				// 检查 temperature 是否不是1也不是1.0
				if tempValue != 1.0 {
					newTemp := 1.0
					request.Temperature = &newTemp
					common.SysLog(fmt.Sprintf("【Moonshot温度调整】模型 %s 的 temperature 从 %v 调整为 1.0",
						request.Model, tempValue))
				}
			}
		}
	}
}

// ========== 处理历史消息中的 tool_calls 格式 ==========
// ProcessToolCallsInMessages 遍历所有消息，处理 assistant 角色的消息，拆分 <tool_calls> 格式为标准 tool_calls 格式
func ProcessToolCallsInMessages(request *dto.GeneralOpenAIRequest) {
	for i := 0; i < len(request.Messages); i++ {
		msg := &request.Messages[i]
		if msg.Role == "assistant" && msg.Content != nil {
			content := msg.StringContent()
			// 检查是否包含 <tool_calls 格式
			if strings.Contains(content, "<tool_calls") {
				// 正则匹配所有 <tool_calls name="xxx" arguments="xxx" result="xxx"/>
				// arguments 是可选的
				re := regexp.MustCompile(`<tool_calls\s+name="([^"]*)"(?:\s+arguments="([^"]*)")?\s+result="([^"]*)"/>`)
				matches := re.FindAllStringSubmatchIndex(content, -1)

				if len(matches) > 0 {
					// 需要拆分消息
					newMessages := make([]dto.Message, 0)
					lastEnd := 0

					for idx, match := range matches {
						// match[0], match[1] 是整个匹配的索引
						// match[2], match[3] 是 name 的索引
						// match[4], match[5] 是 arguments 的索引（可能为 -1 表示不存在）
						// match[6], match[7] 是 result 的索引

						// 提取工具名称
						toolName := content[match[2]:match[3]]

						// 提取 arguments（如果存在）
						var queryValue string
						var ragContent string
						if match[4] != -1 {
							// 存在 arguments
							queryValue = content[match[4]:match[5]]
							ragContent = content[match[6]:match[7]]
						} else {
							// 不存在 arguments，使用"无参数"
							queryValue = "无参数"
							ragContent = content[match[6]:match[7]]
						}

						// 1. 添加 tool_calls 之前的 assistant 消息内容
						preContent := ""
						if match[0] > lastEnd {
							preContent = content[lastEnd:match[0]]
						}
						// 确保不为空，添加换行符
						if preContent == "" {
							preContent = "\n"
						}

						// 构建 arguments JSON，安全地转义 queryValue
						argsMap := map[string]string{"query": queryValue}
						argsJSON, _ := json.Marshal(argsMap)

						// 创建 tool_calls 结构
						toolCallObj := map[string]interface{}{
							"index": idx,
							"id":    fmt.Sprintf("%s:%d", toolName, idx),
							"type":  "function",
							"function": map[string]interface{}{
								"name":      toolName,
								"arguments": string(argsJSON),
							},
						}
						toolCallsArr := []interface{}{toolCallObj}
						toolCallsJSON, _ := json.Marshal(toolCallsArr)

						newMessages = append(newMessages, dto.Message{
							Role:      "assistant",
							Content:   preContent,
							ToolCalls: toolCallsJSON,
						})

						// 2. 添加 tool 类型的消息
						toolCallId := fmt.Sprintf("%s:%d", toolName, idx)
						newMessages = append(newMessages, dto.Message{
							Role:       "tool",
							ToolCallId: toolCallId,
							Content:    ragContent,
						})

						lastEnd = match[1]
					}

					// 3. 添加最后一个 tool_calls 之后的 assistant 消息内容
					postContent := ""
					if lastEnd < len(content) {
						postContent = content[lastEnd:]
					}
					// 确保不为空，添加换行符
					if postContent == "" {
						postContent = "\n"
					}
					newMessages = append(newMessages, dto.Message{
						Role:    "assistant",
						Content: postContent,
					})

					// 替换原来的消息
					// 删除原来的 assistant 消息，插入新消息
					before := request.Messages[:i]
					after := request.Messages[i+1:]
					request.Messages = append(before, append(newMessages, after...)...)

					// 调整索引，继续处理新插入的消息之后的位置
					i += len(newMessages) - 1
				}
			}
		}
	}
}

// ========== 工具参数类型补全逻辑 ==========
// ProcessToolParameters 如果工具存在且参数缺少type字段，默认设置为string类型
func ProcessToolParameters(request *dto.GeneralOpenAIRequest) {
	if len(request.Tools) > 0 {
		for i := range request.Tools {
			tool := &request.Tools[i]
			if tool.Function.Parameters != nil {
				// 尝试将parameters转换为map[string]interface{}
				if params, ok := tool.Function.Parameters.(map[string]interface{}); ok {
					// 检查properties字段
					if properties, hasProps := params["properties"].(map[string]interface{}); hasProps && properties != nil {
						for propName, propValue := range properties {
							if propMap, isMap := propValue.(map[string]interface{}); isMap {
								// 如果参数定义中没有type字段，默认设置为string
								if _, hasType := propMap["type"]; !hasType {
									propMap["type"] = "string"
									common.SysLog(fmt.Sprintf("工具 %s 的参数 %s 缺少type字段，已默认设置为string类型", tool.Function.Name, propName))
								}
							}
						}
					}
				}
			}
		}
	}
}

// ========== 根据模型名称注入 extra_body 逻辑 ==========
// ProcessExtraBodyInjection 规则：模型名称包含"LNOT"时注入 chat_template_kwargs，包含"RNOT"时注入 thinking
func ProcessExtraBodyInjection(request *dto.GeneralOpenAIRequest) {
	if strings.Contains(request.Model, "LNOT") || strings.Contains(request.Model, "RNOT") {
		extraBodyMap := make(map[string]interface{})

		// 如果请求中已有 extra_body，先解析它
		if len(request.ExtraBody) > 0 {
			_ = json.Unmarshal(request.ExtraBody, &extraBodyMap)
		}

		// 根据模型名称规则注入相应字段
		if strings.Contains(request.Model, "LNOT") || request.Model == "衡云·明理" {
			extraBodyMap["chat_template_kwargs"] = map[string]interface{}{
				"enable_thinking": false,
			}
			common.SysLog(fmt.Sprintf("模型 %s 包含 LNOT，已注入 chat_template_kwargs.enable_thinking=false", request.Model))
		}

		if strings.Contains(request.Model, "RNOT") {
			extraBodyMap["thinking"] = map[string]interface{}{
				"type": "disabled",
			}
			common.SysLog(fmt.Sprintf("模型 %s 包含 RNOT，已注入 thinking.type=disabled", request.Model))
		}

		// 将修改后的 extra_body 序列化回请求
		extraBodyJSON, err := json.Marshal(extraBodyMap)
		if err == nil {
			request.ExtraBody = extraBodyJSON
		}
	}
}

// ========== 打印截断后的请求体日志 ==========
// LogTruncatedRequestBody 打印截断后的请求体（用于调试moonshot工具调用问题）
func LogTruncatedRequestBody(jsonData []byte, channelId int, model string) {
	var reqMap map[string]interface{}
	if err := json.Unmarshal(jsonData, &reqMap); err == nil {
		// 处理messages，截断content字段
		if messages, ok := reqMap["messages"].([]interface{}); ok {
			for i, msg := range messages {
				if msgMap, ok := msg.(map[string]interface{}); ok {
					// 截断content字段
					if content, ok := msgMap["content"].(string); ok && len(content) > 30 {
						msgMap["content"] = content[:20] + "...(" + fmt.Sprintf("%d", len(content)) + " chars)"
					}
					// 截断tool_calls中的function.arguments
					if toolCalls, ok := msgMap["tool_calls"].([]interface{}); ok {
						for j, tc := range toolCalls {
							if tcMap, ok := tc.(map[string]interface{}); ok {
								if fn, ok := tcMap["function"].(map[string]interface{}); ok {
									if args, ok := fn["arguments"].(string); ok && len(args) > 50 {
										fn["arguments"] = args[:30] + "...(" + fmt.Sprintf("%d", len(args)) + " chars)"
									}
								}
							}
							toolCalls[j] = tc
						}
					}
				}
				messages[i] = msg
			}
		}
		// 序列化并打印
		if logData, err := json.Marshal(reqMap); err == nil {
			common.SysError(fmt.Sprintf("【模型诊断器】【发出】渠道=%d, 模型=%s, 请求体=%s",
				channelId, model, string(logData)))
		}
	}
}

// ProcessAllRequestModifications 执行所有的请求修改操作
// 这是统一入口，方便在两个地方调用
func ProcessAllRequestModifications(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) {
	// 1. Moonshot kimi-k2.5 模型 temperature 处理
	ProcessMoonshotTemperature(c, info, request)

	// 2. 处理历史消息中的 tool_calls 格式
	ProcessToolCallsInMessages(request)

	// 3. 工具参数类型补全逻辑
	ProcessToolParameters(request)

	// 4. 根据模型名称注入 extra_body
	ProcessExtraBodyInjection(request)
}
