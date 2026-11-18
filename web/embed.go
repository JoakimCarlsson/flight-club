// Package web embeds the built Vite SPA so the server ships as one binary.
//
// A placeholder dist/index.html is committed so the package always compiles;
// `bun run build` regenerates it plus the hashed assets under dist/assets,
// which are what get embedded into a release build.
package web

import "embed"

// Dist holds the built SPA. Consumers should fs.Sub into "dist".
//
//go:embed all:dist
var Dist embed.FS
