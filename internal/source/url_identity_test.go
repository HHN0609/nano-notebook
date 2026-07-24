package source_test

import (
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/source"
)

func TestCanonicalURLIdentityDropsTrackingFragmentAndSortsQuery(t *testing.T) {
	got, err := source.CanonicalURLIdentity(" HTTPS://Example.COM:443/path?utm_source=brave&b=2&a=1&gclid=x#part ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://example.com/path?a=1&b=2" {
		t.Fatalf("identity=%q", got)
	}
}

func TestCanonicalURLIdentityRejectsUnsafeShape(t *testing.T) {
	for _, raw := range []string{"ftp://example.com/file", "https://user@example.com/file", "https:///missing"} {
		if _, err := source.CanonicalURLIdentity(raw); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}
