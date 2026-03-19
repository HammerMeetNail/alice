package id

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

func New(prefix string) string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(err)
	}

	return prefix + "_" + time.Now().UTC().Format("20060102T150405.000000000") + "_" + hex.EncodeToString(raw[:])
}
