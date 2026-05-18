// Пакет service содержит бизнес-логику приложения.
//
// Взаимодействие с хранилищем осуществляется через интерфейс.
// Определите этот интерфейс здесь, по месту использования.
package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"gopherledger/internal/auth"
	"log"
	"math/rand"
	"strconv"
	"sync"
	"time"

	"gopherledger/internal/domain"

	"golang.org/x/sync/errgroup"
)

// Service реализует бизнес-логику приложения.
// Замените поле repo в структуре на свой интерфейс.
//
// processingOrders хранит номера заказов, которые сейчас обрабатываются воркером.
// Защитите конкурентный доступ к этому полю самостоятельно.
type repository interface {
	CreateUser(login, passwordHash string) (*domain.User, error)
	GetUserByLogin(login string) (*domain.User, error)
	CreateOrder(userID int64, number string) (*domain.Order, error)
	GetUserOrders(userID int64) ([]domain.Order, error)
	GetOrdersForProcessing() ([]domain.Order, error)
	UpdateOrderStatus(number, status string, accrual float64) error
	GetBalance(userID int64) (domain.Balance, error)
	Withdraw(userID int64, orderNumber string, sum float64) error
	GetWithdrawals(userID int64) ([]domain.Withdrawal, error)
	GetOverallStats() (int, map[string]int, float64, float64, error)
}
type Service struct {
	repo             repository
	processingOrders map[string]bool
	mu               sync.RWMutex
}

// New создаёт Service.
func New(repo repository) *Service {
	return &Service{
		repo:             repo,
		processingOrders: make(map[string]bool),
	}
}

func Hash(password string) string {
	hash := sha256.Sum256([]byte(password))
	passwordHash := hex.EncodeToString(hash[:])
	return passwordHash
}

// ---------------------------------------------------------------------------
// Методы бизнес-логики - реализуйте самостоятельно
// ---------------------------------------------------------------------------

// RegisterUser регистрирует нового пользователя и возвращает токен аутентификации.
// Хешируйте пароль перед сохранением с помощью crypto/sha256.
func (s *Service) RegisterUser(login, password string) (string, error) {
	passwordHash := Hash(password)
	user, err := s.repo.CreateUser(login, passwordHash)
	if err != nil {
		return "", err
	}

	token, tokenErr := auth.GenerateToken(user.ID)
	if tokenErr != nil {
		return "", fmt.Errorf("Ошибка генерации токена при аутентификации: %w", tokenErr)
	}

	return token, nil
}

// LoginUser проверяет учётные данные и возвращает токен аутентификации.
func (s *Service) LoginUser(login, password string) (string, error) {
	user, err := s.repo.GetUserByLogin(login)
	if err != nil {
		return "", err
	}
	if user == nil {
		return "", domain.ErrUserNotFound
	}
	passwordHash := Hash(password)
	if passwordHash != user.PasswordHash {
		return "", domain.ErrInvalidPassword
	}
	token, tokenErr := auth.GenerateToken(user.ID)
	if tokenErr != nil {
		return "", fmt.Errorf("Ошибка генерации токена при аутентификации: %w", err)
	}
	return token, nil
}

// CreateOrder проверяет номер заказа по алгоритму Луна и сохраняет заказ.
func (s *Service) CreateOrder(userID int64, number string) (*domain.Order, error) {
	if lun := validateLuhn(number); !lun {
		return nil, domain.ErrInvalidOrder
	}
	order, err := s.repo.CreateOrder(userID, number)
	if err != nil {
		return nil, err
	}
	return order, nil
}

// GetUserOrders возвращает все заказы пользователя.
func (s *Service) GetUserOrders(userID int64) ([]domain.Order, error) {
	orders, err := s.repo.GetUserOrders(userID)
	if err != nil {
		return nil, err
	}
	return orders, nil

}

// GetBalance возвращает текущий баланс пользователя.
func (s *Service) GetBalance(userID int64) (domain.Balance, error) {
	balance, err := s.repo.GetBalance(userID)
	if err != nil {
		return domain.Balance{}, err
	}
	return balance, nil
}

