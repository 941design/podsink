package fuzzy

import (
	"testing"
)

func TestLevenshteinDistance(t *testing.T) {
	tests := []struct {
		s1       string
		s2       string
		expected int
	}{
		{"", "", 0},
		{"a", "", 1},
		{"", "a", 1},
		{"abc", "abc", 0},
		{"ABC", "abc", 0}, // case insensitive
		{"kitten", "sitting", 3},
		{"saturday", "sunday", 3},
		{"podcast", "podkast", 1}, // typo
		{"tech", "teck", 1},       // typo
	}

	for _, tt := range tests {
		result := LevenshteinDistance(tt.s1, tt.s2)
		if result != tt.expected {
			t.Errorf("LevenshteinDistance(%q, %q) = %d; want %d", tt.s1, tt.s2, result, tt.expected)
		}
	}
}

func TestSimilarity(t *testing.T) {
	tests := []struct {
		s1          string
		s2          string
		minExpected float64
	}{
		{"abc", "abc", 1.0},
		{"ABC", "abc", 1.0},
		{"podcast", "podkast", 0.85},
		{"tech", "teck", 0.7},
		{"hello", "world", 0.0},
		{"", "", 1.0},
	}

	for _, tt := range tests {
		result := Similarity(tt.s1, tt.s2)
		if result < tt.minExpected {
			t.Errorf("Similarity(%q, %q) = %f; want >= %f", tt.s1, tt.s2, result, tt.minExpected)
		}
	}
}

func TestContainsFuzzy(t *testing.T) {
	tests := []struct {
		text     string
		query    string
		expected bool
	}{
		{"The Tech Podcast", "tech", true},
		{"The Tech Podcast", "TECH", true},
		{"The Tech Podcast", "teck", true}, // typo (1 char diff)
		{"The Tech Podcast", "tekh", true}, // typo (1 char diff)
		{"My Favorite Show", "favorite", true},
		{"My Favorite Show", "favorit", true},  // typo (1 char missing)
		{"The Rabbit Podcast", "rabitt", true}, // typo (1 extra letter)
		{"The Rabbit Podcast", "rabit", true},  // typo (1 missing letter)
		{"My Favorite Show", "xyz", false},
		{"Daily News Podcast", "news", true},
		{"Daily News Podcast", "newz", true}, // typo (1 char diff)
		{"", "query", false},
		{"text", "", false},
	}

	for _, tt := range tests {
		result := ContainsFuzzy(tt.text, tt.query)
		if result != tt.expected {
			t.Errorf("ContainsFuzzy(%q, %q) = %v; want %v", tt.text, tt.query, result, tt.expected)
		}
	}
}

func TestMatchScore(t *testing.T) {
	tests := []struct {
		text        string
		query       string
		minExpected float64
	}{
		{"The Tech Podcast", "tech", 0.9},
		{"Tech Podcast", "tech", 0.9},
		{"The Technology Show", "tech", 0.7},
		{"My Favorite Show", "favorite", 0.9},
		{"Completely Different", "tech", 0.0},
	}

	for _, tt := range tests {
		result := MatchScore(tt.text, tt.query)
		if result < tt.minExpected {
			t.Errorf("MatchScore(%q, %q) = %f; want >= %f", tt.text, tt.query, result, tt.minExpected)
		}
	}
}

func TestMatchScoreOrdering(t *testing.T) {
	query := "tech"
	texts := []string{
		"Tech Podcast",         // should score highest (prefix match)
		"Technology Podcast",   // should score high (contains substring)
		"The Tech Show",        // should score high (exact word match)
		"Completely Unrelated", // should score lowest
	}

	scores := make([]float64, len(texts))
	for i, text := range texts {
		scores[i] = MatchScore(text, query)
	}

	// Verify first scores higher than last
	if scores[0] <= scores[len(scores)-1] {
		t.Errorf("Expected %q (score=%f) to rank higher than %q (score=%f)",
			texts[0], scores[0], texts[len(scores)-1], scores[len(scores)-1])
	}

	// Verify no zero scores except for last one
	for i := 0; i < len(scores)-1; i++ {
		if scores[i] < 0.7 {
			t.Errorf("Expected %q to have reasonable score, got %f", texts[i], scores[i])
		}
	}
}
