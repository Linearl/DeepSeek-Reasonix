package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/provider"
	"reasonix/internal/sessioncatalog"
)

func TestDefaultTopicTitleLocalizesAtAPIBoundary(t *testing.T) {
	app := &App{}
	for _, tt := range []struct {
		locale string
		want   string
	}{
		{locale: "en-US", want: defaultTopicTitleEn},
		{locale: "zh-CN", want: defaultTopicTitle},
		{locale: "zh-TW", want: defaultTopicTitleZhTW},
	} {
		app.setDesktopLocale(tt.locale)
		if got := app.localizedTopicTitle(defaultTopicTitle, topicTitleSourceAuto); got != tt.want {
			t.Fatalf("locale %q title = %q, want %q", tt.locale, got, tt.want)
		}
	}
}

func TestManualDefaultTopicTitleIsNotLocalized(t *testing.T) {
	app := &App{}
	for _, tt := range []struct {
		locale string
		title  string
	}{
		{locale: "zh-CN", title: defaultTopicTitleEn},
		{locale: "en-US", title: defaultTopicTitle},
		{locale: "zh-CN", title: defaultTopicTitleZhTW},
	} {
		app.setDesktopLocale(tt.locale)
		if got := app.localizedTopicTitle(tt.title, topicTitleSourceManual); got != tt.title {
			t.Fatalf("locale %q manual title = %q, want %q", tt.locale, got, tt.title)
		}
	}
}

func TestCreateTopicPreservesManualDefaultTitle(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := &App{}
	app.projectTreeChangedHook = func() {}
	app.setDesktopLocale("zh-CN")

	manual, err := app.CreateTopic("global", "", defaultTopicTitleEn)
	if err != nil {
		t.Fatalf("CreateTopic manual: %v", err)
	}
	if manual.Title != defaultTopicTitleEn {
		t.Fatalf("manual title = %q, want %q", manual.Title, defaultTopicTitleEn)
	}
	if got := loadTopicTitleSource("", manual.ID); got != topicTitleSourceManual {
		t.Fatalf("manual title source = %q, want %q", got, topicTitleSourceManual)
	}
	tree := app.ListProjectTree()
	if len(tree) == 0 || len(tree[0].Children) == 0 || tree[0].Children[0].Label != defaultTopicTitleEn {
		t.Fatalf("manual project-tree title was localized: %+v", tree)
	}

	automatic, err := app.CreateTopic("global", "", "")
	if err != nil {
		t.Fatalf("CreateTopic automatic: %v", err)
	}
	if automatic.Title != defaultTopicTitle {
		t.Fatalf("automatic title = %q, want localized %q", automatic.Title, defaultTopicTitle)
	}
	if err := app.RenameTopic(automatic.ID, defaultTopicTitleEn); err != nil {
		t.Fatalf("RenameTopic manual default: %v", err)
	}
	if got := loadTopicTitleSource("", automatic.ID); got != topicTitleSourceManual {
		t.Fatalf("renamed title source = %q, want %q", got, topicTitleSourceManual)
	}
	tree = app.ListProjectTree()
	if len(tree) == 0 || len(tree[0].Children) == 0 || tree[0].Children[0].Label != defaultTopicTitleEn {
		t.Fatalf("renamed project-tree title was localized: %+v", tree)
	}
}

func TestDefaultTopicTitleVariantsRemainPersistenceSentinels(t *testing.T) {
	for _, title := range []string{defaultTopicTitle, defaultTopicTitleEn, defaultTopicTitleZhTW} {
		if !isDefaultTopicTitle(title) {
			t.Fatalf("title %q was not recognized as a default sentinel", title)
		}
	}
	if got := topicTitleFromText(defaultTopicTitleEn); got != "" {
		t.Fatalf("English default title became a generated title: %q", got)
	}
}

func TestForkTopicTitleUsesDesktopLocale(t *testing.T) {
	app := &App{}
	app.setDesktopLocale("en")
	if got := app.forkTopicTitle("New session"); got != "Forked session" {
		t.Fatalf("English fork title = %q", got)
	}
	app.setDesktopLocale("zh-TW")
	if got := app.forkTopicTitle(defaultTopicTitle); got != "分叉會話" {
		t.Fatalf("Traditional Chinese fork title = %q", got)
	}
	app.desktopLocale.Store(desktopLocaleUnknown)
	if got := app.forkTopicTitle(""); got != "分叉会话" {
		t.Fatalf("legacy fallback fork title = %q", got)
	}
}

