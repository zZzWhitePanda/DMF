package instance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	nethttp "net/http"
	neturl "net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/captcha"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/config"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/discord"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/events"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/humanize"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/parser"
)

// Command is a pluggable command executor.
type Command interface {
	Name() string
	DefaultCooldown() time.Duration
	// Run invokes the command. Returns the duration before the next invocation can run (cooldown).
	Run(ctx context.Context, inst *Instance) (time.Duration, error)
	// OnResponse is called when a Dank Memer message arrives that may be a response.
	OnResponse(ctx context.Context, inst *Instance, msg *discord.Message) error
}

// Instance is a running account.
type Instance struct {
	Name       string
	Token      string
	Proxy      string
	GuildID    string
	ChannelID  string
	IsMaster   bool
	MasterUser string

	Cluster *config.Cluster
	Cfg     *config.Config
	Rest    *discord.RESTClient
	Gateway *discord.Gateway
	Bus     *events.Bus

	SelfUserID string

	State    *State
	Commands []Command
	Log      *log.Entry

	pendingCmd Command
	pMu        sync.Mutex

	// Run's context. OnResponse gets it so a handler blocked on the user
	// is released when the farm stops.
	runCtx context.Context

	// Last time we received any message from DM in our channel. The stuck
	// detector uses this to decide when to resync.
	activityMu   sync.Mutex
	lastActivity time.Time
}

func (i *Instance) markActivity() {
	i.activityMu.Lock()
	i.lastActivity = time.Now()
	i.activityMu.Unlock()
}

func (i *Instance) sinceLastActivity() time.Duration {
	i.activityMu.Lock()
	defer i.activityMu.Unlock()
	if i.lastActivity.IsZero() {
		return 0
	}
	return time.Since(i.lastActivity)
}

// New constructs an Instance for the given account inside a cluster.
// If config.Features.Proxies has entries, probes up to 100 random proxies
// in parallel and picks the first one that responds — picking a dead proxy
// at random was leaving the gateway offline forever on big free-proxy pools.
func New(cfg *config.Config, cl *config.Cluster, inst *config.Instance, isMaster bool, logger *log.Entry, bus *events.Bus) (*Instance, error) {
	proxy := strings.TrimSpace(inst.Proxy)
	if len(cfg.Features.Proxies) > 0 {
		pool := make([]string, 0, len(cfg.Features.Proxies))
		for _, p := range cfg.Features.Proxies {
			if p = strings.TrimSpace(p); p != "" {
				pool = append(pool, p)
			}
		}
		if len(pool) > 0 {
			bus.Send(events.Event{Kind: events.Info, Message: fmt.Sprintf("testing proxies (pool: %d, probing up to 100 in parallel, 15s cap)...", len(pool))})
			if working := pickWorkingProxy(pool); working != "" {
				proxy = working
				bus.Send(events.Event{Kind: events.Success, Message: "working proxy picked: " + maskProxy(proxy)})
				logger.WithField("proxy", maskProxy(proxy)).Info("using proxy from pool")
			} else {
				bus.Send(events.Event{Kind: events.Warn, Message: "no proxy in pool responded within 15s - connecting direct"})
				proxy = ""
			}
		}
	}
	rest, err := discord.NewRESTClient(inst.Token, proxy)
	if err != nil {
		return nil, err
	}
	gw := discord.NewGateway(inst.Token, proxy, logger)
	return &Instance{
		Name:      inst.Name,
		Token:     inst.Token,
		Proxy:     proxy,
		GuildID:   cl.GuildID,
		ChannelID: cl.ChannelID,
		IsMaster:  isMaster,
		Cluster:   cl,
		Cfg:       cfg,
		Rest:      rest,
		Gateway:   gw,
		Bus:       bus,
		State:     NewState(),
		Log:       logger,
	}, nil
}

// eventPhraseRE captures the phrase inside quotes in a DM random event.
// Example matches:
//   First to say "ZERO RIZZ" wins!            → "ZERO RIZZ"
//   first to say "I PLEDGE MY LIFE TO ..."   → "I PLEDGE MY LIFE TO ..."
// Handles ASCII and Unicode (fancy) quote marks.
var eventPhraseRE = regexp.MustCompile(`(?i)first to say\s*["'` + "`" + `“”‘’]([^"'` + "`" + `“”‘’]+)["'` + "`" + `“”‘’]`)

