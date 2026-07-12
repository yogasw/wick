package airouter

import (
	"fmt"
	"net"
	"testing"
)

func TestRegistryRegisterGetListSorted(t *testing.T) {
	Register(Descriptor{ID: "zzz-b", DisplayName: "B", PrefPort: 31000})
	Register(Descriptor{ID: "zzz-a", DisplayName: "A", PrefPort: 31001})
	// Idempotent: a duplicate ID is ignored.
	Register(Descriptor{ID: "zzz-a", DisplayName: "A-dup", PrefPort: 31002})

	rt, ok := Get("zzz-a")
	if !ok {
		t.Fatal("Get(zzz-a) not found")
	}
	if rt.Desc.DisplayName != "A" {
		t.Fatalf("duplicate Register clobbered the first: %q", rt.Desc.DisplayName)
	}

	// zzz-a must sort before zzz-b in IDs().
	ai, bi := -1, -1
	for i, id := range IDs() {
		if id == "zzz-a" {
			ai = i
		}
		if id == "zzz-b" {
			bi = i
		}
	}
	if ai < 0 || bi < 0 || ai > bi {
		t.Fatalf("IDs not sorted: a=%d b=%d ids=%v", ai, bi, IDs())
	}
}

func TestAllocPortRemapsWhenTaken(t *testing.T) {
	p1 := allocPort(31900)
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p1))
	if err != nil {
		t.Fatalf("listen p1=%d: %v", p1, err)
	}
	defer ln.Close()

	// With p1 held, a second alloc from the same preferred port must not return
	// the taken port — this is what lets two routers that both default to 20128
	// run concurrently on distinct loopback ports.
	p2 := allocPort(31900)
	if p2 == p1 {
		t.Fatalf("allocPort returned the taken port %d again", p1)
	}
}
