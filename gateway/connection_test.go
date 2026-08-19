package gateway

import (
	"net"
	"sync"
	"testing"
	"time"
)

func TestConnectionCloseIsIdempotent(t *testing.T) {
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	client, err := net.DialTCP("tcp", nil, listener.Addr().(*net.TCPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server, err := listener.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}

	previousPool := ep
	ep = &ePool{}
	defer func() { ep = previousPool }()

	conn := &connection{conn: server}
	conn.Close()
	conn.Close()
}

func newFixedTimeGenerator(t *testing.T, nodeID uint64, now int64) *ConnIDGenerater {
	t.Helper()
	generator, err := NewConnIDGenerater(nodeID)
	if err != nil {
		t.Fatal(err)
	}
	generator.now = func() int64 { return now }
	return generator
}

func TestConnIDDiffersAcrossGatewayNodesAtSameMillisecond(t *testing.T) {
	now := twepoch + 12345
	first, err := newFixedTimeGenerator(t, 7, now).NextID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := newFixedTimeGenerator(t, 8, now).NextID()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("different gateway nodes generated the same connID: %d", first)
	}
	if got := (first >> nodeLeft) & maxNodeID; got != 7 {
		t.Fatalf("first connID node ID = %d, want 7", got)
	}
	if got := (second >> nodeLeft) & maxNodeID; got != 8 {
		t.Fatalf("second connID node ID = %d, want 8", got)
	}
	if got := first >> versionLeft; got != version {
		t.Fatalf("first connID version = %d, want %d", got, version)
	}
}

func TestConnIDSequenceIsUniqueWithinGateway(t *testing.T) {
	generator, err := NewConnIDGenerater(9)
	if err != nil {
		t.Fatal(err)
	}

	const count = 2000
	ids := make(chan uint64, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, nextErr := generator.NextID()
			if nextErr != nil {
				t.Errorf("NextID: %v", nextErr)
				return
			}
			ids <- id
		}()
	}
	wg.Wait()
	close(ids)

	seen := make(map[uint64]struct{}, count)
	for id := range ids {
		if _, exists := seen[id]; exists {
			t.Fatalf("duplicate connID: %d", id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != count {
		t.Fatalf("generated %d unique IDs, want %d", len(seen), count)
	}
}

func TestConnIDGeneratorRejectsInvalidNodeID(t *testing.T) {
	if _, err := NewConnIDGenerater(maxNodeID + 1); err == nil {
		t.Fatal("expected invalid node ID error")
	}
}

func TestConnIDGeneratorRejectsClockRollback(t *testing.T) {
	generator := newFixedTimeGenerator(t, 1, time.Now().UnixMilli())
	if _, err := generator.NextID(); err != nil {
		t.Fatal(err)
	}
	generator.now = func() int64 { return generator.lastStamp - 1 }
	if _, err := generator.NextID(); err == nil {
		t.Fatal("expected clock rollback error")
	}
}
