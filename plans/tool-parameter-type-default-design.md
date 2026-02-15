# 工具参数类型默认化机制设计方案

## 问题描述

前端系统在调用本系统时，有时候工具调用不符合jinja规范，有的字段没有写类型。在relay-text.go外发工具请求到后端的过程中，需要设计一个机制：如果工具function的"parameters"存在，这些参数如果只有description/title没有type，默认设为string，再外发。

## 问题分析

1. **当前处理流程**：
   - 请求在`relay-text.go:277`通过`adaptor.ConvertOpenAIRequest(c, relayInfo, textRequest)`进行转换
   - 系统已有`ProcessToolTypes()`方法处理工具类型，但不处理参数内部的`type`字段
   - 工具参数使用JSON Schema格式，参数定义在`parameters.properties`中

2. **问题点**：
   - 前端发送的工具参数定义可能缺少`type`字段
   - 这会导致后端无法正确解析参数类型
   - 需要在外发前补全缺失的`type`字段

## 解决方案

### 核心思路

在`relay-text.go`中，在`ConvertOpenAIRequest`调用之前，添加工具参数类型检查和补全逻辑。

### 实现步骤

1. **检查工具存在性**：遍历`textRequest.Tools`数组
2. **解析参数结构**：检查每个工具的`Function.Parameters`字段
3. **检查属性定义**：在`parameters.properties`中遍历每个参数定义
4. **补全缺失类型**：如果参数定义中没有`type`字段，默认设置为`string`
5. **记录日志**：记录类型补全操作以便调试

### 代码实现

在`relay-text.go`的第276行后添加以下代码：

```go
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
```

### 处理逻辑说明

1. **遍历工具**：检查`textRequest.Tools`数组中的每个工具
2. **参数解析**：将`Function.Parameters`解析为`map[string]interface{}`
3. **属性检查**：查找`parameters.properties`中的参数定义
4. **类型补全**：对每个参数定义，如果没有`type`字段则设置为`string`
5. **日志记录**：记录补全操作便于调试和监控

### 兼容性考虑

- **类型安全**：使用类型断言确保安全的类型转换
- **空值处理**：检查每个层级的空值情况
- **原有数据保留**：只添加缺失的`type`字段，不修改现有字段
- **错误容忍**：如果解析失败，保持原数据不变

### 性能影响

- **轻量级操作**：只在有工具时进行处理
- **内存友好**：原地修改，不创建新数据结构
- **快速返回**：任何步骤失败都会快速返回，不影响主流程

## 测试验证

### 测试场景

1. **正常参数**：参数已有`type`字段，不应被修改
2. **缺失类型**：参数缺少`type`字段，应被设置为`string`
3. **空参数**：参数为`nil`，不应处理
4. **无效结构**：参数结构无效，不应影响主流程

### 验证方法

1. **单元测试**：创建包含各种参数结构的测试用例
2. **集成测试**：通过实际请求验证类型补全效果
3. **日志监控**：检查系统日志中的补全记录
4. **后端验证**：确保补全后的参数能被后端正确解析

## 实施计划

1. **代码实现**：在`relay-text.go`中添加类型补全逻辑
2. **测试验证**：创建测试用例验证各种场景
3. **部署上线**：逐步部署到生产环境
4. **监控观察**：通过日志监控补全操作的发生情况

## 风险控制

- **回滚机制**：如有问题可快速回滚代码
- **开关控制**：可添加配置开关控制功能启用
- **灰度发布**：先在测试环境验证，再逐步推广