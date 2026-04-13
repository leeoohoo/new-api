package openai

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

func TestIsNativeOpenAIBaseURL(t *testing.T) {
	testCases := []struct {
		name     string
		baseURL  string
		expected bool
	}{
		{
			name:     "empty base url defaults to native",
			baseURL:  "",
			expected: true,
		},
		{
			name:     "official openai host is native",
			baseURL:  "https://api.openai.com",
			expected: true,
		},
		{
			name:     "non native host is not native",
			baseURL:  "https://relay.nf.video",
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := isNativeOpenAIBaseURL(tc.baseURL)
			if got != tc.expected {
				t.Fatalf("unexpected result for %q: got %t, want %t", tc.baseURL, got, tc.expected)
			}
		})
	}
}

func TestNormalizeResponsesSystemInputsToInstructions(t *testing.T) {
	request := &dto.OpenAIResponsesRequest{
		Input: []byte(`[
			{"role":"system","content":"系统提示 A"},
			{"role":"developer","content":[{"type":"input_text","text":"开发者提示 B"}]},
			{"role":"user","content":"你好"}
		]`),
	}

	changed, err := normalizeResponsesSystemInputsToInstructions(request)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !changed {
		t.Fatalf("expected changed=true")
	}

	var instructions string
	if err := common.Unmarshal(request.Instructions, &instructions); err != nil {
		t.Fatalf("failed to unmarshal instructions: %v", err)
	}
	expectedInstructions := "系统提示 A\n\n开发者提示 B"
	if instructions != expectedInstructions {
		t.Fatalf("unexpected instructions: got %q, want %q", instructions, expectedInstructions)
	}

	var inputItems []map[string]any
	if err := common.Unmarshal(request.Input, &inputItems); err != nil {
		t.Fatalf("failed to unmarshal normalized input: %v", err)
	}
	if len(inputItems) != 1 {
		t.Fatalf("expected 1 input item after normalization, got %d", len(inputItems))
	}
	if role := common.Interface2String(inputItems[0]["role"]); role != "user" {
		t.Fatalf("expected remaining role=user, got %q", role)
	}
}

func TestNormalizeResponsesSystemInputsToInstructionsMergeExistingInstructions(t *testing.T) {
	request := &dto.OpenAIResponsesRequest{
		Input:        []byte(`[{"role":"system","content":"新增系统提示"},{"role":"user","content":"hi"}]`),
		Instructions: []byte(`"已有指令"`),
	}

	changed, err := normalizeResponsesSystemInputsToInstructions(request)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !changed {
		t.Fatalf("expected changed=true")
	}

	var instructions string
	if err := common.Unmarshal(request.Instructions, &instructions); err != nil {
		t.Fatalf("failed to unmarshal instructions: %v", err)
	}
	expectedInstructions := "已有指令\n\n新增系统提示"
	if instructions != expectedInstructions {
		t.Fatalf("unexpected merged instructions: got %q, want %q", instructions, expectedInstructions)
	}
}

func TestConvertOpenAIResponsesRequestCompatForNonNativeUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	stream := true
	request := dto.OpenAIResponsesRequest{
		Model:         "gpt-5.3-codex",
		Input:         []byte(`[{"role":"system","content":"系统规则"},{"role":"user","content":"hi"}]`),
		Stream:        &stream,
		StreamOptions: &dto.StreamOptions{IncludeUsage: true},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:    constant.ChannelTypeOpenAI,
			ChannelBaseUrl: "https://relay.nf.video",
		},
	}

	adaptor := &Adaptor{}
	convertedAny, err := adaptor.ConvertOpenAIResponsesRequest(ctx, info, request)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	converted, ok := convertedAny.(dto.OpenAIResponsesRequest)
	if !ok {
		t.Fatalf("expected dto.OpenAIResponsesRequest, got %T", convertedAny)
	}
	if converted.StreamOptions != nil {
		t.Fatalf("expected stream_options to be removed for non-native upstream")
	}

	var instructions string
	if err := common.Unmarshal(converted.Instructions, &instructions); err != nil {
		t.Fatalf("failed to unmarshal converted instructions: %v", err)
	}
	if instructions != "系统规则" {
		t.Fatalf("unexpected converted instructions: %q", instructions)
	}

	var inputItems []map[string]any
	if err := common.Unmarshal(converted.Input, &inputItems); err != nil {
		t.Fatalf("failed to unmarshal converted input: %v", err)
	}
	if len(inputItems) != 1 || common.Interface2String(inputItems[0]["role"]) != "user" {
		t.Fatalf("expected only user input after normalization, got %+v", inputItems)
	}
}

func TestConvertOpenAIRequestRemovesStreamOptionsForNonNativeUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	stream := true
	request := &dto.GeneralOpenAIRequest{
		Model:         "gpt-5.3-codex",
		Messages:      []dto.Message{{Role: "user", Content: "hi"}},
		Stream:        &stream,
		StreamOptions: &dto.StreamOptions{IncludeUsage: true},
	}

	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:    constant.ChannelTypeOpenAI,
			ChannelBaseUrl: "https://relay.nf.video",
		},
	}
	adaptor := &Adaptor{}
	convertedAny, err := adaptor.ConvertOpenAIRequest(ctx, info, request)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	converted, ok := convertedAny.(*dto.GeneralOpenAIRequest)
	if !ok {
		t.Fatalf("expected *dto.GeneralOpenAIRequest, got %T", convertedAny)
	}
	if converted.StreamOptions != nil {
		t.Fatalf("expected stream_options to be removed for non-native upstream")
	}
}
