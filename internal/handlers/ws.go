package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"sharetext/internal/store"
)

type wsMsg struct {
	Content string `json:"content"`
}

func (a *API) WS(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	ok, err := a.Store.Exists(r.Context(), slug)
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer c.CloseNow()

	ch := a.Hub.Join(slug)
	defer a.Hub.Leave(slug, ch)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	if sess, err := a.Store.Get(ctx, slug); err == nil {
		init, _ := json.Marshal(toSessionResp(sess))
		_ = c.Write(ctx, websocket.MessageText, init)
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
				err := c.Write(wctx, websocket.MessageText, msg)
				wcancel()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()

	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		var m wsMsg
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		sess, err := a.Store.Update(ctx, slug, m.Content)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrExpired) {
				return
			}
			continue
		}
		out, _ := json.Marshal(toSessionResp(sess))
		a.Hub.Broadcast(slug, out, ch)
	}
}
