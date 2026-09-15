package relay

import (
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
)

// shouldNormalizeOpenAITextInbound reports whether an ordinary LLM request should
// pass through NewAPI's OpenAI-compatible normalization/conversion pipeline.
// Video, image, audio, embedding and task endpoints keep their own specialized
// handlers; each handler is still responsible for keeping its client-facing
// surface OpenAI-compatible and translating only at the selected adaptor.
func shouldNormalizeOpenAITextInbound(info *relaycommon.RelayInfo) bool {
	if info == nil {
		return false
	}
	switch info.RelayFormat {
	case types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, types.RelayFormatOpenAIResponsesCompaction:
	default:
		return false
	}
	switch info.RelayMode {
	case relayconstant.RelayModeChatCompletions,
		relayconstant.RelayModeCompletions,
		relayconstant.RelayModeResponses,
		relayconstant.RelayModeResponsesCompact:
		return true
	default:
		return false
	}
}

// shouldNormalizeOpenAIImageInbound reports whether an Images API request
// should use the image adaptor pipeline. That keeps clients on OpenAI's Images
// wire format even when a global or channel pass-through switch is enabled.
// Multipart image edits are still OpenAI-format requests; the OpenAI adaptor
// preserves them by re-serializing the official form fields and files.
func shouldNormalizeOpenAIImageInbound(info *relaycommon.RelayInfo) bool {
	if info == nil || info.RelayFormat != types.RelayFormatOpenAIImage {
		return false
	}
	switch info.RelayMode {
	case relayconstant.RelayModeImagesGenerations, relayconstant.RelayModeImagesEdits:
		return true
	default:
		return false
	}
}

func shouldUseVerbatimOpenAIInboundBody(info *relaycommon.RelayInfo, globalPassThrough bool) bool {
	if shouldNormalizeOpenAITextInbound(info) || shouldNormalizeOpenAIImageInbound(info) {
		return false
	}
	return globalPassThrough || (info != nil && info.ChannelMeta != nil && info.ChannelSetting.PassThroughBodyEnabled)
}
