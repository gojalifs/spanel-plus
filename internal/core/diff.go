package core

import "strings"

// DiffKind classifies one line of a config diff.
type DiffKind string

const (
	DiffContext DiffKind = "context"
	DiffRemove  DiffKind = "remove"
	DiffAdd     DiffKind = "add"
)

// DiffLine is one rendered diff line.
type DiffLine struct {
	Kind DiffKind `json:"kind"`
	Text string   `json:"text"`
}

// DiffLines computes a line-based diff (LCS) between two texts.
// Generated Apache configs are small (tens of lines), so the quadratic
// dynamic program is comfortably fast and fully deterministic.
func DiffLines(a, b string) []DiffLine {
	aLines := splitLines(a)
	bLines := splitLines(b)
	n, m := len(aLines), len(bLines)

	// lcs[i][j] = LCS length of aLines[i:] and bLines[j:].
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if aLines[i] == bLines[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	var out []DiffLine
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case aLines[i] == bLines[j]:
			out = append(out, DiffLine{Kind: DiffContext, Text: aLines[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, DiffLine{Kind: DiffRemove, Text: aLines[i]})
			i++
		default:
			out = append(out, DiffLine{Kind: DiffAdd, Text: bLines[j]})
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, DiffLine{Kind: DiffRemove, Text: aLines[i]})
	}
	for ; j < m; j++ {
		out = append(out, DiffLine{Kind: DiffAdd, Text: bLines[j]})
	}
	return out
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
