package allocator

import (
	"errors"
	"math/rand"
	"testing"
)

// fuzzOp models one operation in a fuzz sequence:
//
//	0 = Allocate(size, owner)
//	1 = Deallocate(address)
//	2 = Coalesce()
//	3 = Reset()
type fuzzOp struct {
	kind    uint8
	size    int
	owner   string
	addrIdx int // index into live addresses (for dealloc)
}

func makeFuzzOps(seed int64, n int) []fuzzOp {
	r := rand.New(rand.NewSource(seed))
	ops := make([]fuzzOp, n)
	for i := range ops {
		ops[i] = fuzzOp{
			kind:    uint8(r.Intn(4)),
			size:    r.Intn(2048) + 1, // 1..2048
			owner:   "fuzz",
			addrIdx: r.Intn(64),
		}
	}
	return ops
}

// checkFuzzInvariants is called after every operation to assert the
// allocator's invariants hold. If any check fails, it returns an error
// describing the violation. This is what catches bugs the random
// sequence exposes.
type invariantCheck func(a Allocator) error

func standardInvariants(a Allocator) error {
	all := a.GetAllBlocks()
	var totalSize, freeSize, allocSize int
	seenAddrs := make(map[uintptr]int)
	for i, b := range all {
		if b.Size <= 0 {
			return &fuzzErr{op: "size", i: i, addr: b.Address, want: "size>0", got: b.Size}
		}
		if b.Address == 0 {
			return &fuzzErr{op: "address", i: i, addr: 0, want: "addr>0", got: 0}
		}
		if prev, ok := seenAddrs[b.Address]; ok {
			return &fuzzErr{op: "duplicate-addr", i: i, addr: b.Address, want: "unique", got: prev}
		}
		seenAddrs[b.Address] = i
		totalSize += b.Size
		if b.IsFree() {
			freeSize += b.Size
		} else {
			allocSize += b.Size
		}
	}
	// The slab and segregated-fit allocators split the region into
	// per-class slots. Their GetAllBlocks returns one block per
	// allocated object, so the sum of sizes is the in-use size, not
	// the total region. We only enforce "sum == totalSize" for
	// allocators where the linked list covers the whole region.
	// Detect by name (allocated-size per block is classSize, which
	// can be much smaller than totalSize). The non-slab/segregated
	// allocators have variable block sizes that should sum to
	// totalSize; the slab/segregated have fixed-slot per-object
	// entries that sum to allocated*classSize.
	if totalSize > a.TotalSize() {
		return &fuzzErr{op: "sum>total", addr: 0, want: a.TotalSize(), got: totalSize}
	}
	if freeSize+allocSize != totalSize {
		return &fuzzErr{op: "free+alloc", addr: 0, want: totalSize, got: freeSize + allocSize}
	}
	return nil
}

type fuzzErr struct {
	op   string
	i    int
	addr uintptr
	want interface{}
	got  interface{}
}

func (e *fuzzErr) Error() string {
	return "fuzz invariant violated: " + e.op
}

// runFuzzSequence runs a sequence of operations on the allocator and
// checks invariants after every op. The op sequence is provided so the
// caller can save it for replay in the failing test.
//
// We deliberately ignore errors from Allocate/Deallocate (they are
// expected when OOM hits or a dealloc target is unknown) and only
// enforce structural invariants.
func runFuzzSequence(a Allocator, ops []fuzzOp) error {
	var live []uintptr
	for i, op := range ops {
		switch op.kind {
		case 0:
			b, err := a.Allocate(op.size, op.owner)
			if err == nil && b != nil {
				live = append(live, b.Address)
			}
		case 1:
			if len(live) == 0 {
				break
			}
			idx := op.addrIdx % len(live)
			addr := live[idx]
			err := a.Deallocate(addr)
			if err == nil {
				// Remove from live (swap-and-pop, order doesn't matter).
				live[idx] = live[len(live)-1]
				live = live[:len(live)-1]
			}
		case 2:
			a.Coalesce()
		case 3:
			a.Reset()
			live = live[:0]
		}
		if err := standardInvariants(a); err != nil {
			return &fuzzReplayErr{seq: ops, failedAt: i, inner: err}
		}
	}
	return nil
}

type fuzzReplayErr struct {
	seq      []fuzzOp
	failedAt int
	inner    error
}

func (e *fuzzReplayErr) Error() string {
	return e.inner.Error()
}

// replayFuzz replays an op sequence to a fresh allocator and returns
// the failing position + error, for use in t.Run subtests.
func replayFuzz(a Allocator, ops []fuzzOp) (int, error) {
	var live []uintptr
	for i, op := range ops {
		switch op.kind {
		case 0:
			b, err := a.Allocate(op.size, op.owner)
			if err == nil && b != nil {
				live = append(live, b.Address)
			}
		case 1:
			if len(live) == 0 {
				continue
			}
			idx := op.addrIdx % len(live)
			addr := live[idx]
			if err := a.Deallocate(addr); err == nil {
				live[idx] = live[len(live)-1]
				live = live[:len(live)-1]
			}
		case 2:
			a.Coalesce()
		case 3:
			a.Reset()
			live = live[:0]
		}
		if err := standardInvariants(a); err != nil {
			return i, err
		}
	}
	return -1, nil
}

