package app

import "testing"

func TestResolveLanguage(t *testing.T) {
	cases := map[string]string{
		"":           "en",
		"en-US":      "en",
		"fr-FR":      "en",
		"zh-TW":      "zh-TW",
		"zh-Hant-HK": "zh-TW",
		"zh_HK":      "zh-TW",
		"zh-CN":      "zh-CN",
		"zh-Hans-SG": "zh-CN",
		"es-419":     "es",
		"ja-JP":      "ja",
		"ko-KR":      "ko",
	}
	for in, want := range cases {
		if got := ResolveLanguage(in); got != want {
			t.Errorf("ResolveLanguage(%q) = %q, want %q", in, got, want)
		}
	}
}
