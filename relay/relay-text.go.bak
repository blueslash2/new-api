package relay

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"one-api/common"
	"one-api/constant"
	"one-api/dto"
	"one-api/model"
	relaycommon "one-api/relay/common"
	relayconstant "one-api/relay/constant"
	"one-api/relay/helper"
	"one-api/service"
	"one-api/setting"
	"one-api/setting/model_setting"
	"one-api/setting/operation_setting"
	"one-api/types"
	"regexp"
	"strings"
	"time"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/shopspring/decimal"

	"github.com/gin-gonic/gin"
)

// 递归处理properties字段的辅助函数
func processProperties(schema map[string]interface{}, toolName string) {
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
					processProperties(propMap, toolName)
				}
			}
		}
	}
}

func getAndValidateTextRequest(c *gin.Context, relayInfo *relaycommon.RelayInfo) (*dto.GeneralOpenAIRequest, error) {
	textRequest := &dto.GeneralOpenAIRequest{}
	err := common.UnmarshalBodyReusable(c, textRequest)
	if err != nil {
		return nil, err
	}
	if relayInfo.RelayMode == relayconstant.RelayModeModerations && textRequest.Model == "" {
		textRequest.Model = "text-moderation-latest"
	}
	if relayInfo.RelayMode == relayconstant.RelayModeEmbeddings && textRequest.Model == "" {
		textRequest.Model = c.Param("model")
	}

	if textRequest.MaxTokens > math.MaxInt32/2 {
		return nil, errors.New("max_tokens is invalid")
	}
	if textRequest.Model == "" {
		return nil, errors.New("model is required")
	}
	if textRequest.WebSearchOptions != nil {
		if textRequest.WebSearchOptions.SearchContextSize != "" {
			validSizes := map[string]bool{
				"high":   true,
				"medium": true,
				"low":    true,
			}
			if !validSizes[textRequest.WebSearchOptions.SearchContextSize] {
				return nil, errors.New("invalid search_context_size, must be one of: high, medium, low")
			}
		} else {
			textRequest.WebSearchOptions.SearchContextSize = "medium"
		}
	}
	switch relayInfo.RelayMode {
	case relayconstant.RelayModeCompletions:
		if textRequest.Prompt == "" {
			return nil, errors.New("field prompt is required")
		}
	case relayconstant.RelayModeChatCompletions:
		if len(textRequest.Messages) == 0 {
			return nil, errors.New("field messages is required")
		}
	case relayconstant.RelayModeEmbeddings:
	case relayconstant.RelayModeModerations:
		if textRequest.Input == nil || textRequest.Input == "" {
			return nil, errors.New("field input is required")
		}
	case relayconstant.RelayModeEdits:
		if textRequest.Instruction == "" {
			return nil, errors.New("field instruction is required")
		}
	}
	relayInfo.IsStream = textRequest.Stream
	return textRequest, nil
}

