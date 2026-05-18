package handler

import (
	"context"
	"encoding/json"
	"gopherledger/internal/domain"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// Напишите тесты для HTTP-обработчиков.
//
// Используйте пакет net/http/httptest:
// https://pkg.go.dev/net/http/httptest
//
// Для тестирования без реального сервиса реализуйте fakeService,
// который реализует локальный интерфейс service.
//
// Покройте тестами минимум:
//   - Register: успех, дублирование логина, пустые поля
//   - Login: успех, неверные данные
//   - CreateOrder: успех, неверный номер Луна, конфликты
//   - GetOrders: с заказами, без заказов
//   - GetBalance: успех
//   - Withdraw: успех, нехватка баллов
//   - GetWithdrawals: с записями, без записей
//   - ExportStats: успех, проверьте что файл создан на диске

type fakeService struct {
	users       map[string]string
	orders      map[string]int64
	balances    map[int64]domain.Balance
	withdrawals map[int64][]domain.Withdrawal
}

func newFakeService() *fakeService {
	return &fakeService{
		users:       make(map[string]string),
		orders:      make(map[string]int64),
		balances:    make(map[int64]domain.Balance),
		withdrawals: make(map[int64][]domain.Withdrawal),
	}
}

func (s *fakeService) RegisterUser(login, password string) (string, error) {
	if login == "conflict" {
		return "", domain.ErrUserExists
	}
	token := "fake-token-for-" + login
	s.users[login] = token
	return token, nil
}

func (s *fakeService) LoginUser(login, password string) (string, error) {
	if login == "unknown" {
		return "", domain.ErrUserNotFound
	}
	if password == "wrong" {
		return "", domain.ErrInvalidPassword
	}
	return "fake-token-for-" + login, nil
}

func (s *fakeService) CreateOrder(userID int64, number string) (*domain.Order, error) {
	if number == "invalid" {
		return nil, domain.ErrInvalidOrder
	}
	if owner, ok := s.orders[number]; ok {
		if owner == userID {
			return nil, domain.ErrOrderOwnedByUser
		}
		return nil, domain.ErrOrderExists
	}

	s.orders[number] = userID
	return &domain.Order{
		Number:     number,
		Status:     domain.OrderStatusNew,
		UploadedAt: time.Now(),
	}, nil
}

func (s *fakeService) GetUserOrders(userID int64) ([]domain.Order, error) {
	var result []domain.Order
	for num, uid := range s.orders {
		if uid == userID {
			result = append(result, domain.Order{Number: num, Status: domain.OrderStatusNew})
		}
	}
	return result, nil
}

func (s *fakeService) GetBalance(userID int64) (domain.Balance, error) {
	if bal, ok := s.balances[userID]; ok {
		return bal, nil
	}
	return domain.Balance{Current: 0, Withdrawn: 0}, nil
}

func (s *fakeService) Withdraw(userID int64, orderNumber string, sum float64) error {
	if orderNumber == "invalid" {
		return domain.ErrInvalidOrder
	}
	bal := s.balances[userID]
	if bal.Current < sum {
		return domain.ErrInsufficientFunds
	}
	return nil
}

func (s *fakeService) GetWithdrawals(userID int64) ([]domain.Withdrawal, error) {
	return s.withdrawals[userID], nil
}

func (s *fakeService) GetSystemStats() (int, map[string]int, float64, float64, error) {
	return 1, map[string]int{"PROCESSED": 5}, 1000.0, 500.0, nil
}

func TestRegisterHandler(t *testing.T) {
	tests := []struct {
		name           string
		requestBody    string
		method         string
		setupService   func(s *fakeService)
		expectedStatus int
		expectToken    bool
	}{
		{
			name:           "Успешная регистрация",
			requestBody:    `{"login": "ivan", "password": "123"}`,
			method:         http.MethodPost,
			setupService:   func(s *fakeService) {},
			expectedStatus: http.StatusOK,
			expectToken:    true,
		},
		{
			name:           "Дублирование логина",
			requestBody:    `{"login": "conflict", "password": "123"}`,
			method:         http.MethodPost,
			setupService:   func(s *fakeService) { s.RegisterUser("ivan", "123") },
			expectedStatus: http.StatusConflict,
			expectToken:    false,
		},
		{
			name:           "пустые поля",
			requestBody:    `{"login": "", "password": ""}`,
			method:         http.MethodPost,
			setupService:   func(s *fakeService) {},
			expectedStatus: http.StatusBadRequest,
			expectToken:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := newFakeService()
			tc.setupService(svc)
			h := New(svc)

			req := httptest.NewRequest(tc.method, "/api/user/register", strings.NewReader(tc.requestBody))
			w := httptest.NewRecorder()

			h.Register(w, req)

			if w.Code != tc.expectedStatus {
				t.Errorf("%s: ожидали статус %d, получили %d", tc.name, tc.expectedStatus, w.Code)
			}
			if tc.expectToken {
				token := w.Header().Get("Authorization")
				if token == "" {
					t.Error("Ожидался заголовок Authorization, но его нет или он пустой")
				}
			}
		})
	}
}