func TestSetTrayLocaleDoesNotChangeAutoCurrency(t *testing.T) {
	isolateDesktopUserDirs(t)
	cfg := config.Default()
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save auto config: %v", err)
	}

	app := NewApp()
	app.projectTreeChangedHook = func() {}
	activeCtrl := control.New(control.Options{Label: "active"})
	inactiveCtrl := control.New(control.Options{Label: "inactive"})
	t.Cleanup(activeCtrl.Close)
	t.Cleanup(inactiveCtrl.Close)
	app.tabs = map[string]*WorkspaceTab{
		"active":   {ID: "active", Ctrl: activeCtrl},
		"inactive": {ID: "inactive", Ctrl: inactiveCtrl},
	}
	app.activeTabID = "active"
	app.setDesktopLocale("en")

	if err := app.SetTrayLocale("zh-CN"); err != nil {
		t.Fatalf("SetTrayLocale: %v", err)
	}
	for _, tabID := range []string{"active", "inactive"} {
		if app.deferredRebuildPending(tabID) {
			t.Fatalf("locale change scheduled currency refresh for %q", tabID)
		}
	}
}

func TestSetTrayLocaleKeepsExplicitCurrencyRuntime(t *testing.T) {
	isolateDesktopUserDirs(t)
	cfg := config.Default()
	cfg.Desktop.Currency = "USD"
	cfg.ApplyDeepSeekOfficialDefaultPricing()
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save explicit currency config: %v", err)
	}

	app := NewApp()
	app.projectTreeChangedHook = func() {}
	ctrl := control.New(control.Options{Label: "active"})
	t.Cleanup(ctrl.Close)
	app.tabs = map[string]*WorkspaceTab{"active": {ID: "active", Ctrl: ctrl}}
	app.activeTabID = "active"
	app.setDesktopLocale("en")

	if err := app.SetTrayLocale("zh-CN"); err != nil {
		t.Fatalf("SetTrayLocale: %v", err)
	}
	if app.deferredRebuildPending("active") {
		t.Fatal("explicit USD currency scheduled an automatic locale refresh")
	}
}

func TestDesktopEffectivePricingCurrencyUsesLocaleOnlyForAuto(t *testing.T) {
	app := NewApp()
	app.setDesktopLocale("zh-CN")

	cfg := config.Default()
	if got := app.desktopEffectivePricingCurrency(cfg); got != "" {
		t.Fatalf("auto pricing currency = %q, want unresolved auto", got)
	}

	cfg.Desktop.Language = "en"
	cfg.ApplyDeepSeekOfficialDefaultPricing()
	if got := app.desktopEffectivePricingCurrency(cfg); got != "" {
		t.Fatalf("desktop language changed pricing currency = %q", got)
	}

	cfg.Desktop.Language = ""
	cfg.Language = "en"
	cfg.ApplyDeepSeekOfficialDefaultPricing()
	if got := app.desktopEffectivePricingCurrency(cfg); got != "" {
		t.Fatalf("CLI language changed pricing currency = %q", got)
	}

	cfg.Language = ""
	cfg.Desktop.Currency = "USD"
	cfg.ApplyDeepSeekOfficialDefaultPricing()
	if got := app.desktopEffectivePricingCurrency(cfg); got != "USD" {
		t.Fatalf("explicit currency pricing currency = %q, want USD", got)
	}
}

func installLocaleTestCatalog(t *testing.T, app *App) *sessioncatalog.Catalog {
	t.Helper()
	catalog, err := sessioncatalog.Open(context.Background(), sessioncatalog.Options{InMemory: true, DisableRepair: true})
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	app.sessionCatalog.Store(catalog)
	t.Cleanup(func() {
		app.sessionCatalog.CompareAndSwap(catalog, nil)
		_ = catalog.Close(context.Background())
	})
	if err := catalog.ReconcileDirectory(context.Background(), sessioncatalog.DirectoryTarget{Path: desktopSessionDir(globalWorkspaceRoot()), Scope: "global", WorkspaceRoot: ""}); err != nil {
		t.Fatalf("reconcile catalog: %v", err)
	}
	if err := app.syncSessionCatalogMetadataBounded(context.Background(), catalog); err != nil {
		t.Fatalf("sync catalog metadata: %v", err)
	}
	return catalog
}

