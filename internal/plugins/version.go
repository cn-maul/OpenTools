package plugins

import (
	"errors"
	"strings"
)

// errorsNew is a tiny local helper so storeclient avoids importing errors
// just for static strings while keeping messages consistent with the rest
// of the package.
func errorsNew(message string) error { return errors.New(message) }

// compareVersion compares dotted numeric versions. A missing segment is
// zero; a "-beta" suffix is ignored, matching the release convention.
func compareVersion(left, right string) int {
	a := versionParts(left)
	b := versionParts(right)
	for i := 0; i < max(len(a), len(b)); i++ {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	return 0
}

func versionParts(value string) []int {
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == '.' || r == '-' || r == '+'
	})
	out := make([]int, 0, len(fields))
	for _, field := range fields {
		number := 0
		for _, r := range field {
			if r < '0' || r > '9' {
				break
			}
			number = number*10 + int(r-'0')
		}
		out = append(out, number)
	}
	return out
}
