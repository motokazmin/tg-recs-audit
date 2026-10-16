package paper

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"net"
	"net/http"
	"strings"
	"time"

	"tg-recs-audit/internal/contract"
)

// Server — админка: сводка стратегий, страница стратегии, пересчёт по кнопке.
type Server struct {
	runner *Runner
	bench  Bench
	token  string
}

func NewServer(r *Runner, b Bench, token string) *Server {
	return &Server{runner: r, bench: b, token: token}
}

// ValidateListen — публичный bind без токена запрещён (как в боте).
func ValidateListen(listen, token string) error {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return fmt.Errorf("адрес %q: %w", listen, err)
	}
	if ip := net.ParseIP(host); host == "localhost" || (ip != nil && ip.IsLoopback()) {
		return nil
	}
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("ADMIN_TOKEN обязателен при %q (не localhost)", listen)
	}
	return nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.auth(s.overview))
	mux.HandleFunc("GET /s/{name}", s.auth(s.strategy))
	mux.HandleFunc("GET /api/status", s.auth(s.apiStatus))
	mux.HandleFunc("POST /run", s.auth(s.run))
	return mux
}

const cookieName = "paperd_token"

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.token == "" {
			next(w, r)
			return
		}
		got := ""
		if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
			got = strings.TrimSpace(strings.TrimPrefix(a, "Bearer "))
		} else if q := r.URL.Query().Get("access_token"); q != "" {
			got = q
		} else if c, err := r.Cookie(cookieName); err == nil {
			got = c.Value
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `<!doctype html><meta charset="utf-8"><title>paperd</title><form method="get" style="font:16px sans-serif;margin:3em">ADMIN_TOKEN: <input name="access_token" type="password" autofocus> <button>Войти</button></form>`)
			return
		}
		if r.URL.Query().Get("access_token") != "" {
			http.SetCookie(w, &http.Cookie{Name: cookieName, Value: got, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 30 * 86400})
		}
		next(w, r)
	}
}

type row struct {
	Name, Title, Bench, Start string
	Equity, PnL, BenchPnL     float64
	Sum                       Summary
	Status                    Status
}

func (s *Server) rows() []row {
	var out []row
	asOf := Today()
	for _, st := range s.runner.Strategies() {
		r := row{Name: st.Name(), Title: st.Title(), Bench: st.Benchmark(), Start: st.Start().Format(time.DateOnly), Status: s.runner.Status(st.Name())}
		ops, _ := s.runner.Journal(st.Name())
		if bars, err := s.bench.Bars(st.Benchmark()); err == nil {
			if c := Curve(st, ops, bars, asOf); len(c) > 0 {
				last := c[len(c)-1]
				r.Equity, r.PnL, r.BenchPnL = last.Equity(), last.Equity()-st.Capital(), last.Bench-st.Capital()
			} else {
				r.Equity = st.Capital()
			}
			r.Sum = Summarize(Trades(st, ops, bars, asOf))
		}
		out = append(out, r)
	}
	return out
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	render(w, overviewTpl, map[string]any{"Rows": s.rows(), "Now": time.Now().In(MSK).Format("2006-01-02 15:04")})
}

func (s *Server) apiStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.rows())
}

func (s *Server) run(w http.ResponseWriter, r *http.Request) {
	go s.runner.RunAll(Today())
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) strategy(w http.ResponseWriter, r *http.Request) {
	var st Strategy
	for _, x := range s.runner.Strategies() {
		if x.Name() == r.PathValue("name") {
			st = x
		}
	}
	if st == nil {
		http.NotFound(w, r)
		return
	}
	asOf := Today()
	ops, err := s.runner.Journal(st.Name())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	bars, err := s.bench.Bars(st.Benchmark())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	curve := Curve(st, ops, bars, asOf)
	trades := Trades(st, ops, bars, asOf)
	var open, closed []Trade
	for _, t := range trades {
		if t.Close.IsZero() {
			open = append(open, t)
		} else {
			closed = append(closed, t)
		}
	}
	reverse(closed)
	tail := ops
	if len(tail) > 300 {
		tail = tail[len(tail)-300:]
	}
	tail = append([]contract.Op(nil), tail...)
	for i, j := 0, len(tail)-1; i < j; i, j = i+1, j-1 {
		tail[i], tail[j] = tail[j], tail[i]
	}
	render(w, strategyTpl, map[string]any{
		"S": st, "Status": s.runner.Status(st.Name()), "Sum": Summarize(trades), "Chart": chart(curve),
		"Open": open, "Closed": closed, "Ops": tail,
	})
}

