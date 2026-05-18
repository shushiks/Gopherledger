package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"gopherledger/internal/domain"
	"sync"
	"testing"
	"time"
)

// Напишите тесты для бизнес-логики.
//
// Для тестирования без реального хранилища реализуйте fakeStore -
// структуру, которая реализует интерфейс, определённый вами в пакете service.
// (domain.Repository в проекте отсутствует - интерфейс определяете вы сами)
//
// Покройте тестами минимум:
//   - RegisterUser: успех, повторная регистрация
//   - LoginUser: успех, неверный пароль, несуществующий пользователь
//   - CreateOrder: успех, неверный номер Луна, повторная загрузка тем же пользователем, другим пользователем
//   - Withdraw: успех, нехватка баллов, неверный номер Луна
type FakeStore struct {
	mu          sync.RWMutex
	users       map[string]*domain.User
	orders      map[string]*domain.Order
	balances    map[int64]*domain.Balance
	withdrawals map[int64][]domain.Withdrawal
	nextID      int64
}

func NewFakeStore() *FakeStore {
	return &FakeStore{
		users:       make(map[string]*domain.User),
		orders:      make(map[string]*domain.Order),
		balances:    make(map[int64]*domain.Balance),
		withdrawals: make(map[int64][]domain.Withdrawal),
		nextID:      1,
	}
}

func (s *FakeStore) CreateUser(login, passwordHash string) (*domain.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.users[login]; ok {
		return nil, domain.ErrUserExists
	}

	user := &domain.User{
		ID:           s.nextID,
		Login:        login,
		PasswordHash: passwordHash,
	}
	s.users[login] = user
	s.balances[user.ID] = &domain.Balance{Current: 0, Withdrawn: 0}
	s.nextID++
	return user, nil
}

func (s *FakeStore) GetUserByLogin(login string) (*domain.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	user, ok := s.users[login]
	if !ok {
		return nil, domain.ErrUserNotFound
	}
	return user, nil
}

func (s *FakeStore) CreateOrder(userID int64, number string) (*domain.Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.orders[number]; ok {
		if existing.UserID == userID {
			return nil, domain.ErrOrderOwnedByUser
		}
		return nil, domain.ErrOrderExists
	}

	order := &domain.Order{
		ID:         s.nextID,
		UserID:     userID,
		Number:     number,
		Status:     domain.OrderStatusNew,
		UploadedAt: time.Now(),
	}
	s.orders[number] = order
	s.nextID++
	return order, nil
}

func (s *FakeStore) GetUserOrders(userID int64) ([]domain.Order, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []domain.Order
	for _, o := range s.orders {
		if o.UserID == userID {
			result = append(result, *o)
		}
	}
	return result, nil
}

func (s *FakeStore) GetBalance(userID int64) (domain.Balance, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	bal, ok := s.balances[userID]
	if !ok {
		return domain.Balance{}, domain.ErrUserNotFound
	}
	return *bal, nil
}

func (s *FakeStore) Withdraw(userID int64, orderNumber string, sum float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	bal := s.balances[userID]
	if bal.Current < sum {
		return domain.ErrInsufficientFunds
	}

	bal.Current -= sum
	bal.Withdrawn += sum

	s.withdrawals[userID] = append(s.withdrawals[userID], domain.Withdrawal{
		OrderNumber: orderNumber,
		Sum:         sum,
		ProcessedAt: time.Now(),
	})
	return nil
}

func (s *FakeStore) UpdateOrderStatus(number, status string, accrual float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	order, ok := s.orders[number]
	if !ok {
		return domain.ErrInvalidOrder
	}

	order.Status = status
	order.Accrual = accrual

	// Если заказ обработан, пополняем баланс пользователя
	if status == domain.OrderStatusProcessed && accrual > 0 {
		if bal, exists := s.balances[order.UserID]; exists {
			bal.Current += accrual
		}
	}
	return nil
}

func (s *FakeStore) GetOrdersForProcessing() ([]domain.Order, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []domain.Order
	for _, o := range s.orders {
		if o.Status == domain.OrderStatusNew || o.Status == domain.OrderStatusProcessing {
			result = append(result, *o)
		}
	}
	return result, nil
}

