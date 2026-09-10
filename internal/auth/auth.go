package auth

import (
	"context"
	"errors"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"net/mail"
	"strings"
	"time"
)

type Service struct {
	Store  *p.Store
	Secret []byte
}

func (s Service) Register(ctx context.Context, email, password string) (string, error) {
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || len(email) > 254 || len(password) < 10 || len(password) > 72 {
		return "", errors.New("valid email and 10..72 byte password required")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return s.Store.NewUser(ctx, email, string(h))
}
func (s Service) Login(ctx context.Context, email, password string) (string, error) {
	id, h, err := s.Store.Credentials(ctx, email)
	if err != nil {
		return "", errors.New("invalid credentials")
	}
	if err = bcrypt.CompareHashAndPassword([]byte(h), []byte(password)); err != nil {
		return "", errors.New("invalid credentials")
	}
	return s.Token(id)
}
func (s Service) Token(id string) (string, error) {
	if len(s.Secret) < 32 {
		return "", errors.New("JWT_SECRET requires 32+ bytes")
	}
	now := time.Now()
	return jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{Subject: id, Issuer: "campustrace", Audience: jwt.ClaimStrings{"campustrace"}, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(12 * time.Hour))}).SignedString(s.Secret)
}
func (s Service) Verify(token string) (string, error) {
	if len(s.Secret) < 32 {
		return "", errors.New("JWT not configured")
	}
	claims := &jwt.RegisteredClaims{}
	v, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) { return s.Secret, nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer("campustrace"), jwt.WithAudience("campustrace"), jwt.WithExpirationRequired())
	if err != nil || !v.Valid || strings.TrimSpace(claims.Subject) == "" {
		return "", errors.New("invalid token")
	}
	return claims.Subject, nil
}
