package agent

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// WorktreeMergeConflict names two fleet items whose worktree branches both
// change the same files relative to their merge base. Preflight surfaces the
// pair and the overlapping files so the caller can depends_on, split paths, or
// merge serially instead of discovering the clash only at merge time.
type WorktreeMergeConflict struct {
	TaskA string   `json:"taskA"`
	TaskB string   `json:"taskB"`
	RootA string   `json:"rootA"`
	RootB string   `json:"rootB"`
	Files []string `json:"files"`
}

// WorktreeRef is one worktree-backed fleet item as the detector sees it.
type WorktreeRef struct {
	ID   string
	Root string
}

const fleetGitProbeTimeout = 15 * time.Second

// DetectWorktreeMergeConflicts compares every unordered pair of worktree roots.
// Roots that are not Git worktrees of a shared repository are skipped (the
// caller still has write_paths preflight). Detection is read-only.
func DetectWorktreeMergeConflicts(ctx context.Context, items []WorktreeRef) ([]WorktreeMergeConflict, error) {
	type resolved struct {
		id, root, commonDir, head string
	}
	kept := make([]resolved, 0, len(items))
	for _, item := range items {
		root := strings.TrimSpace(item.Root)
		if root == "" {
			continue
		}
		id := strings.TrimSpace(item.ID)
		if id == "" {
			id = root
		}
		commonDir, err := gitOutput(ctx, root, "rev-parse", "--git-common-dir")
		if err != nil {
			// Not a git worktree: skip rather than fail the whole preflight.
			continue
		}
		head, err := gitOutput(ctx, root, "rev-parse", "HEAD")
		if err != nil {
			continue
		}
		kept = append(kept, resolved{
			id:        id,
			root:      root,
			commonDir: filepath.Clean(strings.TrimSpace(commonDir)),
			head:      strings.TrimSpace(head),
		})
	}
	var conflicts []WorktreeMergeConflict
	for i := range kept {
		for j := i + 1; j < len(kept); j++ {
			a, b := kept[i], kept[j]
			if !sameGitCommonDir(a.commonDir, b.commonDir) {
				continue
			}
			files, err := overlappingChangedFiles(ctx, a.root, a.head, b.root, b.head)
			if err != nil {
				return nil, fmt.Errorf("compare %s and %s: %w", a.id, b.id, err)
			}
			if len(files) == 0 {
				continue
			}
			conflicts = append(conflicts, WorktreeMergeConflict{
				TaskA: a.id, TaskB: b.id, RootA: a.root, RootB: b.root, Files: files,
			})
		}
	}
	return conflicts, nil
}

// overlappingChangedFiles returns the sorted intersection of files each branch
// changed relative to their merge base. A missing merge base (unrelated
// histories) reports no overlap: those branches cannot auto-merge anyway and
// the existing merge inspection owns that failure.
func overlappingChangedFiles(ctx context.Context, rootA, headA, rootB, headB string) ([]string, error) {
	base, err := gitOutput(ctx, rootA, "merge-base", headA, headB)
	if err != nil || strings.TrimSpace(base) == "" {
		return nil, nil
	}
	base = strings.TrimSpace(base)
	filesA, err := changedFilesSince(ctx, rootA, base, headA)
	if err != nil {
		return nil, err
	}
	filesB, err := changedFilesSince(ctx, rootB, base, headB)
	if err != nil {
		return nil, err
	}
	var overlap []string
	for file := range filesA {
		if filesB[file] {
			overlap = append(overlap, file)
		}
	}
	sort.Strings(overlap)
	return overlap, nil
}

func changedFilesSince(ctx context.Context, root, base, head string) (map[string]bool, error) {
	out, err := gitOutput(ctx, root, "diff", "--name-only", base, head)
	if err != nil {
		return nil, err
	}
	files := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			files[filepath.ToSlash(line)] = true
		}
	}
	return files, nil
}

func sameGitCommonDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("git directory is required")
	}
	probeCtx, cancel := context.WithTimeout(ctx, fleetGitProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, msg)
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return stdout.String(), nil
}

// validateConcurrentWorktreeConflicts is the fleet preflight hook. It only
// inspects pairs that can actually run at the same time, matching the
// write_paths rule: ordered tasks may legitimately share files.
func (p fleetPlan) validateConcurrentWorktreeConflicts(ctx context.Context, items []fleetTaskItem) error {
	idxByID := make(map[string]int, len(items))
	refs := make([]WorktreeRef, 0, len(items))
	for i, item := range items {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			id = fmt.Sprintf("%d", i+1)
		}
		idxByID[id] = i
		root := strings.TrimSpace(item.WorktreeRoot)
		if root == "" {
			continue
		}
		refs = append(refs, WorktreeRef{ID: id, Root: root})
	}
	if len(refs) < 2 {
		return nil
	}
	// Drop refs whose item is ordered against every other worktree-backed item.
	filtered := refs[:0:0]
	for _, ref := range refs {
		i := idxByID[ref.ID]
		for _, other := range refs {
			if other.ID == ref.ID {
				continue
			}
			if !p.ordered(i, idxByID[other.ID]) {
				filtered = append(filtered, ref)
				break
			}
		}
	}
	if len(filtered) < 2 {
		return nil
	}
	conflicts, err := DetectWorktreeMergeConflicts(ctx, filtered)
	if err != nil {
		return err
	}
	for _, c := range conflicts {
		ia, ib := idxByID[c.TaskA], idxByID[c.TaskB]
		if p.ordered(ia, ib) {
			continue
		}
		files := c.Files
		if len(files) > 8 {
			files = append(append([]string{}, files[:8]...), fmt.Sprintf("… (+%d more)", len(c.Files)-8))
		}
		return fmt.Errorf(
			"%s and %s can run at the same time and their worktree branches both change: %s; add a depends_on between them, split the files, or merge one first",
			p.describe(ia), p.describe(ib), strings.Join(files, ", "),
		)
	}
	return nil
}
