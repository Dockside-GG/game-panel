package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/dockside-gg/game-panel/internal/store"
)

type Client struct {
	http *http.Client
}

func New() *Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          20,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, resolved := range addresses {
				if !publicIP(resolved.IP) {
					continue
				}
				connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(resolved.IP.String(), port))
				if err == nil {
					return connection, nil
				}
			}
			return nil, errors.New("webhook host has no permitted public address")
		},
	}
	return &Client{
		http: &http.Client{
			Transport: transport,
			Timeout:   30 * time.Minute,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (c *Client) SendBackup(
	ctx context.Context,
	job store.BackupDeliveryJob,
	rawURL, filename string,
	content io.Reader,
) (status int, retryAfter time.Duration, permanent bool, err error) {
	if err := ValidateURL(rawURL, "discord"); err != nil {
		return 0, 0, true, err
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return 0, 0, true, err
	}
	query := parsed.Query()
	query.Set("wait", "true")
	parsed.RawQuery = query.Encode()
	payload, err := json.Marshal(map[string]any{
		"username":         "Dockside.GG",
		"allowed_mentions": map[string]any{"parse": []string{}},
		"embeds": []map[string]any{{
			"title": "Backup delivered: " + job.BackupName,
			"description": fmt.Sprintf(
				"Restorable Dockside backup `%s`\nSHA-256 `%s`",
				job.Format, job.SHA256,
			),
			"color":  0x11C3F5,
			"footer": map[string]string{"text": "Dockside.GG game server backup"},
		}},
	})
	if err != nil {
		return 0, 0, true, err
	}
	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	go func() {
		var writeErr error
		if field, fieldErr := multipartWriter.CreateFormField("payload_json"); fieldErr != nil {
			writeErr = fieldErr
		} else if _, fieldErr = field.Write(payload); fieldErr != nil {
			writeErr = fieldErr
		}
		if writeErr == nil {
			var part io.Writer
			part, writeErr = multipartWriter.CreateFormFile("files[0]", filename)
			if writeErr == nil {
				_, writeErr = io.Copy(part, content)
			}
		}
		if closeErr := multipartWriter.Close(); writeErr == nil {
			writeErr = closeErr
		}
		_ = writer.CloseWithError(writeErr)
	}()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, parsed.String(), reader)
	if err != nil {
		return 0, 0, true, err
	}
	request.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Dockside.GG-Game-Panel/1")
	request.Header.Set("X-Dockside-Delivery", job.DeliveryID)
	response, err := c.http.Do(request)
	if err != nil {
		return 0, backoff(job.Attempts), job.Attempts >= 10, err
	}
	defer response.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return response.StatusCode, 0, false, nil
	}
	retry := parseRetryAfter(response.Header.Get("Retry-After"))
	if retry == 0 {
		retry = backoff(job.Attempts)
	}
	transient := response.StatusCode == http.StatusRequestTimeout ||
		response.StatusCode == http.StatusConflict ||
		response.StatusCode == http.StatusTooManyRequests ||
		response.StatusCode >= 500
	detail := strings.TrimSpace(string(responseBody))
	if len(detail) > 500 {
		detail = detail[:500]
	}
	return response.StatusCode, retry, !transient || job.Attempts >= 10,
		fmt.Errorf("Discord backup upload returned HTTP %d: %s", response.StatusCode, detail)
}

func ValidateURL(rawURL, kind string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.Fragment != "" {
		return errors.New("webhook URL must be an HTTPS URL without credentials or fragments")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return errors.New("webhook URL cannot target localhost")
	}
	if ip := net.ParseIP(host); ip != nil && !publicIP(ip) {
		return errors.New("webhook URL must target a public address")
	}
	if kind == "discord" {
		if host != "discord.com" && host != "canary.discord.com" && host != "ptb.discord.com" {
			return errors.New("Discord webhook must use discord.com")
		}
		if !strings.HasPrefix(parsed.Path, "/api/webhooks/") {
			return errors.New("invalid Discord incoming webhook path")
		}
	}
	return nil
}

func (c *Client) Send(
	ctx context.Context,
	job store.WebhookDeliveryJob,
	rawURL, signingSecret string,
) (status int, retryAfter time.Duration, permanent bool, err error) {
	if err := ValidateURL(rawURL, job.Kind); err != nil {
		return 0, 0, true, err
	}
	payload, err := deliveryPayload(job)
	if err != nil {
		return 0, 0, true, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(payload))
	if err != nil {
		return 0, 0, true, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Dockside.GG-Game-Panel/1")
	request.Header.Set("X-Dockside-Event", job.EventType)
	request.Header.Set("X-Dockside-Delivery", job.DeliveryID)
	if signingSecret != "" {
		signature := hmac.New(sha256.New, []byte(signingSecret))
		_, _ = signature.Write(payload)
		request.Header.Set("X-Dockside-Signature", "sha256="+hex.EncodeToString(signature.Sum(nil)))
	}
	response, err := c.http.Do(request)
	if err != nil {
		return 0, backoff(job.Attempts), job.Attempts >= 10, err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return response.StatusCode, 0, false, nil
	}
	retry := parseRetryAfter(response.Header.Get("Retry-After"))
	if retry == 0 {
		retry = backoff(job.Attempts)
	}
	transient := response.StatusCode == http.StatusRequestTimeout ||
		response.StatusCode == http.StatusConflict ||
		response.StatusCode == http.StatusTooEarly ||
		response.StatusCode == http.StatusTooManyRequests ||
		response.StatusCode >= 500
	return response.StatusCode, retry, !transient || job.Attempts >= 10,
		fmt.Errorf("webhook returned HTTP %d", response.StatusCode)
}

func deliveryPayload(job store.WebhookDeliveryJob) ([]byte, error) {
	if job.Kind == "discord" {
		color := map[string]int{"info": 0x11C3F5, "warning": 0xFDB022, "error": 0xF97066}[job.Severity]
		return json.Marshal(map[string]any{
			"username": "Dockside.GG",
			"allowed_mentions": map[string]any{
				"parse": []string{},
			},
			"embeds": []map[string]any{{
				"title":       job.Summary,
				"description": "`" + job.EventType + "`",
				"color":       color,
				"timestamp":   job.CreatedAt.UTC().Format(time.RFC3339),
				"footer":      map[string]string{"text": "Dockside.GG game server event"},
			}},
		})
	}
	var data any
	if len(job.Data) > 0 {
		_ = json.Unmarshal(job.Data, &data)
	}
	return json.Marshal(map[string]any{
		"version":     1,
		"delivery_id": job.DeliveryID,
		"event": map[string]any{
			"id":         job.EventID,
			"type":       job.EventType,
			"severity":   job.Severity,
			"summary":    job.Summary,
			"data":       data,
			"created_at": job.CreatedAt.UTC(),
			"server_id":  job.ServerID,
		},
	})
}

func publicIP(ip net.IP) bool {
	return ip != nil && !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsUnspecified() &&
		!ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsMulticast()
}

func backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	seconds := 1 << min(attempt, 10)
	if seconds > 900 {
		seconds = 900
	}
	return time.Duration(seconds) * time.Second
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	seconds, err := strconv.ParseFloat(value, 64)
	if err == nil && seconds > 0 {
		return time.Duration(seconds * float64(time.Second))
	}
	if when, err := http.ParseTime(value); err == nil {
		return time.Until(when)
	}
	return 0
}
