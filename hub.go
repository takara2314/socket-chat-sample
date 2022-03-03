// Copyright 2013 The Gorilla WebSocket Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

// Hub maintains the set of active clients and broadcasts messages to the
// clients.
// ハブはアクティブなクライアントのセットを維持し、クライアントにメッセージをブロードキャストします。
type Hub struct {
	// Registered clients.
	clients map[*Client]bool

	// Inbound messages from the clients.
	broadcast chan []byte

	// Register requests from the clients.
	register chan *Client

	// Unregister requests from clients.
	unregister chan *Client
}

func newHub() *Hub {
	return &Hub{
		// ここにメッセージぶっこめば、全員にメッセージが送信される
		broadcast: make(chan []byte),
		// ここにクライアントをぶち込めば、それを登録する
		register: make(chan *Client),
		// ここにクライアントをぶち込めば、それを削除する
		unregister: make(chan *Client),
		// 登録されている、クライアント一覧
		clients: make(map[*Client]bool),
	}
}

func (h *Hub) run() {
	for {
		select {
		case client := <-h.register:
			h.clients[client] = true
		// そのクライアントのコネクション情報を削除し、sendチャネルを切断
		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
		case message := <-h.broadcast:
			for client := range h.clients {
				select {
				// ここでメッセージ送れんかったら、速やかにコネクションを切断
				// 送れたら切断せんけど
				case client.send <- message:
				default:
					close(client.send)
					delete(h.clients, client)
				}
			}
		}
	}
}
