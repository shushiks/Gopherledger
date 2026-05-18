// Пакет handler содержит HTTP-обработчики.
//
// Взаимодействие с бизнес-логикой осуществляется через интерфейс.
// Определите этот интерфейс здесь, по месту использования.
// Реализуйте все обработчики самостоятельно.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gopherledger/internal/domain"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// Handler хранит зависимость от бизнес-логики.
type Service interface {
	RegisterUser(login string, password string) (string, error)
	LoginUser(login string, password string) (string, error)
	CreateOrder(userID int64, number string) (*domain.Order, error)
	GetUserOrders(userID int64) ([]domain.Order, error)
	GetBalance(userID int64) (domain.Balance, error)
	Withdraw(userID int64, orderNumber string, sum float64) error
	GetWithdrawals(userID int64) ([]domain.Withdrawal, error)
	GetSystemStats() (int, map[string]int, float64, float64, error)
}
type Handler struct {
	svc Service
}

// New создаёт Handler.
func New(svc Service) *Handler {
	return &Handler{svc: svc}
}

type ErrorResponse struct {
	Code    int    `json:"code"`
	UserMsg string `json:"message"`
}

// writeError записывает JSON-ответ с ошибкой.
// Клиент видит только userMsg. Внутренние детали пишутся только в лог.
// Прочитайте ТЗ и создайте структуру тела ответа самостоятельно.
func writeError(w http.ResponseWriter, status int, code, userMsg string, internalErr error) {
	if internalErr != nil {
		log.Printf("ошибка code=%s status=%d: %v", code, status, internalErr)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	userError := ErrorResponse{Code: status, UserMsg: userMsg}
	encoder := json.NewEncoder(w)
	if err := encoder.Encode(userError); err != nil {
		log.Printf("Ошибка сериализации: %s", err)
	}
}

// writeJSON записывает успешный JSON-ответ.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Обработчики - реализуйте самостоятельно
// ---------------------------------------------------------------------------

// Register обрабатывает POST /api/user/register.
// При успехе: 200 OK, заголовок Authorization с токеном.
// При дублировании логина: 409 Conflict.
// При некорректных данных: 400 Bad Request.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var data struct {
		Login    string `json:"login"`
		Password string `json:"password"`
	}
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil || data.Login == "" || data.Password == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "некорректные данные", err)
		return
	}

	token, err := h.svc.RegisterUser(data.Login, data.Password)
	if err != nil {
		if errors.Is(err, domain.ErrUserExists) {
			writeError(w, http.StatusConflict, "Conflict", "дублирование логина", err)
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "ошибка сервера", err)
		return
	}

	w.Header().Set("Authorization", token)
	w.WriteHeader(http.StatusOK)
}

