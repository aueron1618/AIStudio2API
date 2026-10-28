package app

import "testing"

func TestSelfUpdaterKeepsForkReleaseSource(t *testing.T) {
	updater := newSelfUpdater("dev", "aistudio2api", nil, nil, nil)
	if updater.releaseURL != "https://api.github.com/repos/aueron1618/AIStudio2API/releases/latest" {
		t.Fatalf("updater would replace the fork with another distribution: %s", updater.releaseURL)
	}
}
