package discord

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// APIBase is the Discord REST base URL.
const APIBase = "https://discord.com/api/v10"

// RESTClient wraps authenticated HTTP calls to the Discord REST API.
type RESTClient struct {
	token      string
	http       *http.Client
	userAgent  string
	xSuperProp string

	mu       sync.Mutex
	cmdCache map[string][]ApplicationCommand // keyed by channelID
}

// NewRESTClient builds a REST client. proxy may be empty.
func NewRESTClient(token, proxy string) (*RESTClient, error) {
	transport := &http.Transport{}
	if proxy != "" {
		pu, err := url.Parse(proxy)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy: %w", err)
		}
		transport.Proxy = http.ProxyURL(pu)
	}
	return &RESTClient{
		token:      token,
		http:       &http.Client{Transport: transport, Timeout: 30 * time.Second},
		userAgent:  "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		xSuperProp: DefaultXSuperProperties(),
		cmdCache:   make(map[string][]ApplicationCommand),
	}, nil
}

// do issues an authenticated request. Honors 429 with a single retry.
func (c *RESTClient) do(method, path string, body any) (*http.Response, []byte, error) {
	var buf io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, nil, err
		}
		buf = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, APIBase+path, buf)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("X-Super-Properties", c.xSuperProp)
	req.Header.Set("X-Discord-Locale", "en-US")
	req.Header.Set("Origin", "https://discord.com")
	req.Header.Set("Referer", "https://discord.com/channels/@me")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode == 429 {
		var r struct {
			RetryAfter float64 `json:"retry_after"`
		}
		_ = json.Unmarshal(data, &r)
		wait := time.Duration(r.RetryAfter * float64(time.Second))
		if wait <= 0 || wait > 60*time.Second {
			wait = 5 * time.Second
		}
		time.Sleep(wait)
		return c.do(method, path, body)
	}
	return resp, data, nil
}

// FetchGuildCommands returns application commands registered in the guild for the given app.
// Uses the guild-wide application-command-index endpoint which returns the full set
// without requiring per-user lazy indexing (unlike the per-channel search endpoint).
// Cached per (guildID, applicationID).
func (c *RESTClient) FetchGuildCommands(guildID, applicationID string) ([]ApplicationCommand, error) {
	key := guildID + ":" + applicationID
	c.mu.Lock()
	if cached, ok := c.cmdCache[key]; ok {
		c.mu.Unlock()
		return cached, nil
	}
	c.mu.Unlock()

	resp, data, err := c.do("GET",
		fmt.Sprintf("/guilds/%s/application-command-index", guildID),
		nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("guild app cmd index: %d: %s", resp.StatusCode, string(data))
	}
	var out CommandSearchResult
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	// Filter to just the target application.
	filtered := make([]ApplicationCommand, 0, 32)
	for _, cmd := range out.ApplicationCommands {
		if cmd.ApplicationID == applicationID {
			filtered = append(filtered, cmd)
		}
	}
	c.mu.Lock()
	c.cmdCache[key] = filtered
	c.mu.Unlock()
	return filtered, nil
}

// InvalidateCommandCache clears the slash command cache (useful after a long run).
func (c *RESTClient) InvalidateCommandCache() {
	c.mu.Lock()
	c.cmdCache = make(map[string][]ApplicationCommand)
	c.mu.Unlock()
}

// SendInteraction posts an interaction (slash command or component).
func (c *RESTClient) SendInteraction(payload any) error {
	resp, data, err := c.do("POST", "/interactions", payload)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("interaction: %d: %s", resp.StatusCode, string(data))
	}
	return nil
}

// SendMessage posts a plain-text message to a channel. Used by the random-event
// responder to type phrases like "zero rizz" / "i pledge my life to dank memer".
func (c *RESTClient) SendMessage(channelID, content string) error {
	body := map[string]interface{}{"content": content}
	resp, data, err := c.do("POST", fmt.Sprintf("/channels/%s/messages", channelID), body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("send message: HTTP %d: %s", resp.StatusCode, string(data))
	}
	return nil
}

// GetChannelMessages fetches the most recent `limit` messages in a channel.
// Used by the stuck-detector to resync when nothing's been coming through
// the gateway for a while.
func (c *RESTClient) GetChannelMessages(channelID string, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	resp, data, err := c.do("GET", fmt.Sprintf("/channels/%s/messages?limit=%d", channelID, limit), nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get messages: HTTP %d: %s", resp.StatusCode, string(data))
	}
	var msgs []Message
	if err := json.Unmarshal(data, &msgs); err != nil {
		return nil, fmt.Errorf("parse messages: %w", err)
	}
	return msgs, nil
}

// DeleteMessage deletes a message by ID. Used to auto-dismiss ephemeral
// confirmation / success popups so the user's chat doesn't fill up.
// Returns 204 on success; 403/404 (common for ephemeral) returned as error.
func (c *RESTClient) DeleteMessage(channelID, messageID string) error {
	resp, data, err := c.do("DELETE", fmt.Sprintf("/channels/%s/messages/%s", channelID, messageID), nil)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("delete msg: HTTP %d: %s", resp.StatusCode, string(data))
	}
	return nil
}

// SendTyping sends a typing indicator to the channel.
func (c *RESTClient) SendTyping(channelID string) error {
	resp, data, err := c.do("POST", fmt.Sprintf("/channels/%s/typing", channelID), nil)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("typing: %d: %s", resp.StatusCode, string(data))
	}
	return nil
}

// SendWebhook posts a plain content payload to an arbitrary webhook URL.
func SendWebhook(webhookURL, content string) error {
	body, _ := json.Marshal(map[string]string{"content": content})
	req, err := http.NewRequest("POST", webhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook: %d", resp.StatusCode)
	}
	return nil
}

// GenerateNonce creates a Discord-style snowflake nonce from the current time.
func GenerateNonce() string {
	const discordEpoch = int64(1420070400000)
	ms := time.Now().UnixMilli() - discordEpoch
	return strconv.FormatInt(ms<<22, 10)
}

// DefaultXSuperProperties returns a base64 x-super-properties header mimicking the Discord web client.
// If Discord rejects this (401/invalid), update client_build_number and the UA to current values.
func DefaultXSuperProperties() string {
	props := map[string]interface{}{
		"os":                       "Windows",
		"browser":                  "Chrome",
		"device":                   "",
		"system_locale":            "en-US",
		"browser_user_agent":       "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"browser_version":          "120.0.0.0",
		"os_version":               "10",
		"referrer":                 "",
		"referring_domain":         "",
		"referrer_current":         "",
		"referring_domain_current": "",
		"release_channel":          "stable",
		"client_build_number":      260292,
		"client_event_source":      nil,
	}
	b, _ := json.Marshal(props)
	return base64.StdEncoding.EncodeToString(b)
}
