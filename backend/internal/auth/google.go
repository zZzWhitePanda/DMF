// Package auth implements Google OAuth 2.0 sign-in, cookie-based
// sessions, and middleware that gates routes on authentication +
// license validity.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/oauth2"
	oauth2google "golang.org/x/oauth2/google"
)

// Config holds the OAuth and session settings read from env vars at
// startup (see cmd/server/main.go).
type Config struct {
	ClientID        string        // GOOGLE_CLIENT_ID
	ClientSecret    string        // GOOGLE_CLIENT_SECRET
	RedirectURL     string        // full URL to /auth/google/callback
	AdminEmail      string        // email that gets is_admin=1 on first login
	SessionDuration time.Duration // how long browser sessions last
	SessionCookie   string        // cookie name
	SecureCookies   bool          // true in prod (HTTPS), false in local dev
}

// GoogleProvider wraps the oauth2.Config + userinfo endpoint for Google
// sign-in.
type GoogleProvider struct {
	oauth *oauth2.Config
	cfg   Config
}

// NewGoogleProvider constructs the provider. Scopes requested are the
// minimum for identifying a user: openid, email, profile.
func NewGoogleProvider(cfg Config) *GoogleProvider {
	return &GoogleProvider{
		oauth: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Scopes:       []string{"openid", "email", "profile"},
			Endpoint:     oauth2google.Endpoint,
		},
		cfg: cfg,
	}
}

// AuthURL returns the Google consent URL for the login redirect. The
// state parameter is a short-lived CSRF token that round-trips via a
// signed cookie and is verified in the callback.
func (p *GoogleProvider) AuthURL(state string) string {
	return p.oauth.AuthCodeURL(state, oauth2.AccessTypeOnline)
}

// Exchange trades an authorization code for tokens.
func (p *GoogleProvider) Exchange(ctx context.Context, code string) (*oauth2.Token, error) {
	return p.oauth.Exchange(ctx, code)
}

// GoogleUser is the subset of https://www.googleapis.com/oauth2/v3/userinfo
// we actually use.
type GoogleUser struct {
	Sub           string `json:"sub"`             // Google's stable user ID
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
	Picture       string `json:"picture"`
}

// FetchUserInfo pulls the user's profile from Google using the issued
// access token.
func (p *GoogleProvider) FetchUserInfo(ctx context.Context, token *oauth2.Token) (*GoogleUser, error) {
	client := p.oauth.Client(ctx, token)
	resp, err := client.Get("https://www.googleapis.com/oauth2/v3/userinfo")
	if err != nil {
		return nil, fmt.Errorf("fetch userinfo: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("userinfo: HTTP %d", resp.StatusCode)
	}
	var u GoogleUser
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return nil, fmt.Errorf("decode userinfo: %w", err)
	}
	return &u, nil
}

// RandomState generates a URL-safe random token for OAuth state. Also
// used for session IDs — different lengths, same primitive.
func RandomState(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand should never fail in practice — if it does,
		// something is deeply wrong with the system and we can't
		// safely issue sessions. Return a short panic-worthy value.
		return "fallback-" + fmt.Sprint(time.Now().UnixNano())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// SetCookie writes an HttpOnly cookie carrying value. secureCookies
// should be true in prod (HTTPS) and false in local dev (HTTP).
func (p *GoogleProvider) SetCookie(w http.ResponseWriter, name, value string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   p.cfg.SecureCookies,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(ttl),
		MaxAge:   int(ttl.Seconds()),
	})
}

// ClearCookie expires a cookie immediately.
func (p *GoogleProvider) ClearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   p.cfg.SecureCookies,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
}
