// Пакет middleware содержит HTTP-middleware.
// Реализуйте Auth, Logging и Recover самостоятельно.
package middleware

import (
	"context"
	"gopherledger/internal/auth"
	"gopherledger/internal/handler"
	"log"
	"net/http"
	"strings"
	"time"
)

// Auth проверяет токен из заголовка Authorization и помещает ID пользователя в контекст.
// Запросы без валидного токена получают ответ 401 Unauthorized.
//
// Что нужно сделать:
//   - прочитать токен из заголовка
//   - проверить токен через пакет auth
//   - поместить ID пользователя в контекст запроса
//   - передать управление следующему handler или вернуть 401
func Auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		token := strings.TrimSpace(authHeader)
		if token == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		userID, err := auth.ValidateToken(token)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), handler.CtxKeyUserID, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// statusRecorder оборачивает http.ResponseWriter для перехвата статус-кода.
// Используйте эту структуру в Logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (str *statusRecorder) WriteHeader(status int) {
	str.status = status
	str.ResponseWriter.WriteHeader(status)
}

// Logging логирует метод, путь, статус ответа и время выполнения каждого запроса.
//
// Что нужно сделать:
//   - зафиксировать время начала запроса
//   - обернуть w в statusRecorder для перехвата статус-кода
//   - после выполнения handler записать лог
func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rec, r)

		log.Printf("%s %s %d %s", r.Method, r.URL.Path, rec.status, time.Since(start))
	})
}

// Recover перехватывает панику внутри handler, логирует её и возвращает
// клиенту ответ 500 Internal Server Error вместо того, чтобы уронить сервер.
//
// Что нужно сделать:
//   - добавить defer с вызовом recover()
//   - если паника произошла, залогировать её и отдать 500
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("panic: %v", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()

		next.ServeHTTP(w, r)
	})
}
