package relay

import (
	"testing"

	"github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
)

func TestShouldNormalizeOpenAITextInboundOnlyOrdinaryLLMEndpoints(t *testing.T) {
	tests := []struct {
		name        string
		relayFormat types.RelayFormat
		relayMode   int
		want        bool
	}{
		{
			name:        "chat completions",
			relayFormat: types.RelayFormatOpenAI,
			relayMode:   relayconstant.RelayModeChatCompletions,
			want:        true,
		},
		{
			name:        "responses",
			relayFormat: types.RelayFormatOpenAIResponses,
			relayMode:   relayconstant.RelayModeResponses,
			want:        true,
		},
		{
			name:        "responses compact",
			relayFormat: types.RelayFormatOpenAIResponsesCompaction,
			relayMode:   relayconstant.RelayModeResponsesCompact,
			want:        true,
		},
		{
			name:        "images stay out of text gateway",
			relayFormat: types.RelayFormatOpenAIImage,
			relayMode:   relayconstant.RelayModeImagesGenerations,
			want:        false,
		},
		{
			name:        "videos stay out of text gateway",
			relayFormat: types.RelayFormatTask,
			relayMode:   relayconstant.RelayModeVideoSubmit,
			want:        false,
		},
		{
			name:        "native claude route is not OpenAI inbound",
			relayFormat: types.RelayFormatClaude,
			relayMode:   relayconstant.RelayModeUnknown,
			want:        false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			info := &common.RelayInfo{
				RelayFormat: test.relayFormat,
				RelayMode:   test.relayMode,
			}
			assert.Equal(t, test.want, shouldNormalizeOpenAITextInbound(info))
		})
	}
}

func TestShouldNormalizeOpenAIImageInboundOnlyImagesAPI(t *testing.T) {
	tests := []struct {
		name        string
		relayFormat types.RelayFormat
		relayMode   int
		want        bool
	}{
		{
			name:        "image generations",
			relayFormat: types.RelayFormatOpenAIImage,
			relayMode:   relayconstant.RelayModeImagesGenerations,
			want:        true,
		},
		{
			name:        "image edits",
			relayFormat: types.RelayFormatOpenAIImage,
			relayMode:   relayconstant.RelayModeImagesEdits,
			want:        true,
		},
		{
			name:        "chat stays in text gateway",
			relayFormat: types.RelayFormatOpenAI,
			relayMode:   relayconstant.RelayModeChatCompletions,
			want:        false,
		},
		{
			name:        "video stays in task plugin protocol",
			relayFormat: types.RelayFormatTask,
			relayMode:   relayconstant.RelayModeVideoSubmit,
			want:        false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			info := &common.RelayInfo{
				RelayFormat: test.relayFormat,
				RelayMode:   test.relayMode,
			}
			assert.Equal(t, test.want, shouldNormalizeOpenAIImageInbound(info))
		})
	}
}

func TestShouldUseVerbatimOpenAIInboundBodyNeverBypassesGateway(t *testing.T) {
	info := &common.RelayInfo{
		ChannelMeta: &common.ChannelMeta{},
		RelayFormat: types.RelayFormatOpenAI,
		RelayMode:   relayconstant.RelayModeChatCompletions,
	}

	assert.False(t, shouldUseVerbatimOpenAIInboundBody(info, true))

	info.ChannelSetting.PassThroughBodyEnabled = true
	assert.False(t, shouldUseVerbatimOpenAIInboundBody(info, false))

	info.RelayFormat = types.RelayFormatOpenAIImage
	info.RelayMode = relayconstant.RelayModeImagesGenerations
	assert.False(t, shouldUseVerbatimOpenAIInboundBody(info, true))
	assert.False(t, shouldUseVerbatimOpenAIInboundBody(info, false))

	info.RelayFormat = types.RelayFormatOpenAIAudio
	info.RelayMode = relayconstant.RelayModeAudioSpeech
	assert.True(t, shouldUseVerbatimOpenAIInboundBody(info, false))
}
