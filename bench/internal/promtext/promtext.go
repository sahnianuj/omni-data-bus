// Package promtext reads single values out of a Prometheus text exposition (the worker's
// /metrics), without a client library.
package promtext

import (
	"bufio"
	"io"
	"strconv"
	"strings"
)

// Value returns the value of the first sample named name (any labels); ok is false when the
// metric isn't there.
func Value(r io.Reader, name string) (v float64, ok bool, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' || !strings.HasPrefix(line, name) {
			continue
		}
		rest := line[len(name):]
		if rest == "" || (rest[0] != ' ' && rest[0] != '{') {
			continue // a longer name with the same prefix
		}
		if rest[0] == '{' {
			end := strings.LastIndexByte(rest, '}')
			if end < 0 {
				continue
			}
			rest = rest[end+1:]
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		v, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			return 0, false, err
		}
		return v, true, nil
	}
	return 0, false, sc.Err()
}
