package test

import (
	"encoding/json"
	"fmt"
	"one-api/dto"
	"testing"
)

// 模拟工具参数类型补全逻辑
func processToolParameters(tools []dto.ToolCallRequest) {
	if len(tools) > 0 {
		for i := range tools {
			tool := &tools[i]
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
									fmt.Printf("工具 %s 的参数 %s 缺少type字段，已默认设置为string类型\n", tool.Function.Name, propName)
								}
							}
						}
					}
				}
			}
		}
	}
}

func TestToolParameterTypeCompletion(t *testing.T) {
	// 测试用例1: 正常参数（已有type字段）
	t.Run("Normal parameters with type field", func(t *testing.T) {
		tools := []dto.ToolCallRequest{
			{
				Function: dto.FunctionRequest{
					Name: "test_function",
					Parameters: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"param1": map[string]interface{}{
								"type": "string",
								"description": "参数1",
							},
							"param2": map[string]interface{}{
								"type": "integer", 
								"description": "参数2",
							},
						},
					},
				},
			},
		}

		// 处理前的JSON
		beforeJSON, _ := json.MarshalIndent(tools[0].Function.Parameters, "", "  ")
		t.Logf("处理前参数: %s", beforeJSON)

		processToolParameters(tools)

		// 处理后的JSON
		afterJSON, _ := json.MarshalIndent(tools[0].Function.Parameters, "", "  ")
		t.Logf("处理后参数: %s", afterJSON)

		// 验证type字段未被修改
		params := tools[0].Function.Parameters.(map[string]interface{})
		properties := params["properties"].(map[string]interface{})
		param1 := properties["param1"].(map[string]interface{})
		param2 := properties["param2"].(map[string]interface{})

		if param1["type"] != "string" {
			t.Errorf("param1的type应该保持为string，实际为: %v", param1["type"])
		}
		if param2["type"] != "integer" {
			t.Errorf("param2的type应该保持为integer，实际为: %v", param2["type"])
		}
	})

	// 测试用例2: 缺失type字段的参数
	t.Run("Parameters missing type field", func(t *testing.T) {
		tools := []dto.ToolCallRequest{
			{
				Function: dto.FunctionRequest{
					Name: "test_function_missing_type",
					Parameters: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"param1": map[string]interface{}{
								"description": "参数1缺少type",
							},
							"param2": map[string]interface{}{
								"type": "integer",
								"description": "参数2有type",
							},
							"param3": map[string]interface{}{
								"title": "参数3",
								"description": "参数3缺少type但有title",
							},
						},
					},
				},
			},
		}

		// 处理前的JSON
		beforeJSON, _ := json.MarshalIndent(tools[0].Function.Parameters, "", "  ")
		t.Logf("处理前参数: %s", beforeJSON)

		processToolParameters(tools)

		// 处理后的JSON
		afterJSON, _ := json.MarshalIndent(tools[0].Function.Parameters, "", "  ")
		t.Logf("处理后参数: %s", afterJSON)

		// 验证type字段补全
		params := tools[0].Function.Parameters.(map[string]interface{})
		properties := params["properties"].(map[string]interface{})
		param1 := properties["param1"].(map[string]interface{})
		param2 := properties["param2"].(map[string]interface{})
		param3 := properties["param3"].(map[string]interface{})

		if param1["type"] != "string" {
			t.Errorf("param1的type应该被补全为string，实际为: %v", param1["type"])
		}
		if param2["type"] != "integer" {
			t.Errorf("param2的type应该保持为integer，实际为: %v", param2["type"])
		}
		if param3["type"] != "string" {
			t.Errorf("param3的type应该被补全为string，实际为: %v", param3["type"])
		}
	})

	// 测试用例3: 空参数
	t.Run("Empty parameters", func(t *testing.T) {
		tools := []dto.ToolCallRequest{
			{
				Function: dto.FunctionRequest{
					Name:       "test_function_empty",
					Parameters: nil,
				},
			},
		}

		processToolParameters(tools)

		// 验证空参数未被处理
		if tools[0].Function.Parameters != nil {
			t.Errorf("空参数应该保持为nil")
		}
	})

	// 测试用例4: 无效参数结构
	t.Run("Invalid parameter structure", func(t *testing.T) {
		tools := []dto.ToolCallRequest{
			{
				Function: dto.FunctionRequest{
					Name:       "test_function_invalid",
					Parameters: "invalid string parameters",
				},
			},
		}

		// 不应该panic
		processToolParameters(tools)

		t.Log("无效参数结构处理完成，未发生panic")
	})

	// 测试用例5: 复杂嵌套结构（简化版本 - 只处理顶层）
	t.Run("Complex nested structure", func(t *testing.T) {
		tools := []dto.ToolCallRequest{
			{
				Function: dto.FunctionRequest{
					Name: "test_function_complex",
					Parameters: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"user_info": map[string]interface{}{
								"type": "object",
								"description": "用户信息对象",
							},
							"simple_field": map[string]interface{}{
								"description": "简单字段",
							},
						},
					},
				},
			},
		}

		// 处理前的JSON
		beforeJSON, _ := json.MarshalIndent(tools[0].Function.Parameters, "", "  ")
		t.Logf("处理前参数: %s", beforeJSON)

		processToolParameters(tools)

		// 处理后的JSON
		afterJSON, _ := json.MarshalIndent(tools[0].Function.Parameters, "", "  ")
		t.Logf("处理后参数: %s", afterJSON)

		// 验证顶层结构中的type补全（当前实现只处理顶层）
		params := tools[0].Function.Parameters.(map[string]interface{})
		properties := params["properties"].(map[string]interface{})
		userInfo := properties["user_info"].(map[string]interface{})
		simpleField := properties["simple_field"].(map[string]interface{})

		// user_info字段本身有type字段，应该保持不变
		if userInfo["type"] != "object" {
			t.Errorf("user_info字段的type应该保持为object，实际为: %v", userInfo["type"])
		}
		// simple_field字段缺少type字段，应该被补全
		if simpleField["type"] != "string" {
			t.Errorf("simple_field的type应该被补全为string，实际为: %v", simpleField["type"])
		}
		
		t.Log("注意：当前实现只处理顶层的properties，不递归处理嵌套对象中的properties")
	})
}

