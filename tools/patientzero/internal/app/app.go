// Package app is PatientZero's HTTP surface: entry points modelled on
// OutSystems 11 ones, each of which runs business SQL and writes the log rows
// the platform would write for the request, all under one Request_Key with
// the caller's session, user and client IP (ADR 0010).
package app

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"heartbeat/tools/patientzero/internal/oslog"
	"heartbeat/tools/patientzero/internal/shop"
)

// The caller's identity. A load generator sets these to give each virtual
// user a stable user and session; X-Forwarded-For gives its client IP.
const (
	HeaderUser    = "X-PatientZero-User"
	HeaderSession = "X-PatientZero-Session"
)

// Entry point paths, shaped like the URLs of the OutSystems entry points they
// stand for.
const (
	PathOrderList  = "/PatientZero_Web/OrderList.aspx"
	PathPlaceOrder = "/PatientZero_App/screenservices/PatientZero_App/MainFlow/Checkout/ServiceAPIPlaceOrder"
	PathAPIOrders  = "/PatientZero_API/rest/Orders/orders"
)

// The application and its modules (eSpaces), as the log rows name them.
const (
	Application    = "PatientZero"
	ApplicationKey = "0a7e1e57-0000-4000-8000-00000000a001"
	TenantID       = 1
)

type module struct {
	Name string
	ID   int
}

var (
	webModule = module{"PatientZero_Web", 101} // Traditional web screens
	appModule = module{"PatientZero_App", 102} // Reactive app and its server actions
	apiModule = module{"PatientZero_API", 103} // Exposed REST API
)

// ordersShown is how many orders the screens and the API return.
const ordersShown = 20

// Server serves the entry points.
type Server struct {
	Store  shop.Store
	Logs   oslog.Writer
	Schema oslog.Schema
	// FrontEnd is the front-end server name written as Executed_By/Server.
	FrontEnd string
	// Ready reports whether both databases are usable; nil means always.
	Ready func(context.Context) error
	Now   func() time.Time
	Log   *slog.Logger
}

