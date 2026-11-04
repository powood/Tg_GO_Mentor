package config

import (
	"errors"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Env      string
	Bot      BotConfig
	Postgres PostgresConfig
	Redis    RedisConfig
	AI       AIConfig
}

type BotConfig struct {
	Token   string
	Timeout time.Duration
}

type PostgresConfig struct {
	URL         string
	MaxConns    int32
	MinConns    int32
	MaxConnIdle time.Duration
}

type RedisConfig struct {
	Addr     string
	Password string
	DB       int
	StateTTL time.Duration
}

type AIConfig struct {
	DeepgramKey  string
	GroqKeys     []string
	DeepSeekKeys []string
	DeepSeekURL  string
	Model        string
	ProxyURL     string
}

func parseKeyList(pluralEnv, singularEnv string) []string {
	var keys []string
	if val := os.Getenv(pluralEnv); val != "" {
		for _, k := range strings.Split(val, ",") {
			k = strings.TrimSpace(k)
			if k != "" {
				keys = append(keys, k)
			}
		}
	} else if single := strings.TrimSpace(os.Getenv(singularEnv)); single != "" {
		keys = append(keys, single)
	}

	return keys
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	env := strings.ToLower(strings.TrimSpace(getEnv("ENV", "local")))
	if env == "prod" {
		env = "production"
	}

	token := strings.TrimSpace(os.Getenv("BOT_TOKEN"))
	if token == "" {
		return nil, errors.New("конфигурация: переменная BOT_TOKEN обязательна")
	}

	groqKeys := parseKeyList("GROQ_API_KEYS", "GROQ_API_KEY")
	if len(groqKeys) == 0 {
		return nil, errors.New("конфигурация: необходимо указать хотя бы один ключ GROQ_API_KEY(S)")
	}

	deepSeekKeys := parseKeyList("DEEPSEEK_API_KEYS", "DEEPSEEK_API_KEY")
	if len(deepSeekKeys) == 0 {
		return nil, errors.New("конфигурация: необходимо указать хотя бы один ключ DEEPSEEK_API_KEY(S)")
	}

	proxyRaw := strings.TrimSpace(getEnv("PROXY_URL", os.Getenv("GROQ_PROXY_URL")))
	if proxyRaw != "" {
		u, err := url.Parse(proxyRaw)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5" && u.Scheme != "socks5h") {
			return nil, errors.New("конфигурация: некорректный PROXY_URL (ожидается http/https/socks5://host:port)")
		}
	}

	defaultDBPort := getEnv("POSTGRES_PORT", "5434")
	defaultDBURL := "postgres://postgres:postgrespassword@localhost:" + defaultDBPort + "/tg_mentor?sslmode=disable"
	dbURL := getEnv("DATABASE_URL", defaultDBURL)
	redisPassword := os.Getenv("REDIS_PASSWORD")

	if env == "production" {
		if strings.TrimSpace(os.Getenv("DATABASE_URL")) == "" || strings.Contains(dbURL, "postgrespassword") {
			return nil, errors.New("конфигурация (production): задайте надежный DATABASE_URL вместо дефолтного пароля")
		}
		if strings.TrimSpace(redisPassword) == "" {
			return nil, errors.New("конфигурация (production): переменная REDIS_PASSWORD обязательна в продакшене")
		}
	}

	cfg := &Config{
		Env: env,
		Bot: BotConfig{
			Token:   token,
			Timeout: 10 * time.Second,
		},
		Postgres: PostgresConfig{
			URL:         dbURL,
			MaxConns:    10,
			MinConns:    2,
			MaxConnIdle: 5 * time.Minute,
		},
		Redis: RedisConfig{
			Addr:     getEnv("REDIS_ADDR", "localhost:6379"),
			Password: redisPassword,
			DB:       0,
			StateTTL: 24 * time.Hour,
		},
		AI: AIConfig{
			DeepgramKey:  strings.TrimSpace(os.Getenv("DEEPGRAM_API_KEY")),
			GroqKeys:     groqKeys,
			DeepSeekKeys: deepSeekKeys,
			DeepSeekURL:  getEnv("DEEPSEEK_BASE_URL", "https://crassusmarket.xyz"),
			Model:        getEnv("DEEPSEEK_MODEL", "gpt-6.1-terra"),
			ProxyURL:     proxyRaw,
		},
	}
	return cfg, nil
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
