package app

import "golang.org/x/sys/windows"

// systemLanguage is the user's first Windows display language, e.g. "zh-TW".
func systemLanguage() string {
	langs, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err != nil || len(langs) == 0 {
		return ""
	}
	return langs[0]
}
