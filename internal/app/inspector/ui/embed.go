// Package ui embeds the trace viewer's static assets.
package ui

import "embed"

// Assets carries the SPA shell. The trace server strips the "assets/" prefix
// to serve files at "/".
//
//go:embed assets
var Assets embed.FS
