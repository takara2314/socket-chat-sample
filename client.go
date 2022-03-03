// Copyright 2013 The Gorilla WebSocket Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write a message to the peer.
	// 信号を受けてから、読み込みきれるまでの制限時間
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 512
)

var (
	newline = []byte{'\n'}
	space   = []byte{' '}
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

// Client is a middleman between the websocket connection and the hub.
// クライアントは、WebSocket接続とハブの間の仲介者です。
type Client struct {
	// どのハブを使用しているかの情報を持っていたほうがいい
	hub *Hub

	// The websocket connection.
	conn *websocket.Conn

	// Buffered channel of outbound messages.
	// outbound: 〈飛行機・船が〉外国行きの. 2. 〈交通機関など〉市外に向かう.
	// このチャンネルにデータぶっこんだら、クライアントに文字が発信される
	send chan []byte
}

// readPump pumps messages from the websocket connection to the hub.
//
// The application runs readPump in a per-connection goroutine. The application
// ensures that there is at most one reader on a connection by executing all
// reads from this goroutine.

// readPumpは、WebSocket接続からハブにメッセージを送ります。
// アプリケーションは、接続ごとのゴルーチンでreadPumpを実行します。
// アプリケーションは、このゴルーチンからのすべての読み取りを実行することにより、
// 接続上に最大で1つのリーダーが存在することを確認します。
func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()
	// 読み込みサイズ上限
	c.conn.SetReadLimit(maxMessageSize)
	// 読み込み時間制限
	// SetReadDeadlineは、基盤となるネットワーク接続の読み取り期限を設定します。 読み取りがタイムアウトした後、WebSocket接続状態が破損し、それ以降のすべての読み取りでエラーが返されます。 tのゼロ値は、読み取りがタイムアウトしないことを意味します。
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	// Pingもらったら、Pongを返す
	// SetPongHandlerは、ピアから受信したpongメッセージのハンドラーを設定します。 hのappData引数は、PONGメッセージアプリケーションデータです。 デフォルトのpongハンドラーは何もしません。
	// ハンドラー関数は、NextReader、ReadMessage、およびメッセージリーダーのReadメソッドから呼び出されます。 上記の制御メッセージのセクションで説明されているように、アプリケーションは接続を読み取ってpongメッセージを処理する必要があります。
	c.conn.SetPongHandler(func(string) error { c.conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("error: %v", err)
			}
			break
		}
		// データを加工して、全員に発信
		// ある一人から受けたメッセージを、全員に向けるんだね！
		message = bytes.TrimSpace(bytes.Replace(message, newline, space, -1))
		c.hub.broadcast <- message
	}
}

// writePump pumps messages from the hub to the websocket connection.
//
// A goroutine running writePump is started for each connection. The
// application ensures that there is at most one writer to a connection by
// executing all writes from this goroutine.

// writePumpは、ハブからWebSocket接続にメッセージを送ります。
// writePumpを実行するゴルーチンが接続ごとに開始されます。
// アプリケーションは、このゴルーチンからのすべての書き込みを実行することにより、
// 接続への書き込みが最大で1つであることを確認します。
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// The hub closed the channel.
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			// Add queued chat messages to the current websocket message.
			// まだメッセージ残っていたら、改行してメッセージをはき続ける
			n := len(c.send)
			for i := 0; i < n; i++ {
				w.Write(newline)
				w.Write(<-c.send)
			}

			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// serveWs handles websocket requests from the peer.
func serveWs(hub *Hub, w http.ResponseWriter, r *http.Request) {
	// GETからWebSocketにアップグレード
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}
	// 新規接続クライアントを、WebSocketチャネルに新規登録
	// どのハブを経由しているかの情報を持っていたほうがいい
	client := &Client{hub: hub, conn: conn, send: make(chan []byte, 256)}
	// hub.register <- client でも良さそう
	// clientに紐づけたhubからもできるよ！ってことを誇張したかったのかな
	client.hub.register <- client

	// Allow collection of memory referenced by the caller by doing all work in
	// new goroutines.
	// 新しいgoroutineですべての作業を行うことにより、呼び出し元が参照するメモリの収集を許可します。
	// pump: ポンプ
	go client.writePump()
	go client.readPump()
}