// dispatchRandomEvents runs the full random-event handler pipeline for a
// single message. Extracted from onMessage so the startup backfill can
// re-use the exact same logic (role-ping gate, per-handler toggle,
// dedup, text/button action dispatch, diagnostic warning when the gate
// blocks a matching detector). Caller is responsible for the master
// RandomEvents enabled check + pause check — this method just does the
// per-message work.
func (i *Instance) dispatchRandomEvents(m *discord.Message) {
	if i.State.HasHandledRandomEvent(m.ID) {
		return
	}
	roleID := i.Cfg.Features.RandomEventRoleID
	rolePinged := isEventRolePinged(m, roleID)
	// Gate: if role ID is set, require the ping. Otherwise fall through
	// on text-match only (users without a configured role ID aren't
	// affected by the gate).
	if roleID != "" && !rolePinged {
		// Role gate blocked dispatch. Check whether any detector WOULD
		// have matched, and if so log a visible warning — this surfaces
		// the common case of the role ID being misconfigured OR
		// Discord's mention_roles field not populating for a legit
		// event ping (webhook sends, etc.). Without this the user just
		// sees silence.
		for _, h := range randomEventHandlers {
			if !randomEventHandlerEnabled(i.Cfg.Features.RandomEventHandlers, h.Key) {
				continue
			}
			if h.Detect(m) != nil {
				i.Bus.Send(events.Event{
					Kind:    events.Warn,
					Message: fmt.Sprintf("%s event matched but Event Role not pinged on this message — double-check Event Role ID in Settings → Values (role %q)", h.Name, roleID),
				})
				break
			}
		}
		return
	}

	handled := false
	for _, h := range randomEventHandlers {
		if !randomEventHandlerEnabled(i.Cfg.Features.RandomEventHandlers, h.Key) {
			continue
		}
		action := h.Detect(m)
		if action == nil {
			continue
		}
		handled = true
		i.State.MarkRandomEventHandled(m.ID)
		switch {
		case len(action.Texts) > 0:
			go func(ch string, texts []string, name string) {
				for idx, resp := range texts {
					if idx == 0 {
						time.Sleep(time.Duration(200+rand.Intn(600)) * time.Millisecond)
					} else {
						time.Sleep(time.Duration(700+rand.Intn(600)) * time.Millisecond)
					}
					if err := i.Rest.SendMessage(ch, resp); err != nil {
						i.Bus.Send(events.Event{Kind: events.Warn, Message: fmt.Sprintf("%s event send failed: %v", name, err)})
						return
					}
					i.Bus.Send(events.Event{Kind: events.Success, Message: fmt.Sprintf("%s event: typed %q", name, resp)})
				}
			}(i.ChannelID, action.Texts, h.Name)
		case action.ButtonCustomID != "":
			msgID := m.ID
			go func(mid, custom, name string) {
				time.Sleep(time.Duration(200+rand.Intn(600)) * time.Millisecond)
				if err := i.ClickButton(mid, custom); err != nil {
					i.Bus.Send(events.Event{Kind: events.Warn, Message: fmt.Sprintf("%s event click failed: %v", name, err)})
				} else {
					i.Bus.Send(events.Event{Kind: events.Success, Message: fmt.Sprintf("%s event: clicked target button", name)})
				}
			}(msgID, action.ButtonCustomID, h.Name)
		}
		break
	}
	if !handled && rolePinged {
		// No handler produced an action. Two possibilities:
		//   a) Unknown event type we haven't written a detector for.
		//   b) Known event type whose detector matched the title but
		//      couldn't extract the phrase/target (phrase bundled with
		//      boilerplate, grid layout changed, button labels differ,
		//      etc.) — these are the ones we actually can fix.
		// Try identifying case (b) by scanning the message text for the
		// well-known event titles. If any match, surface a more
		// actionable warning naming the event + the first ~160 chars of
		// text so we can see what shape DM is sending.
		eventName := identifyEventByTitle(m)
		if eventName != "" {
			snippet := eventTextSnippet(m, 160)
			i.Bus.Send(events.Event{
				Kind:    events.Warn,
				Message: fmt.Sprintf("%s event detected (role ping) but handler couldn't extract target — text seen: %q", eventName, snippet),
			})
		} else {
			i.Bus.Send(events.Event{
				Kind:    events.Warn,
				Message: "random event detected (role ping) but no handler matched — screenshot it so we can add a detector",
			})
		}
	}
}

// backfillRecentEvents scans the channel's last few DM messages at
// startup and dispatches any random event handlers that match. Lets us
// catch events that started while the bot was offline — bounded to a
// 5-minute window so we never respond to events that have long since
// ended, and to the newest ~5 messages so we don't walk deep history.
//
// Called once per Instance.Run from a goroutine after READY + command
// prefetch, so it runs in parallel with the main grind loop starting.
// Dedup state (handledRandomEvents) prevents a double-fire if the real
// MESSAGE_CREATE for the same event arrives via the gateway shortly
// after we backfill it.
func (i *Instance) backfillRecentEvents(ctx context.Context) {
	if !i.Cfg.Features.RandomEvents {
		return
	}
	msgs, err := i.Rest.GetChannelMessages(i.ChannelID, 5)
	if err != nil {
		i.Log.WithError(err).Debug("startup event backfill fetch failed")
		return
	}
	cutoff := time.Now().Add(-5 * time.Minute)
	scanned := 0
	for _, m := range msgs {
		if ctx.Err() != nil {
			return
		}
		if m.Author.ID != discord.DankMemerAppID && m.ApplicationID != discord.DankMemerAppID {
			continue
		}
		// Age filter: Discord timestamps are RFC3339-ish with fractional
		// seconds. Try RFC3339Nano first (handles microseconds), fall
		// back to plain RFC3339 if that fails. If both fail we skip the
		// age check and process anyway — a parse error on a legit DM
		// message shouldn't block event handling.
		if m.Timestamp != "" {
			t, err := time.Parse(time.RFC3339Nano, m.Timestamp)
			if err != nil {
				t, err = time.Parse(time.RFC3339, m.Timestamp)
			}
			if err == nil && t.Before(cutoff) {
				continue
			}
		}
		scanned++
		i.dispatchRandomEvents(&m)
	}
	if scanned > 0 {
		i.Bus.Send(events.Event{
			Kind:    events.Info,
			Message: fmt.Sprintf("startup backfill: scanned %d recent DM messages for active random events", scanned),
		})
	}
}

