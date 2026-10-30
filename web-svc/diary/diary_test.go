package diary

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTree_OrdersNewestFirstAndCountsEntries(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "2026", "03", "01", "entry-08-00-00.txt"), "a")
	writeFile(t, filepath.Join(root, "2026", "03", "01", "entry-09-00-00.txt"), "b")
	writeFile(t, filepath.Join(root, "2026", "03", "05", "entry-09-00-00.txt"), "c")
	writeFile(t, filepath.Join(root, "2025", "12", "31", "entry-09-00-00.txt"), "d")

	got, err := Tree(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []Year{
		{Year: "2026", Months: []Month{{Month: "03", Days: []Day{{Day: "05", Count: 1}, {Day: "01", Count: 2}}}}},
		{Year: "2025", Months: []Month{{Month: "12", Days: []Day{{Day: "31", Count: 1}}}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestTree_IgnoresStrayFilesAndFolders(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "desktop.ini"), "x")
	if err := os.Mkdir(filepath.Join(root, "abc"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "2026", "notes.txt"), "x")
	writeFile(t, filepath.Join(root, "2026", "03", "04", "x.tmp"), "x")

	got, err := Tree(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty tree, got %+v", got)
	}
}

func TestTree_MissingRoot_ReturnsErrRootUnavailable(t *testing.T) {
	_, err := Tree(filepath.Join(t.TempDir(), "nope"))
	if !errors.Is(err, ErrRootUnavailable) {
		t.Fatalf("got %v", err)
	}
}

func TestListDay_ReturnsEntriesAscendingWithContent(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "2026", "03", "04", "entry-10-00-00.txt"), "later")
	writeFile(t, filepath.Join(root, "2026", "03", "04", "entry-09-30-00.txt"), "earlier")
	writeFile(t, filepath.Join(root, "2026", "03", "04", "stray.txt"), "no")

	got, err := ListDay(root, "2026-03-04")
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{
		{Name: "entry-09-30-00.txt", Time: "09:30:00", Content: "earlier"},
		{Name: "entry-10-00-00.txt", Time: "10:00:00", Content: "later"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestListDay_InvalidDate_ReturnsErrInvalidName(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"2026-3-1", "../x", ""} {
		if _, err := ListDay(root, d); !errors.Is(err, ErrInvalidName) {
			t.Errorf("date %q: got %v", d, err)
		}
	}
}

func TestListDay_MissingDay_ReturnsErrNotFound(t *testing.T) {
	root := t.TempDir()
	if _, err := ListDay(root, "2026-03-04"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestCreate_WritesToDatedPathAndCreatesFolders(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 3, 4, 9, 30, 0, 0, time.UTC)
	date, e, err := Create(root, "hello", now)
	if err != nil {
		t.Fatal(err)
	}
	if date != "2026-03-04" || e.Time != "09:30:00" || e.Name != "entry-09-30-00.txt" || e.Content != "hello" {
		t.Fatalf("got %q %+v", date, e)
	}
	b, err := os.ReadFile(filepath.Join(root, "2026", "03", "04", "entry-09-30-00.txt"))
	if err != nil || string(b) != "hello" {
		t.Fatalf("file: %v %q", err, b)
	}
}

func TestCreate_SameSecond_AdvancesToNextSecond(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 3, 4, 9, 30, 0, 0, time.UTC)
	if _, _, err := Create(root, "first", now); err != nil {
		t.Fatal(err)
	}
	_, e, err := Create(root, "second", now)
	if err != nil {
		t.Fatal(err)
	}
	if e.Time != "09:30:01" {
		t.Fatalf("time %s", e.Time)
	}
	b, _ := os.ReadFile(filepath.Join(root, "2026", "03", "04", "entry-09-30-00.txt"))
	if string(b) != "first" {
		t.Fatalf("first overwritten: %q", b)
	}
}

func TestCreate_EmptyContent_ReturnsErrEmptyContent(t *testing.T) {
	root := t.TempDir()
	_, _, err := Create(root, "  \n", time.Now())
	if !errors.Is(err, ErrEmptyContent) {
		t.Fatalf("got %v", err)
	}
}

func TestCreate_MissingRoot_ReturnsErrRootUnavailable(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nope")
	_, _, err := Create(root, "x", time.Now())
	if !errors.Is(err, ErrRootUnavailable) {
		t.Fatalf("got %v", err)
	}
	if _, statErr := os.Stat(root); !os.IsNotExist(statErr) {
		t.Fatalf("root was created")
	}
}

func TestUpdate_OverwritesExisting(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "2026", "03", "04", "entry-09-30-00.txt")
	writeFile(t, p, "old")
	if err := Update(root, "2026-03-04", "entry-09-30-00.txt", "new"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "new" {
		t.Fatalf("got %q", b)
	}
}

func TestUpdate_Missing_ReturnsErrNotFound(t *testing.T) {
	root := t.TempDir()
	err := Update(root, "2026-03-04", "entry-09-30-00.txt", "x")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestUpdate_TraversalNames_ReturnErrInvalidName(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"../entry-09-30-00.txt", "..\\x", "entry-09-30-00.txt/..", "C:\\x.txt"} {
		if err := Update(root, "2026-03-04", n, "x"); !errors.Is(err, ErrInvalidName) {
			t.Errorf("name %q: got %v", n, err)
		}
	}
	if err := Update(root, "../..", "entry-09-30-00.txt", "x"); !errors.Is(err, ErrInvalidName) {
		t.Errorf("date: got %v", err)
	}
}