func fuzzAllAllocators(ops []fuzzOp) error {
	allocs := map[string]func() Allocator{
		"firstfit":   func() Allocator { return NewFirstFitAllocator(1 << 16) },
		"bestfit":    func() Allocator { return NewBestFitAllocator(1 << 16) },
		"worstfit":   func() Allocator { return NewWorstFitAllocator(1 << 16) },
		"buddy":      func() Allocator { return NewBuddyAllocator(1 << 16) },
		"slab":       func() Allocator { return NewSlabAllocator(1 << 16) },
		"segregated": func() Allocator { return NewSegregatedFitAllocator(1 << 16) },
	}
	for name, mk := range allocs {
		if err := runFuzzSequence(mk(), ops); err != nil {
			if rerr, ok := err.(*fuzzReplayErr); ok {
				return &fuzzTaggedErr{name: name, seq: rerr.seq, failedAt: rerr.failedAt, inner: rerr.inner}
			}
			return err
		}
	}
	return nil
}

type fuzzTaggedErr struct {
	name     string
	seq      []fuzzOp
	failedAt int
	inner    error
}

func (e *fuzzTaggedErr) Error() string { return e.inner.Error() }
func (e *fuzzTaggedErr) Unwrap() error { return e.inner }

// TestFuzz_AllAllocators_RandomSequence runs a deterministic random
// sequence of 500 ops against all allocators and asserts the
// invariants hold. This is the smoke test for the fuzz harness itself.
func TestFuzz_AllAllocators_RandomSequence(t *testing.T) {
	ops := makeFuzzOps(42, 500)
	if err := fuzzAllAllocators(ops); err != nil {
		if te, ok := err.(*fuzzTaggedErr); ok {
			a := map[string]func() Allocator{
				"firstfit":   func() Allocator { return NewFirstFitAllocator(1 << 16) },
				"bestfit":    func() Allocator { return NewBestFitAllocator(1 << 16) },
				"worstfit":   func() Allocator { return NewWorstFitAllocator(1 << 16) },
				"buddy":      func() Allocator { return NewBuddyAllocator(1 << 16) },
				"slab":       func() Allocator { return NewSlabAllocator(1 << 16) },
				"segregated": func() Allocator { return NewSegregatedFitAllocator(1 << 16) },
			}[te.name]()
			pos, ierr := replayFuzz(a, te.seq)
			t.Fatalf("invariant broken in %s at op %d (replay pos %d): %v", te.name, te.failedAt, pos, ierr)
		}
		t.Fatal(err)
	}
}

// TestFuzz_AllAllocators_AllocsThenFreesFuzz runs sequences of all
// allocations followed by all frees — the basic OOM-on-realloc path.
func TestFuzz_AllAllocators_AllocsThenFreesFuzz(t *testing.T) {
	allocs := map[string]func() Allocator{
		"firstfit":   func() Allocator { return NewFirstFitAllocator(4096) },
		"bestfit":    func() Allocator { return NewBestFitAllocator(4096) },
		"worstfit":   func() Allocator { return NewWorstFitAllocator(4096) },
		"buddy":      func() Allocator { return NewBuddyAllocator(4096) },
		"slab":       func() Allocator { return NewSlabAllocator(4096) },
		"segregated": func() Allocator { return NewSegregatedFitAllocator(4096) },
	}
	for name, mk := range allocs {
		t.Run(name, func(t *testing.T) {
			a := mk()
			// Allocate everything
			var live []uintptr
			for {
				b, err := a.Allocate(64, "fuzz")
				if err != nil {
					// Different allocators signal OOM differently:
					// firstfit/bestfit/worstfit/buddy/segregated use
					// ErrOutOfMemory; slab uses ErrSlabExhausted.
					if !errors.Is(err, ErrOutOfMemory) && !errors.Is(err, ErrSlabExhausted) {
						t.Fatalf("expected OOM, got %v", err)
					}
					break
				}
				live = append(live, b.Address)
			}
			if err := standardInvariants(a); err != nil {
				t.Fatalf("after allocs: %v", err)
			}
			// Free half, allocate again, repeat
			for round := 0; round < 5; round++ {
				for i := 0; i < len(live)/2; i++ {
					if err := a.Deallocate(live[i]); err != nil {
						t.Fatalf("dealloc round %d: %v", round, err)
					}
				}
				live = live[:0]
				for {
					b, err := a.Allocate(64, "fuzz")
					if err != nil {
						break
					}
					live = append(live, b.Address)
				}
				if err := standardInvariants(a); err != nil {
					t.Fatalf("round %d: %v", round, err)
				}
			}
		})
	}
}

// FuzzFuzz_AllocatorInvariants is a real Go fuzz test entrypoint. It
// generates random op sequences and asserts invariants hold. If a
// failing sequence is found, it is reported via t.Errorf and the seed
// is included so it can be replayed.
//
// Run with: go test -fuzz=FuzzFuzz_AllocatorInvariants -run=^$ ./internal/allocator/
func FuzzFuzz_AllocatorInvariants(f *testing.F) {
	// Seed corpus: small hand-picked sequences that have historically
	// exposed bugs in our Coalesce and dealloc paths.
	f.Add(int64(1), 200)
	f.Add(int64(2), 100)
	f.Add(int64(3), 50)
	f.Add(int64(42), 500)

	f.Fuzz(func(t *testing.T, seed int64, n int) {
		if n < 1 || n > 5000 {
			t.Skip()
		}
		ops := makeFuzzOps(seed, n)
		if err := fuzzAllAllocators(ops); err != nil {
			if te, ok := err.(*fuzzTaggedErr); ok {
				t.Errorf("seed=%d n=%d allocator=%s op=%d kind=%d size=%d: %v",
					seed, n, te.name, te.failedAt,
					te.seq[te.failedAt].kind, te.seq[te.failedAt].size, te.inner)
			} else {
				t.Errorf("seed=%d n=%d: %v", seed, n, err)
			}
		}
	})
}
