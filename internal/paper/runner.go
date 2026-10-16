package paper

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"tg-recs-audit/internal/contract"
	"tg-recs-audit/internal/infra/ledgerfile"
	"tg-recs-audit/internal/ledger"
)

var MSK = time.FixedZone("MSK", 3*3600)

// Today — сегодняшний день по Москве (полночь в зоне UTC — как дни операций и баров).
func Today() time.Time {
	t := time.Now().In(MSK)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// Runner — пересчёт стратегий, журналы, память, статусы.
type Runner struct {
	dir        string
	strategies []Strategy
	alert      Alerter
	mu         sync.Mutex
	status     map[string]Status
	running    sync.Mutex // ночной пересчёт и кнопка не идут одновременно
}

func NewRunner(dir string, ss []Strategy, alert Alerter) *Runner {
	return &Runner{dir: dir, strategies: ss, alert: alert, status: map[string]Status{}}
}

func (r *Runner) Strategies() []Strategy { return r.strategies }

func (r *Runner) Status(name string) Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status[name]
}

func (r *Runner) Journal(name string) ([]contract.Op, error) {
	return ledgerfile.File{Path: filepath.Join(r.dir, name+".jsonl")}.Load()
}

// RunAll пересчитывает все стратегии на asOf; один запуск за раз.
func (r *Runner) RunAll(asOf time.Time) {
	r.running.Lock()
	defer r.running.Unlock()
	r.mu.Lock()
	ss := r.strategies
	r.mu.Unlock()
	for _, s := range ss {
		st := r.runOne(s, asOf)
		r.mu.Lock()
		r.status[s.Name()] = st
		r.mu.Unlock()
		if st.Err != "" {
			r.alert.Alert(fmt.Sprintf("paperd %s: %s", s.Name(), st.Err))
		}
		log.Printf("paperd %s: +%d операций (всего %d) %s", s.Name(), st.Added, st.Total, st.Err)
	}
}

func (r *Runner) runOne(s Strategy, asOf time.Time) Status {
	st := Status{At: time.Now()}
	if prev := r.Status(s.Name()); prev.Stopped {
		st.Stopped, st.Err = true, prev.Err
		return st
	}
	mem, err := loadMemory(filepath.Join(r.dir, s.Name()+".memory.json"))
	if err != nil {
		st.Err = err.Error()
		return st
	}
	ops, err := s.Ops(asOf, mem)
	if err != nil {
		st.Err = "пересчёт: " + err.Error()
		return st
	}
	for i := range ops {
		ops[i].Seq = i + 1
	}
	journal := ledgerfile.File{Path: filepath.Join(r.dir, s.Name()+".jsonl")}
	old, err := journal.Load()
	if err != nil {
		st.Err = err.Error()
		return st
	}
	fresh, err := ledger.Reconcile(old, ops, asOf)
	if err != nil {
		st.Err, st.Stopped = err.Error(), true
		return st
	}
	if err := journal.Append(fresh); err != nil {
		st.Err = err.Error()
		return st
	}
	if err := mem.save(); err != nil {
		st.Err = err.Error()
		return st
	}
	st.Added, st.Total = len(fresh), len(old)+len(fresh)
	return st
}

// Schedule — пересчёт сразу и каждый день в hour:00 по Москве.
func (r *Runner) Schedule(hour int, stop <-chan struct{}) {
	r.RunAll(Today())
	for {
		now := time.Now().In(MSK)
		next := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, MSK)
		if !next.After(now) {
			next = next.AddDate(0, 0, 1)
		}
		select {
		case <-stop:
			return
		case <-time.After(time.Until(next)):
			r.RunAll(Today())
		}
	}
}

// --- память ---------------------------------------------------------------

type fileMemory struct {
	path string
	m    map[string]string
}

func loadMemory(path string) (*fileMemory, error) {
	fm := &fileMemory{path: path, m: map[string]string{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return fm, nil
	}
	if err != nil {
		return nil, err
	}
	return fm, json.Unmarshal(b, &fm.m)
}

func (f *fileMemory) Get(k string) (string, bool) { v, ok := f.m[k]; return v, ok }
func (f *fileMemory) Set(k, v string)             { f.m[k] = v }

func (f *fileMemory) Keys(prefix string) []string {
	var out []string
	for k := range f.m {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func (f *fileMemory) save() error {
	if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(f.m, "", " ")
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, f.path)
}
