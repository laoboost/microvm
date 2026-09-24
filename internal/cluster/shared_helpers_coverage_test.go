package cluster

import (
	"github.com/google/btree"

	"github.com/aerol-ai/microvm/pkg/capacity"
)

func step3FatCapacity() capacity.Snapshot {
	return capacity.Snapshot{
		HostCPUCores: 16, HostMemoryTotalMB: 65536, HostDiskTotalGB: 1024,
		CPUBudget: 16, MemoryBudgetMB: 65536, DiskBudgetGB: 1024,
		AvailableCPU: 16, AvailableMemoryMB: 65536, AvailableDiskGB: 1024,
		CanAdmit: true,
	}
}

func fmtErr(v any) string {
	if v == nil {
		return ""
	}
	return v.(error).Error()
}

// ownerIndexTree builds a placement owner-index tree from literal IDs, for
// tests that poison or preseed placementFSM.ownerIndex directly.
func ownerIndexTree(ids ...string) *btree.BTreeG[string] {
	t := newPlacementIDIndex()
	for _, id := range ids {
		t.ReplaceOrInsert(id)
	}
	return t
}

// ownerIndexDigest flattens the btree-backed owner index into ordered slices
// so a state digest can marshal it. The tree is already ordered, so the
// encoding is stable across runs — which is what the digest compares.
func ownerIndexDigest(idx map[string]*btree.BTreeG[string]) map[string][]string {
	out := make(map[string][]string, len(idx))
	for node, tree := range idx {
		if tree == nil {
			continue
		}
		ids := []string{}
		tree.Ascend(func(id string) bool {
			ids = append(ids, id)
			return true
		})
		out[node] = ids
	}
	return out
}
