package locale_test

import (
	"testing"
	"testing/fstest"

	"github.com/stubbedev/lys-polkit/internal/locale"
)

func TestEmbeddedGermanTranslatesEveryMessage(t *testing.T) {
	t.Setenv("LANGUAGE", "")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", "de_DE.UTF-8")

	c, err := locale.Load(locale.Embedded)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, m := range locale.Messages {
		if got := c.Get(m); got == "" || got == string(m) {
			t.Errorf("German %q = %q, want a translation", m, got)
		}
	}
}

func TestUnknownLocaleIsEnglish(t *testing.T) {
	t.Setenv("LANGUAGE", "")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", "xx_XX.UTF-8")

	c, err := locale.Load(locale.Embedded)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, m := range locale.Messages {
		if got := c.Get(m); got != string(m) {
			t.Errorf("%q = %q, want it untranslated", m, got)
		}
	}
}

func TestBrokenCatalogFails(t *testing.T) {
	t.Setenv("LANGUAGE", "")
	t.Setenv("LC_ALL", "de_DE.UTF-8")

	fsys := fstest.MapFS{"de/LC_MESSAGES/" + locale.Domain + ".po": {Data: []byte("msgstr \"orphan\"\n\"dangling\n")}}
	if _, err := locale.Load(fsys); err == nil {
		t.Error("Load accepted a catalog that does not parse")
	}
}
