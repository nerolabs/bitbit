package credit

import (
	"errors"
	"math"
	"testing"

	"github.com/nerolabs/silt/ports"
)

func id(b byte) ports.NodeID { return ports.HashBytes([]byte{b}) }

func TestEarnAndSpend(t *testing.T) {
	l := New(100, 0)
	server, requester := id(1), id(2)
	l.Register(server)
	l.Register(requester)

	if l.CanPublish(server) {
		t.Fatal("zero-grant node should not afford the fee")
	}
	l.RecordServe(server, requester, ports.HashBytes([]byte("c")), 250)
	if got := l.Balance(server); got != 250 {
		t.Fatalf("balance %d, want 250 (1 byte = 1 credit)", got)
	}
	if l.FetchedBytes(requester) != 250 || l.ServedBytes(server) != 250 {
		t.Fatal("served/fetched accounting wrong")
	}
	if err := l.ChargePublish(server); err != nil {
		t.Fatal(err)
	}
	if got := l.Balance(server); got != 150 {
		t.Fatalf("balance %d after fee, want 150", got)
	}
	if err := l.ChargePublish(requester); !errors.Is(err, ports.ErrInsufficientCredit) {
		t.Fatalf("broke node charge: want ErrInsufficientCredit, got %v", err)
	}
}

func TestGrantAppliedOnceAndSelfServeIgnored(t *testing.T) {
	l := New(100, 100)
	n := id(3)
	l.Register(n)
	l.Register(n)
	if l.Balance(n) != 100 {
		t.Fatalf("double registration granted twice: %d", l.Balance(n))
	}
	l.RecordServe(n, n, ports.HashBytes([]byte("c")), 1000)
	if l.Balance(n) != 100 {
		t.Fatal("self-serving must not mint credit")
	}
	if err := l.ChargePublish(n); err != nil {
		t.Fatal("grant should cover exactly one publish")
	}
	if l.CanPublish(n) {
		t.Fatal("after spending the grant the node should be broke")
	}
}

func TestGini(t *testing.T) {
	cases := []struct {
		values []int64
		want   float64
	}{
		{nil, 0},
		{[]int64{0, 0, 0}, 0},
		{[]int64{5, 5, 5, 5}, 0},
		{[]int64{0, 0, 0, 100}, 0.75}, // one node holds everything
		{[]int64{1, 2, 3, 4}, 0.25},
	}
	for _, c := range cases {
		if got := Gini(c.values); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("Gini(%v) = %v, want %v", c.values, got, c.want)
		}
	}
}
