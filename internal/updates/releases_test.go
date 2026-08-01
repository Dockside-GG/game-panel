package updates

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		left, right string
		want        int
	}{
		{"0.1.0-alpha.2", "0.1.0-alpha.1", 1},
		{"0.1.0", "0.1.0-alpha.9", 1},
		{"1.0.0", "1.0.0", 0},
		{"1.0.0-alpha.1", "1.0.0-alpha.beta", -1},
	}
	for _, test := range cases {
		if got := compareVersions(test.left, test.right); got != test.want {
			t.Fatalf("compareVersions(%q, %q) = %d, want %d", test.left, test.right, got, test.want)
		}
	}
}

func TestDevelopmentBuildDoesNotContactGitHub(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	checker := NewCheckerForTest(server.URL, server.Client())
	check, err := checker.Check(context.Background(), "dev", true, true)
	if err != nil {
		t.Fatal(err)
	}
	if check.UpdatesSupported {
		t.Fatal("development build unexpectedly supports in-panel updates")
	}
	if requests.Load() != 0 {
		t.Fatalf("development check made %d remote requests", requests.Load())
	}
}

func TestCheckerFiltersAndRequiresReleaseAssets(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"tag_name":"v0.2.0-alpha.1","name":"Alpha","html_url":"https://github.com/Dockside-GG/game-panel/releases/tag/v0.2.0-alpha.1","prerelease":true,"assets":[{"name":"dockside-game-panel-0.2.0-alpha.1.zip","browser_download_url":"https://github.com/Dockside-GG/game-panel/releases/download/v0.2.0-alpha.1/dockside-game-panel-0.2.0-alpha.1.zip"},{"name":"SHA256SUMS","browser_download_url":"https://github.com/Dockside-GG/game-panel/releases/download/v0.2.0-alpha.1/SHA256SUMS"}]},
			{"tag_name":"v0.1.1","name":"Stable","html_url":"https://github.com/Dockside-GG/game-panel/releases/tag/v0.1.1","prerelease":false,"assets":[{"name":"dockside-game-panel-0.1.1.zip","browser_download_url":"https://github.com/Dockside-GG/game-panel/releases/download/v0.1.1/dockside-game-panel-0.1.1.zip"},{"name":"SHA256SUMS","browser_download_url":"https://github.com/Dockside-GG/game-panel/releases/download/v0.1.1/SHA256SUMS"}]},
			{"tag_name":"v9.9.9","name":"Incomplete","prerelease":false,"assets":[]}
		]`))
	}))
	defer server.Close()
	checker := NewCheckerForTest(server.URL, server.Client())
	stable, err := checker.Check(context.Background(), "0.1.0", false, true)
	if err != nil {
		t.Fatal(err)
	}
	if stable.Latest == nil || stable.Latest.Version != "0.1.1" {
		t.Fatalf("unexpected stable release: %#v", stable.Latest)
	}
	preview, err := checker.Check(context.Background(), "0.1.0", true, true)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Latest == nil || preview.Latest.Version != "0.2.0-alpha.1" {
		t.Fatalf("unexpected preview release: %#v", preview.Latest)
	}
}
