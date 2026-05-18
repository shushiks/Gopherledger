// Пакет router собирает маршруты и middleware в единый HTTP-обработчик.
// Реализуйте этот пакет самостоятельно.
package router

import (
	"gopherledger/internal/middleware"
	"net/http"

	"gopherledger/internal/handler"
)

// New создаёт и возвращает HTTP-обработчик со всеми маршрутами.
//
// Публичные маршруты (без авторизации):
//
//	POST /api/user/register
//	POST /api/user/login
//
// Защищённые маршруты (требуют токен):
//
//	POST /api/user/orders
//	GET  /api/user/orders
//	GET  /api/user/balance
//	POST /api/user/balance/withdraw
//	GET  /api/user/withdrawals
//	POST /api/stats/export
func New(h *handler.Handler) http.Handler {
	router := http.NewServeMux()
	router.HandleFunc("POST /api/user/register", h.Register)
	router.HandleFunc("POST /api/user/login", h.Login)

	router.Handle("POST /api/user/orders", middleware.Auth(http.HandlerFunc(h.CreateOrder)))
	router.Handle("GET /api/user/orders", middleware.Auth(http.HandlerFunc(h.GetOrders)))
	router.Handle("GET /api/user/balance", middleware.Auth(http.HandlerFunc(h.GetBalance)))
	router.Handle("POST /api/user/balance/withdraw", middleware.Auth(http.HandlerFunc(h.Withdraw)))
	router.Handle("GET /api/user/withdrawals", middleware.Auth(http.HandlerFunc(h.GetWithdrawals)))
	router.Handle("POST /api/stats/export", middleware.Auth(http.HandlerFunc(h.ExportStats)))

	var handlers http.Handler = router
	handlers = middleware.Recover(handlers)
	handlers = middleware.Logging(handlers)

	return handlers
}
