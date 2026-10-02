package boot

import "reasonix/internal/config"

func appendCorePolicies(prompt string) string {
	// 20261002 提示词调研：UserCommunicationPolicy 插在 Autonomy 与
	// CompletionReport 之间——先「怎么把话说清」，再「收尾报告格式」。
	for _, policy := range []string{config.UserDecisionPolicy, config.WorkPracticePolicy, config.TodoUpdatePolicy, config.AutonomyPolicy, config.UserCommunicationPolicy, config.CompletionReportPolicy, config.LanguagePolicy, config.ContextManagementPolicy} {
		prompt += "\n\n" + policy
	}
	return prompt
}

func appendOfflineEnvironmentNote(prompt string, offline bool) string {
	if offline {
		prompt += "\n\n" + config.OfflineEnvironmentNote
	}
	return prompt
}
