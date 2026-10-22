package diff

import (
	"reflect"
	"testing"

	"code-review-agent/gateway/internal/testfixtures"
)

func TestParseLineMapping(t *testing.T) {
	fd, err := Parse(testfixtures.Path, testfixtures.Patch)
	if err != nil {
		t.Fatal(err)
	}
	if len(fd.Hunks) != 1 {
		t.Fatalf("hunks = %d, want 1", len(fd.Hunks))
	}
	if got, want := fd.AddedLines(), []int{5, 9, 10}; !reflect.DeepEqual(got, want) {
		t.Fatalf("added = %v, want %v", got, want)
	}
	for _, l := range []int{3, 5, 7, 9, 14} {
		if !fd.Commentable(l) {
			t.Errorf("line %d should be commentable", l)
		}
	}
	for _, l := range []int{1, 2, 15, 16} {
		if fd.Commentable(l) {
			t.Errorf("line %d is outside the hunk", l)
		}
	}
	if text, _ := fd.LineText(10); text != "\trow := db.QueryRow(q)" {
		t.Errorf("line 10 text = %q", text)
	}
	if first, last := fd.Hunks[0].NewRange(); first != 3 || last != 14 {
		t.Errorf("new range = %d-%d, want 3-14", first, last)
	}
}

func TestDeletionsBefore(t *testing.T) {
	fd, _ := Parse(testfixtures.Path, testfixtures.Patch)
	dels := fd.DeletionsBefore()
	if len(dels[9]) != 1 {
		t.Fatalf("expected the removed QueryRow line before new line 9, got %v", dels)
	}
}

func TestSameHunk(t *testing.T) {
	fd, err := Parse("f.txt", testfixtures.SecondHunkPatch)
	if err != nil {
		t.Fatal(err)
	}
	if !fd.SameHunk(1, 4) {
		t.Error("1 and 4 are in the first hunk")
	}
	if fd.SameHunk(2, 52) {
		t.Error("2 and 52 are in different hunks")
	}
	if fd.Commentable(10) {
		t.Error("line 10 is between hunks")
	}
}

func TestParseRejectsMalformedHeader(t *testing.T) {
	if _, err := Parse("x", "@@ nonsense @@\n+a"); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseEmptyPatch(t *testing.T) {
	fd, err := Parse("x", "")
	if err != nil || len(fd.Hunks) != 0 || fd.Commentable(1) {
		t.Fatalf("empty patch: %+v %v", fd, err)
	}
}
