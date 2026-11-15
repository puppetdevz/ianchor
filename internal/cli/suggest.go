package cli

import (
	"sort"
	"strings"
)

// suggestSimilar mirrors commander's did-you-mean: Damerau-Levenshtein
// distance with a 0.4 similarity floor, best match only, and a two-item
// "one of" phrasing when several candidates tie.
func suggestSimilar(word string, candidates []string) string {
	const maxDistance = 3
	seen := make(map[string]bool, len(candidates))
	unique := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if !seen[c] {
			seen[c] = true
			unique = append(unique, c)
		}
	}
	candidates = unique

	stripDashes := strings.HasPrefix(word, "--")
	if stripDashes {
		word = strings.TrimPrefix(word, "--")
	}
	wordLen := len([]rune(word))

	bestDistance := maxDistance
	var similar []string
	for _, candidate := range candidates {
		name := candidate
		if stripDashes {
			name = strings.TrimPrefix(name, "--")
		}
		if len([]rune(name)) <= 1 {
			continue
		}
		nameLen := len([]rune(name))
		distance := damerauLevenshtein(word, name, maxDistance)
		maxLen := max(wordLen, nameLen) // >= 2: shorter names continued above
		similarity := float64(maxLen-distance) / float64(maxLen)
		if similarity <= 0.4 {
			continue
		}
		if distance < bestDistance {
			bestDistance = distance
			similar = []string{candidate}
		} else if distance == bestDistance {
			similar = append(similar, candidate)
		}
	}
	sort.SliceStable(similar, func(i, j int) bool { return similar[i] < similar[j] })
	suggestions := similar
	if len(suggestions) == 1 {
		return "\n(Did you mean " + suggestions[0] + "?)"
	}
	if len(suggestions) > 1 {
		return "\n(Did you mean one of " + strings.Join(suggestions, ", ") + "?)"
	}
	return ""
}

func containsString(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

// damerauLevenshtein computes the optimal string alignment distance, bailing
// out with a value above maxDistance when the lengths differ too much.
func damerauLevenshtein(a, b string, maxDistance int) int {
	ar, br := []rune(a), []rune(b)
	if diff := len(ar) - len(br); diff > maxDistance || -diff > maxDistance {
		return max(len(ar), len(br))
	}
	prevPrev := make([]int, len(br)+1)
	prev := make([]int, len(br)+1)
	curr := make([]int, len(br)+1)
	for j := 0; j <= len(br); j++ {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		curr[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && ar[i-1] == br[j-2] && ar[i-2] == br[j-1] {
				curr[j] = min(curr[j], prevPrev[j-2]+1)
			}
		}
		prevPrev, prev, curr = prev, curr, prevPrev
	}
	return prev[len(br)]
}
