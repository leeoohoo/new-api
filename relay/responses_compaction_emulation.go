package relay

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const responsesCompactionEmulationInstruction = "Compact the provided conversation/context for continuation. Preserve durable instructions, user preferences, unresolved tasks, important facts, tool results, file or artifact references, and decisions. Remove redundancy and transient wording. Return only the compacted context, suitable for continuing the conversation."

func shouldEmulateResponsesCompaction(info *relaycommon.RelayInfo) bool {
	return info != nil &&
		info.RelayMode == relayconstant.RelayModeResponsesCompact &&
		!common.SupportsResponsesCompact(info.ChannelType, info.ApiType)
}

func emulateResponsesCompaction(c *gin.Context, info *relaycommon.RelayInfo, req *dto.OpenAIResponsesCompactionRequest) (*dto.Usage, *types.NewAPIError) {
	logger.LogWarn(c, fmt.Sprintf(
		"responses compaction emulated: channel_id=%d channel_type=%d api_type=%d model=%q",
		info.ChannelId, info.ChannelType, info.ApiType, info.OriginModelName,
	))

	emulatedReq, err := buildResponsesCompactionEmulationRequest(req)
	if err != nil {
		return nil, types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}

	saved := saveResponsesCompactionRelayState(info)
	applyResponsesCompactionEmulationRelayState(info, emulatedReq)
	defer saved.restore(info)

	adaptor, requestBody, closer, apiErr := PrepareResponsesRequest(c, info, emulatedReq)
	if apiErr != nil {
		return nil, apiErr
	}
	defer closer.Close()

	resp, err := adaptor.DoRequest(c, info, requestBody)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}
	httpResp, _ := resp.(*http.Response)
	if httpResp == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	if httpResp.StatusCode != http.StatusOK {
		apiErr := service.RelayErrorHandler(c.Request.Context(), httpResp, false)
		service.ResetStatusCode(apiErr, c.GetString("status_code_mapping"))
		return nil, apiErr
	}

	captured := newBufferedGinResponseWriter(c.Writer)
	originalWriter := c.Writer
	c.Writer = captured
	usageAny, apiErr := adaptor.DoResponse(c, httpResp, info)
	c.Writer = originalWriter
	if apiErr != nil {
		service.ResetStatusCode(apiErr, c.GetString("status_code_mapping"))
		return nil, apiErr
	}
	usage, ok := usageAny.(*dto.Usage)
	if !ok || usage == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid usage type %T", usageAny), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	responseBody, apiErr := wrapEmulatedResponsesCompaction(captured.Bytes(), usage)
	if apiErr != nil {
		return nil, apiErr
	}
	service.IOCopyBytesGracefully(c, &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}, responseBody)
	return usage, nil
}

func buildResponsesCompactionEmulationRequest(req *dto.OpenAIResponsesCompactionRequest) (*dto.OpenAIResponsesRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("request is nil")
	}
	instructions := responsesCompactionEmulationInstruction
	if extra := strings.TrimSpace(string(req.Instructions)); extra != "" && extra != "null" {
		instructions += "\n\nAdditional compaction instructions from the client:\n" + extra
	}
	instructionsJSON, err := common.Marshal(instructions)
	if err != nil {
		return nil, err
	}
	temperature := 0.0
	return &dto.OpenAIResponsesRequest{
		Model:                req.Model,
		Input:                req.Input,
		Instructions:         instructionsJSON,
		PreviousResponseID:   req.PreviousResponseID,
		ParallelToolCalls:    req.ParallelToolCalls,
		ServiceTier:          req.ServiceTier,
		PromptCacheKey:       req.PromptCacheKey,
		PromptCacheOptions:   req.PromptCacheOptions,
		PromptCacheRetention: req.PromptCacheRetention,
		Text:                 req.Text,
		Temperature:          &temperature,
		Stream:               common.GetPointer(false),
	}, nil
}

