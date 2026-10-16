// Package telegram — тревоги в Telegram, как в боте (TG_BOT_TOKEN, TG_CHAT_ID из
// /etc/trading-bot/env). Без них — только лог: локальный запуск не должен падать.
package telegram

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"
)

type Alerter struct {
	token, chat, host string
	http              *http.Client
}

func FromEnv() *Alerter {
	host, _ := os.Hostname()
	a := &Alerter{token: os.Getenv("TG_BOT_TOKEN"), chat: os.Getenv("TG_CHAT_ID"), host: host, http: &http.Client{Timeout: 20 * time.Second}}
	if a.token == "" || a.chat == "" {
		log.Printf("Telegram не настроен (TG_BOT_TOKEN, TG_CHAT_ID) — тревоги только в лог")
	}
	return a
}

func (a *Alerter) Alert(msg string) {
	log.Printf("ТРЕВОГА: %s", msg)
	if a.token == "" || a.chat == "" {
		return
	}
	go func() {
		resp, err := a.http.PostForm("https://api.telegram.org/bot"+a.token+"/sendMessage",
			url.Values{"chat_id": {a.chat}, "text": {fmt.Sprintf("[%s] %s", a.host, msg)}})
		if err != nil {
			log.Printf("Telegram: тревога не отправлена: %v", err)
			return
		}
		resp.Body.Close()
	}()
}
