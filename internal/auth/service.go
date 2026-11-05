// Package auth implements phone OTP sign-in, sessions and the signed-in account.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/big"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"javis_api/internal/auth/authdb"
	"javis_api/pkg/postgres"
)

const (
	codeTTL           = 5 * time.Minute
	resendAfter       = 45 * time.Second
	maxCodeAttempts   = 5
	maxSendsPerPhone  = 5
	maxSendsPerIP     = 20
	sendLimitWindow   = time.Hour
	smsTimeout        = 5 * time.Second
	sessionTTL        = 90 * 24 * time.Hour
	sessionTouchEvery = time.Hour
	maxNameLength     = 60
)

var (
	ErrInvalidPhone    = errors.New("invalid phone number")
	ErrInvalidRequest  = errors.New("invalid request")
	ErrInvalidCode     = errors.New("invalid code")
	ErrTooManyAttempts = errors.New("too many attempts")
	ErrSMSUnavailable  = errors.New("sms unavailable")
	ErrInvalidName     = errors.New("invalid name")
	ErrUnauthorized    = errors.New("unauthorized")
)

// RateLimitedError reports when another code request can succeed.
type RateLimitedError struct {
	RetryAfter time.Duration
}

func (e RateLimitedError) Error() string { return "rate limited" }

var (
	thaiMobile = regexp.MustCompile(`^\+66[689]\d{8}$`)
	sixDigits  = regexp.MustCompile(`^\d{6}$`)
)

// SMSSender delivers a code to a phone number. It must honor its context.
type SMSSender interface {
	Send(ctx context.Context, phone, code string) error
}

type User struct {
	ID    uuid.UUID
	Phone string
	Name  *string
}

// Challenge describes an issued code. DebugCode is set only in OTP debug mode.
type Challenge struct {
	ResendAfter time.Duration
	ExpiresIn   time.Duration
	DebugCode   *string
}

type Session struct {
	Token     string
	ExpiresAt time.Time
	User      User
}

// Principal identifies the caller of an authenticated request.
type Principal struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
}

type Options struct {
	// Pepper keys code hashes; six digits alone are trivially reversed.
	Pepper []byte
	Sender SMSSender
	// ExposeCode returns the code to the caller. Config allows it only in development.
	ExposeCode bool
}

type Service struct {
	pool       *pgxpool.Pool
	queries    *authdb.Queries
	pepper     []byte
	sender     SMSSender
	exposeCode bool
}

func NewService(pool *pgxpool.Pool, options Options) *Service {
	return &Service{
		pool:       pool,
		queries:    authdb.New(pool),
		pepper:     options.Pepper,
		sender:     options.Sender,
		exposeCode: options.ExposeCode,
	}
}

// RequestCode issues and sends a code. It behaves the same whether or not the
// number has an account, so it cannot reveal who is a customer.
func (s *Service) RequestCode(ctx context.Context, phone, clientIP string) (Challenge, error) {
	if !thaiMobile.MatchString(phone) {
		return Challenge{}, ErrInvalidPhone
	}
	code, err := newCode()
	if err != nil {
		return Challenge{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Challenge{}, err
	}
	err = postgres.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		queries := authdb.New(tx)
		// Parallel requests for one number must not slip past the limits below.
		if err := queries.LockPhone(ctx, phone); err != nil {
			return err
		}
		if err := checkSendLimits(ctx, queries, phone, clientIP); err != nil {
			return err
		}
		if err := queries.ConsumeOpenChallenges(ctx, phone); err != nil {
			return err
		}
		return queries.CreateChallenge(ctx, authdb.CreateChallengeParams{
			ID:         id,
			Phone:      phone,
			ClientIp:   clientIP,
			CodeHash:   s.hashCode(id, phone, code),
			TtlSeconds: codeTTL.Seconds(),
		})
	})
	if err != nil {
		return Challenge{}, err
	}

	sendCtx, cancel := context.WithTimeout(ctx, smsTimeout)
	defer cancel()
	if err := s.sender.Send(sendCtx, phone, code); err != nil {
		// A code nobody received must not count against the user's limits.
		_ = s.queries.DeleteChallenge(context.WithoutCancel(ctx), id)
		return Challenge{}, ErrSMSUnavailable
	}
	challenge := Challenge{ResendAfter: resendAfter, ExpiresIn: codeTTL}
	if s.exposeCode {
		challenge.DebugCode = &code
	}
	return challenge, nil
}

func checkSendLimits(ctx context.Context, queries *authdb.Queries, phone, clientIP string) error {
	sinceLast, err := queries.SecondsSinceLastChallenge(ctx, phone)
	if err != nil {
		return err
	}
	if remaining := resendAfter.Seconds() - sinceLast; remaining > 0 {
		return RateLimitedError{RetryAfter: time.Duration(math.Ceil(remaining)) * time.Second}
	}
	sendsToPhone, err := queries.CountRecentChallengesByPhone(ctx, phone)
	if err != nil {
		return err
	}
	sendsFromIP, err := queries.CountRecentChallengesByIP(ctx, clientIP)
	if err != nil {
		return err
	}
	if sendsToPhone >= maxSendsPerPhone || sendsFromIP >= maxSendsPerIP {
		return RateLimitedError{RetryAfter: sendLimitWindow}
	}
	return nil
}

