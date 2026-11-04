package logger

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
)

const (
	EnvLocal = "local"
	EnvProd  = "production"
	EnvDev   = "dev"
)

// Setup создает структурированный логгер и централизованно вырезает переданные секреты из любых логов.
func Setup(env string, rawSecrets ...string) *slog.Logger {
	secrets := collectSecrets(rawSecrets...)

	sanitize := func(s string) string {
		for _, sec := range secrets {
			s = strings.ReplaceAll(s, sec, "<redacted>")
		}
		return s
	}

	replaceAttr := func(_ []string, a slog.Attr) slog.Attr {
		if len(secrets) == 0 {
			return a
		}
		switch a.Value.Kind() {
		case slog.KindString:
			return slog.String(a.Key, sanitize(a.Value.String()))
		case slog.KindAny:
			if err, ok := a.Value.Any().(error); ok && err != nil {
				return slog.String(a.Key, sanitize(err.Error()))
			}
			if str, ok := a.Value.Any().(fmt.Stringer); ok && str != nil {
				return slog.String(a.Key, sanitize(str.String()))
			}
		}
		return a
	}

	switch env {
	case EnvLocal, EnvDev:
		return slog.New(
			slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
				Level:       slog.LevelDebug,
				ReplaceAttr: replaceAttr,
			}),
		)
	case EnvProd:
		return slog.New(
			slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
				Level:       slog.LevelInfo,
				ReplaceAttr: replaceAttr,
			}),
		)
	default:
		return slog.New(
			slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
				Level:       slog.LevelInfo,
				ReplaceAttr: replaceAttr,
			}),
		)
	}
}

// collectSecrets собирает уникальные непустые секреты (>= 4 символов),
// а из URL-строк (прокси, БД) дополнительно извлекает пароль.
func collectSecrets(raw ...string) []string {
	var out []string
	seen := make(map[string]struct{})

	add := func(s string) {
		s = strings.TrimSpace(s)
		if len(s) < 4 {
			return
		}
		if _, exists := seen[s]; !exists {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}

	for _, item := range raw {
		add(item)
		if strings.Contains(item, "://") {
			if u, err := url.Parse(strings.TrimSpace(item)); err == nil && u.User != nil {
				if pass, ok := u.User.Password(); ok {
					add(pass)
				}
			}
		}
	}
	return out
}
