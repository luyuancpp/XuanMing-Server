package passwd

import (
	"testing"
)

func TestHashVerifyOK(t *testing.T) {
	h, err := Hash("client-digest", DevCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := Verify(h, "client-digest"); err != nil {
		t.Fatalf("verify ok: %v", err)
	}
}

func TestVerifyMismatch(t *testing.T) {
	h, err := Hash("abc", DevCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := Verify(h, "def"); err != ErrMismatch {
		t.Fatalf("want ErrMismatch, got %v", err)
	}
}

func TestVerifyBadHash(t *testing.T) {
	if err := Verify("not-a-bcrypt-hash", "abc"); err == nil {
		t.Fatalf("want err for bad hash, got nil")
	}
}

func TestHashCostClamp(t *testing.T) {
	// cost 越界时强制 DevCost,不应 panic / 报错
	if _, err := Hash("x", 1); err != nil {
		t.Fatalf("hash cost<min: %v", err)
	}
	if _, err := Hash("x", 99); err != nil {
		t.Fatalf("hash cost>max: %v", err)
	}
}
