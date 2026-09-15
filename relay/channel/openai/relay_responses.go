package openai

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func OaiResponsesHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	// read response body
	var responsesResponse dto.OpenAIResponsesResponse
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	err = common.Unmarshal(responseBody, &responsesResponse)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if oaiError := responsesResponse.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}

	responseBody = rewriteSGLangResponsesCreatedAt(info, responseBody, "created_at", responsesResponse.CreatedAt)
	responseBody = filterResponsesImageGenerationTools(info, responseBody)

	// 写入新的 response body
	service.IOCopyBytesGracefully(c, resp, responseBody)

	// compute usage
	usage := &dto.Usage{}
	service.ApplyResponsesUsage(usage, responsesResponse.Usage)
	// Count actual tool invocations from Output (not tool declarations).
	for _, output := range responsesResponse.Output {
		switch output.Type {
		case dto.BuildInCallWebSearchCall:
			info.CountBillableToolCall(dto.BuildInCallWebSearchCall, "")
		case dto.BuildInCallFileSearchCall:
			info.CountBillableToolCall(dto.BuildInCallFileSearchCall, "")
		case dto.BuildInCallFunctionCall:
			info.CountBillableToolCall(dto.BuildInCallFunctionCall, output.Name)
		}
	}

	imageCounter := &relaycommon.ImageGenerationCallCounter{}
	if !relaycommon.IsNonBillableResponsesStatus(responsesResponse.Status) {
		for i := range responsesResponse.Output {
			idx := i
			imageCounter.Observe(&responsesResponse.Output[i], &idx)
		}
	}
	imageCounter.Commit(info)

	return usage, nil
}

func OaiResponsesStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		logger.LogError(c, "invalid response or response body")
		return nil, types.NewError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse)
	}

	defer service.CloseResponseBodyGracefully(resp)

	accumulator := service.NewResponsesUsageAccumulator(info)

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {

		// 检查当前数据是否包含 completed 状态和 usage 信息
		var streamResponse dto.ResponsesStreamResponse
		if err := common.UnmarshalJsonStr(data, &streamResponse); err != nil {
			logger.LogError(c, "failed to unmarshal stream response: "+err.Error())
			sr.Error(err)
			return
		}
		if streamResponse.Response != nil {
			data = string(rewriteSGLangResponsesCreatedAt(info, []byte(data), "response.created_at", streamResponse.Response.CreatedAt))
		}
		data = filterResponsesImageGenerationStreamData(info, streamResponse, data)
		sendResponsesStreamData(c, streamResponse, data)
		accumulator.Observe(&streamResponse)
	})

	return accumulator.Finish(), nil
}

func filterResponsesImageGenerationStreamData(info *relaycommon.RelayInfo, streamResponse dto.ResponsesStreamResponse, data string) string {
	if !shouldFilterResponsesImageGenerationTools(info) {
		return data
	}
	if streamResponse.Item != nil && streamResponse.Item.Type == dto.ResponsesOutputTypeImageGenerationCall {
		return ""
	}
	if strings.Contains(streamResponse.Type, "image_generation_call") {
		return ""
	}
	filtered := filterResponsesImageGenerationTools(info, []byte(data))
	return string(filtered)
}

func filterResponsesImageGenerationTools(info *relaycommon.RelayInfo, payload []byte) []byte {
	if !shouldFilterResponsesImageGenerationTools(info) || len(payload) == 0 {
		return payload
	}
	filtered := filterResponsesImageGenerationArray(payload, "tools", isResponsesImageGenerationTool)
	filtered = filterResponsesImageGenerationArray(filtered, "response.tools", isResponsesImageGenerationTool)
	filtered = filterResponsesImageGenerationArray(filtered, "output", isResponsesImageGenerationOutput)
	filtered = filterResponsesImageGenerationArray(filtered, "response.output", isResponsesImageGenerationOutput)
	filtered = filterResponsesImageGenerationItem(filtered, "item")
	filtered = filterResponsesImageGenerationItem(filtered, "response.item")
	filtered = deleteJSONPathIfExists(filtered, "tool_usage.image_gen")
	filtered = deleteJSONPathIfExists(filtered, "response.tool_usage.image_gen")
	return filtered
}

func shouldFilterResponsesImageGenerationTools(info *relaycommon.RelayInfo) bool {
	modelName := ""
	if info != nil {
		modelName = info.OriginModelName
		if info.ChannelMeta != nil && info.ChannelMeta.UpstreamModelName != "" {
			modelName = info.ChannelMeta.UpstreamModelName
		}
	}
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	return modelName == "" || !strings.Contains(modelName, "gpt-image-")
}

func filterResponsesImageGenerationArray(payload []byte, path string, discard func(gjson.Result) bool) []byte {
	array := gjson.GetBytes(payload, path)
	if !array.IsArray() {
		return payload
	}
	values := make([]any, 0)
	changed := false
	for _, item := range array.Array() {
		if discard(item) {
			changed = true
			continue
		}
		values = append(values, item.Value())
	}
	if !changed {
		return payload
	}
	patched, err := sjson.SetBytes(payload, path, values)
	if err != nil {
		return payload
	}
	return patched
}

func filterResponsesImageGenerationItem(payload []byte, path string) []byte {
	item := gjson.GetBytes(payload, path)
	if !item.Exists() || !isResponsesImageGenerationOutput(item) {
		return payload
	}
	patched, err := sjson.DeleteBytes(payload, path)
	if err != nil {
		return payload
	}
	return patched
}

func deleteJSONPathIfExists(payload []byte, path string) []byte {
	if !gjson.GetBytes(payload, path).Exists() {
		return payload
	}
	patched, err := sjson.DeleteBytes(payload, path)
	if err != nil {
		return payload
	}
	return patched
}

func isResponsesImageGenerationTool(item gjson.Result) bool {
	toolType := strings.TrimSpace(item.Get("type").String())
	toolModel := strings.ToLower(strings.TrimSpace(item.Get("model").String()))
	return toolType == dto.BuildInToolImageGeneration || strings.Contains(toolModel, "gpt-image-")
}

func isResponsesImageGenerationOutput(item gjson.Result) bool {
	return strings.TrimSpace(item.Get("type").String()) == dto.ResponsesOutputTypeImageGenerationCall
}

func rewriteSGLangResponsesCreatedAt(info *relaycommon.RelayInfo, payload []byte, path string, createdAt dto.IntValue) []byte {
	if info.GetChannelType() != constant.ChannelTypeSGLang {
		return payload
	}
	if !gjson.GetBytes(payload, path).Exists() {
		return payload
	}
	patched, err := sjson.SetBytes(payload, path, int(createdAt))
	if err != nil {
		return payload
	}
	return patched
}
