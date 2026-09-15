package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestEmulateResponsesCompactionUsesResponsesEndpointAndWrapsOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service.InitHttpClient()

	received := make(chan []byte, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/responses", r.URL.Path)
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		received <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"resp_1",
			"object":"response",
			"created_at":1710000000,
			"status":"completed",
			"output":[{"type":"message","id":"msg_1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"compact summary"}]}],
			"usage":{"input_tokens":10,"output_tokens":3,"total_tokens":13}
		}`)
	}))
	t.Cleanup(upstream.Close)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(`{"model":"llama","input":"long conversation"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOllama)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
	common.SetContextKey(c, constant.ContextKeyChannelKey, "test-key")
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "llama")

	req := &dto.OpenAIResponsesCompactionRequest{
		Model: "llama",
		Input: mustJSONRawMessage(t, `"long conversation"`),
	}
	info := &relaycommon.RelayInfo{
		Request:         req,
		OriginModelName: "llama",
		RelayMode:       relayconstant.RelayModeResponsesCompact,
		RelayFormat:     types.RelayFormatOpenAIResponsesCompaction,
		RequestURLPath:  "/v1/responses/compact",
	}
	info.InitChannelMeta(c)
	require.True(t, shouldEmulateResponsesCompaction(info))

	usage, apiErr := emulateResponsesCompaction(c, info, req)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 10, usage.PromptTokens)
	assert.Equal(t, 3, usage.CompletionTokens)
	assert.Equal(t, 13, usage.TotalTokens)

	require.Len(t, received, 1)
	upstreamBody := <-received
	assert.Equal(t, "llama", gjson.GetBytes(upstreamBody, "model").String())
	assert.Equal(t, false, gjson.GetBytes(upstreamBody, "stream").Bool())
	assert.Contains(t, gjson.GetBytes(upstreamBody, "instructions").String(), "Compact the provided conversation")

	assert.Equal(t, http.StatusOK, recorder.Code)
	body := recorder.Body.Bytes()
	assert.Equal(t, "resp_1", gjson.GetBytes(body, "id").String())
	assert.Equal(t, "response.compaction", gjson.GetBytes(body, "object").String())
	assert.Equal(t, "compact summary", gjson.GetBytes(body, "output.0.content.0.text").String())
	assert.Equal(t, int64(10), gjson.GetBytes(body, "usage.input_tokens").Int())
	assert.False(t, gjson.GetBytes(body, "metadata.newapi_compaction_mode").Exists())
	assert.Equal(t, relayconstant.RelayModeResponsesCompact, info.RelayMode)
	assert.EqualValues(t, types.RelayFormatOpenAIResponsesCompaction, info.RelayFormat)
	assert.Equal(t, "/v1/responses/compact", info.RequestURLPath)
}

func mustJSONRawMessage(t *testing.T, value string) []byte {
	t.Helper()
	var raw any
	require.NoError(t, common.Unmarshal([]byte(value), &raw))
	payload, err := common.Marshal(raw)
	require.NoError(t, err)
	return payload
}