// VerifyCode exchanges a correct code for a session. The first success for a
// number creates its account. Wrong attempts are committed, not rolled back.
func (s *Service) VerifyCode(ctx context.Context, phone, code string) (Session, error) {
	if !thaiMobile.MatchString(phone) || !sixDigits.MatchString(code) {
		return Session{}, ErrInvalidRequest
	}
	var session Session
	var outcome error
	err := postgres.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		queries := authdb.New(tx)
		challenge, err := queries.LockActiveChallenge(ctx, phone)
		if errors.Is(err, pgx.ErrNoRows) {
			outcome = ErrInvalidCode
			return nil
		}
		if err != nil {
			return err
		}
		if challenge.Attempts >= maxCodeAttempts {
			outcome = ErrTooManyAttempts
			return nil
		}
		if !hmac.Equal(challenge.CodeHash, s.hashCode(challenge.ID, phone, code)) {
			outcome = ErrInvalidCode
			return queries.RecordFailedAttempt(ctx, challenge.ID)
		}
		if err := queries.ConsumeChallenge(ctx, challenge.ID); err != nil {
			return err
		}
		session, err = createSession(ctx, queries, phone)
		return err
	})
	if err != nil {
		return Session{}, err
	}
	if outcome != nil {
		return Session{}, outcome
	}
	return session, nil
}

func createSession(ctx context.Context, queries *authdb.Queries, phone string) (Session, error) {
	userID, err := uuid.NewV7()
	if err != nil {
		return Session{}, err
	}
	sessionID, err := uuid.NewV7()
	if err != nil {
		return Session{}, err
	}
	user, err := queries.UpsertUserByPhone(ctx, authdb.UpsertUserByPhoneParams{ID: userID, Phone: phone})
	if err != nil {
		return Session{}, err
	}
	token, err := newToken()
	if err != nil {
		return Session{}, err
	}
	expiresAt, err := queries.CreateSession(ctx, authdb.CreateSessionParams{
		ID:         sessionID,
		UserID:     user.ID,
		TokenHash:  hashToken(token),
		TtlSeconds: sessionTTL.Seconds(),
	})
	if err != nil {
		return Session{}, err
	}
	return Session{Token: token, ExpiresAt: expiresAt, User: User(user)}, nil
}

// Authenticate resolves a bearer token. Anything but ErrUnauthorized means the
// check itself failed, which must not look like being signed out.
func (s *Service) Authenticate(ctx context.Context, token string) (Principal, error) {
	session, err := s.queries.GetActiveSession(ctx, hashToken(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrUnauthorized
	}
	if err != nil {
		return Principal{}, err
	}
	if session.IdleSeconds >= sessionTouchEvery.Seconds() {
		// A failed extension only shortens the session; the request is still valid.
		_ = s.queries.ExtendSession(ctx, authdb.ExtendSessionParams{ID: session.ID, TtlSeconds: sessionTTL.Seconds()})
	}
	return Principal{UserID: session.UserID, SessionID: session.ID}, nil
}

func (s *Service) Logout(ctx context.Context, principal Principal) error {
	return s.queries.RevokeSession(ctx, principal.SessionID)
}

func (s *Service) Me(ctx context.Context, principal Principal) (User, error) {
	user, err := s.queries.GetUser(ctx, principal.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUnauthorized
	}
	if err != nil {
		return User{}, err
	}
	return User(user), nil
}

func (s *Service) SetName(ctx context.Context, principal Principal, name string) (User, error) {
	name = strings.TrimSpace(name)
	if !validName(name) {
		return User{}, ErrInvalidName
	}
	user, err := s.queries.UpdateUserName(ctx, authdb.UpdateUserNameParams{ID: principal.UserID, Name: &name})
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUnauthorized
	}
	if err != nil {
		return User{}, err
	}
	return User(user), nil
}

func validName(name string) bool {
	length := utf8.RuneCountInString(name)
	if length == 0 || length > maxNameLength {
		return false
	}
	return !strings.ContainsFunc(name, unicode.IsControl)
}

// RunCleanup removes old challenges and dead sessions until ctx ends. Challenges
// hold phone numbers and IP addresses, so they are not kept longer than needed.
func (s *Service) RunCleanup(ctx context.Context, every time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		if err := s.purge(ctx); err != nil && ctx.Err() == nil {
			logger.ErrorContext(ctx, "auth cleanup failed", "error_type", fmt.Sprintf("%T", err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) purge(ctx context.Context) error {
	if err := s.queries.PurgeOldChallenges(ctx); err != nil {
		return err
	}
	return s.queries.PurgeDeadSessions(ctx)
}

func newCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func newToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// hashCode binds the code to its challenge and number, so the same digits
// never produce the same hash twice.
func (s *Service) hashCode(id uuid.UUID, phone, code string) []byte {
	mac := hmac.New(sha256.New, s.pepper)
	mac.Write(id[:])
	mac.Write([]byte(phone))
	mac.Write([]byte(code))
	return mac.Sum(nil)
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
