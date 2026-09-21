// Package inspector embeds the shared UI so normal use requires only the Go binary.
package inspector

import "embed"

// Assets contains only files served by the broker, excluding development sources.
//
//go:embed public/index.html public/app.js public/style.css public/brote-plant.png
var Assets embed.FS
