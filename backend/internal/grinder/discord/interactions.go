package discord

import "encoding/json"

// FindCommand returns the application command descriptor by name.
func FindCommand(cmds []ApplicationCommand, name string) *ApplicationCommand {
	for i := range cmds {
		if cmds[i].Name == name {
			return &cmds[i]
		}
	}
	return nil
}

// BuildSlashCommand builds an interaction payload for an application command.
// options is a list of {"type": N, "name": "...", "value": ...} maps.
func BuildSlashCommand(cmd *ApplicationCommand, guildID, channelID, sessionID string, options []map[string]interface{}) map[string]interface{} {
	if options == nil {
		options = []map[string]interface{}{}
	}
	rawAppCmd, _ := json.Marshal(cmd)
	var appCmdMap map[string]interface{}
	_ = json.Unmarshal(rawAppCmd, &appCmdMap)
	return map[string]interface{}{
		"type":           InteractionTypeApplicationCmd,
		"application_id": cmd.ApplicationID,
		"guild_id":       guildID,
		"channel_id":     channelID,
		"session_id":     sessionID,
		"data": map[string]interface{}{
			"version":             cmd.Version,
			"id":                  cmd.ID,
			"name":                cmd.Name,
			"type":                cmd.Type,
			"options":             options,
			"application_command": appCmdMap,
			"attachments":         []interface{}{},
		},
		"nonce":              GenerateNonce(),
		"analytics_location": "slash_ui",
	}
}

// BuildButtonClick builds an interaction payload for clicking a message button.
func BuildButtonClick(appID, guildID, channelID, sessionID, messageID, customID string) map[string]interface{} {
	return map[string]interface{}{
		"type":           InteractionTypeMessageComponent,
		"application_id": appID,
		"guild_id":       guildID,
		"channel_id":     channelID,
		"session_id":     sessionID,
		"message_flags":  0,
		"message_id":     messageID,
		"data": map[string]interface{}{
			"component_type": ComponentButton,
			"custom_id":      customID,
		},
		"nonce": GenerateNonce(),
	}
}

// BuildSelectMenu builds an interaction payload for a select menu choice.
func BuildSelectMenu(appID, guildID, channelID, sessionID, messageID, customID string, values []string) map[string]interface{} {
	return map[string]interface{}{
		"type":           InteractionTypeMessageComponent,
		"application_id": appID,
		"guild_id":       guildID,
		"channel_id":     channelID,
		"session_id":     sessionID,
		"message_flags":  0,
		"message_id":     messageID,
		"data": map[string]interface{}{
			"component_type": ComponentSelectMenu,
			"custom_id":      customID,
			"type":           ComponentSelectMenu,
			"values":         values,
		},
		"nonce": GenerateNonce(),
	}
}

// FindSelect walks a component tree for the first enabled select menu.
// Handles V2 Section.Accessory and nested Container.components.
func FindSelect(components []MessageComponent) *MessageComponent {
	for i := range components {
		c := &components[i]
		if c.Type == ComponentSelectMenu && !c.Disabled {
			return c
		}
		if c.Accessory != nil && c.Accessory.Type == ComponentSelectMenu && !c.Accessory.Disabled {
			return c.Accessory
		}
		if len(c.Components) > 0 {
			if found := FindSelect(c.Components); found != nil {
				return found
			}
		}
	}
	return nil
}

// CollectButtons returns every enabled button with a custom_id in a component tree.
// Handles V2 Section.Accessory and nested Container.components.
func CollectButtons(cs []MessageComponent) []*MessageComponent {
	var out []*MessageComponent
	var walk func([]MessageComponent)
	walk = func(cs []MessageComponent) {
		for i := range cs {
			c := &cs[i]
			if c.Type == ComponentButton && !c.Disabled && c.CustomID != "" {
				out = append(out, c)
			}
			if c.Accessory != nil && c.Accessory.Type == ComponentButton &&
				!c.Accessory.Disabled && c.Accessory.CustomID != "" {
				out = append(out, c.Accessory)
			}
			if len(c.Components) > 0 {
				walk(c.Components)
			}
		}
	}
	walk(cs)
	return out
}

// CollectText walks the component tree and returns all TextDisplay content strings.
// This is how Components V2 messages carry their visible text (instead of embeds).
func CollectText(cs []MessageComponent) []string {
	var out []string
	var walk func([]MessageComponent)
	walk = func(cs []MessageComponent) {
		for i := range cs {
			c := &cs[i]
			if c.Type == 10 && c.Content != "" { // TextDisplay
				out = append(out, c.Content)
			}
			if c.Accessory != nil {
				walk([]MessageComponent{*c.Accessory})
			}
			if len(c.Components) > 0 {
				walk(c.Components)
			}
		}
	}
	walk(cs)
	return out
}

// CollectImageURLs walks the component tree for V2 image sources —
// MediaGallery items (type 12), Thumbnails (type 11), Files (type 13).
func CollectImageURLs(cs []MessageComponent) []string {
	var out []string
	var walk func([]MessageComponent)
	walk = func(cs []MessageComponent) {
		for i := range cs {
			c := &cs[i]
			if c.Media != nil && c.Media.URL != "" {
				out = append(out, c.Media.URL)
			}
			for _, item := range c.Items {
				if item.Media.URL != "" {
					out = append(out, item.Media.URL)
				}
			}
			if c.Accessory != nil {
				walk([]MessageComponent{*c.Accessory})
			}
			if len(c.Components) > 0 {
				walk(c.Components)
			}
		}
	}
	walk(cs)
	return out
}
