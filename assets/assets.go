// Package assets embeds the typeface and licence into the binary so the
// app is a single file with no runtime dependencies.
package assets

import _ "embed"

//go:embed fonts/CourierPrime-Regular.ttf
var CourierPrimeRegular []byte

// FontLicence is embedded, not read by any code path, so the SIL OFL text
// travels inside the single-file binary alongside the font it covers - the
// licence's own requirement for redistribution. Do not remove as "unused".
//
//go:embed fonts/OFL.txt
var FontLicence []byte
