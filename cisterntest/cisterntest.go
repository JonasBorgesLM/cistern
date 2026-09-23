// Package cisterntest is the conformance suite for cistern.Store
// implementations (RF-14). A third-party store that passes RunStore can be
// used as either cache level; one that fails it will break cistern in the way
// the failing subtest names.
//
// The suite needs no clock control: its one timing test uses a TTL of
// 100 milliseconds and waits up to five seconds, which suits stores with
// millisecond resolution such as Redis.
package cisterntest

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/JonasBorgesLM/cistern"
)

// Keys shaped like the physical keys cistern builds (ADR-0008).
const (
	keyA = "cistern:v1:cisterntest:-:k:user:42:list"
	keyB = "cistern:v1:cisterntest:-:k:user:43:list"
)

// RunStore runs the Store conformance suite. newStore is called once per
// subtest and must return an empty store; it may register cleanup on t.
func RunStore(t *testing.T, newStore func(t *testing.T) cistern.Store) {
	t.Helper()
	for _, tc := range []struct {
		name string
		run  func(*testing.T, cistern.Store)
	}{
		{"MissingKeyIsAMiss", missingKeyIsAMiss},
		{"SetThenGet", setThenGet},
		{"SetReplaces", setReplaces},
		{"DeleteRemoves", deleteRemoves},
		{"KeysAreIndependent", keysAreIndependent},
		{"KeysOfAnyShape", keysOfAnyShape},
		{"ValuesAreBinarySafe", valuesAreBinarySafe},
		{"EntriesExpire", entriesExpire},
		{"NonPositiveTTLIsRejected", nonPositiveTTLIsRejected},
		{"SetDoesNotRetainTheCallersSlice", setDoesNotRetainTheCallersSlice},
		{"GetDoesNotExposeTheStoredBytes", getDoesNotExposeTheStoredBytes},
		{"CancelledContextIsAnError", cancelledContextIsAnError},
		{"ConcurrentUse", concurrentUse},
	} {
		t.Run(tc.name, func(t *testing.T) { tc.run(t, newStore(t)) })
	}
}

func set(t *testing.T, s cistern.Store, key string, value []byte, ttl time.Duration) {
	t.Helper()
	if err := s.Set(context.Background(), key, value, ttl); err != nil {
		t.Fatalf("Set(%q): %v", key, err)
	}
}

func wantValue(t *testing.T, s cistern.Store, key string, want []byte) {
	t.Helper()
	got, ok, err := s.Get(context.Background(), key)
	if err != nil || !ok || !bytes.Equal(got, want) {
		t.Fatalf("Get(%q) = %q, %v, %v; want %q, true, nil", key, got, ok, err, want)
	}
}

// wantMiss asserts that keyA is a miss.
func wantMiss(t *testing.T, s cistern.Store) {
	t.Helper()
	got, ok, err := s.Get(context.Background(), keyA)
	if err != nil || ok {
		t.Fatalf("Get(%q) = %q, %v, %v; want a miss: _, false, nil", keyA, got, ok, err)
	}
}

// A miss is (_, false, nil). An error means the store could not answer, and
// cistern would count every miss as an outage.
func missingKeyIsAMiss(t *testing.T, s cistern.Store) {
	wantMiss(t, s)
}

func setThenGet(t *testing.T, s cistern.Store) {
	set(t, s, keyA, []byte("value"), time.Minute)
	wantValue(t, s, keyA, []byte("value"))
}

func setReplaces(t *testing.T, s cistern.Store) {
	set(t, s, keyA, []byte("old"), time.Minute)
	set(t, s, keyA, []byte("new"), time.Minute)
	wantValue(t, s, keyA, []byte("new"))
}

// Delete is how cistern invalidates (ADR-0002); deleting what is not there is
// not an error, because invalidating twice is normal.
func deleteRemoves(t *testing.T, s cistern.Store) {
	set(t, s, keyA, []byte("value"), time.Minute)
	if err := s.Delete(context.Background(), keyA); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	wantMiss(t, s)
	if err := s.Delete(context.Background(), keyA); err != nil {
		t.Fatalf("Delete of a missing key: %v, want nil", err)
	}
}

