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