func (s *FakeStore) GetWithdrawals(userID int64) ([]domain.Withdrawal, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.withdrawals[userID], nil
}

func (s *FakeStore) GetOverallStats() (int, map[string]int, float64, float64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	statusMap := make(map[string]int)
	var accrual float64
	for _, o := range s.orders {
		statusMap[o.Status]++
		accrual += o.Accrual
	}

	var withdrawn float64
	for _, list := range s.withdrawals {
		for _, w := range list {
			withdrawn += w.Sum
		}
	}

	return len(s.users), statusMap, accrual, withdrawn, nil
}

func TestRegisterUser(t *testing.T) {
	tests := []struct {
		name          string
		login         string
		password      string
		prepareStore  func(s *FakeStore)
		wantErr       bool
		expectedError error
	}{
		{
			name:          "Успешная регистрация нового пользователя",
			login:         "Ivan",
			password:      "123",
			prepareStore:  func(s *FakeStore) {},
			wantErr:       false,
			expectedError: nil,
		},
		{
			name:     "Ошибка при создании дубликата пользователя",
			login:    "Ivan",
			password: "123",
			prepareStore: func(s *FakeStore) {
				s.CreateUser("Ivan", "hello")
			},
			wantErr:       true,
			expectedError: domain.ErrUserExists,
		},
	}

	for _, tc := range tests {
		t.Run(
			tc.name,
			func(t *testing.T) {
				store := NewFakeStore()
				tc.prepareStore(store)
				svc := New(store)

				token, err := svc.RegisterUser(tc.login, tc.password)
				if tc.wantErr {
					if err == nil {
						t.Errorf("RegisterUser() ожидает ошибку, но получил пустоту")
						return
					}
					if !errors.Is(err, tc.expectedError) {
						t.Errorf("RegisterUser() error = %v, ожидалось %v", err, tc.expectedError)
					}
				} else {
					if err != nil {
						t.Errorf("RegisterUser() неожиданная error: %v", err)
						return
					}
					if token == "" {
						t.Error("RegisterUser() вернул пустой токен при успехе")
					}
				}
			},
		)
	}
}

