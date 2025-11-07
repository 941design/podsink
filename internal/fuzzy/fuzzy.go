package fuzzy

import (
	"strings"
	"unicode"
)

// LevenshteinDistance calculates the edit distance between two strings.
func LevenshteinDistance(s1, s2 string) int {
	s1Lower := strings.ToLower(s1)
	s2Lower := strings.ToLower(s2)

	if len(s1Lower) == 0 {
		return len(s2Lower)
	}
	if len(s2Lower) == 0 {
		return len(s1Lower)
	}

	// Create a 2D slice for dynamic programming
	matrix := make([][]int, len(s1Lower)+1)
	for i := range matrix {
		matrix[i] = make([]int, len(s2Lower)+1)
		matrix[i][0] = i
	}
	for j := range matrix[0] {
		matrix[0][j] = j
	}

	// Calculate distances
	for i := 1; i <= len(s1Lower); i++ {
		for j := 1; j <= len(s2Lower); j++ {
			cost := 1
			if s1Lower[i-1] == s2Lower[j-1] {
				cost = 0
			}

			matrix[i][j] = min(
				matrix[i-1][j]+1,      // deletion
				matrix[i][j-1]+1,      // insertion
				matrix[i-1][j-1]+cost, // substitution
			)
		}
	}

	return matrix[len(s1Lower)][len(s2Lower)]
}

// Similarity returns a score between 0 and 1 indicating how similar two strings are.
// 1.0 means identical, 0.0 means completely different.
func Similarity(s1, s2 string) float64 {
	if s1 == "" && s2 == "" {
		return 1.0
	}

	s1Lower := strings.ToLower(s1)
	s2Lower := strings.ToLower(s2)

	maxLen := max(len(s1Lower), len(s2Lower))
	if maxLen == 0 {
		return 1.0
	}

	distance := LevenshteinDistance(s1, s2)
	return 1.0 - float64(distance)/float64(maxLen)
}

// ContainsFuzzy checks if the query exists within the text with tolerance for typos.
// Returns true if the similarity is above a threshold (0.7).
func ContainsFuzzy(text, query string) bool {
	if query == "" {
		return false
	}

	textLower := strings.ToLower(text)
	queryLower := strings.ToLower(query)

	// First try exact substring match
	if strings.Contains(textLower, queryLower) {
		return true
	}

	// Adaptive threshold based on query length
	// Longer queries can tolerate more typos (e.g., "rabitt" matching "rabbit")
	threshold := 0.65 // default for longer words (6+ chars)
	if len(queryLower) <= 3 {
		threshold = 0.8 // very strict for short queries
	} else if len(queryLower) <= 5 {
		threshold = 0.7 // moderately strict for medium queries
	}

	// Try to find fuzzy matches in the text
	words := strings.FieldsFunc(textLower, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})

	for _, word := range words {
		if Similarity(word, queryLower) >= threshold {
			return true
		}
	}

	// Also check if query words are fuzzy-contained in text
	queryWords := strings.FieldsFunc(queryLower, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})

	if len(queryWords) == 0 {
		return false
	}

	matchCount := 0
	for _, qWord := range queryWords {
		// Use threshold based on individual word length
		wordThreshold := 0.65
		if len(qWord) <= 3 {
			wordThreshold = 0.8
		} else if len(qWord) <= 5 {
			wordThreshold = 0.7
		}

		for _, tWord := range words {
			if Similarity(tWord, qWord) >= wordThreshold {
				matchCount++
				break
			}
		}
	}

	// If most query words match, consider it a match
	return float64(matchCount)/float64(len(queryWords)) >= 0.6
}

// MatchScore calculates a relevance score for how well text matches the query.
// Higher scores indicate better matches.
func MatchScore(text, query string) float64 {
	textLower := strings.ToLower(text)
	queryLower := strings.ToLower(query)

	// Exact match at start gets highest score
	if strings.HasPrefix(textLower, queryLower) {
		return 1.0
	}

	// Exact substring match gets high score
	if strings.Contains(textLower, queryLower) {
		return 0.95
	}

	// Calculate fuzzy match score
	words := strings.FieldsFunc(textLower, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})

	queryWords := strings.FieldsFunc(queryLower, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})

	if len(queryWords) == 0 {
		return 0.0
	}

	var totalScore float64
	for _, qWord := range queryWords {
		var bestMatch float64
		for _, tWord := range words {
			sim := Similarity(tWord, qWord)
			if sim > bestMatch {
				bestMatch = sim
			}
		}
		totalScore += bestMatch
	}

	// Scale fuzzy matches to be lower than exact matches
	return (totalScore / float64(len(queryWords))) * 0.9
}
