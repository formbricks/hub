package service

import (
	"errors"
	"testing"
)

func TestBuildListPaginationMeta_NoHasMore(t *testing.T) {
	encodeCalled := false

	meta, err := BuildListPaginationMeta(10, false, func() (string, error) {
		encodeCalled = true

		return "cursor", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if meta.Limit != 10 {
		t.Errorf("expected Limit 10, got %d", meta.Limit)
	}

	if meta.NextCursor != "" {
		t.Errorf("hasMore false should produce empty NextCursor, got %q", meta.NextCursor)
	}

	if encodeCalled {
		t.Error("encodeLast should not be called when hasMore is false")
	}
}

func TestBuildListPaginationMeta_HasMore_EncodeSucceeds(t *testing.T) {
	meta, err := BuildListPaginationMeta(5, true, func() (string, error) {
		return "abc123", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if meta.Limit != 5 {
		t.Errorf("expected Limit 5, got %d", meta.Limit)
	}

	if meta.NextCursor != "abc123" {
		t.Errorf("expected NextCursor abc123, got %q", meta.NextCursor)
	}
}

func TestBuildListPaginationMeta_HasMore_EncodeError(t *testing.T) {
	encodeErr := errors.New("encode failed")

	meta, err := BuildListPaginationMeta(5, true, func() (string, error) {
		return "", encodeErr
	})
	if err == nil {
		t.Fatal("expected error from encodeLast")
	}

	if !errors.Is(err, encodeErr) {
		t.Errorf("expected errors.Is(encodeErr), got %v", err)
	}

	if meta.Limit != 5 {
		t.Errorf("meta.Limit should still be set, got %d", meta.Limit)
	}

	if meta.NextCursor != "" {
		t.Errorf("on error, NextCursor should be empty, got %q", meta.NextCursor)
	}
}

func TestBuildListPaginationMeta_HasMore_NilEncodeLast(t *testing.T) {
	_, err := BuildListPaginationMeta(10, true, nil)
	if !errors.Is(err, ErrPaginationInvariantViolated) {
		t.Fatalf("expected ErrPaginationInvariantViolated, got %v", err)
	}
}