func TestLoginUser(t *testing.T) {
	tests := []struct {
		name          string
		login         string
		password      string
		prepareStore  func(s *FakeStore)
		wantErr       bool
		expectedError error
	}{
		{
			name:     "Успешный логин",
			login:    "Ivan",
			password: "123",
			prepareStore: func(s *FakeStore) {
				hash := sha256.Sum256([]byte("123"))
				passwordHash := hex.EncodeToString(hash[:])
				s.CreateUser("Ivan", passwordHash)
			},
			wantErr:       false,
			expectedError: nil,
		},
		{
			name:     "Неверный пароль",
			login:    "Ivan",
			password: "123",
			prepareStore: func(s *FakeStore) {
				hash := sha256.Sum256([]byte("1234"))
				passwordHash := hex.EncodeToString(hash[:])
				s.CreateUser("Ivan", passwordHash)
			},
			wantErr:       true,
			expectedError: domain.ErrInvalidPassword,
		},
		{
			name:          "Несуществующий пользователь",
			login:         "Ivan",
			password:      "123",
			prepareStore:  func(s *FakeStore) {},
			wantErr:       true,
			expectedError: domain.ErrUserNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(
			tc.name,
			func(t *testing.T) {
				store := NewFakeStore()
				tc.prepareStore(store)
				svc := New(store)

				token, err := svc.LoginUser(tc.login, tc.password)
				if tc.wantErr {
					if err == nil {
						t.Errorf("LoginUser() ожидает ошибку, но получил пустоту")
						return
					}
					if !errors.Is(err, tc.expectedError) {
						t.Errorf("LoginUser() error = %v, ожидалось %v", err, tc.expectedError)
					}
				} else {
					if err != nil {
						t.Errorf("LoginUser() неожиданная error: %v", err)
						return
					}
					if token == "" {
						t.Error("LoginUser() вернул пустой токен при успехе")
					}
				}
			},
		)
	}
}

func TestCreateOrder(t *testing.T) {
	const (
		validLuhn   = "79927398713"
		invalidLuhn = "1234567890"
		userID      = 100
		otherUserID = 200
	)

	tests := []struct {
		name          string
		uid           int64
		number        string
		prepareStore  func(s *FakeStore)
		wantErr       bool
		expectedError error
	}{
		{
			name:          "Успешное создание заказа",
			uid:           userID,
			number:        validLuhn,
			prepareStore:  func(s *FakeStore) {},
			wantErr:       false,
			expectedError: nil,
		},
		{
			name:          "Ошибка: неверный номер Луна",
			uid:           userID,
			number:        invalidLuhn,
			prepareStore:  func(s *FakeStore) {},
			wantErr:       true,
			expectedError: domain.ErrInvalidOrder,
		},
		{
			name:   "Заказ уже загружен этим же пользователем",
			uid:    userID,
			number: validLuhn,
			prepareStore: func(s *FakeStore) {
				_, _ = s.CreateOrder(userID, validLuhn)
			},
			wantErr:       true,
			expectedError: domain.ErrOrderOwnedByUser,
		},
		{
			name:   "Заказ уже загружен другим пользователем",
			uid:    userID,
			number: validLuhn,
			prepareStore: func(s *FakeStore) {
				_, _ = s.CreateOrder(otherUserID, validLuhn)
			},
			wantErr:       true,
			expectedError: domain.ErrOrderExists,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := NewFakeStore()
			tc.prepareStore(store)
			svc := New(store)

			_, err := svc.CreateOrder(tc.uid, tc.number)
			if tc.wantErr {
				if !errors.Is(err, tc.expectedError) {
					t.Errorf("ожидали error %v, получили %v", tc.expectedError, err)
				}
			} else if err != nil {
				t.Errorf("неожиданная error: %v", err)
			}
		})
	}
}

func TestWithdraw(t *testing.T) {
	const (
		validLuhn = "79927398713"
		userID    = 1
	)

	tests := []struct {
		name          string
		uid           int64
		order         string
		sum           float64
		prepareStore  func(s *FakeStore)
		wantErr       bool
		expectedError error
	}{
		{
			name:  "Успешное списание",
			uid:   userID,
			order: validLuhn,
			sum:   100,
			prepareStore: func(s *FakeStore) {
				_, _ = s.CreateUser("Ivan", "hash")
				_, _ = s.CreateOrder(userID, validLuhn)
				_ = s.UpdateOrderStatus(validLuhn, domain.OrderStatusProcessed, 500)
			},
			wantErr: false,
		},
		{
			name:  "Ошибка: недостаточно средств",
			uid:   userID,
			order: validLuhn,
			sum:   1000,
			prepareStore: func(s *FakeStore) {
				_, _ = s.CreateUser("Ivan", "hash")
				s.balances[userID].Current = 100
			},
			wantErr:       true,
			expectedError: domain.ErrInsufficientFunds,
		},
		{
			name:          "Ошибка: неверный номер заказа (Луна)",
			uid:           userID,
			order:         "123",
			sum:           10,
			prepareStore:  func(s *FakeStore) {},
			wantErr:       true,
			expectedError: domain.ErrInvalidOrder,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := NewFakeStore()
			tc.prepareStore(store)
			svc := New(store)

			err := svc.Withdraw(tc.uid, tc.order, tc.sum)
			if tc.wantErr {
				if !errors.Is(err, tc.expectedError) {
					t.Errorf("ожидали error %v, получили %v", tc.expectedError, err)
				}
			} else if err != nil {
				t.Errorf("неожиданная error: %v", err)
			}
		})
	}
}

