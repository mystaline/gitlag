package gitea

import (
	"fmt"
	"strings"
)

// buildUnifiedDiff produces a unified diff between oldContent and newContent
// using LCS. Context lines = 3 (standard). Line numbers in @@ headers are omitted
// intentionally — the AI reviewer doesn't need them.
func buildUnifiedDiff(filename, oldContent, newContent string) string {
	if oldContent == newContent {
		return ""
	}

	old := splitLines(oldContent)
	nw := splitLines(newContent)

	edits := lcsEdits(old, nw)

	const ctx = 3
	var sb strings.Builder
	fmt.Fprintf(&sb, "--- a/%s\n+++ b/%s\n", filename, filename)

	// Collect change positions
	var changeIdx []int
	for i, e := range edits {
		if e.kind != 0 {
			changeIdx = append(changeIdx, i)
		}
	}
	if len(changeIdx) == 0 {
		return ""
	}

	// Merge nearby change ranges into hunks
	type span struct{ lo, hi int }
	var spans []span
	lo := maxInt(0, changeIdx[0]-ctx)
	hi := minInt(len(edits)-1, changeIdx[0]+ctx)
	for _, c := range changeIdx[1:] {
		if c-ctx <= hi {
			hi = minInt(len(edits)-1, c+ctx)
		} else {
			spans = append(spans, span{lo, hi})
			lo = maxInt(0, c-ctx)
			hi = minInt(len(edits)-1, c+ctx)
		}
	}
	spans = append(spans, span{lo, hi})

	for _, s := range spans {
		sb.WriteString("@@\n")
		for i := s.lo; i <= s.hi; i++ {
			e := edits[i]
			switch e.kind {
			case -1:
				fmt.Fprintf(&sb, "-%s\n", e.text)
			case 1:
				fmt.Fprintf(&sb, "+%s\n", e.text)
			default:
				fmt.Fprintf(&sb, " %s\n", e.text)
			}
		}
	}

	return sb.String()
}

type lineEdit struct {
	kind int // -1=delete, 0=equal, +1=insert
	text string
}

// lcsEdits computes a minimal edit script using LCS.
func lcsEdits(old, nw []string) []lineEdit {
	m, n := len(old), len(nw)

	// DP table for LCS lengths (bounded by maxLCSLines to avoid OOM on huge files)
	const maxLCSLines = 2000
	if m > maxLCSLines || n > maxLCSLines {
		// Fall back to simple append for very large files
		var edits []lineEdit
		for _, l := range old {
			edits = append(edits, lineEdit{-1, l})
		}
		for _, l := range nw {
			edits = append(edits, lineEdit{1, l})
		}
		return edits
	}

	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := m - 1; i >= 0; i-- {
		for j := n - 1; j >= 0; j-- {
			if old[i] == nw[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	var edits []lineEdit
	i, j := 0, 0
	for i < m && j < n {
		if old[i] == nw[j] {
			edits = append(edits, lineEdit{0, old[i]})
			i++
			j++
		} else if dp[i+1][j] >= dp[i][j+1] {
			edits = append(edits, lineEdit{-1, old[i]})
			i++
		} else {
			edits = append(edits, lineEdit{1, nw[j]})
			j++
		}
	}
	for ; i < m; i++ {
		edits = append(edits, lineEdit{-1, old[i]})
	}
	for ; j < n; j++ {
		edits = append(edits, lineEdit{1, nw[j]})
	}
	return edits
}

func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
