// Package tgweb — история публичного канала через веб-превью t.me/s/<канал>.
//
// Работает без аккаунта для любого канала с публичным именем. Ограничения:
//   - видно только факт правки поста («edited»), время правки — нет;
//   - удалённые посты не видны вовсе, только дыры в нумерации ID;
//   - закрытые каналы (без @имени) так не прочитать — нужен экспорт или MTProto.
package tgweb

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"tg-recs-audit/internal/contract"
)

type Source struct {
	Channel  string // имя без @ и t.me/
	CacheDir string
	Refresh  bool // перекачать всё, а не только новые посты
	Offline  bool // только кэш, без сети
	Pause    time.Duration
	http     *http.Client
}

func New(channel, cacheDir string, refresh bool) *Source {
	return &Source{
		Channel: channel, CacheDir: cacheDir, Refresh: refresh,
		Pause: 700 * time.Millisecond, http: &http.Client{Timeout: 30 * time.Second},
	}
}

type cached struct {
	Title    string             `json:"title"`
	Messages []contract.Message `json:"messages"`
}

func (s *Source) Messages() (contract.ChannelInfo, []contract.Message, error) {
	info := contract.ChannelInfo{Ref: "t.me/" + s.Channel, Source: "web"}
	path := filepath.Join(s.CacheDir, s.Channel+".json")

	var c cached
	if !s.Refresh {
		if b, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(b, &c)
		}
	}
	known := int64(0)
	for _, m := range c.Messages {
		known = max(known, m.ID)
	}

	if s.Offline {
		if len(c.Messages) == 0 {
			return info, nil, fmt.Errorf("%s: кэша канала нет, а -offline запрещает сеть", path)
		}
		info.Title = c.Title
		return info, c.Messages, nil
	}

	byID := map[int64]contract.Message{}
	for _, m := range c.Messages {
		byID[m.ID] = m
	}
	// Идём от свежих постов к старым, пока не упрёмся в уже скачанные или в начало канала.
	before := int64(0)
	for pages := 0; ; pages++ {
		title, msgs, err := s.page(before)
		if err != nil {
			return info, nil, err
		}
		if title != "" {
			c.Title = title
		}
		if len(msgs) == 0 {
			break
		}
		lowest := msgs[0].ID
		for _, m := range msgs {
			byID[m.ID] = m
			lowest = min(lowest, m.ID)
		}
		if pages%20 == 19 {
			log.Printf("tgweb %s: дошли до поста %d", s.Channel, lowest)
		}
		if lowest <= known || (before != 0 && lowest >= before) || lowest <= 1 {
			break
		}
		before = lowest
		time.Sleep(s.Pause)
	}

	out := make([]contract.Message, 0, len(byID))
	for _, m := range byID {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	c.Messages = out
	if err := os.MkdirAll(s.CacheDir, 0o755); err != nil {
		return info, nil, err
	}
	b, _ := json.Marshal(c)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return info, nil, err
	}
	info.Title = c.Title
	return info, out, nil
}

// page — одна страница превью (до ~20 постов). before=0 — самые свежие.
func (s *Source) page(before int64) (string, []contract.Message, error) {
	u := "https://t.me/s/" + s.Channel
	if before > 0 {
		u += "?before=" + strconv.FormatInt(before, 10)
	}
	var body []byte
	for attempt := 0; ; attempt++ {
		req, _ := http.NewRequest(http.MethodGet, u, nil)
		req.Header.Set("User-Agent", "Mozilla/5.0")
		resp, err := s.http.Do(req)
		if err == nil {
			body, err = io.ReadAll(resp.Body)
			resp.Body.Close()
			if err == nil && resp.StatusCode == http.StatusTooManyRequests {
				err = fmt.Errorf("HTTP 429")
			} else if err == nil && resp.StatusCode != http.StatusOK {
				return "", nil, fmt.Errorf("%s: HTTP %d", u, resp.StatusCode)
			}
		}
		if err == nil {
			break
		}
		if attempt == 4 {
			return "", nil, fmt.Errorf("%s: %w", u, err)
		}
		time.Sleep(time.Duration(5<<attempt) * time.Second)
	}
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return "", nil, err
	}
	title, msgs := parsePage(doc, s.Channel)
	if before == 0 && len(msgs) == 0 {
		return "", nil, fmt.Errorf("%s: постов нет — канал закрытый, без веб-превью или имя неверное", u)
	}
	return title, msgs, nil
}

var singleRe = regexp.MustCompile(`/\d+\?single$`)

func parsePage(doc *html.Node, channel string) (string, []contract.Message) {
	var title string
	var out []contract.Message
	walk(doc, func(n *html.Node) bool {
		if hasClass(n, "tgme_channel_info_header_title") && title == "" {
			title = strings.TrimSpace(textOf(n))
		}
		post := attr(n, "data-post")
		if post == "" || !hasClass(n, "tgme_widget_message") {
			return true
		}
		_, idStr, ok := strings.Cut(post, "/")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if !ok || err != nil {
			return false
		}
		m := contract.Message{Channel: channel, ID: id, IDSpan: 1}
		singles := 0
		walk(n, func(c *html.Node) bool {
			switch {
			case c.Data == "time" && m.PostedAt.IsZero():
				if t, err := time.Parse(time.RFC3339, attr(c, "datetime")); err == nil {
					m.PostedAt = t
				}
			case hasClass(c, "tgme_widget_message_text") && m.Text == "":
				m.Text = strings.TrimSpace(textOf(c))
				walk(c, func(a *html.Node) bool {
					if a.Data != "a" {
						return true
					}
					if href := attr(a, "href"); strings.HasPrefix(href, "http") {
						m.Links = append(m.Links, href)
					}
					if t := textOf(a); strings.HasPrefix(t, "$") || strings.HasPrefix(t, "#") {
						m.Tags = append(m.Tags, strings.TrimLeft(t, "$#"))
					}
					return false
				})
				return false
			case hasClass(c, "tgme_widget_message_meta") && strings.Contains(textOf(c), "edited"):
				m.Edited = true
			case c.Data == "a" && singleRe.MatchString(attr(c, "href")):
				singles++
			}
			return true
		})
		if singles > 1 {
			m.IDSpan = singles
		}
		if !m.PostedAt.IsZero() {
			out = append(out, m)
		}
		return false
	})
	return title, out
}

// walk обходит дерево; f возвращает false, чтобы не спускаться в детей узла.
func walk(n *html.Node, f func(*html.Node) bool) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && !f(c) {
			continue
		}
		walk(c, f)
	}
}

func textOf(n *html.Node) string {
	var sb strings.Builder
	var rec func(*html.Node)
	rec = func(n *html.Node) {
		switch {
		case n.Type == html.TextNode:
			sb.WriteString(n.Data)
		case n.Type == html.ElementNode && n.Data == "br":
			sb.WriteByte('\n')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			rec(c)
		}
	}
	rec(n)
	return sb.String()
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func hasClass(n *html.Node, class string) bool {
	for _, c := range strings.Fields(attr(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
}