func TextHelper(c *gin.Context) (newAPIError *types.NewAPIError) {

	relayInfo := relaycommon.GenRelayInfo(c)

	// get & validate textRequest 获取并验证文本请求
	textRequest, err := getAndValidateTextRequest(c, relayInfo)

	if err != nil {
		return types.NewError(err, types.ErrorCodeInvalidRequest)
	}

	if textRequest.WebSearchOptions != nil {
		c.Set("chat_completion_web_search_context_size", textRequest.WebSearchOptions.SearchContextSize)
	}

	if setting.ShouldCheckPromptSensitive() {
		words, err := checkRequestSensitive(textRequest, relayInfo)
		if err != nil {
			common.LogWarn(c, fmt.Sprintf("user sensitive words detected: %s", strings.Join(words, ", ")))
			return types.NewError(err, types.ErrorCodeSensitiveWordsDetected)
		}
	}

	common.SysLog(fmt.Sprintf("【模型诊断器】【转换前】原始模型: %s", relayInfo.OriginModelName))
	err = helper.ModelMappedHelper(c, relayInfo, textRequest)
	if err != nil {
		return types.NewError(err, types.ErrorCodeChannelModelMappedError)
	}

	// ========== Moonshot kimi-k2.5 模型 temperature 处理 ==========
	// 如果渠道类型是 Moonshot 且模型名称包含 "kimi-k2.5"，将非1.0的 temperature 改为1.0
	if relayInfo.ChannelType == constant.ChannelTypeMoonshot {
		if strings.Contains(textRequest.Model, "kimi-k2.5") {
			if textRequest.Temperature != nil {
				tempValue := *textRequest.Temperature
				// 检查 temperature 是否不是1也不是1.0
				if tempValue != 1.0 {
					newTemp := 1.0
					textRequest.Temperature = &newTemp
					common.SysLog(fmt.Sprintf("【Moonshot温度调整】模型 %s 的 temperature 从 %v 调整为 1.0",
						textRequest.Model, tempValue))
				}
			}
		}
	}
	// ========== Moonshot kimi-k2.5 模型 temperature 处理结束 ==========

	// 获取 promptTokens，如果上下文中已经存在，则直接使用
	var promptTokens int
	if value, exists := c.Get("prompt_tokens"); exists {
		promptTokens = value.(int)
		relayInfo.PromptTokens = promptTokens
	} else {
		promptTokens, err = getPromptTokens(textRequest, relayInfo)
		// count messages token error 计算promptTokens错误
		if err != nil {
			return types.NewError(err, types.ErrorCodeCountTokenFailed)
		}
		c.Set("prompt_tokens", promptTokens)
	}

	priceData, err := helper.ModelPriceHelper(c, relayInfo, promptTokens, int(math.Max(float64(textRequest.MaxTokens), float64(textRequest.MaxCompletionTokens))))
	if err != nil {
		return types.NewError(err, types.ErrorCodeModelPriceError)
	}

	// pre-consume quota 预消耗配额
	preConsumedQuota, userQuota, newApiErr := preConsumeQuota(c, priceData.ShouldPreConsumedQuota, relayInfo)
	if newApiErr != nil {
		return newApiErr
	}
	defer func() {
		if newApiErr != nil {
			returnPreConsumedQuota(c, relayInfo, userQuota, preConsumedQuota)
		}
	}()
	includeUsage := false
	// 判断用户是否需要返回使用情况
	if textRequest.StreamOptions != nil && textRequest.StreamOptions.IncludeUsage {
		includeUsage = true
	}

	// 如果不支持StreamOptions，将StreamOptions设置为nil
	if !relayInfo.SupportStreamOptions || !textRequest.Stream {
		textRequest.StreamOptions = nil
	} else {
		// 如果支持StreamOptions，且请求中没有设置StreamOptions，根据配置文件设置StreamOptions
		if constant.ForceStreamOption {
			textRequest.StreamOptions = &dto.StreamOptions{
				IncludeUsage: true,
			}
		}
	}

	if includeUsage {
		relayInfo.ShouldIncludeUsage = true
	}

	adaptor := GetAdaptor(relayInfo.ApiType)
	if adaptor == nil {
		return types.NewError(fmt.Errorf("invalid api type: %d", relayInfo.ApiType), types.ErrorCodeInvalidApiType)
	}
	adaptor.Init(relayInfo)
	var requestBody io.Reader

	if model_setting.GetGlobalSettings().PassThroughRequestEnabled {
		body, err := common.GetRequestBody(c)
		if err != nil {
			return types.NewErrorWithStatusCode(err, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest)
		}
		requestBody = bytes.NewBuffer(body)
	} else {

		// ========== 添加这段代码：处理历史消息中的 tool_calls 格式 ==========
		// 遍历所有消息，处理 assistant 角色的消息，拆分 tool_calls 格式
		for i := 0; i < len(textRequest.Messages); i++ {
			msg := &textRequest.Messages[i]
			if msg.Role == "assistant" && msg.Content != nil {
				content := msg.StringContent()
				// 检查是否包含 <tool_calls 格式
				if strings.Contains(content, "<tool_calls") {
					// 正则匹配所有 <tool_calls name="xxx" result="xxx"/>
					re := regexp.MustCompile(`<tool_calls\s+name="([^"]*)"\s+result="([^"]*)"/>`)
					matches := re.FindAllStringSubmatchIndex(content, -1)

					if len(matches) > 0 {
						// 需要拆分消息
						newMessages := make([]dto.Message, 0)
						lastEnd := 0

						for idx, match := range matches {
							// match[0], match[1] 是整个匹配的索引
							// match[2], match[3] 是 name 的索引
							// match[4], match[5] 是 result 的索引

							// 提取工具名称和 RAG 文本
							toolName := content[match[2]:match[3]]
							ragContent := content[match[4]:match[5]]

							// 1. 添加 tool_calls 之前的 assistant 消息内容
							preContent := ""
							if match[0] > lastEnd {
								preContent = content[lastEnd:match[0]]
							}
							// 确保不为空，添加换行符
							if preContent == "" {
								preContent = "\n"
							}

							// 创建 tool_calls 结构
							toolCallObj := map[string]interface{}{
								"index": idx,
								"id":    fmt.Sprintf("%s:%d", toolName, idx),
								"type":  "function",
								"function": map[string]interface{}{
									"name":      toolName,
									"arguments": `{"query": "因脱敏需要而屏蔽"}`,
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
						before := textRequest.Messages[:i]
						after := textRequest.Messages[i+1:]
						textRequest.Messages = append(before, append(newMessages, after...)...)

						// 调整索引，继续处理新插入的消息之后的位置
						i += len(newMessages) - 1
					}
				}
			}
		}
		// ========== 处理代码结束 ==========

		// ========== 添加工具参数类型补全逻辑 ==========
		// 如果工具存在且参数缺少type字段，默认设置为string类型
		if len(textRequest.Tools) > 0 {
			for i := range textRequest.Tools {
				tool := &textRequest.Tools[i]
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
		// ========== 工具参数类型补全逻辑结束 ==========

		// ========== 根据模型名称注入 extra_body 逻辑 ==========
		// 规则：模型名称包含"LNOT"时注入 chat_template_kwargs，包含"RNOT"时注入 thinking
		if strings.Contains(textRequest.Model, "LNOT") || strings.Contains(textRequest.Model, "RNOT") {
			extraBodyMap := make(map[string]interface{})
			
			// 如果请求中已有 extra_body，先解析它
			if len(textRequest.ExtraBody) > 0 {
				_ = json.Unmarshal(textRequest.ExtraBody, &extraBodyMap)
			}
			
			// 根据模型名称规则注入相应字段
				if strings.Contains(textRequest.Model, "LNOT") || textRequest.Model == "衡云·明理" {
					extraBodyMap["chat_template_kwargs"] = map[string]interface{}{
					"enable_thinking": false,
				}
				common.SysLog(fmt.Sprintf("模型 %s 包含 LNOT，已注入 chat_template_kwargs.enable_thinking=false", textRequest.Model))
			}
			
			if strings.Contains(textRequest.Model, "RNOT") {
				extraBodyMap["thinking"] = map[string]interface{}{
					"type": "disabled",
				}
				common.SysLog(fmt.Sprintf("模型 %s 包含 RNOT，已注入 thinking.type=disabled", textRequest.Model))
			}
			
			// 将修改后的 extra_body 序列化回请求
			extraBodyJSON, err := json.Marshal(extraBodyMap)
			if err == nil {
				textRequest.ExtraBody = extraBodyJSON
			}
		}
		// ========== extra_body 注入逻辑结束 ==========

		convertedRequest, err := adaptor.ConvertOpenAIRequest(c, relayInfo, textRequest)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed)
		}
		jsonData, err := json.Marshal(convertedRequest)

		// ========== 添加这段日志代码 ==========
		// 打印截断后的请求体（用于调试moonshot工具调用问题）
		func() {
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
						relayInfo.ChannelId, textRequest.Model, string(logData)))
				}
			}
		}()
		// ========== 日志代码结束 ==========

		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed)
		}

		// apply param override
		if len(relayInfo.ParamOverride) > 0 {
			reqMap := make(map[string]interface{})
			_ = common.Unmarshal(jsonData, &reqMap)
			for key, value := range relayInfo.ParamOverride {
				reqMap[key] = value
			}
			jsonData, err = common.Marshal(reqMap)
			if err != nil {
				return types.NewError(err, types.ErrorCodeChannelParamOverrideInvalid)
			}
		}

		if common.DebugEnabled {
			println("requestBody: ", string(jsonData))
		}
		requestBody = bytes.NewBuffer(jsonData)
	}

	var httpResp *http.Response
	resp, err := adaptor.DoRequest(c, relayInfo, requestBody)

	if err != nil {
		return types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}

	statusCodeMappingStr := c.GetString("status_code_mapping")

	if resp != nil {
		httpResp = resp.(*http.Response)
		relayInfo.IsStream = relayInfo.IsStream || strings.HasPrefix(httpResp.Header.Get("Content-Type"), "text/event-stream")
		if httpResp.StatusCode != http.StatusOK {
			newApiErr = service.RelayErrorHandler(httpResp, false)
			// reset status code 重置状态码
			service.ResetStatusCode(newApiErr, statusCodeMappingStr)
			return newApiErr
		}
	}

	usage, newApiErr := adaptor.DoResponse(c, httpResp, relayInfo)
	if newApiErr != nil {
		// reset status code 重置状态码
		service.ResetStatusCode(newApiErr, statusCodeMappingStr)
		return newApiErr
	}

	if strings.HasPrefix(relayInfo.OriginModelName, "gpt-4o-audio") {
		service.PostAudioConsumeQuota(c, relayInfo, usage.(*dto.Usage), preConsumedQuota, userQuota, priceData, "")
	} else {
		postConsumeQuota(c, relayInfo, usage.(*dto.Usage), preConsumedQuota, userQuota, priceData, "")
	}
	return nil
}