func TestLoginHandler(t *testing.T) {
	tests := []struct {
		name           string
		requestBody    string
		method         string
		setupService   func(s *fakeService)
		expectedStatus int
		expectToken    bool
	}{
		{
			name:           "Успешный login",
			requestBody:    `{"login": "ivan", "password": "123"}`,
			method:         http.MethodPost,
			setupService:   func(s *fakeService) {},
			expectedStatus: http.StatusOK,
			expectToken:    true,
		},
		{
			name:           "Неверный пароль",
			requestBody:    `{"login": "ivan", "password": "wrong"}`,
			method:         http.MethodPost,
			setupService:   func(s *fakeService) {},
			expectedStatus: http.StatusUnauthorized,
			expectToken:    false,
		},
		{
			name:           "Пользователь не найден",
			requestBody:    `{"login": "unknown", "password": "123"}`,
			method:         http.MethodPost,
			setupService:   func(s *fakeService) {},
			expectedStatus: http.StatusUnauthorized,
			expectToken:    false,
		},
		{
			name:           "Невалидный json",
			requestBody:    `{"login": "", "password": ""}`,
			method:         http.MethodPost,
			setupService:   func(s *fakeService) {},
			expectedStatus: http.StatusBadRequest,
			expectToken:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := newFakeService()
			tc.setupService(svc)
			h := New(svc)

			req := httptest.NewRequest(tc.method, "/api/user/login", strings.NewReader(tc.requestBody))
			w := httptest.NewRecorder()

			h.Login(w, req)

			if w.Code != tc.expectedStatus {
				t.Errorf("%s: ожидали статус %d, получили %d", tc.name, tc.expectedStatus, w.Code)
			}
			if tc.expectToken {
				token := w.Header().Get("Authorization")
				if token == "" {
					t.Error("Ожидался заголовок Authorization, но его нет или он пустой")
				}
			}
		})
	}
}

