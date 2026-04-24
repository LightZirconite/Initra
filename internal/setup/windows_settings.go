package setup

import (
	"sort"
	"strconv"
	"strings"
)

func parseWindowsProcessIDs(output string) []int {
	seen := map[int]bool{}
	var ids []int
	for _, field := range strings.Fields(output) {
		id, err := strconv.Atoi(strings.TrimSpace(field))
		if err != nil || id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}
