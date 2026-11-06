package aistudio

import (
	"context"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
)

type pageCatalogTestProvider func(context.Context, string, bool) (camoufoxnative.PageCatalog, error)

func (provider pageCatalogTestProvider) PageCatalog(ctx context.Context, account string, reload bool) (camoufoxnative.PageCatalog, error) {
	return provider(ctx, account, reload)
}

func TestClientLoadsCatalogOnlyFromPageProvider(t *testing.T) {
	var reloads []bool
	client := NewClient(ClientOptions{Pages: pageCatalogTestProvider(func(_ context.Context, account string, reload bool) (camoufoxnative.PageCatalog, error) {
		if account != "fixture-account" {
			t.Fatalf("catalog requested for %q", account)
		}
		reloads = append(reloads, reload)
		return camoufoxnative.PageCatalog{Logging: []byte("[]"), Tier: []byte("[1]"), Models: []byte(pageFixtureModels)}, nil
	})})
	for _, reload := range []bool{false, true} {
		tier, models, err := client.loadPageCatalog(t.Context(), "fixture-account", reload)
		if err != nil || tier != BenefitTierPro || len(models) != 1 || models[0].ID != "fixture" {
			t.Fatalf("page catalog = %v, %v, %v", tier, models, err)
		}
	}
	if len(reloads) != 2 || reloads[0] || !reloads[1] {
		t.Fatal("catalog refresh lost its reload contract")
	}
	if _, _, err := NewClient(ClientOptions{}).loadPageCatalog(t.Context(), "fixture-account", false); err == nil {
		t.Fatal("missing page provider silently fell back to unrelated RPC requests")
	}
}

func TestPageCatalogRequiresAllColdStartResponses(t *testing.T) {
	for _, missing := range []string{"logging", "tier", "models"} {
		t.Run(missing, func(t *testing.T) {
			page := camoufoxnative.PageCatalog{Logging: []byte("[]"), Tier: []byte("[1]"), Models: []byte(pageFixtureModels)}
			switch missing {
			case "logging":
				page.Logging = nil
			case "tier":
				page.Tier = nil
			case "models":
				page.Models = nil
			}
			client := NewClient(ClientOptions{})
			if _, _, err := client.ApplyPageCatalog(t.Context(), "fixture", page); err == nil {
				t.Fatal("incomplete cold-start catalog was published")
			}
			if len(client.catalogs) != 0 || len(client.tiers) != 0 {
				t.Fatal("incomplete page changed live model or tier state")
			}
		})
	}
}
