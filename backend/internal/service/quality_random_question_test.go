package service

import (
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	mathrand "math/rand/v2"
	"strings"
	"testing"
)

func TestRandomQualityBatchHasFreshBalancedQuestions(t *testing.T) {
	samples, err := generateQualityRandomSamples(8, strings.NewReader("abcdefghABCDEFGH"))
	require.NoError(t, err)
	require.Len(t, samples, 8)
	families, seen := map[string]int{}, map[string]bool{}
	for _, sample := range samples {
		require.NotEmpty(t, sample.Prompt)
		require.True(t, json.Valid([]byte(sample.ExpectedAnswer)))
		require.NotNil(t, sample.Metadata)
		require.False(t, seen[sample.Metadata.ID])
		seen[sample.Metadata.ID] = true
		families[sample.Metadata.Family]++
		require.NotContains(t, sample.Prompt, sample.ExpectedAnswer)
	}
	require.Equal(t, map[string]int{"paths": 4, "weighted_xor": 4}, families)
	other, err := generateQualityRandomSamples(8, strings.NewReader("ijklmnopIJKLMNOP"))
	require.NoError(t, err)
	require.NotEqual(t, samples[0].Metadata.ID, other[0].Metadata.ID)
	_, err = generateQualityRandomSamples(1, strings.NewReader("short"))
	require.ErrorContains(t, err, "entropy unavailable")
}

// Compare the state-counting solver against a separate whole-walk enumeration
// on a small synthetic grid, not a stored production question/answer fixture.
func TestRandomQualityPathOracleAgainstWalkEnumeration(t *testing.T) {
	for turns := 0; turns < 8; turns++ {
		p := qualityPathProblem{Width: 4, Height: 3, Turns: turns,
			Blocked:     [3]qualityGridPoint{{1, 1}, {2, 2}, {3, 0}},
			Checkpoints: [2]qualityGridPoint{{1, 0}, {0, 2}}}
		expected := qualityPathAnswer{}
		for mask := 0; mask < 1<<7; mask++ {
			x, y, hits, changes := 0, 0, 0, 0
			var word strings.Builder
			valid := true
			for i := 0; i < 7; i++ {
				if mask>>i&1 != 0 {
					x++
					_ = word.WriteByte('R')
				} else {
					y++
					_ = word.WriteByte('U')
				}
				for _, point := range p.Blocked {
					if point == (qualityGridPoint{x, y}) {
						valid = false
					}
				}
				for _, point := range p.Checkpoints {
					if point == (qualityGridPoint{x, y}) {
						hits++
					}
				}
			}
			path := word.String()
			for i := 1; i < len(path); i++ {
				if path[i] != path[i-1] {
					changes++
				}
			}
			if valid && x == p.Width && y == p.Height && hits == 1 && changes == turns && !strings.Contains(path, "RRR") {
				expected.Count++
				if expected.First == "" || path < expected.First {
					expected.First = path
				}
			}
		}
		require.Equal(t, expected, solveQualityPath(p))
	}
}

func TestRandomQualityXOROracleFindsAllMinimumWeightRepairs(t *testing.T) {
	rows := []qualityXORObservation{{[]int{0}, 0, 2}, {[]int{0}, 1, 5}, {[]int{1}, 0, 3}}
	answer := solveQualityXOR(rows)
	require.Equal(t, 2, answer.Penalty)
	var expected []string
	for i := 0; i < 16; i++ {
		expected = append(expected, fmt.Sprintf("10%04b", i))
	}
	require.Equal(t, expected, answer.Assignments)
	// Generated references must be nontrivial and bounded without publishing a
	// golden production question or relying on a real model as an oracle.
	rng := mathrand.New(mathrand.NewPCG(7, 11)) //nolint:gosec
	for i := 0; i < 8; i++ {
		_, reference, err := generateQualityXORQuestion(rng)
		require.NoError(t, err)
		value, ok := reference.(qualityXORAnswer)
		require.True(t, ok)
		require.Positive(t, value.Penalty)
		require.NotEmpty(t, value.Assignments)
		require.LessOrEqual(t, len(value.Assignments), 4)
	}
}