func TestCatalogTopicTitleLocalizesAtSidebarBoundary(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.projectTreeChangedHook = func() {}
	app.setDesktopLocale("en-US")

	// tl 1036 ⑤: the topic must be pinned so it is never treated as a
	// recovery-only or blank-shell row and is guaranteed to render as one
	// ordinary topic. The localization contract this test owns is the LABEL
	// at the sidebar boundary, not recovery/blank-shell visibility.
	if _, err := app.EnsureBlankTab("global", ""); err != nil {
		t.Fatalf("EnsureBlankTab: %v", err)
	}
	sessionDir := desktopSessionDir(globalWorkspaceRoot())
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir global sessions: %v", err)
	}
	blank := filepath.Join(sessionDir, "blank-shell.jsonl")
	sess := agent.NewSession("system")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "localize me"})
	if err := sess.SaveSnapshot(blank); err != nil {
		t.Fatalf("save blank-shell session: %v", err)
	}
	if err := agent.UpdateBranchMeta(blank, false, func(meta *agent.BranchMeta) error {
		meta.TopicTitle = defaultTopicTitle
		meta.TopicID = "blank-shell"
		return nil
	}); err != nil {
		t.Fatalf("update branch meta: %v", err)
	}
	// Pin the topic through the topic-state registry so the catalog row
	// carries TitleSource=auto (localized) and Pinned=true — a pinned row is
	// never treated as recovery-only or a blank shell, so the sidebar always
	// renders exactly this one ordinary topic.
	if err := ensureTopicIndexedWithCreatedAt("global", "", "blank-shell", defaultTopicTitle, topicTitleSourceAuto, time.Now().UnixMilli()); err != nil {
		t.Fatalf("index topic: %v", err)
	}
	if err := app.SetTopicPinned("blank-shell", true); err != nil {
		t.Fatalf("pin topic: %v", err)
	}
	app.mu.Lock()
	for _, tab := range app.tabs {
		tab.TopicTitle = defaultTopicTitle
		tab.TopicID = "blank-shell"
		tab.SessionPath = blank
	}
	app.mu.Unlock()
	catalog := installLocaleTestCatalog(t, app)
	page, err := app.catalogTopicPage(catalog, ProjectTopicPageRequest{Scope: "global", WorkspaceRoot: "", Limit: 100})
	if err != nil {
		t.Fatalf("catalogTopicPage: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Label != defaultTopicTitleEn {
		t.Fatalf("catalog sidebar label = %+v, want localized %q", page.Items, defaultTopicTitleEn)
	}
}

func TestCatalogManualDefaultTitleIsNotLocalized(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.projectTreeChangedHook = func() {}
	app.setDesktopLocale("en-US")

	manual, err := app.CreateTopic("global", "", defaultTopicTitle)
	if err != nil {
		t.Fatalf("CreateTopic manual: %v", err)
	}
	if manual.Title != defaultTopicTitle {
		t.Fatalf("manual title = %q, want %q", manual.Title, defaultTopicTitle)
	}
	sessionDir := desktopSessionDir(globalWorkspaceRoot())
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir global sessions: %v", err)
	}
	// tl 1036 ⑥: the session carries one real turn so the topic is not a
	// zero-turn blank shell — task 550 ③ hides exactly those. A MANUALLY
	// named default-titled topic must stay visible with its literal title
	// (isDefaultTopicTitle + topicTitleSourceManual is never localized). The
	// contract under test is still "manual titles are left alone".
	manualSession := filepath.Join(sessionDir, "manual-default.jsonl")
	sess := agent.NewSession("system")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "manual default title should remain unchanged"})
	if err := sess.SaveSnapshot(manualSession); err != nil {
		t.Fatalf("save manual-default session: %v", err)
	}
	if err := agent.SaveBranchMeta(manualSession, agent.BranchMeta{
		CreatedAt:  time.Now().Add(-time.Minute),
		UpdatedAt:  time.Now(),
		Scope:      "global",
		TopicID:    manual.ID,
		TopicTitle: defaultTopicTitle,
	}); err != nil {
		t.Fatalf("save branch meta: %v", err)
	}
	catalog := installLocaleTestCatalog(t, app)
	page, err := app.catalogTopicPage(catalog, ProjectTopicPageRequest{Scope: "global", WorkspaceRoot: "", Limit: 100})
	if err != nil {
		t.Fatalf("catalogTopicPage: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].TopicID != manual.ID || page.Items[0].Label != defaultTopicTitle {
		t.Fatalf("manual catalog topic = %+v, want topic %q with untouched label %q", page.Items, manual.ID, defaultTopicTitle)
	}
}
