package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	Login string `json:"login"`
	jwt.RegisteredClaims
}

type JWTService struct {
	secretKey   []byte
	tokenExpiry time.Duration
}

func NewJWTService(secretKey string, expiryHours int) *JWTService {
	return &JWTService{
		secretKey:   []byte(secretKey),
		tokenExpiry: time.Duration(expiryHours) * time.Hour,
	}
}

func (j *JWTService) GenerateToken(login string) (string, error) {
	claims := Claims{
		Login: login,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(j.tokenExpiry)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(j.secretKey)
}

func (j *JWTService) ValidateToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return j.secretKey, nil
	})

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}

	return nil, errors.New("invalid token")
}

func ExtractTokenFromHeader(authHeader string) (string, error) {
	if authHeader == "" {
		return "", errors.New("authorization header is required")
	}

	var token string
	_, err := fmt.Sscanf(authHeader, "Bearer %s", &token)
	if err != nil {
		return "", errors.New("invalid authorization header format, expected: Bearer <token>")
	}

	return token, nil
}
