// Package merge implements a small, dependency-free line-based three-way
// merge (diff3), used by worktree jobs to reconcile a file that changed on
// both replicas relative to a common base.
//
// The caller decides what to do with the outcome:
//   - Clean == true : write Result.Merged to both replicas.
//   - Clean == false: keep one side live and write the other as a
//     ".mergefail.<timestamp>" sidecar (see ADR-001). Result.Merged still holds
//     a best-effort rendering with conflict markers for reporting, but callers
//     should not silently write markers into a live note.
package merge

import "strings"

// Result is the outcome of a three-way merge.
type Result struct {
	Merged    string // merged text (with conflict markers when Clean is false)
	Clean     bool   // true when no hunk conflicted
	Conflicts int    // number of conflicting hunks
}

// Conflict marker labels used when rendering a non-clean merge for reporting.
const (
	markerOurs = "<<<<<<< ours"
	markerBase = "||||||| base"
	markerMid  = "======="
	markerEnd  = ">>>>>>> theirs"
)

// Merge performs a line-based three-way merge of base (common ancestor),
// a (ours) and b (theirs). It is deterministic and allocates O(n*m) for the
// per-side LCS, which is fine for the small text notes this tool syncs.
func Merge(base, a, b string) Result {
	if a == b {
		return Result{Merged: a, Clean: true}
	}
	if a == base {
		return Result{Merged: b, Clean: true}
	}
	if b == base {
		return Result{Merged: a, Clean: true}
	}

	oLines := splitLines(base)
	aLines := splitLines(a)
	bLines := splitLines(b)

	chunks := diff3Chunks(oLines, aLines, bLines)

	var sb strings.Builder
	conflicts := 0
	for _, c := range chunks {
		if !c.conflict {
			for _, ln := range c.lines {
				sb.WriteString(ln)
			}
			continue
		}
		conflicts++
		writeMarkerBlock(&sb, c.aLines, c.oLines, c.bLines)
	}

	return Result{Merged: sb.String(), Clean: conflicts == 0, Conflicts: conflicts}
}

type chunk struct {
	conflict bool
	lines    []string // resolved lines (when !conflict)
	aLines   []string // ours (when conflict)
	oLines   []string // base (when conflict)
	bLines   []string // theirs (when conflict)
}

// diff3Chunks aligns a and b against the common base o and produces a sequence
// of stable (agreed) and unstable (changed) chunks. Unstable chunks that
// changed on only one side, or identically on both, resolve cleanly; only
// genuinely divergent regions are marked conflict.
func diff3Chunks(o, a, b []string) []chunk {
	ma := matchIndex(o, a) // o-index -> a-index for LCS-matched lines
	mb := matchIndex(o, b)

	var chunks []chunk
	oi, ai, bi := 0, 0, 0

	for oi < len(o) {
		// If the current base line is matched in both a and b exactly at the
		// current cursors, it is agreed by all three: emit it and step forward.
		if aj, aok := ma[oi]; aok && aj == ai {
			if bj, bok := mb[oi]; bok && bj == bi {
				chunks = append(chunks, chunk{conflict: false, lines: []string{o[oi]}})
				oi, ai, bi = oi+1, ai+1, bi+1
				continue
			}
		}

		// Unstable region: advance base to the next line matched in both sides
		// at consistent positions, collecting the divergent slices.
		oStart, aStart, bStart := oi, ai, bi
		oi, ai, bi = nextSyncPoint(o, a, b, ma, mb, oi, ai, bi)

		chunks = append(chunks, resolveChunk(o[oStart:oi], a[aStart:ai], b[bStart:bi]))
	}

	// Trailing lines in a / b beyond the last base match.
	if ai < len(a) || bi < len(b) {
		chunks = append(chunks, resolveChunk(nil, a[ai:], b[bi:]))
	}

	return chunks
}

// nextSyncPoint scans forward from an unstable region to the next base line
// that is matched in both a and b at mutually consistent offsets, returning the
// base/a/b cursors at that sync point (or the ends of each slice).
func nextSyncPoint(o, a, b []string, ma, mb map[int]int, oi, ai, bi int) (int, int, int) {
	for k := oi; k < len(o); k++ {
		aj, aok := ma[k]
		bj, bok := mb[k]
		if aok && bok && aj >= ai && bj >= bi {
			return k, aj, bj
		}
	}
	return len(o), len(a), len(b)
}

// resolveChunk decides an unstable region: clean if only one side changed (or
// both changed identically), conflict otherwise.
func resolveChunk(o, a, b []string) chunk {
	aEqO := equal(a, o)
	bEqO := equal(b, o)
	switch {
	case aEqO && bEqO:
		return chunk{conflict: false, lines: append([]string(nil), o...)}
	case aEqO:
		return chunk{conflict: false, lines: append([]string(nil), b...)} // only b changed
	case bEqO:
		return chunk{conflict: false, lines: append([]string(nil), a...)} // only a changed
	case equal(a, b):
		return chunk{conflict: false, lines: append([]string(nil), a...)} // same change both sides
	default:
		return chunk{conflict: true, aLines: a, oLines: o, bLines: b}
	}
}

// matchIndex returns a map from index-in-o to index-in-x for lines on the
// longest common subsequence of o and x.
func matchIndex(o, x []string) map[int]int {
	n, m := len(o), len(x)
	// DP table of LCS lengths.
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if o[i] == x[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	match := make(map[int]int)
	i, j := 0, 0
	for i < n && j < m {
		if o[i] == x[j] {
			match[i] = j
			i++
			j++
		} else if dp[i+1][j] >= dp[i][j+1] {
			i++
		} else {
			j++
		}
	}
	return match
}

func writeMarkerBlock(sb *strings.Builder, a, o, b []string) {
	writeLine(sb, markerOurs)
	for _, ln := range a {
		sb.WriteString(ln)
	}
	ensureNL(sb, a)
	writeLine(sb, markerBase)
	for _, ln := range o {
		sb.WriteString(ln)
	}
	ensureNL(sb, o)
	writeLine(sb, markerMid)
	for _, ln := range b {
		sb.WriteString(ln)
	}
	ensureNL(sb, b)
	writeLine(sb, markerEnd)
}

func writeLine(sb *strings.Builder, s string) {
	sb.WriteString(s)
	sb.WriteString("\n")
}

// ensureNL adds a newline if the last emitted slice line lacked one, so markers
// stay on their own lines.
func ensureNL(sb *strings.Builder, lines []string) {
	if len(lines) == 0 {
		return
	}
	if !strings.HasSuffix(lines[len(lines)-1], "\n") {
		sb.WriteString("\n")
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// splitLines splits s into lines, each retaining its trailing "\n". A final
// line without a newline is kept as-is. The empty string yields no lines.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
