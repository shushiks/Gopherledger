// Пакет store реализует хранилище данных в памяти.
// Используйте отдельные мьютексы для независимых групп данных.
// Реализуйте этот пакет самостоятельно.
package store

import (
	"gopherledger/internal/domain"
	"sort"
	"sync"
	"time"
)

// Store хранит все данные приложения в памяти.
// Добавьте средства защиты конкурентного доступа самостоятельно.
type Store struct {
	mu sync.RWMutex
	// users хранит пользователей по их ID
	users map[int64]*domain.User

	// usersByLogin хранит пользователей по логину - для быстрого поиска при авторизации
	usersByLogin map[string]*domain.User

	// orders хранит заказы по номеру заказа
	orders map[string]*domain.Order

	// balances хранит текущий баланс каждого пользователя по его ID
	balances map[int64]*domain.Balance

	// withdrawals хранит историю списаний для каждого пользователя по его ID
	withdrawals map[int64][]*domain.Withdrawal

	// nextID используется для генерации уникальных числовых ID
	nextID int64
}

// New создаёт и возвращает новое пустое хранилище.
func New() *Store {
	return &Store{
		users:        make(map[int64]*domain.User),
		usersByLogin: make(map[string]*domain.User),
		orders:       make(map[string]*domain.Order),
		balances:     make(map[int64]*domain.Balance),
		withdrawals:  make(map[int64][]*domain.Withdrawal),
		nextID:       1,
	}
}

// CreateUser добавляет нового пользователя.
// Возвращает domain.ErrUserExists если логин уже занят.
func (s *Store) CreateUser(login, passwordHash string) (*domain.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.usersByLogin[login]; exists {
		return nil, domain.ErrUserExists
	}
	user := &domain.User{
		ID:           s.nextID,
		Login:        login,
		PasswordHash: passwordHash,
	}
	s.users[user.ID] = user
	s.usersByLogin[user.Login] = user
	s.balances[user.ID] = &domain.Balance{Current: 0, Withdrawn: 0}
	s.nextID += 1
	return user, nil
}

// GetUserByLogin возвращает пользователя по логину.
// Возвращает domain.ErrUserNotFound если пользователь не найден.
func (s *Store) GetUserByLogin(login string) (*domain.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if user, exists := s.usersByLogin[login]; exists {
		return user, nil
	}
	return nil, domain.ErrUserNotFound
}

// CreateOrder добавляет новый заказ для пользователя.
// Возвращает domain.ErrOrderOwnedByUser если этот пользователь уже загружал этот номер.
// Возвращает domain.ErrOrderExists если номер принадлежит другому пользователю.
func (s *Store) CreateOrder(userID int64, number string) (*domain.Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if order, exists := s.orders[number]; exists {
		if order.UserID == userID {
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
	s.orders[order.Number] = order
	s.nextID += 1
	return order, nil
}

// GetUserOrders возвращает все заказы пользователя, сначала новые.
func (s *Store) GetUserOrders(userID int64) ([]domain.Order, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	orders := make([]domain.Order, 0, len(s.orders))
	for _, order := range s.orders {
		if order.UserID == userID {
			orders = append(orders, *order)
		}
	}
	sort.Slice(orders, func(i, j int) bool {
		return orders[i].UploadedAt.After(orders[j].UploadedAt)
	})
	return orders, nil
}

// GetOrdersForProcessing возвращает все заказы в статусе NEW или PROCESSING.
func (s *Store) GetOrdersForProcessing() ([]domain.Order, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	orders := make([]domain.Order, 0, len(s.orders))
	for _, order := range s.orders {
		if order.Status == domain.OrderStatusProcessing || order.Status == domain.OrderStatusNew {
			orders = append(orders, *order)
		}
	}
	return orders, nil
}

// UpdateOrderStatus обновляет статус и начисление заказа.
// Если статус PROCESSED и accrual > 0, баланс пользователя пополняется.
func (s *Store) UpdateOrderStatus(number, status string, accrual float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	order, exists := s.orders[number]
	if !exists {
		return nil
	}
	order.Status = status
	order.Accrual = accrual

	if status == domain.OrderStatusProcessed && accrual > 0 {
		if balance, exists := s.balances[order.UserID]; exists {
			balance.Current += accrual
		}
	}

	return nil
}

// GetBalance возвращает баланс пользователя.
func (s *Store) GetBalance(userID int64) (domain.Balance, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if balance, exists := s.balances[userID]; exists {
		return *balance, nil
	}
	return domain.Balance{}, domain.ErrUserNotFound
}

// Withdraw списывает сумму с баланса и записывает операцию.
// Возвращает domain.ErrInsufficientFunds если баланса не хватает.
// Обе операции должны быть атомарны: либо обе успешны, либо ни одна.
func (s *Store) Withdraw(userID int64, orderNumber string, sum float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	order, exists := s.orders[orderNumber]
	if !exists {
		return domain.ErrInvalidOrder
	}
	if order.UserID != userID {
		return domain.ErrInvalidOrder
	}
	balance, exists := s.balances[userID]
	if !exists {
		return domain.ErrUserNotFound
	}
	if balance.Current < sum {
		return domain.ErrInsufficientFunds
	}
	balance.Current -= sum
	balance.Withdrawn += sum
	withdrawal := &domain.Withdrawal{
		ID:          s.nextID,
		UserID:      userID,
		OrderNumber: orderNumber,
		Sum:         sum,
		ProcessedAt: time.Now(),
	}
	s.withdrawals[userID] = append(s.withdrawals[userID], withdrawal)
	s.nextID += 1
	return nil
}

// GetWithdrawals возвращает историю списаний пользователя, сначала новые.
func (s *Store) GetWithdrawals(userID int64) ([]domain.Withdrawal, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := s.withdrawals[userID]
	withdrawals := make([]domain.Withdrawal, 0, len(list))
	for i := len(list) - 1; i >= 0; i-- {
		withdrawals = append(withdrawals, *list[i])
	}
	return withdrawals, nil
}

func (s *Store) GetOverallStats() (int, map[string]int, float64, float64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	userCount := len(s.users)

	statusCounts := make(map[string]int)
	var totalAccrual float64
	for _, order := range s.orders {
		statusCounts[order.Status]++
		totalAccrual += order.Accrual
	}

	var totalWithdrawn float64
	for _, userWithdrawals := range s.withdrawals {
		for _, w := range userWithdrawals {
			totalWithdrawn += w.Sum
		}
	}

	return userCount, statusCounts, totalAccrual, totalWithdrawn, nil
}
