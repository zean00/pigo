package a2a

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

func NewID(prefix string) string {
	var data [8]byte
	if _, err := rand.Read(data[:]); err != nil {
		return strings.TrimSuffix(prefix, "-") + "-0000000000000000"
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return hex.EncodeToString(data[:])
	}
	return prefix + "-" + hex.EncodeToString(data[:])
}
