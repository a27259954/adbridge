package app

import "strings"

// ResolveLanguage maps a BCP 47 tag (e.g. "zh-Hant-TW", "ja-JP", "es-419")
// to one of the supported UI languages, falling back to English.
func ResolveLanguage(tag string) string {
	t := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(tag), "_", "-"))
	switch {
	case t == "":
		return "en"
	case strings.HasPrefix(t, "zh"):
		if strings.Contains(t, "hant") || strings.HasSuffix(t, "-tw") || strings.HasSuffix(t, "-hk") || strings.HasSuffix(t, "-mo") {
			return "zh-TW"
		}
		return "zh-CN"
	case strings.HasPrefix(t, "es"):
		return "es"
	case strings.HasPrefix(t, "ja"):
		return "ja"
	case strings.HasPrefix(t, "ko"):
		return "ko"
	}
	return "en"
}

// Language returns the effective UI language: the configured one, or the
// Windows display language when set to auto.
func (c *Core) Language() string {
	if l := c.Config.Get().Language; l != "" {
		return l
	}
	return ResolveLanguage(systemLanguage())
}

// TrayLabels are the system tray menu strings.
type TrayLabels struct{ Open, StopTunnel, Quit string }

var trayLabels = map[string]TrayLabels{
	"en":    {"Open adbridge", "Stop tunnel", "Quit"},
	"zh-CN": {"打开 adbridge", "停止隧道", "退出"},
	"zh-TW": {"開啟 adbridge", "停止通道", "結束"},
	"es":    {"Abrir adbridge", "Detener túnel", "Salir"},
	"ja":    {"adbridge を開く", "トンネルを停止", "終了"},
	"ko":    {"adbridge 열기", "터널 중지", "종료"},
}

// Tray returns tray labels for the effective language.
func (c *Core) Tray() TrayLabels {
	if l, ok := trayLabels[c.Language()]; ok {
		return l
	}
	return trayLabels["en"]
}