// Withdraw проверяет номер заказа по алгоритму Луна и списывает сумму с баланса.
func (s *Service) Withdraw(userID int64, orderNumber string, sum float64) error {
	if lun := validateLuhn(orderNumber); !lun {
		return domain.ErrInvalidOrder
	}
	err := s.repo.Withdraw(userID, orderNumber, sum)
	if err != nil {
		return err
	}
	return nil
}

// GetWithdrawals возвращает историю списаний пользователя.
func (s *Service) GetWithdrawals(userID int64) ([]domain.Withdrawal, error) {
	orders, err := s.repo.GetWithdrawals(userID)
	if err != nil {
		return nil, err
	}
	return orders, nil
}

// validateLuhn проверяет контрольную сумму номера заказа по алгоритму Луна.
// Вызывается при загрузке заказа и при списании баллов.
func validateLuhn(number string) bool {
	sum := 0
	doubleFlag := false

	for i := len(number) - 1; i >= 0; i-- {
		c := number[i]

		if c < '0' || c > '9' {
			continue
		}

		digit, err := strconv.Atoi(string(c))
		if err != nil {
			return false
		}

		if doubleFlag {
			digit *= 2
			if digit > 9 {
				digit -= 9
			}
		}
		sum += digit
		doubleFlag = !doubleFlag
	}
	return sum%10 == 0
}

func (s *Service) GetSystemStats() (int, map[string]int, float64, float64, error) {
	return s.repo.GetOverallStats()
}

// ---------------------------------------------------------------------------
// Воркер начислений
//
// StartAccrualWorker предоставлен. Реализуйте processAllPendingOrders
// и processOrder самостоятельно.
//
// Это самая интересная часть проекта: конкурентная обработка заказов.
// Подумайте, как защитить доступ к processingOrders из нескольких горутин.
// ---------------------------------------------------------------------------

// StartAccrualWorker запускает фоновый цикл, который каждые 3 секунды
// передаёт необработанные заказы в processAllPendingOrders.
// Останавливается при отмене ctx.
func (s *Service) StartAccrualWorker(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.processAllPendingOrders(ctx)
		}
	}
}

// processAllPendingOrders получает заказы для обработки и запускает горутины.
// Реализуйте самостоятельно.
func (s *Service) processAllPendingOrders(ctx context.Context) {
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(5)

	orders, err := s.repo.GetOrdersForProcessing()
	if err != nil {
		log.Printf("ошибка получения заказов: %v", err)
		return
	}
	for _, order := range orders {
		if gctx.Err() != nil {
			return
		}
		if order.Status == domain.OrderStatusProcessing {
			continue
		}
		s.mu.Lock()
		if s.processingOrders[order.Number] {
			s.mu.Unlock()
			continue
		}
		s.processingOrders[order.Number] = true
		s.mu.Unlock()
		number := order.Number
		g.Go(func() error {
			s.processOrder(gctx, number)
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		log.Printf("воркер: ошибка группы: %v", err)
	}
}

// processOrder обрабатывает один заказ. Реализуйте самостоятельно.
// Используйте вспомогательные функции ниже для генерации случайных значений.
func (s *Service) processOrder(ctx context.Context, number string) {
	defer func() {
		s.mu.Lock()
		delete(s.processingOrders, number)
		s.mu.Unlock()
	}()

	delay := randomDelay()
	select {
	case <-ctx.Done():
		return
	case <-time.After(delay):
	}
	if isInvalid() {
		err := s.repo.UpdateOrderStatus(number, domain.OrderStatusInvalid, 0)
		if err != nil {
			return
		}
		return
	}
	accrual := randomAccrual()
	err := s.repo.UpdateOrderStatus(number, domain.OrderStatusProcessed, accrual)
	if err != nil {
		return
	}
}

// ---------------------------------------------------------------------------
// Вспомогательные функции - предоставлены
// ---------------------------------------------------------------------------

// randomAccrual возвращает случайное начисление от 10 до 500 баллов.
func randomAccrual() float64 {
	return float64(rand.Intn(491) + 10)
}

// randomDelay возвращает случайную задержку от 2 до 6 секунд.
func randomDelay() time.Duration {
	return time.Duration(rand.Intn(5)+2) * time.Second
}

// isInvalid возвращает true примерно в 10% случаев.
func isInvalid() bool {
	return rand.Intn(10) == 0
}