// identifyEventByTitle returns the display name of a known Dank Memer
// random event if its title or body text is found anywhere in the
// message, or "" if none match. Used by the diagnostic warning in
// dispatchRandomEvents to surface "we see the event, our handler just
// can't extract its target" cases distinctly from unknown events.
func identifyEventByTitle(m *discord.Message) string {
	hay := strings.ToLower(m.Content)
	for _, e := range m.Embeds {
		hay += " " + strings.ToLower(e.Title) + " " + strings.ToLower(e.Description)
		for _, f := range e.Fields {
			hay += " " + strings.ToLower(f.Name) + " " + strings.ToLower(f.Value)
		}
	}
	for _, t := range discord.CollectText(m.Components) {
		hay += " " + strings.ToLower(t)
	}
	// Order matters: more-specific matches first so "reverse reverse"
	// isn't shadowed by anything else.
	checks := []struct{ needle, name string }{
		{"reverse reverse", "Reverse Reverse"},
		{"typed in reverse", "Reverse Reverse"},
		{"fortnite dance mode", "Fortnite Dance Mode"},
		{"fornite dance mode", "Fortnite Dance Mode"},
		{"corresponding direction", "Fortnite Dance Mode"},
		{"punch pepe", "Punch Pepe"},
		{"pepeboxer", "Punch Pepe"},
		{"steal mel's beard", "Steal Mel's Beard"},
		{"steal mels beard", "Steal Mel's Beard"},
		{"mel's beard has been spotted", "Steal Mel's Beard"},
		{"dank scrambled eggs", "Dank Scrambled Eggs"},
		{"unscramble the given phrase", "Dank Scrambled Eggs"},
		{"trivia night", "Trivia Night"},
		{"it's trivia night", "Trivia Night"},
		{"first to say", "First to Say"},
	}
	for _, c := range checks {
		if strings.Contains(hay, c.needle) {
			return c.name
		}
	}
	return ""
}

// eventTextSnippet returns the first `n` chars of the message's combined
// text (content + embed title/description + TextDisplay nodes) collapsed
// to single-space whitespace, for use in diagnostic log lines.
func eventTextSnippet(m *discord.Message, n int) string {
	var sb strings.Builder
	sb.WriteString(m.Content)
	for _, e := range m.Embeds {
		if e.Title != "" {
			sb.WriteString(" | ")
			sb.WriteString(e.Title)
		}
		if e.Description != "" {
			sb.WriteString(" | ")
			sb.WriteString(e.Description)
		}
	}
	for _, t := range discord.CollectText(m.Components) {
		sb.WriteString(" | ")
		sb.WriteString(t)
	}
	s := strings.Join(strings.Fields(sb.String()), " ")
	if len(s) > n {
		s = s[:n] + "..."
	}
	return s
}

// isEventRolePinged reports whether this message pings the configured
// Event Role. Scans every text location DM might stash the mention in,
// because Discord doesn't consistently populate mention_roles across
// all message delivery paths (webhook sends, Components V2 messages
// with empty m.Content and the ping buried in a TextDisplay node,
// etc.).
//
// Signals, in order:
//
//  1. m.MentionRoles — Discord's parsed field (most reliable when set).
//  2. Raw <@&roleID> substring in m.Content.
//  3. Raw <@&roleID> in any embed title / description / field value.
//  4. Raw <@&roleID> in any Components V2 TextDisplay node (critical
//     for new DM event messages that render the ping via Container +
//     TextDisplay rather than legacy m.Content).
//  5. Raw <@&roleID> in m.ReferencedMessage.Content (reply chains).
//
// Any single hit returns true. Empty roleID returns false — caller
// handles that case by falling back to pure text-match detection.
func isEventRolePinged(m *discord.Message, roleID string) bool {
	if roleID == "" {
		return false
	}
	for _, r := range m.MentionRoles {
		if r == roleID {
			return true
		}
	}
	needle := "<@&" + roleID + ">"
	if strings.Contains(m.Content, needle) {
		return true
	}
	for _, e := range m.Embeds {
		if strings.Contains(e.Title, needle) || strings.Contains(e.Description, needle) {
			return true
		}
		for _, f := range e.Fields {
			if strings.Contains(f.Name, needle) || strings.Contains(f.Value, needle) {
				return true
			}
		}
	}
	for _, t := range discord.CollectText(m.Components) {
		if strings.Contains(t, needle) {
			return true
		}
	}
	if m.ReferencedMessage != nil && strings.Contains(m.ReferencedMessage.Content, needle) {
		return true
	}
	return false
}