func TestToolParameterTypeCompletionRealWorldExample(t *testing.T) {
	// 真实场景测试：模拟前端发送的不完整工具定义
	t.Run("Real world example - incomplete tool definition", func(t *testing.T) {
		tools := []dto.ToolCallRequest{
			{
				Function: dto.FunctionRequest{
					Name:        "web_search",
					Description: "搜索网络信息",
					Parameters: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"query": map[string]interface{}{
								"description": "搜索查询词",
								"title":       "搜索查询",
							},
							"max_results": map[string]interface{}{
								"description": "最大结果数量",
								"default":     10,
							},
							"language": map[string]interface{}{
								"description": "搜索语言",
								"enum":        []string{"zh", "en", "ja"},
							},
						},
						"required": []string{"query"},
					},
				},
			},
		}

		t.Logf("工具名称: %s", tools[0].Function.Name)
		t.Logf("工具描述: %s", tools[0].Function.Description)

		// 处理前的JSON
		beforeJSON, _ := json.MarshalIndent(tools[0].Function.Parameters, "", "  ")
		t.Logf("处理前参数:\n%s", beforeJSON)

		processToolParameters(tools)

		// 处理后的JSON
		afterJSON, _ := json.MarshalIndent(tools[0].Function.Parameters, "", "  ")
		t.Logf("处理后参数:\n%s", afterJSON)

		// 验证补全结果
		params := tools[0].Function.Parameters.(map[string]interface{})
		properties := params["properties"].(map[string]interface{})
		
		query := properties["query"].(map[string]interface{})
		maxResults := properties["max_results"].(map[string]interface{})
		language := properties["language"].(map[string]interface{})

		if query["type"] != "string" {
			t.Errorf("query参数的type应该被补全为string，实际为: %v", query["type"])
		}
		if maxResults["type"] != "string" {
			t.Errorf("max_results参数的type应该被补全为string，实际为: %v", maxResults["type"])
		}
		if language["type"] != "string" {
			t.Errorf("language参数的type应该被补全为string，实际为: %v", language["type"])
		}

		// 验证其他字段保持不变
		if query["description"] != "搜索查询词" {
			t.Errorf("query参数的description字段应该保持不变")
		}
		if maxResults["default"] != 10 {
			t.Errorf("max_results参数的default字段应该保持不变")
		}
		if len(language["enum"].([]string)) != 3 {
			t.Errorf("language参数的enum字段应该保持不变")
		}
	})
}