package mapping

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
)

// Location is where a mapping writes (OC-036): {kind:"couchbase", bucket, scope, collection}
// or {kind:"directory", folder}.
type Location = store.Location

// LocationFromV1 converts a 1.0 mapping target into a Couchbase location (OC-036 AC4).
func LocationFromV1(t Target) Location {
	return store.MappingTarget(t).Location()
}

// V1Target is the 1.0 form of a Couchbase location (zero for other kinds).
func V1Target(l Location) Target { return Target(l.MappingTarget()) }

// Folder rules (OC-038): up to three "/"-separated segments of [A-Za-z0-9._-], each at most
// 64 bytes, none "." or "..".
const (
	maxFolderDepth   = 3
	maxFolderSegment = 64
)

var folderSegment = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// ValidateLocation checks a location for a target of kind (the project's target type); ""
// skips the kind match. Problems are LOCATION_INVALID with the offending field.
func ValidateLocation(l Location, kind string) error {
	switch {
	case l.Kind == "":
		return locationInvalid("location.kind", "is required", "The location doesn't say which kind of target it's for.")
	case kind != "" && l.Kind != kind:
		return locationInvalid("location.kind", "doesn't match the target",
			fmt.Sprintf("This project's target is %s, so a %s location doesn't apply.", kind, l.Kind))
	}
	switch l.Kind {
	case store.KindCouchbase:
		for field, v := range map[string]string{"bucket": l.Bucket, "scope": l.Scope, "collection": l.Collection} {
			if strings.TrimSpace(v) == "" {
				return locationInvalid("location."+field, "is required", "A Couchbase location needs a bucket, a scope and a collection.")
			}
		}
		if l.Folder != "" {
			return locationInvalid("location.folder", "isn't used by Couchbase", "A Couchbase location has no folder.")
		}
	case store.KindDirectory:
		if err := ValidateFolder(l.Folder); err != nil {
			return err
		}
		if l.Bucket != "" || l.Scope != "" || l.Collection != "" {
			return locationInvalid("location.bucket", "isn't used by a directory", "A directory location has only a folder.")
		}
	default:
		return locationInvalid("location.kind", "is unknown", fmt.Sprintf("%q isn't a target kind.", l.Kind))
	}
	return nil
}

// ValidateFolder checks a directory location's folder, relative to the target's root.
func ValidateFolder(folder string) error {
	bad := func(msg string) error { return locationInvalid("location.folder", msg, "Folder "+folder+" "+msg+".") }
	if folder == "" {
		return locationInvalid("location.folder", "is required", "A directory location needs a folder.")
	}
	if strings.HasPrefix(folder, "/") || strings.Contains(folder, `\`) {
		return bad("must be relative to the target's root")
	}
	segs := strings.Split(strings.TrimSuffix(folder, "/"), "/")
	if len(segs) > maxFolderDepth {
		return bad(fmt.Sprintf("is nested more than %d levels", maxFolderDepth))
	}
	for _, s := range segs {
		switch {
		case s == "" || s == "." || s == "..":
			return bad("has an empty, \".\" or \"..\" segment")
		case len(s) > maxFolderSegment:
			return bad(fmt.Sprintf("has a segment longer than %d bytes", maxFolderSegment))
		case !folderSegment.MatchString(s):
			return bad("may only use letters, digits, \".\", \"_\" and \"-\"")
		}
	}
	return nil
}

func locationInvalid(field, msg, detail string) problem.Problem {
	p := problem.New(http.StatusUnprocessableEntity, problem.CodeLocationInvalid, "Invalid location", detail)
	p.FieldErrors = []problem.FieldError{{Field: field, Message: msg}}
	return p
}