func TestCreateOrder(t *testing.T) {

	const (
		myUserID    int64 = 1
		otherUserID int64 = 2
		validOrder        = "79927398713"
	)

	tests := []struct {
		name           string
		isAuth         bool
		userID         int64
		orderNumber    string
		setupService   func(s *fakeService)
		expectedStatus int
	}{
		{
			name:        "Успешное создание заказа (202)",
			isAuth:      true,
			userID:      myUserID,
			orderNumber: validOrder,
			setupService: func(s *fakeService) {

			},
			expectedStatus: http.StatusAccepted,
		},
		{
			name:        "Заказ уже загружен этим же пользователем (200)",
			isAuth:      true,
			userID:      myUserID,
			orderNumber: validOrder,
			setupService: func(s *fakeService) {
				s.orders[validOrder] = myUserID
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:        "Конфликт: заказ принадлежит другому пользователю (409)",
			isAuth:      true,
			userID:      myUserID,
			orderNumber: validOrder,
			setupService: func(s *fakeService) {
				s.orders[validOrder] = otherUserID
			},
			expectedStatus: http.StatusConflict,
		},
		{
			name:        "Ошибка Луна / Некорректный номер (422)",
			isAuth:      true,
			userID:      myUserID,
			orderNumber: "invalid",
			setupService: func(s *fakeService) {
			},
			expectedStatus: http.StatusUnprocessableEntity,
		},
		{
			name:           "Ошибка: пользователь не авторизован (401)",
			isAuth:         false,
			orderNumber:    validOrder,
			setupService:   func(s *fakeService) {},
			expectedStatus: http.StatusUnauthorized,
		},
	}

	for _, tc := range tests {
		t.Run(
			tc.name,
			func(t *testing.T) {
				svc := newFakeService()
				tc.setupService(svc)
				h := New(svc)

				req := httptest.NewRequest(http.MethodPost, "/api/user/orders", strings.NewReader(tc.orderNumber))

				if tc.isAuth {
					ctx := context.WithValue(req.Context(), CtxKeyUserID, tc.userID)
					req = req.WithContext(ctx)
				}
				w := httptest.NewRecorder()
				h.CreateOrder(w, req)

				if w.Code != tc.expectedStatus {
					t.Errorf("%s: ожидали статус %d, получили %d", tc.name, tc.expectedStatus, w.Code)
				}
			},
		)
	}
}

func TestGetOrders(t *testing.T) {
	tests := []struct {
		name           string
		userId         int64
		isAuth         bool
		setupService   func(s *fakeService)
		expectedStatus int
		expectedCount  int
	}{
		{
			name:   "Успешное получение списка",
			isAuth: true,
			setupService: func(s *fakeService) {
				s.orders["79927398713"] = 1
				s.orders["12345678903"] = 1
			},
			expectedStatus: http.StatusOK,
			expectedCount:  2,
		},
		{
			name:   "Список пуст",
			isAuth: true,
			setupService: func(s *fakeService) {
			},
			expectedStatus: http.StatusNoContent,
			expectedCount:  0,
		},
		{
			name:           "Неавторизован (401)",
			isAuth:         false,
			setupService:   func(s *fakeService) {},
			expectedStatus: http.StatusUnauthorized,
		},
	}

	for _, tc := range tests {
		t.Run(
			tc.name,
			func(t *testing.T) {
				svc := newFakeService()
				tc.setupService(svc)
				h := New(svc)

				req := httptest.NewRequest(http.MethodGet, "/api/user/orders", nil)

				if tc.isAuth {
					ctx := context.WithValue(req.Context(), CtxKeyUserID, int64(1))
					req = req.WithContext(ctx)
				}

				w := httptest.NewRecorder()
				h.GetOrders(w, req)
				if w.Code != tc.expectedStatus {
					t.Errorf("%s: ожидали статус %d, получили %d", tc.name, tc.expectedStatus, w.Code)
				}
				if w.Code == http.StatusOK {
					var orders []domain.Order
					err := json.NewDecoder(w.Body).Decode(&orders)
					if err != nil {
						t.Fatalf("не удалось распарсить JSON ответа: %v", err)
					}
					if len(orders) != tc.expectedCount {
						t.Errorf("ожидали %d заказов, получили %d", tc.expectedCount, len(orders))
					}
				}
			},
		)
	}
}

func TestGetBalance(t *testing.T) {
	tests := []struct {
		name           string
		isAuth         bool
		setupService   func(s *fakeService)
		expectedStatus int
		expectedBody   domain.Balance
	}{
		{
			name:   "Успешное получение баланса",
			isAuth: true,
			setupService: func(s *fakeService) {
				s.balances[1] = domain.Balance{
					Current:   500.55,
					Withdrawn: 100.22,
				}
			},
			expectedStatus: http.StatusOK,
			expectedBody: domain.Balance{
				Current:   500.55,
				Withdrawn: 100.22,
			},
		},
		{
			name:           "Новый пользователь (нулевой баланс)",
			isAuth:         true,
			setupService:   func(s *fakeService) {},
			expectedStatus: http.StatusOK,
			expectedBody: domain.Balance{
				Current:   0,
				Withdrawn: 0,
			},
		},
		{
			name:           "Ошибка: неавторизован",
			isAuth:         false,
			setupService:   func(s *fakeService) {},
			expectedStatus: http.StatusUnauthorized,
		},
	}

	for _, tc := range tests {
		t.Run(
			tc.name,
			func(t *testing.T) {
				svc := newFakeService()
				tc.setupService(svc)
				h := New(svc)

				req := httptest.NewRequest(http.MethodGet, "/api/user/balance", nil)
				if tc.isAuth {
					ctx := context.WithValue(req.Context(), CtxKeyUserID, int64(1))
					req = req.WithContext(ctx)
				}

				w := httptest.NewRecorder()
				h.GetBalance(w, req)

				if w.Code != tc.expectedStatus {
					t.Errorf("%s: ожидали статус %d, получили %d", tc.name, tc.expectedStatus, w.Code)
				}

				if w.Code == http.StatusOK {
					var actualBalance domain.Balance
					if err := json.NewDecoder(w.Body).Decode(&actualBalance); err != nil {
						t.Fatalf("не удалось декодировать JSON-ответ: %v", err)
					}

					if actualBalance.Current != tc.expectedBody.Current {
						t.Errorf("Текущий баланс: получено %f, ожидалось %f", actualBalance.Current, tc.expectedBody.Current)
					}
					if actualBalance.Withdrawn != tc.expectedBody.Withdrawn {
						t.Errorf("Списано баллов: получено %f, ожидалось %f", actualBalance.Withdrawn, tc.expectedBody.Withdrawn)
					}
				}
			},
		)
	}
}

func TestWithdraw(t *testing.T) {
	const myUID int64 = 1
	tests := []struct {
		name   string
		body   string
		setup  func(s *fakeService)
		status int
	}{
		{
			name:   "Успех",
			body:   `{"order":"79927398713","sum":100}`,
			setup:  func(s *fakeService) { s.balances[myUID] = domain.Balance{Current: 500} },
			status: http.StatusOK,
		},
		{
			name:   "Мало баллов",
			body:   `{"order":"79927398713","sum":1000}`,
			setup:  func(s *fakeService) { s.balances[myUID] = domain.Balance{Current: 10} },
			status: http.StatusPaymentRequired,
		},
		{
			name:   "Плохой заказ",
			body:   `{"order":"invalid","sum":10}`,
			setup:  func(s *fakeService) { s.balances[myUID] = domain.Balance{Current: 100} },
			status: http.StatusUnprocessableEntity,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newFakeService()
			tt.setup(s)
			h := New(s)
			req := httptest.NewRequest("POST", "/api/user/balance/withdraw", strings.NewReader(tt.body))
			req = req.WithContext(context.WithValue(req.Context(), CtxKeyUserID, myUID))
			rr := httptest.NewRecorder()
			h.Withdraw(rr, req)
			if rr.Code != tt.status {
				t.Errorf("получено %d, ожидалось %d", rr.Code, tt.status)
			}
		})
	}
}

func TestExportStats(t *testing.T) {
	h := New(newFakeService())
	req := httptest.NewRequest("POST", "/api/stats/export", nil)
	w := httptest.NewRecorder()

	ctx := context.WithValue(req.Context(), CtxKeyUserID, int64(1))
	req = req.WithContext(ctx)
	defer os.Remove("stats.txt")

	_ = os.Remove("stats.txt")

	h.ExportStats(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("получили %d, ожидали 200", w.Code)
	}

	if _, err := os.Stat("stats.txt"); os.IsNotExist(err) {
		t.Error("файл stats.txt не был создан")
	}
}
