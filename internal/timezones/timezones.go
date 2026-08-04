// Package timezones resolves IANA zone identifiers, mirroring
// lib/tasks/timezones.rb as far as the read path and the probe need it: the
// identifier gate, the detection precedence, and the tzdb self-report.
package timezones

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Fallback is the zone every detection failure lands on.
const Fallback = "Etc/UTC"

// Error is the one failure this package raises, matching Tasks::Timezones::Error.
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

// Get validates an identifier and returns its canonical spelling. The gate is
// deliberately narrow: anything without a "/" is refused unless it is exactly
// "UTC", so a POSIX TZ string cannot masquerade as a zone.
func Get(identifier string) (string, error) {
	id := strings.TrimSpace(identifier)
	if id == "" {
		return "", &Error{"time zone is required"}
	}
	if !strings.Contains(id, "/") && id != "UTC" {
		return "", &Error{fmt.Sprintf("%q is not an IANA time-zone identifier", id)}
	}
	if _, err := time.LoadLocation(id); err != nil {
		return "", &Error{fmt.Sprintf("unknown IANA time zone %q", id)}
	}
	return id, nil
}

// Detect resolves the host's zone: the TZ variable, then the /etc/localtime
// symlink, then the UTC fallback. The third return value is the "we fell back"
// warning flag Config carries.
func Detect(tz string, localtime string) (string, string, bool) {
	if tz != "" {
		if id, err := Get(tz); err == nil {
			return id, "TZ env", false
		}
		return Fallback, "UTC fallback", true
	}
	if info, err := os.Lstat(localtime); err == nil && info.Mode()&os.ModeSymlink != 0 {
		if target, err := filepath.EvalSymlinks(localtime); err == nil {
			const marker = "/zoneinfo/"
			if index := strings.Index(target, marker); index >= 0 {
				if id, err := Get(target[index+len(marker):]); err == nil {
					return id, "host /etc/localtime", false
				}
				return Fallback, "UTC fallback", true
			}
		}
	}
	return Fallback, "UTC fallback", true
}

// TZDBVersion is the advisory self-report of where zone data came from. Ruby
// answers with TZInfo's data source; Go's is the zoneinfo directory the
// runtime found, or its embedded copy.
func TZDBVersion() string {
	for _, dir := range []string{"/usr/share/zoneinfo", "/usr/share/lib/zoneinfo", "/usr/lib/locale/TZ"} {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return "Zoneinfo DataSource: " + dir
		}
	}
	return "Zoneinfo DataSource: embedded"
}
