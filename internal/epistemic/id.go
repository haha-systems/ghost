package epistemic

import (
	"crypto/rand"
	"encoding/hex"
)

func newID() (ID, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return ID("ces_" + hex.EncodeToString(raw[:])), nil
}
