// Package paths centralises every on-disk location adbridge uses.
//
// The release is a single exe; everything it writes lives under the user's
// profile so the folder the exe sits in stays untouched:
//
//	%APPDATA%\adbridge\config.json   settings (roams with the user)
//	%LOCALAPPDATA%\adbridge\          machine-bound state, downloads, logs
package paths

import (
	"os"
	"path/filepath"
)

const appName = "adbridge"

// ConfigDir is %APPDATA%\adbridge.
func ConfigDir() string { return under(os.UserConfigDir) }

// DataDir is %LOCALAPPDATA%\adbridge.
func DataDir() string { return under(os.UserCacheDir) }

func ConfigFile() string    { return filepath.Join(ConfigDir(), "config.json") }
func DevicesFile() string   { return filepath.Join(DataDir(), "devices.json") }
func BinDir() string        { return filepath.Join(DataDir(), "bin") }
func LogDir() string        { return filepath.Join(DataDir(), "logs") }
func WebviewDir() string    { return filepath.Join(DataDir(), "webview") }
func UploadsDir() string    { return filepath.Join(DataDir(), "uploads") }
func RecordingsDir() string { return filepath.Join(DataDir(), "recordings") }

// Ensure creates every directory adbridge writes to.
func Ensure() error {
	for _, d := range []string{ConfigDir(), DataDir(), BinDir(), LogDir(), WebviewDir(), RecordingsDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func under(base func() (string, error)) string {
	dir, err := base()
	if err != nil || dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, appName)
}
