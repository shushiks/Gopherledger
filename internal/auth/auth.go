// Пакет auth отвечает за генерацию и проверку токенов аутентификации.
// Токен - это случайная уникальная строка (например, UUID или hex-строка),
// которая однозначно связана с конкретным пользователем.
//
// Внутри пакета нужно хранить соответствие токен -> userID.
// Используйте для этого map с защитой от конкурентного доступа.
// Реализуйте этот пакет самостоятельно.
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
)

// ErrInvalidToken возвращается, если токен не найден или недействителен.
var ErrInvalidToken = errors.New("недействительный токен")

var (
	mu     sync.RWMutex
	tokens = make(map[string]int64)
)

// GenerateToken создаёт новый токен для пользователя с указанным ID
// и сохраняет связь токен -> userID внутри пакета.
func GenerateToken(userID int64) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)

	mu.Lock()
	defer mu.Unlock()
	tokens[token] = userID

	return token, nil
}

// ValidateToken проверяет токен и возвращает ID пользователя.
// Возвращает ErrInvalidToken если токен не найден.
func ValidateToken(token string) (int64, error) {
	mu.RLock()
	defer mu.RUnlock()
	UserID, ok := tokens[token]
	if !ok {
		return 0, ErrInvalidToken
	}
	return UserID, nil
}
