package upload

import (
	"context"
	"strings"
	"sync"
	"time"

	json "encoding/json/v2"

	"github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v3"
	"github.com/valyala/fasthttp"
)

const (
	wsPingInterval = 15 * time.Second
	wsReadTimeout  = 60 * time.Second
	wsWriteTimeout = 10 * time.Second
	wsReadLimit    = 4096
)

// The ticket, not the Origin header, is what ties a socket to its owner, and
// behind the proxy the Host the backend sees is not the browser's.
var inheritWSUpgrader = websocket.FastHTTPUpgrader{
	ReadBufferSize:  2048,
	WriteBufferSize: 4096,
	CheckOrigin:     func(*fasthttp.RequestCtx) bool { return true },
}

// wsClientMessage is a client-to-server frame: {"type":"confirm", password |
// vault_pin [, previous_password]}, {"type":"cancel"} or {"type":"pong"}.
type wsClientMessage struct {
	Type             string `json:"type"`
	Password         string `json:"password"`
	PreviousPassword string `json:"previous_password"`
	VaultPIN         string `json:"vault_pin"`
}

func (s *inheritService) handleWS(c fiber.Ctx) error {
	meta, p := captureMeta(c)
	if p != nil {
		return respondProblem(c, p)
	}
	jobID := strings.Clone(c.Params("job_id"))
	ticket := strings.Clone(c.Query("ticket"))
	if !websocket.FastHTTPIsWebSocketUpgrade(c.RequestCtx()) {
		return respondProblem(c, problem(fiber.StatusUpgradeRequired, "websocket_required", "需要 WebSocket 连接"))
	}
	if !s.redeemWSTicket(c.Context(), ticket, meta.UserID, jobID) {
		return respondProblem(c, errWSTicketInvalid)
	}
	if err := inheritWSUpgrader.Upgrade(c.RequestCtx(), func(conn *websocket.Conn) {
		s.serveWS(conn, meta, jobID)
	}); err != nil {
		// The upgrader has already written the error response.
		return nil
	}
	return nil
}

type wsConn struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (w *wsConn) writeRaw(msg []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	return w.conn.WriteMessage(websocket.TextMessage, msg)
}

func (w *wsConn) write(msg wsMessage) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return w.writeRaw(body)
}

func (w *wsConn) control(kind int, data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn.WriteControl(kind, data, time.Now().Add(wsWriteTimeout))
}

func (s *inheritService) serveWS(conn *websocket.Conn, meta requestMeta, jobID string) {
	defer conn.Close()
	ws := &wsConn{conn: conn}
	ctx := context.Background()
	job, rt, p := s.loadJob(ctx, meta.UserID, jobID)
	if p != nil {
		_ = ws.write(wsMessage{Type: "error", Code: p.Code, Message: p.Message})
		return
	}
	var events <-chan []byte
	if rt != nil {
		var unsubscribe func()
		events, unsubscribe = rt.subscribe()
		defer unsubscribe()
		rt.mu.Lock()
		job = rt.job.clone()
		rt.mu.Unlock()
	}
	if err := ws.write(wsMessage{Type: "snapshot", Job: s.view(job)}); err != nil {
		return
	}
	if rt == nil {
		// Finished (or not running here): the snapshot is all there is.
		_ = ws.control(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "job finished"))
		return
	}

	conn.SetReadLimit(wsReadLimit)
	_ = conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
	})
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			_, body, err := conn.ReadMessage()
			if err != nil {
				return
			}
			_ = conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
			var msg wsClientMessage
			if json.Unmarshal(body, &msg) != nil {
				_ = ws.write(wsMessage{Type: "error", Code: errInvalidRequest.Code, Message: errInvalidRequest.Message})
				continue
			}
			s.handleWSCommand(ws, meta, jobID, msg)
		}
	}()
	// The reader must be gone before serveWS returns: once the hijack
	// handler returns, fasthttp pools the connection's buffers, and a reader
	// still inside ReadMessage would share them with the next request.
	// Closing the connection ends a pending read at once.
	defer func() {
		_ = conn.Close()
		<-readerDone
	}()

	ticker := time.NewTicker(wsPingInterval)
	defer ticker.Stop()
	for {
		select {
		case msg, ok := <-events:
			if !ok {
				_ = ws.control(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "job finished"))
				return
			}
			if ws.writeRaw(msg) != nil {
				return
			}
		case <-ticker.C:
			now := time.Now().UTC()
			if ws.write(wsMessage{Type: "ping", At: &now}) != nil || ws.control(websocket.PingMessage, nil) != nil {
				return
			}
		case <-readerDone:
			return
		}
	}
}

func (s *inheritService) handleWSCommand(ws *wsConn, meta requestMeta, jobID string, msg wsClientMessage) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var view *jobView
	var p *apiProblem
	switch msg.Type {
	case "confirm":
		view, p = s.confirm(ctx, meta, jobID, unlockInput{Password: msg.Password, PreviousPassword: msg.PreviousPassword, VaultPIN: msg.VaultPIN})
	case "cancel":
		view, p = s.cancel(ctx, meta, jobID)
	case "pong", "ping":
		return
	default:
		p = errInvalidRequest
	}
	if p != nil {
		_ = ws.write(wsMessage{Type: "error", Action: msg.Type, Code: p.Code, Message: p.Message})
		return
	}
	_ = ws.write(wsMessage{Type: "ack", Action: msg.Type, Job: view})
}
