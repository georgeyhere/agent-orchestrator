package spec

import (
	"path"
	"strings"
)

// globsOverlap reports whether some path could match both owns globs. Globs
// use path.Match syntax per segment, ** matches any number of segments, and a
// trailing slash owns the whole directory. When two wildcard segments meet,
// the check compares their literal prefixes and suffixes, so it can report an
// overlap that no real file produces, but it never misses one.
func globsOverlap(a, b string) bool {
	return segmentsOverlap(globSegments(a), globSegments(b))
}

func globSegments(glob string) []string {
	if strings.HasSuffix(glob, "/") {
		glob = strings.TrimSuffix(glob, "/") + "/**"
	}
	return strings.Split(path.Clean(glob), "/")
}

func segmentsOverlap(a, b []string) bool {
	switch {
	case len(a) == 0 && len(b) == 0:
		return true
	case len(a) == 0:
		return allDoubleStar(b)
	case len(b) == 0:
		return allDoubleStar(a)
	case a[0] == "**":
		return segmentsOverlap(a[1:], b) || segmentsOverlap(a, b[1:])
	case b[0] == "**":
		return segmentsOverlap(a, b[1:]) || segmentsOverlap(a[1:], b)
	}
	return segmentOverlap(a[0], b[0]) && segmentsOverlap(a[1:], b[1:])
}

func allDoubleStar(segs []string) bool {
	for _, s := range segs {
		if s != "**" {
			return false
		}
	}
	return true
}

func segmentOverlap(a, b string) bool {
	am, bm := hasMeta(a), hasMeta(b)
	switch {
	case !am && !bm:
		return a == b
	case !am:
		ok, _ := path.Match(b, a)
		return ok
	case !bm:
		ok, _ := path.Match(a, b)
		return ok
	}
	ap, as := literalEnds(a)
	bp, bs := literalEnds(b)
	return compatible(ap, bp, strings.HasPrefix) && compatible(as, bs, strings.HasSuffix)
}

// literalEnds returns the literal text before the first and after the last
// wildcard of a segment pattern.
func literalEnds(seg string) (prefix, suffix string) {
	first := strings.IndexAny(seg, `*?[\`)
	last := strings.LastIndexAny(seg, `*?]\`)
	return seg[:first], seg[last+1:]
}

func compatible(x, y string, has func(string, string) bool) bool {
	return has(x, y) || has(y, x)
}

func hasMeta(seg string) bool {
	return strings.ContainsAny(seg, `*?[\`)
}
