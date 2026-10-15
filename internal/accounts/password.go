package accounts

import (
	"context"
	"errors"
	"unicode/utf8"

	"github.com/alexedwards/argon2id"
)

var ErrBusy = errors.New("password processing is busy")
var ErrInvalid = errors.New("invalid account input")
var ErrHash = errors.New("stored password hash is malformed or exceeds supported limits")

type Passwords struct {
	slots   chan struct{}
	dummy   string
	minimum int
}

func NewPasswords(minimum int) (*Passwords, error) {
	if minimum < 0 || minimum > 1024 {
		return nil, ErrInvalid
	}
	p := &Passwords{slots: make(chan struct{}, 2), minimum: minimum}
	hash, err := p.hash(context.Background(), "not-an-account-password")
	p.dummy = hash
	return p, err
}

func (p *Passwords) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case p.slots <- struct{}{}:
		return nil
	default:
		return ErrBusy
	}
}
func (p *Passwords) Hash(ctx context.Context, password string) (string, error) {
	if !utf8.ValidString(password) || utf8.RuneCountInString(password) < p.minimum || len(password) > 1024 {
		return "", ErrInvalid
	}
	return p.hash(ctx, password)
}
func (p *Passwords) hash(ctx context.Context, password string) (string, error) {
	if err := p.acquire(ctx); err != nil {
		return "", err
	}
	defer func() { <-p.slots }()
	hash, err := argon2id.CreateHash(password, &argon2id.Params{Memory: 64 * 1024, Iterations: 3, Parallelism: 2, SaltLength: 16, KeyLength: 32})
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return hash, err
}
func (p *Passwords) Check(ctx context.Context, password, hash string) (bool, error) {
	if len(password) > 1024 {
		return false, ErrInvalid
	}
	if len(hash) > 512 {
		return false, ErrHash
	}
	params, _, _, err := argon2id.DecodeHash(hash)
	if err != nil || params.Memory < 8*uint32(params.Parallelism) || params.Memory > 128*1024 || params.Iterations < 1 || params.Iterations > 6 || params.Parallelism < 1 || params.Parallelism > 8 || params.SaltLength < 16 || params.SaltLength > 64 || params.KeyLength < 16 || params.KeyLength > 64 {
		return false, ErrHash
	}
	if err := p.acquire(ctx); err != nil {
		return false, err
	}
	defer func() { <-p.slots }()
	ok, err := argon2id.ComparePasswordAndHash(password, hash)
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	return ok, err
}
