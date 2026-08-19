package perf

import (
	"fmt"
	"net"

	"github.com/hardcore-os/plato/common/sdk"
)

var (
	TcpConnNum int32
)

func RunMain() {
	for i := 0; i < int(TcpConnNum); i++ {
		did := uint64(100000 + i)
		account := fmt.Sprintf("perf-%d", did)
		identity, err := sdk.LoginAccount(account, "perf-password", did)
		if err != nil {
			identity, err = sdk.RegisterAccount(account, "perf-password", account, did)
		}
		if err == nil {
			sdk.NewChat(net.ParseIP("127.0.0.1"), 8900, identity.UserID, did, identity.SessionToken, "zjr", "123", nil)
		}
	}
}