// isSellConfirmation returns true when the message is Dank Memer's
// "Selling Creatures" bucket-sell popup — the confirmation screen that
// lists every fish about to be sold and has Cancel / Sell for N Coins /
// Sell for N Tokens buttons. We skip reward + item parsing on this
// message type because:
//
//   - Each listed fish line matches our generic item regex and would be
//     counted as a brand-new catch, inflating inventory and fish totals.
//   - The "You will receive ⏣ N" line would be read as a reward and
//     double-added to the balance (coins mode) or confuse the fish-token
//     accounting.
//
// Detection uses two independent signals so one DM text-format change
// doesn't silently break it: the literal "Selling Creatures" title text
// (walking content, embeds, and Components V2 TextDisplay nodes), and
// any button whose label starts with "Sell for" — those labels are
// unique to this popup.
//
// A third fallback is a sanity floor: real catches contain exactly one
// fish name ("You caught a Sturgeon."); if the item parser surfaced more
// than one unique item name AND the message has a Sell-for button, this
// is definitely the sell popup even if the title text was missed.
func isSellConfirmation(m *discord.Message) bool {
	// Signal 1: literal title text, searched across every text channel in
	// the message (content, embeds, Components V2 text).
	var all strings.Builder
	all.WriteString(strings.ToLower(m.Content))
	for _, e := range m.Embeds {
		all.WriteString(" ")
		all.WriteString(strings.ToLower(e.Title))
		all.WriteString(" ")
		all.WriteString(strings.ToLower(e.Description))
	}
	for _, t := range discord.CollectText(m.Components) {
		all.WriteString(" ")
		all.WriteString(strings.ToLower(t))
	}
	text := all.String()
	if strings.Contains(text, "selling creatures") {
		return true
	}
	// Signal 2: "Sell for N Coins" / "Sell for N Tokens" button labels
	// are unique to this screen (regular caught-fish screens use "Sell
	// Creature"/"Sell Fish" instead).
	for _, b := range discord.CollectButtons(m.Components) {
		if strings.Contains(strings.ToLower(b.Label), "sell for") {
			return true
		}
	}
	return false
}

// extractEventPhrase walks all text in a message (content, embeds, components)
// and returns the lowercased phrase from a "first to say" random event, or "".
func extractEventPhrase(m *discord.Message) string {
	parts := []string{m.Content}
	for _, e := range m.Embeds {
		parts = append(parts, e.Title, e.Description)
		for _, f := range e.Fields {
			parts = append(parts, f.Name, f.Value)
		}
	}
	parts = append(parts, discord.CollectText(m.Components)...)
	text := strings.Join(parts, " ")
	match := eventPhraseRE.FindStringSubmatch(text)
	if match == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(match[1]))
}

