package router

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hardcore-os/plato/common/cache"
)

const (
	gatewayRouterKey = "gateway_router_%d"
	ttl7D            = 7 * 24 * 60 * 60
)

type Record struct {
	Endpoint string
	ConndID  uint64
}

func AddRecord(ctx context.Context, did uint64, endpoint string, conndID uint64) error {
	key := fmt.Sprintf(gatewayRouterKey, did)
	value := fmt.Sprintf("%s-%d", endpoint, conndID)
	return cache.SetString(ctx, key, value, ttl7D*time.Second)
}
func DelRecord(ctx context.Context, did uint64) error {
	key := fmt.Sprintf(gatewayRouterKey, did)
	return cache.Del(ctx, key)
}
func QueryRecord(ctx context.Context, did uint64) (*Record, error) {
	key := fmt.Sprintf(gatewayRouterKey, did)
	data, err := cache.GetString(ctx, key)
	if err != nil {
		return nil, err
	}
	ec := strings.Split(data, "-")
	conndID, err := strconv.ParseUint(ec[1], 10, 64)
	if err != nil {
		return nil, err
	}
	return &Record{
		Endpoint: ec[0],
		ConndID:  conndID,
	}, nil
}