// Handler routes the health endpoints and the entry points.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.HandleFunc("GET "+PathOrderList, s.orderList)
	mux.HandleFunc("POST "+PathPlaceOrder, s.placeOrder)
	mux.HandleFunc("GET "+PathAPIOrders, s.apiOrders)
	return mux
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	if s.Ready != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := s.Ready(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// call is one request: who made it, under which Request_Key, since when.
type call struct {
	RequestKey string
	Session    string
	Username   string
	UserID     int
	ClientIP   string
	Start      time.Time
	module     module
	entryPoint string
	action     string
}

func (s *Server) begin(r *http.Request, m module, entryPoint, action string) *call {
	c := &call{
		RequestKey: newGUID(),
		Session:    r.Header.Get(HeaderSession),
		Username:   r.Header.Get(HeaderUser),
		ClientIP:   clientIP(r),
		Start:      s.now(),
		module:     m,
		entryPoint: entryPoint,
		action:     action,
	}
	if c.Session == "" {
		c.Session = newSessionID()
	}
	return c
}

// identify resolves the user against the business database, as the
// platform's session does. Anonymous callers are user 0.
func (s *Server) identify(ctx context.Context, c *call) error {
	if c.Username == "" {
		return nil
	}
	id, err := s.Store.Customer(ctx, c.Username)
	c.UserID = id
	return err
}

// common are the columns every log row of the call carries.
func (c *call) common() map[string]any {
	return map[string]any{
		"Request_Key":      c.RequestKey,
		"Espace_Id":        c.module.ID,
		"Espace_Name":      c.module.Name,
		"Tenant_Id":        TenantID,
		"Application_Name": Application,
		"Application_Key":  ApplicationKey,
	}
}

func (c *call) user(cols map[string]any) map[string]any {
	if c.Username != "" {
		cols["User_Id"] = c.UserID
		cols["Username"] = c.Username
	} else {
		cols["User_Id"] = 0
	}
	return cols
}

// errorRow is the error log row for a failed call. Its Id links the rows of
// the other log types that have an Error_Id.
func (s *Server) errorRow(c *call, at time.Time, err error) (oslog.Row, string) {
	id := newGUID()
	cols := c.common()
	cols["Id"] = id
	cols["Instant"] = at
	cols["Session_Id"] = c.Session
	cols["Message"] = err.Error()
	cols["Stack"] = fmt.Sprintf("at %s.%s()\nat %s.%s()", c.module.Name, c.action, c.module.Name, c.entryPoint)
	cols["Module_Name"] = c.module.Name
	cols["Server"] = s.FrontEnd
	cols["Entrypoint_Name"] = c.entryPoint
	cols["Action_Name"] = c.action
	cols["EnvironmentInformation"] = fmt.Sprintf("FrontEnd: %s, Module: %s (Id=%d)", s.FrontEnd, c.module.Name, c.module.ID)
	return oslog.Row{Type: "error", Columns: c.user(cols)}, id
}

// finish writes the call's log rows. A failed write is logged and does not
// change the response: the platform's log service is asynchronous too.
func (s *Server) finish(ctx context.Context, c *call, rows []oslog.Row) {
	if err := s.Logs.Write(context.WithoutCancel(ctx), rows); err != nil {
		s.logger().Error("writing log rows failed", "request_key", c.RequestKey, "entry_point", c.entryPoint, "err", err)
	}
}

// Traditional web screen: OrderList, the user's newest orders.
func (s *Server) orderList(w http.ResponseWriter, r *http.Request) {
	c := s.begin(r, webModule, "OrderList", "Preparation")
	var orders []shop.Order
	err := s.identify(r.Context(), c)
	if err == nil {
		orders, err = s.Store.Orders(r.Context(), c.UserID, ordersShown)
	}
	var page strings.Builder
	if err == nil {
		err = orderListPage.Execute(&page, struct {
			User   string
			Orders []shop.Order
		}{c.Username, orders})
	}
	end := s.now()

	// The screen log's Instant is when the request started.
	screen := c.common()
	screen["Instant"] = c.Start
	screen["Duration"] = millis(end.Sub(c.Start))
	screen["Screen"] = c.entryPoint
	screen["Screen_Type"] = "Web"
	screen["Access_Mode"] = "Navigate"
	screen["Action_Name"] = c.action
	screen["Session_Id"] = c.Session
	screen["Client_IP"] = c.ClientIP
	screen["Executed_By"] = s.FrontEnd
	screen["Viewstate_Bytes"] = 0
	rows := []oslog.Row{{Type: "screen", Columns: c.user(screen)}}
	if err != nil {
		row, _ := s.errorRow(c, end, err)
		rows = append(rows, row)
	}
	s.finish(r.Context(), c, rows)

	if err != nil {
		http.Error(w, "An error occurred processing your request.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(page.String()))
}

var orderListPage = template.Must(template.New("OrderList").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><title>Orders</title></head><body>
<h1>Orders{{with .User}} of {{.}}{{end}}</h1>
<table>{{range .Orders}}
<tr><td>{{.ID}}</td><td>{{.Product}}</td><td>{{.Quantity}}</td><td>{{printf "%.2f" .Total}}</td></tr>{{else}}
<tr><td>No orders yet.</td></tr>{{end}}
</table></body></html>
`))

// placeOrderInput is the server action's input, as the Reactive client posts
// it.
type placeOrderInput struct {
	ProductID int `json:"productId"`
	Quantity  int `json:"quantity"`
}

// Reactive server action: Checkout's PlaceOrder.
func (s *Server) placeOrder(w http.ResponseWriter, r *http.Request) {
	c := s.begin(r, appModule, "Checkout", "PlaceOrder")
	var in placeOrderInput
	var order shop.Order
	err := s.identify(r.Context(), c)
	if err == nil {
		if derr := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); derr != nil {
			err = fmt.Errorf("%w: %v", shop.ErrInvalid, derr)
		}
	}
	if err == nil {
		order, err = s.Store.PlaceOrder(r.Context(), c.UserID, in.ProductID, in.Quantity)
	}
	end := s.now()

	var rows []oslog.Row
	errID := ""
	if err != nil {
		var row oslog.Row
		row, errID = s.errorRow(c, end, err)
		rows = append(rows, row)
	}
	// The mobile request log's Instant is when the request completed; its
	// Duration is in seconds, as the OutSystems reference documents it.
	req := c.common()
	req["Id"] = newGUID()
	req["Instant"] = end
	req["Duration"] = int64(end.Sub(c.Start).Round(time.Second) / time.Second)
	req["Screen"] = c.entryPoint
	req["Endpoint"] = c.action
	req["Source"] = c.ClientIP
	req["Executed_By"] = s.FrontEnd
	req["Login_Id"] = c.Session
	req["Error_Id"] = errID
	rows = append([]oslog.Row{{Type: "mobile_request", Columns: c.user(req)}}, rows...)
	s.finish(r.Context(), c, rows)

	if err != nil {
		writeServerActionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"Order": order}})
}

// Exposed REST API: GET orders of the calling user.
func (s *Server) apiOrders(w http.ResponseWriter, r *http.Request) {
	c := s.begin(r, apiModule, "Orders", "GetOrders")
	var orders []shop.Order
	err := s.identify(r.Context(), c)
	if err == nil {
		orders, err = s.Store.Orders(r.Context(), c.UserID, ordersShown)
	}
	end := s.now()

	var rows []oslog.Row
	errID := ""
	if err != nil {
		var row oslog.Row
		row, errID = s.errorRow(c, end, err)
		rows = append(rows, row)
	}
	// The integration log has no user or session: the API's own LogMessage
	// carries them, as an app that logs its callers does.
	general := c.common()
	general["Instant"] = end
	general["Session_Id"] = c.Session
	general["Client_IP"] = c.ClientIP
	general["Message"] = fmt.Sprintf("GetOrders returned %d orders", len(orders))
	general["Message_Type"] = "Orders"
	general["Module_Name"] = c.module.Name
	general["Action_Name"] = c.action
	general["Entrypoint_Name"] = c.entryPoint
	general["Error_Id"] = errID
	// The integration log's Instant is when the call completed. Source is
	// the caller of an exposed API; Endpoint is empty for exposed ones.
	integration := c.common()
	integration["Id"] = newGUID()
	integration["Instant"] = end
	integration["Duration"] = millis(end.Sub(c.Start))
	integration["Source"] = c.ClientIP
	integration["Endpoint"] = ""
	integration["Action"] = c.action
	integration["Type"] = "REST"
	integration["Executed_By"] = s.FrontEnd
	integration["Is_Expose"] = true
	integration["Error_Id"] = errID
	rows = append([]oslog.Row{
		{Type: "general", Columns: c.user(general)},
		{Type: "integration", Columns: integration},
	}, rows...)
	s.finish(r.Context(), c, rows)

	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"Errors": "Internal Server Error"})
		return
	}
	writeJSON(w, http.StatusOK, orders)
}

func writeServerActionError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, shop.ErrInvalid) {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]any{"exception": map[string]string{"name": "ServerException", "message": err.Error()}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Server) logger() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func millis(d time.Duration) int64 { return d.Milliseconds() }

// clientIP is the first X-Forwarded-For address, as the platform logs the
// original caller behind a load balancer, or the connection's peer.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first, _, _ := strings.Cut(xff, ",")
		if ip := net.ParseIP(strings.TrimSpace(first)); ip != nil {
			return ip.String()
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// newGUID is a random (version 4) GUID, the form of Request_Key and Id.
func newGUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// newSessionID looks like an ASP.NET session id: 24 lower-case characters.
func newSessionID() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz012345"
	var b [24]byte
	_, _ = rand.Read(b[:])
	for i := range b {
		b[i] = alphabet[b[i]%32]
	}
	return string(b[:])
}