// pickWorkingProxy races up to 100 random proxies from the pool against a
// quick HTTP probe (cloudflare trace, 5s per-probe timeout, 15s overall cap).
// Returns the first one that responds with a 2xx/3xx, or "" if none respond.
func pickWorkingProxy(pool []string) string {
	if len(pool) == 0 {
		return ""
	}
	shuffled := make([]string, len(pool))
	copy(shuffled, pool)
	rand.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	if len(shuffled) > 100 {
		shuffled = shuffled[:100]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	winner := make(chan string, 1)
	var wg sync.WaitGroup
	for _, p := range shuffled {
		wg.Add(1)
		go func(proxy string) {
			defer wg.Done()
			if probeProxy(ctx, proxy, 5*time.Second) {
				select {
				case winner <- proxy:
					cancel() // cancel remaining probes
				default:
				}
			}
		}(p)
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case p := <-winner:
		return p
	case <-done:
		select {
		case p := <-winner:
			return p
		default:
			return ""
		}
	case <-ctx.Done():
		select {
		case p := <-winner:
			return p
		default:
			return ""
		}
	}
}

// probeProxy fires a single HTTP GET through the proxy and reports whether
// a reasonable response came back within timeout. Cloudflare trace endpoint
// is fast and doesn't rate-limit on probe traffic.
func probeProxy(ctx context.Context, proxyURL string, timeout time.Duration) bool {
	pu, err := neturl.Parse(proxyURL)
	if err != nil {
		return false
	}
	client := &nethttp.Client{
		Timeout: timeout,
		Transport: &nethttp.Transport{
			Proxy:                 nethttp.ProxyURL(pu),
			DisableKeepAlives:     true,
			TLSHandshakeTimeout:   timeout,
			ResponseHeaderTimeout: timeout,
		},
	}
	req, err := nethttp.NewRequestWithContext(ctx, "GET", "https://www.cloudflare.com/cdn-cgi/trace", nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 400
}

// maskProxy hides proxy credentials in log output (keeps scheme + host).
func maskProxy(p string) string {
	// http://user:pass@host:port → http://***@host:port
	if idx := strings.Index(p, "@"); idx > 0 {
		if scheme := strings.Index(p, "://"); scheme >= 0 && scheme < idx {
			return p[:scheme+3] + "***@" + p[idx+1:]
		}
	}
	return p
}

// SetCommands installs the enabled command set.
func (i *Instance) SetCommands(cmds []Command) {
	i.Commands = cmds
}

// Run blocks until ctx is done. Connects the gateway, prefetches commands, enters the main loop.
func (i *Instance) Run(ctx context.Context) {
	i.runCtx = ctx
	i.Gateway.On("READY", func(data json.RawMessage) {
		var r discord.Ready
		if err := json.Unmarshal(data, &r); err == nil {
			i.SelfUserID = r.User.ID
			i.Bus.Send(events.Event{Kind: events.GatewayUp, Message: r.User.Username})
		}
	})
	i.Gateway.On("RESUMED", func(json.RawMessage) {
		i.Bus.Send(events.Event{Kind: events.GatewayUp})
	})
	i.Gateway.On("MESSAGE_CREATE", i.onMessage)
	i.Gateway.On("MESSAGE_UPDATE", i.onMessage)
	i.Gateway.OnDisconnect = func(err error) {
		i.Bus.Send(events.Event{Kind: events.GatewayDown, Message: err.Error()})
	}

	go func() {
		if err := i.Gateway.Connect(ctx); err != nil {
			i.Log.WithError(err).Error("gateway stopped")
			ev := events.Event{Kind: events.GatewayDown, Message: err.Error()}
			if errors.Is(err, discord.ErrAuthFailed) {
				ev.Outcome = events.OutcomeAuthFailed
			}
			i.Bus.Send(ev)
		}
	}()

	// Humanized break scheduler — alternates session/break cycles with
	// optional jitter so the grinder doesn't run perfectly uniform for
	// hours. Does nothing if breaks are disabled in config.
	go i.runBreakScheduler(ctx)

	// Stuck detector — if the gateway is up and we're not paused but no
	// messages have come in from DM for 3+ minutes, refetch channel history
	// and re-fire the handler on recent DM messages to recover from missed
	// gateway events (reconnect race, DM glitch, etc.).
	go i.runStuckDetector(ctx)

	// Subscribe to TUI pause/resume signals so dashboard q/esc can
	// toggle grinder activity without exiting the whole session.
	go func() {
		if i.Bus == nil {
			return
		}
		for {
			select {
			case <-ctx.Done():
				return
			case pause, ok := <-i.Bus.PauseSignals():
				if !ok {
					return
				}
				if pause {
					i.State.Pause()
					i.Bus.Send(events.Event{Kind: events.Info, Message: "grinder paused by user"})
				} else {
					i.State.Resume()
					i.Bus.Send(events.Event{Kind: events.Info, Message: "grinder resumed"})
				}
			}
		}
	}()

	select {
	case <-i.Gateway.Ready():
	case <-ctx.Done():
		return
	}

	// Prefetch DM slash commands via guild-wide index.
	for {
		if ctx.Err() != nil {
			return
		}
		cmds, err := i.Rest.FetchGuildCommands(i.GuildID, discord.DankMemerAppID)
		if err != nil {
			i.Log.WithError(err).Warn("guild command fetch failed, retrying in 30s")
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
			}
			continue
		}
		if len(cmds) == 0 {
			i.Log.Warn("fetched 0 Dank Memer commands - is DM in this guild and this user a member?")
			i.Bus.Send(events.Event{Kind: events.Warn, Message: "fetched 0 commands - verify DM is in the guild and this user is a member"})
		} else {
			i.Log.Infof("fetched %d Dank Memer slash commands", len(cmds))
			i.Bus.Send(events.Event{Kind: events.Success, Message: fmt.Sprintf("fetched %d DM commands, grinder ready", len(cmds))})
		}
		break
	}

	// Startup backfill: scan the last few DM messages for active random
	// events (someone running /flow mid-session shouldn't miss a Reverse
	// Reverse that started during a restart). Runs in parallel with the
	// grind loop coming up — the dedup map prevents double-firing if a
	// real MESSAGE_CREATE for the same event arrives via gateway while
	// the backfill is still working.
	go i.backfillRecentEvents(ctx)

	i.loop(ctx)
}

func (i *Instance) loop(ctx context.Context) {
	shift := &i.Cluster.Shift
	var nextOff, nextOn time.Time
	if shift.Enabled {
		nextOff = time.Now().Add(humanize.ShiftJitter(shift.OnDuration.Std(), shift.Variation.Std()))
	}

	for {
		if ctx.Err() != nil {
			return
		}

		if shift.Enabled {
			if !nextOff.IsZero() && time.Now().After(nextOff) {
				nextOn = time.Now().Add(humanize.ShiftJitter(shift.OffDuration.Std(), shift.Variation.Std()))
				nextOff = time.Time{}
				i.Log.Infof("shift sleep until %s", nextOn.Format("15:04:05"))
				i.Bus.Send(events.Event{Kind: events.ShiftOff})
			}
			if !nextOn.IsZero() {
				if time.Now().Before(nextOn) {
					select {
					case <-ctx.Done():
						return
					case <-time.After(2 * time.Second):
					}
					continue
				}
				nextOff = time.Now().Add(humanize.ShiftJitter(shift.OnDuration.Std(), shift.Variation.Std()))
				nextOn = time.Time{}
				i.Log.Infof("shift wake until %s", nextOff.Format("15:04:05"))
				i.Bus.Send(events.Event{Kind: events.ShiftOn})
			}
		}

		if i.State.IsPaused() {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}

		picked := i.pickCommand()
		if picked == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(1 * time.Second):
			}
			continue
		}

		humanize.SleepRand(i.Cfg.Humanize.MinDelay.Std(), i.Cfg.Humanize.MaxDelay.Std())
		if i.Cfg.Humanize.TypingIndicator {
			_ = i.Rest.SendTyping(i.ChannelID)
			humanize.SleepRand(500*time.Millisecond, i.Cfg.Humanize.TypingVariance.Std())
		}

		i.pMu.Lock()
		i.pendingCmd = picked
		i.pMu.Unlock()

		i.Bus.Send(events.Event{Kind: events.CmdStart, Command: picked.Name()})
		cd, err := picked.Run(ctx, i)
		if err != nil {
			i.Log.WithError(err).WithField("cmd", picked.Name()).Warn("command run failed")
			i.Bus.Send(events.Event{Kind: events.CmdFail, Command: picked.Name(), Message: err.Error()})
			cd = 30 * time.Second
		}
		if cd <= 0 {
			cd = picked.DefaultCooldown()
		}
		i.State.SetCooldown(picked.Name(), time.Now().Add(cd))
	}
}

