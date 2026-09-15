package relay

import (
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
)

// shouldNormalizeOpenAIInbound reports whether an ordinary LLM request should
// pass through NewAPI's OpenAI-compatible normalization/conversion pipeline.
// Video, image, audio, embedding and task endpoints keep their specialized
// handlers. This keeps downstream clients on OpenAI wire formats while still
// allowing the selected channel adaptor to translate to the provider protocol.
func shouldNormalizeOpenAIInbound(info *relaycommon.RelayInfo) bool {
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

func shouldUseVerbatimOpenAIInboundBody(info *relaycommon.RelayInfo, globalPassThrough bool) bool {
	if shouldNormalizeOpenAIInbound(info) {
		return false
	}
	return globalPassThrough || (info != nil && info.ChannelSetting.PassThroughBodyEnabled)
}
