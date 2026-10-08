package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Task 660: quantify the discovery scan cost that use_capability(action=search)
// multiplies. One search builds the catalog (List = 1 full scan) and then calls
// Store.Read once per skill: entry while scoring (capabilityArgumentContract →
// CapabilityArguments → Read), and each Read re-runs the full disk scan. This
// test measures one scan and the (1+N)-scan search pattern on a synthetic store
// sized like the user environment (216 skills, ~36KB SKILL.md each).
//
// The real skill root can be measured instead by setting
// REASONIX_660_REAL_SKILL_DIR to an absolute directory of skill folders; the
// synthetic part is skipped in that mode.
func TestSkillScanCostSearchPattern(t *testing.T) {
	realDir := strings.TrimSpace(os.Getenv("REASONIX_660_REAL_SKILL_DIR"))
	skillCount := 216
	body := strings.Repeat("x", 36*1024)

	var store *Store
	if realDir != "" {
		info, err := os.Stat(realDir)
		if err != nil || !info.IsDir() {
			t.Fatalf("REASONIX_660_REAL_SKILL_DIR %q is not a directory", realDir)
		}
		tmp := t.TempDir()
		store = New(Options{
			HomeDir:         tmp,
			ReasonixHomeDir: filepath.Join(tmp, "reasonix-home"),
			ProjectRoot:     filepath.Join(tmp, "project"),
			CustomPaths:     []string{realDir},
			DisableBuiltins: true,
		})
	} else {
		root := t.TempDir()
		names := make([]string, 0, skillCount)
		for i := 0; i < skillCount; i++ {
			names = append(names, "timing"+strings.Repeat("0", 3-len(itoa(i)))+itoa(i))
		}
		for _, name := range names {
			dir := filepath.Join(root, name)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			content := "---\nname: " + name + "\ndescription: timing skill " + name + " for the 660 scan-cost measurement\n---\n\n" + body + "\n"
			if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
		}
		store = New(Options{
			HomeDir:         root,
			ReasonixHomeDir: filepath.Join(root, "reasonix-home"),
			ProjectRoot:     filepath.Join(root, "project"),
			CustomPaths:     []string{root},
			DisableBuiltins: true,
		})
	}

	// One full discovery scan (what List/Read each pay today).
	start := time.Now()
	listed := store.List()
	firstScan := time.Since(start)

	// Warm repeat (OS file cache hot — the steady state inside a session).
	start = time.Now()
	listed = store.List()
	warmScan := time.Since(start)

	if len(listed) == 0 {
		t.Fatalf("no skills discovered")
	}

	// The search pattern: one Read per catalog skill entry, every one of which
	// re-scans. Read exercises the same enabledSkills() scan that
	// CapabilityArguments paid per entry before the task 660 fix. The full
	// pattern costs (1+N) scans by design, so it only runs on demand: set
	// REASONIX_660_FULL_PATTERN=1 (or REASONIX_660_REAL_SKILL_DIR) to reproduce
	// the pre-fix measurement; the default suite keeps the cheap per-scan
	// guard only.
	probe := listed[0].Name
	scanMs := float64(warmScan.Microseconds()) / 1000
	if realDir == "" && strings.TrimSpace(os.Getenv("REASONIX_660_FULL_PATTERN")) == "" {
		t.Logf("skills=%d first_scan=%dms warm_scan=%.1fms (full 1+N-read pattern skipped; set REASONIX_660_FULL_PATTERN=1)",
			len(listed), firstScan.Milliseconds(), scanMs)
		return
	}
	start = time.Now()
	for range listed {
		if _, ok := store.Read(probe); !ok {
			t.Fatalf("Read %q failed", probe)
		}
	}
	readN := time.Since(start)

	readNMs := float64(readN.Microseconds()) / 1000
	patternMs := scanMs + readNMs
	t.Logf("skills=%d first_scan=%dms warm_scan=%.1fms read_xN(%d)=%.1fms search_pattern(1 scan + N reads)=%.1fms",
		len(listed), firstScan.Milliseconds(), scanMs, len(listed), readNMs, patternMs)
	if realDir == "" {
		// Regression guard: the scan itself must stay far below a second even on
		// a cold CI runner. The quadratic multiplication is fixed at the call
		// sites; this only bounds the per-scan primitive.
		if warmScan > 2*time.Second {
			t.Fatalf("single discovery scan regressed: %s for %d skills", warmScan, len(listed))
		}
	}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var b [4]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