func getPromptTokens(textRequest *dto.GeneralOpenAIRequest, info *relaycommon.RelayInfo) (int, error) {
	var promptTokens int
	var err error
	switch info.RelayMode {
	case relayconstant.RelayModeChatCompletions:
		promptTokens, err = service.CountTokenChatRequest(info, *textRequest)
	case relayconstant.RelayModeCompletions:
		promptTokens = service.CountTokenInput(textRequest.Prompt, textRequest.Model)
	case relayconstant.RelayModeModerations:
		promptTokens = service.CountTokenInput(textRequest.Input, textRequest.Model)
	case relayconstant.RelayModeEmbeddings:
		promptTokens = service.CountTokenInput(textRequest.Input, textRequest.Model)
	default:
		err = errors.New("unknown relay mode")
		promptTokens = 0
	}
	info.PromptTokens = promptTokens
	return promptTokens, err
}

func checkRequestSensitive(textRequest *dto.GeneralOpenAIRequest, info *relaycommon.RelayInfo) ([]string, error) {
	var err error
	var words []string
	switch info.RelayMode {
	case relayconstant.RelayModeChatCompletions:
		words, err = service.CheckSensitiveMessages(textRequest.Messages)
	case relayconstant.RelayModeCompletions:
		words, err = service.CheckSensitiveInput(textRequest.Prompt)
	case relayconstant.RelayModeModerations:
		words, err = service.CheckSensitiveInput(textRequest.Input)
	case relayconstant.RelayModeEmbeddings:
		words, err = service.CheckSensitiveInput(textRequest.Input)
	}
	return words, err
}

