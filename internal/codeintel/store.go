package codeintel

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type Symbol struct {
	ID        int64
	File      string
	Name      string
	Kind      string
	Signature string
	Line      int
}

type Call struct {
	CallerFile   string
	CallerSymbol string
	CalleeName   string
	Line         int
}

type Store struct {
	db *sql.DB
}

func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS files (
  path TEXT PRIMARY KEY,
  lang TEXT,
  mtime INTEGER,
  size INTEGER
);
CREATE TABLE IF NOT EXISTS symbols (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  file TEXT,
  name TEXT,
  kind TEXT,
  signature TEXT,
  line INTEGER
);
CREATE INDEX IF NOT EXISTS idx_symbols_name ON symbols(name);
CREATE INDEX IF NOT EXISTS idx_symbols_file ON symbols(file);
CREATE TABLE IF NOT EXISTS calls (
  caller_file TEXT,
  caller_symbol TEXT,
  callee_name TEXT,
  line INTEGER
);
CREATE INDEX IF NOT EXISTS idx_calls_callee ON calls(callee_name);
CREATE INDEX IF NOT EXISTS idx_calls_caller ON calls(caller_symbol);
`)
	if err != nil {
		return err
	}
	s.db.Exec("ALTER TABLE files ADD COLUMN size INTEGER")
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

type FileStat struct {
	MTime int64
	Size  int64
}

func (s *Store) FileMTimes(ctx context.Context) (map[string]FileStat, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT path, mtime, size FROM files")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]FileStat{}
	for rows.Next() {
		var p string
		var st FileStat
		if err := rows.Scan(&p, &st.MTime, &st.Size); err != nil {
			return nil, err
		}
		out[p] = st
	}
	return out, rows.Err()
}

func (s *Store) ReplaceFile(ctx context.Context, path, lang string, mtime, size int64, symbols []Symbol, calls []Call) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM symbols WHERE file = ?", path); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM calls WHERE caller_file = ?", path); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO files(path, lang, mtime, size) VALUES(?,?,?,?) ON CONFLICT(path) DO UPDATE SET lang=excluded.lang, mtime=excluded.mtime, size=excluded.size", path, lang, mtime, size); err != nil {
		return err
	}
	for _, sym := range symbols {
		if _, err := tx.ExecContext(ctx, "INSERT INTO symbols(file, name, kind, signature, line) VALUES(?,?,?,?,?)",
			path, sym.Name, sym.Kind, sym.Signature, sym.Line); err != nil {
			return err
		}
	}
	for _, c := range calls {
		if _, err := tx.ExecContext(ctx, "INSERT INTO calls(caller_file, caller_symbol, callee_name, line) VALUES(?,?,?,?)",
			path, c.CallerSymbol, c.CalleeName, c.Line); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) RemoveFile(ctx context.Context, path string) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM symbols WHERE file = ?", path); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM calls WHERE caller_file = ?", path); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM files WHERE path = ?", path)
	return err
}

func (s *Store) FindSymbols(ctx context.Context, query string, limit int) ([]Symbol, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, file, name, kind, signature, line FROM symbols WHERE name = ? OR name LIKE ? ORDER BY (name = ?) DESC, file LIMIT ?",
		query, "%"+query+"%", query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSymbols(rows)
}

func (s *Store) FileSymbols(ctx context.Context, path string) ([]Symbol, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, file, name, kind, signature, line FROM symbols WHERE file = ? ORDER BY line", path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSymbols(rows)
}

func (s *Store) References(ctx context.Context, name string, limit int) ([]Call, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT caller_file, caller_symbol, callee_name, line FROM calls WHERE callee_name = ? LIMIT ?", name, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Call
	for rows.Next() {
		var c Call
		if err := rows.Scan(&c.CallerFile, &c.CallerSymbol, &c.CalleeName, &c.Line); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) FileMap(ctx context.Context) ([]struct {
	Path   string
	Lang   string
	Count  int
}, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT f.path, f.lang, COUNT(s.id) FROM files f LEFT JOIN symbols s ON s.file = f.path GROUP BY f.path ORDER BY f.path")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []struct {
		Path  string
		Lang  string
		Count int
	}
	for rows.Next() {
		var r struct {
			Path  string
			Lang  string
			Count int
		}
		if err := rows.Scan(&r.Path, &r.Lang, &r.Count); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) Stats(ctx context.Context) (files, symbols, calls int, err error) {
	if err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM files").Scan(&files); err != nil {
		return
	}
	if err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM symbols").Scan(&symbols); err != nil {
		return
	}
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM calls").Scan(&calls)
	return
}

func scanSymbols(rows *sql.Rows) ([]Symbol, error) {
	var out []Symbol
	for rows.Next() {
		var sym Symbol
		if err := rows.Scan(&sym.ID, &sym.File, &sym.Name, &sym.Kind, &sym.Signature, &sym.Line); err != nil {
			return nil, err
		}
		out = append(out, sym)
	}
	return out, rows.Err()
}

func DBPathFor(workspace string) string {
	abs, err := filepath.Abs(workspace)
	if err != nil {
		abs = workspace
	}
	home, _ := os.UserHomeDir()
	h := sha1Hex(abs)
	return filepath.Join(home, ".crv", "codegraph", h[:12]+".db")
}
