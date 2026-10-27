package tasks

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

func newID(now time.Time) (string, error) {
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", fmt.Errorf("generate job id: %w", err)
	}
	return now.Format("20060102-150405") + "-" + hex.EncodeToString(suffix[:]), nil
}
