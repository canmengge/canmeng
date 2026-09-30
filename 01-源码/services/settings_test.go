package services

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSettingsServiceDefaultsAndRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "settings.json")
	service := newSettingsService(path)
	settings, err := service.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.AnnotationTagPlacement != AnnotationTagAfterTarget {
		t.Fatalf("default placement = %q", settings.AnnotationTagPlacement)
	}
	if settings.ExplorerOpenMode != ExplorerOpenSingleClick {
		t.Fatalf("default explorer open mode = %q", settings.ExplorerOpenMode)
	}
	if settings.VimMode {
		t.Fatal("default vim mode = true, want false")
	}
	if settings.BackupSourceOnSave {
		t.Fatal("default backup source on save = true, want false（2026-09-24：保存前整包备份默认关闭以加快保存）")
	}
	if settings.Theme != ThemeDark {
		t.Fatalf("default theme = %q, want %q", settings.Theme, ThemeDark)
	}

	settings.AnnotationTagPlacement = AnnotationTagLineEnd
	settings.ExplorerOpenMode = ExplorerOpenDoubleClick
	settings.VimMode = true
	settings.BackupSourceOnSave = false
	settings.Theme = ThemeLight
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := service.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded, settings) {
		t.Fatalf("loaded = %#v, want %#v", loaded, settings)
	}
	assertPrivateFileMode(t, path)
}

func TestSettingsServiceRejectsInvalidPlacement(t *testing.T) {
	service := newSettingsService(filepath.Join(t.TempDir(), "settings.json"))
	err := service.SaveSettings(AppSettings{
		AnnotationTagPlacement: "floating",
		ExplorerOpenMode:       ExplorerOpenSingleClick,
		Theme:                  ThemeDark,
	})
	if err == nil {
		t.Fatal("expected invalid placement error")
	}
}

func TestSettingsServiceEnablesBackupForLegacySettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	legacy := []byte(`{"annotationTagPlacement":"after-target","explorerOpenMode":"single-click","vimMode":false}`)
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}

	settings, err := newSettingsService(path).GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.BackupSourceOnSave {
		t.Fatal("legacy settings enabled source backup, want false（默认已改为关闭）")
	}
}

// 老配置（没有 stringTableGuardDefaultMigrated 字段）会被一次性迁移为"写保护开启"；
// 迁移过之后再手动关闭则保持关闭，不被反复覆盖。
func TestSettingsServiceMigratesGuardDefaultToOne(t *testing.T) {
	defer setStringTableGuardEnabled(false)
	path := filepath.Join(t.TempDir(), "settings.json")
	legacy := []byte(`{"protectedStringTableGuard":false,"theme":"dark"}`)
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	settings, err := newSettingsService(path).GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !settings.ProtectedStringTableGuard {
		t.Fatal("老配置应被迁移为写保护开启")
	}
	if !settings.StringTableGuardDefaultMigrated {
		t.Fatal("迁移标记应已写入")
	}

	// 用户显式关闭 → 保存 → 再读，必须保持关闭。
	settings.ProtectedStringTableGuard = false
	if err := newSettingsService(path).SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	again, err := newSettingsService(path).GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if again.ProtectedStringTableGuard {
		t.Fatal("用户显式关闭后不应被迁移覆盖")
	}
}

// 2026-10-01（用户要求，取代 2026-09-24 的"默认关闭"）：
// 字符串表写保护**默认开启**，且保存设置后立即同步到进程内状态。
func TestSettingsServiceSyncsStringTableGuardSwitch(t *testing.T) {
	defer setStringTableGuardEnabled(false)
	if !DefaultAppSettings().ProtectedStringTableGuard {
		t.Fatal("字符串表写保护默认应为开启")
	}
	service := newSettingsService(filepath.Join(t.TempDir(), "settings.json"))
	settings := DefaultAppSettings()
	settings.ProtectedStringTableGuard = true
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if !StringTableGuardEnabled() {
		t.Fatal("保存设置后写保护开关未同步为开启")
	}
	settings.ProtectedStringTableGuard = false
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if StringTableGuardEnabled() {
		t.Fatal("保存设置后写保护开关未同步为关闭")
	}
}

func TestSettingsServiceRejectsInvalidExplorerOpenMode(t *testing.T) {
	service := newSettingsService(filepath.Join(t.TempDir(), "settings.json"))
	err := service.SaveSettings(AppSettings{
		AnnotationTagPlacement: AnnotationTagAfterTarget,
		ExplorerOpenMode:       "middle-click",
		Theme:                  ThemeDark,
	})
	if err == nil {
		t.Fatal("expected invalid explorer open mode error")
	}
}

func TestSettingsServiceSupportsSystemTheme(t *testing.T) {
	service := newSettingsService(filepath.Join(t.TempDir(), "settings.json"))
	settings := DefaultAppSettings()
	settings.Theme = ThemeSystem
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := service.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Theme != ThemeSystem {
		t.Fatalf("loaded theme = %q, want %q", loaded.Theme, ThemeSystem)
	}
}

func TestSettingsServiceLegacySettingsUseDarkTheme(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	legacy := []byte(`{"annotationTagPlacement":"after-target","explorerOpenMode":"single-click","vimMode":false}`)
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}

	settings, err := newSettingsService(path).GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.Theme != ThemeDark {
		t.Fatalf("legacy theme = %q, want %q", settings.Theme, ThemeDark)
	}
}

func TestSettingsServiceRejectsInvalidTheme(t *testing.T) {
	service := newSettingsService(filepath.Join(t.TempDir(), "settings.json"))
	settings := DefaultAppSettings()
	settings.Theme = "solarized"
	if err := service.SaveSettings(settings); err == nil {
		t.Fatal("expected invalid theme error")
	}
}
