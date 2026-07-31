package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dockside-gg/game-panel/internal/store"
)

const apiBaseURL = "https://discord.com/api/v10"

type Client struct {
	clientID     string
	clientSecret string
	redirectURI  string
	http         *http.Client
}

func New(clientID, clientSecret, redirectURI string) *Client {
	return &Client{
		clientID:     clientID,
		clientSecret: clientSecret,
		redirectURI:  redirectURI,
		http: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (c *Client) AuthorizationURL(state string) string {
	query := url.Values{
		"client_id":     {c.clientID},
		"redirect_uri":  {c.redirectURI},
		"response_type": {"code"},
		"scope":         {"identify"},
		"state":         {state},
		"prompt":        {"consent"},
	}
	return "https://discord.com/oauth2/authorize?" + query.Encode()
}

func (c *Client) Exchange(ctx context.Context, code string) (string, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {c.redirectURI},
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		apiBaseURL+"/oauth2/token",
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return "", fmt.Errorf("create token request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return "", fmt.Errorf("exchange discord code: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read discord token response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("discord token endpoint returned %d", response.StatusCode)
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("decode discord token response: %w", err)
	}
	if payload.AccessToken == "" || !strings.EqualFold(payload.TokenType, "Bearer") {
		return "", fmt.Errorf("discord returned an invalid access token response")
	}
	return payload.AccessToken, nil
}

func (c *Client) CurrentUser(ctx context.Context, accessToken string) (store.DiscordUser, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBaseURL+"/users/@me", nil)
	if err != nil {
		return store.DiscordUser{}, fmt.Errorf("create discord user request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return store.DiscordUser{}, fmt.Errorf("get discord user: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		return store.DiscordUser{}, fmt.Errorf("discord user endpoint returned %d", response.StatusCode)
	}
	var user store.DiscordUser
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&user); err != nil {
		return store.DiscordUser{}, fmt.Errorf("decode discord user: %w", err)
	}
	return user, nil
}
