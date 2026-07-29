package main

import "testing"

func cp(rows []rootRow) []rootRow { return append([]rootRow(nil), rows...) }

func TestPaginateRoots(t *testing.T) {
	rows := []rootRow{
		{Root: "cc", Shards: 5},
		{Root: "aa", Shards: 10},
		{Root: "bb", Shards: 10},
		{Root: "dd", Shards: 1},
	}
	// Sort: shards desc, then root asc → aa(10), bb(10), cc(5), dd(1).
	page, total := paginateRoots(cp(rows), 2, 0)
	if total != 4 {
		t.Fatalf("total=%d, want 4", total)
	}
	if len(page) != 2 || page[0].Root != "aa" || page[1].Root != "bb" {
		t.Fatalf("page1=%+v, want aa,bb (shards desc, root tiebreak)", page)
	}
	// Second page.
	if page, _ = paginateRoots(cp(rows), 2, 2); len(page) != 2 || page[0].Root != "cc" || page[1].Root != "dd" {
		t.Fatalf("page2=%+v, want cc,dd", page)
	}
	// Offset past the end → empty page, total intact (no panic).
	if page, total = paginateRoots(cp(rows), 2, 99); len(page) != 0 || total != 4 {
		t.Fatalf("overshoot: page=%d total=%d, want 0,4", len(page), total)
	}
	// limit <= 0 uses the default page (all 4 fit).
	if page, _ = paginateRoots(cp(rows), 0, 0); len(page) != 4 {
		t.Fatalf("default page len=%d, want 4", len(page))
	}
	// Negative offset clamps to 0.
	if page, _ = paginateRoots(cp(rows), 2, -5); page[0].Root != "aa" {
		t.Fatalf("negative offset should clamp to start, got %+v", page)
	}
	// Empty input.
	if page, total = paginateRoots(nil, 10, 0); len(page) != 0 || total != 0 {
		t.Fatalf("empty: page=%d total=%d, want 0,0", len(page), total)
	}
}

func TestAtoiOr(t *testing.T) {
	if atoiOr("42", 7) != 42 {
		t.Fatal("valid int should parse")
	}
	if atoiOr("", 7) != 7 || atoiOr("nope", 7) != 7 {
		t.Fatal("empty/garbage should fall back to default")
	}
}
