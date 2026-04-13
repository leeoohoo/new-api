package relay

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/samber/lo"

	"github.com/gin-gonic/gin"
)

func TextHelper(c *gin.Context, info *relaycommon.RelayInfo) (newAPIError *types.NewAPIError) {
	info.InitChannelMeta(c)

	textReq, ok := info.Request.(*dto.GeneralOpenAIRequest)
	if !ok {
		return types.NewErrorWithStatusCode(fmt.Errorf("invalid request type, expected dto.GeneralOpenAIRequest, got %T", info.Request), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}

	request, err := common.DeepCopy(textReq)
	if err != nil {
		return types.NewError(fmt.Errorf("failed to copy request to GeneralOpenAIRequest: %w", err), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	if request.WebSearchOptions != nil {
		c.Set("chat_completion_web_search_context_size", request.WebSearchOptions.SearchContextSize)
	}

	err = helper.ModelMappedHelper(c, info, request)
	if err != nil {
		return types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}

	includeUsage := true
	// 判断用户是否需要返回使用情况
	if request.StreamOptions != nil {
		includeUsage = request.StreamOptions.IncludeUsage
	}

	// 如果不支持StreamOptions，将StreamOptions设置为nil
	if !info.SupportStreamOptions || !lo.FromPtrOr(request.Stream, false) {
		request.StreamOptions = nil
	} else {
		// 如果支持StreamOptions，且请求中没有设置StreamOptions，根据配置文件设置StreamOptions
		if constant.ForceStreamOption {
			request.StreamOptions = &dto.StreamOptions{
				IncludeUsage: true,
			}
		}
	}

	info.ShouldIncludeUsage = includeUsage

	if info.ChannelType == constant.ChannelTypeMiniMax {
		applyMiniMaxCompatibility(c, request)
	}

	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return types.NewError(fmt.Errorf("invalid api type: %d", info.ApiType), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}
	adaptor.Init(info)

	passThroughGlobal := model_setting.GetGlobalSettings().PassThroughRequestEnabled
	if info.RelayMode == relayconstant.RelayModeChatCompletions &&
		!passThroughGlobal &&
		!info.ChannelSetting.PassThroughBodyEnabled &&
		service.ShouldChatCompletionsUseResponsesGlobal(info.ChannelId, info.ChannelType, info.OriginModelName) {
		applySystemPromptIfNeeded(c, info, request)
		usage, newApiErr := chatCompletionsViaResponses(c, info, adaptor, request)
		if newApiErr != nil {
			return newApiErr
		}

		var containAudioTokens = usage.CompletionTokenDetails.AudioTokens > 0 || usage.PromptTokensDetails.AudioTokens > 0
		var containsAudioRatios = ratio_setting.ContainsAudioRatio(info.OriginModelName) || ratio_setting.ContainsAudioCompletionRatio(info.OriginModelName)

		if containAudioTokens && containsAudioRatios {
			service.PostAudioConsumeQuota(c, info, usage, "")
		} else {
			service.PostTextConsumeQuota(c, info, usage, nil)
		}
		return nil
	}

	var requestBody io.Reader
	var minimaxUpstreamPayload []byte

	if passThroughGlobal || info.ChannelSetting.PassThroughBodyEnabled {
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			return types.NewErrorWithStatusCode(err, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		if common.DebugEnabled {
			if debugBytes, bErr := storage.Bytes(); bErr == nil {
				println("requestBody: ", string(debugBytes))
			}
		}
		if info.ChannelType == constant.ChannelTypeMiniMax {
			if bodyBytes, bErr := storage.Bytes(); bErr == nil {
				minimaxUpstreamPayload = append([]byte(nil), bodyBytes...)
				bodyText := string(bodyBytes)
				logger.LogInfo(c, "minimax downstream request body: "+bodyText)
				logger.LogInfo(c, "minimax upstream request body (pass-through): "+bodyText)
			} else {
				logger.LogError(c, "failed to read minimax pass-through request body: "+bErr.Error())
			}
		}
		if info.ChannelType == constant.ChannelTypeMiniMax && len(minimaxUpstreamPayload) > 0 {
			requestBody = bytes.NewReader(minimaxUpstreamPayload)
		} else {
			requestBody = common.ReaderOnly(storage)
		}
	} else {
		convertedRequest, err := adaptor.ConvertOpenAIRequest(c, info, request)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		relaycommon.AppendRequestConversionFromRequest(info, convertedRequest)

		if info.ChannelSetting.SystemPrompt != "" {
			// 如果有系统提示，则将其添加到请求中
			request, ok := convertedRequest.(*dto.GeneralOpenAIRequest)
			if ok {
				containSystemPrompt := false
				for _, message := range request.Messages {
					if message.Role == request.GetSystemRoleName() {
						containSystemPrompt = true
						break
					}
				}
				if !containSystemPrompt {
					// 如果没有系统提示，则添加系统提示
					systemMessage := dto.Message{
						Role:    request.GetSystemRoleName(),
						Content: info.ChannelSetting.SystemPrompt,
					}
					request.Messages = append([]dto.Message{systemMessage}, request.Messages...)
				} else if info.ChannelSetting.SystemPromptOverride {
					common.SetContextKey(c, constant.ContextKeySystemPromptOverride, true)
					// 如果有系统提示，且允许覆盖，则拼接到前面
					for i, message := range request.Messages {
						if message.Role == request.GetSystemRoleName() {
							if message.IsStringContent() {
								request.Messages[i].SetStringContent(info.ChannelSetting.SystemPrompt + "\n" + message.StringContent())
							} else {
								contents := message.ParseContent()
								contents = append([]dto.MediaContent{
									{
										Type: dto.ContentTypeText,
										Text: info.ChannelSetting.SystemPrompt,
									},
								}, contents...)
								request.Messages[i].Content = contents
							}
							break
						}
					}
				}
			}
		}

		jsonData, err := common.Marshal(convertedRequest)
		if err != nil {
			return types.NewError(err, types.ErrorCodeJsonMarshalFailed, types.ErrOptionWithSkipRetry())
		}

		// remove disabled fields for OpenAI API
		jsonData, err = relaycommon.RemoveDisabledFields(jsonData, info.ChannelOtherSettings, info.ChannelSetting.PassThroughBodyEnabled)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}

		// apply param override
		if len(info.ParamOverride) > 0 {
			jsonData, err = relaycommon.ApplyParamOverrideWithRelayInfo(jsonData, info)
			if err != nil {
				return newAPIErrorFromParamOverride(err)
			}
		}

		if info.ChannelType == constant.ChannelTypeMiniMax {
			minimaxUpstreamPayload = append([]byte(nil), jsonData...)
			logger.LogInfo(
				c,
				fmt.Sprintf(
					"minimax request summary: model=%s stream=%t messages=%d max_tokens=%d max_completion_tokens=%d temperature_set=%t top_p_set=%t",
					request.Model,
					lo.FromPtrOr(request.Stream, false),
					len(request.Messages),
					lo.FromPtrOr(request.MaxTokens, uint(0)),
					lo.FromPtrOr(request.MaxCompletionTokens, uint(0)),
					request.Temperature != nil,
					request.TopP != nil,
				),
			)

			if storage, sErr := common.GetBodyStorage(c); sErr == nil {
				if downstreamBody, bErr := storage.Bytes(); bErr == nil {
					logger.LogInfo(c, "minimax downstream request body: "+string(downstreamBody))
				} else {
					logger.LogError(c, "failed to read minimax downstream request body: "+bErr.Error())
				}
			} else {
				logger.LogError(c, "failed to read minimax downstream request body storage: "+sErr.Error())
			}

			logger.LogInfo(c, "minimax upstream request body: "+string(jsonData))
		}

		logger.LogDebug(c, fmt.Sprintf("text request body: %s", string(jsonData)))

		requestBody = bytes.NewBuffer(jsonData)
	}

	var httpResp *http.Response
	resp, err := doRequestWithMiniMaxRetry(c, adaptor, info, requestBody, minimaxUpstreamPayload)
	if err != nil {
		return types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}

	statusCodeMappingStr := c.GetString("status_code_mapping")

	if resp != nil {
		httpResp = resp.(*http.Response)
		info.IsStream = info.IsStream || strings.HasPrefix(httpResp.Header.Get("Content-Type"), "text/event-stream")
		if httpResp.StatusCode != http.StatusOK {
			newApiErr := service.RelayErrorHandler(c.Request.Context(), httpResp, false)
			// reset status code 重置状态码
			service.ResetStatusCode(newApiErr, statusCodeMappingStr)
			return newApiErr
		}
	}

	usage, newApiErr := adaptor.DoResponse(c, httpResp, info)
	if newApiErr != nil {
		// reset status code 重置状态码
		service.ResetStatusCode(newApiErr, statusCodeMappingStr)
		return newApiErr
	}

	var containAudioTokens = usage.(*dto.Usage).CompletionTokenDetails.AudioTokens > 0 || usage.(*dto.Usage).PromptTokensDetails.AudioTokens > 0
	var containsAudioRatios = ratio_setting.ContainsAudioRatio(info.OriginModelName) || ratio_setting.ContainsAudioCompletionRatio(info.OriginModelName)

	if containAudioTokens && containsAudioRatios {
		service.PostAudioConsumeQuota(c, info, usage.(*dto.Usage), "")
	} else {
		service.PostTextConsumeQuota(c, info, usage.(*dto.Usage), nil)
	}
	return nil
}

func doRequestWithMiniMaxRetry(c *gin.Context, adaptor interface {
	DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error)
}, info *relaycommon.RelayInfo, requestBody io.Reader, payload []byte) (any, error) {
	resp, err := adaptor.DoRequest(c, info, requestBody)
	if err == nil || info.ChannelType != constant.ChannelTypeMiniMax || len(payload) == 0 || !shouldRetryMiniMaxDoRequestError(err) {
		return resp, err
	}

	const maxAttempts = 3
	lastErr := err
	for attempt := 2; attempt <= maxAttempts; attempt++ {
		backoff := time.Duration(attempt-1) * 400 * time.Millisecond
		logger.LogWarn(
			c,
			fmt.Sprintf(
				"minimax do request failed (attempt=%d/%d): %v; retrying in %s",
				attempt-1,
				maxAttempts,
				lastErr,
				backoff,
			),
		)
		time.Sleep(backoff)

		resp, err = adaptor.DoRequest(c, info, bytes.NewReader(payload))
		if err == nil {
			logger.LogInfo(c, fmt.Sprintf("minimax do request retry succeeded on attempt=%d/%d", attempt, maxAttempts))
			return resp, nil
		}
		lastErr = err
		if !shouldRetryMiniMaxDoRequestError(lastErr) {
			break
		}
	}

	return nil, lastErr
}

func shouldRetryMiniMaxDoRequestError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := strings.ToLower(err.Error())
	retryablePatterns := []string{
		"connection reset by peer",
		"broken pipe",
		"connection refused",
		"tls handshake timeout",
		"i/o timeout",
		"context deadline exceeded",
		"unexpected eof",
		"eof",
		"http2: server sent goaway",
		"temporary failure in name resolution",
	}
	for _, pattern := range retryablePatterns {
		if strings.Contains(errMsg, pattern) {
			return true
		}
	}
	return false
}

func applyMiniMaxCompatibility(c *gin.Context, request *dto.GeneralOpenAIRequest) {
	if request == nil {
		return
	}

	// MiniMax OpenAI-compatible endpoint is stricter with request shape.
	// Keep tool-calling payload, but sanitize fields known to trigger 2013.
	logger.LogInfo(c, "minimax compatibility version: v20260413-flat-tool-history")
	if request.StreamOptions != nil {
		request.StreamOptions = nil
		logger.LogInfo(c, "minimax compatibility: removed stream_options from upstream request")
	}
	if len(request.FunctionCall) > 0 {
		request.FunctionCall = nil
		logger.LogInfo(c, "minimax compatibility: removed deprecated function_call from upstream request")
	}

	if len(request.Messages) == 0 {
		return
	}

	var (
		stringMessages       int
		arrayMessages        int
		nilMessages          int
		otherMessages        int
		textBlocks           int
		nonTextBlocks        int
		convertedMessages    int
		strippedReasonFields int
		removedToolCalls     int
		removedToolMessages  int
		clearedToolCallBlock int
		mergedConsecutive    int
		mergedSystemMessages int
		movedSystemToFront   bool
	)

	for i := range request.Messages {
		message := &request.Messages[i]

		if message.ReasoningContent != "" || message.Reasoning != "" {
			message.ReasoningContent = ""
			message.Reasoning = ""
			strippedReasonFields++
		}

		switch message.Content.(type) {
		case nil:
			nilMessages++
			// Keep tool-calling continuity while ensuring content remains a valid scalar.
			message.SetStringContent("")
			convertedMessages++
		case string:
			stringMessages++
		case []dto.MediaContent:
			arrayMessages++
			contents := message.Content.([]dto.MediaContent)
			textParts := make([]string, 0, len(contents))
			for _, content := range contents {
				if content.Type == dto.ContentTypeText {
					textBlocks++
					if content.Text != "" {
						textParts = append(textParts, content.Text)
					}
					continue
				}
				nonTextBlocks++
			}

			message.SetStringContent(strings.Join(textParts, "\n"))
			convertedMessages++
		case []any:
			arrayMessages++
			contents := message.ParseContent()
			if len(contents) == 0 {
				message.SetStringContent(message.StringContent())
				convertedMessages++
				break
			}

			textParts := make([]string, 0, len(contents))
			for _, content := range contents {
				if content.Type == dto.ContentTypeText {
					textBlocks++
					if content.Text != "" {
						textParts = append(textParts, content.Text)
					}
					continue
				}
				nonTextBlocks++
			}

			message.SetStringContent(strings.Join(textParts, "\n"))
			convertedMessages++
		default:
			otherMessages++
			raw, err := common.Marshal(message.Content)
			if err != nil {
				message.SetStringContent(fmt.Sprintf("%v", message.Content))
			} else {
				message.SetStringContent(string(raw))
			}
			convertedMessages++
		}
	}

	// MiniMax is sensitive to cross-provider function-call transcripts.
	// Keep current tools capability, but flatten historical tool-call traces into plain chat.
	filteredMessages := make([]dto.Message, 0, len(request.Messages))
	for i := range request.Messages {
		message := request.Messages[i]
		if message.Role == "tool" {
			removedToolMessages++
			continue
		}

		if len(message.ToolCalls) > 0 {
			var toolCalls []dto.ToolCallRequest
			if err := common.Unmarshal(message.ToolCalls, &toolCalls); err != nil {
				clearedToolCallBlock++
				removedToolCalls++
			} else {
				removedToolCalls += len(toolCalls)
			}
			message.ToolCalls = nil
			content := strings.TrimSpace(message.StringContent())
			if content == "" || content == "[tool_call]" {
				removedToolMessages++
				continue
			}
		}

		if message.ToolCallId != "" {
			message.ToolCallId = ""
		}

		filteredMessages = append(filteredMessages, message)
	}
	request.Messages, mergedConsecutive, mergedSystemMessages, movedSystemToFront = compactMiniMaxMessages(filteredMessages)
	roleSequence := miniMaxRoleSequence(request.Messages)

	logger.LogInfo(
		c,
		fmt.Sprintf(
			"minimax compatibility: preserved tools/tool_choice/history, normalized messages=%d role_sequence=%s (string=%d array=%d nil=%d other=%d converted=%d text_blocks=%d non_text_blocks=%d stripped_reasoning_fields=%d removed_tool_calls=%d removed_tool_messages=%d cleared_tool_call_blocks=%d merged_consecutive_roles=%d merged_system_messages=%d moved_system_to_front=%t)",
			len(request.Messages),
			roleSequence,
			stringMessages,
			arrayMessages,
			nilMessages,
			otherMessages,
			convertedMessages,
			textBlocks,
			nonTextBlocks,
			strippedReasonFields,
			removedToolCalls,
			removedToolMessages,
			clearedToolCallBlock,
			mergedConsecutive,
			mergedSystemMessages,
			movedSystemToFront,
		),
	)
}

func compactMiniMaxMessages(messages []dto.Message) ([]dto.Message, int, int, bool) {
	if len(messages) == 0 {
		return messages, 0, 0, false
	}

	roleCollapsed, mergedConsecutive := collapseConsecutiveMiniMaxRoles(messages)

	if len(roleCollapsed) == 0 {
		return roleCollapsed, mergedConsecutive, 0, false
	}

	systemCount := 0
	systemFirstIndex := -1
	systemContents := make([]string, 0, 1)
	nonSystemMessages := make([]dto.Message, 0, len(roleCollapsed))
	for i := range roleCollapsed {
		message := roleCollapsed[i]
		if message.Role != "system" {
			nonSystemMessages = append(nonSystemMessages, message)
			continue
		}

		systemCount++
		if systemFirstIndex == -1 {
			systemFirstIndex = i
		}
		text := strings.TrimSpace(message.StringContent())
		if text != "" {
			systemContents = append(systemContents, text)
		}
	}

	if systemCount == 0 {
		return roleCollapsed, mergedConsecutive, 0, false
	}

	mergedSystemMessages := systemCount - 1
	movedSystemToFront := systemFirstIndex > 0
	systemMessage := dto.Message{
		Role: "system",
	}
	systemMessage.SetStringContent(strings.Join(systemContents, "\n\n"))

	normalizedMessages := make([]dto.Message, 0, 1+len(nonSystemMessages))
	normalizedMessages = append(normalizedMessages, systemMessage)
	normalizedMessages = append(normalizedMessages, nonSystemMessages...)
	normalizedMessages, mergedAfterSystem := collapseConsecutiveMiniMaxRoles(normalizedMessages)
	mergedConsecutive += mergedAfterSystem
	return normalizedMessages, mergedConsecutive, mergedSystemMessages, movedSystemToFront
}

func collapseConsecutiveMiniMaxRoles(messages []dto.Message) ([]dto.Message, int) {
	if len(messages) == 0 {
		return messages, 0
	}

	mergedConsecutive := 0
	roleCollapsed := make([]dto.Message, 0, len(messages))
	for i := range messages {
		message := messages[i]
		message.Role = strings.ToLower(strings.TrimSpace(message.Role))
		if message.Role == "" {
			continue
		}
		message.SetStringContent(strings.TrimSpace(message.StringContent()))

		if len(roleCollapsed) > 0 {
			last := &roleCollapsed[len(roleCollapsed)-1]
			if last.Role == message.Role {
				mergedConsecutive++
				lastContent := strings.TrimSpace(last.StringContent())
				currentContent := strings.TrimSpace(message.StringContent())
				if lastContent == "" {
					last.SetStringContent(currentContent)
				} else if currentContent != "" {
					last.SetStringContent(lastContent + "\n\n" + currentContent)
				}
				continue
			}
		}

		roleCollapsed = append(roleCollapsed, message)
	}
	return roleCollapsed, mergedConsecutive
}

func miniMaxRoleSequence(messages []dto.Message) string {
	if len(messages) == 0 {
		return "empty"
	}
	roles := make([]string, 0, len(messages))
	for i := range messages {
		role := strings.TrimSpace(messages[i].Role)
		if role == "" {
			role = "unknown"
		}
		roles = append(roles, role)
	}
	return strings.Join(roles, "->")
}
