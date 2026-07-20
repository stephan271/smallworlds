package bootstrapassets

import (
	"net/http"
	"net/url"
	"testing"
)

func TestRedirectPolicyAllowsOnlyOfficialGitHubReleaseAssetRedirects(t *testing.T) {
	policy := redirectPolicy("https://github.com/stephan271/smallworlds/releases/download/v1.2.24/smallworlds-bootstrap-v1.2.24-linux-amd64.tar.gz")
	allowed, err := http.NewRequest(http.MethodGet, "https://objects.githubusercontent.com/release-asset", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := policy(allowed, []*http.Request{{URL: &url.URL{Host: "github.com"}}}); err != nil {
		t.Fatalf("expected GitHub asset redirect to be allowed, got %v", err)
	}

	blocked, err := http.NewRequest(http.MethodGet, "https://downloads.example.invalid/release-asset", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := policy(blocked, []*http.Request{{URL: &url.URL{Host: "github.com"}}}); err != http.ErrUseLastResponse {
		t.Fatalf("expected non-GitHub redirect to stop, got %v", err)
	}

	generic := redirectPolicy("https://assets.example.invalid/bootstrap.tar.gz")
	if err := generic(allowed, nil); err != http.ErrUseLastResponse {
		t.Fatalf("expected generic asset redirect to stop, got %v", err)
	}
}

func TestOfficialGitHubReleaseURLRequiresExactRepositoryAndPath(t *testing.T) {
	for _, rawURL := range []string{
		"https://github.com/stephan271/smallworlds/releases/download/v1.2.24/asset.tar.gz",
		"https://github.com/stephan271/smallworlds/releases/download/v1.2.24/asset.tar.gz?download=1",
		"https://github.com/other/smallworlds/releases/download/v1.2.24/asset.tar.gz",
		"https://github.com/stephan271/smallworlds/releases/latest/download/asset.tar.gz",
	} {
		want := rawURL == "https://github.com/stephan271/smallworlds/releases/download/v1.2.24/asset.tar.gz"
		if got := isOfficialGitHubReleaseURL(rawURL); got != want {
			t.Fatalf("isOfficialGitHubReleaseURL(%q) = %v, want %v", rawURL, got, want)
		}
	}
}