// Login обрабатывает POST /api/user/login.
// При успехе: 200 OK, заголовок Authorization с токеном.
// При неверных данных: 401 Unauthorized.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var data struct {
		Login    string `json:"login"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil || data.Login == "" || data.Password == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "некорректные данные", err)
		return
	}
	defer r.Body.Close()
	token, err := h.svc.LoginUser(data.Login, data.Password)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrUserNotFound):
			writeError(w, http.StatusUnauthorized, "unauthorized", "пользователь не найден", err)
		case errors.Is(err, domain.ErrInvalidPassword):
			writeError(w, http.StatusUnauthorized, "unauthorized", "неверный пароль", err)
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", "ошибка сервера", err)
		}
		return
	}
	w.Header().Set("Authorization", token)
	w.WriteHeader(http.StatusOK)
}

// CreateOrder обрабатывает POST /api/user/orders.
// Тело запроса: номер заказа в виде обычного текста.
// 202 Accepted  - новый заказ принят в обработку.
// 200 OK        - заказ уже загружен этим пользователем.
// 409 Conflict  - заказ принадлежит другому пользователю.
// 422 Unprocessable Entity - номер не прошёл проверку Луна.
func (h *Handler) CreateOrder(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "ошибка чтения", err)
		return
	}
	defer r.Body.Close()

	orderNumber := strings.TrimSpace(string(body))

	if orderNumber == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "пустой номер", nil)
		return
	}
	userID, ok := UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "требуется аутентификация", nil)
		return
	}

	_, err = h.svc.CreateOrder(userID, orderNumber)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidOrder):
			writeError(w, http.StatusUnprocessableEntity, "invalid_order", "неверный номер заказа", err)
		case errors.Is(err, domain.ErrOrderExists):
			writeError(w, http.StatusConflict, "conflict", "заказ уже загружен другим пользователем", err)
		case errors.Is(err, domain.ErrOrderOwnedByUser):
			w.WriteHeader(http.StatusOK)
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", "внутренняя ошибка", err)
		}
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// GetOrders обрабатывает GET /api/user/orders.
// 200 OK с JSON-массивом заказов или 204 No Content если заказов нет.
func (h *Handler) GetOrders(w http.ResponseWriter, r *http.Request) {
	userID, ok := UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "требуется аутентификация", nil)
		return
	}
	orders, err := h.svc.GetUserOrders(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "ошибка получения заказов", err)
		return
	}
	if len(orders) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, orders)
}

// GetBalance обрабатывает GET /api/user/balance.
func (h *Handler) GetBalance(w http.ResponseWriter, r *http.Request) {
	userID, ok := UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "требуется аутентификация", nil)
		return
	}
	balance, err := h.svc.GetBalance(userID)
	if err != nil {
		if errors.Is(err, domain.ErrUserNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "пользователь не найден", err)
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "ошибка получения баланса", err)
		return
	}
	writeJSON(w, http.StatusOK, balance)

}

// Withdraw обрабатывает POST /api/user/balance/withdraw.
// 200 OK при успехе.
// 402 Payment Required при нехватке баллов.
// 422 Unprocessable Entity при неверном номере заказа.
func (h *Handler) Withdraw(w http.ResponseWriter, r *http.Request) {
	userID, ok := UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "требуется аутентификация", nil)
		return
	}

	var data struct {
		OrderNumber string  `json:"order"`
		Sum         float64 `json:"sum"`
	}

	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "ошибка парсинга JSON", err)
		return
	}
	defer r.Body.Close()
	err := h.svc.Withdraw(userID, data.OrderNumber, data.Sum)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidOrder):
			writeError(w, http.StatusUnprocessableEntity, "invalid_order", "неверный номер заказа", err)
		case errors.Is(err, domain.ErrInsufficientFunds):
			writeError(w, http.StatusPaymentRequired, "insufficient_funds", "недостаточно баллов", err)
		case errors.Is(err, domain.ErrUserNotFound):
			writeError(w, http.StatusNotFound, "not_found", "пользователь не найден", err)
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", "внутренняя ошибка", err)
		}
		return
	}
	w.WriteHeader(http.StatusOK)

}

// GetWithdrawals обрабатывает GET /api/user/withdrawals.
// 200 OK с массивом или 204 No Content если списаний нет.
func (h *Handler) GetWithdrawals(w http.ResponseWriter, r *http.Request) {
	userID, ok := UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "требуется аутентификация", nil)
		return
	}
	withdrawals, err := h.svc.GetWithdrawals(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "ошибка получения истории", err)
		return
	}
	if len(withdrawals) == 0 {
		w.WriteHeader(http.StatusNoContent)
	}

	writeJSON(w, http.StatusOK, withdrawals)
}

// ExportStats обрабатывает POST /api/stats/export.
// Собирает статистику системы и записывает её в текстовый файл stats.txt
// в корне проекта. Возвращает 200 OK при успехе.
//
// Файл должен содержать:
//   - общее число зарегистрированных пользователей
//   - общее число заказов и их распределение по статусам
//   - суммарное количество начисленных баллов
//   - суммарное количество списанных баллов
//   - время генерации отчёта
//
// Для работы с файлами используйте пакет os (неделя 8).
func (h *Handler) ExportStats(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	_, ok := UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "требуется аутентификация", nil)
		return
	}
	userCount, ordersMap, totalAccrual, totalWithdrawn, err := h.svc.GetSystemStats()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Не удалось собрать статистику", err)
		return
	}

	report := "Отчёт системы Gopherledger\n"
	report += fmt.Sprintf("Время генерации: %s\n", time.Now().Format("11.01.2011 16:04:15"))
	report += fmt.Sprintf("Зарегистрировано пользователей: %d\n", userCount)
	report += fmt.Sprintf("Всего заказов: %d\n", len(ordersMap))
	report += "Распределение по статусам:\n"
	for status, count := range ordersMap {
		report += fmt.Sprintf("  - %s: %d\n", status, count)
	}
	report += fmt.Sprintf("Суммарно начислено баллов: %.2f\n", totalAccrual)
	report += fmt.Sprintf("Суммарно списано баллов: %.2f\n", totalWithdrawn)

	err = os.WriteFile("stats.txt", []byte(report), 0644)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "file_error", "Ошибка записи файла на диск", err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ---------------------------------------------------------------------------
// Вспомогательная функция для работы с контекстом - предоставлена
// ---------------------------------------------------------------------------

type contextKey string

const CtxKeyUserID contextKey = "userID"

// UserIDFromContext извлекает ID аутентифицированного пользователя из контекста.
// Возвращает 0, false если значение отсутствует.
func UserIDFromContext(ctx context.Context) (int64, bool) {
	userID, ok := ctx.Value(CtxKeyUserID).(int64)
	return userID, ok
}
