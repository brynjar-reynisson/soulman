// Package diary provides validated read/write access to diary entries under
// a root directory (web.diary_root). Layout:
//
//	<root>/<YYYY>/<MM>/<DD>/entry-<HH-MM-SS>.txt
//
// One file per entry. Every function validates its date/name arguments
// (fixed-width digits, a strict filename regexp) before touching the
// filesystem, and builds paths only from those validated parts, since this
// is reachable from the internet-tunneled dashboard. The root itself is
// never created: if it is missing (e.g. an unmounted drive) functions
// return ErrRootUnavailable. Unrecognised files/folders are ignored.
package diary

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var (
	ErrNotFound        = errors.New("diary: not found")
	ErrExists          = errors.New("diary: already exists")
	ErrInvalidName     = errors.New("diary: invalid name")
	ErrRootUnavailable = errors.New("diary: root unavailable")
	ErrEmptyContent    = errors.New("diary: empty content")
)

type Day struct {
	Day   string `json:"day"`
	Count int    `json:"count"`
}

type Month struct {
	Month string `json:"month"`
	Days  []Day  `json:"days"`
}

type Year struct {
	Year   string  `json:"year"`
	Months []Month `json:"months"`
}

type Entry struct {
	Name    string `json:"name"`
	Time    string `json:"time"`
	Content string `json:"content"`
}

var (
	nameRe = regexp.MustCompile(`^entry-(\d{2})-(\d{2})-(\d{2})\.txt$`)
	dateRe = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})$`)
)

func isDigits(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// entryTime returns "HH:MM:SS" if name is a valid entry filename.
func entryTime(name string) (string, bool) {
	m := nameRe.FindStringSubmatch(name)
	if m == nil {
		return "", false
	}
	return m[1] + ":" + m[2] + ":" + m[3], true
}

func splitDate(date string) (y, m, d string, err error) {
	p := dateRe.FindStringSubmatch(date)
	if p == nil {
		return "", "", "", ErrInvalidName
	}
	return p[1], p[2], p[3], nil
}

func checkRoot(root string) error {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("%w: %s", ErrRootUnavailable, root)
	}
	return nil
}

// subdirs lists directory names under dir with exactly width digits, descending.
func subdirs(dir string, width int) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && isDigits(e.Name(), width) {
			out = append(out, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out, nil
}

func countEntries(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && nameRe.MatchString(e.Name()) {
			n++
		}
	}
	return n
}

// Tree returns years/months/days (newest first) that contain at least one entry.
func Tree(root string) ([]Year, error) {
	years, err := subdirs(root, 4)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRootUnavailable, err)
	}
	out := []Year{}
	for _, y := range years {
		months, err := subdirs(filepath.Join(root, y), 2)
		if err != nil {
			continue
		}
		var ms []Month
		for _, m := range months {
			days, err := subdirs(filepath.Join(root, y, m), 2)
			if err != nil {
				continue
			}
			var ds []Day
			for _, d := range days {
				if c := countEntries(filepath.Join(root, y, m, d)); c > 0 {
					ds = append(ds, Day{Day: d, Count: c})
				}
			}
			if len(ds) > 0 {
				ms = append(ms, Month{Month: m, Days: ds})
			}
		}
		if len(ms) > 0 {
			out = append(out, Year{Year: y, Months: ms})
		}
	}
	return out, nil
}

// ListDay returns the entries for date (YYYY-MM-DD), ascending by name.
func ListDay(root, date string) ([]Entry, error) {
	y, m, d, err := splitDate(date)
	if err != nil {
		return nil, err
	}
	if err := checkRoot(root); err != nil {
		return nil, err
	}
	dir := filepath.Join(root, y, m, d)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("diary: reading %s: %w", dir, err)
	}
	out := []Entry{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		t, ok := entryTime(e.Name())
		if !ok {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("diary: reading %s: %w", e.Name(), err)
		}
		out = append(out, Entry{Name: e.Name(), Time: t, Content: string(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Create writes a new entry stamped now. If that second is taken it advances
// one second at a time (bounded) until a free slot is found.
func Create(root, content string, now time.Time) (string, Entry, error) {
	if strings.TrimSpace(content) == "" {
		return "", Entry{}, ErrEmptyContent
	}
	if err := checkRoot(root); err != nil {
		return "", Entry{}, err
	}
	for i := 0; i < 60; i++ {
		t := now.Add(time.Duration(i) * time.Second)
		dir := filepath.Join(root, t.Format("2006"), t.Format("01"), t.Format("02"))
		name := t.Format("entry-15-04-05") + ".txt"
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", Entry{}, fmt.Errorf("diary: creating %s: %w", dir, err)
		}
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			if os.IsExist(err) {
				continue
			}
			return "", Entry{}, fmt.Errorf("diary: creating %s: %w", name, err)
		}
		_, werr := f.WriteString(content)
		cerr := f.Close()
		if werr != nil {
			return "", Entry{}, fmt.Errorf("diary: writing %s: %w", name, werr)
		}
		if cerr != nil {
			return "", Entry{}, fmt.Errorf("diary: closing %s: %w", name, cerr)
		}
		return t.Format("2006-01-02"), Entry{Name: name, Time: t.Format("15:04:05"), Content: content}, nil
	}
	return "", Entry{}, ErrExists
}

// Update overwrites an existing entry in place.
func Update(root, date, name, content string) error {
	y, m, d, err := splitDate(date)
	if err != nil {
		return err
	}
	if _, ok := entryTime(name); !ok {
		return ErrInvalidName
	}
	if err := checkRoot(root); err != nil {
		return err
	}
	path := filepath.Join(root, y, m, d, name)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		if err == nil || os.IsNotExist(err) {
			return ErrNotFound
		}
		return fmt.Errorf("diary: stat %s: %w", name, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("diary: writing %s: %w", name, err)
	}
	return nil
}
