package api

import (
	"context"
	"errors"
	"strings"
)

const prefLanguage = "language"

func supportedLanguage(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	tag = strings.ReplaceAll(tag, "_", "-")
	tag, _, _ = strings.Cut(tag, ".")
	tag, _, _ = strings.Cut(tag, "@")
	base, _, _ := strings.Cut(tag, "-")
	if base == "zh" {
		// Only Simplified Chinese is translated. Do not advertise a
		// Traditional Chinese translation for Hant/Taiwan/Hong Kong/Macau.
		parts := strings.Split(tag, "-")[1:]
		simplified := false
		for _, part := range parts {
			if part == "hans" {
				simplified = true
			}
			if part == "hant" {
				return ""
			}
		}
		if !simplified {
			for _, part := range parts {
				if part == "tw" || part == "hk" || part == "mo" {
					return ""
				}
			}
		}
	}
	switch base {
	case "ru", "en", "zh", "es", "de", "fr", "pt", "ja":
		return base
	}
	return ""
}

// systemLanguage honors gettext's environment precedence. Within LANGUAGE,
// the first supported entry wins; an explicit C/POSIX means English.
func systemLanguage(getenv func(string) string) string {
	if getenv == nil {
		return ""
	}
	for _, key := range []string{"LANGUAGE", "LC_ALL", "LC_MESSAGES", "LANG"} {
		value := getenv(key)
		if value == "" {
			continue
		}
		for _, tag := range strings.Split(value, ":") {
			if tag == "C" || tag == "POSIX" || strings.HasPrefix(tag, "C.") {
				return "en"
			}
			if language := supportedLanguage(tag); language != "" {
				return language
			}
		}
		return "" // an unsupported explicit locale does not yield to LANG
	}
	return ""
}

// SetLanguage is a UI-only preference, shared by desktop and browser profiles.
// Empty restores automatic detection. It never changes resource/session state.
func (s *Service) SetLanguage(ctx context.Context, language string) error {
	if !fromUI(ctx) {
		return coded("forbidden", errors.New("language preferences are available only in the UI"))
	}
	if language != "" && supportedLanguage(language) != language {
		return coded(CodeBadRequest, nil)
	}
	if err := s.store.SetUIPref(ctx, prefLanguage, language); err != nil {
		return coded(CodeInternal, err)
	}
	return nil
}
