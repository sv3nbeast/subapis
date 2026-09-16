package xai

import "testing"

// A Grok group serves Claude Code and Codex through the gpt-*/claude-* wildcards.
// Those wildcards come from process state that nothing populates during startup,
// so the package default has to match how settings parse an absent
// grok_cross_client_model_map_enabled row: absent means enabled. When the two
// disagreed, a restarted gateway answered Claude Code with "no available accounts
// supporting model: claude-opus-5" until an administrator opened the settings page.
func TestPackageDefaultEnablesCrossClientMap(t *testing.T) {
	if !RuntimeModelMappingOptions().EnableCrossClientMap {
		t.Fatal("EnableCrossClientMap is false before settings load; a restarted gateway would reject claude-*/gpt-* on Grok groups")
	}

	mapping := DefaultModelMapping()
	for _, wildcard := range []string{"claude-*", "gpt-*"} {
		target, ok := mapping[wildcard]
		if !ok {
			t.Errorf("default mapping has no %q entry", wildcard)
			continue
		}
		if target != DefaultTextModel {
			t.Errorf("%q maps to %q, want %q", wildcard, target, DefaultTextModel)
		}
	}
}
