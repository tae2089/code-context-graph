package paging

import "testing"

func TestNormalize_DefaultsLimit(t *testing.T) {
	got, err := Normalize(Request{})
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}
	if got.Limit != DefaultLimit {
		t.Fatalf("limit = %d, want %d", got.Limit, DefaultLimit)
	}
	if got.Offset != 0 {
		t.Fatalf("offset = %d, want 0", got.Offset)
	}
}

func TestNormalize_RejectsTooLargeLimit(t *testing.T) {
	_, err := Normalize(Request{Limit: MaxLimit + 1})
	if err == nil {
		t.Fatal("expected error for limit above max")
	}
}

func TestNormalize_RejectsNegativeOffset(t *testing.T) {
	_, err := Normalize(Request{Limit: 10, Offset: -1})
	if err == nil {
		t.Fatal("expected error for negative offset")
	}
}

func TestNormalizeWithDefault_UsesProvidedDefault(t *testing.T) {
	got, err := NormalizeWithDefault(Request{}, 10)
	if err != nil {
		t.Fatalf("NormalizeWithDefault returned error: %v", err)
	}
	if got.Limit != 10 {
		t.Fatalf("limit = %d, want 10", got.Limit)
	}
}

func TestBuildPage_SetsNextOffsetWhenMoreResultsExist(t *testing.T) {
	page := BuildPage(Request{Limit: 20, Offset: 40}, 20, true)
	if !page.HasMore {
		t.Fatal("expected has_more=true")
	}
	if page.NextOffset == nil || *page.NextOffset != 60 {
		t.Fatalf("next_offset = %v, want 60", page.NextOffset)
	}
}
