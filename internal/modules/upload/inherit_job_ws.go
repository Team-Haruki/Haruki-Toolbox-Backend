package upload

import (
	"context"
	"strings"
	"sync"
	"time"

	json "encoding/json/v2"

	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
	"github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v3"
	"github.com/valyala/fasthttp"
)

const (
	wsPingInterval   = 15 * time.Second
	wsUpgradeTimeout = 10 * time.Second
	wsReadTimeout    = 60 * time.Second
	wsWriteTimeout   = 10 * time.Second
	wsReadLimit      = 4096
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

type inheritWSSlots struct {
	mu     sync.Mutex
	active int
	owners map[string]int
	jobs   map[string]int
}

func (slots *inheritWSSlots) reserve(owner, job string) func() {
	slots.mu.Lock()
	defer slots.mu.Unlock()
	if slots.active >= 128 || slots.owners[owner] >= 4 || slots.jobs[job] >= 2 {
		return nil
	}
	if slots.owners == nil {
		slots.owners = make(map[string]int)
		slots.jobs = make(map[string]int)
	}
	slots.active++
	slots.owners[owner]++
	slots.jobs[job]++
	return func() {
		slots.mu.Lock()
		defer slots.mu.Unlock()
		slots.active--
		slots.owners[owner]--
		slots.jobs[job]--
		if slots.owners[owner] == 0 {
			delete(slots.owners, owner)
		}
		if slots.jobs[job] == 0 {
			delete(slots.jobs, job)
		}
	}
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
	release := s.wsSlots.reserve(meta.UserID, jobID)
	if release == nil {
		return respondProblem(c, problem(fiber.StatusTooManyRequests, "websocket_limit", "当前连接数已达到上限"))
	}
	identityID, _ := c.Locals("identityID").(string)
	sessionID, _ := c.Locals("kratosSessionID").(string)
	identityID, sessionID = strings.Clone(identityID), strings.Clone(sessionID)
	sessionActive := func() bool {
		ctx, cancel := context.WithTimeout(meta.context(context.Background()), 3*time.Second)
		defer cancel()
		return s.wsSessionActive(ctx, meta.UserID, identityID, sessionID)
	}
	if !sessionActive() {
		release()
		return respondProblem(c, errUnauthenticated)
	}
	// fasthttp skips the hijack callback if it cannot send the 101 response.
	// The timer and the callback compete for the pending reservation.
	pending := time.AfterFunc(wsUpgradeTimeout, release)
	if err := inheritWSUpgrader.Upgrade(c.RequestCtx(), func(conn *websocket.Conn) {
		if !pending.Stop() {
			_ = conn.Close()
			return
		}
		defer release()
		s.serveWS(conn, meta, jobID, sessionActive)
	}); err != nil {
		if pending.Stop() {
			release()
		}
		// The upgrader has already written the error response.
		return nil
	}
	return nil
}

func (s *inheritService) checkWSBrowserSession(ctx context.Context, userID, identityID, sessionID string) bool {
	if identityID == "" || sessionID == "" {
		return false
	}
	activeUser, err := s.api.DBManager.DB.User.Query().
		Where(userSchema.IDEQ(userID), userSchema.KratosIdentityIDEQ(identityID), userSchema.BannedEQ(false)).
		Exist(ctx)
	if err != nil || !activeUser {
		return false
	}
	return s.api.SessionHandler.IsKratosSessionActive(ctx, identityID, sessionID)
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

func (s *inheritService) serveWS(conn *websocket.Conn, meta requestMeta, jobID string, sessionActive func() bool) {
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
	if !sessionActive() || ws.write(wsMessage{Type: "snapshot", Job: s.view(job)}) != nil {
		return
	}
	if rt == nil {
		// Finished (or not running here): the snapshot is all there is.
		_ = ws.control(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "job finished"))
		return
	}

	conn.SetReadLimit(wsReadLimit)
	var readDeadlineMu sync.Mutex
	stoppingReader := false
	refreshReadDeadline := func() error {
		readDeadlineMu.Lock()
		defer readDeadlineMu.Unlock()
		if stoppingReader {
			return nil
		}
		return conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
	}
	_ = refreshReadDeadline()
	conn.SetPongHandler(func(string) error { return refreshReadDeadline() })
	readerDone := make(chan struct{})
	// commandMu is held while a client command is handled, so the closing
	// frame of a finished job never overtakes the reply to the command that
	// finished it (a cancel's ack).
	var commandMu sync.Mutex
	go func() {
		defer close(readerDone)
		for {
			_, body, err := conn.ReadMessage()
			if err != nil {
				return
			}
			_ = refreshReadDeadline()
			var msg wsClientMessage
			if json.Unmarshal(body, &msg) != nil {
				_ = ws.write(wsMessage{Type: "error", Code: errInvalidRequest.Code, Message: errInvalidRequest.Message})
				continue
			}
			commandMu.Lock()
			if !sessionActive() {
				commandMu.Unlock()
				return
			}
			s.handleWSCommand(ws, meta, jobID, msg, sessionActive)
			commandMu.Unlock()
		}
	}()
	// The reader must be gone before serveWS returns: once the hijack
	// handler returns, fasthttp pools the connection's buffers, and a reader
	// still inside ReadMessage would share them with the next request.
	// The hijacked wrapper may ignore Close. An expired read deadline wakes
	// the reader, and refreshing it is disabled before waiting for exit.
	defer func() {
		readDeadlineMu.Lock()
		stoppingReader = true
		_ = conn.SetReadDeadline(time.Now())
		readDeadlineMu.Unlock()
		_ = conn.Close()
		<-readerDone
	}()

	ticker := time.NewTicker(wsPingInterval)
	defer ticker.Stop()
	for {
		select {
		case msg, ok := <-events:
			if !ok {
				commandMu.Lock()
				_ = ws.control(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "job finished"))
				commandMu.Unlock()
				return
			}
			if !sessionActive() || ws.writeRaw(msg) != nil {
				return
			}
		case <-ticker.C:
			now := time.Now().UTC()
			if !sessionActive() || ws.write(wsMessage{Type: "ping", At: &now}) != nil || ws.control(websocket.PingMessage, nil) != nil {
				return
			}
		case <-readerDone:
			return
		}
	}
}

func (s *inheritService) handleWSCommand(ws *wsConn, meta requestMeta, jobID string, msg wsClientMessage, sessionActive func() bool) {
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
	if sessionActive() {
		_ = ws.write(wsMessage{Type: "ack", Action: msg.Type, Job: view})
	}
}
