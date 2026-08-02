// Package cli contains thin Go command adapters over the shared core.
package cli

import (
	"encoding/json"
	"io"

	"tasks-go/internal/config"
)

// WriteConfigJSON emits the resolver-owned portion of `tasks config --json`.
// Settings not resolved by internal/config deliberately do not appear here:
// their owning slices must compose them before the public Go CLI claims full
// compatibility with Ruby's config envelope.
func WriteConfigJSON(out io.Writer, options config.Options) error {
	paths, err := config.Resolve(options)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(config.ConfigReport(paths))
}
