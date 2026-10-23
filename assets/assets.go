package assets

import _ "embed"

//go:embed ascii-art.txt
var markedupASCIIArt string

// MarkedupASCIIArt returns the embedded startup ASCII art.
func MarkedupASCIIArt() string {
	return markedupASCIIArt
}