func (i *Instance) pickCommand() Command {
	for _, c := range i.Commands {
		if i.State.Ready(c.Name()) {
			return c
		}
	}
	return nil
}

// runStuckDetector periodically checks whether we've gone too long without
// any DM activity. If so, refetches the channel's recent messages and
// re-fires onMessage on each DM-authored one to catch up state we might
// have missed during a gateway disconnect / reconnect race.
func (i *Instance) runStuckDetector(ctx context.Context) {
	const (
		checkEvery = 60 * time.Second
		stuckAfter = 3 * time.Minute
	)
	// Wait one full window before even considering a resync — gives the
	// grinder time to boot and start producing activity.
	select {
	case <-ctx.Done():
		return
	case <-time.After(stuckAfter):
	}
	i.markActivity() // seed; don't false-positive the first check

	ticker := time.NewTicker(checkEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if i.State.IsPaused() {
			// User paused or on break — not stuck, just resting. Keep
			// activity timestamp fresh so we don't resync immediately on
			// resume.
			i.markActivity()
			continue
		}
		idle := i.sinceLastActivity()
		if idle < stuckAfter {
			continue
		}
		i.Bus.Send(events.Event{
			Kind:    events.Warn,
			Message: fmt.Sprintf("stuck detector: no DM activity for %s, resyncing from channel history", idle.Round(time.Second)),
		})
		i.resyncFromChannel(ctx)
	}
}

// Resync is the public entry point for command implementations that want
// to trigger a manual channel re-fetch (e.g. Flow's 20-second idle
// watchdog). Internally forwards to resyncFromChannel which fetches
// recent messages and re-dispatches onMessage on DM-authored ones. Safe
// to call concurrently with the global stuck detector — both read the
// channel history and dispatch via the same path.
func (i *Instance) Resync(ctx context.Context) {
	i.resyncFromChannel(ctx)
}

// resyncFromChannel pulls the recent channel messages and re-dispatches the
// pending command's OnResponse against DM-authored ones. Helps recover from
// MESSAGE_UPDATEs that landed during a gateway outage.
func (i *Instance) resyncFromChannel(ctx context.Context) {
	msgs, err := i.Rest.GetChannelMessages(i.ChannelID, 10)
	if err != nil {
		i.Bus.Send(events.Event{Kind: events.Warn, Message: fmt.Sprintf("resync fetch failed: %v", err)})
		// Don't retry immediately; next tick will reconsider.
		i.markActivity()
		return
	}
	fired := 0
	for _, m := range msgs {
		if ctx.Err() != nil {
			return
		}
		if m.Author.ID != discord.DankMemerAppID && m.ApplicationID != discord.DankMemerAppID {
			continue
		}
		data, err := json.Marshal(m)
		if err != nil {
			continue
		}
		i.onMessage(data)
		fired++
		if fired >= 3 {
			break
		}
	}
	if fired == 0 {
		i.Bus.Send(events.Event{Kind: events.Warn, Message: "resync: no DM messages found in recent history"})
	} else {
		i.Bus.Send(events.Event{Kind: events.Info, Message: fmt.Sprintf("resync: re-dispatched handler on %d DM messages", fired)})
	}
	// Mark activity so we don't spin on resync if nothing new comes in.
	i.markActivity()
}

