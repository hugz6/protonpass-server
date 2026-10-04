package passcli

import (
	"errors"
	"regexp"
	"strings"
	"unicode"
)

// RegExp for SHARE and ITEM
var idPattern = regexp.MustCompile(`^[A-Za-z0-9_=-]+$`)

func ValidateURI(uri string) error {
	// check if prefix is valid
	uri, validPrefix := strings.CutPrefix(uri, "pass://")
	if !validPrefix {
		return errors.New("prefix should start with pass://")
	}

	// splits parts with /
	parts := strings.Split(uri, "/")
	partsCount := len(parts)
	if partsCount <= 1 || partsCount > 3 {
		return errors.New("uri must be pass://SHARE/ITEM[/FIELD]")
	}
	share := parts[0]
	item := parts[1]

	// ensure field contains only printable character and is not empty
	if partsCount > 2 {
		if strings.TrimSpace(parts[2]) == "" {
			return errors.New("field cannot be empty")
		}
		if strings.ContainsFunc(parts[2], func(r rune) bool { return !unicode.IsPrint(r) }) {
			return errors.New("field must contains only printable char")
		}
	}

	// ensure share and item are matching the proton regexp
	if !idPattern.MatchString(share) {
		return errors.New("share doesn't match the proton regex")
	}
	if !idPattern.MatchString(item) {
		return errors.New("item doesn't match the proton regex")
	}

	return nil
}
