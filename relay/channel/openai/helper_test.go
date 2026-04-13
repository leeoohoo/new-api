package openai

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

func TestHandleLastResponse_EmptyLastStreamData(t *testing.T) {
	var (
		responseId        string
		createAt          int64
		systemFingerprint string
		model             string
		containUsage      bool
		shouldSend        = true
		usage             = &dto.Usage{}
	)

	info := &relaycommon.RelayInfo{
		ShouldIncludeUsage: false,
	}

	err := handleLastResponse(
		"",
		&responseId,
		&createAt,
		&systemFingerprint,
		&model,
		&usage,
		&containUsage,
		info,
		&shouldSend,
	)
	if err != nil {
		t.Fatalf("expected nil error for empty last stream data, got %v", err)
	}
	if shouldSend {
		t.Fatalf("expected shouldSendLastResp=false when last stream data is empty")
	}
}

func TestHandleLastResponse_ValidChunkWithUsage(t *testing.T) {
	var (
		responseId        string
		createAt          int64
		systemFingerprint string
		model             string
		containUsage      bool
		shouldSend        = true
		usage             = &dto.Usage{}
	)

	info := &relaycommon.RelayInfo{
		ShouldIncludeUsage: false,
	}

	lastStreamData := `{
		"id":"chatcmpl-test",
		"object":"chat.completion.chunk",
		"created":1776044447,
		"model":"MiniMax-M2.7",
		"choices":[
			{
				"index":0,
				"delta":{"content":"hi"},
				"finish_reason":null
			}
		],
		"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}
	}`

	err := handleLastResponse(
		lastStreamData,
		&responseId,
		&createAt,
		&systemFingerprint,
		&model,
		&usage,
		&containUsage,
		info,
		&shouldSend,
	)
	if err != nil {
		t.Fatalf("expected nil error for valid last stream data, got %v", err)
	}
	if responseId != "chatcmpl-test" {
		t.Fatalf("unexpected response id: %s", responseId)
	}
	if createAt != 1776044447 {
		t.Fatalf("unexpected created timestamp: %d", createAt)
	}
	if model != "MiniMax-M2.7" {
		t.Fatalf("unexpected model: %s", model)
	}
	if !containUsage {
		t.Fatalf("expected containStreamUsage=true when chunk carries usage")
	}
	if usage == nil || usage.TotalTokens != 3 {
		t.Fatalf("expected usage total tokens=3, got %+v", usage)
	}
	if !shouldSend {
		t.Fatalf("expected shouldSendLastResp=true for valid content chunk")
	}
}

func TestNormalizeMiniMaxStreamResponse_MessageContentFallback(t *testing.T) {
	data := `{
		"id":"chatcmpl-mm-1",
		"object":"chat.completion",
		"created":1776044447,
		"model":"MiniMax-M2.7",
		"choices":[
			{
				"index":0,
				"finish_reason":"stop",
				"message":{
					"role":"assistant",
					"content":"好",
					"reasoning_content":"internal reasoning"
				}
			}
		],
		"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}
	}`

	resp, err := normalizeMiniMaxStreamResponse(data)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if resp.Object != "chat.completion.chunk" {
		t.Fatalf("expected object normalized to chat.completion.chunk, got %s", resp.Object)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
	}
	if got := resp.Choices[0].Delta.GetContentString(); got != "好" {
		t.Fatalf("expected delta content from message content, got %q", got)
	}
	if got := resp.Choices[0].Delta.GetReasoningContent(); got != "internal reasoning" {
		t.Fatalf("expected reasoning content from message reasoning_content, got %q", got)
	}
	if resp.Choices[0].FinishReason != nil {
		t.Fatalf("expected finish_reason to be cleared when content exists")
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 3 {
		t.Fatalf("expected usage preserved, got %+v", resp.Usage)
	}
}

func TestNormalizeMiniMaxStreamResponse_KeepFinishReasonWithoutContent(t *testing.T) {
	data := `{
		"id":"chatcmpl-mm-2",
		"object":"chat.completion.chunk",
		"created":1776044447,
		"model":"MiniMax-M2.7",
		"choices":[
			{
				"index":0,
				"finish_reason":"stop",
				"delta":{"content":""}
			}
		]
	}`

	resp, err := normalizeMiniMaxStreamResponse(data)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
	}
	if resp.Choices[0].FinishReason == nil || *resp.Choices[0].FinishReason != "stop" {
		t.Fatalf("expected finish_reason=stop to be preserved when content is empty")
	}
}
