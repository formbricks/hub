package repository

import (
	"testing"

	"github.com/google/uuid"

	"github.com/formbricks/hub/internal/models"
)

func TestBuildTaxonomyTreePreservesDeepChildren(t *testing.T) {
	runID := uuid.New()
	rootID := uuid.New()
	level1ID := uuid.New()
	level2ID := uuid.New()
	level3ID := uuid.New()
	firstLeafID := uuid.New()
	secondLeafID := uuid.New()

	root := buildTaxonomyTree([]models.TaxonomyNode{
		{
			ID:        rootID,
			RunID:     runID,
			NodeType:  models.TaxonomyNodeTypeRoot,
			Label:     "Feedback taxonomy",
			Level:     0,
			SortOrder: 0,
		},
		{
			ID:        level1ID,
			RunID:     runID,
			ParentID:  &rootID,
			NodeType:  models.TaxonomyNodeTypeBranch,
			Label:     "Customer Feedback",
			Level:     1,
			SortOrder: 0,
		},
		{
			ID:        level2ID,
			RunID:     runID,
			ParentID:  &level1ID,
			NodeType:  models.TaxonomyNodeTypeBranch,
			Label:     "Product Signals",
			Level:     2,
			SortOrder: 0,
		},
		{
			ID:        level3ID,
			RunID:     runID,
			ParentID:  &level2ID,
			NodeType:  models.TaxonomyNodeTypeBranch,
			Label:     "Generated Topics",
			Level:     3,
			SortOrder: 0,
		},
		{
			ID:        secondLeafID,
			RunID:     runID,
			ParentID:  &level3ID,
			NodeType:  models.TaxonomyNodeTypeLeaf,
			Label:     "Second topic",
			Level:     4,
			SortOrder: 1,
		},
		{
			ID:        firstLeafID,
			RunID:     runID,
			ParentID:  &level3ID,
			NodeType:  models.TaxonomyNodeTypeLeaf,
			Label:     "First topic",
			Level:     4,
			SortOrder: 0,
		},
	})

	if root == nil {
		t.Fatal("buildTaxonomyTree() returned nil")
	}

	level1 := onlyChild(t, root, "Feedback taxonomy")
	level2 := onlyChild(t, level1, "Customer Feedback")
	level3 := onlyChild(t, level2, "Product Signals")

	if got := len(level3.Children); got != 2 {
		t.Fatalf("level 3 child count = %d, want 2", got)
	}

	if level3.Children[0].ID != firstLeafID {
		t.Fatalf("first leaf ID = %s, want %s", level3.Children[0].ID, firstLeafID)
	}

	if level3.Children[1].ID != secondLeafID {
		t.Fatalf("second leaf ID = %s, want %s", level3.Children[1].ID, secondLeafID)
	}
}

// TestRollUpNodeRecordCounts verifies that per-cluster record counts roll up into subtree totals:
// a leaf reports its own cluster's records, a branch reports the sum of its descendants, and the
// root reports the whole run. Nodes with no cluster, clusters absent from the count map, and
// orphaned subtrees (parent not visible) are all handled.
func TestRollUpNodeRecordCounts(t *testing.T) {
	rootID := uuid.New()
	branchAID := uuid.New()
	leafA1ID := uuid.New()
	leafA2ID := uuid.New()
	leafBID := uuid.New()
	orphanID := uuid.New()

	clusterA1 := uuid.New()
	clusterA2 := uuid.New()
	clusterB := uuid.New()
	clusterEmpty := uuid.New() // referenced by a node but absent from the counts map -> 0
	clusterOrphan := uuid.New()

	ptr := func(id uuid.UUID) *uuid.UUID { return &id }

	nodes := []models.TaxonomyNode{
		{ID: rootID, NodeType: models.TaxonomyNodeTypeRoot, Level: 0},
		{ID: branchAID, ParentID: ptr(rootID), NodeType: models.TaxonomyNodeTypeBranch, Level: 1},
		{ID: leafA1ID, ParentID: ptr(branchAID), ClusterID: ptr(clusterA1), NodeType: models.TaxonomyNodeTypeLeaf, Level: 2},
		{ID: leafA2ID, ParentID: ptr(branchAID), ClusterID: ptr(clusterA2), NodeType: models.TaxonomyNodeTypeLeaf, Level: 2},
		{ID: leafBID, ParentID: ptr(rootID), ClusterID: ptr(clusterB), NodeType: models.TaxonomyNodeTypeLeaf, Level: 1},
		// Parent is not in the visible set: treated as its own subtree root.
		{ID: orphanID, ParentID: ptr(uuid.New()), ClusterID: ptr(clusterOrphan), NodeType: models.TaxonomyNodeTypeLeaf, Level: 2},
	}

	clusterCounts := map[uuid.UUID]int64{
		clusterA1:     3,
		clusterA2:     5,
		clusterB:      2,
		clusterEmpty:  9, // no node references this; must not appear anywhere
		clusterOrphan: 4,
	}

	got := rollUpNodeRecordCounts(nodes, clusterCounts)

	if len(got) != len(nodes) {
		t.Fatalf("count entries = %d, want %d (one per visible node)", len(got), len(nodes))
	}

	byNode := make(map[uuid.UUID]int64, len(got))
	for _, c := range got {
		byNode[c.NodeID] = c.RecordCount
	}

	want := map[uuid.UUID]int64{
		leafA1ID:  3,
		leafA2ID:  5,
		branchAID: 8, // 3 + 5
		leafBID:   2,
		rootID:    10, // 8 (branch A) + 2 (leaf B); root has no own cluster
		orphanID:  4,  // orphan subtree counted independently, not folded into root
	}

	for id, wantCount := range want {
		if byNode[id] != wantCount {
			t.Fatalf("node %s count = %d, want %d", id, byNode[id], wantCount)
		}
	}
}

func onlyChild(t *testing.T, node *models.TaxonomyNode, label string) *models.TaxonomyNode {
	t.Helper()

	if node.Label != label {
		t.Fatalf("node label = %q, want %q", node.Label, label)
	}

	if got := len(node.Children); got != 1 {
		t.Fatalf("%q child count = %d, want 1", label, got)
	}

	return &node.Children[0]
}
