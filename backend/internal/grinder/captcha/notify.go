package captcha

import (
	"fmt"

	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/discord"
)

// Notify fires a webhook alert when a captcha is detected.
func Notify(webhookURL, mentionUserID, accountName, channelID string) error {
	if webhookURL == "" {
		return nil
	}
	content := ""
	if mentionUserID != "" {
		content = fmt.Sprintf("<@%s> ", mentionUserID)
	}
	content += fmt.Sprintf(
		":rotating_light: **CAPTCHA DETECTED** :rotating_light:\naccount: `%s`\nchannel: <#%s>\ngo solve it before the ban hammer drops.",
		accountName, channelID,
	)
	return discord.SendWebhook(webhookURL, content)
}
