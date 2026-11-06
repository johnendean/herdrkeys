// Package device carries the Keybow firmware inside the binary, so the host
// and the board it provisions always speak the same protocol.
//
// Only boot.py and code.py are ever written to a board; this file stays here.
package device

import _ "embed"

//go:embed boot.py
var Boot []byte

//go:embed code.py
var Code []byte