func keysAreIndependent(t *testing.T, s cistern.Store) {
	set(t, s, keyA, []byte("a"), time.Minute)
	set(t, s, keyB, []byte("b"), time.Minute)
	if err := s.Delete(context.Background(), keyA); err != nil {
		t.Fatal(err)
	}
	wantMiss(t, s)
	wantValue(t, s, keyB, []byte("b"))
}

// Physical keys contain ':' and may contain any UTF-8 (ADR-0008), or a hex
// digest when hashed.
func keysOfAnyShape(t *testing.T, s cistern.Store) {
	for _, key := range []string{
		"cistern:v1:cisterntest:-:k:tarefa:ação:ünïcode",
		"cistern:v1:cisterntest:-:h:" + fmt.Sprintf("%064x", 7),
		"cistern:v1:cisterntest:g7.3:k:a key with spaces",
	} {
		set(t, s, key, []byte(key), time.Minute)
		wantValue(t, s, key, []byte(key))
	}
}

// Stored bytes are envelopes (ADR-0009): arbitrary binary, including NUL.
func valuesAreBinarySafe(t *testing.T, s cistern.Store) {
	value := make([]byte, 256)
	for i := range value {
		value[i] = byte(i)
	}
	set(t, s, keyA, value, time.Minute)
	wantValue(t, s, keyA, value)
}

// An expired entry must stop being returned; cistern also checks the
// envelope's own expiry, but a store that never expires grows without bound.
func entriesExpire(t *testing.T, s cistern.Store) {
	set(t, s, keyA, []byte("short-lived"), 100*time.Millisecond)
	wantValue(t, s, keyA, []byte("short-lived"))
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, ok, err := s.Get(context.Background(), keyA)
		if err != nil {
			t.Fatalf("Get while waiting for expiry: %v", err)
		}
		if !ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("an entry with a 100ms TTL was still returned after 5s")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func nonPositiveTTLIsRejected(t *testing.T, s cistern.Store) {
	for _, ttl := range []time.Duration{0, -time.Second} {
		if err := s.Set(context.Background(), keyA, []byte("v"), ttl); err == nil {
			t.Errorf("Set with ttl %v = nil error, want an error", ttl)
		}
	}
	wantMiss(t, s)
}

// ADR-0014: callers never share bytes with the store.
func setDoesNotRetainTheCallersSlice(t *testing.T, s cistern.Store) {
	value := []byte("original")
	set(t, s, keyA, value, time.Minute)
	copy(value, "XXXXXXXX")
	wantValue(t, s, keyA, []byte("original"))
}

func getDoesNotExposeTheStoredBytes(t *testing.T, s cistern.Store) {
	set(t, s, keyA, []byte("original"), time.Minute)
	got, _, err := s.Get(context.Background(), keyA)
	if err != nil {
		t.Fatal(err)
	}
	copy(got, "YYYYYYYY")
	wantValue(t, s, keyA, []byte("original"))
}

// RF-02: a done context is an error, and a Set under one stores nothing.
func cancelledContextIsAnError(t *testing.T, s cistern.Store) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := s.Get(ctx, keyA); err == nil {
		t.Error("Get with a cancelled context = nil error")
	}
	if err := s.Set(ctx, keyA, []byte("v"), time.Minute); err == nil {
		t.Error("Set with a cancelled context = nil error")
	}
	if err := s.Delete(ctx, keyA); err == nil {
		t.Error("Delete with a cancelled context = nil error")
	}
	wantMiss(t, s)
}

// RNF-05: safe for concurrent use. Every value is its own key, so a torn or
// crossed write shows up as a mismatch.
func concurrentUse(t *testing.T, s cistern.Store) {
	ctx := context.Background()
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 200 {
				key := fmt.Sprintf("cistern:v1:cisterntest:-:k:%d", (g*i)%32)
				if err := s.Set(ctx, key, []byte(key), time.Minute); err != nil {
					t.Errorf("Set: %v", err)
					return
				}
				got, ok, err := s.Get(ctx, key)
				if err != nil {
					t.Errorf("Get: %v", err)
					return
				}
				if ok && string(got) != key {
					t.Errorf("Get(%q) = %q", key, got)
					return
				}
				if i%5 == 0 {
					if err := s.Delete(ctx, key); err != nil {
						t.Errorf("Delete: %v", err)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
}
