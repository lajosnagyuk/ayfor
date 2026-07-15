// Package assets embeds the typeface and licence into the binary so the
// app is a single file with no runtime dependencies.
package assets

import (
	"embed"
)

//go:embed fonts/CourierPrime-Regular.ttf
var CourierPrimeRegular []byte

// FontLicence is embedded, not read by any code path, so the SIL OFL text
// travels inside the single-file binary alongside the font it covers - the
// licence's own requirement for redistribution. Do not remove as "unused".
//
//go:embed fonts/OFL.txt
var FontLicence []byte

// TypewriterReleases contains immutable, canonical package archives. Old
// releases stay embedded forever so documents bound to their exact digest do
// not become unreadable when a newer built-in release is added.
//
//go:embed typewriter-releases/*.aytw
var TypewriterReleases embed.FS
