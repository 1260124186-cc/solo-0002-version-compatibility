package semver

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a stable three-part semantic version.
type Version struct {
	Major uint64
	Minor uint64
	Patch uint64
}

func Parse(raw string) (Version, error) {
	var v Version
	parts := strings.Split(raw, ".")
	if len(parts) != 3 || len(raw) > 32 {
		return v, fmt.Errorf("version %q must have three numeric parts", raw)
	}
	values := []*uint64{&v.Major, &v.Minor, &v.Patch}
	for i, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return Version{}, fmt.Errorf("invalid numeric part in %q", raw)
		}
		for _, c := range part {
			if c < '0' || c > '9' {
				return Version{}, fmt.Errorf("only stable numeric versions are supported: %q", raw)
			}
		}
		n, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return Version{}, fmt.Errorf("numeric part exceeds range in %q", raw)
		}
		*values[i] = n
	}
	return v, nil
}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

func (v Version) Compare(other Version) int {
	a := [3]uint64{v.Major, v.Minor, v.Patch}
	b := [3]uint64{other.Major, other.Minor, other.Patch}
	for i := range a {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}
