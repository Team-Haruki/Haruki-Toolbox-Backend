package bootstrap

import (
	"context"
	harukiRedis "haruki-suite/utils/database/redis"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

func TestOpenMainLogWriterStdout(t *testing.T) {
	writer, cleanup, err := openMainLogWriter("")
	if err != nil {
		t.Fatalf("openMainLogWriter returned error: %v", err)
	}
	if writer == nil {
		t.Fatalf("writer is nil")
	}
	if err := cleanup(); err != nil {
		t.Fatalf("cleanup returned error: %v", err)
	}
}

func TestOpenMainLogWriterFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.log")
	writer, cleanup, err := openMainLogWriter(path)
	if err != nil {
		t.Fatalf("openMainLogWriter returned error: %v", err)
	}
	defer func() {
		if closeErr := cleanup(); closeErr != nil {
			t.Fatalf("cleanup returned error: %v", closeErr)
		}
	}()

	if writer == nil {
		t.Fatalf("writer is nil")
	}
	if _, err := io.WriteString(writer, "hello\n"); err != nil {
		t.Fatalf("WriteString returned error: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(content) == "" {
		t.Fatalf("log file content is empty")
	}
}

func TestEnsureRedisReadyNilManager(t *testing.T) {
	if err := ensureRedisReady(context.Background(), nil); err == nil {
		t.Fatalf("ensureRedisReady should fail for nil manager")
	}
}

func TestEnsureRedisReadyPingFailure(t *testing.T) {
	t.Parallel()

	manager := &harukiRedis.HarukiRedisManager{
		Redis: goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"}),
	}
	defer func() {
		_ = manager.Redis.Close()
	}()

	err := ensureRedisReady(context.Background(), manager)
	if err == nil {
		t.Fatalf("ensureRedisReady should fail when ping fails")
	}
	if !strings.Contains(err.Error(), "redis ping failed") {
		t.Fatalf("error = %v, want redis ping failed", err)
	}
}

func TestEnsureRedisReadySuccess(t *testing.T) {
	t.Parallel()

	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error: %v", err)
	}
	defer srv.Close()

	manager := &harukiRedis.HarukiRedisManager{
		Redis: goredis.NewClient(&goredis.Options{Addr: srv.Addr()}),
	}
	defer func() {
		_ = manager.Redis.Close()
	}()

	if err := ensureRedisReady(context.Background(), manager); err != nil {
		t.Fatalf("ensureRedisReady returned error: %v", err)
	}
}
