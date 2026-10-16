// Package tgexport читает экспорт Telegram Desktop (JSON, result.json).
package tgexport

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tg-recs-audit/internal/contract"
)

type export struct {
	Name     string `json:"name"`
	Messages []struct {
		ID     int64           `json:"id"`
		Type   string          `json:"type"`
		Date   string          `json:"date"`
		Edited string          `json:"edited"`
		Text   json.RawMessage `json:"text"`
	} `json:"messages"`
}

// Экспорт пишет локальное время машины, на которой выгружали, без зоны.
const layout = "2006-01-02T15:04:05"

// Source — экспорт Telegram Desktop как источник канала.
type Source struct{ Path string }

func (s Source) Messages() (contract.ChannelInfo, []contract.Message, error) {
	msgs, title, err := read(s.Path)
	info := contract.ChannelInfo{Ref: s.Path, Title: title, Source: "export", EditTimes: true}
	return info, msgs, err
}

func read(path string) ([]contract.Message, string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	var e export
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, "", fmt.Errorf("%s: %w", path, err)
	}
	channel := e.Name
	if channel == "" {
		channel = filepath.Base(filepath.Dir(path))
	}
	out := make([]contract.Message, 0, len(e.Messages))
	for _, m := range e.Messages {
		if m.Type != "message" {
			continue
		}
		posted, err := time.ParseInLocation(layout, m.Date, time.Local)
		if err != nil {
			return nil, "", fmt.Errorf("%s: пост %d: %w", path, m.ID, err)
		}
		msg := contract.Message{Channel: channel, ID: m.ID, PostedAt: posted, IDSpan: 1}
		if m.Edited != "" {
			msg.Edited = true
			if t, err := time.ParseInLocation(layout, m.Edited, time.Local); err == nil {
				msg.EditedAt = &t
			}
		}
		msg.Text, msg.Tags, msg.Links = flatten(m.Text)
		out = append(out, msg)
	}
	return out, channel, nil
}

// flatten: text в экспорте — строка или список из строк и сущностей {"type","text"}.
func flatten(raw json.RawMessage) (string, []string, []string) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, nil, nil
	}
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) != nil {
		return "", nil, nil
	}
	var sb strings.Builder
	var tags, links []string
	for _, p := range parts {
		if json.Unmarshal(p, &s) == nil {
			sb.WriteString(s)
			continue
		}
		var ent struct {
			Type string `json:"type"`
			Text string `json:"text"`
			Href string `json:"href"`
		}
		if json.Unmarshal(p, &ent) != nil {
			continue
		}
		sb.WriteString(ent.Text)
		if ent.Type == "cashtag" || ent.Type == "hashtag" {
			tags = append(tags, strings.TrimLeft(ent.Text, "$#"))
		}
		switch {
		case ent.Href != "":
			links = append(links, ent.Href)
		case ent.Type == "link":
			links = append(links, ent.Text)
		}
	}
	return sb.String(), tags, links
}