func reverse(ts []Trade) {
	for i, j := 0, len(ts)-1; i < j; i, j = i+1, j-1 {
		ts[i], ts[j] = ts[j], ts[i]
	}
}

// chart — SVG: капитал счёта и тот же капитал в бенчмарке.
func chart(c []Point) template.HTML {
	if len(c) < 2 {
		return template.HTML(`<p class="muted">Кривая появится после второго торгового дня.</p>`)
	}
	const w, h = 900.0, 260.0
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, p := range c {
		lo, hi = math.Min(lo, math.Min(p.Equity(), p.Bench)), math.Max(hi, math.Max(p.Equity(), p.Bench))
	}
	if hi-lo < 1 {
		hi, lo = hi+1, lo-1
	}
	pts := func(f func(Point) float64) string {
		var sb strings.Builder
		for i, p := range c {
			x := float64(i) / float64(len(c)-1) * w
			y := h - (f(p)-lo)/(hi-lo)*h
			fmt.Fprintf(&sb, "%.1f,%.1f ", x, y)
		}
		return sb.String()
	}
	return template.HTML(fmt.Sprintf(`<svg viewBox="0 -10 %.0f %.0f" class="chart" preserveAspectRatio="none">
<polyline fill="none" stroke="#999" stroke-width="1.5" stroke-dasharray="4 3" points="%s"/>
<polyline fill="none" stroke="#2563eb" stroke-width="2" points="%s"/></svg>
<div class="muted">синяя — счёт, пунктир — тот же стартовый капитал в бенчмарке · %s … %s · %.0f … %.0f ₽</div>`,
		w, h+20, pts(func(p Point) float64 { return p.Bench }), pts(func(p Point) float64 { return p.Equity() }),
		c[0].Day.Format(time.DateOnly), c[len(c)-1].Day.Format(time.DateOnly), lo, hi))
}

func render(w http.ResponseWriter, t *template.Template, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.Execute(w, data); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

var funcs = template.FuncMap{
	"rub": func(x float64) string { return fmtRub(x) },
	"pct": func(x float64) string {
		if math.IsNaN(x) {
			return "—"
		}
		return fmt.Sprintf("%+.2f%%", 100*x)
	},
	"share": func(x float64) string { return fmt.Sprintf("%.0f%%", 100*x) },
	"date": func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.Format(time.DateOnly)
	},
	"when": func(t time.Time) string {
		if t.IsZero() {
			return "ещё не было"
		}
		return t.In(MSK).Format("2006-01-02 15:04")
	},
	"cls": func(x float64) string {
		if x > 0 {
			return "pos"
		}
		if x < 0 {
			return "neg"
		}
		return ""
	},
}

func fmtRub(x float64) string {
	s := fmt.Sprintf("%.0f", math.Abs(x))
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(c)
	}
	if x < 0 {
		return "−" + b.String()
	}
	return b.String()
}

const css = `<style>
:root{--fg:#111;--bg:#fff;--muted:#666;--line:#e5e7eb;--pos:#15803d;--neg:#b91c1c}
@media (prefers-color-scheme:dark){:root{--fg:#e5e7eb;--bg:#111827;--muted:#9ca3af;--line:#374151;--pos:#4ade80;--neg:#f87171}}
body{font:14px/1.45 system-ui,sans-serif;color:var(--fg);background:var(--bg);margin:0 auto;max-width:1100px;padding:16px}
table{border-collapse:collapse;width:100%;margin:8px 0 24px}th,td{border-bottom:1px solid var(--line);padding:6px 8px;text-align:left}
td.n,th.n{text-align:right;font-variant-numeric:tabular-nums}.pos{color:var(--pos)}.neg{color:var(--neg)}.muted{color:var(--muted)}
.chart{width:100%;height:280px}a{color:#2563eb}.err{color:var(--neg);white-space:pre-wrap}
.wrap{overflow-x:auto}button{font:inherit;padding:6px 14px}
</style>`

