package service

import (
	"encoding/json"
	"fmt"
	"math/bits"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 从实际下发的题目文字重新解题，不使用生成器的内部问题结构或求解函数。
func TestRandomQualityGeneratedReferencesMatchQuestionText(t *testing.T) {
	samples, err := generateQualityRandomSamples(48, strings.NewReader("question-oracle!"))
	require.NoError(t, err)
	for i, sample := range samples {
		t.Run(fmt.Sprintf("%s/%d", sample.Metadata.Family, i), func(t *testing.T) {
			switch sample.Metadata.Family {
			case "paths":
				verifyQualityPathPrompt(t, sample.Prompt, sample.ExpectedAnswer)
			case "weighted_xor":
				verifyQualityXORPrompt(t, sample.Prompt, sample.ExpectedAnswer)
			default:
				t.Fatalf("未知题型：%s", sample.Metadata.Family)
			}
		})
	}
}

func verifyQualityPathPrompt(t *testing.T, prompt, reference string) {
	t.Helper()
	points := regexp.MustCompile(`\((\d+),(\d+)\)`).FindAllStringSubmatch(prompt, -1)
	// 起点、终点、两个动作向量、三个禁点和两个检查点。
	require.Len(t, points, 9)
	coordinate := func(index int) [2]int {
		x, err := strconv.Atoi(points[index][1])
		require.NoError(t, err)
		y, err := strconv.Atoi(points[index][2])
		require.NoError(t, err)
		return [2]int{x, y}
	}
	destination := coordinate(1)
	blocked := map[[2]int]bool{coordinate(4): true, coordinate(5): true, coordinate(6): true}
	checkpoints := map[[2]int]bool{coordinate(7): true, coordinate(8): true}
	turnMatch := regexp.MustCompile(`恰好转向(\d+)次`).FindStringSubmatch(prompt)
	require.Len(t, turnMatch, 2)
	wantedTurns, err := strconv.Atoi(turnMatch[1])
	require.NoError(t, err)
	require.Contains(t, prompt, "检查点")
	require.Contains(t, prompt, "必须恰好经过一个")
	require.Contains(t, prompt, "不得包含连续三个R")
	require.Contains(t, prompt, "规定R<U")
	var expected, actual struct {
		Count int    `json:"count"`
		First string `json:"first"`
	}
	steps := destination[0] + destination[1]
	// 枚举完整动作串，再按题目各条条件逐条过滤；不用生产代码的状态计数算法。
	for mask := 0; mask < 1<<steps; mask++ {
		if bits.OnesCount(uint(mask)) != destination[0] {
			continue
		}
		path := make([]byte, steps)
		position, hits, turns, valid := [2]int{}, 0, 0, true
		for step := range path {
			if mask>>step&1 == 1 {
				path[step] = 'R'
				position[0]++
			} else {
				path[step] = 'U'
				position[1]++
			}
			if step > 0 && path[step] != path[step-1] {
				turns++
			}
			if blocked[position] {
				valid = false
			}
			if checkpoints[position] {
				hits++
			}
		}
		word := string(path)
		if !valid || hits != 1 || turns != wantedTurns || strings.Contains(word, "RRR") {
			continue
		}
		expected.Count++
		if expected.First == "" || word < expected.First {
			expected.First = word
		}
	}
	require.NoError(t, json.Unmarshal([]byte(reference), &actual))
	require.Equal(t, expected, actual, prompt)
}

func verifyQualityXORPrompt(t *testing.T, prompt, reference string) {
	t.Helper()
	rows := regexp.MustCompile(`(?m)^\d+\. ([A-F](?: XOR [A-F])*) = ([01])；违背代价(\d+)$`).FindAllStringSubmatch(prompt, -1)
	require.Len(t, rows, 11)
	require.Contains(t, prompt, "不是让错误条数最少")
	require.Contains(t, prompt, "所有最优赋值")
	var expected, actual struct {
		Penalty     int      `json:"penalty"`
		Assignments []string `json:"assignments"`
	}
	expected.Penalty = 1000
	for assignment := 0; assignment < 64; assignment++ {
		word := fmt.Sprintf("%06b", assignment)
		penalty := 0
		for _, row := range rows {
			value := 0
			for _, variable := range strings.Split(row[1], " XOR ") {
				value += int(word[variable[0]-'A'] - '0')
			}
			if value%2 != int(row[2][0]-'0') {
				weight, err := strconv.Atoi(row[3])
				require.NoError(t, err)
				penalty += weight
			}
		}
		if penalty < expected.Penalty {
			expected.Penalty = penalty
			expected.Assignments = nil
		}
		if penalty == expected.Penalty {
			expected.Assignments = append(expected.Assignments, word)
		}
	}
	require.NoError(t, json.Unmarshal([]byte(reference), &actual))
	require.Equal(t, expected, actual, prompt)
}