// 预扣费并返回用户剩余配额
func preConsumeQuota(c *gin.Context, preConsumedQuota int, relayInfo *relaycommon.RelayInfo) (int, int, *types.NewAPIError) {
	userQuota, err := model.GetUserQuota(relayInfo.UserId, false)
	if err != nil {
		return 0, 0, types.NewError(err, types.ErrorCodeQueryDataError)
	}
	if userQuota <= 0 {
		return 0, 0, types.NewErrorWithStatusCode(errors.New("user quota is not enough"), types.ErrorCodeInsufficientUserQuota, http.StatusForbidden)
	}
	if userQuota-preConsumedQuota < 0 {
		return 0, 0, types.NewErrorWithStatusCode(fmt.Errorf("pre-consume quota failed, user quota: %s, need quota: %s", common.FormatQuota(userQuota), common.FormatQuota(preConsumedQuota)), types.ErrorCodeInsufficientUserQuota, http.StatusForbidden)
	}
	relayInfo.UserQuota = userQuota
	if userQuota > 100*preConsumedQuota {
		// 用户额度充足，判断令牌额度是否充足
		if !relayInfo.TokenUnlimited {
			// 非无限令牌，判断令牌额度是否充足
			tokenQuota := c.GetInt("token_quota")
			if tokenQuota > 100*preConsumedQuota {
				// 令牌额度充足，信任令牌
				preConsumedQuota = 0
				common.LogInfo(c, fmt.Sprintf("user %d quota %s and token %d quota %d are enough, trusted and no need to pre-consume", relayInfo.UserId, common.FormatQuota(userQuota), relayInfo.TokenId, tokenQuota))
			}
		} else {
			// in this case, we do not pre-consume quota
			// because the user has enough quota
			preConsumedQuota = 0
			common.LogInfo(c, fmt.Sprintf("user %d with unlimited token has enough quota %s, trusted and no need to pre-consume", relayInfo.UserId, common.FormatQuota(userQuota)))
		}
	}

	if preConsumedQuota > 0 {
		err := service.PreConsumeTokenQuota(relayInfo, preConsumedQuota)
		if err != nil {
			return 0, 0, types.NewErrorWithStatusCode(err, types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusForbidden)
		}
		err = model.DecreaseUserQuota(relayInfo.UserId, preConsumedQuota)
		if err != nil {
			return 0, 0, types.NewError(err, types.ErrorCodeUpdateDataError)
		}
	}
	return preConsumedQuota, userQuota, nil
}