// runBreakScheduler drives a fatigue-based break pattern: short random
// breaks scattered through "chunks" of grind time, with a big break at the
// end of each chunk. The big-break size scales with total session runtime
// — early session gets 30-60 min pauses, 24h+ sessions get 1-2h+ sleeps,
// 3-day runs get 3-6h "I'm actually offline" breaks. The size of the last
// big break determines how long the next chunk runs before the next big
// break (bigger break = fresher player = longer chunk). All short breaks
// are 5-15 min at second resolution, deliberately never round numbers,
// scattered randomly across the chunk so the pattern looks like a real
// person playing in bursts rather than a bot on a 2h/10m clock.
//
// See internal/humanize/breaks.go for the planning algorithm.
func (i *Instance) runBreakScheduler(ctx context.Context) {
	if !i.Cfg.Features.Breaks.Enabled {
		return
	}
	sched := humanize.NewFatigueScheduler(time.Now())
	for {
		fireIn, breakDur, kind := sched.Next(time.Now())

		// Wait until the next break is due. ctx cancel wins.
		if fireIn > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(fireIn):
			}
		}

		kindLabel := kind.String() // "short" or "big"
		i.State.PauseBreak()
		i.Bus.Send(events.Event{
			Kind:    events.BreakStart,
			Reward:  int(breakDur.Seconds()),
			Outcome: kindLabel,
			Message: fmt.Sprintf("%s break: pausing %s", kindLabel, humanize.FormatDuration(breakDur)),
		})

		select {
		case <-ctx.Done():
			i.State.ResumeBreak()
			return
		case <-time.After(breakDur):
		}

		i.State.ResumeBreak()
		sched.MarkFired(kind, breakDur)
		i.Bus.Send(events.Event{
			Kind:    events.BreakEnd,
			Reward:  int(breakDur.Seconds()),
			Outcome: kindLabel,
			Message: fmt.Sprintf("%s break ended (%s), back to grinding", kindLabel, humanize.FormatDuration(breakDur)),
		})
	}
}

// InvokeSlashCommand resolves a DM command by name and sends an interaction.
func (i *Instance) InvokeSlashCommand(name string, options []map[string]interface{}) error {
	cmds, err := i.Rest.FetchGuildCommands(i.GuildID, discord.DankMemerAppID)
	if err != nil {
		return err
	}
	cmd := discord.FindCommand(cmds, name)
	if cmd == nil {
		return fmt.Errorf("slash command %q not found", name)
	}
	payload := discord.BuildSlashCommand(cmd, i.GuildID, i.ChannelID, i.Gateway.SessionID(), options)
	return i.Rest.SendInteraction(payload)
}

// ClickButton clicks a message component button. For ephemeral messages
// (flags bit 64 set), the message_flags field in the interaction payload
// MUST include the ephemeral flag or Discord returns 404 Unknown Message.
// Use ClickButtonOnMessage when you have the full Message struct so flags
// are passed through automatically.
func (i *Instance) ClickButton(messageID, customID string) error {
	payload := discord.BuildButtonClick(discord.DankMemerAppID, i.GuildID, i.ChannelID, i.Gateway.SessionID(), messageID, customID)
	return i.Rest.SendInteraction(payload)
}

// ClickButtonOnMessage is like ClickButton but reads the message's actual
// flags so Discord routes ephemeral component interactions correctly.
func (i *Instance) ClickButtonOnMessage(m *discord.Message, customID string) error {
	payload := discord.BuildButtonClick(discord.DankMemerAppID, i.GuildID, i.ChannelID, i.Gateway.SessionID(), m.ID, customID)
	payload["message_flags"] = m.Flags
	return i.Rest.SendInteraction(payload)
}

// SelectMenu picks an option from a select menu.
func (i *Instance) SelectMenu(messageID, customID string, values []string) error {
	payload := discord.BuildSelectMenu(discord.DankMemerAppID, i.GuildID, i.ChannelID, i.Gateway.SessionID(), messageID, customID, values)
	return i.Rest.SendInteraction(payload)
}

