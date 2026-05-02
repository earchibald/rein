package adapter

import "github.com/earchibald/rein/internal/reporoot"

const MarketplaceIndexPath = ".claude-plugin/marketplace.json"

func FindRoot(start string) (string, error) {
	return reporoot.Find(start, MarketplaceIndexPath, "adapter marketplace")
}
