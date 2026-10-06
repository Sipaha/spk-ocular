package api

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSupportedSystemLanguages(t *testing.T) {
	for _, tc := range []struct{ tag, want string }{
		{"ru_RU.UTF-8", "ru"}, {"en-US", "en"}, {"zh_CN.UTF-8", "zh"},
		{"zh-Hans-SG", "zh"}, {"zh-Hans-TW", "zh"}, {"zh-TW", ""}, {"zh-Hant", ""}, {"zh-HK", ""},
		{"es_MX.UTF-8", "es"}, {"de_DE.UTF-8", "de"}, {"fr_CA.UTF-8", "fr"},
		{"pt_BR.UTF-8", "pt"}, {"pt-PT", "pt"}, {"ja_JP.UTF-8", "ja"},
		{"fa-IR", ""}, {"rubbish", ""}, {"", ""},
	} {
		assert.Equal(t, tc.want, supportedLanguage(tc.tag), tc.tag)
	}
	for _, tc := range []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"LANGUAGE": "fa:zh-Hant:pt_BR:en", "LANG": "ru_RU.UTF-8"}, "pt"},
		{map[string]string{"LC_ALL": "C.UTF-8", "LANG": "ja_JP.UTF-8"}, "en"},
		{map[string]string{"LC_MESSAGES": "de_DE.UTF-8", "LANG": "fr_FR.UTF-8"}, "de"},
		{map[string]string{"LANGUAGE": "zh-Hant", "LANG": "zh_CN.UTF-8"}, ""},
	} {
		assert.Equal(t, tc.want, systemLanguage(func(k string) string { return tc.env[k] }), tc.env)
	}
}

func TestLanguagePreferenceAndUIBoundary(t *testing.T) {
	s, _ := newService(t)
	ctx := UIContext(context.Background())
	require.Error(t, s.SetLanguage(context.Background(), "ja"))
	for _, language := range []string{"ru", "en", "zh", "es", "de", "fr", "pt", "ja"} {
		require.NoError(t, s.SetLanguage(ctx, language))
		info, err := s.AppInfo(ctx)
		require.NoError(t, err)
		assert.Equal(t, language, info.Language)
		assert.Equal(t, language, info.LanguagePreference)
	}
	for _, invalid := range []string{"fa", "zh-Hant", "pt-BR", "automatic", "../en", "EN"} {
		require.Error(t, s.SetLanguage(ctx, invalid))
		info, err := s.AppInfo(ctx)
		require.NoError(t, err)
		assert.Equal(t, "ja", info.LanguagePreference)
	}
	// A new service instance reads the same durable profile preference.
	reopened := NewService(s.reg, s.store, s.em, s.opts)
	t.Cleanup(reopened.Close)
	info, err := reopened.AppInfo(ctx)
	require.NoError(t, err)
	assert.Equal(t, "ja", info.Language)
	require.NoError(t, s.SetLanguage(ctx, ""))
	info, err = s.AppInfo(ctx)
	require.NoError(t, err)
	assert.Empty(t, info.LanguagePreference)
	assert.Equal(t, "en", info.Language)
}
