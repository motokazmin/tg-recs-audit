package tgexport

import (
	"fmt"
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

// HTMLSource — экспорт Telegram Desktop в HTML: каталог с messages.html, messages2.html, …
// Так выгружается канал, если в настройках экспорта выбран HTML (по умолчанию).
// Время правки HTML не содержит.
type HTMLSource struct{ Dir string }

var pageNum = regexp.MustCompile(`^messages(\d*)\.html$`)

func (s HTMLSource) Messages() (contract.ChannelInfo, []contract.Message, error) {
	info := contract.ChannelInfo{Ref: s.Dir, Source: "export-html"}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return info, nil, err
	}
	type page struct {
		n    int
		path string
	}
	var pages []page
	for _, e := range entries {
		if m := pageNum.FindStringSubmatch(e.Name()); m != nil {
			n := 1
			if m[1] != "" {
				n, _ = strconv.Atoi(m[1])
			}
			pages = append(pages, page{n, filepath.Join(s.Dir, e.Name())})
		}
	}
	if len(pages) == 0 {
		return info, nil, fmt.Errorf("%s: нет messages*.html — это не HTML-экспорт Telegram", s.Dir)
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].n < pages[j].n })

	var out []contract.Message
	for _, p := range pages {
		f, err := os.Open(p.path)
		if err != nil {
			return info, nil, err
		}
		doc, err := html.Parse(f)
		f.Close()
		if err != nil {
			return info, nil, fmt.Errorf("%s: %w", p.path, err)
		}
		title, msgs, err := parseHTMLPage(doc)
		if err != nil {
			return info, nil, fmt.Errorf("%s: %w", p.path, err)
		}
		if info.Title == "" {
			info.Title = title
		}
		out = append(out, msgs...)
	}
	for i := range out {
		out[i].Channel = info.Title
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return info, out, nil
}

var msgID = regexp.MustCompile(`^message(\d+)$`)

// Время в title: «14.02.2023 08:55:35 UTC+01:00» — с зоной машины, где делали экспорт.
const htmlLayout = "02.01.2006 15:04:05 UTC-07:00"

func parseHTMLPage(doc *html.Node) (string, []contract.Message, error) {
	var title string
	var out []contract.Message
	var err error
	walkHTML(doc, func(n *html.Node) bool {
		if hasClassHTML(n, "page_header") && title == "" {
			walkHTML(n, func(c *html.Node) bool {
				if hasClassHTML(c, "text") && title == "" {
					title = strings.TrimSpace(textHTML(c))
				}
				return true
			})
		}
		m := msgID.FindStringSubmatch(attrHTML(n, "id"))
		if m == nil || !hasClassHTML(n, "default") {
			return true // служебные (service) — не посты
		}
		id, _ := strconv.ParseInt(m[1], 10, 64)
		msg := contract.Message{ID: id, IDSpan: 1}
		walkHTML(n, func(c *html.Node) bool {
			switch {
			case hasClassHTML(c, "date") && hasClassHTML(c, "details") && msg.PostedAt.IsZero():
				t, e := time.Parse(htmlLayout, attrHTML(c, "title"))
				if e != nil {
					err = fmt.Errorf("пост %d: время %q: %w", id, attrHTML(c, "title"), e)
				}
				msg.PostedAt = t.UTC()
			case hasClassHTML(c, "text") && msg.Text == "":
				msg.Text = strings.TrimSpace(textHTML(c))
				walkHTML(c, func(a *html.Node) bool {
					if a.Data != "a" {
						return true
					}
					t := textHTML(a)
					if strings.HasPrefix(t, "#") || strings.HasPrefix(t, "$") {
						msg.Tags = append(msg.Tags, strings.TrimLeft(t, "#$"))
					} else if href := attrHTML(a, "href"); strings.HasPrefix(href, "http") {
						msg.Links = append(msg.Links, href)
					}
					return false
				})
				return false
			}
			return true
		})
		out = append(out, msg)
		return false
	})
	return title, out, err
}

func walkHTML(n *html.Node, f func(*html.Node) bool) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && !f(c) {
			continue
		}
		walkHTML(c, f)
	}
}

func textHTML(n *html.Node) string {
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

func attrHTML(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func hasClassHTML(n *html.Node, class string) bool {
	for _, c := range strings.Fields(attrHTML(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
}
