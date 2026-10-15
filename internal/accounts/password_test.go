package accounts

import (
	"context"
	"errors"
	"testing"
)

func TestPasswordsRejectMalformedPersistentHash(t *testing.T) {
	p, err := NewPasswords(0)
	if err != nil {
		t.Fatal(err)
	}
	for _, hash := range []string{"broken", "$argon2id$v=19$m=999999999,t=3,p=2$MTIzNDU2Nzg5MDEyMzQ1Ng$MTIzNDU2Nzg5MDEyMzQ1Ng", "$argon2id$v=19$m=65536,t=0,p=0$MTIzNDU2Nzg5MDEyMzQ1Ng$MTIzNDU2Nzg5MDEyMzQ1Ng"} {
		if _, err = p.Check(context.Background(), "password", hash); !errors.Is(err, ErrHash) {
			t.Fatalf("expected malformed hash error, got %v", err)
		}
	}
}
func TestPasswordsBoundWorkAndCancellation(t *testing.T) {
	p, err := NewPasswords(0)
	if err != nil {
		t.Fatal(err)
	}
	p.slots <- struct{}{}
	p.slots <- struct{}{}
	if _, err = p.Hash(context.Background(), "a-long-password"); !errors.Is(err, ErrBusy) {
		t.Fatalf("expected busy, got %v", err)
	}
	<-p.slots
	<-p.slots
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = p.Hash(ctx, "a-long-password"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	hash, err := p.Hash(context.Background(), "a-long-password")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := p.Check(context.Background(), "a-long-password", hash)
	if err != nil || !ok {
		t.Fatalf("roundtrip: %v", err)
	}
	ok, err = p.Check(context.Background(), "wrong-password", hash)
	if err != nil || ok {
		t.Fatalf("wrong password: %v", err)
	}
}

func TestConfiguredPasswordMinimumAndExistingCredentials(t *testing.T) {
	ctx := context.Background()
	relaxed, err := NewPasswords(0)
	if err != nil {
		t.Fatal(err)
	}
	strict, err := NewPasswords(12)
	if err != nil {
		t.Fatal(err)
	}
	for _, password := range []string{"", "x", "é猫"} {
		hash, err := relaxed.Hash(ctx, password)
		if err != nil {
			t.Fatal(err)
		}
		if ok, err := strict.Check(ctx, password, hash); err != nil || !ok {
			t.Fatalf("policy change rejected existing credential: %v", err)
		}
		if _, err := strict.Hash(ctx, password); !errors.Is(err, ErrInvalid) {
			t.Fatalf("minimum not enforced: %v", err)
		}
	}
	unicodePolicy, err := NewPasswords(2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unicodePolicy.Hash(ctx, "猫"); !errors.Is(err, ErrInvalid) {
		t.Fatal("minimum counted bytes instead of characters")
	}
	if _, err := unicodePolicy.Hash(ctx, "猫é"); err != nil {
		t.Fatal(err)
	}
	if _, err := relaxed.Hash(ctx, string(make([]byte, 1025))); !errors.Is(err, ErrInvalid) {
		t.Fatal("maximum not enforced")
	}
	if _, err := NewPasswords(1024); err != nil {
		t.Fatal("dummy hash incorrectly subject to policy", err)
	}
}
