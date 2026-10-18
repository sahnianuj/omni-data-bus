// Package legal holds the licence files every Omni-Data-Bus artifact carries (OC-189): copies of
// the repository's LICENSE, NOTICE and THIRD_PARTY_NOTICES.md, kept identical by
// `make notices` (scripts/third-party-notices.py --check fails when they differ). The server
// serves them at /licenses.txt and adds them to exports that include odb.
package legal

import _ "embed"

// SPDX is the licence of Omni-Data-Bus.
const SPDX = "Apache-2.0"

// Copyright is the copyright line, as in NOTICE.
const Copyright = "Copyright 2026 Couchbase, Inc."

// Author credits the author, as in NOTICE.
const Author = "Anuj Sahni, Principal Cloud & Solutions Architect"

// License is the Apache License 2.0 text.
//
//go:embed LICENSE
var License []byte

// Notice is NOTICE: the product, copyright, author and attributions.
//
//go:embed NOTICE
var Notice []byte

// ThirdParty is THIRD_PARTY_NOTICES.md: every shipped dependency, its licence and licence text.
//
//go:embed THIRD_PARTY_NOTICES.md
var ThirdParty []byte

// File is one of the licence files, by its name.
type File struct {
	Name string
	Data []byte
}

// Files are the three files, in the order a reader wants them.
func Files() []File {
	return []File{{"LICENSE", License}, {"NOTICE", Notice}, {"THIRD_PARTY_NOTICES.md", ThirdParty}}
}

// Line is the one-line licence statement for --version and the About page.
func Line() string { return "Licensed under " + SPDX + " · " + Copyright }