var overviewTpl = template.Must(template.New("o").Funcs(funcs).Parse(`<!doctype html><html lang="ru"><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><title>paperd</title>` + css + `
<h1>Проверка идей вперёд</h1>
<p class="muted">Виртуальные деньги, регистрация — docs/analysis/0013. Пересчёт ежедневно в 02:00 МСК. Сейчас {{.Now}}.</p>
<div class="wrap"><table><tr><th>Стратегия</th><th>Старт</th><th class="n">Капитал</th><th class="n">P&L</th><th class="n">Бенчмарк, P&L</th>
<th class="n">Закрыто</th><th class="n">Превыш. ср.</th><th class="n">Медиана</th><th class="n">Открыто</th><th>Пересчёт</th></tr>
{{range .Rows}}<tr><td><a href="/s/{{.Name}}">{{.Title}}</a></td><td>{{.Start}}</td><td class="n">{{rub .Equity}}</td>
<td class="n {{cls .PnL}}">{{rub .PnL}}</td><td class="n {{cls .BenchPnL}}">{{.Bench}}: {{rub .BenchPnL}}</td>
<td class="n">{{.Sum.Closed}}</td><td class="n {{cls .Sum.Mean}}">{{pct .Sum.Mean}}</td><td class="n">{{pct .Sum.Median}}</td><td class="n">{{.Sum.Open}}</td>
<td>{{when .Status.At}} {{if .Status.Err}}<div class="err">{{if .Status.Stopped}}ОСТАНОВЛЕНА: {{end}}{{.Status.Err}}</div>{{else if not .Status.At.IsZero}}<span class="muted">+{{.Status.Added}}</span>{{end}}</td></tr>{{end}}
</table></div>
<form method="post" action="/run"><button>Пересчитать сейчас</button></form>
</html>`))

var strategyTpl = template.Must(template.New("s").Funcs(funcs).Parse(`<!doctype html><html lang="ru"><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.S.Name}} · paperd</title>` + css + `
<p><a href="/">← все стратегии</a></p><h1>{{.S.Title}}</h1>
<p class="muted">Старт {{date .S.Start}} · капитал {{rub .S.Capital}} ₽ · бенчмарк {{.S.Benchmark}} · пересчёт {{when .Status.At}}</p>
{{if .Status.Err}}<p class="err">{{if .Status.Stopped}}ОСТАНОВЛЕНА: {{end}}{{.Status.Err}}</p>{{end}}
{{.Chart}}
<p>Закрыто {{.Sum.Closed}} · превышение над {{.S.Benchmark}}: среднее <b class="{{cls .Sum.Mean}}">{{pct .Sum.Mean}}</b>, медиана {{pct .Sum.Median}}, в плюсе {{share .Sum.Positive}} · средняя доходность сделки {{pct .Sum.MeanRet}}</p>
<h2>Открытые позиции ({{len .Open}})</h2><div class="wrap"><table><tr><th>Бумага</th><th>Вход</th><th class="n">Штук</th><th class="n">Вложено</th><th class="n">Доходность</th><th class="n">{{.S.Benchmark}}</th><th class="n">Превыш.</th></tr>
{{range .Open}}<tr><td>{{.SecID}}</td><td>{{date .Open}}</td><td class="n">{{.Qty}}</td><td class="n">{{rub .Cost}}</td><td class="n {{cls .Return}}">{{pct .Return}}</td><td class="n">{{pct .BenchRet}}</td><td class="n {{cls .Excess}}">{{pct .Excess}}</td></tr>{{end}}</table></div>
<h2>Закрытые сделки ({{len .Closed}})</h2><div class="wrap"><table><tr><th>Бумага</th><th>Вход</th><th>Выход</th><th class="n">Вложено</th><th class="n">Доходность</th><th class="n">{{.S.Benchmark}}</th><th class="n">Превыш.</th></tr>
{{range .Closed}}<tr><td>{{.SecID}}</td><td>{{date .Open}}</td><td>{{date .Close}}</td><td class="n">{{rub .Cost}}</td><td class="n {{cls .Return}}">{{pct .Return}}</td><td class="n">{{pct .BenchRet}}</td><td class="n {{cls .Excess}}">{{pct .Excess}}</td></tr>{{end}}</table></div>
<h2>Журнал (последние операции)</h2><div class="wrap"><table><tr><th>#</th><th>День</th><th>Операция</th><th>Бумага</th><th class="n">Штук</th><th class="n">Цена</th><th class="n">Сумма</th><th>Пост</th><th>Комментарий</th></tr>
{{range .Ops}}<tr><td>{{.Seq}}</td><td>{{.Day}}</td><td>{{.Kind}}</td><td>{{.SecID}}</td><td class="n">{{if .Qty}}{{.Qty}}{{end}}</td><td class="n">{{if .Price}}{{.Price}}{{end}}</td><td class="n {{cls .Amount}}">{{if .Amount}}{{rub .Amount}}{{end}}</td><td>{{if .PostID}}{{.PostID}}{{end}}</td><td class="muted">{{.Reason}}</td></tr>{{end}}</table></div>
</html>`))