func returnPreConsumedQuota(c *gin.Context, relayInfo *relaycommon.RelayInfo, userQuota int, preConsumedQuota int) {
	if preConsumedQuota != 0 {
		gopool.Go(func() {
			relayInfoCopy := *relayInfo

			err := service.PostConsumeQuota(&relayInfoCopy, -preConsumedQuota, 0, false)
			if err != nil {
				common.SysError("error return pre-consumed quota: " + err.Error())
			}
		})
	}
}

func postConsumeQuota(ctx *gin.Context, relayInfo *relaycommon.RelayInfo,
	usage *dto.Usage, preConsumedQuota int, userQuota int, priceData helper.PriceData, extraContent string) {
	if usage == nil {
		usage = &dto.Usage{
			PromptTokens:     relayInfo.PromptTokens,
			CompletionTokens: 0,
			TotalTokens:      relayInfo.PromptTokens,
		}
		extraContent += "（可能是请求出错）"
	}
	useTimeSeconds := time.Now().Unix() - relayInfo.StartTime.Unix()
	promptTokens := usage.PromptTokens
	cacheTokens := usage.PromptTokensDetails.CachedTokens
	imageTokens := usage.PromptTokensDetails.ImageTokens
	audioTokens := usage.PromptTokensDetails.AudioTokens
	completionTokens := usage.CompletionTokens
	modelName := relayInfo.OriginModelName

	tokenName := ctx.GetString("token_name")
	completionRatio := priceData.CompletionRatio
	cacheRatio := priceData.CacheRatio
	imageRatio := priceData.ImageRatio
	modelRatio := priceData.ModelRatio
	groupRatio := priceData.GroupRatioInfo.GroupRatio
	modelPrice := priceData.ModelPrice

	// Convert values to decimal for precise calculation
	dPromptTokens := decimal.NewFromInt(int64(promptTokens))
	dCacheTokens := decimal.NewFromInt(int64(cacheTokens))
	dImageTokens := decimal.NewFromInt(int64(imageTokens))
	dAudioTokens := decimal.NewFromInt(int64(audioTokens))
	dCompletionTokens := decimal.NewFromInt(int64(completionTokens))
	dCompletionRatio := decimal.NewFromFloat(completionRatio)
	dCacheRatio := decimal.NewFromFloat(cacheRatio)
	dImageRatio := decimal.NewFromFloat(imageRatio)
	dModelRatio := decimal.NewFromFloat(modelRatio)
	dGroupRatio := decimal.NewFromFloat(groupRatio)
	dModelPrice := decimal.NewFromFloat(modelPrice)
	dQuotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)

	ratio := dModelRatio.Mul(dGroupRatio)

	// openai web search 工具计费
	var dWebSearchQuota decimal.Decimal
	var webSearchPrice float64
	// response api 格式工具计费
	if relayInfo.ResponsesUsageInfo != nil {
		if webSearchTool, exists := relayInfo.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolWebSearchPreview]; exists && webSearchTool.CallCount > 0 {
			// 计算 web search 调用的配额 (配额 = 价格 * 调用次数 / 1000 * 分组倍率)
			webSearchPrice = operation_setting.GetWebSearchPricePerThousand(modelName, webSearchTool.SearchContextSize)
			dWebSearchQuota = decimal.NewFromFloat(webSearchPrice).
				Mul(decimal.NewFromInt(int64(webSearchTool.CallCount))).
				Div(decimal.NewFromInt(1000)).Mul(dGroupRatio).Mul(dQuotaPerUnit)
			extraContent += fmt.Sprintf("Web Search 调用 %d 次，上下文大小 %s，调用花费 %s",
				webSearchTool.CallCount, webSearchTool.SearchContextSize, dWebSearchQuota.String())
		}
	} else if strings.HasSuffix(modelName, "search-preview") {
		// search-preview 模型不支持 response api
		searchContextSize := ctx.GetString("chat_completion_web_search_context_size")
		if searchContextSize == "" {
			searchContextSize = "medium"
		}
		webSearchPrice = operation_setting.GetWebSearchPricePerThousand(modelName, searchContextSize)
		dWebSearchQuota = decimal.NewFromFloat(webSearchPrice).
			Div(decimal.NewFromInt(1000)).Mul(dGroupRatio).Mul(dQuotaPerUnit)
		extraContent += fmt.Sprintf("Web Search 调用 1 次，上下文大小 %s，调用花费 %s",
			searchContextSize, dWebSearchQuota.String())
	}
	// claude web search tool 计费
	var dClaudeWebSearchQuota decimal.Decimal
	var claudeWebSearchPrice float64
	claudeWebSearchCallCount := ctx.GetInt("claude_web_search_requests")
	if claudeWebSearchCallCount > 0 {
		claudeWebSearchPrice = operation_setting.GetClaudeWebSearchPricePerThousand()
		dClaudeWebSearchQuota = decimal.NewFromFloat(claudeWebSearchPrice).
			Div(decimal.NewFromInt(1000)).Mul(dGroupRatio).Mul(dQuotaPerUnit).Mul(decimal.NewFromInt(int64(claudeWebSearchCallCount)))
		extraContent += fmt.Sprintf("Claude Web Search 调用 %d 次，调用花费 %s",
			claudeWebSearchCallCount, dClaudeWebSearchQuota.String())
	}
	// file search tool 计费
	var dFileSearchQuota decimal.Decimal
	var fileSearchPrice float64
	if relayInfo.ResponsesUsageInfo != nil {
		if fileSearchTool, exists := relayInfo.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolFileSearch]; exists && fileSearchTool.CallCount > 0 {
			fileSearchPrice = operation_setting.GetFileSearchPricePerThousand()
			dFileSearchQuota = decimal.NewFromFloat(fileSearchPrice).
				Mul(decimal.NewFromInt(int64(fileSearchTool.CallCount))).
				Div(decimal.NewFromInt(1000)).Mul(dGroupRatio).Mul(dQuotaPerUnit)
			extraContent += fmt.Sprintf("File Search 调用 %d 次，调用花费 %s",
				fileSearchTool.CallCount, dFileSearchQuota.String())
		}
	}

	var quotaCalculateDecimal decimal.Decimal

	var audioInputQuota decimal.Decimal
	var audioInputPrice float64
	if !priceData.UsePrice {
		baseTokens := dPromptTokens
		// 减去 cached tokens
		var cachedTokensWithRatio decimal.Decimal
		if !dCacheTokens.IsZero() {
			baseTokens = baseTokens.Sub(dCacheTokens)
			cachedTokensWithRatio = dCacheTokens.Mul(dCacheRatio)
		}

		// 减去 image tokens
		var imageTokensWithRatio decimal.Decimal
		if !dImageTokens.IsZero() {
			baseTokens = baseTokens.Sub(dImageTokens)
			imageTokensWithRatio = dImageTokens.Mul(dImageRatio)
		}

		// 减去 Gemini audio tokens
		if !dAudioTokens.IsZero() {
			audioInputPrice = operation_setting.GetGeminiInputAudioPricePerMillionTokens(modelName)
			if audioInputPrice > 0 {
				// 重新计算 base tokens
				baseTokens = baseTokens.Sub(dAudioTokens)
				audioInputQuota = decimal.NewFromFloat(audioInputPrice).Div(decimal.NewFromInt(1000000)).Mul(dAudioTokens).Mul(dGroupRatio).Mul(dQuotaPerUnit)
				extraContent += fmt.Sprintf("Audio Input 花费 %s", audioInputQuota.String())
			}
		}
		promptQuota := baseTokens.Add(cachedTokensWithRatio).Add(imageTokensWithRatio)

		completionQuota := dCompletionTokens.Mul(dCompletionRatio)

		quotaCalculateDecimal = promptQuota.Add(completionQuota).Mul(ratio)

		if !ratio.IsZero() && quotaCalculateDecimal.LessThanOrEqual(decimal.Zero) {
			quotaCalculateDecimal = decimal.NewFromInt(1)
		}
	} else {
		quotaCalculateDecimal = dModelPrice.Mul(dQuotaPerUnit).Mul(dGroupRatio)
	}
	// 添加 responses tools call 调用的配额
	quotaCalculateDecimal = quotaCalculateDecimal.Add(dWebSearchQuota)
	quotaCalculateDecimal = quotaCalculateDecimal.Add(dFileSearchQuota)
	// 添加 audio input 独立计费
	quotaCalculateDecimal = quotaCalculateDecimal.Add(audioInputQuota)

	quota := int(quotaCalculateDecimal.Round(0).IntPart())
	totalTokens := promptTokens + completionTokens

	var logContent string
	if !priceData.UsePrice {
		logContent = fmt.Sprintf("模型倍率 %.2f，补全倍率 %.2f，分组倍率 %.2f", modelRatio, completionRatio, groupRatio)
	} else {
		logContent = fmt.Sprintf("模型价格 %.2f，分组倍率 %.2f", modelPrice, groupRatio)
	}

	// record all the consume log even if quota is 0
	if totalTokens == 0 {
		// in this case, must be some error happened
		// we cannot just return, because we may have to return the pre-consumed quota
		quota = 0
		logContent += fmt.Sprintf("（可能是上游超时）")
		common.LogError(ctx, fmt.Sprintf("total tokens is 0, cannot consume quota, userId %d, channelId %d, "+
			"tokenId %d, model %s， pre-consumed quota %d", relayInfo.UserId, relayInfo.ChannelId, relayInfo.TokenId, modelName, preConsumedQuota))
	} else {
		model.UpdateUserUsedQuotaAndRequestCount(relayInfo.UserId, quota)
		model.UpdateChannelUsedQuota(relayInfo.ChannelId, quota)
	}

	quotaDelta := quota - preConsumedQuota
	if quotaDelta != 0 {
		err := service.PostConsumeQuota(relayInfo, quotaDelta, preConsumedQuota, true)
		if err != nil {
			common.LogError(ctx, "error consuming token remain quota: "+err.Error())
		}
	}

	logModel := modelName
	if strings.HasPrefix(logModel, "gpt-4-gizmo") {
		logModel = "gpt-4-gizmo-*"
		logContent += fmt.Sprintf("，模型 %s", modelName)
	}
	if strings.HasPrefix(logModel, "gpt-4o-gizmo") {
		logModel = "gpt-4o-gizmo-*"
		logContent += fmt.Sprintf("，模型 %s", modelName)
	}
	if extraContent != "" {
		logContent += ", " + extraContent
	}
	other := service.GenerateTextOtherInfo(ctx, relayInfo, modelRatio, groupRatio, completionRatio, cacheTokens, cacheRatio, modelPrice, priceData.GroupRatioInfo.GroupSpecialRatio)
	if imageTokens != 0 {
		other["image"] = true
		other["image_ratio"] = imageRatio
		other["image_output"] = imageTokens
	}
	if !dWebSearchQuota.IsZero() {
		if relayInfo.ResponsesUsageInfo != nil {
			if webSearchTool, exists := relayInfo.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolWebSearchPreview]; exists {
				other["web_search"] = true
				other["web_search_call_count"] = webSearchTool.CallCount
				other["web_search_price"] = webSearchPrice
			}
		} else if strings.HasSuffix(modelName, "search-preview") {
			other["web_search"] = true
			other["web_search_call_count"] = 1
			other["web_search_price"] = webSearchPrice
		}
	} else if !dClaudeWebSearchQuota.IsZero() {
		other["web_search"] = true
		other["web_search_call_count"] = claudeWebSearchCallCount
		other["web_search_price"] = claudeWebSearchPrice
	}
	if !dFileSearchQuota.IsZero() && relayInfo.ResponsesUsageInfo != nil {
		if fileSearchTool, exists := relayInfo.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolFileSearch]; exists {
			other["file_search"] = true
			other["file_search_call_count"] = fileSearchTool.CallCount
			other["file_search_price"] = fileSearchPrice
		}
	}
	if !audioInputQuota.IsZero() {
		other["audio_input_seperate_price"] = true
		other["audio_input_token_count"] = audioTokens
		other["audio_input_price"] = audioInputPrice
	}
	model.RecordConsumeLog(ctx, relayInfo.UserId, model.RecordConsumeLogParams{
		ChannelId:        relayInfo.ChannelId,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		ModelName:        logModel,
		TokenName:        tokenName,
		Quota:            quota,
		Content:          logContent,
		TokenId:          relayInfo.TokenId,
		UserQuota:        userQuota,
		UseTimeSeconds:   int(useTimeSeconds),
		IsStream:         relayInfo.IsStream,
		Group:            relayInfo.UsingGroup,
		Other:            other,
	})
}