func wrapEmulatedResponsesCompaction(responseBody []byte, usage *dto.Usage) ([]byte, *types.NewAPIError) {
	var responsesResp dto.OpenAIResponsesResponse
	if err := common.Unmarshal(responseBody, &responsesResp); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if oaiError := responsesResp.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.WithOpenAIError(*oaiError, http.StatusOK)
	}
	output, err := common.Marshal(responsesResp.Output)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
	}
	if responsesResp.ID == "" {
		responsesResp.ID = "resp_compaction_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	if responsesResp.CreatedAt == 0 {
		responsesResp.CreatedAt = dto.IntValue(time.Now().Unix())
	}
	compactUsage := responsesResp.Usage
	if compactUsage == nil {
		compactUsage = usage
		compactUsage.InputTokens = usage.PromptTokens
		compactUsage.OutputTokens = usage.CompletionTokens
	}
	compactResp := dto.OpenAIResponsesCompactionResponse{
		ID:        responsesResp.ID,
		Object:    "response.compaction",
		CreatedAt: responsesResp.CreatedAt,
		Output:    output,
		Usage:     compactUsage,
	}
	payload, err := common.Marshal(compactResp)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
	}
	return payload, nil
}

type responsesCompactionRelayState struct {
	request                dto.Request
	relayMode              int
	relayFormat            types.RelayFormat
	requestURLPath         string
	isStream               bool
	responsesUsageInfo     *relaycommon.ResponsesUsageInfo
	requestConversionChain []types.RelayFormat
	finalRequestFormat     types.RelayFormat
}

func saveResponsesCompactionRelayState(info *relaycommon.RelayInfo) responsesCompactionRelayState {
	return responsesCompactionRelayState{
		request:                info.Request,
		relayMode:              info.RelayMode,
		relayFormat:            info.RelayFormat,
		requestURLPath:         info.RequestURLPath,
		isStream:               info.IsStream,
		responsesUsageInfo:     info.ResponsesUsageInfo,
		requestConversionChain: append([]types.RelayFormat(nil), info.RequestConversionChain...),
		finalRequestFormat:     info.FinalRequestRelayFormat,
	}
}

func (s responsesCompactionRelayState) restore(info *relaycommon.RelayInfo) {
	info.Request = s.request
	info.RelayMode = s.relayMode
	info.RelayFormat = s.relayFormat
	info.RequestURLPath = s.requestURLPath
	info.IsStream = s.isStream
	info.ResponsesUsageInfo = s.responsesUsageInfo
	info.RequestConversionChain = s.requestConversionChain
	info.FinalRequestRelayFormat = s.finalRequestFormat
}

func applyResponsesCompactionEmulationRelayState(info *relaycommon.RelayInfo, req *dto.OpenAIResponsesRequest) {
	info.Request = req
	info.RelayMode = relayconstant.RelayModeResponses
	info.RelayFormat = types.RelayFormatOpenAIResponses
	info.RequestURLPath = "/v1/responses"
	info.IsStream = false
	info.ResponsesUsageInfo = &relaycommon.ResponsesUsageInfo{BuiltInTools: make(map[string]*relaycommon.BuildInToolInfo)}
	info.RequestConversionChain = nil
	info.FinalRequestRelayFormat = ""
}

type bufferedGinResponseWriter struct {
	gin.ResponseWriter
	header http.Header
	body   bytes.Buffer
	status int
}

func newBufferedGinResponseWriter(base gin.ResponseWriter) *bufferedGinResponseWriter {
	return &bufferedGinResponseWriter{
		ResponseWriter: base,
		header:         make(http.Header),
		status:         http.StatusOK,
	}
}

func (w *bufferedGinResponseWriter) Header() http.Header {
	return w.header
}

func (w *bufferedGinResponseWriter) WriteHeader(code int) {
	if code > 0 && !w.Written() {
		w.status = code
	}
}

func (w *bufferedGinResponseWriter) WriteHeaderNow() {
	if w.status == 0 {
		w.status = http.StatusOK
	}
}

func (w *bufferedGinResponseWriter) Write(data []byte) (int, error) {
	w.WriteHeaderNow()
	return w.body.Write(data)
}

func (w *bufferedGinResponseWriter) WriteString(data string) (int, error) {
	w.WriteHeaderNow()
	return w.body.WriteString(data)
}

func (w *bufferedGinResponseWriter) Status() int {
	return w.status
}

func (w *bufferedGinResponseWriter) Size() int {
	return w.body.Len()
}

func (w *bufferedGinResponseWriter) Written() bool {
	return w.body.Len() > 0
}

func (w *bufferedGinResponseWriter) Flush() {}

func (w *bufferedGinResponseWriter) Bytes() []byte {
	return w.body.Bytes()
}

func (w *bufferedGinResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.ResponseWriter.Hijack()
}
