package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Component           string
	InstanceID          string
	PublicURL           *url.URL
	ListenAddress       string
	DatabaseURL         string
	EncryptionKey       []byte
	SessionKey          []byte
	DiscordClientID     string
	DiscordClientSecret string
	EngineURL           string
	EngineToken         string
	BootstrapToken      string
	MFAPolicy           string
	ServerDataRoot      string
	BackupRoot          string
	GamePortStart       int
	GamePortEnd         int
	ServerUID           int
	ServerGID           int
	SecureCookies       bool
	LogLevel            string
	SessionIdle         time.Duration
	SessionAbsolute     time.Duration
}

func Load(component string) (Config, error) {
	cfg := Config{
		Component:       component,
		InstanceID:      strings.TrimSpace(os.Getenv("DOCKSIDE_INSTANCE_ID")),
		ListenAddress:   envDefault("DOCKSIDE_LISTEN_ADDRESS", defaultListen(component)),
		EngineURL:       envDefault("DOCKSIDE_ENGINE_URL", "http://engine:8081"),
		ServerDataRoot:  envDefault("DOCKSIDE_SERVER_DATA_ROOT", "/var/lib/dockside/servers"),
		BackupRoot:      envDefault("DOCKSIDE_BACKUP_ROOT", "/var/lib/dockside/backups"),
		GamePortStart:   envInt("DOCKSIDE_GAME_PORT_START", 20000),
		GamePortEnd:     envInt("DOCKSIDE_GAME_PORT_END", 29999),
		ServerUID:       envInt("DOCKSIDE_SERVER_UID", 1000),
		ServerGID:       envInt("DOCKSIDE_SERVER_GID", 1000),
		SecureCookies:   envBool("DOCKSIDE_SECURE_COOKIES", true),
		LogLevel:        envDefault("DOCKSIDE_LOG_LEVEL", "info"),
		SessionIdle:     envDuration("DOCKSIDE_SESSION_IDLE", 8*time.Hour),
		SessionAbsolute: envDuration("DOCKSIDE_SESSION_ABSOLUTE", 24*time.Hour),
	}

	if rawURL := strings.TrimSpace(os.Getenv("DOCKSIDE_PUBLIC_URL")); rawURL != "" {
		parsed, err := url.Parse(rawURL)
		if err != nil {
			return Config{}, fmt.Errorf("parse DOCKSIDE_PUBLIC_URL: %w", err)
		}
		if parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return Config{}, fmt.Errorf("DOCKSIDE_PUBLIC_URL must contain only scheme, host, and optional port")
		}
		parsed.Path = strings.TrimSuffix(parsed.Path, "/")
		if parsed.Path != "" {
			return Config{}, fmt.Errorf("DOCKSIDE_PUBLIC_URL path prefixes are not supported")
		}
		if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname())) {
			return Config{}, fmt.Errorf("DOCKSIDE_PUBLIC_URL must use HTTPS unless it is loopback")
		}
		cfg.PublicURL = parsed
	}

	var err error
	if component != "engine" {
		cfg.DatabaseURL, err = readSecret("DOCKSIDE_DATABASE_URL", "DOCKSIDE_DATABASE_URL_FILE", true)
		if err != nil {
			return Config{}, err
		}
		cfg.EncryptionKey, err = readKey("DOCKSIDE_ENCRYPTION_KEY", "DOCKSIDE_ENCRYPTION_KEY_FILE", true)
		if err != nil {
			return Config{}, err
		}
	}
	if component == "app" {
		if cfg.PublicURL == nil {
			return Config{}, errors.New("DOCKSIDE_PUBLIC_URL is required for app")
		}
		cfg.SessionKey, err = readKey("DOCKSIDE_SESSION_KEY", "DOCKSIDE_SESSION_KEY_FILE", true)
		if err != nil {
			return Config{}, err
		}
		cfg.DiscordClientID = strings.TrimSpace(os.Getenv("DOCKSIDE_DISCORD_CLIENT_ID"))
		if cfg.DiscordClientID == "" {
			return Config{}, errors.New("DOCKSIDE_DISCORD_CLIENT_ID is required")
		}
		cfg.DiscordClientSecret, err = readSecret("DOCKSIDE_DISCORD_CLIENT_SECRET", "DOCKSIDE_DISCORD_CLIENT_SECRET_FILE", true)
		if err != nil {
			return Config{}, err
		}
		cfg.BootstrapToken, err = readSecret("DOCKSIDE_BOOTSTRAP_TOKEN", "DOCKSIDE_BOOTSTRAP_TOKEN_FILE", false)
		if err != nil {
			return Config{}, err
		}
		cfg.MFAPolicy = envDefault("DOCKSIDE_MFA_POLICY", "administrators")
		if cfg.MFAPolicy != "off" && cfg.MFAPolicy != "administrators" &&
			cfg.MFAPolicy != "operators" && cfg.MFAPolicy != "everyone" {
			return Config{}, errors.New("DOCKSIDE_MFA_POLICY must be off, administrators, operators, or everyone")
		}
	}
	cfg.EngineToken, err = readSecret("DOCKSIDE_ENGINE_TOKEN", "DOCKSIDE_ENGINE_TOKEN_FILE", component == "app" || component == "worker" || component == "engine")
	if err != nil {
		return Config{}, err
	}

	if cfg.InstanceID == "" {
		return Config{}, errors.New("DOCKSIDE_INSTANCE_ID is required")
	}
	if cfg.GamePortStart < 1024 || cfg.GamePortEnd > 65535 || cfg.GamePortStart > cfg.GamePortEnd {
		return Config{}, errors.New("invalid game port range")
	}
	if cfg.ServerUID < 1 || cfg.ServerUID > 65535 || cfg.ServerGID < 1 || cfg.ServerGID > 65535 {
		return Config{}, errors.New("server UID and GID must be 1-65535")
	}
	return cfg, nil
}

func defaultListen(component string) string {
	if component == "engine" {
		return ":8081"
	}
	return ":8080"
}

func envDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func readSecret(valueEnv, fileEnv string, required bool) (string, error) {
	if file := strings.TrimSpace(os.Getenv(fileEnv)); file != "" {
		value, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", fileEnv, err)
		}
		secret := strings.TrimSpace(string(value))
		if required && secret == "" {
			return "", fmt.Errorf("%s is empty", fileEnv)
		}
		return secret, nil
	}
	value := strings.TrimSpace(os.Getenv(valueEnv))
	if required && value == "" {
		return "", fmt.Errorf("%s or %s is required", valueEnv, fileEnv)
	}
	return value, nil
}

func readKey(valueEnv, fileEnv string, required bool) ([]byte, error) {
	value, err := readSecret(valueEnv, fileEnv, required)
	if err != nil || value == "" {
		return nil, err
	}
	decoded, decodeErr := base64.RawURLEncoding.DecodeString(value)
	if decodeErr == nil && len(decoded) == 32 {
		return decoded, nil
	}
	if len(value) == 32 {
		return []byte(value), nil
	}
	return nil, fmt.Errorf("%s must contain 32 raw bytes or unpadded base64url for 32 bytes", valueEnv)
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
