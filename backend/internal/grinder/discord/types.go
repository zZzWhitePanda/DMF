package discord

import "encoding/json"

// Gateway opcodes.
const (
	OpDispatch            = 0
	OpHeartbeat           = 1
	OpIdentify            = 2
	OpPresenceUpdate      = 3
	OpVoiceStateUpdate    = 4
	OpResume              = 6
	OpReconnect           = 7
	OpRequestGuildMembers = 8
	OpInvalidSession      = 9
	OpHello               = 10
	OpHeartbeatAck        = 11
)

// Interaction types.
const (
	InteractionTypePing             = 1
	InteractionTypeApplicationCmd   = 2
	InteractionTypeMessageComponent = 3
	InteractionTypeAutocomplete     = 4
	InteractionTypeModalSubmit      = 5
)

// Component types.
const (
	ComponentActionRow  = 1
	ComponentButton     = 2
	ComponentSelectMenu = 3
	ComponentTextInput  = 4
)

// DankMemerAppID is Dank Memer's Discord application id.
const DankMemerAppID = "270904126974590976"

// GatewayEvent is the common wrapper for all WS frames.
type GatewayEvent struct {
	Op int             `json:"op"`
	S  *int            `json:"s,omitempty"`
	T  string          `json:"t,omitempty"`
	D  json.RawMessage `json:"d,omitempty"`
}

// Hello is the op=10 payload.
type Hello struct {
	HeartbeatInterval int `json:"heartbeat_interval"`
}

// Ready is dispatched after a successful identify.
type Ready struct {
	V         int    `json:"v"`
	SessionID string `json:"session_id"`
	ResumeURL string `json:"resume_gateway_url"`
	User      User   `json:"user"`
}

type User struct {
	ID            string `json:"id"`
	Username      string `json:"username"`
	Discriminator string `json:"discriminator,omitempty"`
	GlobalName    string `json:"global_name,omitempty"`
	Bot           bool   `json:"bot,omitempty"`
}

type Message struct {
	ID                string             `json:"id"`
	ChannelID         string             `json:"channel_id"`
	GuildID           string             `json:"guild_id,omitempty"`
	Author            User               `json:"author"`
	Content           string             `json:"content"`
	Timestamp         string             `json:"timestamp"`
	EditedTimestamp   string             `json:"edited_timestamp,omitempty"`
	Embeds            []Embed            `json:"embeds,omitempty"`
	Components        []MessageComponent `json:"components,omitempty"`
	Attachments       []Attachment       `json:"attachments,omitempty"`
	Flags             int                `json:"flags,omitempty"`
	Nonce             string             `json:"nonce,omitempty"`
	MessageReference  *MessageReference  `json:"message_reference,omitempty"`
	Interaction       *InteractionRef    `json:"interaction,omitempty"`
	ReferencedMessage *Message           `json:"referenced_message,omitempty"`
	Mentions          []User             `json:"mentions,omitempty"`
	MentionRoles      []string           `json:"mention_roles,omitempty"`
	ApplicationID     string             `json:"application_id,omitempty"`
}

