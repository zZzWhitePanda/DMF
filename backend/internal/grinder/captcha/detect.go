package captcha

import (
	"strings"

	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/discord"
)

// captchaKeywords are substrings that strongly indicate a Dank Memer captcha prompt.
var captchaKeywords = []string{
	"please verify",
	"are you a real human",
	"captcha",
	"fateful",
	"verification",
	"human verification",
	"i'm not a robot",
	"please solve",
	"prove you are human",
	"please type the following",
}

// IsCaptcha returns true if the message looks like a Dank Memer captcha prompt.
func IsCaptcha(m *discord.Message) bool {
	if m.ApplicationID != discord.DankMemerAppID && m.Author.ID != discord.DankMemerAppID {
		return false
	}
	hay := strings.ToLower(m.Content)
	for _, e := range m.Embeds {
		hay += "\n" + strings.ToLower(e.Title) + "\n" + strings.ToLower(e.Description)
		for _, f := range e.Fields {
			hay += "\n" + strings.ToLower(f.Name) + "\n" + strings.ToLower(f.Value)
		}
		if e.Author != nil {
			hay += "\n" + strings.ToLower(e.Author.Name)
		}
	}
	for _, kw := range captchaKeywords {
		if strings.Contains(hay, kw) {
			return true
		}
	}
	return false
}
