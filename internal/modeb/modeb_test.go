package modeb

import (
	"testing"

	"dnsredirector/internal/rules"
)

func TestRoutingDomains(t *testing.T) {
	got := RoutingDomains([]rules.Rule{
		{Match: rules.Match{Domains: []string{"discord.com", "*.discord.com", "steamcommunity.com"}}},
		{Match: rules.Match{Domains: []string{"*.Discord.COM."}}},
	})
	want := []string{"~discord.com", "~steamcommunity.com"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}