func TestGetBalance(t *testing.T) {
	tests := []struct {
		name          string
		uid           int64
		prepareStore  func(s *FakeStore)
		expectedBal   domain.Balance
		wantErr       bool
		expectedError error
	}{
		{
			name: "Успешное получение баланса",
			uid:  1,
			prepareStore: func(s *FakeStore) {
				_, _ = s.CreateUser("user1", "hash")
				s.balances[1] = &domain.Balance{Current: 150.5, Withdrawn: 50.0}
			},
			expectedBal: domain.Balance{Current: 150.5, Withdrawn: 50.0},
			wantErr:     false,
		},
		{
			name:          "Пользователь не найден",
			uid:           999,
			prepareStore:  func(s *FakeStore) {},
			wantErr:       true,
			expectedError: domain.ErrUserNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := NewFakeStore()
			tc.prepareStore(store)
			svc := New(store)

			bal, err := svc.GetBalance(tc.uid)

			if tc.wantErr {
				if !errors.Is(err, tc.expectedError) {
					t.Errorf("ожидали error %v, получили %v", tc.expectedError, err)
				}
			} else {
				if err != nil {
					t.Errorf("неожиданная error: %v", err)
				}
				if bal != tc.expectedBal {
					t.Errorf("ожидали balance %v, получили %v", tc.expectedBal, bal)
				}
			}
		})
	}
}

func TestGetLists(t *testing.T) {
	const uid int64 = 1

	t.Run("GetUserOrders Table", func(t *testing.T) {
		tests := []struct {
			name         string
			prepareStore func(s *FakeStore)
			expectedLen  int
		}{
			{
				name: "Заказы есть",
				prepareStore: func(s *FakeStore) {
					_, _ = s.CreateOrder(uid, "79927398713")
					_, _ = s.CreateOrder(uid, "12345678903")
				},
				expectedLen: 2,
			},
			{
				name: "Заказов нет",
				prepareStore: func(s *FakeStore) {
				},
				expectedLen: 0,
			},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				store := NewFakeStore()
				tc.prepareStore(store)
				svc := New(store)
				res, _ := svc.GetUserOrders(uid)
				if len(res) != tc.expectedLen {
					t.Errorf("ожидали %d orders, получили %d", tc.expectedLen, len(res))
				}
			})
		}
	})

	t.Run("GetWithdrawals Table", func(t *testing.T) {
		tests := []struct {
			name         string
			prepareStore func(s *FakeStore)
			expectedLen  int
		}{
			{
				name: "Списания есть",
				prepareStore: func(s *FakeStore) {
					s.balances[uid] = &domain.Balance{Current: 1000}
					_ = s.Withdraw(uid, "79927398713", 100)
				},
				expectedLen: 1,
			},
			{
				name: "Списаний нет",
				prepareStore: func(s *FakeStore) {
				},
				expectedLen: 0,
			},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				store := NewFakeStore()
				tc.prepareStore(store)
				svc := New(store)
				res, _ := svc.GetWithdrawals(uid)
				if len(res) != tc.expectedLen {
					t.Errorf("ожидали %d withdrawals, получили %d", tc.expectedLen, len(res))
				}
			})
		}
	})
}

func TestGetSystemStats(t *testing.T) {
	store := NewFakeStore()
	svc := New(store)

	_, _ = store.CreateUser("user1", "hash")
	_, _ = store.CreateUser("user2", "hash")

	_, _ = store.CreateOrder(1, "79927398713")
	_, _ = store.CreateOrder(2, "12345678903")
	_ = store.UpdateOrderStatus("79927398713", domain.OrderStatusProcessed, 150.5)

	store.balances[1].Current = 100
	_ = store.Withdraw(1, "79927398713", 40.0)

	users, stats, accrual, withdrawn, err := svc.GetSystemStats()

	if err != nil {
		t.Fatalf("неожиданная error: %v", err)
	}
	if users != 2 {
		t.Errorf("ожидали 2 users, получили %d", users)
	}
	if stats[domain.OrderStatusProcessed] != 1 {
		t.Errorf("ожидали 1 processed order, получили %d", stats[domain.OrderStatusProcessed])
	}
	if accrual != 150.5 {
		t.Errorf("ожидали 150.5 accrual, получли %f", accrual)
	}
	if withdrawn != 40.0 {
		t.Errorf("ожидали 40.0 withdrawn, получили %f", withdrawn)
	}
}