func (i *Instance) onMessage(data json.RawMessage) {
	var m discord.Message
	if err := json.Unmarshal(data, &m); err != nil {
		return
	}
	if m.ChannelID != i.ChannelID {
		return
	}
	if m.Author.ID != discord.DankMemerAppID && m.ApplicationID != discord.DankMemerAppID {
		return
	}
	// Fresh DM activity — reset the stuck-detector clock.
	i.markActivity()

	if captcha.IsCaptcha(&m) {
		i.handleCaptcha(&m)
		return
	}

	// Pause gate. Captcha detection above still runs so DM's captcha
	// prompt gets caught + notified regardless. Everything below here
	// is skipped when paused — the grinder observes radio silence while
	// paused by user OR humanized break:
	//
	//   - no random event auto-responses (type / click)
	//   - no cmd.OnResponse → no button clicks on /fish /flow /hunt
	//     /search messages
	//   - no reward / item extraction → dashboard balance + inventory
	//     stop updating until the pause ends (LO's rule: "shouldn't be
	//     reading anything when paused")
	//
	// The only things that keep running during pause are the captcha
	// detector above (so an inbound captcha still pauses + notifies)
	// and markActivity (which just updates an internal timestamp the
	// stuck detector uses; purely state, not user-visible).
	paused := i.State.IsPaused()

	// Random event auto-responder — fires regardless of grinder mode. Each
	// supported event type has a handler in randomEventHandlers (see
	// randomevents.go). We iterate the registry, run detectors in order,
	// and the first match sends its response. Per-handler toggles let the
	// user disable individual auto-responses while leaving the master
	// Random Events switch on.
	//
	// Three guards prevent false positives from edited/replayed messages:
	//
	//  1. Dedupe by message ID — never respond twice to the same message.
	//     Critical for the stuck-detector resync, which replays recent DM
	//     messages through onMessage and would otherwise re-trigger
	//     auto-responses on events we already handled.
	//  2. Role-ping gate — if Event Role ID is configured in settings,
	//     REQUIRE the role to be in mention_roles on this message. DM
	//     pings the role only on new events; edits/results don't re-ping,
	//     so this filters out replayed result messages that still match
	//     the detector's title text.
	//  3. Detector-level strict phrase validation (see findStandalonePhrase
	//     with phraseFilterStrict) rejects non-natural-language phrases
	//     like "1 <:Apple:123> Apple" that match the "any short text block"
	//     heuristic but are obviously not what DM asked for.
	//
	// If role ID is configured but NOT pinged on this message, dispatch is
	// skipped entirely — no detectors run, no warning fires. If role ID is
	// NOT configured, we fall back to pure text-match detection as before.
	if i.Cfg.Features.RandomEvents && !paused {
		i.dispatchRandomEvents(&m)
	}

	// Scope to interactions we initiated.
	if m.Interaction != nil && i.SelfUserID != "" && m.Interaction.User.ID != i.SelfUserID {
		return
	}

	if parser.LooksLikeCooldown(&m) {
		i.pMu.Lock()
		cmd := i.pendingCmd
		i.pMu.Unlock()
		if cmd != nil {
			i.State.SetCooldown(cmd.Name(), time.Now().Add(30*time.Second))
			i.Log.WithField("cmd", cmd.Name()).Debug("cooldown signaled, backing off")
			i.Bus.Send(events.Event{Kind: events.CmdCooldown, Command: cmd.Name(), Reward: 30})
		}
		return
	}

	i.pMu.Lock()
	cmd := i.pendingCmd
	i.pMu.Unlock()

	// Bucket sell confirmation ("Selling Creatures" popup) lists every fish
	// in the bucket. The generic item-line regex would match each listed
	// fish as a +1 catch, inflating inventory + fish-count stats. Likewise
	// the "You will receive ⏣ N" line would be read as a reward. Detect
	// the popup and skip both extractions.
	//
	// Also skip both extractions when paused — the dashboard should stop
	// moving entirely while the grinder is deliberately idle (manual
	// pause OR humanized break).
	sellConf := isSellConfirmation(&m)

	if !paused {
		if n, ok := parser.ExtractReward(&m); ok && !sellConf {
			i.State.AddBalance(n)
			if cmd != nil {
				i.Bus.Send(events.Event{Kind: events.CmdSuccess, Command: cmd.Name(), Reward: n})
			}
		}

		if items, ok := parser.ExtractItems(&m); ok && !sellConf {
			i.Bus.Send(events.Event{Kind: events.Item, Items: items})
		}
	}

	if cmd != nil && !paused {
		if err := cmd.OnResponse(i.runCtx, i, &m); err != nil {
			// Surface as a visible dashboard warning (previously logged
			// at Debug and invisible). Silent click-failures were the
			// root cause of multi-hour flow stalls — a stale custom_id
			// or 429 would just vanish into the log file.
			i.Log.WithError(err).WithField("cmd", cmd.Name()).Warn("OnResponse error")
			i.Bus.Send(events.Event{Kind: events.Warn, Message: fmt.Sprintf("%s handler error: %v", cmd.Name(), err)})
		}
	}
}

func (i *Instance) handleCaptcha(m *discord.Message) {
	i.Log.Warn("CAPTCHA detected — pausing instance")
	i.Bus.Send(events.Event{Kind: events.Captcha})
	if i.Cfg.Captcha.PauseOnDetect {
		i.State.Pause()
	}
	if err := captcha.Notify(i.Cfg.Captcha.Webhook, i.Cfg.Captcha.MentionUserID, i.Name, i.ChannelID); err != nil {
		i.Log.WithError(err).Warn("captcha webhook failed")
	}
}
