package tui

import (
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cache"
)

func TestCachePolicyDefaultsToLRUAndCyclesExplicitly(t *testing.T) {
	model := editTestModel(t)
	if model.CachePolicy != cache.PolicyLRU {
		t.Fatalf("default cache policy = %q, want %q", model.CachePolicy, cache.PolicyLRU)
	}
	for _, want := range []cache.Policy{cache.PolicyEphemeral, cache.PolicyPinnedOffline, cache.PolicyLRU} {
		model, _ = Reduce(model, KeyPress{Key: KeyPath})
		var intents []Intent
		model, intents = Reduce(model, TextInput{Text: "p"})
		if model.Mode != ModeNormal || model.CachePolicy != want || len(intents) != 1 || intents[0].Kind != IntentCachePolicy || intents[0].CachePolicy != want {
			t.Fatalf("cycle to %q = mode %q policy %q intents %#v", want, model.Mode, model.CachePolicy, intents)
		}
	}
}

func TestStatusAlwaysDisplaysCurrentCachePolicy(t *testing.T) {
	model := editTestModel(t)
	surface := newMemorySurface(100, 10)
	Render(surface, model, RenderOptions{})
	if !strings.Contains(surface.String(), "cache:lru") {
		t.Fatalf("status omitted default cache policy:\n%s", surface.String())
	}
	model.CachePolicy = cache.PolicyPinnedOffline
	surface = newMemorySurface(100, 10)
	Render(surface, model, RenderOptions{})
	if !strings.Contains(surface.String(), "cache:pinned_offline") {
		t.Fatalf("status omitted restored cache policy:\n%s", surface.String())
	}
}

func TestCacheClearRequiresExplicitScopedConfirmation(t *testing.T) {
	for _, test := range []struct {
		input string
		scope CacheClearScope
	}{
		{input: "c", scope: CacheClearWorkspace},
		{input: "C", scope: CacheClearAll},
	} {
		model := editTestModel(t)
		model, _ = Reduce(model, KeyPress{Key: KeyPath})
		model, intents := Reduce(model, TextInput{Text: test.input})
		if model.Mode != ModeCacheClearConfirm || model.CacheClearScope != test.scope || len(intents) != 0 {
			t.Fatalf("scope %q entry = mode %q scope %q intents %#v", test.scope, model.Mode, model.CacheClearScope, intents)
		}
		model, intents = Reduce(model, KeyPress{Key: KeySubmit})
		if model.Mode != ModeNormal || len(intents) != 1 || intents[0].Kind != IntentCacheClear || intents[0].CacheClearScope != test.scope {
			t.Fatalf("scope %q confirmation = mode %q intents %#v", test.scope, model.Mode, intents)
		}
	}
}

func TestCacheClearCancelAndResultRemainNonDestructiveInTheReducer(t *testing.T) {
	model := editTestModel(t)
	model, _ = Reduce(model, KeyPress{Key: KeyPath})
	model, _ = Reduce(model, TextInput{Text: "c"})
	model, intents := Reduce(model, KeyPress{Key: KeyEscape})
	if model.Mode != ModeNormal || len(intents) != 0 {
		t.Fatalf("cancel = mode %q intents %#v", model.Mode, intents)
	}
	model, _ = Reduce(model, CacheCleared{Deleted: 2, Protected: 3, RemainingBytes: 4096})
	if model.Notice == "" {
		t.Fatal("cache clear result was not visible")
	}
}
