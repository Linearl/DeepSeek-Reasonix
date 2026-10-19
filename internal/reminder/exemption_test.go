package reminder

import (
	"os"
	"strings"
	"testing"
)

// TestPackageNeverImportsCrossSessionDelivery pins the tl 1000 structural
// exemption: reminder deliveries enter the owning session through the
// controller's follow-up enqueue, never through the cross-session mail face.
// If this package ever grows a sessioncollab (talk_to_session) dependency, the
// to==from self-send detection and receipt channel come into play and the
// structural exemption silently dies — this test fails first.
func TestPackageNeverImportsCrossSessionDelivery(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, banned := range []string{"sessioncollab", "sessioninbox"} {
			if strings.Contains(string(data), "reasonix/internal/"+banned) {
				t.Fatalf("%s imports %s — the reminder delivery face must stay inside the controller enqueue", name, banned)
			}
		}
	}
}
