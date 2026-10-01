package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"omnidatabus/internal/connectors/target/directory"
)

const docUsage = `usage: odb doc <command>

commands:
  path --folder F [--layout flat|sharded|key-path] [--gzip] KEY
        print where the document with KEY lands (relative to the target folder)
  get  --root DIR --folder F VALUE...
        print the document; VALUEs are the key template's columns in order (e.g. 1001 3),
        or the whole key; the layout and template come from DIR/_odb/run.json`

// runDoc finds documents in a directory target's output by primary key (OC-039 AC5).
func runDoc(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, docUsage)
		return 2
	}
	fs := flag.NewFlagSet("doc "+args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	folder := fs.String("folder", "", "the mapping's folder, e.g. orders")
	layout := fs.String("layout", "flat", "flat | sharded | key-path")
	gz := fs.Bool("gzip", false, "files are .json.gz")
	root := fs.String("root", "", "the target folder")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	cfg := directory.Config{Layout: *layout, Compression: "none"}
	if *gz {
		cfg.Compression = "gzip"
	}
	switch args[0] {
	case "path":
		if fs.NArg() != 1 || *folder == "" {
			fmt.Fprintln(stderr, docUsage)
			return 2
		}
		p, hashed := cfg.DocPath(*folder, fs.Arg(0))
		fmt.Fprintln(stdout, p)
		if hashed {
			fmt.Fprintln(stderr, "note: the key is too long for a file name; its file is named by a hash (see the manifest)")
		}
		return 0
	case "get":
		if fs.NArg() == 0 || *folder == "" || *root == "" {
			fmt.Fprintln(stderr, docUsage)
			return 2
		}
		path, doc, err := directory.LookupFile(*root, *folder, fs.Args(), cfg)
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(stderr, "no document with that key (%s doesn't exist)\n", path)
			return 1
		}
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, string(doc))
		return 0
	}
	fmt.Fprintf(stderr, "unknown doc command %q\n\n%s\n", args[0], docUsage)
	return 2
}