type Attachment struct {
	ID       string `json:"id"`
	Filename string `json:"filename,omitempty"`
	URL      string `json:"url,omitempty"`
	ProxyURL string `json:"proxy_url,omitempty"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
}

type MessageReference struct {
	MessageID string `json:"message_id,omitempty"`
	ChannelID string `json:"channel_id,omitempty"`
	GuildID   string `json:"guild_id,omitempty"`
}

type InteractionRef struct {
	ID   string `json:"id"`
	Type int    `json:"type"`
	Name string `json:"name"`
	User User   `json:"user"`
}

type Embed struct {
	Title       string       `json:"title,omitempty"`
	Type        string       `json:"type,omitempty"`
	Description string       `json:"description,omitempty"`
	URL         string       `json:"url,omitempty"`
	Timestamp   string       `json:"timestamp,omitempty"`
	Color       int          `json:"color,omitempty"`
	Footer      *EmbedFooter `json:"footer,omitempty"`
	Author      *EmbedAuthor `json:"author,omitempty"`
	Fields      []EmbedField `json:"fields,omitempty"`
	Image       *EmbedMedia  `json:"image,omitempty"`
	Thumbnail   *EmbedMedia  `json:"thumbnail,omitempty"`
}

type EmbedFooter struct {
	Text    string `json:"text"`
	IconURL string `json:"icon_url,omitempty"`
}

type EmbedAuthor struct {
	Name    string `json:"name,omitempty"`
	IconURL string `json:"icon_url,omitempty"`
	URL     string `json:"url,omitempty"`
}

type EmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

type EmbedMedia struct {
	URL string `json:"url,omitempty"`
}

type MessageComponent struct {
	Type       int                `json:"type"`
	ID         int                `json:"id,omitempty"`
	Components []MessageComponent `json:"components,omitempty"`

	// Button fields (V1)
	Style    int    `json:"style,omitempty"`
	Label    string `json:"label,omitempty"`
	Emoji    *Emoji `json:"emoji,omitempty"`
	CustomID string `json:"custom_id,omitempty"`
	URL      string `json:"url,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`

	// Select menu fields (V1)
	Placeholder string         `json:"placeholder,omitempty"`
	MinValues   int            `json:"min_values,omitempty"`
	MaxValues   int            `json:"max_values,omitempty"`
	Options     []SelectOption `json:"options,omitempty"`

	// Components V2 fields.
	Content     string             `json:"content,omitempty"`      // TextDisplay (type 10)
	Accessory   *MessageComponent  `json:"accessory,omitempty"`    // Section (type 9)
	AccentColor int                `json:"accent_color,omitempty"` // Container (type 17)
	Spoiler     bool               `json:"spoiler,omitempty"`
	Divider     bool               `json:"divider,omitempty"` // Separator (type 14)
	Spacing     int                `json:"spacing,omitempty"`
	Items       []MediaGalleryItem `json:"items,omitempty"` // MediaGallery (type 12)
	Media       *UnfurledMedia     `json:"media,omitempty"` // Thumbnail (type 11), File (type 13)
}

// MediaGalleryItem is one entry inside a MediaGallery (type 12) component.
type MediaGalleryItem struct {
	Media       UnfurledMedia `json:"media"`
	Description string        `json:"description,omitempty"`
	Spoiler     bool          `json:"spoiler,omitempty"`
}

// UnfurledMedia holds a resolvable URL + metadata for V2 image/file components.
type UnfurledMedia struct {
	URL         string `json:"url"`
	ProxyURL    string `json:"proxy_url,omitempty"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
	ContentType string `json:"content_type,omitempty"`
}

type Emoji struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	Animated bool   `json:"animated,omitempty"`
}

type SelectOption struct {
	Label       string `json:"label"`
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
	Emoji       *Emoji `json:"emoji,omitempty"`
	Default     bool   `json:"default,omitempty"`
}

// ApplicationCommand describes a slash command fetched from the search endpoint.
type ApplicationCommand struct {
	ID                   string                     `json:"id"`
	ApplicationID        string                     `json:"application_id"`
	Version              string                     `json:"version"`
	Type                 int                        `json:"type"`
	Name                 string                     `json:"name"`
	NameLocalized        string                     `json:"name_localized,omitempty"`
	Description          string                     `json:"description,omitempty"`
	DescriptionLocalized string                     `json:"description_localized,omitempty"`
	DMPermission         bool                       `json:"dm_permission,omitempty"`
	ContextIDs           []int                      `json:"contexts,omitempty"`
	IntegrationTypes     []int                      `json:"integration_types,omitempty"`
	GlobalPopularityRank int                        `json:"global_popularity_rank,omitempty"`
	Options              []ApplicationCommandOption `json:"options,omitempty"`
}

type ApplicationCommandOption struct {
	Type        int                        `json:"type"`
	Name        string                     `json:"name"`
	Description string                     `json:"description,omitempty"`
	Required    bool                       `json:"required,omitempty"`
	Choices     []ApplicationCommandChoice `json:"choices,omitempty"`
	Options     []ApplicationCommandOption `json:"options,omitempty"`
}

type ApplicationCommandChoice struct {
	Name  string      `json:"name"`
	Value interface{} `json:"value"`
}

// CommandSearchResult is the response of /channels/{id}/application-commands/search.
type CommandSearchResult struct {
	ApplicationCommands []ApplicationCommand `json:"application_commands"`
	Applications        []Application        `json:"applications"`
	Cursor              Cursor               `json:"cursor"`
}

type Application struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	BotID string `json:"bot,omitempty"`
}

type Cursor struct {
	Previous string `json:"previous"`
	Next     string `json:"next"`
	Repaired bool   `json:"repaired"`
}
