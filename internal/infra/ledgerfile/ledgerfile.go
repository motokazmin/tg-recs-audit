// Package ledgerfile — журнал операций в JSONL: строка на операцию, только дописывается.
// Файл коммитится: история счёта видна в git и не переписывается тихо.
package ledgerfile

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"tg-recs-audit/internal/contract"
)

type File struct{ Path string }

func (f File) Load() ([]contract.Op, error) {
	fh, err := os.Open(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	var out []contract.Op
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for n := 1; sc.Scan(); n++ {
		var op contract.Op
		if err := json.Unmarshal(sc.Bytes(), &op); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", f.Path, n, err)
		}
		out = append(out, op)
	}
	return out, sc.Err()
}

func (f File) Append(ops []contract.Op) error {
	if len(ops) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
		return err
	}
	fh, err := os.OpenFile(f.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(fh)
	for _, op := range ops {
		b, err := json.Marshal(op)
		if err != nil {
			fh.Close()
			return err
		}
		w.Write(b)
		w.WriteByte('\n')
	}
	if err := w.Flush(); err != nil {
		fh.Close()
		return err
	}
	return fh.Close()
}
