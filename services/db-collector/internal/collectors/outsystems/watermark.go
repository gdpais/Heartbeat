package outsystems

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// watermarkVersion is the version of the watermark file format.
const watermarkVersion = 1

// wallLayout formats a wall-clock Instant without a time zone, to the 100 ns
// precision of datetime2.
const wallLayout = "2006-01-02T15:04:05.9999999"

// WatermarkKey identifies one watermark: a log type of a target of a
// collector.
type WatermarkKey struct {
	Collector string
	Target    string
	LogType   string
}

// Watermark is the last Instant delivered for a [WatermarkKey].
type Watermark struct {
	// Instant is a wall-clock value in the log time zone (see [Wall]).
	// Extraction reads the rows after it.
	Instant time.Time
	// InstantType is the database type of the Instant column, such as
	// "DATETIME"; empty until a query has run.
	InstantType string
	// Source is the database it was read from, host:port/database.  A
	// watermark is only used for the same source: after a target is pointed at
	// another database, extraction starts over from initial_lookback.
	Source string
}

// watermarkFile is the JSON content of a watermark file.
type watermarkFile struct {
	Version     int       `json:"version"`
	Collector   string    `json:"collector"`
	Target      string    `json:"target"`
	LogType     string    `json:"log_type"`
	Source      string    `json:"source"`
	Instant     string    `json:"instant"`
	InstantType string    `json:"instant_type,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Store keeps watermarks in memory and in one small JSON file per key under
// its directory, written atomically: a temporary file in the same directory
// is written and synced, then renamed over the old one, and the directory is
// synced.  A crash leaves either the old or the new watermark, never a mix.
// Files of removed collectors are left in place.  Store is safe for
// concurrent use.
type Store struct {
	dir string

	mu    sync.Mutex
	cache map[WatermarkKey]Watermark
}

// NewStore returns a store that keeps its files in dir, creating it on the
// first save.
func NewStore(dir string) *Store {
	return &Store{dir: dir, cache: map[WatermarkKey]Watermark{}}
}

// Dir returns the store's directory.
func (s *Store) Dir() string { return s.dir }

// Load returns the watermark of key: the one in memory, or else the one in
// its file.  found is false when there is neither; err reports a file that
// exists but cannot be read or parsed.
func (s *Store) Load(key WatermarkKey) (watermark Watermark, found bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if watermark, ok := s.cache[key]; ok {
		return watermark, true, nil
	}
	content, err := os.ReadFile(s.path(key))
	if errors.Is(err, fs.ErrNotExist) {
		return Watermark{}, false, nil
	}
	if err != nil {
		return Watermark{}, false, err
	}
	var file watermarkFile
	if err := json.Unmarshal(content, &file); err != nil {
		return Watermark{}, false, fmt.Errorf("parse %s: %w", s.path(key), err)
	}
	if file.Version != watermarkVersion {
		return Watermark{}, false, fmt.Errorf("%s has version %d, want %d", s.path(key), file.Version, watermarkVersion)
	}
	instant, err := time.Parse(wallLayout, file.Instant)
	if err != nil {
		return Watermark{}, false, fmt.Errorf("parse %s: instant: %w", s.path(key), err)
	}
	watermark = Watermark{Instant: instant, InstantType: file.InstantType, Source: file.Source}
	s.cache[key] = watermark
	return watermark, true, nil
}

// Remember keeps watermark for key in memory only.  Extraction uses it for
// the starting point of a log type without a usable file, so it does not
// move, or warn again, while the first batch keeps failing.
func (s *Store) Remember(key WatermarkKey, watermark Watermark) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache[key] = watermark
}

// Save stores watermark for key in memory and then in its file.  When the
// file cannot be written the watermark still moves in memory, so delivered
// rows are not sent again every cycle, and the error is returned: a restart
// would resume from the last watermark on disk.
func (s *Store) Save(key WatermarkKey, watermark Watermark, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache[key] = watermark
	content, err := json.MarshalIndent(watermarkFile{
		Version:     watermarkVersion,
		Collector:   key.Collector,
		Target:      key.Target,
		LogType:     key.LogType,
		Source:      watermark.Source,
		Instant:     watermark.Instant.Format(wallLayout),
		InstantType: watermark.InstantType,
		UpdatedAt:   now.UTC(),
	}, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(s.path(key), append(content, '\n')); err != nil {
		return fmt.Errorf("save watermark: %w", err)
	}
	return nil
}

// path returns the file of key: <dir>/<collector>.<target>.<log type>.json,
// with the names escaped so that no name can reach outside dir or collide
// with another.
func (s *Store) path(key WatermarkKey) string {
	return filepath.Join(s.dir, escapeName(key.Collector)+"."+escapeName(key.Target)+"."+escapeName(key.LogType)+".json")
}

// escapeName percent-encodes every byte of name except ASCII letters,
// digits, '-' and '_', so the result has no '.', '/' or '\'.
func escapeName(name string) string {
	var b strings.Builder
	for _, c := range []byte(name) {
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9', c == '-', c == '_':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// writeFileAtomic replaces path with content through a synced temporary file
// and a rename, then syncs the directory so the rename survives a crash.
func writeFileAtomic(path string, content []byte) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".watermark-*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	return syncDir(dir)
}

// syncDir flushes a directory's entries to disk.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
